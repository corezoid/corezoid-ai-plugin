package main

import (
	"context"
	"strings"
	"testing"
)

// companyMismatchServer answers every op with the API's refusal of an object
// that lives in another company.
func companyMismatchServer(t *testing.T, desc string) {
	t.Helper()
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{"proc": "error", "description": desc},
			},
		}
	})
	setProjectAuth(t, srv.URL)
}

// Listing the tasks of a process in another workspace used to return only the
// raw "company mismatch" text, and the model spent several calls working out
// that the session was logged into the wrong workspace. The error must say so
// and name login as the fix, with the object's workspace when the API gives it.
func TestListNodeTasks_CompanyMismatchNamesWorkspaceAndLogin(t *testing.T) {
	resetGlobals(t)
	companyMismatchServer(t, "Company mismatch: conv 12345 belongs to company i999000111")

	res, isErr := handleListNodeTasks(context.Background(), map[string]interface{}{
		"process_id": float64(12345),
		"node_id":    "aaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if !isErr {
		t.Fatalf("expected an error, got: %s", res)
	}
	for _, want := range []string{
		"Company mismatch",
		"different workspace",
		"current workspace: i260836082",
		"object's workspace: i999000111",
		`login with workspace_id="i999000111"`,
	} {
		if !strings.Contains(res, want) {
			t.Errorf("error missing %q:\n%s", want, res)
		}
	}
}

func TestShowTask_CompanyMismatchWithoutIDPointsToListWorkspaces(t *testing.T) {
	resetGlobals(t)
	companyMismatchServer(t, "Object does not belong to this company")

	res, isErr := handleShowTask(context.Background(), map[string]interface{}{
		"process_id": float64(12345),
		"task_id":    "t1",
	})
	if !isErr {
		t.Fatalf("expected an error, got: %s", res)
	}
	for _, want := range []string{"current workspace: i260836082", "list-workspaces", "call login"} {
		if !strings.Contains(res, want) {
			t.Errorf("error missing %q:\n%s", want, res)
		}
	}
	if strings.Contains(res, "object's workspace") {
		t.Errorf("no workspace id in the API text, so none should be claimed:\n%s", res)
	}
}

func TestCompanyMismatchHint_Matching(t *testing.T) {
	for _, desc := range []string{
		"company mismatch",
		"Company id mismatch",
		"company_id does not match",
		"Object conv 1 does not belong to company i123456789",
		"process is in another company",
	} {
		if companyMismatchHint(desc, "i1000000") == "" {
			t.Errorf("expected a hint for %q", desc)
		}
	}
	for _, desc := range []string{
		"Value is not valid / company_id",
		"access denied",
		"task not found",
		"ref already exists",
	} {
		if h := companyMismatchHint(desc, "i1000000"); h != "" {
			t.Errorf("unexpected hint for %q: %s", desc, h)
		}
	}
}

func TestCompanyMismatchHint_SkipsCurrentWorkspaceID(t *testing.T) {
	h := companyMismatchHint("company mismatch: expected i111111111, got i222222222", "i111111111")
	if !strings.Contains(h, "object's workspace: i222222222") {
		t.Errorf("the current workspace must not be reported as the object's:\n%s", h)
	}
}
