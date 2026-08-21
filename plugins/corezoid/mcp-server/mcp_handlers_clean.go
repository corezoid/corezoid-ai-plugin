package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// handleCleanProcess removes inactive nodes from a Corezoid process and saves
// the result as <title>_cleaned.conv.json next to the original.
//
// Algorithm (mirrors the clean-corezoid-process skill):
//  1. Export the process and collect per-node time-series statistics.
//  2. Apply three exclusion criteria to protect structurally important inactive nodes.
//  3. Delete inactive nodes, redirect dangling references to their go-successors,
//     then cascade-remove newly empty condition nodes.
//  4. Remove pass-through nodes (obj_type=0 with a single unconditional go that
//     originally had conditional branches).
//  5. Remove delay→final nodes (obj_type=0, no logics, all semaphor targets are finals).
//  6. Validate the resulting scheme and save.
func handleCleanProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	days := 90
	if d, err2 := intArg(args, "days"); err2 == nil && d > 0 {
		days = d
	}
	overwrite := false
	if ow, ok := args["overwrite"].(bool); ok {
		overwrite = ow
	}

	v := NewValidator(ctx, processID)

	// ── Step 1: Export process ────────────────────────────────────────────────
	exported, err := v.ExportProcess()
	if err != nil {
		return fmt.Sprintf("Error fetching process: %v", err), true
	}
	var procMap map[string]interface{}
	if arr, ok := exported.([]interface{}); ok && len(arr) > 0 {
		procMap, _ = arr[0].(map[string]interface{})
	} else {
		procMap, _ = exported.(map[string]interface{})
	}
	if procMap == nil {
		return "Error: could not extract process data", true
	}
	scheme, _ := procMap["scheme"].(map[string]interface{})
	if scheme == nil {
		return "Error: process has no scheme", true
	}
	rawNodes, _ := scheme["nodes"].([]interface{})

	nodes := make([]map[string]interface{}, 0, len(rawNodes))
	for _, rn := range rawNodes {
		if nm, ok := rn.(map[string]interface{}); ok {
			nodes = append(nodes, nm)
		}
	}
	originalCount := len(nodes)
	if originalCount == 0 {
		return "Process has no nodes — nothing to clean.", false
	}

	// Keep a snapshot of the original structure for pass-through detection and
	// for following go-successor chains of nodes that will be removed.
	origNmap := cleanNodeMapByID(nodes)

	// ── Step 2: Collect node statistics concurrently ──────────────────────────
	endTS := time.Now().Unix()
	startTS := endTS - int64(days)*86400

	type statResult struct {
		nodeID string
		active bool
		err    error
	}

	resultCh := make(chan statResult, len(nodes))
	sem := make(chan struct{}, 16) // max 16 concurrent API requests
	var wg sync.WaitGroup

	for _, node := range nodes {
		nodeID, _ := node["id"].(string)
		if nodeID == "" {
			continue
		}
		wg.Add(1)
		go func(nid string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ops := []map[string]any{
				{
					"obj":             "stat",
					"type":            "show",
					"group":           "time",
					"conv_id":         processID,
					"node_id":         nid,
					"company_id":      v.WorkspaceID,
					"start":           int(startTS),
					"end":             int(endTS),
					"interval":        "day",
					"timezone_offset": 0,
				},
			}
			resp, reqErr := v.req("get_node_stat", ops)
			if reqErr != nil {
				resultCh <- statResult{nodeID: nid, err: reqErr}
				return
			}
			resultCh <- statResult{nodeID: nid, active: cleanIsNodeActive(resp)}
		}(nodeID)
	}
	wg.Wait()
	close(resultCh)

	activeIDs := make(map[string]bool)
	inactiveIDs := make(map[string]bool)
	errorIDs := make(map[string]bool)
	for r := range resultCh {
		switch {
		case r.err != nil:
			errorIDs[r.nodeID] = true
		case r.active:
			activeIDs[r.nodeID] = true
		default:
			inactiveIDs[r.nodeID] = true
		}
	}

	// ── Step 3: Exclusions ────────────────────────────────────────────────────
	excluded := cleanApplyExclusions(nodes, activeIDs, inactiveIDs)

	toRemove := make(map[string]bool)
	for id := range inactiveIDs {
		if !excluded[id] {
			toRemove[id] = true
		}
	}
	initialRemoveCount := len(toRemove)

	// ── Step 4: Delete + cascade with redirect ────────────────────────────────
	var droppedErrHandlers []string
	nodes = cleanDeleteAndCascade(nodes, toRemove, origNmap, &droppedErrHandlers)
	cascadeCount := len(toRemove) - initialRemoveCount

	// ── Step 5: Pass-through removal ──────────────────────────────────────────
	nodes, ptCount := cleanRemovePassThrough(nodes, origNmap)

	// ── Step 6: Delay→final removal ───────────────────────────────────────────
	nodes, dfCount := cleanRemoveDelayToFinal(nodes)

	// ── Step 7: Validate ──────────────────────────────────────────────────────
	validationErrors := cleanValidate(nodes)

	// Update scheme
	rawCleaned := make([]interface{}, 0, len(nodes))
	for _, n := range nodes {
		rawCleaned = append(rawCleaned, n)
	}
	scheme["nodes"] = rawCleaned

	// ── Save ──────────────────────────────────────────────────────────────────
	title, _ := procMap["title"].(string)
	baseFileName := convFileName(processID, title)
	const ext = ".conv.json"
	cleanedFileName := baseFileName
	if len(cleanedFileName) > len(ext) && cleanedFileName[len(cleanedFileName)-len(ext):] == ext {
		cleanedFileName = cleanedFileName[:len(cleanedFileName)-len(ext)]
	}
	cleanedFileName += "_cleaned.conv.json"

	var dir string
	if parentID := int(cleanFloat(procMap["parent_id"])); parentID != 0 && v.StageID != 0 {
		if resolved, resolveErr := v.resolveFolderPathFromAPI(parentID); resolveErr == nil {
			if stageRoot := findStageRootFromCWD(v.StageID); stageRoot != "" {
				dir = filepath.Join(stageRoot, resolved)
			} else {
				dir = resolved
			}
		}
	}
	if dir != "" {
		if mkErr := os.MkdirAll(dir, 0755); mkErr != nil {
			return fmt.Sprintf("Error creating directory: %v", mkErr), true
		}
	}
	filePath := filepath.Join(dir, cleanedFileName)

	if !overwrite {
		if _, statErr := os.Stat(filePath); statErr == nil {
			return fmt.Sprintf(
				"Error: %s already exists. Delete it first or pass overwrite=true to replace it.",
				filePath,
			), true
		}
	}

	data, marshalErr := json.MarshalIndent(procMap, "", "  ")
	if marshalErr != nil {
		return fmt.Sprintf("Error marshaling cleaned process: %v", marshalErr), true
	}
	if writeErr := os.WriteFile(filePath, data, 0644); writeErr != nil {
		return fmt.Sprintf("Error writing file: %v", writeErr), true
	}

	// ── Report ────────────────────────────────────────────────────────────────
	finalCount := len(nodes)
	report := fmt.Sprintf(
		"Process %d (%q) cleaned.\n"+
			"Nodes: %d → %d (removed %d)\n"+
			"Period: last %d days\n"+
			"  Active: %d, Inactive: %d, Stat-errors: %d\n"+
			"  Excluded by rules: %d\n"+
			"  Removed: %d initial + %d cascade + %d pass-through + %d delay→final\n"+
			"Validation errors: %d\n"+
			"Saved: %s",
		processID, title,
		originalCount, finalCount, originalCount-finalCount,
		days,
		len(activeIDs), len(inactiveIDs), len(errorIDs),
		len(excluded),
		initialRemoveCount, cascadeCount, ptCount, dfCount,
		len(validationErrors),
		filePath,
	)
	if len(droppedErrHandlers) > 0 {
		report += fmt.Sprintf("\n\nWarning: %d err_node_id reference(s) were dropped because the handler node was removed and had no unambiguous go-successor. Review these logics manually:", len(droppedErrHandlers))
		for i, w := range droppedErrHandlers {
			if i >= 10 {
				report += fmt.Sprintf("\n  … and %d more", len(droppedErrHandlers)-10)
				break
			}
			report += "\n  - " + w
		}
	}
	isErr := len(validationErrors) > 0
	if isErr {
		report += "\n\nValidation issues:"
		for i, e := range validationErrors {
			if i >= 10 {
				report += fmt.Sprintf("\n  … and %d more", len(validationErrors)-10)
				break
			}
			report += "\n  - " + e
		}
	}
	return report, isErr
}

