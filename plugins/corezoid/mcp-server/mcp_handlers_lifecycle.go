package main

import (
	"context"
	"fmt"
	"strings"
)

const maxFolderLineageDepth = 64

type folderPlacement struct {
	FolderID   int
	ParentID   int
	Title      string
	Kind       string
	ProjectID  int
	StageID    int
	Path       string
	AncestorID map[int]struct{}
}

func handlePauseProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	return handleProcessLifecycleStatus(ctx, args, "paused")
}

func handleResumeProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	return handleProcessLifecycleStatus(ctx, args, "active")
}

func handleProcessLifecycleStatus(ctx context.Context, args map[string]interface{}, targetStatus string) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil || processID <= 0 {
		if err == nil {
			err = fmt.Errorf("process_id must be greater than zero")
		}
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, processID)
	info, err := v.ShowProcessLifecycle(processID)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	if info.Status == targetStatus {
		return fmt.Sprintf("Process #%d %q is already %s. No change performed.", processID, info.Title, targetStatus), false
	}
	if info.Status != "active" && info.Status != "paused" && info.Status != "debug" {
		return fmt.Sprintf("Error: process #%d has unsupported current status %q; no change performed", processID, info.Status), true
	}

	wantConfirm := fmt.Sprintf("process#%d:%s->%s", processID, info.Status, targetStatus)
	preview := processStatusPreview(info, targetStatus)
	apply := boolishArg(args, "apply")
	confirm, _ := args["confirm"].(string)
	if !apply {
		return fmt.Sprintf("%s\n\nDRY-RUN - status was not changed. Show this preview to the user, get explicit approval, then re-run with apply=true and confirm=%q.", preview, wantConfirm), false
	}
	if strings.TrimSpace(confirm) != wantConfirm {
		return fmt.Sprintf("Confirmation required - status was not changed.\n\n%s\n\nAfter explicit user approval, re-run with apply=true and confirm=%q.", preview, wantConfirm), true
	}

	changed, err := v.SetProcessLifecycleStatus(processID, targetStatus)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	verified, verifyErr := v.ShowProcessLifecycle(processID)
	if verifyErr != nil {
		return fmt.Sprintf("Error: Corezoid accepted the status request for process #%d, but post-verification failed: %v. The status may already have changed; run the tool again with apply=false before retrying.", processID, verifyErr), true
	}
	if verified.Status != targetStatus {
		return fmt.Sprintf("Error: status verification failed for process #%d: expected %q, live status is %q. Do not retry blindly; run a dry-run first.", processID, targetStatus, verified.Status), true
	}

	action := "paused"
	operationalNote := "New task creation is now rejected by Corezoid. Already-running or parked tasks were not modified by this tool and must be inspected separately."
	if targetStatus == "active" {
		action = "resumed"
		operationalNote = "Corezoid can accept new tasks for this process immediately."
	}
	return fmt.Sprintf("Process #%d %q %s (status %s -> %s, changed=%t). %s", processID, verified.Title, action, info.Status, verified.Status, changed, operationalNote), false
}

func processStatusPreview(info *ProcessLifecycleInfo, targetStatus string) string {
	action := "PAUSE PROCESS"
	note := "Effect: Corezoid will reject new task creation with conveyor_is_not_active. This is admission control, not a graph deploy, task deletion, or proof that already-running/parked tasks stopped."
	if targetStatus == "active" {
		action = "RESUME PROCESS"
		note = "Effect: new tasks may enter immediately from API clients, schedules, callbacks, and other processes. Confirm that maintenance is complete before resuming."
	}
	return fmt.Sprintf("%s\nProcess: #%d %q\nCurrent status: %s\nTarget status: %s\nProject/stage: %d/%d\nStage immutable: %t\n%s", action, info.ObjID, info.Title, info.Status, targetStatus, info.ProjectID, info.StageID, info.Immutable, note)
}

func handleMoveProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil || processID <= 0 {
		if err == nil {
			err = fmt.Errorf("process_id must be greater than zero")
		}
		return "Error: " + err.Error(), true
	}
	destinationID, err := moveDestinationArg(args)
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, processID)
	info, err := v.ShowProcessLifecycle(processID)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	return moveWithConfirmation(v, args, moveSubject{
		kind:          "process",
		apiType:       "conv",
		id:            processID,
		title:         info.Title,
		currentParent: info.ParentObjID,
	}, destinationID)
}

func handleMoveFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil || folderID <= 0 {
		if err == nil {
			err = fmt.Errorf("folder_id must be greater than zero")
		}
		return "Error: " + err.Error(), true
	}
	destinationID, err := moveDestinationArg(args)
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	info, err := v.ShowFolder(folderID)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	if info.ObjID != folderID {
		return fmt.Sprintf("Error: show folder #%d returned metadata for #%d", folderID, info.ObjID), true
	}
	if info.ObjType != 0 {
		return fmt.Sprintf("Error: folder #%d %q is kind %s. move-folder only moves normal folders; projects and stages require their dedicated lifecycle tools.", folderID, info.Title, folderKind(info.ObjType)), true
	}
	return moveWithConfirmation(v, args, moveSubject{
		kind:          "folder",
		apiType:       "folder",
		id:            folderID,
		title:         info.Title,
		currentParent: info.ParentObjID,
	}, destinationID)
}

