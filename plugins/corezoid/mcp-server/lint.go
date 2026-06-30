package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// LintResult holds the combined lint output
type LintResult struct {
	ProcessTitle             string
	NoopConditions           []NoopCondition
	UnusedSetParams          []UnusedSetParam
	OrphanedNodes            []OrphanedNode
	PassthroughEscalations   []PassthroughEscalation
	TotalNodes               int
	ReachableCount           int
	SchemaValid              bool
	SchemaError              string
}

type NoopCondition struct {
	ID                string
	Title             string
	RoutingCount      int
	SingleDestination string
	DestinationTitle  string
	Issue             string
}

type UnusedSetParam struct {
	ID              string
	Title           string
	UnusedVariables []string
	Issue           string
}

type OrphanedNode struct {
	ID      string
	Title   string
	ObjType string
}

// PassthroughEscalation represents an escalation node (obj_type:3) whose only logic
// is a bare `go` — it adds no value and should be replaced by a direct err_node_id
// pointing straight to the final error node.
type PassthroughEscalation struct {
	ID          string
	Title       string
	TargetID    string
	TargetTitle string
	Issue       string
}

// processNode is the typed representation of a Corezoid node used throughout lint checks.
type processNode struct {
	id      string
	title   string
	objType float64
	logics  []map[string]interface{}
	sems    []map[string]interface{}
}

// parseProcessNodes decodes raw node interfaces into typed processNode values.
// Fields missing or of the wrong type are silently zeroed — no type assertion panics.
func parseProcessNodes(rawNodes []interface{}) []processNode {
	nodes := make([]processNode, 0, len(rawNodes))
	for _, raw := range rawNodes {
		n, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := n["id"].(string)
		title, _ := n["title"].(string)
		objType, _ := n["obj_type"].(float64)
		cond, _ := n["condition"].(map[string]interface{})
		nodes = append(nodes, processNode{
			id:      id,
			title:   title,
			objType: objType,
			logics:  toMapSlice(cond["logics"]),
			sems:    toMapSlice(cond["semaphors"]),
		})
	}
	return nodes
}

// lintProcess loads a process JSON file and runs lint checks
func lintProcess(filePath string) (*LintResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	var proc map[string]interface{}
	if err := json.Unmarshal(data, &proc); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}

	nodes, err := getNodes(proc)
	if err != nil {
		return nil, fmt.Errorf("failed to extract nodes: %v", err)
	}

	title, _ := proc["title"].(string)

	result := &LintResult{ProcessTitle: title, TotalNodes: len(nodes)}

	typed := parseProcessNodes(nodes)
	result.NoopConditions, result.UnusedSetParams = findNoopNodes(typed)
	result.OrphanedNodes, result.ReachableCount = findOrphanedNodes(typed)
	result.PassthroughEscalations = findPassthroughEscalations(typed)

	schemaErr := ValidateJSONSchema(filePath, debug)
	if schemaErr != nil {
		result.SchemaValid = false
		result.SchemaError = schemaErr.Error()
	} else {
		result.SchemaValid = true
	}

	return result, nil
}