// ── helpers ───────────────────────────────────────────────────────────────────

// cleanIsNodeActive reports whether a get_node_stat response contains at least
// one data point with a non-zero in or out count.
func cleanIsNodeActive(resp map[string]interface{}) bool {
	ops, ok := resp["ops"].([]interface{})
	if !ok || len(ops) == 0 {
		return false
	}
	op, ok := ops[0].(map[string]interface{})
	if !ok {
		return false
	}
	data, ok := op["data"].([]interface{})
	if !ok {
		return false
	}
	for _, d := range data {
		entry, ok := d.(map[string]interface{})
		if !ok {
			continue
		}
		if cleanToInt(entry["in"]) > 0 || cleanToInt(entry["out"]) > 0 {
			return true
		}
	}
	return false
}

// cleanNodeMapByID returns a map from node id → node.
func cleanNodeMapByID(nodes []map[string]interface{}) map[string]map[string]interface{} {
	m := make(map[string]map[string]interface{}, len(nodes))
	for _, n := range nodes {
		if id, ok := n["id"].(string); ok && id != "" {
			m[id] = n
		}
	}
	return m
}

// cleanNodeLogics returns the logics slice from a node's condition, never nil.
func cleanNodeLogics(node map[string]interface{}) []interface{} {
	if cond, ok := node["condition"].(map[string]interface{}); ok {
		if l, ok := cond["logics"].([]interface{}); ok {
			return l
		}
	}
	return nil
}

