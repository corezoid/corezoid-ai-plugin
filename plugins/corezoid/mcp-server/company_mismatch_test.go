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
		"Object belongs to another company",
	} {
		if companyMismatchHint(desc, "i1000000", "i1000000") == "" {
			t.Errorf("expected a hint for %q", desc)
		}
	}
	for _, desc := range []string{
		"Value is not valid / company_id",
		"access denied",
		"task not found",
		"ref already exists",
		// A validation error and refusals to move or copy across companies:
		// logging into another workspace fixes none of them.
		"Wrong company_id",
		"Cannot move object to another company",
		"Cannot copy process to a different company",
	} {
		if h := companyMismatchHint(desc, "i1000000", "i1000000"); h != "" {
			t.Errorf("unexpected hint for %q: %s", desc, h)
		}
	}
}

func TestCompanyMismatchHint_SkipsCurrentWorkspaceID(t *testing.T) {
	h := companyMismatchHint("company mismatch: expected i111111111, got i222222222", "i111111111", "i111111111")
	if !strings.Contains(h, "object's workspace: i222222222") {
		t.Errorf("the current workspace must not be reported as the object's:\n%s", h)
	}
}

// Hosted mode has no login tool: the workspace comes from the per-call scope
// argument, so the hint must name scope, not login or a reconnect.
func TestCompanyMismatchHint_HostedPointsToScope(t *testing.T) {
	prev := hostedMode
	hostedMode = true
	t.Cleanup(func() { hostedMode = prev })

	h := companyMismatchHint("company mismatch: conv 1 belongs to company i222222222", "i111111111", "i111111111")
	if !strings.Contains(h, `scope: {"company_id": "i222222222"`) {
		t.Errorf("hosted hint must name scope.company_id of the object:\n%s", h)
	}
	if strings.Contains(h, "login") || strings.Contains(h, "Reconnect") {
		t.Errorf("hosted hint must not suggest login or reconnecting:\n%s", h)
	}

	h = companyMismatchHint("company mismatch", "i111111111", "i111111111")
	for _, want := range []string{"list-workspaces", "list-stages", "scope:"} {
		if !strings.Contains(h, want) {
			t.Errorf("hosted hint without an id missing %q:\n%s", want, h)
		}
	}
}

// An op sent with an explicit company_id (deploy-stage, discovery tools) is
// scoped by that argument, not by the session: the hint reports the company the
// op was sent with and says to change the argument, since login would not.
func TestCheckError_CompanyMismatchNamesRequestCompany(t *testing.T) {
	e := &Executor{WorkspaceID: "i111111111"}
	rsp := map[string]interface{}{
		"request_proc": "ok",
		"ops": []interface{}{
			map[string]interface{}{"proc": "error", "description": "Company mismatch: stage belongs to company i333333333"},
		},
	}
	err := e.checkError(rsp, []map[string]any{{"company_id": "i222222222"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"current workspace: i222222222", "object's workspace: i333333333", `company_id="i333333333"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "call login") {
		t.Errorf("login does not change an explicit company_id:\n%s", msg)
	}
}

// Viewing projects, folders and processes that live in another workspace
// returned the raw API refusal, and sessions spent several calls on other
// workspaces and argument combinations before logging in elsewhere. Every
// cz-structure view must carry the hint, whichever shape the refusal comes in:
// an op description, per-object errors, or a failed request envelope.
func TestCzStructure_CompanyMismatchNamesWorkspaceAndLogin(t *testing.T) {
	const desc = "Company mismatch: conv 12345 belongs to company i999000111"
	shapes := map[string]map[string]interface{}{
		"op description": {
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "error", "description": desc}},
		},
		"op errors": {
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":   "error",
				"errors": map[string]interface{}{"12345": []interface{}{desc}},
			}},
		},
		"request envelope": {"request_proc": "error", "description": desc},
	}
	actions := map[string]map[string]interface{}{
		"list-projects": {},
		"show-project":  {"project_id": float64(1)},
		"list-stages":   {"project_id": float64(1)},
		"list-folders":  {"folder_id": float64(1)},
		"show-folder":   {"folder_id": float64(1)},
		"show-process":  {"process_id": float64(12345)},
	}
	for shape, rsp := range shapes {
		for action, args := range actions {
			t.Run(shape+"/"+action, func(t *testing.T) {
				resetGlobals(t)
				srv, _ := mockAPIServer(t, func([]map[string]interface{}) interface{} { return rsp })
				setProjectAuth(t, srv.URL)
				origAccount := accountURL
				accountURL = srv.URL
				t.Cleanup(func() { accountURL = origAccount })
				stageID = 20

				res, isErr := handleToolCall(context.Background(), "cz-structure", map[string]interface{}{
					"action": action,
					"args":   args,
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
			})
		}
	}
}
