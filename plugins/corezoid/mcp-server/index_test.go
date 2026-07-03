package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runBuildOnFixture builds the index against a copy of the fixture directory
// (so tests never mutate committed fixture data — .corezoid/ is created
// alongside the .conv.json files) and returns the ProjectMap.
func runBuildOnFixture(t *testing.T, name string) (*ProjectMap, string) {
	t.Helper()
	src := filepath.Join("testdata", "index_fixtures", name)
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("fixture %s missing: %v", name, err)
	}
	dst := t.TempDir()
	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	pm, warnings, err := BuildProjectIndex(context.Background(), dst)
	if err != nil {
		t.Fatalf("build failed: %v (warnings: %v)", err, warnings)
	}
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}
	return pm, dst
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// TestIndex_BasicFixture is the primary happy-path test — a small project
// with aliases, env vars, cross-process edges of all three types, an api node
// with an external URL, a state store, and an orphaned-candidate with a
// suspicious name. Verifies the full pipeline in one go.
func TestIndex_BasicFixture(t *testing.T) {
	pm, _ := runBuildOnFixture(t, "basic")

	if pm.ProcessCount != 3 {
		t.Errorf("ProcessCount = %d, want 3 (100, 200, 400 — state store 300 is separate)", pm.ProcessCount)
	}
	if pm.StateStoreCount != 1 {
		t.Errorf("StateStoreCount = %d, want 1", pm.StateStoreCount)
	}
	if _, ok := pm.Processes["100"]; !ok {
		t.Fatalf("process 100 missing from index")
	}
	if _, ok := pm.StateStores["300"]; !ok {
		t.Fatalf("state store 300 missing")
	}

	// Edges: 100 -> 200 (api_rpc via @notify), 100 -> 300 (api_copy create),
	// 200 -> 100 (api_get_task).
	edges := indexEdges(pm)
	expect := []string{
		"100->200/api_rpc/@notify",
		"100->300/api_copy/create",
		"200->100/api_get_task",
	}
	for _, e := range expect {
		if _, ok := edges[e]; !ok {
			t.Errorf("missing edge: %s (got: %v)", e, keys(edges))
		}
	}

	// calls_in derived from edges
	if callers := pm.CallsIn["100"]; len(callers) != 1 || callers[0] != "200" {
		t.Errorf("calls_in[100] = %v, want [200]", callers)
	}
	// state_stores.written_by derived from api_copy edges
	if wb := pm.StateStores["300"].WrittenBy; len(wb) != 1 || wb[0] != "100" {
		t.Errorf("state_stores[300].written_by = %v, want [100]", wb)
	}

	// Aliases resolved
	if pm.ByAlias["@notify"] != "200" {
		t.Errorf("by_alias[@notify] = %q, want 200", pm.ByAlias["@notify"])
	}
	if pm.ByAlias["notify"] != "200" {
		t.Errorf("by_alias without @ should also resolve: got %q", pm.ByAlias["notify"])
	}

	// Env vars: STRIPE_URL and STRIPE_TOKEN used by 100 (via api node).
	if used := pm.EnvVars["STRIPE_URL"].UsedBy; len(used) != 1 || used[0] != "100" {
		t.Errorf("env_vars[STRIPE_URL].used_by = %v, want [100]", used)
	}
	if used := pm.EnvVars["STRIPE_TOKEN"].UsedBy; len(used) != 1 || used[0] != "100" {
		t.Errorf("env_vars[STRIPE_TOKEN].used_by = %v, want [100]", used)
	}

	// External APIs — the api node's url has a template expression, so the
	// stored url is the raw template string. Verify 100 is listed as a caller.
	found := false
	for _, callers := range pm.ExternalAPIs {
		for _, c := range callers {
			if c == "100" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("external_apis has no entry from process 100: %+v", pm.ExternalAPIs)
	}

	// graph_stats: 400 has fan_in=0, no alias, no callback, boring name
	// starting with "test" — should be orphaned (suspicious).
	sawOrphaned400 := false
	for _, o := range pm.GraphStats.Orphaned {
		if o.ConvID == "400" {
			sawOrphaned400 = true
			if !o.SuspiciousName {
				t.Errorf("400 title starts with 'test' — SuspiciousName should be true")
			}
		}
	}
	if !sawOrphaned400 {
		t.Errorf("process 400 should be orphaned; got %+v", pm.GraphStats.Orphaned)
	}

	// State store 300 should NOT be in orphaned/entry_points at all.
	for _, o := range pm.GraphStats.Orphaned {
		if o.ConvID == "300" {
			t.Errorf("state store 300 must not be marked orphaned (heuristic excludes conv_type=state)")
		}
	}
	for _, ep := range pm.GraphStats.EntryPoints {
		if ep == "300" {
			t.Errorf("state store 300 must not be marked entry_point")
		}
	}

	// 100 has fan_in > 0 (200 calls it via api_get_task), so it's not
	// classified as entry_point. Verify via calls_in (fan_in/fan_out maps
	// are no longer stored in graph_stats — derive from edges or calls_in).
	if callers := pm.CallsIn["100"]; len(callers) != 1 || callers[0] != "200" {
		t.Errorf("calls_in[100] = %v, want [200] (api_get_task from 200)", callers)
	}
	outgoing := 0
	for _, e := range pm.Edges {
		if e.From == "100" {
			outgoing++
		}
	}
	if outgoing != 2 {
		t.Errorf("outgoing edges from 100 = %d, want 2 (api_rpc to 200, api_copy to 300)", outgoing)
	}
}

// TestIndex_NoOptionalFiles verifies TZ §12 requirement: a project with no
// _ALIASES_.json, no _ENV_VARS_.json, and no *.instance.json builds cleanly,
// producing empty-but-valid sections rather than errors.
func TestIndex_NoOptionalFiles(t *testing.T) {
	pm, _ := runBuildOnFixture(t, "no_optional")

	if pm.ProcessCount != 1 {
		t.Errorf("ProcessCount = %d, want 1", pm.ProcessCount)
	}
	if pm.ByAlias == nil {
		t.Error("by_alias must be initialised (empty), not nil")
	}
	if pm.EnvVars == nil {
		t.Error("env_vars must be initialised, not nil")
	}
	if pm.Instances == nil {
		t.Error("instances must be initialised, not nil")
	}
	if len(pm.ByAlias) != 0 {
		t.Errorf("by_alias should be empty, got %v", pm.ByAlias)
	}
	if len(pm.EnvVars) != 0 {
		t.Errorf("env_vars should be empty, got %v", pm.EnvVars)
	}
	if len(pm.Instances) != 0 {
		t.Errorf("instances should be empty, got %v", pm.Instances)
	}
	if len(pm.SecurityHotspots) != 0 {
		t.Errorf("security_hotspots should be empty, got %v", pm.SecurityHotspots)
	}
}

// TestIndex_Cycles verifies both A→B→A and self-loop detection.
func TestIndex_Cycles(t *testing.T) {
	pm, _ := runBuildOnFixture(t, "cycle")

	if len(pm.GraphStats.Cycles) < 2 {
		t.Fatalf("expected at least 2 cycles (600↔601 and 602 self), got %v", pm.GraphStats.Cycles)
	}
	sawAB := false
	sawSelf := false
	for _, c := range pm.GraphStats.Cycles {
		joined := strings.Join(c, "->")
		if strings.Contains(joined, "600->601") || strings.Contains(joined, "601->600") {
			sawAB = true
		}
		if len(c) == 1 && c[0] == "602" {
			sawSelf = true
		}
	}
	if !sawAB {
		t.Errorf("missing 600↔601 cycle: %v", pm.GraphStats.Cycles)
	}
	if !sawSelf {
		t.Errorf("missing 602 self-loop: %v", pm.GraphStats.Cycles)
	}
}

// TestIndex_UnresolvedAlias verifies that an @alias missing from
// _ALIASES_.json ends up in unresolved_targets, not silently dropped.
func TestIndex_UnresolvedAlias(t *testing.T) {
	dst := t.TempDir()
	// Process that references @nonexistent.
	proc := `{
	  "obj_type":1, "obj_id":900, "title":"Broken",
	  "conv_type":"process", "status":"active",
	  "scheme":{"nodes":[
	    {"id":"9990000000000000000000f1","title":"Start","obj_type":1,
	     "condition":{"logics":[{"type":"go","to_node_id":"9990000000000000000000f2"}]}},
	    {"id":"9990000000000000000000f2","title":"Call missing","obj_type":0,
	     "condition":{"logics":[{"type":"api_rpc","conv_id":"@nonexistent"}]}}
	  ]}
	}`
	if err := os.WriteFile(filepath.Join(dst, "900_broken.conv.json"), []byte(proc), 0644); err != nil {
		t.Fatal(err)
	}
	// Empty aliases file — real Corezoid exports use a JSON array shape.
	if err := os.WriteFile(filepath.Join(dst, "_ALIASES_.json"), []byte(`[]`), 0644); err != nil {
		t.Fatal(err)
	}

	pm, _, err := BuildProjectIndex(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	list, ok := pm.UnresolvedTargets["900"]
	if !ok || len(list) == 0 {
		t.Fatalf("expected unresolved_targets[900] to contain @nonexistent, got %v", pm.UnresolvedTargets)
	}
	if len(pm.Edges) != 0 {
		t.Errorf("expected 0 edges (broken alias), got %d: %+v", len(pm.Edges), pm.Edges)
	}
}

// TestIndex_AliasRealExportShape locks in the actual _ALIASES_.json shape
// that Corezoid exports write — a JSON **array** of alias records with
// short_name / obj_to_id / obj_to_type, not a flat name→id map. An earlier
// implementation only accepted the flat map (matched an assumed shape), and
// on a real project 73/95 processes' cross-alias calls silently disappeared
// from the graph because Unmarshal into a map fails on an array payload.
//
// This test is deliberately verbose about the shape — it's the regression
// guard against re-introducing the "wrong assumed shape" bug.
func TestIndex_AliasRealExportShape(t *testing.T) {
	dst := t.TempDir()
	// Real-shape aliases file: array of records.
	// Includes an entry with obj_to_id: null (unresolved alias, must be
	// silently skipped) and an entry with obj_to_type: "folder" (still
	// tracked in by_alias but only edges emitted if the target conv_id is a
	// process in the inventory).
	aliases := `[
	  {"short_name": "payments", "obj_to_id": 5000, "obj_to_type": "conv",
	   "title": "payments", "obj_id": 1},
	  {"short_name": "orphan-alias", "obj_to_id": null, "obj_to_type": null,
	   "title": "orphan-alias", "obj_id": 2},
	  {"short_name": "some-folder", "obj_to_id": 9999, "obj_to_type": "folder",
	   "title": "some-folder", "obj_id": 3},
	  {"short_name": "", "obj_to_id": 5001, "obj_to_type": "conv",
	   "title": "notify", "obj_id": 4}
	]`
	if err := os.WriteFile(filepath.Join(dst, "_ALIASES_.json"), []byte(aliases), 0644); err != nil {
		t.Fatal(err)
	}
	// Two processes: 5000 (referenced by @payments) and 5001 (referenced by
	// @notify via the title fallback since short_name was empty). A third
	// process that calls both via alias, so we can verify edges resolve.
	os.WriteFile(filepath.Join(dst, "5000_pay.conv.json"), []byte(`{
	  "obj_id":5000,"title":"Pay","conv_type":"process","status":"active",
	  "scheme":{"nodes":[{"id":"aaaa000000000000000050001","title":"S","obj_type":1,
	   "condition":{"logics":[{"type":"go","to_node_id":"aaaa000000000000000050002"}]}},
	   {"id":"aaaa000000000000000050002","title":"E","obj_type":2,"condition":{"logics":[]}}]}
	}`), 0644)
	os.WriteFile(filepath.Join(dst, "5001_notify.conv.json"), []byte(`{
	  "obj_id":5001,"title":"N","conv_type":"process","status":"active",
	  "scheme":{"nodes":[{"id":"bbbb000000000000000050101","title":"S","obj_type":1,
	   "condition":{"logics":[{"type":"go","to_node_id":"bbbb000000000000000050102"}]}},
	   {"id":"bbbb000000000000000050102","title":"E","obj_type":2,"condition":{"logics":[]}}]}
	}`), 0644)
	os.WriteFile(filepath.Join(dst, "5100_caller.conv.json"), []byte(`{
	  "obj_id":5100,"title":"Caller","conv_type":"process","status":"active",
	  "scheme":{"nodes":[
	   {"id":"cccc000000000000000051001","title":"S","obj_type":1,
	    "condition":{"logics":[{"type":"go","to_node_id":"cccc000000000000000051002"}]}},
	   {"id":"cccc000000000000000051002","title":"CallPay","obj_type":0,
	    "condition":{"logics":[{"type":"api_rpc","conv_id":"@payments"}]}},
	   {"id":"cccc000000000000000051003","title":"CallNotify","obj_type":0,
	    "condition":{"logics":[{"type":"api_rpc","conv_id":"@notify"}]}}
	  ]}
	}`), 0644)

	pm, warnings, err := BuildProjectIndex(context.Background(), dst)
	if err != nil {
		t.Fatalf("build failed on real-shape aliases: %v (warnings: %v)", err, warnings)
	}

	// Aliases must resolve — this is the critical assertion the bug broke.
	if pm.ByAlias["@payments"] != "5000" {
		t.Errorf("by_alias[@payments] = %q, want 5000 (real-shape array parsing broken)", pm.ByAlias["@payments"])
	}
	if pm.ByAlias["@notify"] != "5001" {
		t.Errorf("by_alias[@notify] = %q, want 5001 (title fallback when short_name empty broken)", pm.ByAlias["@notify"])
	}
	// Alias to a folder should still be tracked in by_alias, but no edge
	// materialises because 9999 isn't a process in this project.
	if pm.ByAlias["@some-folder"] != "9999" {
		t.Errorf("by_alias[@some-folder] = %q, want 9999 (folder-typed aliases still trackable)", pm.ByAlias["@some-folder"])
	}
	// obj_to_id: null must be silently skipped, not emit @orphan-alias.
	if _, present := pm.ByAlias["@orphan-alias"]; present {
		t.Errorf("orphan alias with obj_to_id=null must be skipped, but by_alias has it")
	}

	// Cross-check that the caller's edges resolved via the alias map.
	sawPay, sawNotify := false, false
	for _, e := range pm.Edges {
		if e.From == "5100" && e.To == "5000" && e.Type == "api_rpc" {
			sawPay = true
		}
		if e.From == "5100" && e.To == "5001" && e.Type == "api_rpc" {
			sawNotify = true
		}
	}
	if !sawPay {
		t.Errorf("edge 5100→5000 (via @payments) missing — alias resolution broken")
	}
	if !sawNotify {
		t.Errorf("edge 5100→5001 (via @notify, title-fallback) missing")
	}
	// And that unresolved_targets is empty for this caller (nothing dropped).
	if len(pm.UnresolvedTargets["5100"]) != 0 {
		t.Errorf("5100 has unresolved targets: %v — should all resolve via aliases",
			pm.UnresolvedTargets["5100"])
	}
}

// TestIndex_ApiGetTaskEdge is the explicit test called out in TZ §12: without
// api_get_task recognition the graph systematically undercounts links and
// diverges from stage-scan output.
func TestIndex_ApiGetTaskEdge(t *testing.T) {
	pm, _ := runBuildOnFixture(t, "basic")
	found := false
	for _, e := range pm.Edges {
		if e.Type == "api_get_task" && e.From == "200" && e.To == "100" {
			found = true
		}
	}
	if !found {
		t.Errorf("api_get_task edge 200->100 not found; edges: %+v", pm.Edges)
	}
}

// TestIndex_SecretsNeverLeakValues is the security-critical test: no output
// file may contain the value of any secret-shaped field. We deliberately put
// unusual, high-entropy-looking values in the fixture and grep the serialised
// output for them.
func TestIndex_SecretsNeverLeakValues(t *testing.T) {
	pm, dst := runBuildOnFixture(t, "secrets")

	// Persist the map like the real handler would.
	if err := WriteProjectMap(dst, pm); err != nil {
		t.Fatal(err)
	}
	if err := WriteQueriesMD(dst); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateClaudeMD(dst, pm); err != nil {
		t.Fatal(err)
	}

	forbidden := []string{
		"FAKE_sk_live_actualsecrethere",
		"FAKE_P@ssw0rd-ShouldBeCaught",
		"FAKE_S3cret!Value",
		"FAKE_abcdefghij_012345_secret_value",
	}
	filesToScan := []string{
		filepath.Join(dst, IndexOutputDir, IndexMapFile),
		filepath.Join(dst, IndexOutputDir, IndexQueriesFile),
		filepath.Join(dst, "CLAUDE.md"),
	}
	for _, f := range filesToScan {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, needle := range forbidden {
			if strings.Contains(string(body), needle) {
				t.Errorf("SECURITY: secret value %q leaked into %s", needle, f)
			}
		}
	}

	// But the FIELD NAMES must be recorded, otherwise the feature is useless.
	if len(pm.SecurityHotspots) == 0 {
		t.Fatal("no security_hotspots recorded — detection is broken")
	}
	sawInstance := false
	sawDiagram := false
	for _, sh := range pm.SecurityHotspots {
		if sh.Source == "instance" && contains(sh.Fields, "password") {
			sawInstance = true
		}
		if sh.Source == "diagram" {
			sawDiagram = true
			// Should include field names (values-shaped) but NOT the templated
			// X-Password-Templated (value is {{...}}) nor the short Authorization.
			if contains(sh.Fields, "X-Password-Templated") {
				t.Errorf("templated field must be filtered out: got %v", sh.Fields)
			}
			if contains(sh.Fields, "Authorization") {
				t.Errorf("short 'short' value should be filtered as unlikely-secret: got %v", sh.Fields)
			}
		}
	}
	if !sawInstance {
		t.Error("no instance-source hotspot recorded for db password")
	}
	if !sawDiagram {
		t.Error("no diagram-source hotspot recorded for hardcoded diagram fields")
	}
}

// TestIndex_EnvVarUsageAggregation ensures {{env_var[@X]}} references from
// nested logic blocks (set_param.extra map, api_code.code string) all get
// aggregated into env_vars[X].used_by.
func TestIndex_EnvVarUsageAggregation(t *testing.T) {
	pm, _ := runBuildOnFixture(t, "envvars")

	if len(pm.EnvVars) < 3 {
		t.Fatalf("expected at least 3 env vars discovered from usage (API_URL, API_TOKEN, WORKSPACE); got %v",
			pm.EnvVars)
	}
	for _, name := range []string{"API_URL", "API_TOKEN", "WORKSPACE"} {
		ev, ok := pm.EnvVars[name]
		if !ok {
			t.Errorf("env var %s not discovered from {{env_var[@%s]}} usage", name, name)
			continue
		}
		if len(ev.UsedBy) != 1 || ev.UsedBy[0] != "800" {
			t.Errorf("env_vars[%s].used_by = %v, want [800]", name, ev.UsedBy)
		}
	}
}

// TestIndex_CallsInFromEdges verifies calls_in is derived from edges only —
// calls_out was removed (derive via '[.edges[] | select(.from == $id)]' in jq).
func TestIndex_CallsInFromEdges(t *testing.T) {
	pm, _ := runBuildOnFixture(t, "basic")

	// Recompute calls_in from edges and compare.
	rebuilt := map[string]map[string]struct{}{}
	for _, e := range pm.Edges {
		if rebuilt[e.To] == nil {
			rebuilt[e.To] = map[string]struct{}{}
		}
		rebuilt[e.To][e.From] = struct{}{}
	}
	for cid, set := range rebuilt {
		want := mapKeysSorted(set)
		got := pm.CallsIn[cid]
		if !equalStrings(want, got) {
			t.Errorf("calls_in[%s] = %v, want %v", cid, got, want)
		}
	}
}

// TestIndex_HashStabilityOnRawBytes locks in the definition from TZ §5: hash
// is the SHA-1 of raw file bytes as they exist on disk. If a future change
// switches to hashing the parsed-and-remarshalled JSON, this test breaks
// (which is the point — the change would silently invalidate every existing
// index without bumping schema_version).
func TestIndex_HashStabilityOnRawBytes(t *testing.T) {
	src := filepath.Join("testdata", "index_fixtures", "no_optional", "500_simple.conv.json")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	direct := hashBytes(raw)

	pm, _ := runBuildOnFixture(t, "no_optional")
	if pm.Processes["500"].Hash != direct {
		t.Errorf("stored hash %q != raw-bytes hash %q — hash must be over the on-disk bytes",
			pm.Processes["500"].Hash, direct)
	}
	if len(pm.Processes["500"].Hash) != IndexHashHexLen {
		t.Errorf("hash length %d, want %d", len(pm.Processes["500"].Hash), IndexHashHexLen)
	}
}

// TestIndex_CheckFreshness_NoChangesAfterCheckout is the specific test called
// out in TZ criterion #14 — `git checkout` / repeated `pull-folder` stamps
// new mtimes but doesn't change content, and --check must NOT report those
// files as stale. Simulate by rebuilding the index, then bumping mtimes.
func TestIndex_CheckFreshness_NoChangesAfterCheckout(t *testing.T) {
	pm, dst := runBuildOnFixture(t, "basic")
	if err := WriteProjectMap(dst, pm); err != nil {
		t.Fatal(err)
	}

	// Bump mtime of every .conv.json without touching content, simulating
	// `git checkout`.
	newTime := os.FileInfo(nil)
	_ = newTime
	err := filepath.Walk(dst, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		if !strings.HasSuffix(info.Name(), ".conv.json") {
			return nil
		}
		// Push mtime forward by 60 seconds — enough to defeat RFC3339 second
		// resolution comparison.
		t := info.ModTime().Add(60_000_000_000) // 60s in ns
		return os.Chtimes(path, t, t)
	})
	if err != nil {
		t.Fatal(err)
	}

	rpt, err := CheckIndexFreshness(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(rpt.Changed) != 0 || len(rpt.Added) != 0 || len(rpt.Removed) != 0 {
		t.Errorf("mtime-only change should NOT be reported stale; got %+v", rpt)
	}
}

// TestIndex_CheckFreshness_RealChange verifies the check does detect real
// content changes (the other side of criterion #14).
func TestIndex_CheckFreshness_RealChange(t *testing.T) {
	pm, dst := runBuildOnFixture(t, "no_optional")
	if err := WriteProjectMap(dst, pm); err != nil {
		t.Fatal(err)
	}

	// Modify one file's contents.
	target := filepath.Join(dst, "500_simple.conv.json")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	modified := strings.Replace(string(data), `"title": "Simple"`, `"title": "Simple2"`, 1)
	if err := os.WriteFile(target, []byte(modified), 0644); err != nil {
		t.Fatal(err)
	}
	// Bump mtime by 2 seconds so the --check mtime fast-path doesn't skip
	// hash comparison (same-second write → mtime unchanged at RFC3339 resolution).
	info2, _ := os.Stat(target)
	future := info2.ModTime().Add(2 * 1e9)
	_ = os.Chtimes(target, future, future)

	rpt, err := CheckIndexFreshness(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(rpt.Changed, "500") {
		t.Errorf("expected 500 in Changed, got %+v", rpt)
	}
}

// --- helpers -----------------------------------------------------------

func indexEdges(pm *ProjectMap) map[string]struct{} {
	out := map[string]struct{}{}
	for _, e := range pm.Edges {
		key := e.From + "->" + e.To + "/" + e.Type
		if e.Mode != "" {
			key += "/" + e.Mode
		}
		if e.ViaAlias != "" {
			key += "/" + e.ViaAlias
		}
		out[key] = struct{}{}
	}
	return out
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}


// TestIndex_AutoRebuildHelper verifies the shared helper that
// handlePullFolder and handlePushProcess call at the end of a successful
// operation. This is the code-level guarantee that a fresh pull/push
// produces a fresh index without relying on a SKILL.md prompt to remember —
// see the note in handlePullFolder and handlePushProcess for the rationale.
func TestIndex_AutoRebuildHelper(t *testing.T) {
	src := filepath.Join("testdata", "index_fixtures", "basic")
	dst := t.TempDir()
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dst); err != nil {
		t.Fatal(err)
	}

	// Fresh project — no .corezoid yet.
	if _, err := os.Stat(filepath.Join(dst, IndexOutputDir)); err == nil {
		t.Fatalf("fixture should not contain .corezoid before auto-rebuild")
	}

	out := autoRebuildIndex(context.Background(), ".")
	if !strings.Contains(out, "Project index refreshed") {
		t.Errorf("expected success message; got %q", out)
	}
	// All three artefacts must exist after the helper returns.
	for _, name := range []string{IndexMapFile, IndexQueriesFile, IndexConfigFile} {
		p := filepath.Join(dst, IndexOutputDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s after auto-rebuild, missing: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md not created by auto-rebuild: %v", err)
	}
	// First-creation notice must be present so the user knows CLAUDE.md
	// was touched — see TZ §6.2 social contract.
	if !strings.Contains(out, "Added an auto-generated section to CLAUDE.md") {
		t.Errorf("first-run auto-rebuild should announce CLAUDE.md creation; got %q", out)
	}

	// Second run — same directory, no new files — must not announce
	// first-creation again (block is idempotent, markers exist).
	out2 := autoRebuildIndex(context.Background(), ".")
	if strings.Contains(out2, "Added an auto-generated section to CLAUDE.md") {
		t.Errorf("second run should not repeat the first-creation notice; got %q", out2)
	}
}

// TestIndex_CyclesEmptyIsArrayNotNull locks in that graph_stats.cycles
// serialises as [] when no cycles are found, not null. Go's zero value for
// [][]string is nil, and nil slices JSON-encode to null — but the rest of
// the schema uses [] uniformly for "empty", and downstream consumers
// (corezoid-project-review reading .graph_stats.cycles directly, jq recipes
// in QUERIES.md query #7) rely on that invariant. Regressing to null would
// silently break them.
func TestIndex_CyclesEmptyIsArrayNotNull(t *testing.T) {
	// no_optional fixture has one process with no cross-process calls →
	// zero cycles.
	pm, _ := runBuildOnFixture(t, "no_optional")
	if pm.GraphStats == nil {
		t.Fatal("GraphStats nil — fixture broken?")
	}
	if pm.GraphStats.Cycles == nil {
		t.Fatal("Cycles is nil — must be an empty slice so JSON emits [] not null")
	}
	if len(pm.GraphStats.Cycles) != 0 {
		t.Fatalf("expected 0 cycles on no_optional fixture, got %d", len(pm.GraphStats.Cycles))
	}
	data, err := json.Marshal(pm.GraphStats)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, `"cycles":null`) {
		t.Errorf(`graph_stats serialised "cycles":null; want "cycles":[]. Payload: %s`, s)
	}
	if !strings.Contains(s, `"cycles":[]`) {
		t.Errorf(`graph_stats does not contain "cycles":[]. Payload: %s`, s)
	}
}

// TestIndex_JSONShape guards the schema field names — jq queries in
// QUERIES.md depend on them. If someone accidentally renames a struct field
// (or forgets a `json:` tag), the QUERIES.md commands stop working with no
// compile-time signal, so we assert the shape here.
func TestIndex_JSONShape(t *testing.T) {
	pm, _ := runBuildOnFixture(t, "basic")
	data, err := json.MarshalIndent(pm, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	must := []string{
		`"schema_version"`,
		`"processes"`,
		`"by_alias"`,
		`"env_vars"`,
		`"edges"`,
		`"calls_in"`,
		`"unresolved_targets"`,
		`"external_apis"`,
		`"state_stores"`,
		`"instances"`,
		`"graph_stats"`,
		`"security_hotspots"`,
	}
	s := string(data)
	for _, k := range must {
		if !strings.Contains(s, k) {
			t.Errorf("project-map.json missing required top-level key %s", k)
		}
	}
	// Fields inside graph_stats used by QUERIES.md query #7
	for _, k := range []string{`"high_fan_in"`, `"high_fan_out"`, `"orphaned"`, `"entry_points"`, `"cycles"`} {
		if !strings.Contains(s, k) {
			t.Errorf("graph_stats missing required key %s", k)
		}
	}
}

// ---------------------------------------------------------------------------
// from index_claude_md_test.go
// ---------------------------------------------------------------------------



func makeStubPM() *ProjectMap {
	return &ProjectMap{
		SchemaVersion: IndexSchemaVersion,
		GeneratedAt:   "2026-07-02T00:00:00Z",
		ProcessCount:  3,
		StateStoreCount: 0,
		InstanceCount: 0,
		Processes:     map[string]*ProcessEntry{},
		EnvVars:       map[string]*EnvVarEntry{},
		GraphStats:    &GraphStats{},
	}
}

// TestCLAUDE_CreateFromScratch — TZ §12: file doesn't exist → scaffold+block.
func TestCLAUDE_CreateFromScratch(t *testing.T) {
	dir := t.TempDir()
	pm := makeStubPM()
	first, err := UpdateClaudeMD(dir, pm)
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Errorf("expected firstCreation=true when file didn't exist")
	}
	body, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, "# Project Notes") {
		t.Errorf("scaffold missing — file must include user-facing header when created fresh")
	}
	if !strings.Contains(s, "<!-- corezoid-index:start -->") {
		t.Errorf("start marker missing")
	}
	if !strings.Contains(s, "<!-- corezoid-index:end -->") {
		t.Errorf("end marker missing")
	}
}

// TestCLAUDE_NoMarkers — TZ §12: existing file without markers → block
// appended, original content preserved.
func TestCLAUDE_NoMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	orig := "# Team CLAUDE.md\n\nOur rules here.\n"
	if err := os.WriteFile(path, []byte(orig), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := UpdateClaudeMD(dir, makeStubPM())
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Errorf("expected firstCreation=true when markers were absent")
	}
	body, _ := os.ReadFile(path)
	s := string(body)
	if !strings.HasPrefix(s, "# Team CLAUDE.md\n\nOur rules here.\n") {
		t.Errorf("original content was not preserved verbatim at start:\n%s", s)
	}
	if !strings.Contains(s, "<!-- corezoid-index:start -->") {
		t.Errorf("marker start missing")
	}
}