// cleanNodeSemaphors returns the semaphors slice from a node's condition, never nil.
func cleanNodeSemaphors(node map[string]interface{}) []interface{} {
	if cond, ok := node["condition"].(map[string]interface{}); ok {
		if s, ok := cond["semaphors"].([]interface{}); ok {
			return s
		}
	}
	return nil
}

// cleanObjType returns a node's obj_type as int (-1 if absent).
func cleanObjType(node map[string]interface{}) int {
	if node == nil {
		return -1
	}
	return cleanToInt(node["obj_type"])
}

// cleanToInt converts a JSON number (float64) or int to int.
func cleanToInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// cleanFloat safely extracts a float64 from an interface{}.
func cleanFloat(v interface{}) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// cleanCloneMap shallow-copies a map[string]interface{}.
func cleanCloneMap(m map[string]interface{}) map[string]interface{} {
	clone := make(map[string]interface{}, len(m))
	for k, v := range m {
		clone[k] = v
	}
	return clone
}

// ── Step 3: Exclusions ────────────────────────────────────────────────────────

// cleanApplyExclusions returns the set of inactive node IDs that must NOT be
// removed, based on the three exclusion criteria in the skill spec.
func cleanApplyExclusions(
	nodes []map[string]interface{},
	activeIDs, inactiveIDs map[string]bool,
) map[string]bool {
	nmap := cleanNodeMapByID(nodes)
	excluded := make(map[string]bool)

	// Criterion 1 & 2: active nodes with set_param or unconditional go
	for id := range activeIDs {
		node := nmap[id]
		if node == nil {
			continue
		}
		for _, l := range cleanNodeLogics(node) {
			logic, ok := l.(map[string]interface{})
			if !ok {
				continue
			}
			ltype, _ := logic["type"].(string)
			toID, _ := logic["to_node_id"].(string)
			if toID == "" || !inactiveIDs[toID] {
				continue
			}
			if ltype == "set_param" || ltype == "go" {
				excluded[toID] = true
			}
		}
	}

	// Criterion 3: escalation chains — iterate until stable.
	changed := true
	for changed {
		changed = false

		// will_stay = active ∪ excluded ∪ nodes not in inactive
		willStay := make(map[string]bool, len(activeIDs)+len(excluded))
		for id := range activeIDs {
			willStay[id] = true
		}
		for id := range excluded {
			willStay[id] = true
		}
		for _, n := range nodes {
			id, _ := n["id"].(string)
			if !inactiveIDs[id] {
				willStay[id] = true
			}
		}

		for id := range willStay {
			node := nmap[id]
			if node == nil {
				continue
			}
			for _, l := range cleanNodeLogics(node) {
				logic, ok := l.(map[string]interface{})
				if !ok {
					continue
				}
				errID, _ := logic["err_node_id"].(string)
				if errID == "" {
					continue
				}
				if inactiveIDs[errID] && !excluded[errID] {
					excluded[errID] = true
					changed = true
				}
				if errNode := nmap[errID]; errNode != nil {
					if cleanProtectErrChildren(errNode, nmap, inactiveIDs, excluded) {
						changed = true
					}
				}
			}
			// Active err_handler nodes (obj_type=3) also protect their children
			if cleanObjType(node) == 3 {
				if cleanProtectErrChildren(node, nmap, inactiveIDs, excluded) {
					changed = true
				}
			}
		}
	}
	return excluded
}