// findNoopNodes detects functionally useless nodes:
// 1. No-op conditions: all routing branches go to the same destination
// 2. Unused set_param: variables set but never referenced downstream
func findNoopNodes(nodes []processNode) ([]NoopCondition, []UnusedSetParam) {
	nodeMap := make(map[string]processNode, len(nodes))
	for _, n := range nodes {
		nodeMap[n.id] = n
	}

	// --- Pattern 1: No-op conditions ---
	var noopConditions []NoopCondition
	noopNodeIDs := make(map[string]bool)

	for _, n := range nodes {
		targets := make(map[string]bool)
		hasRouting := false
		routingCount := 0

		for _, lg := range n.logics {
			lgType, _ := lg["type"].(string)
			if lgType == "go" || lgType == "go_if_const" {
				hasRouting = true
				routingCount++
				if tid, ok := lg["to_node_id"].(string); ok && tid != "" {
					targets[tid] = true
				}
			}
		}

		if hasRouting && len(targets) == 1 && routingCount >= 2 {
			dest := ""
			for k := range targets {
				dest = k
			}
			destTitle := ""
			if dn, ok := nodeMap[dest]; ok {
				destTitle = dn.title
				if destTitle == "" {
					destTitle = "(untitled)"
				}
			}
			displayTitle := n.title
			if displayTitle == "" {
				displayTitle = "(untitled)"
			}
			noopConditions = append(noopConditions, NoopCondition{
				ID:                n.id,
				Title:             displayTitle,
				RoutingCount:      routingCount,
				SingleDestination: dest,
				DestinationTitle:  destTitle,
				Issue: fmt.Sprintf("All %d branches route to the same node '%s' (%s)",
					routingCount, destTitle, dest),
			})
			noopNodeIDs[n.id] = true
		}
	}

	// --- Pattern 2: Unused set_param ---
	// Build reference blob from all non-noop, non-set_param logics
	var refParts []string
	for _, n := range nodes {
		if noopNodeIDs[n.id] {
			continue
		}
		for _, lg := range n.logics {
			if t, _ := lg["type"].(string); t == "set_param" {
				continue
			}
			refParts = append(refParts, fmt.Sprintf("%v", lg))
		}
		for _, sem := range n.sems {
			refParts = append(refParts, fmt.Sprintf("%v", sem))
		}
	}
	refBlob := strings.Join(refParts, " ")

	var unusedSetParams []UnusedSetParam
	for _, n := range nodes {
		for _, lg := range n.logics {
			if t, _ := lg["type"].(string); t != "set_param" {
				continue
			}
			extra, _ := lg["extra"].(map[string]interface{})
			if len(extra) == 0 {
				continue
			}
			var unreferenced []string
			for varName := range extra {
				pattern := "{{" + varName + "}}"
				if !strings.Contains(refBlob, pattern) {
					unreferenced = append(unreferenced, varName)
				}
			}
			if len(unreferenced) > 0 {
				displayTitle := n.title
				if displayTitle == "" {
					displayTitle = "(untitled)"
				}
				unusedSetParams = append(unusedSetParams, UnusedSetParam{
					ID:              n.id,
					Title:           displayTitle,
					UnusedVariables: unreferenced,
					Issue: fmt.Sprintf("set_param sets %v but no downstream node references them",
						unreferenced),
				})
			}
		}
	}

	return noopConditions, unusedSetParams
}

// findPassthroughEscalations detects escalation nodes (obj_type:3) that contain only
// a bare `go` logic and no action logic (api_rpc_reply, set_param, etc.).
// Such nodes are pure pass-throughs: the err_node_id should point directly to the
// final error node instead of routing through an empty escalation node.
func findPassthroughEscalations(nodes []processNode) []PassthroughEscalation {
	nodeMap := make(map[string]processNode, len(nodes))
	for _, n := range nodes {
		nodeMap[n.id] = n
	}

	actionTypes := map[string]bool{
		"api_rpc_reply": true,
		"api_rpc":       true,
		"api_copy":      true,
		"api":           true,
		"api_code":      true,
		"set_param":     true,
		"api_sum":       true,
		"db_call":       true,
		"api_git":       true,
		"api_queue":     true,
		"api_get_task":  true,
	}

	var result []PassthroughEscalation
	for _, n := range nodes {
		if n.objType != 3 {
			continue
		}
		hasAction := false
		goTarget := ""
		for _, lg := range n.logics {
			lgType, _ := lg["type"].(string)
			if actionTypes[lgType] {
				hasAction = true
				break
			}
			if lgType == "go" {
				goTarget, _ = lg["to_node_id"].(string)
			}
		}
		if hasAction || goTarget == "" {
			continue
		}
		targetTitle := ""
		if target, ok := nodeMap[goTarget]; ok {
			targetTitle = target.title
			if targetTitle == "" {
				targetTitle = "(untitled)"
			}
		}
		displayTitle := n.title
		if displayTitle == "" {
			displayTitle = "(untitled)"
		}
		result = append(result, PassthroughEscalation{
			ID:          n.id,
			Title:       displayTitle,
			TargetID:    goTarget,
			TargetTitle: targetTitle,
			Issue: fmt.Sprintf(
				"Escalation node (obj_type:3) has no action logic — wire err_node_id directly to '%s' (%s)",
				targetTitle, goTarget,
			),
		})
	}
	return result
}

