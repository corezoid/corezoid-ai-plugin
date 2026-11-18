package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// uploadSchemeResult is what /api/2/upload answers for a scheme file uploaded
// for a merge: the server keeps the parsed file and hands back its id, which
// /api/2/compare and /api/2/merge then take as obj_to_id with
// obj_to_type "scheme".
type uploadSchemeResult struct {
	SchemeID   string
	SchemeType string // what the file holds: "stage", "folder", "conv", …
}

// UploadSchemeForMerge uploads an exported scheme file (the .zip or .json the
// Corezoid export / pull produces) as the source of a merge INTO targetStage.
//
// It is the first half of the UI's "Merge from file" on a stage:
//
//	POST /api/2/upload  multipart: scheme=<file>, type=merge, obj=obj_scheme,
//	                    obj_id=<target stage>, obj_type=stage, company_id,
//	                    project_id, apply_mode=false
//	→ {"ops":[{"proc":"ok","scheme_id":"…","scheme_type":"stage"}]}
//
// Nothing changes on the server yet: the merge itself is deploy-stage with
// source_scheme_id. The upload is multipart, so it cannot go through req()
// (JSON body); auth is the same Simulator token header.
//
// Only OAuth (token) sessions are supported. The API-key signature covers the
// raw request body, and Corezoid answers an API-key-signed multipart upload
// with "access denied", so an API-key session is refused up front with a
// clear message instead of a confusing server error.
func (v *Executor) UploadSchemeForMerge(filePath string, targetStage, projectID int) (uploadSchemeResult, error) {
	var res uploadSchemeResult
	if v.Token == "" {
		return res, fmt.Errorf("upload needs an OAuth session (login without api_login/api_secret): Corezoid rejects API-key signed scheme uploads with \"access denied\"")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", filePath, err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fields := [][2]string{
		{"type", "merge"},
		{"obj", "obj_scheme"},
		{"obj_id", strconv.Itoa(targetStage)},
		{"obj_type", "stage"},
		{"apply_mode", "false"},
	}
	if v.WorkspaceID != "" {
		fields = append(fields, [2]string{"company_id", v.WorkspaceID})
	}
	if projectID > 0 {
		fields = append(fields, [2]string{"project_id", strconv.Itoa(projectID)})
	}
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return res, err
		}
	}
	fw, err := mw.CreateFormFile("scheme", filepath.Base(filePath))
	if err != nil {
		return res, err
	}
	if _, err := fw.Write(data); err != nil {
		return res, err
	}
	if err := mw.Close(); err != nil {
		return res, err
	}

	ctx := v.Ctx
	if ctx == nil {
		return res, fmt.Errorf("executor has no context")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.APIUrl+"/api/2/upload", &body)
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Simulator "+v.Token)
	setCorezoidOrigin(req, v.APIUrl)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return res, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		snippet := string(raw)
		if len(snippet) > 256 {
			snippet = snippet[:256]
		}
		return res, fmt.Errorf("upload: HTTP %d, non-JSON response: %s", resp.StatusCode, snippet)
	}
	if rp, _ := parsed["request_proc"].(string); rp != "ok" {
		return res, fmt.Errorf("upload: HTTP %d, request_proc=%v", resp.StatusCode, parsed["request_proc"])
	}
	op, err := firstOp(parsed)
	if err != nil {
		return res, fmt.Errorf("upload: %w", err)
	}
	res.SchemeID = stringValue(op, "scheme_id")
	res.SchemeType = stringValue(op, "scheme_type")
	if res.SchemeID == "" {
		return res, fmt.Errorf("upload: server answered ok but returned no scheme_id")
	}
	return res, nil
}

// CreateStage adds a stage to an existing project. create-project can create
// stages only together with a new project; this is the missing op for an
// existing one.
func (v *Executor) CreateStage(companyID string, projectID int, title, shortName, description string, immutable bool) (int64, string, error) {
	op := map[string]any{
		"type":       "create",
		"obj":        "stage",
		"project_id": projectID,
		"company_id": companyID,
		"title":      title,
		"immutable":  immutable,
	}
	if shortName != "" {
		op["short_name"] = shortName
	}
	if description != "" {
		op["description"] = description
	}
	resp, err := v.req("create_stage", []map[string]any{op})
	if err != nil {
		return 0, "", err
	}
	o, err := firstOp(resp)
	if err != nil {
		return 0, "", err
	}
	var id int64
	if f, ok := o["obj_id"].(float64); ok {
		id = int64(f)
	}
	return id, stringValue(o, "short_name"), nil
}