// cleanProtectErrChildren protects the children of a condition/err_handler node
// that must stay alive when the parent is kept in the schema.
func cleanProtectErrChildren(
	errNode map[string]interface{},
	nmap map[string]map[string]interface{},
	inactiveIDs, excluded map[string]bool,
) bool {
	added := false
	for _, l := range cleanNodeLogics(errNode) {
		logic, ok := l.(map[string]interface{})
		if !ok {
			continue
		}
		childID, _ := logic["to_node_id"].(string)
		if childID == "" {
			continue
		}
		child := nmap[childID]
		if child == nil {
			continue
		}
		switch ct := cleanObjType(child); ct {
		case 2: // final node
			if inactiveIDs[childID] && !excluded[childID] {
				excluded[childID] = true
				added = true
			}
		case 0:
			if len(cleanNodeSemaphors(child)) == 0 {
				continue // not a delay node
			}
			// Delay node with semaphors
			if inactiveIDs[childID] && !excluded[childID] {
				excluded[childID] = true
				added = true
			}
			// Protect only final targets (not retry/self-back targets)
			for _, sl := range cleanNodeSemaphors(child) {
				sem, ok := sl.(map[string]interface{})
				if !ok {
					continue
				}
				tid, _ := sem["to_node_id"].(string)
				if tn := nmap[tid]; tn != nil && cleanObjType(tn) == 2 {
					if inactiveIDs[tid] && !excluded[tid] {
						excluded[tid] = true
						added = true
					}
				}
			}
			for _, dl := range cleanNodeLogics(child) {
				dlogic, ok := dl.(map[string]interface{})
				if !ok {
					continue
				}
				did, _ := dlogic["to_node_id"].(string)
				if dn := nmap[did]; dn != nil && cleanObjType(dn) == 2 {
					if inactiveIDs[did] && !excluded[did] {
						excluded[did] = true
						added = true
					}
				}
			}
		}
	}
	return added
}

// ── Step 4: Delete + cascade with redirect ────────────────────────────────────

