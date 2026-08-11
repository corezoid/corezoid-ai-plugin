package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type lifecycleFolderState struct {
	title  string
	kind   int
	parent int
}

type lifecycleMock struct {
	processStatus string
	processParent int
	processTitle  string
	projectID     int
	stageID       int
	folders       map[int]*lifecycleFolderState

	statusCalls int
	moveCalls   int
	lastStatus  map[string]interface{}
	lastMove    map[string]interface{}
	statusMoves bool
	objectMoves bool
	afterMove   func()
	statusErr   string
	moveErr     string
}

func newLifecycleMock() *lifecycleMock {
	return &lifecycleMock{
		processStatus: "active",
		processParent: 300,
		processTitle:  "Orders",
		projectID:     100,
		stageID:       200,
		statusMoves:   true,
		objectMoves:   true,
		folders: map[int]*lifecycleFolderState{
			100: {title: "Project", kind: 5, parent: 0},
			110: {title: "Other Project", kind: 5, parent: 0},
			200: {title: "develop", kind: 10, parent: 100},
			210: {title: "production", kind: 10, parent: 100},
			220: {title: "other-stage", kind: 10, parent: 110},
			300: {title: "Source", kind: 0, parent: 200},
			350: {title: "Concurrent", kind: 0, parent: 200},
			400: {title: "Destination", kind: 0, parent: 200},
			500: {title: "Child", kind: 0, parent: 400},
			600: {title: "Prod Folder", kind: 0, parent: 210},
		},
	}
}

func (m *lifecycleMock) fn(ops []map[string]interface{}) interface{} {
	if len(ops) == 0 {
		return lifecycleResponse(map[string]interface{}{"proc": "ok"})
	}
	op := ops[0]
	typ, _ := op["type"].(string)
	obj, _ := op["obj"].(string)
	switch {
	case typ == "show" && obj == "conv":
		return lifecycleResponse(map[string]interface{}{
			"proc":            "ok",
			"obj":             "conv",
			"obj_id":          float64(10),
			"title":           m.processTitle,
			"description":     "Processes orders",
			"status":          m.processStatus,
			"conv_type":       "process",
			"parent_obj_id":   float64(m.processParent),
			"parent_obj_type": "folder",
			"project_id":      float64(m.projectID),
			"stage_id":        float64(m.stageID),
			"immutable":       false,
		})
	case typ == "modify" && obj == "conv":
		m.statusCalls++
		m.lastStatus = cloneLifecycleOp(op)
		if m.statusErr != "" {
			return lifecycleResponse(map[string]interface{}{"proc": "error", "description": m.statusErr})
		}
		old := m.processStatus
		target, _ := op["status"].(string)
		if m.statusMoves {
			m.processStatus = target
		}
		return lifecycleResponse(map[string]interface{}{
			"proc":       "ok",
			"obj":        "conv",
			"obj_id":     float64(10),
			"old_status": lifecycleStatusCode(old),
			"new_status": lifecycleStatusCode(target),
			"is_changed": old != target,
		})
	case typ == "show" && obj == "folder":
		id := int(op["obj_id"].(float64))
		folder, ok := m.folders[id]
		if !ok {
			return lifecycleResponse(map[string]interface{}{"proc": "error", "description": "folder not found"})
		}
		return lifecycleResponse(map[string]interface{}{
			"proc":            "ok",
			"obj":             "folder",
			"obj_id":          float64(id),
			"title":           folder.title,
			"obj_type":        float64(folder.kind),
			"parent_obj_id":   float64(folder.parent),
			"parent_obj_type": "folder",
		})
	case typ == "link" && obj == "folder":
		m.moveCalls++
		m.lastMove = cloneLifecycleOp(op)
		if m.moveErr != "" {
			return lifecycleResponse(map[string]interface{}{"proc": "error", "description": m.moveErr})
		}
		objType, _ := op["obj_type"].(string)
		objID := int(op["obj_id"].(float64))
		destination := int(op["folder_id"].(float64))
		if m.objectMoves {
			if objType == "conv" {
				m.processParent = destination
			} else if folder := m.folders[objID]; folder != nil {
				folder.parent = destination
			}
		}
		if m.afterMove != nil {
			m.afterMove()
		}
		return lifecycleResponse(map[string]interface{}{
			"proc":        "ok",
			"obj_type":    objType,
			"obj_id":      float64(objID),
			"from_folder": op["parent_id"],
			"to_folder":   op["folder_id"],
		})
	default:
		return lifecycleResponse(map[string]interface{}{"proc": "error", "description": fmt.Sprintf("unexpected %s:%s", typ, obj)})
	}
}