// TestCLAUDE_OrphanStartMarker — TZ §12: only start marker present. Behaviour
// documented: treat as if no markers exist, append fresh block, do not
// attempt to guess the intended block boundary.
func TestCLAUDE_OrphanStartMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	orig := "# Broken\n\n<!-- corezoid-index:start -->\nleftover content\n\nsome team notes below.\n"
	if err := os.WriteFile(path, []byte(orig), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := UpdateClaudeMD(dir, makeStubPM())
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Errorf("expected firstCreation=true when markers are malformed (start only)")
	}
	body, _ := os.ReadFile(path)
	s := string(body)
	// Original content must be preserved verbatim.
	if !strings.Contains(s, "leftover content") {
		t.Errorf("original content lost after malformed-marker recovery: %s", s)
	}
	// A fresh, well-formed block must be appended (so there are now two
	// start markers, which is fine — the next run will find the first
	// well-formed pair).
	if strings.Count(s, "<!-- corezoid-index:end -->") != 1 {
		t.Errorf("expected exactly one end marker after recovery, got %d", strings.Count(s, "<!-- corezoid-index:end -->"))
	}
}

// TestCLAUDE_UserContentPreservedAroundBlock — TZ §12: user content before
// AND after the block round-trips verbatim.
func TestCLAUDE_UserContentPreservedAroundBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	before := "# Header\n\nRules above.\n\n"
	block := "<!-- corezoid-index:start -->\nold auto-block\n<!-- corezoid-index:end -->"
	after := "\n\nMore rules below.\n"
	orig := before + block + after
	if err := os.WriteFile(path, []byte(orig), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := UpdateClaudeMD(dir, makeStubPM())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	s := string(body)
	if !strings.HasPrefix(s, before) {
		t.Errorf("content before block not preserved verbatim:\n%s", s)
	}
	if !strings.Contains(s, "More rules below.") {
		t.Errorf("content after block not preserved:\n%s", s)
	}
	if strings.Contains(s, "old auto-block") {
		t.Errorf("stale auto-block content not replaced:\n%s", s)
	}
}