type moveSubject struct {
	kind          string
	apiType       string
	id            int
	title         string
	currentParent int
}

func moveWithConfirmation(v *Executor, args map[string]interface{}, subject moveSubject, destinationID int) (string, bool) {
	if subject.currentParent == destinationID {
		return fmt.Sprintf("%s #%d %q is already in destination folder #%d. No change performed.", titleCaseKind(subject.kind), subject.id, subject.title, destinationID), false
	}

	sourcePlacement, err := inspectFolderPlacement(v, subject.currentParent)
	if err != nil {
		return fmt.Sprintf("Error resolving current parent #%d: %v", subject.currentParent, err), true
	}
	destination, err := inspectFolderPlacement(v, destinationID)
	if err != nil {
		return fmt.Sprintf("Error resolving destination folder #%d: %v", destinationID, err), true
	}
	if subject.kind == "folder" {
		if subject.id == destinationID {
			return fmt.Sprintf("Error: folder #%d cannot be moved into itself", subject.id), true
		}
		if _, isDescendant := destination.AncestorID[subject.id]; isDescendant {
			return fmt.Sprintf("Error: folder #%d cannot be moved into descendant folder #%d; this would create a hierarchy cycle", subject.id, destinationID), true
		}
	}

	contextChanged := sourcePlacement.ProjectID != destination.ProjectID || sourcePlacement.StageID != destination.StageID
	allowCrossStage := boolishArg(args, "allow_cross_stage")
	// Include the destination's live parent as well as the source parent. This
	// invalidates a dry-run token if somebody moves the destination folder
	// before apply (especially important if that changes stage context).
	wantConfirm := moveConfirmationToken(subject, sourcePlacement, destination)
	preview := movePreview(subject, sourcePlacement, destination, contextChanged)
	apply := boolishArg(args, "apply")
	confirm, _ := args["confirm"].(string)
	if !apply {
		extra := ""
		if contextChanged {
			extra = " The move also requires allow_cross_stage=true because the project/stage context changes."
		}
		return fmt.Sprintf("%s\n\nDRY-RUN - nothing moved.%s Show this preview to the user, get explicit approval, then re-run with apply=true and confirm=%q.", preview, extra, wantConfirm), false
	}
	if contextChanged && !allowCrossStage {
		return fmt.Sprintf("Cross-stage/context move blocked - nothing moved.\n\n%s\n\nAfter the user explicitly accepts the environment, alias, variable, access, and deployment risks, re-run with allow_cross_stage=true, apply=true, and confirm=%q.", preview, wantConfirm), true
	}
	if strings.TrimSpace(confirm) != wantConfirm {
		return fmt.Sprintf("Confirmation required - nothing moved.\n\n%s\n\nAfter explicit user approval, re-run with apply=true and confirm=%q.", preview, wantConfirm), true
	}

	if err := v.MoveCorezoidObject(subject.apiType, subject.id, subject.currentParent, destinationID); err != nil {
		return "Error: " + err.Error(), true
	}

	liveParent, verifyErr := movedObjectParent(v, subject)
	if verifyErr != nil {
		return fmt.Sprintf("Error: Corezoid accepted the move request for %s #%d, but post-verification failed: %v. The object may already have moved; run the tool again with apply=false before retrying.", subject.kind, subject.id, verifyErr), true
	}
	if liveParent != destinationID {
		return fmt.Sprintf("Error: move verification failed for %s #%d: expected parent #%d, live parent is #%d. Do not retry blindly; run a dry-run first.", subject.kind, subject.id, destinationID, liveParent), true
	}
	verifiedDestination, verifyDestinationErr := inspectFolderPlacement(v, destinationID)
	if verifyDestinationErr != nil {
		return fmt.Sprintf("Error: %s #%d now has destination parent #%d, but destination post-verification failed: %v. The move may already have completed; run a fresh dry-run before any retry.", titleCaseKind(subject.kind), subject.id, destinationID, verifyDestinationErr), true
	}
	if !samePlacementContext(destination, verifiedDestination) {
		return fmt.Sprintf("Error: %s #%d now has destination parent #%d, but that destination changed during the operation (before: %s; now: %s). The move already completed; review the new context and run a fresh dry-run before any further move.", titleCaseKind(subject.kind), subject.id, destinationID, placementLabel(destination), placementLabel(verifiedDestination)), true
	}

	return fmt.Sprintf("%s #%d %q moved from %s to %s. Object ID is unchanged; this was a server-side reparent, not a copy or deploy. Local mirror paths were not moved - re-pull the destination before removing any stale local copy.", titleCaseKind(subject.kind), subject.id, subject.title, placementLabel(sourcePlacement), placementLabel(verifiedDestination)), false
}

func samePlacementContext(a, b folderPlacement) bool {
	return a.FolderID == b.FolderID &&
		a.ParentID == b.ParentID &&
		a.ProjectID == b.ProjectID &&
		a.StageID == b.StageID
}