func lifecycleResponse(op map[string]interface{}) interface{} {
	return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{op}}
}

func lifecycleStatusCode(status string) float64 {
	switch status {
	case "active":
		return 1
	case "paused":
		return 2
	case "debug":
		return 3
	default:
		return 0
	}
}

func cloneLifecycleOp(op map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(op))
	for key, value := range op {
		out[key] = value
	}
	return out
}

func callLifecycleTool(t *testing.T, m *lifecycleMock, tool string, args map[string]interface{}) (string, bool) {
	t.Helper()
	resetGlobals(t)
	t.Chdir(t.TempDir())
	srv, _ := mockAPIServer(t, m.fn)
	setProjectAuth(t, srv.URL)
	originalAccountURL := accountURL
	accountURL = srv.URL
	t.Cleanup(func() { accountURL = originalAccountURL })
	stageID = 200
	return handleToolCall(context.Background(), tool, args)
}

func TestFolderContainerKinds_CurrentAndLegacy(t *testing.T) {
	tests := []struct {
		objType int
		want    string
	}{
		{objType: 0, want: "folder"},
		{objType: 2, want: "project"},
		{objType: 3, want: "stage"},
		{objType: 5, want: "project"},
		{objType: 10, want: "stage"},
	}
	for _, tt := range tests {
		if got := folderKind(tt.objType); got != tt.want {
			t.Errorf("folderKind(%d) = %q, want %q", tt.objType, got, tt.want)
		}
	}
}

func TestResolveStageIDByFolder_CurrentAndLegacyStageObjType(t *testing.T) {
	for _, objType := range []int{10, 3} {
		t.Run(fmt.Sprintf("obj_type_%d", objType), func(t *testing.T) {
			m := newLifecycleMock()
			m.folders[200].kind = objType
			resetGlobals(t)
			t.Chdir(t.TempDir())
			srv, _ := mockAPIServer(t, m.fn)
			setProjectAuth(t, srv.URL)
			originalAccountURL := accountURL
			accountURL = srv.URL
			t.Cleanup(func() { accountURL = originalAccountURL })

			v := NewValidator(context.Background(), 0)
			got, err := v.ResolveStageIDByFolder(300)
			if err != nil {
				t.Fatalf("ResolveStageIDByFolder returned error: %v", err)
			}
			if got != 200 {
				t.Fatalf("ResolveStageIDByFolder(300) = %d, want stage 200", got)
			}
		})
	}
}

func TestPauseProcess_DryRunDoesNotMutate(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "pause-process", map[string]interface{}{"process_id": 10})
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if m.statusCalls != 0 || m.processStatus != "active" {
		t.Fatalf("dry-run mutated status: calls=%d status=%s", m.statusCalls, m.processStatus)
	}
	for _, want := range []string{"DRY-RUN", "conveyor_is_not_active", `confirm="process#10:active->paused"`} {
		if !strings.Contains(result, want) {
			t.Errorf("dry-run missing %q:\n%s", want, result)
		}
	}
}

func TestPauseProcess_RequiresExactConfirmation(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "pause-process", map[string]interface{}{
		"process_id": 10, "apply": true, "confirm": "process#10:paused->paused",
	})
	if !isErr || !strings.Contains(result, "Confirmation required") {
		t.Fatalf("expected confirmation refusal, got isErr=%v: %s", isErr, result)
	}
	if m.statusCalls != 0 {
		t.Fatal("wrong confirmation reached modify API")
	}
}