// TestCLAUDE_NoOp — TZ §12: rebuild with same input produces byte-identical
// output (ignoring the generated_at timestamp, which we hold fixed via
// makeStubPM).
func TestCLAUDE_NoOp(t *testing.T) {
	dir := t.TempDir()
	pm := makeStubPM()
	if _, err := UpdateClaudeMD(dir, pm); err != nil {
		t.Fatal(err)
	}
	body1, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	// Second call with same pm: on the not-first path, firstCreation=false
	// (well-formed markers exist).
	first, err := UpdateClaudeMD(dir, pm)
	if err != nil {
		t.Fatal(err)
	}
	if first {
		t.Errorf("expected firstCreation=false on repeat run")
	}
	body2, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if string(body1) != string(body2) {
		t.Errorf("repeat rebuild changed CLAUDE.md — expected byte-for-byte identical:\n--- first ---\n%s\n--- second ---\n%s",
			body1, body2)
	}
}

// ---------------------------------------------------------------------------
// from index_config_test.go
// ---------------------------------------------------------------------------



// TestConfig_AutoCreateWithDefaults — TZ §7 config lifecycle: file absent →
// created with defaults on first run.
func TestConfig_AutoCreateWithDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, warnings, err := LoadOrCreateIndexConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("no warnings expected on first creation, got %v", warnings)
	}
	defaults := defaultIndexConfig()
	if !equalStrings(cfg.EntryPointNamePrefixes, defaults.EntryPointNamePrefixes) {
		t.Errorf("prefixes differ from defaults: %v vs %v", cfg.EntryPointNamePrefixes, defaults.EntryPointNamePrefixes)
	}
	path := filepath.Join(dir, IndexOutputDir, IndexConfigFile)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("index-config.json not created: %v", err)
	}
}

