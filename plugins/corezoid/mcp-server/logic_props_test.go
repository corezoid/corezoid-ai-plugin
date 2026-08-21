package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// resetSchemaCache puts the package back into the state a freshly started server
// is in. Tests that care about cold-cache behaviour cannot rely on running
// first: Go executes a package's tests in one process, so any earlier test that
// touches the schema warms the cache and hides the very bug being pinned.
func resetSchemaCache(t *testing.T) {
	t.Helper()
	restore := func() {
		compiledSchemaOnce = sync.Once{}
		compiledSchema = nil
		compiledSchemaErr = nil
		knownLogicProps = map[string]map[string]bool{}
		logicSchemaPathByType = map[string]string{}
	}
	restore()
	t.Cleanup(func() {
		restore()
		// Leave the cache warm for whatever runs next, so a reset here cannot
		// change another test's outcome.
		if _, err := loadCompiledSchema(); err != nil {
			t.Fatalf("rewarm schema cache: %v", err)
		}
	})
}

// findUnknownLogicProps used to read knownLogicProps directly, and the only thing
// that populates it is loadCompiledSchema — called just after, via
// ValidateJSONSchema. So on the first lint-process of a session the map was
// empty, every logic missed, and the advisory was a silent no-op. Since the
// relaxation makes this advisory the only remaining detection for undeclared
// properties, "silently finds nothing" was the whole check being off.
func TestFindUnknownLogicProps_ColdCacheStillDetects(t *testing.T) {
	resetSchemaCache(t)
	if len(knownLogicProps) != 0 {
		t.Fatalf("precondition: cache must start cold, got %d entries", len(knownLogicProps))
	}

	nodes := []processNode{{
		id: "n1", title: "Setter", objType: 0,
		logics: []map[string]interface{}{
			{"type": "set_param", "extra": map[string]interface{}{"a": "1"}, "err_nod_id": "oops"},
		},
	}}
	got := findUnknownLogicProps(nodes)
	if len(got) != 1 {
		t.Fatalf("cold cache must still detect the typo, got %d advisories", len(got))
	}
	if !strings.Contains(got[0].Issue, "err_nod_id") {
		t.Errorf("advisory must name the property, got: %s", got[0].Issue)
	}
}

// A logic type whose name differs from its schema's filename must still be
// checked. api_git.json declares both `api_git` and `git_call`; keying the map by
// filename left git_call with no entry, so after the relaxation its undeclared
// properties were neither rejected by the schema nor reported by lint — a total
// gap for the node type the corezoid-gitcall skill emits.
func TestFindUnknownLogicProps_CoversTypeAliases(t *testing.T) {
	resetSchemaCache(t)
	nodes := []processNode{{
		id: "n1", title: "Custom code", objType: 0,
		logics: []map[string]interface{}{
			{"type": "git_call", "src": "x", "lang": "python", "err_node_id": "e", "totally_bogus": 1},
		},
	}}
	got := findUnknownLogicProps(nodes)
	if len(got) != 1 {
		t.Fatalf("git_call must be checked like any other logic, got %d advisories", len(got))
	}
	if !strings.Contains(got[0].Issue, "totally_bogus") {
		t.Errorf("advisory must name the property, got: %s", got[0].Issue)
	}
}

// The alias above was found by reading api_git.json. This closes the class
// instead of the instance: every type any logics schema pins must be a key, so a
// type added to an enum later cannot silently fall out of the check.
func TestKnownLogicProps_CoversEveryDeclaredType(t *testing.T) {
	if _, err := loadCompiledSchema(); err != nil {
		t.Fatalf("loadCompiledSchema: %v", err)
	}
	for _, d := range schemaDefinitions {
		if !strings.HasPrefix(d.path, "json-schema/logics/") {
			continue
		}
		data, err := schemaFS.ReadFile(d.path)
		if err != nil {
			t.Fatalf("read %s: %v", d.path, err)
		}
		var sch struct {
			Properties struct {
				Type struct {
					Const *string  `json:"const"`
					Enum  []string `json:"enum"`
				} `json:"type"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(data, &sch); err != nil {
			t.Fatalf("parse %s: %v", d.path, err)
		}
		declared := sch.Properties.Type.Enum
		if sch.Properties.Type.Const != nil {
			declared = append(declared, *sch.Properties.Type.Const)
		}
		for _, typeName := range declared {
			if _, ok := knownLogicProps[typeName]; !ok {
				t.Errorf("%s declares type %q, which has no knownLogicProps entry — "+
					"its undeclared properties are neither rejected nor reported", d.path, typeName)
			}
			if _, ok := logicSchemaPathByType[typeName]; !ok {
				t.Errorf("%s declares type %q with no logicSchemaPathByType entry", d.path, typeName)
			}
		}
	}
}

// knownLogicProps is written inside compiledSchemaOnce and read by lint. Reading
// it without going through the Once both misses a cold cache and races the
// writer — and a concurrent map read/write is a fatal, unrecoverable Go runtime
// error, so in HTTP transport mode (one goroutine per request) a lint-process
// racing the session's first push-process would take the whole server down.
// Meaningful under -race, which CI runs.
func TestLogicProps_ConcurrentReadersAndWriter(t *testing.T) {
	resetSchemaCache(t)

	nodes := []processNode{{
		id: "n1", title: "Code", objType: 0,
		logics: []map[string]interface{}{
			{"type": "api_code", "lang": "js", "src": "x", "err_node_id": "e", "surprise": 1},
		},
	}}

	const goroutines = 24
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				// The writer path: what push-process reaches via ValidateJSONSchema.
				if _, err := loadCompiledSchema(); err != nil {
					t.Errorf("loadCompiledSchema: %v", err)
				}
				return
			}
			if got := findUnknownLogicProps(nodes); len(got) != 1 {
				t.Errorf("reader saw %d advisories, want 1", len(got))
			}
		}(i)
	}
	wg.Wait()
}