func TestPauseProcess_AppliesMinimalStatusOpAndVerifies(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "pause-process", map[string]interface{}{
		"process_id": 10, "apply": true, "confirm": "process#10:active->paused",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if m.processStatus != "paused" || m.statusCalls != 1 {
		t.Fatalf("status not paused: calls=%d status=%s", m.statusCalls, m.processStatus)
	}
	for _, forbidden := range []string{"title", "description"} {
		if _, present := m.lastStatus[forbidden]; present {
			t.Errorf("status op must not overwrite %s: %#v", forbidden, m.lastStatus)
		}
	}
	if !strings.Contains(result, "Already-running or parked tasks were not modified") {
		t.Errorf("success output lost in-flight caveat: %s", result)
	}
}

func TestPauseProcess_AlreadyPausedIsNoOp(t *testing.T) {
	m := newLifecycleMock()
	m.processStatus = "paused"
	result, isErr := callLifecycleTool(t, m, "pause-process", map[string]interface{}{"process_id": 10, "apply": true})
	if isErr || !strings.Contains(result, "already paused") || m.statusCalls != 0 {
		t.Fatalf("expected idempotent no-op, got isErr=%v calls=%d: %s", isErr, m.statusCalls, result)
	}
}

func TestResumeProcess_AppliesAndWarnsTrafficIsImmediate(t *testing.T) {
	m := newLifecycleMock()
	m.processStatus = "paused"
	result, isErr := callLifecycleTool(t, m, "resume-process", map[string]interface{}{
		"process_id": 10, "apply": true, "confirm": "process#10:paused->active",
	})
	if isErr || m.processStatus != "active" {
		t.Fatalf("resume failed isErr=%v status=%s: %s", isErr, m.processStatus, result)
	}
	if !strings.Contains(result, "accept new tasks") {
		t.Errorf("resume output missing immediate-traffic warning: %s", result)
	}
}

func TestPauseProcess_PostVerificationFailureIsError(t *testing.T) {
	m := newLifecycleMock()
	m.statusMoves = false
	result, isErr := callLifecycleTool(t, m, "pause-process", map[string]interface{}{
		"process_id": 10, "apply": true, "confirm": "process#10:active->paused",
	})
	if !isErr || !strings.Contains(result, "verification failed") {
		t.Fatalf("expected verification error, got isErr=%v: %s", isErr, result)
	}
}

func TestPauseProcess_DebugStatusUsesFreshConfirmation(t *testing.T) {
	m := newLifecycleMock()
	m.processStatus = "debug"
	result, isErr := callLifecycleTool(t, m, "pause-process", map[string]interface{}{
		"process_id": 10, "apply": true, "confirm": "process#10:active->paused",
	})
	if !isErr || m.statusCalls != 0 || !strings.Contains(result, `confirm="process#10:debug->paused"`) {
		t.Fatalf("debug status should invalidate old token, isErr=%v calls=%d: %s", isErr, m.statusCalls, result)
	}
}

func TestPauseProcess_ServerErrorIsSurfaced(t *testing.T) {
	m := newLifecycleMock()
	m.statusErr = "Stage is immutable"
	result, isErr := callLifecycleTool(t, m, "pause-process", map[string]interface{}{
		"process_id": 10, "apply": true, "confirm": "process#10:active->paused",
	})
	if !isErr || !strings.Contains(result, m.statusErr) {
		t.Fatalf("status server error not surfaced, isErr=%v: %s", isErr, result)
	}
}

func TestMoveProcess_DryRunDoesNotMutate(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
	})
	if isErr || m.moveCalls != 0 || m.processParent != 300 {
		t.Fatalf("unexpected dry-run result isErr=%v calls=%d parent=%d: %s", isErr, m.moveCalls, m.processParent, result)
	}
	for _, want := range []string{"DRY-RUN", "No copy or deploy", `confirm="process#10:300->400@200:ctx=100/200->100/200"`} {
		if !strings.Contains(result, want) {
			t.Errorf("move preview missing %q:\n%s", want, result)
		}
	}
}

func TestMoveProcess_StaleConfirmationCannotMoveFromNewParent(t *testing.T) {
	m := newLifecycleMock()
	m.processParent = 350
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
		"apply": true, "confirm": "process#10:300->400@200:ctx=100/200->100/200",
	})
	if !isErr || m.moveCalls != 0 {
		t.Fatalf("stale confirmation must block move, got isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
	}
	if !strings.Contains(result, `confirm="process#10:350->400@200:ctx=100/200->100/200"`) {
		t.Errorf("result must expose fresh-parent token: %s", result)
	}
}

func TestMoveProcess_StaleConfirmationCannotUseRelocatedDestination(t *testing.T) {
	m := newLifecycleMock()
	m.folders[400].parent = 210
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
		"allow_cross_stage": true, "apply": true,
		"confirm": "process#10:300->400@200:ctx=100/200->100/200",
	})
	if !isErr || m.moveCalls != 0 || !strings.Contains(result, `confirm="process#10:300->400@210:ctx=100/200->100/210"`) {
		t.Fatalf("relocated destination token should block move, isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
	}
}

func TestMoveProcess_StaleConfirmationCannotUseRecontextualizedDestination(t *testing.T) {
	m := newLifecycleMock()
	m.folders[400].parent = 210
	m.folders[210].parent = 110
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
		"allow_cross_stage": true, "apply": true,
		"confirm": "process#10:300->400@210:ctx=100/200->100/210",
	})
	if !isErr || m.moveCalls != 0 || !strings.Contains(result, `confirm="process#10:300->400@210:ctx=100/200->110/210"`) {
		t.Fatalf("destination context change should invalidate token, isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
	}
}