// TestConfig_NotOverwrittenOnSecondRun — TZ §7: existing config with custom
// values must not be overwritten on subsequent rebuilds.
func TestConfig_NotOverwrittenOnSecondRun(t *testing.T) {
	dir := t.TempDir()
	// Prime a custom config.
	custom := IndexConfig{
		EntryPointNamePrefixes:     []string{"эндпоинт", "публичный"},
		EntryPointLocationKeywords: []string{"api", "public"},
		SuspiciousNameKeywords:     []string{"тест", "временный", "копия"},
	}
	os.MkdirAll(filepath.Join(dir, IndexOutputDir), 0755)
	data, _ := json.MarshalIndent(custom, "", "  ")
	os.WriteFile(filepath.Join(dir, IndexOutputDir, IndexConfigFile), data, 0644)

	cfg, warnings, err := LoadOrCreateIndexConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if !equalStrings(cfg.SuspiciousNameKeywords, custom.SuspiciousNameKeywords) {
		t.Errorf("custom Cyrillic list lost on reload: %v", cfg.SuspiciousNameKeywords)
	}
	// Verify file on disk still holds the custom content byte-for-byte.
	onDisk, _ := os.ReadFile(filepath.Join(dir, IndexOutputDir, IndexConfigFile))
	if !strings.Contains(string(onDisk), "тест") {
		t.Errorf("existing config was rewritten — should be immutable across runs")
	}
}

