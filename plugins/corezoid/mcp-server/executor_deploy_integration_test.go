package main

import (
	"context"
	"os"
	"strconv"
	"testing"
)

// TestDeployStageIntegration exercises the real compare/merge admin endpoints and
// the progress WebSocket against a live Corezoid workspace. It is skipped unless
// COREZOID_DEPLOY_IT=1 and the connection + sandbox env vars are provided:
//
//	COREZOID_DEPLOY_IT=1
//	ACCESS_TOKEN=...            Simulator token
//	COREZOID_API_URL=https://admin.corezoid.com
//	WORKSPACE_ID=...
//	COREZOID_DEPLOY_PROJECT=... project id containing both stages
//	COREZOID_DEPLOY_SRC=...     source (develop) stage id
//	COREZOID_DEPLOY_TGT=...     target (production, immutable) stage id
//
// Use a throwaway sandbox project — it deploys for real.
func TestDeployStageIntegration(t *testing.T) {
	if os.Getenv("COREZOID_DEPLOY_IT") != "1" {
		t.Skip("set COREZOID_DEPLOY_IT=1 (and ACCESS_TOKEN/COREZOID_API_URL/WORKSPACE_ID/COREZOID_DEPLOY_PROJECT/SRC/TGT) to run the live deploy test")
	}
	token := os.Getenv("ACCESS_TOKEN")
	apiURL := os.Getenv("COREZOID_API_URL")
	ws := os.Getenv("WORKSPACE_ID")
	proj := atoiEnv(t, "COREZOID_DEPLOY_PROJECT")
	src := atoiEnv(t, "COREZOID_DEPLOY_SRC")
	tgt := atoiEnv(t, "COREZOID_DEPLOY_TGT")
	if token == "" || apiURL == "" {
		t.Fatal("ACCESS_TOKEN and COREZOID_API_URL are required")
	}
	v := &Executor{Ctx: context.Background(), Token: token, APIUrl: apiURL, WorkspaceID: ws}

	// The target must be immutable — Corezoid only merges into immutable stages.
	imm, _, title, _, err := v.stageInfo(tgt, proj)
	if err != nil {
		t.Fatalf("stageInfo(target): %v", err)
	}
	t.Logf("target stage %d (%q) immutable=%v", tgt, title, imm)
	if !imm {
		t.Fatalf("target stage %d must be immutable to be a deploy target", tgt)
	}

	// compare target vs source.
	cmp, err := v.req("compare", []map[string]any{{
		"type": "compare", "obj": "obj_scheme", "obj_id": tgt, "obj_type": "stage",
		"obj_to_id": src, "obj_to_type": "stage", "diff_status": true,
		"project_id": proj, "company_id": ws,
	}})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	list, derr := deployDiffList(cmp)
	if derr != "" {
		t.Fatalf("compare op: %s", derr)
	}
	t.Logf("diff has %d object(s)", len(list))

	// merge (deploy) + watch the progress WebSocket to completion.
	mrg, err := v.req("merge", []map[string]any{{
		"type": "merge", "obj": "obj_scheme", "obj_id": tgt, "obj_type": "stage",
		"obj_to_id": src, "obj_to_type": "stage", "apply_mode": true, "company_id": ws,
	}})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if e := deployOpProc(mrg); e != "" {
		t.Fatalf("merge op: %s", e)
	}
	if hash := deployMergeHash(mrg); hash != "" {
		progress, werr := v.monitorDeployProgress(hash)
		t.Logf("merge progress:\n%s", progress)
		if werr != nil {
			t.Fatalf("monitorDeployProgress: %v", werr)
		}
	}

	// After merge the stages should be in sync.
	cmp2, err := v.req("compare", []map[string]any{{
		"type": "compare", "obj": "obj_scheme", "obj_id": tgt, "obj_type": "stage",
		"obj_to_id": src, "obj_to_type": "stage", "diff_status": true,
		"project_id": proj, "company_id": ws,
	}})
	if err != nil {
		t.Fatalf("compare after: %v", err)
	}
	list2, _ := deployDiffList(cmp2)
	changed := 0
	for _, it := range list2 {
		if s, _ := it["__status"].(string); s != "" {
			changed++
		}
	}
	if changed != 0 {
		t.Errorf("after deploy expected no changed objects, got %d", changed)
	}
}

// TestMergeIntoMutableFailsIntegration confirms the "merge only in immutable
// stage" constraint against the live API. Requires the same env, plus it will
// temporarily flip the TARGET to mutable — use a throwaway sandbox.
func TestMergeIntoMutableFailsIntegration(t *testing.T) {
	if os.Getenv("COREZOID_DEPLOY_IT") != "1" {
		t.Skip("set COREZOID_DEPLOY_IT=1 to run")
	}
	token := os.Getenv("ACCESS_TOKEN")
	apiURL := os.Getenv("COREZOID_API_URL")
	ws := os.Getenv("WORKSPACE_ID")
	proj := atoiEnv(t, "COREZOID_DEPLOY_PROJECT")
	src := atoiEnv(t, "COREZOID_DEPLOY_SRC")
	tgt := atoiEnv(t, "COREZOID_DEPLOY_TGT")
	v := &Executor{Ctx: context.Background(), Token: token, APIUrl: apiURL, WorkspaceID: ws}

	_, _, title, short, err := v.stageInfo(tgt, proj)
	if err != nil {
		t.Fatalf("stageInfo: %v", err)
	}
	if err := v.setStageImmutable(tgt, proj, title, short, false); err != nil {
		t.Fatalf("make mutable: %v", err)
	}
	t.Cleanup(func() { _ = v.setStageImmutable(tgt, proj, title, short, true) }) // restore

	mrg, err := v.req("merge", []map[string]any{{
		"type": "merge", "obj": "obj_scheme", "obj_id": tgt, "obj_type": "stage",
		"obj_to_id": src, "obj_to_type": "stage", "apply_mode": true, "company_id": ws,
	}})
	if err == nil {
		if e := deployOpProc(mrg); e == "" {
			t.Fatalf("expected merge into mutable target to fail")
		}
	}
}

func atoiEnv(t *testing.T, k string) int {
	t.Helper()
	n, _ := strconv.Atoi(os.Getenv(k))
	return n
}