func TestMoveFolder_StaleConfirmationCannotMoveFromNewParent(t *testing.T) {
	m := newLifecycleMock()
	m.folders[400].parent = 300
	result, isErr := callLifecycleTool(t, m, "move-folder", map[string]interface{}{
		"folder_id": 400, "destination_folder_id": 350,
		"apply": true, "confirm": "folder#400:200->350@200:ctx=100/200->100/200",
	})
	if !isErr || m.moveCalls != 0 || !strings.Contains(result, `confirm="folder#400:300->350@200:ctx=100/200->100/200"`) {
		t.Fatalf("stale folder token should block move, isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
	}
}

func TestMoveProcess_AppliesFreshParentAndPostVerifies(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
		"apply": true, "confirm": "process#10:300->400@200:ctx=100/200->100/200",
	})
	if isErr || m.processParent != 400 || m.moveCalls != 1 {
		t.Fatalf("move failed isErr=%v calls=%d parent=%d: %s", isErr, m.moveCalls, m.processParent, result)
	}
	if int(m.lastMove["parent_id"].(float64)) != 300 || int(m.lastMove["folder_id"].(float64)) != 400 {
		t.Errorf("move op used wrong parents: %#v", m.lastMove)
	}
	if m.lastMove["obj_type"] != "conv" {
		t.Errorf("move-process obj_type=%v, want conv", m.lastMove["obj_type"])
	}
	if !strings.Contains(result, "Object ID is unchanged") || !strings.Contains(result, "Local mirror paths were not moved") {
		t.Errorf("success output missing identity/local-mirror contract: %s", result)
	}
}

func TestMoveProcess_SameParentIsNoOp(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 300, "apply": true,
	})
	if isErr || m.moveCalls != 0 || !strings.Contains(result, "already in destination") {
		t.Fatalf("expected no-op, got isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
	}
}

func TestMoveProcess_CrossStageRequiresSeparateAcknowledgement(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 600,
		"apply": true, "confirm": "process#10:300->600@210:ctx=100/200->100/210",
	})
	if !isErr || m.moveCalls != 0 || !strings.Contains(result, "allow_cross_stage=true") {
		t.Fatalf("cross-stage move should be blocked, isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
	}

	result, isErr = callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 600, "allow_cross_stage": true,
		"apply": true, "confirm": "process#10:300->600@210:ctx=100/200->100/210",
	})
	if isErr || m.processParent != 600 {
		t.Fatalf("acknowledged cross-stage move failed, isErr=%v parent=%d: %s", isErr, m.processParent, result)
	}
}

func TestMoveFolder_RejectsSelfAndDescendantBeforeAPI(t *testing.T) {
	for name, destination := range map[string]int{"self": 400, "descendant": 500} {
		t.Run(name, func(t *testing.T) {
			m := newLifecycleMock()
			result, isErr := callLifecycleTool(t, m, "move-folder", map[string]interface{}{
				"folder_id": 400, "destination_folder_id": destination,
			})
			if !isErr || m.moveCalls != 0 {
				t.Fatalf("cycle must be rejected, isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
			}
			if !strings.Contains(result, "cannot be moved") {
				t.Errorf("missing cycle explanation: %s", result)
			}
		})
	}
}

func TestMoveFolder_RejectsNonFolderContainersWithAccurateKind(t *testing.T) {
	tests := []struct {
		id   int
		kind string
	}{
		{id: 100, kind: "project"},
		{id: 200, kind: "stage"},
		{id: 700, kind: "root"},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			m := newLifecycleMock()
			if tt.kind == "root" {
				m.folders[tt.id] = &lifecycleFolderState{title: "User root", kind: 1, parent: 0}
			}
			result, isErr := callLifecycleTool(t, m, "move-folder", map[string]interface{}{
				"folder_id": tt.id, "destination_folder_id": 300,
			})
			want := fmt.Sprintf("%s containers cannot be reparented by this tool", tt.kind)
			if !isErr || m.moveCalls != 0 || !strings.Contains(result, want) {
				t.Fatalf("container #%d should be rejected as %s, isErr=%v calls=%d: %s", tt.id, tt.kind, isErr, m.moveCalls, result)
			}
		})
	}
}