// TestConfig_PartialBrokenPerKeyFallback — TZ §7: malformed key falls back to
// default without breaking the others, and produces a diagnostic warning.
func TestConfig_PartialBrokenPerKeyFallback(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, IndexOutputDir), 0755)
	broken := `{
	  "entry_point_name_prefixes": ["public"],
	  "suspicious_name_keywords": "not-an-array",
	  "entry_point_location_keywords": ["ext"]
	}`
	os.WriteFile(filepath.Join(dir, IndexOutputDir, IndexConfigFile), []byte(broken), 0644)

	cfg, warnings, err := LoadOrCreateIndexConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Errorf("expected a warning about bad suspicious_name_keywords key")
	}
	// Good keys survived
	if len(cfg.EntryPointNamePrefixes) != 1 || cfg.EntryPointNamePrefixes[0] != "public" {
		t.Errorf("good prefix list lost: %v", cfg.EntryPointNamePrefixes)
	}
	if len(cfg.EntryPointLocationKeywords) != 1 || cfg.EntryPointLocationKeywords[0] != "ext" {
		t.Errorf("good location list lost: %v", cfg.EntryPointLocationKeywords)
	}
	// Bad key fell back to defaults, not to empty.
	defaults := defaultIndexConfig()
	if !equalStrings(cfg.SuspiciousNameKeywords, defaults.SuspiciousNameKeywords) {
		t.Errorf("bad key should fall back to default list, got %v", cfg.SuspiciousNameKeywords)
	}
}

