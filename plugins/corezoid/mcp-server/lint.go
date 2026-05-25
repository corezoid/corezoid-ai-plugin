package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// LintResult holds the combined lint output
type LintResult struct {
	ProcessTitle    string
	NoopConditions  []NoopCondition
	UnusedSetParams []UnusedSetParam
	OrphanedNodes   []OrphanedNode
	TotalNodes      int
	ReachableCount  int
	SchemaValid     bool
	SchemaError     string
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

	result.NoopConditions, result.UnusedSetParams = findNoopNodes(nodes)
	result.OrphanedNodes, result.ReachableCount = findOrphanedNodes(nodes)

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
func findNoopNodes(rawNodes []interface{}) ([]NoopCondition, []UnusedSetParam) {
	type nodeData struct {
		id     string
		title  string
		logics []map[string]interface{}
		sems   []map[string]interface{}
	}

	nodes := make([]nodeData, 0, len(rawNodes))
	nodeMap := make(map[string]map[string]interface{})

	for _, raw := range rawNodes {
		n, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := n["id"].(string)
		title, _ := n["title"].(string)
		nodeMap[id] = n

		cond, _ := n["condition"].(map[string]interface{})
		logics := toMapSlice(cond["logics"])
		sems := toMapSlice(cond["semaphors"])
		nodes = append(nodes, nodeData{id: id, title: title, logics: logics, sems: sems})
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
				destTitle, _ = dn["title"].(string)
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

// findOrphanedNodes does a BFS from the Start node and returns unreachable nodes
func findOrphanedNodes(rawNodes []interface{}) ([]OrphanedNode, int) {
	typeLabels := map[float64]string{0: "standard", 1: "start", 2: "final", 3: "escalation"}

	type nodeEntry struct {
		id      string
		title   string
		objType float64
		logics  []map[string]interface{}
		sems    []map[string]interface{}
	}

	entries := make([]nodeEntry, 0, len(rawNodes))
	nodeMap := make(map[string]nodeEntry)

	for _, raw := range rawNodes {
		n, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := n["id"].(string)
		title, _ := n["title"].(string)
		objType, _ := n["obj_type"].(float64)
		cond, _ := n["condition"].(map[string]interface{})
		logics := toMapSlice(cond["logics"])
		sems := toMapSlice(cond["semaphors"])
		e := nodeEntry{id: id, title: title, objType: objType, logics: logics, sems: sems}
		entries = append(entries, e)
		nodeMap[id] = e
	}

	// Build adjacency list
	adj := make(map[string][]string)
	for _, e := range entries {
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
	for _, e := range entries {
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
	for _, e := range entries {
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