func TestMoveObject_RejectsMissingDestination(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 999,
	})
	if !isErr || m.moveCalls != 0 || !strings.Contains(result, "folder not found") {
		t.Fatalf("missing destination should fail, isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
	}
}

func TestMoveObject_RejectsCorruptDestinationHierarchy(t *testing.T) {
	m := newLifecycleMock()
	m.folders[400].parent = 500
	m.folders[500].parent = 400
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
	})
	if !isErr || m.moveCalls != 0 || !strings.Contains(result, "hierarchy cycle detected") {
		t.Fatalf("corrupt hierarchy should fail, isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
	}
}

func TestMoveFolder_AppliesAndUsesFolderWireType(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "move-folder", map[string]interface{}{
		"folder_id": 400, "destination_folder_id": 300,
		"apply": true, "confirm": "folder#400:200->300@200:ctx=100/200->100/200",
	})
	if isErr || m.folders[400].parent != 300 {
		t.Fatalf("folder move failed, isErr=%v parent=%d: %s", isErr, m.folders[400].parent, result)
	}
	if m.lastMove["obj_type"] != "folder" {
		t.Errorf("move-folder obj_type=%v, want folder", m.lastMove["obj_type"])
	}
}

func TestMoveFolder_WorkspaceRootIsSupportedButContextGated(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "move-folder", map[string]interface{}{
		"folder_id": 400, "destination_folder_id": 0,
	})
	if isErr || !strings.Contains(result, "workspace root") || !strings.Contains(result, "allow_cross_stage=true") {
		t.Fatalf("root dry-run missing context guard, isErr=%v: %s", isErr, result)
	}

	result, isErr = callLifecycleTool(t, m, "move-folder", map[string]interface{}{
		"folder_id": 400, "destination_folder_id": 0, "allow_cross_stage": true,
		"apply": true, "confirm": "folder#400:200->0@0:ctx=100/200->0/0",
	})
	if isErr || m.folders[400].parent != 0 {
		t.Fatalf("root move failed, isErr=%v parent=%d: %s", isErr, m.folders[400].parent, result)
	}
}

func TestMoveObject_ServerErrorIsSurfaced(t *testing.T) {
	m := newLifecycleMock()
	m.moveErr = "Move parent folder to child is not allowed"
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
		"apply": true, "confirm": "process#10:300->400@200:ctx=100/200->100/200",
	})
	if !isErr || !strings.Contains(result, m.moveErr) {
		t.Fatalf("server error not surfaced, isErr=%v: %s", isErr, result)
	}
}

func TestMoveObject_PostVerificationFailureIsError(t *testing.T) {
	m := newLifecycleMock()
	m.objectMoves = false
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
		"apply": true, "confirm": "process#10:300->400@200:ctx=100/200->100/200",
	})
	if !isErr || !strings.Contains(result, "verification failed") {
		t.Fatalf("expected post-verification error, isErr=%v: %s", isErr, result)
	}
}

func TestMoveObject_DestinationContextChangeAfterRequestIsReported(t *testing.T) {
	m := newLifecycleMock()
	m.afterMove = func() { m.folders[400].parent = 210 }
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
		"apply": true, "confirm": "process#10:300->400@200:ctx=100/200->100/200",
	})
	if !isErr || m.processParent != 400 || !strings.Contains(result, "destination changed during the operation") || !strings.Contains(result, "move already completed") {
		t.Fatalf("concurrent destination move must report uncertain changed context, isErr=%v parent=%d: %s", isErr, m.processParent, result)
	}
}

func TestMoveObject_DestinationDisappearsAfterRequestIsReported(t *testing.T) {
	m := newLifecycleMock()
	m.afterMove = func() { delete(m.folders, 400) }
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": 400,
		"apply": true, "confirm": "process#10:300->400@200:ctx=100/200->100/200",
	})
	if !isErr || m.processParent != 400 || !strings.Contains(result, "destination post-verification failed") || !strings.Contains(result, "may already have completed") {
		t.Fatalf("missing destination after move must report uncertain state, isErr=%v parent=%d: %s", isErr, m.processParent, result)
	}
}

func TestMoveDestination_RejectsNegativeID(t *testing.T) {
	m := newLifecycleMock()
	result, isErr := callLifecycleTool(t, m, "move-process", map[string]interface{}{
		"process_id": 10, "destination_folder_id": -1,
	})
	if !isErr || !strings.Contains(result, "must be zero") || m.moveCalls != 0 {
		t.Fatalf("negative destination should fail, isErr=%v calls=%d: %s", isErr, m.moveCalls, result)
	}
}