// cleanDeleteAndCascade removes nodes in toRemove, fixes all references
// (redirecting to a removed node's go-successor where possible), and
// cascades empty condition nodes into toRemove until stable.
// droppedErrs is populated with a description each time an err_node_id is
// dropped without a redirect (the handler had no unambiguous go-successor).
func cleanDeleteAndCascade(
	nodes []map[string]interface{},
	toRemove map[string]bool,
	origNmap map[string]map[string]interface{},
	droppedErrs *[]string,
) []map[string]interface{} {
	changed := true
	for changed {
		changed = false

		// Remove deleted nodes
		kept := nodes[:0]
		for _, n := range nodes {
			if id, _ := n["id"].(string); !toRemove[id] {
				kept = append(kept, n)
			}
		}
		nodes = kept

		for _, node := range nodes {
			cond, ok := node["condition"].(map[string]interface{})
			if !ok {
				continue
			}
			logics, _ := cond["logics"].([]interface{})
			sems, _ := cond["semaphors"].([]interface{})
			nodeID, _ := node["id"].(string)

			newLogics := make([]interface{}, 0, len(logics))
			for _, l := range logics {
				logic, ok := l.(map[string]interface{})
				if !ok {
					newLogics = append(newLogics, l)
					continue
				}
				// Redirect or drop logic whose target was removed
				if toID, _ := logic["to_node_id"].(string); toID != "" && toRemove[toID] {
					if successor := cleanFindGoSuccessor(toID, origNmap, toRemove); successor != "" {
						logic = cleanCloneMap(logic)
						logic["to_node_id"] = successor
						newLogics = append(newLogics, logic)
					}
					// else drop the logic entirely
					continue
				}
				// Clean dangling err_node_id — record a warning if dropped
				if errID, _ := logic["err_node_id"].(string); errID != "" && toRemove[errID] {
					logic = cleanCloneMap(logic)
					delete(logic, "err_node_id")
					if droppedErrs != nil {
						ltype, _ := logic["type"].(string)
						*droppedErrs = append(*droppedErrs,
							fmt.Sprintf("node %s logic (type=%s): err_node_id=%s removed (no go-successor)", nodeID, ltype, errID))
					}
				}
				newLogics = append(newLogics, logic)
			}

			newSems := make([]interface{}, 0, len(sems))
			for _, s := range sems {
				sem, ok := s.(map[string]interface{})
				if !ok {
					newSems = append(newSems, s)
					continue
				}
				if toID, _ := sem["to_node_id"].(string); toID != "" && toRemove[toID] {
					if successor := cleanFindGoSuccessor(toID, origNmap, toRemove); successor != "" {
						sem = cleanCloneMap(sem)
						sem["to_node_id"] = successor
						newSems = append(newSems, sem)
					}
					continue
				}
				newSems = append(newSems, s)
			}

			cond["logics"] = newLogics
			cond["semaphors"] = newSems

			// Cascade: obj_type 0/1/3 with no logics and no semaphors
			id, _ := node["id"].(string)
			if ot := cleanObjType(node); (ot == 0 || ot == 1 || ot == 3) &&
				len(newLogics) == 0 && len(newSems) == 0 && !toRemove[id] {
				toRemove[id] = true
				changed = true
			}
		}
	}
	return nodes
}

// cleanFindGoSuccessor follows the unconditional go-logic chain starting from
// startID (using origNmap) and returns the first node ID not in toRemove.
// Returns "" if no live successor is found or if a cycle is detected.
func cleanFindGoSuccessor(startID string, origNmap map[string]map[string]interface{}, toRemove map[string]bool) string {
	visited := map[string]bool{startID: true}
	cur := startID
	for {
		node := origNmap[cur]
		if node == nil {
			return ""
		}
		var goTarget string
		for _, l := range cleanNodeLogics(node) {
			logic, ok := l.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := logic["type"].(string); t == "go" {
				goTarget, _ = logic["to_node_id"].(string)
				break
			}
		}
		if goTarget == "" || visited[goTarget] {
			return ""
		}
		if !toRemove[goTarget] {
			return goTarget
		}
		visited[goTarget] = true
		cur = goTarget
	}
}

// ── Step 5: Pass-through removal ─────────────────────────────────────────────