func moveConfirmationToken(subject moveSubject, source, destination folderPlacement) string {
	return fmt.Sprintf(
		"%s#%d:%d->%d@%d:ctx=%d/%d->%d/%d",
		subject.kind,
		subject.id,
		subject.currentParent,
		destination.FolderID,
		destination.ParentID,
		source.ProjectID,
		source.StageID,
		destination.ProjectID,
		destination.StageID,
	)
}

func movedObjectParent(v *Executor, subject moveSubject) (int, error) {
	if subject.apiType == "conv" {
		info, err := v.ShowProcessLifecycle(subject.id)
		if err != nil {
			return 0, err
		}
		return info.ParentObjID, nil
	}
	info, err := v.ShowFolder(subject.id)
	if err != nil {
		return 0, err
	}
	if info.ObjID != subject.id {
		return 0, fmt.Errorf("show folder #%d returned metadata for #%d", subject.id, info.ObjID)
	}
	return info.ParentObjID, nil
}

func moveDestinationArg(args map[string]interface{}) (int, error) {
	destinationID, err := intArg(args, "destination_folder_id")
	if err != nil {
		return 0, err
	}
	if destinationID < 0 {
		return 0, fmt.Errorf("destination_folder_id must be zero (workspace root) or greater")
	}
	return destinationID, nil
}

func inspectFolderPlacement(v *Executor, folderID int) (folderPlacement, error) {
	if folderID == 0 {
		return folderPlacement{
			FolderID:   0,
			ParentID:   0,
			Title:      "Root folder",
			Kind:       "root",
			Path:       "workspace root",
			AncestorID: map[int]struct{}{},
		}, nil
	}

	placement := folderPlacement{FolderID: folderID, AncestorID: make(map[int]struct{})}
	currentID := folderID
	var names []string
	for depth := 0; depth < maxFolderLineageDepth; depth++ {
		if _, seen := placement.AncestorID[currentID]; seen {
			return folderPlacement{}, fmt.Errorf("folder hierarchy cycle detected at #%d", currentID)
		}
		placement.AncestorID[currentID] = struct{}{}
		info, err := v.ShowFolder(currentID)
		if err != nil {
			return folderPlacement{}, err
		}
		if info.ObjID != currentID {
			return folderPlacement{}, fmt.Errorf("show folder #%d returned metadata for #%d", currentID, info.ObjID)
		}
		if info.ObjType != 0 && info.ObjType != 1 && !isFolderProjectObjType(info.ObjType) && !isFolderStageObjType(info.ObjType) {
			return folderPlacement{}, fmt.Errorf("folder #%d has unsupported obj_type %d", currentID, info.ObjType)
		}
		if depth == 0 {
			placement.Title = info.Title
			placement.Kind = folderKind(info.ObjType)
			placement.ParentID = info.ParentObjID
		}
		if info.Title != "" {
			names = append(names, info.Title)
		}
		switch {
		case isFolderProjectObjType(info.ObjType):
			if placement.ProjectID == 0 {
				placement.ProjectID = info.ObjID
			}
		case isFolderStageObjType(info.ObjType):
			if placement.StageID == 0 {
				placement.StageID = info.ObjID
			}
		}
		if info.ParentObjID == 0 {
			break
		}
		if info.ParentObjID == currentID {
			return folderPlacement{}, fmt.Errorf("folder #%d points to itself as parent", currentID)
		}
		currentID = info.ParentObjID
		if depth == maxFolderLineageDepth-1 {
			return folderPlacement{}, fmt.Errorf("folder hierarchy exceeds %d levels", maxFolderLineageDepth)
		}
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	placement.Path = strings.Join(names, " / ")
	if placement.Path == "" {
		placement.Path = fmt.Sprintf("folder #%d", folderID)
	}
	return placement, nil
}

func folderKind(objType int) string {
	switch {
	case objType == 0:
		return "folder"
	case objType == 1:
		return "root"
	case isFolderProjectObjType(objType):
		return "project"
	case isFolderStageObjType(objType):
		return "stage"
	default:
		return fmt.Sprintf("unknown(%d)", objType)
	}
}

func placementLabel(p folderPlacement) string {
	return fmt.Sprintf("%s #%d %q (project=%d, stage=%d)", p.Kind, p.FolderID, p.Path, p.ProjectID, p.StageID)
}

func movePreview(subject moveSubject, source, destination folderPlacement, contextChanged bool) string {
	contextNote := "Project/stage context is unchanged."
	if contextChanged {
		contextNote = "WARNING: project/stage context changes. Stage-scoped aliases and environment variables, access rules, and deployment behavior are not migrated or rewritten by this move. For a folder, this risk applies to every descendant."
	}
	return fmt.Sprintf("MOVE %s\nObject: #%d %q\nCurrent parent: %s\nDestination: %s\nEffect: preserve the same object ID and graph; change only the server-side parent. No copy or deploy is performed.\n%s\nLocal mirror: no local files/directories will be moved automatically.", strings.ToUpper(subject.kind), subject.id, subject.title, placementLabel(source), placementLabel(destination), contextNote)
}

func titleCaseKind(kind string) string {
	if kind == "" {
		return "Object"
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}