// TestConfig_CustomKeywordsChangeClassification — TZ §7 criterion #15:
// overriding suspicious_name_keywords must actually change classification
// without recompiling.
func TestConfig_CustomKeywordsChangeClassification(t *testing.T) {
	// Use a fixture with a process named "test_playground" (a default match)
	// and override the config to drop "test" from suspicious words.
	src := filepath.Join("testdata", "index_fixtures", "basic")
	dst := t.TempDir()
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dst, IndexOutputDir), 0755)
	custom := IndexConfig{
		EntryPointNamePrefixes:     []string{"api"},
		EntryPointLocationKeywords: []string{"api"},
		SuspiciousNameKeywords:     []string{"unused_word_only"},
	}
	data, _ := json.MarshalIndent(custom, "", "  ")
	os.WriteFile(filepath.Join(dst, IndexOutputDir, IndexConfigFile), data, 0644)

	pm, _, err := BuildProjectIndex(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range pm.GraphStats.Orphaned {
		if o.ConvID == "400" && o.SuspiciousName {
			t.Errorf("400 was flagged suspicious even though 'test' isn't in the override list")
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// from index_config_references_test.go
// ---------------------------------------------------------------------------



// --- 1. defaults ----------------------------------------------------------

// TestConfigRefs_DefaultsSeeded — the 10 preseeded refs from
// defaultIndexConfig() appear in index-config.json on first creation.
// Regressing the preseed would blank out config_references for every
// project that relies on the default list.
func TestConfigRefs_DefaultsSeeded(t *testing.T) {
	dir := t.TempDir()
	cfg, warnings, err := LoadOrCreateIndexConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("first-run warnings should be empty: %v", warnings)
	}
	if len(cfg.ConfigReferences.Tasks) != 10 {
		t.Fatalf("expected 10 default tasks, got %d: %+v",
			len(cfg.ConfigReferences.Tasks), cfg.ConfigReferences.Tasks)
	}
	must := map[string]bool{"config": false, "simulator": false, "corezoid": false}
	for _, task := range cfg.ConfigReferences.Tasks {
		if _, ok := must[task.Ref]; ok {
			must[task.Ref] = true
		}
	}
	for ref, seen := range must {
		if !seen {
			t.Errorf("default tasks missing ref %q", ref)
		}
	}
}

// --- 2. local scan populates used_by + read_fields -----------------------

// TestConfigRefs_LocalScanUsedByAndFields is the primary happy-path test
// for the local-scan model. Two processes reference @config differently:
// one reads two fields, the other reads one field plus mentions the ref
// bare. The result should merge both readers under `used_by` and expose
// the union of read fields under `read_fields`.
func TestConfigRefs_LocalScanUsedByAndFields(t *testing.T) {
	dir := t.TempDir()

	// Config allow-list — override defaults so we test just one ref.
	os.MkdirAll(filepath.Join(dir, IndexOutputDir), 0755)
	os.WriteFile(filepath.Join(dir, IndexOutputDir, IndexConfigFile), []byte(`{
	  "config_references": {
	    "tasks": [{"ref": "config", "label": "config"}],
	    "mask_field_names": [], "mask_field_name_patterns": [], "never_mask_field_names": []
	  }
	}`), 0644)

	// Reader A: {{conv[@config].ref[api_url]}} and {{conv[@config].ref[dev_mode]}}
	os.WriteFile(filepath.Join(dir, "100_reader_a.conv.json"), []byte(`{
	  "obj_id":100,"title":"Reader A","conv_type":"process","status":"active",
	  "scheme":{"nodes":[
	    {"id":"aaaa000000000000000000a1","title":"S","obj_type":1,
	     "condition":{"logics":[{"type":"go","to_node_id":"aaaa000000000000000000a2"}]}},
	    {"id":"aaaa000000000000000000a2","title":"Use","obj_type":0,
	     "condition":{"logics":[{"type":"set_param","extra":{
	       "url":"{{conv[@config].ref[api_url]}}/x",
	       "flag":"{{conv[@config].ref[dev_mode]}}"
	     }}]}}
	  ]}
	}`), 0644)
	// Reader B: only .ref[timeout], plus a bare {{conv[@config]}} check.
	os.WriteFile(filepath.Join(dir, "200_reader_b.conv.json"), []byte(`{
	  "obj_id":200,"title":"Reader B","conv_type":"process","status":"active",
	  "scheme":{"nodes":[
	    {"id":"bbbb000000000000000000b1","title":"S","obj_type":1,
	     "condition":{"logics":[{"type":"go","to_node_id":"bbbb000000000000000000b2"}]}},
	    {"id":"bbbb000000000000000000b2","title":"UseAndCheck","obj_type":0,
	     "condition":{"logics":[{"type":"set_param","extra":{
	       "timeout":"{{conv[@config].ref[timeout]}}",
	       "exists":"{{conv[@config]}}"
	     }}]}}
	  ]}
	}`), 0644)
	// A third process that doesn't touch @config — should NOT appear in used_by.
	os.WriteFile(filepath.Join(dir, "300_bystander.conv.json"), []byte(`{
	  "obj_id":300,"title":"Bystander","conv_type":"process","status":"active",
	  "scheme":{"nodes":[
	    {"id":"cccc000000000000000000c1","title":"S","obj_type":1,
	     "condition":{"logics":[{"type":"go","to_node_id":"cccc000000000000000000c2"}]}},
	    {"id":"cccc000000000000000000c2","title":"E","obj_type":2,"condition":{"logics":[]}}
	  ]}
	}`), 0644)

	pm, _, err := BuildProjectIndex(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	entry := pm.ConfigReferences["config"]
	if entry == nil {
		t.Fatalf("config entry missing; got %+v", pm.ConfigReferences)
	}
	if entry.SourceRef != "config" {
		t.Errorf("SourceRef = %q, want config", entry.SourceRef)
	}
	// used_by should contain 100 and 200, not 300.
	if !equalStrings(entry.UsedBy, []string{"100", "200"}) {
		t.Errorf("UsedBy = %v, want [100 200]", entry.UsedBy)
	}
	// read_fields — union of api_url, dev_mode, timeout. Sorted.
	if !equalStrings(entry.ReadFields, []string{"api_url", "dev_mode", "timeout"}) {
		t.Errorf("ReadFields = %v, want [api_url dev_mode timeout]", entry.ReadFields)
	}
	// No alias defined for @config in this fixture → LocalConvID stays empty.
	if entry.LocalConvID != "" {
		t.Errorf("LocalConvID should be empty (no alias defined), got %q", entry.LocalConvID)
	}
}

// --- 3. Ref resolves to local state-store cross-populates state_stores ---

// TestConfigRefs_CrossPopulatesLocalStateStore ensures that when a
// configured ref resolves via _ALIASES_.json to a local state-store
// process, both the config_references entry and the state_stores entry
// carry the reader info. This is the "record in state_stores" outcome
// from the user's clarification.
func TestConfigRefs_CrossPopulatesLocalStateStore(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, IndexOutputDir), 0755)
	os.WriteFile(filepath.Join(dir, IndexOutputDir, IndexConfigFile), []byte(`{
	  "config_references": {
	    "tasks": [{"ref": "config", "label": "config"}],
	    "mask_field_names": [], "mask_field_name_patterns": [], "never_mask_field_names": []
	  }
	}`), 0644)

	// State-store process with alias @config.
	os.WriteFile(filepath.Join(dir, "_ALIASES_.json"), []byte(`[
	  {"short_name":"config","obj_to_id":500,"obj_to_type":"conv","title":"config","obj_id":1}
	]`), 0644)
	os.WriteFile(filepath.Join(dir, "500_config_store.conv.json"), []byte(`{
	  "obj_id":500,"title":"Config Store","conv_type":"state","status":"active",
	  "scheme":{"nodes":[
	    {"id":"eeee000000000000000000e1","title":"S","obj_type":1,
	     "condition":{"logics":[{"type":"go","to_node_id":"eeee000000000000000000e2"}]}},
	    {"id":"eeee000000000000000000e2","title":"Set","obj_type":0,
	     "condition":{"logics":[{"type":"set_param","extra":{"api_url":"init"}}]}}
	  ]}
	}`), 0644)
	// Reader
	os.WriteFile(filepath.Join(dir, "100_reader.conv.json"), []byte(`{
	  "obj_id":100,"title":"Reader","conv_type":"process","status":"active",
	  "scheme":{"nodes":[
	    {"id":"aaaa000000000000000000a1","title":"S","obj_type":1,
	     "condition":{"logics":[{"type":"go","to_node_id":"aaaa000000000000000000a2"}]}},
	    {"id":"aaaa000000000000000000a2","title":"Read","obj_type":0,
	     "condition":{"logics":[{"type":"set_param","extra":{"u":"{{conv[@config].ref[api_url]}}"}}]}}
	  ]}
	}`), 0644)

	pm, _, err := BuildProjectIndex(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	// config_references entry has LocalConvID pointing at the state store
	entry := pm.ConfigReferences["config"]
	if entry == nil || entry.LocalConvID != "500" {
		t.Fatalf("expected LocalConvID=500 on config entry, got %+v", entry)
	}
	if !equalStrings(entry.UsedBy, []string{"100"}) {
		t.Errorf("UsedBy = %v, want [100]", entry.UsedBy)
	}
	if !equalStrings(entry.ReadFields, []string{"api_url"}) {
		t.Errorf("ReadFields = %v, want [api_url]", entry.ReadFields)
	}
	// state_stores[500] carries ReadBy and ReadFields alongside WrittenBy.
	ss := pm.StateStores["500"]
	if ss == nil {
		t.Fatalf("state_stores[500] missing")
	}
	if !equalStrings(ss.ReadBy, []string{"100"}) {
		t.Errorf("state_stores[500].ReadBy = %v, want [100]", ss.ReadBy)
	}
	if !equalStrings(ss.ReadFields, []string{"api_url"}) {
		t.Errorf("state_stores[500].ReadFields = %v, want [api_url]", ss.ReadFields)
	}
}

// --- 4. Unused refs are omitted -----------------------------------------

// TestConfigRefs_UnusedRefsOmitted — a ref listed in the allow-list but
// never referenced by any diagram must NOT appear as a zero-usage stub.
// Presence signals real usage; absence signals "not part of the flow".
func TestConfigRefs_UnusedRefsOmitted(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, IndexOutputDir), 0755)
	os.WriteFile(filepath.Join(dir, IndexOutputDir, IndexConfigFile), []byte(`{
	  "config_references": {
	    "tasks": [
	      {"ref": "config", "label": "config"},
	      {"ref": "never_used", "label": "never_used"}
	    ],
	    "mask_field_names": [], "mask_field_name_patterns": [], "never_mask_field_names": []
	  }
	}`), 0644)
	os.WriteFile(filepath.Join(dir, "100_uses_config.conv.json"), []byte(`{
	  "obj_id":100,"title":"UsesConfig","conv_type":"process","status":"active",
	  "scheme":{"nodes":[
	    {"id":"aaaa000000000000000000a1","title":"S","obj_type":1,
	     "condition":{"logics":[{"type":"go","to_node_id":"aaaa000000000000000000a2"}]}},
	    {"id":"aaaa000000000000000000a2","title":"R","obj_type":0,
	     "condition":{"logics":[{"type":"set_param","extra":{"u":"{{conv[@config].ref[x]}}"}}]}}
	  ]}
	}`), 0644)

	pm, _, err := BuildProjectIndex(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pm.ConfigReferences["config"]; !ok {
		t.Errorf("config entry should be present (has usage)")
	}
	if _, ok := pm.ConfigReferences["never_used"]; ok {
		t.Errorf("never_used should be omitted (0 usages), got: %+v", pm.ConfigReferences["never_used"])
	}
}

// --- 5. Auto-include local state-store aliases -------------------------

// TestConfigRefs_AutoIncludeLocalStateStoreAliases — a state-store alias
// that is NOT in the user's index-config allow-list still gets scanned
// and reported, because the effective allow-list unions the config with
// every local state-store's aliases. Rationale: state-stores are
// config-sources by construction (other processes read them via
// {{conv[@X].ref[Y]}}), so requiring each project to hand-add their
// stores to the seed list means silent gaps for projects whose aliases
// aren't in the default 10.
func TestConfigRefs_AutoIncludeLocalStateStoreAliases(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, IndexOutputDir), 0755)
	// Deliberately narrow config: NO refs configured. Auto-include must
	// still surface the local state-store alias.
	os.WriteFile(filepath.Join(dir, IndexOutputDir, IndexConfigFile), []byte(`{
	  "config_references": {
	    "tasks": [],
	    "mask_field_names": [], "mask_field_name_patterns": [], "never_mask_field_names": []
	  }
	}`), 0644)
	// Alias @cache-sessions → local state-store 500.
	os.WriteFile(filepath.Join(dir, "_ALIASES_.json"), []byte(`[
	  {"short_name":"cache-sessions","obj_to_id":500,"obj_to_type":"conv","title":"cache-sessions","obj_id":1}
	]`), 0644)
	os.WriteFile(filepath.Join(dir, "500_cache.conv.json"), []byte(`{
	  "obj_id":500,"title":"Cache","conv_type":"state","status":"active",
	  "scheme":{"nodes":[
	    {"id":"eeee000000000000000000e1","title":"S","obj_type":1,
	     "condition":{"logics":[{"type":"go","to_node_id":"eeee000000000000000000e2"}]}},
	    {"id":"eeee000000000000000000e2","title":"E","obj_type":2,"condition":{"logics":[]}}
	  ]}
	}`), 0644)
	// A reader referencing @cache-sessions.ref[user_id]
	os.WriteFile(filepath.Join(dir, "100_reader.conv.json"), []byte(`{
	  "obj_id":100,"title":"Reader","conv_type":"process","status":"active",
	  "scheme":{"nodes":[
	    {"id":"aaaa000000000000000000a1","title":"S","obj_type":1,
	     "condition":{"logics":[{"type":"go","to_node_id":"aaaa000000000000000000a2"}]}},
	    {"id":"aaaa000000000000000000a2","title":"R","obj_type":0,
	     "condition":{"logics":[{"type":"set_param","extra":{"u":"{{conv[@cache-sessions].ref[user_id]}}"}}]}}
	  ]}
	}`), 0644)

	pm, _, err := BuildProjectIndex(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	entry := pm.ConfigReferences["cache-sessions"]
	if entry == nil {
		t.Fatalf("cache-sessions entry missing — auto-inclusion of state-store aliases broken; got %+v", pm.ConfigReferences)
	}
	if entry.LocalConvID != "500" {
		t.Errorf("LocalConvID = %q, want 500", entry.LocalConvID)
	}
	if !equalStrings(entry.UsedBy, []string{"100"}) {
		t.Errorf("UsedBy = %v, want [100]", entry.UsedBy)
	}
	if !equalStrings(entry.ReadFields, []string{"user_id"}) {
		t.Errorf("ReadFields = %v, want [user_id]", entry.ReadFields)
	}
}

