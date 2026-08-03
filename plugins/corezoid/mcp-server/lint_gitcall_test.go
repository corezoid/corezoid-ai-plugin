package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	gitCallStartID = "aaaaaaaaaaaaaaaaaaaaaaaa"
	gitCallWorkID  = "bbbbbbbbbbbbbbbbbbbbbbbb"
	gitCallDoneID  = "cccccccccccccccccccccccc"
	gitCallErrorID = "dddddddddddddddddddddddd"
)

// writeGitConv writes a schema-valid Start -> Work -> Done process whose middle
// node carries a logic of the given type ("git_call", "api_git", or api_code).
func writeGitConv(t *testing.T, logicType string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "123_git.conv.json")
	logic := `{"type":"api_code","lang":"js","src":"data.result=true;","err_node_id":"` + gitCallErrorID + `"}`
	if logicType == "git_call" || logicType == "api_git" {
		logic = `{"type":"` + logicType + `","version":2,"lang":"js","code":"module.exports=(d)=>d;","err_node_id":"` + gitCallErrorID + `"}`
	}
	conv := `{"conv_type":"process","obj_id":123,"obj_type":1,"ref_mask":true,"title":"t","status":"active","params":[],"scheme":{"nodes":[
	 {"id":"` + gitCallStartID + `","obj_type":1,"title":"Start","x":100,"y":100,"condition":{"logics":[{"type":"go","to_node_id":"` + gitCallWorkID + `"}],"semaphors":[]}},
	 {"id":"` + gitCallWorkID + `","obj_type":0,"title":"Work","x":100,"y":260,"condition":{"logics":[` + logic + `,
	   {"type":"go","to_node_id":"` + gitCallDoneID + `"}],"semaphors":[]}},
	 {"id":"` + gitCallDoneID + `","obj_type":2,"title":"Done","x":100,"y":420,"condition":{"logics":[],"semaphors":[]}},
	 {"id":"` + gitCallErrorID + `","obj_type":2,"title":"Error","x":420,"y":260,"condition":{"logics":[],"semaphors":[]}}
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
	if !res.SchemaValid {
		t.Fatalf("git_call fixture must remain schema-valid: %s", res.SchemaError)
	}
	if res.GitCallUsages[0].ID != gitCallWorkID {
		t.Fatalf("expected node %s flagged, got %q", gitCallWorkID, res.GitCallUsages[0].ID)
	}
	if !strings.Contains(res.GitCallUsages[0].Issue, "approximately 60s") {
		t.Fatalf("advisory should state the observed execution deadline, got: %s", res.GitCallUsages[0].Issue)
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
	if !res.SchemaValid {
		t.Fatalf("api_git fixture must remain schema-valid: %s", res.SchemaError)
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
	if !res.SchemaValid {
		t.Fatalf("api_code fixture must remain schema-valid: %s", res.SchemaError)
	}
}

func TestFormatLintResult_GitCallUsageIsClearlyAdvisory(t *testing.T) {
	result := &LintResult{
		ProcessTitle: "Git Call Process",
		TotalNodes:   3,
		SchemaValid:  true,
		GitCallUsages: []GitCallUsage{{
			ID:    gitCallWorkID,
			Title: "Parse document",
			Issue: "selection rule",
		}},
	}

	out := FormatLintResult(result)
	for _, want := range []string{
		"GIT_CALL USAGE (1)",
		"advisory",
		gitCallWorkID,
		"Total issues: 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected formatted lint output to contain %q, got:\n%s", want, out)
		}
	}
}
