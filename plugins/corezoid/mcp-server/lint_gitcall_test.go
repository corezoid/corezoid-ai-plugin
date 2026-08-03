package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGitConv writes a minimal Start -> GitCall -> Done process whose middle
// node carries a logic of the given type ("git_call", "api_git", or a
// non-git type for the negative case), and returns its path.
func writeGitConv(t *testing.T, logicType string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "1_git.conv.json")
	conv := `{"conv_type":"process","obj_id":1,"obj_type":1,"ref_mask":true,"title":"t","status":"active","params":[],"scheme":{"nodes":[
	 {"id":"start1","obj_type":1,"title":"Start","x":100,"y":100,"condition":{"logics":[{"type":"go","to_node_id":"work01"}],"semaphors":[]}},
	 {"id":"work01","obj_type":0,"title":"Work","x":100,"y":260,"condition":{"logics":[
	   {"type":"` + logicType + `","version":2,"lang":"js","code":"module.exports=(d)=>d;","err_node_id":"error1"},
	   {"type":"go","to_node_id":"done01"}],"semaphors":[]}},
	 {"id":"done01","obj_type":2,"title":"Done","x":100,"y":420,"condition":{"logics":[],"semaphors":[]}},
	 {"id":"error1","obj_type":2,"title":"Error","x":420,"y":260,"condition":{"logics":[],"semaphors":[]}}
	],"web_settings":[[],[]]}}`
	if err := os.WriteFile(p, []byte(conv), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLint_GitCallUsage_FlaggedAsAdvisory(t *testing.T) {
	res, err := lintProcess(writeGitConv(t, "git_call"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GitCallUsages) != 1 {
		t.Fatalf("expected 1 git_call usage, got %d: %+v", len(res.GitCallUsages), res.GitCallUsages)
	}
	if res.GitCallUsages[0].ID != "work01" {
		t.Fatalf("expected node work01 flagged, got %q", res.GitCallUsages[0].ID)
	}
	if !strings.Contains(res.GitCallUsages[0].Issue, "hard execution timeout") {
		t.Fatalf("advisory issue text should state git_call's hard execution timeout, got: %s", res.GitCallUsages[0].Issue)
	}
}

func TestLint_GitCallUsage_ApiGitAlias(t *testing.T) {
	// the platform also stores the logic type as "api_git" — it must be flagged too
	res, err := lintProcess(writeGitConv(t, "api_git"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GitCallUsages) != 1 {
		t.Fatalf("expected 1 git_call usage for api_git, got %d", len(res.GitCallUsages))
	}
}

func TestLint_GitCallUsage_NoneWhenAbsent(t *testing.T) {
	// a plain api_code node must NOT be flagged as a git_call usage
	res, err := lintProcess(writeGitConv(t, "api_code"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GitCallUsages) != 0 {
		t.Fatalf("api_code must not be flagged as git_call, got %d", len(res.GitCallUsages))
	}
}