// --- 6. No refs configured → section omitted ---------------------------

// TestConfigRefs_EmptyAllowlistNoSection — with an empty tasks list the
// section is absent from project-map.json (omitempty).
func TestConfigRefs_EmptyAllowlistNoSection(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, IndexOutputDir), 0755)
	os.WriteFile(filepath.Join(dir, IndexOutputDir, IndexConfigFile), []byte(`{
	  "config_references": {
	    "tasks": [],
	    "mask_field_names": [], "mask_field_name_patterns": [], "never_mask_field_names": []
	  }
	}`), 0644)
	os.WriteFile(filepath.Join(dir, "100_x.conv.json"), []byte(`{
	  "obj_id":100,"title":"X","conv_type":"process","status":"active",
	  "scheme":{"nodes":[]}
	}`), 0644)
	pm, _, err := BuildProjectIndex(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if pm.ConfigReferences != nil {
		t.Errorf("empty allow-list should yield nil ConfigReferences, got %+v", pm.ConfigReferences)
	}
	data, _ := json.Marshal(pm)
	if hasSubstring(string(data), `"config_references"`) {
		t.Errorf("empty section should be omitted (omitempty); found key in JSON: %s", data)
	}
}

func hasSubstring(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// from index_describe_test.go
// ---------------------------------------------------------------------------



func buildAndPersist(t *testing.T, fixture string) string {
	t.Helper()
	src := filepath.Join("testdata", "index_fixtures", fixture)
	dst := t.TempDir()
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	pm, _, err := BuildProjectIndex(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteProjectMap(dst, pm); err != nil {
		t.Fatal(err)
	}
	return dst
}

func callDescribe(t *testing.T, root string, args map[string]interface{}) describeProcessResult {
	t.Helper()
	args["project_path"] = root
	// Use direct path — but describe-process uses confineToWorkdir which
	// rejects absolute paths. Simulate the intended usage: cd into project.
	cur, _ := os.Getwd()
	defer os.Chdir(cur)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	args["project_path"] = "."
	result, isErr := handleDescribeProcess(nil, args)
	if isErr {
		t.Fatalf("describe-process errored: %s", result)
	}
	var r describeProcessResult
	if err := json.Unmarshal([]byte(result), &r); err != nil {
		t.Fatalf("describe-process output not valid JSON: %v\n%s", err, result)
	}
	return r
}

// TestDescribe_ByConvID — the primary resolution path used by corezoid-edit
// when the user provides a numeric ID.
func TestDescribe_ByConvID(t *testing.T) {
	root := buildAndPersist(t, "basic")
	r := callDescribe(t, root, map[string]interface{}{"identifier": "100"})
	if !r.Found {
		t.Fatalf("expected found=true, got %+v", r)
	}
	if r.ConvID != "100" {
		t.Errorf("ConvID = %q, want 100", r.ConvID)
	}
	if r.Title == "" {
		t.Errorf("Title empty; want the payment title")
	}
	if r.CallsInCount != 1 {
		t.Errorf("CallsInCount = %d, want 1 (200 calls 100 via api_get_task)", r.CallsInCount)
	}
	if r.HighFanIn {
		t.Errorf("HighFanIn = true, want false (calls_in=1 <= 5)")
	}
	if r.Stale {
		t.Errorf("Stale=true right after build; want false")
	}
	if r.IndexHash == "" || r.CurrentFileHash == "" {
		t.Errorf("both hashes should be populated: index=%q current=%q", r.IndexHash, r.CurrentFileHash)
	}
	if r.IndexHash != r.CurrentFileHash {
		t.Errorf("hashes differ right after build: index=%q current=%q", r.IndexHash, r.CurrentFileHash)
	}
}

// TestDescribe_ByAlias — @alias resolution, with and without leading @.
func TestDescribe_ByAlias(t *testing.T) {
	root := buildAndPersist(t, "basic")
	for _, id := range []string{"@notify", "notify"} {
		r := callDescribe(t, root, map[string]interface{}{"identifier": id})
		if !r.Found || r.ConvID != "200" {
			t.Errorf("identifier=%q → ConvID=%q, want 200 (found=%v)", id, r.ConvID, r.Found)
		}
	}
}

// TestDescribe_StaleDetection — after the fixture's .conv.json is modified,
// the field arrives as stale=true so the edit skill sees it without any
// extra "remember to check" prompting.
func TestDescribe_StaleDetection(t *testing.T) {
	root := buildAndPersist(t, "basic")
	target := filepath.Join(root, "100_payment.conv.json")
	data, _ := os.ReadFile(target)
	modified := strings.Replace(string(data), `"title": "API: Create Payment"`, `"title": "API: Create Payment v2"`, 1)
	if err := os.WriteFile(target, []byte(modified), 0644); err != nil {
		t.Fatal(err)
	}
	r := callDescribe(t, root, map[string]interface{}{"identifier": "100"})
	if !r.Stale {
		t.Errorf("Stale=false after file modification; expected true (index_hash=%q current_hash=%q)",
			r.IndexHash, r.CurrentFileHash)
	}
}

// TestDescribe_MultipleTitleMatches — ambiguous title fragment returns
// candidates instead of guessing. corezoid-edit will surface the list to the
// user for disambiguation.
func TestDescribe_MultipleTitleMatches(t *testing.T) {
	// Build a two-process fixture where both titles contain the word "user".
	dst := t.TempDir()
	os.WriteFile(filepath.Join(dst, "10_create_user.conv.json"), []byte(`{
	  "obj_id":10,"title":"Create user","conv_type":"process","status":"active",
	  "scheme":{"nodes":[]}
	}`), 0644)
	os.WriteFile(filepath.Join(dst, "11_delete_user.conv.json"), []byte(`{
	  "obj_id":11,"title":"Delete user","conv_type":"process","status":"active",
	  "scheme":{"nodes":[]}
	}`), 0644)
	pm, _, _ := BuildProjectIndex(context.Background(), dst)
	WriteProjectMap(dst, pm)

	r := callDescribe(t, dst, map[string]interface{}{"identifier": "user"})
	if r.Found {
		t.Errorf("expected Found=false with multiple candidates; got %+v", r)
	}
	if len(r.Candidates) < 2 {
		t.Errorf("expected ≥2 candidates for ambiguous 'user'; got %+v", r.Candidates)
	}
}

// TestDescribe_IndexMissingFallback — without an index the tool falls back
// to filesystem scan and reports index_missing=true so the caller can decide
// whether to grep or build first.
func TestDescribe_IndexMissingFallback(t *testing.T) {
	dst := t.TempDir()
	os.WriteFile(filepath.Join(dst, "42_foo.conv.json"), []byte(`{"obj_id":42,"title":"Foo","conv_type":"process","scheme":{"nodes":[]}}`), 0644)
	r := callDescribe(t, dst, map[string]interface{}{"identifier": "42"})
	if !r.IndexMissing {
		t.Errorf("expected index_missing=true (no .corezoid/project-map.json), got %+v", r)
	}
	if len(r.Candidates) == 0 {
		t.Errorf("expected filesystem candidates even without index; got none")
	}
}

// TestDescribe_HighFanInFlag — a process with calls_in > IndexHighFanIn (5)
// gets HighFanIn=true reported to the caller. This is the flag the model
// consumes to trigger the blast-radius warning without a separate MANDATORY
// prompt.
func TestDescribe_HighFanInFlag(t *testing.T) {
	dst := t.TempDir()
	// One popular process (id 1000) called by seven others (2001..2007).
	popular := `{"obj_id":1000,"title":"Popular","conv_type":"process","status":"active","scheme":{"nodes":[]}}`
	os.WriteFile(filepath.Join(dst, "1000_popular.conv.json"), []byte(popular), 0644)
	for i := 2001; i <= 2007; i++ {
		caller := `{"obj_id":` + itoa(i) + `,"title":"Caller","conv_type":"process","status":"active",` +
			`"scheme":{"nodes":[{"id":"cccc00000000000000000` + itoa(i)[len(itoa(i))-3:] + `","title":"C","obj_type":0,` +
			`"condition":{"logics":[{"type":"api_rpc","conv_id":1000}]}}]}}`
		os.WriteFile(filepath.Join(dst, itoa(i)+"_c.conv.json"), []byte(caller), 0644)
	}
	pm, _, err := BuildProjectIndex(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	WriteProjectMap(dst, pm)

	r := callDescribe(t, dst, map[string]interface{}{"identifier": "1000"})
	if !r.HighFanIn {
		t.Errorf("HighFanIn=false with %d callers; expected true (threshold=%d)", r.CallsInCount, IndexHighFanIn)
	}
	if r.CallsInCount != 7 {
		t.Errorf("CallsInCount = %d, want 7", r.CallsInCount)
	}
}

func itoa(n int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