// findOrphanedNodes does a BFS from the Start node and returns unreachable nodes
func findOrphanedNodes(nodes []processNode) ([]OrphanedNode, int) {
	typeLabels := map[float64]string{0: "standard", 1: "start", 2: "final", 3: "escalation"}

	nodeMap := make(map[string]processNode, len(nodes))
	for _, e := range nodes {
		nodeMap[e.id] = e
	}

	// Build adjacency list
	adj := make(map[string][]string)
	for _, e := range nodes {
		adj[e.id] = nil
		for _, lg := range e.logics {
			if tid, ok := lg["to_node_id"].(string); ok && tid != "" {
				adj[e.id] = append(adj[e.id], tid)
			}
			if eid, ok := lg["err_node_id"].(string); ok && eid != "" {
				adj[e.id] = append(adj[e.id], eid)
			}
		}
		for _, sem := range e.sems {
			if tid, ok := sem["to_node_id"].(string); ok && tid != "" {
				adj[e.id] = append(adj[e.id], tid)
			}
		}
	}

	// Find start node (obj_type == 1)
	startID := ""
	for _, e := range nodes {
		if e.objType == 1 {
			startID = e.id
			break
		}
	}
	if startID == "" {
		return nil, 0
	}

	// BFS
	visited := make(map[string]bool)
	queue := []string{startID}
	visited[startID] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if !visited[nb] {
				if _, exists := nodeMap[nb]; exists {
					visited[nb] = true
					queue = append(queue, nb)
				}
			}
		}
	}

	var orphaned []OrphanedNode
	for _, e := range nodes {
		if !visited[e.id] {
			label, ok := typeLabels[e.objType]
			if !ok {
				label = fmt.Sprintf("unknown_%v", e.objType)
			}
			displayTitle := e.title
			if displayTitle == "" {
				displayTitle = "(untitled)"
			}
			orphaned = append(orphaned, OrphanedNode{
				ID:      e.id,
				Title:   displayTitle,
				ObjType: label,
			})
		}
	}

	return orphaned, len(visited)
}

// FormatLintResult renders a LintResult as human-readable text suitable for MCP tool output.
func FormatLintResult(result *LintResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Lint results for: %s\n", result.ProcessTitle))
	sb.WriteString(fmt.Sprintf("Total nodes: %d\n", result.TotalNodes))

	hasIssues := false

	if !result.SchemaValid {
		hasIssues = true
		sb.WriteString("\n=== JSON SCHEMA VALIDATION FAILED ===\n")
		sb.WriteString(fmt.Sprintf("  %s\n", result.SchemaError))
	}

	if len(result.NoopConditions) > 0 {
		hasIssues = true
		sb.WriteString(fmt.Sprintf("\n=== NOOP CONDITIONS (%d) ===\n", len(result.NoopConditions)))
		for _, nc := range result.NoopConditions {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", nc.ID, nc.Title))
			sb.WriteString(fmt.Sprintf("  Issue: %s\n", nc.Issue))
		}
	}

	if len(result.UnusedSetParams) > 0 {
		hasIssues = true
		sb.WriteString(fmt.Sprintf("\n=== UNUSED SET_PARAM (%d) ===\n", len(result.UnusedSetParams)))
		for _, up := range result.UnusedSetParams {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", up.ID, up.Title))
			sb.WriteString(fmt.Sprintf("  Issue: %s\n", up.Issue))
		}
	}

	if len(result.OrphanedNodes) > 0 {
		hasIssues = true
		sb.WriteString(fmt.Sprintf("\n=== ORPHANED NODES (%d / %d reachable from Start) ===\n",
			len(result.OrphanedNodes), result.ReachableCount))
		for _, on := range result.OrphanedNodes {
			sb.WriteString(fmt.Sprintf("  [%s] %s  (type: %s)\n", on.ID, on.Title, on.ObjType))
		}
	}

	if len(result.PassthroughEscalations) > 0 {
		hasIssues = true
		sb.WriteString(fmt.Sprintf("\n=== PASSTHROUGH ESCALATION NODES (%d) ===\n", len(result.PassthroughEscalations)))
		for _, pe := range result.PassthroughEscalations {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", pe.ID, pe.Title))
			sb.WriteString(fmt.Sprintf("  Issue: %s\n", pe.Issue))
		}
	}

	if !hasIssues {
		sb.WriteString("\nNo issues found.")
	} else {
		schemaIssues := 0
		if !result.SchemaValid {
			schemaIssues = 1
		}
		total := len(result.NoopConditions) + len(result.UnusedSetParams) + len(result.OrphanedNodes) + len(result.PassthroughEscalations) + schemaIssues
		sb.WriteString(fmt.Sprintf("\nTotal issues: %d\n", total))
	}

	return sb.String()
}

// toMapSlice safely converts an interface{} to []map[string]interface{}
func toMapSlice(v interface{}) []map[string]interface{} {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			result = append(result, m)
		}
	}
	return result
}
