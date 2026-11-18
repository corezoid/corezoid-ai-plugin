package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// handleCreateStage creates a stage in an existing project.
func handleCreateStage(ctx context.Context, args map[string]interface{}) (string, bool) {
	v := NewValidator(ctx, 0)
	companyID, err := companyIDArg(v, args)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	projectID, err := intArg(args, "project_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	title, err := strArg(args, "title")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	immutable := false
	if _, ok := args["immutable"]; ok {
		b, berr := requiredBoolArg(args, "immutable")
		if berr != "" {
			return berr, true
		}
		immutable = b
	}
	id, short, err := v.CreateStage(companyID, projectID, title, optStrArg(args, "short_name"), optStrArg(args, "description"), immutable)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	out := fmt.Sprintf("Stage %q created in project %d — stage_id=%d", title, projectID, id)
	if short != "" {
		out += fmt.Sprintf(" short_name=%q", short)
	}
	out += fmt.Sprintf(" (%s)", immutableWord(immutable))
	return out, false
}

// handleUploadScheme uploads a local exported scheme file (.zip / .json) as a
// merge source for a target stage and returns its scheme_id. Pair it with
// deploy-stage source_scheme_id to merge the file into the stage — the API
// counterpart of the UI's "Merge from file", which is how a stage is moved
// between two Corezoid installations (e.g. DEV → PROD).
func handleUploadScheme(ctx context.Context, args map[string]interface{}) (string, bool) {
	p := strings.TrimSpace(optStrArg(args, "file_path"))
	if p == "" {
		return "Error: file_path is required (an exported .zip or .json inside the working directory)", true
	}
	safe, err := confineToWorkdir(p)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	st, err := os.Stat(safe)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	if st.IsDir() {
		return fmt.Sprintf("Error: %s is a directory; pass the exported .zip or .json file", p), true
	}
	targetStage, err := intArg(args, "target_stage_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	projectID := 0
	if _, ok := args["project_id"]; ok {
		if projectID, err = intArg(args, "project_id"); err != nil {
			return "Error: " + err.Error(), true
		}
	}

	v := NewValidator(ctx, 0)
	if c := optStrArg(args, "company_id"); c != "" {
		v.WorkspaceID = c
	}
	res, err := v.UploadSchemeForMerge(safe, targetStage, projectID)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	return fmt.Sprintf("✅ Uploaded %s for stage %d — scheme_id=%s (scheme_type=%s).\nNothing is merged yet. Preview the merge with deploy-stage {project_id, target_stage_id:%d, source_scheme_id:%q} (apply=false), then apply it only after the user confirms.",
		p, targetStage, res.SchemeID, res.SchemeType, targetStage, res.SchemeID), false
}
