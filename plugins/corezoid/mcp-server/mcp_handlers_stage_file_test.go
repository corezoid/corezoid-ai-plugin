package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// ---- create-stage ----------------------------------------------------------

func TestCreateStage_OK(t *testing.T) {
	resetGlobals(t)
	var captured []map[string]interface{}
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return wrapOp(map[string]interface{}{"proc": "ok", "obj": "stage", "obj_id": float64(501), "short_name": "s1"})
	})
	setProjectAuth(t, srv.URL)

	res, isErr := handleToolCall(context.Background(), "cz-structure", map[string]interface{}{
		"action": "create-stage",
		"args":   map[string]interface{}{"project_id": float64(500), "title": "1", "short_name": "s1"},
	})
	if isErr {
		t.Fatalf("unexpected error: %s", res)
	}
	if !strings.Contains(res, "stage_id=501") || !strings.Contains(res, "mutable") {
		t.Fatalf("result does not name the stage: %s", res)
	}
	if len(captured) != 1 {
		t.Fatalf("want 1 op, got %d", len(captured))
	}
	op := captured[0]
	if op["type"] != "create" || op["obj"] != "stage" || op["title"] != "1" || op["short_name"] != "s1" {
		t.Fatalf("wrong op: %v", op)
	}
	if op["project_id"].(float64) != 500 || op["company_id"] != "i260836082" || op["immutable"] != false {
		t.Fatalf("wrong scope/flags: %v", op)
	}
}

func TestCreateStage_Immutable(t *testing.T) {
	resetGlobals(t)
	var captured []map[string]interface{}
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return wrapOp(map[string]interface{}{"proc": "ok", "obj_id": float64(5)})
	})
	setProjectAuth(t, srv.URL)
	res, isErr := handleToolCall(context.Background(), "create-stage", map[string]interface{}{
		"project_id": float64(1), "title": "prod", "immutable": true,
	})
	if isErr || captured[0]["immutable"] != true || !strings.Contains(res, "immutable") {
		t.Fatalf("isErr=%v res=%s op=%v", isErr, res, captured)
	}
	if _, ok := captured[0]["short_name"]; ok {
		t.Fatalf("short_name must be omitted when not given: %v", captured[0])
	}
}

func TestCreateStage_MissingArgsAndAPIError(t *testing.T) {
	resetGlobals(t)
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return wrapOp(map[string]interface{}{"proc": "error", "description": "Access denied"})
	})
	setProjectAuth(t, srv.URL)
	if res, isErr := handleToolCall(context.Background(), "create-stage", map[string]interface{}{"title": "x"}); !isErr {
		t.Fatalf("missing project_id must fail: %s", res)
	}
	if res, isErr := handleToolCall(context.Background(), "create-stage", map[string]interface{}{"project_id": float64(1)}); !isErr {
		t.Fatalf("missing title must fail: %s", res)
	}
	res, isErr := handleToolCall(context.Background(), "create-stage", map[string]interface{}{"project_id": float64(1), "title": "x"})
	if !isErr || !strings.Contains(res, "Access denied") {
		t.Fatalf("API error must surface: isErr=%v %s", isErr, res)
	}
}

// ---- upload-scheme ---------------------------------------------------------

// uploadServer records the multipart upload and answers like /api/2/upload.
type uploadCapture struct {
	path, auth string
	fields     map[string]string
	fileName   string
	fileBody   string
}

