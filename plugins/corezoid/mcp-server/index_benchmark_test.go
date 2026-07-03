package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sla200SoftBudget is the target ceiling from the TZ (§8 / criterion #19):
// full rebuild on a 200-process synthetic hub-topology project must finish
// in ≤ 500 ms on typical developer hardware. This is a soft budget — CI
// runners vary widely in speed, so a hard failure on CI would produce
// flakes. Instead the SLA test warns loudly (t.Log) when the budget is
// exceeded, and the CI job's job-level timeout is the ultimate cap. Set
// COREZOID_SLA_STRICT=1 in the environment to promote the warning to a
// hard failure — useful locally on a known-good machine.
const sla200SoftBudget = 500 * time.Millisecond

// synthesise200ProcessHubProject generates a project tree with 200 processes
// arranged in a hub topology: 5 hub processes referenced by 195 caller
// processes via mixed api_rpc / api_copy / api_get_task with aliases. This
// matches the shape of real-world Corezoid projects where a shared
// error-handling / logging / lookup process is referenced from many places —
// the topology that broke the original findCycles implementation before the
// globalDone optimisation (see index_graph.go).
//
// Written to t.TempDir(); no committed bytes needed. Fast to generate
// (~10ms) so it can be regenerated per-test without blowing the run time.
func synthesise200ProcessHubProject(t testing.TB, root string) {
	t.Helper()

	// 5 hubs, ids 1..5. Each hub is a minimal 2-node process (Start → End)
	// so parsing is cheap but structure is realistic.
	for i := 1; i <= 5; i++ {
		writeSyntheticProcess(t, root, i, fmt.Sprintf("hub-%d", i), nil)
	}
	// Aliases file in the real (array) shape — hub-1..hub-5 point to id 1..5.
	var aliasEntries []string
	for i := 1; i <= 5; i++ {
		aliasEntries = append(aliasEntries,
			fmt.Sprintf(`{"short_name":"hub-%d","obj_to_id":%d,"obj_to_type":"conv","title":"hub-%d","obj_id":%d}`,
				i, i, i, 900000+i))
	}
	aliasJSON := "[" + strings.Join(aliasEntries, ",") + "]"
	if err := os.WriteFile(filepath.Join(root, "_ALIASES_.json"), []byte(aliasJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// A modest _ENV_VARS_.json (10 vars, half referenced later) so the
	// env-var scanner has real work to do.
	envVarsEntries := []string{}
	for i := 1; i <= 10; i++ {
		envVarsEntries = append(envVarsEntries,
			fmt.Sprintf(`"VAR_%d": {"description": "test var %d"}`, i, i))
	}
	envJSON := "{" + strings.Join(envVarsEntries, ",") + "}"
	if err := os.WriteFile(filepath.Join(root, "_ENV_VARS_.json"), []byte(envJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// 195 caller processes, ids 100..294. Each has a Start node plus 3
	// cross-process nodes: api_rpc to a random hub via @alias, api_copy to
	// hub-1 (state-store simulation), api_get_task to another hub. Also
	// includes an api node with an env_var reference so the env_var scanner
	// exercises the whole logic tree.
	for i := 0; i < 195; i++ {
		id := 100 + i
		hubA := (i % 5) + 1                    // via alias
		hubB := ((i + 2) % 5) + 1              // direct numeric
		envVar := fmt.Sprintf("VAR_%d", (i%10)+1)
		writeSyntheticProcess(t, root, id, fmt.Sprintf("caller-%d", id),
			[]syntheticNode{
				{ID: "a" + padID(id, 1), Type: "api",
					Logic: fmt.Sprintf(`{"type":"api","url":"{{env_var[@%s]}}/x","method":"GET"}`, envVar)},
				{ID: "a" + padID(id, 2), Type: "api_rpc",
					Logic: fmt.Sprintf(`{"type":"api_rpc","conv_id":"@hub-%d"}`, hubA)},
				{ID: "a" + padID(id, 3), Type: "api_copy",
					Logic: fmt.Sprintf(`{"type":"api_copy","conv_id":1,"mode":"create"}`)},
				{ID: "a" + padID(id, 4), Type: "api_get_task",
					Logic: fmt.Sprintf(`{"type":"api_get_task","conv_id":%d}`, hubB)},
			})
	}
}

type syntheticNode struct {
	ID    string
	Type  string
	Logic string
}

func writeSyntheticProcess(t testing.TB, root string, id int, title string, extraNodes []syntheticNode) {
	t.Helper()
	startID := "s" + padID(id, 0)
	endID := "e" + padID(id, 0)

	// Assemble node JSON blobs — Start goes to first extra node (if any) or
	// End; each extra node also goes to End for a simple linear flow.
	firstNext := endID
	if len(extraNodes) > 0 {
		firstNext = extraNodes[0].ID
	}
	nodeBlobs := []string{
		fmt.Sprintf(`{"id":"%s","title":"Start","obj_type":1,"condition":{"logics":[{"type":"go","to_node_id":"%s"}]}}`,
			startID, firstNext),
	}
	for i, n := range extraNodes {
		nextID := endID
		if i+1 < len(extraNodes) {
			nextID = extraNodes[i+1].ID
		}
		// Extra node carries the specified logic, then a go to next.
		nodeBlobs = append(nodeBlobs, fmt.Sprintf(
			`{"id":"%s","title":"%s-%d","obj_type":0,"condition":{"logics":[%s,{"type":"go","to_node_id":"%s"}]}}`,
			n.ID, n.Type, i, n.Logic, nextID))
	}
	nodeBlobs = append(nodeBlobs, fmt.Sprintf(
		`{"id":"%s","title":"End","obj_type":2,"condition":{"logics":[]}}`, endID))

	body := fmt.Sprintf(`{
	  "obj_type":1,"obj_id":%d,"title":%q,"conv_type":"process","status":"active",
	  "scheme":{"nodes":[%s]}
	}`, id, title, strings.Join(nodeBlobs, ","))

	path := filepath.Join(root, fmt.Sprintf("%d_%s.conv.json", id, sanitise(title)))
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// padID returns a 24-char-safe hex-looking suffix for a numeric process id
// and a sub-index. Corezoid node IDs are 24 hex chars; the synthesised IDs
// aren't real hashes but the length is right so any downstream validation
// that expects 24 chars keeps working.
func padID(processID, subIdx int) string {
	base := fmt.Sprintf("%d%d", processID, subIdx)
	return strings.Repeat("0", 24-len(base)-1) + base
}

func sanitise(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
	return out
}

// TestIndex_SLA_200Processes materialises the 200-process hub-topology
// project and asserts that a full BuildProjectIndex + WriteProjectMap +
// WriteQueriesMD + UpdateClaudeMD sequence completes within the soft budget
// (see sla200SoftBudget). A budget miss is logged loudly. Set
// COREZOID_SLA_STRICT=1 to fail hard instead of logging.
//
// This is TZ criterion #19: the SLA must be verified by test, not by hand
// timing. The topology deliberately mirrors real-world hub graphs — the
// worst case for the cycle detector and edge resolver — so a passing budget
// here is stronger evidence than 200 unrelated processes would be.
func TestIndex_SLA_200Processes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SLA test in -short mode")
	}
	root := t.TempDir()
	synthesise200ProcessHubProject(t, root)

	start := time.Now()
	pm, warnings, err := BuildProjectIndex(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteProjectMap(root, pm); err != nil {
		t.Fatal(err)
	}
	if err := WriteQueriesMD(root); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateClaudeMD(root, pm); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	// Sanity-check the fixture actually produced the expected graph — a
	// bug here (e.g. all edges going to unresolved_targets) would make the
	// tool artificially fast and mask a real perf regression.
	if pm.ProcessCount != 200 {
		t.Fatalf("ProcessCount = %d, want 200 (fixture generation broken?)", pm.ProcessCount)
	}
	if len(pm.Edges) < 195*3 { // each caller emits 3 cross-process edges
		t.Fatalf("edges = %d, want ≥ %d — alias resolution or edge collection is broken",
			len(pm.Edges), 195*3)
	}
	if len(warnings) > 0 {
		t.Logf("build warnings: %v", warnings)
	}

	t.Logf("BuildProjectIndex+writers on 200-process hub topology: %v (budget %v)",
		elapsed, sla200SoftBudget)

	if elapsed > sla200SoftBudget {
		msg := fmt.Sprintf("SLA violation: %v > budget %v on 200-process hub project",
			elapsed, sla200SoftBudget)
		if os.Getenv("COREZOID_SLA_STRICT") == "1" {
			t.Fatal(msg)
		} else {
			t.Log("WARN: " + msg + " — soft budget, set COREZOID_SLA_STRICT=1 to fail hard")
		}
	}
}

// BenchmarkBuildProjectIndex measures the same 200-process build for use
// with `go test -bench . -benchmem`. Complements the pass/fail SLA test
// above with continuous measurement — useful when comparing branches, and
// the natural home for the "hub topology" performance concern from review
// point 3.
func BenchmarkBuildProjectIndex(b *testing.B) {
	root := b.TempDir()
	synthesise200ProcessHubProject(b, root)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pm, _, err := BuildProjectIndex(context.Background(), root)
		if err != nil {
			b.Fatal(err)
		}
		_ = pm
	}
}