// cleanRemovePassThrough removes obj_type=0 nodes that now have exactly one
// unconditional go-logic and no semaphors, but originally had conditional
// branches (go_if_const). References to them are redirected to their target.
func cleanRemovePassThrough(
	nodes []map[string]interface{},
	origNmap map[string]map[string]interface{},
) ([]map[string]interface{}, int) {
	total := 0
	changed := true
	for changed {
		changed = false
		curNmap := cleanNodeMapByID(nodes)

		var pts []struct{ id, target string }
		for _, node := range nodes {
			if cleanObjType(node) != 0 {
				continue
			}
			logics := cleanNodeLogics(node)
			sems := cleanNodeSemaphors(node)
			if len(sems) != 0 || len(logics) != 1 {
				continue
			}
			logic, ok := logics[0].(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := logic["type"].(string); t != "go" {
				continue
			}
			goTarget, _ := logic["to_node_id"].(string)
			if goTarget == "" || curNmap[goTarget] == nil {
				continue
			}
			// Must have originally had at least one go_if_const
			orig := origNmap[node["id"].(string)]
			if orig == nil {
				continue
			}
			hadConditional := false
			for _, ol := range cleanNodeLogics(orig) {
				ol2, ok := ol.(map[string]interface{})
				if !ok {
					continue
				}
				if t, _ := ol2["type"].(string); t == "go_if_const" {
					hadConditional = true
					break
				}
			}
			if !hadConditional {
				continue
			}
			pts = append(pts, struct{ id, target string }{id: node["id"].(string), target: goTarget})
		}

		if len(pts) == 0 {
			break
		}
		redirect := make(map[string]string, len(pts))
		ptSet := make(map[string]bool, len(pts))
		for _, p := range pts {
			redirect[p.id] = p.target
			ptSet[p.id] = true
		}
		total += len(pts)

		var kept []map[string]interface{}
		for _, node := range nodes {
			id, _ := node["id"].(string)
			if ptSet[id] {
				continue
			}
			cond, _ := node["condition"].(map[string]interface{})
			if cond == nil {
				kept = append(kept, node)
				continue
			}
			logics, _ := cond["logics"].([]interface{})
			sems, _ := cond["semaphors"].([]interface{})
			newLogics := make([]interface{}, 0, len(logics))
			for _, l := range logics {
				logic, ok := l.(map[string]interface{})
				if !ok {
					newLogics = append(newLogics, l)
					continue
				}
				logic = cleanCloneMap(logic)
				if tid, _ := logic["to_node_id"].(string); ptSet[tid] {
					logic["to_node_id"] = redirect[tid]
				}
				if eid, _ := logic["err_node_id"].(string); ptSet[eid] {
					logic["err_node_id"] = redirect[eid]
				}
				newLogics = append(newLogics, logic)
			}
			newSems := make([]interface{}, 0, len(sems))
			for _, s := range sems {
				sem, ok := s.(map[string]interface{})
				if !ok {
					newSems = append(newSems, s)
					continue
				}
				sem = cleanCloneMap(sem)
				if tid, _ := sem["to_node_id"].(string); ptSet[tid] {
					sem["to_node_id"] = redirect[tid]
				}
				newSems = append(newSems, sem)
			}
			cond["logics"] = newLogics
			cond["semaphors"] = newSems
			kept = append(kept, node)
		}
		nodes = kept
		changed = true
	}
	return nodes, total
}

// ── Step 6: Delay→final removal ───────────────────────────────────────────────

// cleanRemoveDelayToFinal removes obj_type=0 nodes that have no logics and
// whose every semaphor target is a final node (obj_type=2). This pattern
// arises when a condition node between a delay and a final was removed.
func cleanRemoveDelayToFinal(nodes []map[string]interface{}) ([]map[string]interface{}, int) {
	total := 0
	changed := true
	for changed {
		changed = false
		curNmap := cleanNodeMapByID(nodes)

		var candidates []struct{ id, target string }
		for _, node := range nodes {
			if cleanObjType(node) != 0 {
				continue
			}
			logics := cleanNodeLogics(node)
			sems := cleanNodeSemaphors(node)
			if len(logics) != 0 || len(sems) == 0 {
				continue
			}
			allFinal := true
			singleTarget := ""
			for _, s := range sems {
				sem, ok := s.(map[string]interface{})
				if !ok {
					allFinal = false
					break
				}
				tid, _ := sem["to_node_id"].(string)
				tn := curNmap[tid]
				if tn == nil || cleanObjType(tn) != 2 {
					allFinal = false
					break
				}
				if singleTarget == "" {
					singleTarget = tid
				} else if singleTarget != tid {
					// Semaphors point to different finals — cannot safely redirect
					// all callers to one of them; skip this node.
					allFinal = false
					break
				}
			}
			if !allFinal || singleTarget == "" {
				continue
			}
			candidates = append(candidates, struct{ id, target string }{id: node["id"].(string), target: singleTarget})
		}

		if len(candidates) == 0 {
			break
		}
		redirect := make(map[string]string, len(candidates))
		dfSet := make(map[string]bool, len(candidates))
		for _, c := range candidates {
			redirect[c.id] = c.target
			dfSet[c.id] = true
		}
		total += len(candidates)

		var kept []map[string]interface{}
		for _, node := range nodes {
			id, _ := node["id"].(string)
			if dfSet[id] {
				continue
			}
			cond, _ := node["condition"].(map[string]interface{})
			if cond == nil {
				kept = append(kept, node)
				continue
			}
			logics, _ := cond["logics"].([]interface{})
			sems, _ := cond["semaphors"].([]interface{})
			newLogics := make([]interface{}, 0, len(logics))
			for _, l := range logics {
				logic, ok := l.(map[string]interface{})
				if !ok {
					newLogics = append(newLogics, l)
					continue
				}
				logic = cleanCloneMap(logic)
				if tid, _ := logic["to_node_id"].(string); dfSet[tid] {
					logic["to_node_id"] = redirect[tid]
				}
				if eid, _ := logic["err_node_id"].(string); dfSet[eid] {
					logic["err_node_id"] = redirect[eid]
				}
				newLogics = append(newLogics, logic)
			}
			newSems := make([]interface{}, 0, len(sems))
			for _, s := range sems {
				sem, ok := s.(map[string]interface{})
				if !ok {
					newSems = append(newSems, s)
					continue
				}
				sem = cleanCloneMap(sem)
				if tid, _ := sem["to_node_id"].(string); dfSet[tid] {
					sem["to_node_id"] = redirect[tid]
				}
				newSems = append(newSems, sem)
			}
			cond["logics"] = newLogics
			cond["semaphors"] = newSems
			kept = append(kept, node)
		}
		nodes = kept
		changed = true
	}
	return nodes, total
}

// ── Step 7: Validate ──────────────────────────────────────────────────────────

// cleanValidate returns a list of structural errors in the cleaned scheme.
func cleanValidate(nodes []map[string]interface{}) []string {
	nset := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if id, ok := n["id"].(string); ok {
			nset[id] = true
		}
	}
	var errs []string
	for _, node := range nodes {
		id, _ := node["id"].(string)
		for _, l := range cleanNodeLogics(node) {
			logic, ok := l.(map[string]interface{})
			if !ok {
				continue
			}
			if tid, _ := logic["to_node_id"].(string); tid != "" && !nset[tid] {
				errs = append(errs, fmt.Sprintf("node %s: logic to_node_id=%s is dangling", id, tid))
			}
			if eid, _ := logic["err_node_id"].(string); eid != "" && !nset[eid] {
				errs = append(errs, fmt.Sprintf("node %s: err_node_id=%s is dangling", id, eid))
			}
		}
		for _, s := range cleanNodeSemaphors(node) {
			sem, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			if tid, _ := sem["to_node_id"].(string); tid != "" && !nset[tid] {
				errs = append(errs, fmt.Sprintf("node %s: semaphor to_node_id=%s is dangling", id, tid))
			}
		}
		ot := cleanObjType(node)
		if ot == 0 || ot == 1 || ot == 3 {
			if len(cleanNodeLogics(node)) == 0 && len(cleanNodeSemaphors(node)) == 0 {
				errs = append(errs, fmt.Sprintf("node %s (obj_type=%d): no logics and no semaphors", id, ot))
			}
		}
	}
	return errs
}
