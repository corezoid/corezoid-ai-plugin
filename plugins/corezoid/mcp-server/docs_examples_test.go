package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var fencedJSON = regexp.MustCompile("(?s)```jsonc?\n(.*?)```")

// TestDocExamplesMatchSchema validates every JSON example in
// docs/node-structures.md against the same logic schemas lint-process uses.
//
// Why this exists: the doc is what an agent copies verbatim, so a doc example
// that contradicts the schema is a guaranteed failed lint round for whoever
// trusts it. The api_copy example shipped without the schema-required `group`
// and produced exactly that ("missing property 'group'") — the class of defect
// is "two sources of truth drift", so it is checked mechanically rather than by
// reading.
func TestDocExamplesMatchSchema(t *testing.T) {
	path := filepath.Join("..", "docs", "node-structures.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		// Not a skip: the doc ships in this repo at a fixed relative path, and a
		// silent skip is exactly how a drift check stops checking anything.
		t.Fatalf("doc not readable at %s: %v", path, err)
	}
	if _, err := loadCompiledSchema(); err != nil {
		t.Fatalf("loadCompiledSchema: %v", err)
	}

	matches := fencedJSON.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatal("no fenced JSON blocks found — did the doc format change?")
	}

	checkedLogics := 0
	for i, m := range matches {
		block := m[1]
		// Blocks marked as counter-examples demonstrate what NOT to write.
		if strings.Contains(block, "✗") || strings.Contains(strings.ToLower(block), "wrong —") {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(block), &doc); err != nil {
			continue // illustrative fragment, not a complete object
		}
		cond, _ := doc["condition"].(map[string]any)
		if cond == nil {
			continue
		}
		logics, _ := cond["logics"].([]any)
		for _, lgRaw := range logics {
			lg, _ := lgRaw.(map[string]any)
			logicType, _ := lg["type"].(string)
			if logicType == "" || logicType == "go" {
				continue
			}
			known, ok := knownLogicProps[logicType]
			if !ok {
				continue // no schema for this logic type
			}
			checkedLogics++
			// Required properties, read straight from the shipped schema file.
			for _, req := range requiredPropsFor(t, logicType) {
				if _, present := lg[req]; !present {
					t.Errorf("doc block #%d: %s example is missing schema-required property %q "+
						"— copying this example verbatim fails lint", i+1, logicType, req)
				}
			}
			// And nothing undeclared, which would be a typo in the doc.
			for name := range lg {
				if !known[name] {
					t.Errorf("doc block #%d: %s example carries %q, which the schema does not declare",
						i+1, logicType, name)
				}
			}
		}
	}
	if checkedLogics == 0 {
		t.Fatal("validated 0 logic examples — the extraction stopped matching, so this test " +
			"would silently pass forever")
	}
	t.Logf("validated %d logic examples from the doc", checkedLogics)
}

// requiredPropsFor reads the "required" list out of the embedded schema for a
// logic type, so the test cannot drift from the schema it is checking against.
func requiredPropsFor(t *testing.T, logicType string) []string {
	t.Helper()
	data, err := schemaFS.ReadFile("json-schema/logics/" + logicType + ".json")
	if err != nil {
		return nil
	}
	var sch struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(data, &sch); err != nil {
		t.Fatalf("parse schema for %s: %v", logicType, err)
	}
	return sch.Required
}