func newUploadServer(t *testing.T, answer map[string]interface{}) (*httptest.Server, *uploadCapture) {
	t.Helper()
	c := &uploadCapture{fields: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.path = r.URL.Path
		c.auth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			for k, v := range r.MultipartForm.Value {
				c.fields[k] = v[0]
			}
			if fh, ok := r.MultipartForm.File["scheme"]; ok {
				c.fileName = fh[0].Filename
				f, _ := fh[0].Open()
				b, _ := io.ReadAll(f)
				c.fileBody = string(b)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(answer) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func writeSchemeFile(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.WriteFile("stage_1.zip", []byte("PK-fake-zip"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUploadScheme_OK(t *testing.T) {
	resetGlobals(t)
	writeSchemeFile(t)
	srv, c := newUploadServer(t, map[string]interface{}{
		"request_proc": "ok",
		"ops":          []interface{}{map[string]interface{}{"proc": "ok", "obj": "obj_scheme", "scheme_id": "sch-0001", "scheme_type": "stage"}},
	})
	setProjectAuth(t, srv.URL)

	res, isErr := handleToolCall(context.Background(), "cz-structure", map[string]interface{}{
		"action": "upload-scheme",
		"args":   map[string]interface{}{"file_path": "stage_1.zip", "target_stage_id": float64(502), "project_id": float64(500)},
	})
	if isErr {
		t.Fatalf("unexpected error: %s", res)
	}
	if !strings.Contains(res, "scheme_id=sch-0001") || !strings.Contains(res, "source_scheme_id") {
		t.Fatalf("result must hand back scheme_id and the next step: %s", res)
	}
	if c.path != "/api/2/upload" || c.auth != "Simulator test-token" {
		t.Fatalf("wrong endpoint/auth: %s %q", c.path, c.auth)
	}
	want := map[string]string{"type": "merge", "obj": "obj_scheme", "obj_id": "502", "obj_type": "stage", "apply_mode": "false", "company_id": "i260836082", "project_id": "500"}
	for k, v := range want {
		if c.fields[k] != v {
			t.Errorf("field %s = %q, want %q", k, c.fields[k], v)
		}
	}
	if c.fileName != "stage_1.zip" || c.fileBody != "PK-fake-zip" {
		t.Fatalf("file not sent as 'scheme': %q %q", c.fileName, c.fileBody)
	}
}

func TestUploadScheme_ServerError(t *testing.T) {
	resetGlobals(t)
	writeSchemeFile(t)
	srv, _ := newUploadServer(t, map[string]interface{}{
		"request_proc": "ok",
		"ops":          []interface{}{map[string]interface{}{"proc": "error", "description": "scheme or scheme_id isn't found"}},
	})
	setProjectAuth(t, srv.URL)
	res, isErr := handleToolCall(context.Background(), "upload-scheme", map[string]interface{}{"file_path": "stage_1.zip", "target_stage_id": float64(2)})
	if !isErr || !strings.Contains(res, "scheme or scheme_id isn't found") {
		t.Fatalf("server error must surface: isErr=%v %s", isErr, res)
	}
}

func TestUploadScheme_BadPaths(t *testing.T) {
	resetGlobals(t)
	writeSchemeFile(t)
	srv, c := newUploadServer(t, map[string]interface{}{"request_proc": "ok"})
	setProjectAuth(t, srv.URL)
	for name, args := range map[string]map[string]interface{}{
		"missing file_path": {"target_stage_id": float64(2)},
		"escapes workdir":   {"file_path": "../x.zip", "target_stage_id": float64(2)},
		"not found":         {"file_path": "nope.zip", "target_stage_id": float64(2)},
		"directory":         {"file_path": ".", "target_stage_id": float64(2)},
		"missing target":    {"file_path": "stage_1.zip"},
	} {
		if res, isErr := handleToolCall(context.Background(), "upload-scheme", args); !isErr {
			t.Errorf("%s: expected error, got %s", name, res)
		}
	}
	if c.path != "" {
		t.Fatalf("no request may be sent for invalid args, got %s", c.path)
	}
}

func TestUploadScheme_APIKeySessionRefused(t *testing.T) {
	_, e := mockAPIServer(t, func([]map[string]interface{}) interface{} { return nil })
	e.Token = ""
	e.APILogin, e.APISecret = "1", "s"
	e.Ctx = context.Background()
	if _, err := e.UploadSchemeForMerge("x.zip", 1, 0); err == nil || !strings.Contains(err.Error(), "OAuth") {
		t.Fatalf("API-key session must be refused with a clear reason, got %v", err)
	}
}

// ---- deploy-stage from an uploaded scheme ----------------------------------

func TestDeployStage_FromScheme(t *testing.T) {
	var seenOps []map[string]interface{}
	m := deployMock{immutable: true, title: "2", diff: []map[string]interface{}{
		{"obj_id": float64(9001), "title": "sample", "obj_type": "conv", "__status": "added"},
	}}
	fn := func(ops []map[string]interface{}) interface{} {
		seenOps = append(seenOps, ops...)
		return m.fn(ops)
	}
	args := map[string]interface{}{"source_scheme_id": "abc123"}
	res, isErr := callDeployWithFnNoSource(t, fn, args)
	if isErr {
		t.Fatalf("dry-run failed: %s", res)
	}
	if !strings.Contains(res, "uploaded scheme abc123") || !strings.Contains(res, `confirm="scheme:abc123->300"`) {
		t.Fatalf("dry-run must name the scheme source and its confirm token: %s", res)
	}
	for _, op := range seenOps {
		if op["type"] == "show" && op["obj_id"].(float64) != 300 {
			t.Fatalf("only the target stage may be checked for a scheme source, saw %v", op)
		}
		if op["type"] == "compare" && (op["obj_to_id"] != "abc123" || op["obj_to_type"] != "scheme") {
			t.Fatalf("compare must point at the scheme: %v", op)
		}
	}

	// A stage-style confirm must not apply a scheme merge.
	args = map[string]interface{}{"source_scheme_id": "abc123", "apply": true, "confirm": "200->300"}
	if res, isErr := callDeployWithFnNoSource(t, fn, args); !isErr || !strings.Contains(res, "confirmation required") {
		t.Fatalf("wrong confirm must block: %s", res)
	}

	seenOps = nil
	args = map[string]interface{}{"source_scheme_id": "abc123", "apply": true, "confirm": "scheme:abc123->300"}
	res, _ = callDeployWithFnNoSource(t, fn, args)
	merged := false
	for _, op := range seenOps {
		if op["type"] == "merge" {
			merged = true
			if op["obj_to_id"] != "abc123" || op["obj_to_type"] != "scheme" || op["apply_mode"] != true {
				t.Fatalf("merge must apply the scheme: %v", op)
			}
		}
	}
	if !merged {
		t.Fatalf("merge not sent: %s", res)
	}
}

func TestDeployStage_SchemeAndStageBoth(t *testing.T) {
	res, isErr := callDeploy(t, deployMock{immutable: true}, map[string]interface{}{"source_scheme_id": "abc"})
	if !isErr || !strings.Contains(res, "not both") {
		t.Fatalf("both sources must be refused: %s", res)
	}
}

// callDeployWithFnNoSource is callDeployWithFn without the default
// source_stage_id, for scheme-sourced deploys.
func callDeployWithFnNoSource(t *testing.T, fn func([]map[string]interface{}) interface{}, args map[string]interface{}) (string, bool) {
	t.Helper()
	resetGlobals(t)
	srv, _ := mockAPIServer(t, fn)
	setProjectAuth(t, srv.URL)
	base := map[string]interface{}{"project_id": float64(100), "target_stage_id": float64(300), "company_id": "i1"}
	for k, v := range args {
		base[k] = v
	}
	return handleToolCall(context.Background(), "deploy-stage", base)
}
