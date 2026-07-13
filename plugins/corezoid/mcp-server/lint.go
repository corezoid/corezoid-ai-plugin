package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// LintResult holds the combined lint output
type LintResult struct {
	ProcessTitle           string
	NoopConditions         []NoopCondition
	UnusedSetParams        []UnusedSetParam
	OrphanedNodes          []OrphanedNode
	PassthroughEscalations []PassthroughEscalation
	LiteralReplyValues     []LiteralReplyValue
	SharedErrorClusters    []SharedErrorCluster
	OldFormatNodes         []OldFormatNode
	UnrepliedTerminals     []UnrepliedTerminal
	MissingDefaultGo       []MissingDefaultGo
	TotalNodes             int
	ReachableCount         int
	SchemaValid            bool
	SchemaError            string
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
// SharedErrorCluster is an error-cluster node reached from the error paths of
// MORE THAN ONE main-flow node. The house rule is one dedicated error cluster
// per failing node: a single node's error may fan through its own Condition
// into one Error terminal, but a neighbouring node's error must never join it.
type SharedErrorCluster struct {
	ID      string
	Title   string
	Sources []string // "id (title)" of each distinct failing node feeding it
}

type PassthroughEscalation struct {
	ID          string
	Title       string
	TargetID    string
	TargetTitle string
	Issue       string
}

// LiteralReplyValue represents an api_rpc_reply logic whose res_data (or extra)
// contains literal non-string values ([], {}, 0, true, null). The commit service
// hangs on serialising such values when the process is pushed through the API
// ("no response from server"), even though the JSON schema accepts them — reply
// parameters must be "{{variable}}" templates or plain strings, with the real
// type declared in res_data_type.
type LiteralReplyValue struct {
	ID     string
	Title  string
	Fields []string
	Issue  string
}

// OldFormatNode is a node the platform's "Convert process to new format" dialog
// would rewrite on open. Two shapes trigger it:
//  1. a node mixing an action logic (set_param, api_code, …) with go_if_const
//     conditions — the converter splits it into two nodes;
//  2. an err_node_id target with obj_type:0 — escalation targets must be
//     obj_type:3 (business-flow conditions reached via go stay obj_type:0).
type OldFormatNode struct {
	ID    string
	Title string
	Issue string
}

// UnrepliedTerminal is a final node (obj_type:2) reachable from Start without
// passing any api_rpc_reply — in a process that DOES reply elsewhere. When such
// a process is invoked via Call a Process (api_rpc), the caller's task hangs in
// the call node until its timeout semaphor on every path leading here.
type UnrepliedTerminal struct {
	ID    string
	Title string
	Issue string
}

// MissingDefaultGo is a non-final node whose logics do not end with a bare
// `go`. The server rejects the deploy: "Each node in the condition.logics
// array must always have a logic with type go at the end".
type MissingDefaultGo struct {
	ID    string
	Title string
	Issue string
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
	result.LiteralReplyValues = findLiteralReplyValues(typed)
	result.SharedErrorClusters = findSharedErrorClusters(typed)
	result.OldFormatNodes = findOldFormatNodes(typed)
	result.UnrepliedTerminals = findUnrepliedTerminals(typed)
	result.MissingDefaultGo = findMissingDefaultGo(typed)

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

// findSharedErrorClusters flags error-cluster nodes shared between the error
// paths of different main-flow nodes. Allowed: ONE node's error fanning
// through its own Condition (any number of go_if_const branches) into one
// Error terminal — that whole cluster belongs to that node. Forbidden: a
// second node's err_node_id (or its cluster tail) converging onto any node of
// another node's cluster; every failing node gets its own Reply/Error cluster
// (see docs/process/error-handling.md, "Dedicated Error Cluster Pattern").
func findSharedErrorClusters(nodes []processNode) []SharedErrorCluster {
	byID := make(map[string]processNode, len(nodes))
	for _, n := range nodes {
		byID[n.id] = n
	}
	forward := func(n processNode) []string {
		var out []string
		for _, lg := range n.logics {
			t, _ := lg["type"].(string)
			if t == "go" || t == "go_if_const" {
				if to, _ := lg["to_node_id"].(string); to != "" {
					if _, ok := byID[to]; ok {
						out = append(out, to)
					}
				}
			}
		}
		for _, sm := range n.sems {
			if to, _ := sm["to_node_id"].(string); to != "" {
				if _, ok := byID[to]; ok {
					out = append(out, to)
				}
			}
		}
		return out
	}

	// main flow = forward closure from the Start nodes. Escalations
	// (obj_type 3) and terminals (obj_type 2) are then EXCLUDED: an error
	// cluster does not become "business flow" just because one business
	// branch also routes into it (e.g. a condition's fatal branch entering
	// the reply node) — the err fan-in into it is still the violation.
	mainFlow := map[string]bool{}
	var stack []string
	for _, n := range nodes {
		if n.objType == 1 {
			mainFlow[n.id] = true
			stack = append(stack, n.id)
		}
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, v := range forward(byID[u]) {
			if !mainFlow[v] {
				mainFlow[v] = true
				stack = append(stack, v)
			}
		}
	}
	for _, n := range nodes {
		if n.objType == 2 || n.objType == 3 {
			delete(mainFlow, n.id)
		}
	}

	// walk every node's error tail (outside the main flow) and attribute the
	// visited cluster nodes to that source
	sources := map[string][]string{} // cluster node id -> distinct source labels
	seenSrc := map[string]map[string]bool{}
	var order []string
	for _, src := range nodes {
		targets := map[string]bool{}
		for _, lg := range src.logics {
			if e, _ := lg["err_node_id"].(string); e != "" {
				if _, ok := byID[e]; ok && !mainFlow[e] {
					targets[e] = true
				}
			}
		}
		if len(targets) == 0 {
			continue
		}
		label := src.id
		if src.title != "" {
			label = fmt.Sprintf("%s (%s)", src.id, src.title)
		}
		visited := map[string]bool{}
		var st []string
		for _, n := range nodes { // deterministic: doc order
			if targets[n.id] {
				st = append(st, n.id)
			}
		}
		for len(st) > 0 {
			u := st[len(st)-1]
			st = st[:len(st)-1]
			if visited[u] || mainFlow[u] {
				continue
			}
			visited[u] = true
			if seenSrc[u] == nil {
				seenSrc[u] = map[string]bool{}
				order = append(order, u)
			}
			if !seenSrc[u][label] {
				seenSrc[u][label] = true
				sources[u] = append(sources[u], label)
			}
			for _, v := range forward(byID[u]) {
				if !mainFlow[v] {
					st = append(st, v)
				}
			}
		}
	}

	var out []SharedErrorCluster
	for _, id := range order {
		if len(sources[id]) < 2 {
			continue
		}
		out = append(out, SharedErrorCluster{
			ID:      id,
			Title:   byID[id].title,
			Sources: sources[id],
		})
	}
	return out
}

// lintActionTypes are logic types that DO something to the task (as opposed to
// routing it): mixing any of them with go_if_const in one node is old format.
var lintActionTypes = map[string]bool{
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

// findOldFormatNodes detects the two node shapes that make the Corezoid UI show
// "Convert process to new format" on open (and silently rewrite the scheme):
//  1. action logic + go_if_const in one node — the converter splits it in two;
//  2. err_node_id pointing at an obj_type:0 node — escalation targets must be
//     obj_type:3. Nodes reached only via go/go_if_const may stay obj_type:0.
func findOldFormatNodes(nodes []processNode) []OldFormatNode {
	nodeMap := make(map[string]processNode, len(nodes))
	for _, n := range nodes {
		nodeMap[n.id] = n
	}

	var result []OldFormatNode
	flaggedErrTargets := make(map[string]bool)
	for _, n := range nodes {
		hasAction := false
		hasCondition := false
		for _, lg := range n.logics {
			lgType, _ := lg["type"].(string)
			if lintActionTypes[lgType] {
				hasAction = true
			}
			if lgType == "go_if_const" {
				hasCondition = true
			}
			if errID, _ := lg["err_node_id"].(string); errID != "" && !flaggedErrTargets[errID] {
				if target, ok := nodeMap[errID]; ok && target.objType == 0 {
					flaggedErrTargets[errID] = true
					title := target.title
					if title == "" {
						title = "(untitled)"
					}
					result = append(result, OldFormatNode{
						ID:    target.id,
						Title: title,
						Issue: fmt.Sprintf("err_node_id target has obj_type:0 — escalation targets must be obj_type:3 (referenced from '%s' %s); the UI will force-convert the process", n.title, n.id),
					})
				}
			}
		}
		// count-semaphor escalations (esc_node_id) are escalation targets too
		for _, sem := range n.sems {
			if escID, _ := sem["esc_node_id"].(string); escID != "" && !flaggedErrTargets[escID] {
				if target, ok := nodeMap[escID]; ok && target.objType == 0 {
					flaggedErrTargets[escID] = true
					title := target.title
					if title == "" {
						title = "(untitled)"
					}
					result = append(result, OldFormatNode{
						ID:    target.id,
						Title: title,
						Issue: fmt.Sprintf("esc_node_id target has obj_type:0 — escalation targets must be obj_type:3 (referenced from '%s' %s); the UI will force-convert the process", n.title, n.id),
					})
				}
			}
		}
		if hasAction && hasCondition {
			title := n.title
			if title == "" {
				title = "(untitled)"
			}
			result = append(result, OldFormatNode{
				ID:    n.id,
				Title: title,
				Issue: "node mixes an action logic with go_if_const conditions — old format; the UI converter will split it into an action node plus a separate condition node. Split it yourself: action + go into a new condition node",
			})
		}
	}
	return result
}

// findMissingDefaultGo flags non-final nodes whose logics list does not end
// with a bare `go`. The platform requires a default route on every routing
// node and rejects the whole deploy otherwise — lint turns that push error
// into a local finding. Final nodes (obj_type:2) carry no logics and are
// exempt, as are nodes with no logics at all.
func findMissingDefaultGo(nodes []processNode) []MissingDefaultGo {
	var result []MissingDefaultGo
	for _, n := range nodes {
		if n.objType == 2 || len(n.logics) == 0 {
			continue
		}
		last := n.logics[len(n.logics)-1]
		if t, _ := last["type"].(string); t != "go" {
			title := n.title
			if title == "" {
				title = "(untitled)"
			}
			result = append(result, MissingDefaultGo{
				ID:    n.id,
				Title: title,
				Issue: fmt.Sprintf("logics end with '%s' instead of a bare go — the server rejects the deploy; add a final go with the default destination", t),
			})
		}
	}
	return result
}

// findUnrepliedTerminals detects final nodes reachable from Start without
// crossing any api_rpc_reply, in processes that reply somewhere else. A process
// that replies on its error paths but ends its success path in a bare final is
// inconsistent by construction: an RPC caller learns about every failure but
// hangs until timeout on success. Fire-and-forget processes (no api_rpc_reply
// anywhere) are exempt — they are not RPC-style.
func findUnrepliedTerminals(nodes []processNode) []UnrepliedTerminal {
	nodeMap := make(map[string]processNode, len(nodes))
	hasReply := false
	replies := make(map[string]bool)
	for _, n := range nodes {
		nodeMap[n.id] = n
		for _, lg := range n.logics {
			if t, _ := lg["type"].(string); t == "api_rpc_reply" {
				hasReply = true
				replies[n.id] = true
			}
		}
	}
	if !hasReply {
		return nil
	}

	type state struct {
		id      string
		replied bool
	}
	var stack []state
	for _, n := range nodes {
		if n.objType == 1 {
			stack = append(stack, state{n.id, false})
		}
	}
	seen := make(map[state]bool)
	unreplied := make(map[string]bool)
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[s] {
			continue
		}
		seen[s] = true
		n, ok := nodeMap[s.id]
		if !ok {
			continue
		}
		replied := s.replied || replies[s.id]
		if n.objType == 2 {
			if !replied {
				unreplied[s.id] = true
			}
			continue
		}
		for _, lg := range n.logics {
			if to, _ := lg["to_node_id"].(string); to != "" {
				stack = append(stack, state{to, replied})
			}
			if errID, _ := lg["err_node_id"].(string); errID != "" {
				stack = append(stack, state{errID, replied})
			}
		}
		for _, sem := range n.sems {
			if to, _ := sem["to_node_id"].(string); to != "" {
				stack = append(stack, state{to, replied})
			}
			if esc, _ := sem["esc_node_id"].(string); esc != "" {
				stack = append(stack, state{esc, replied})
			}
		}
	}

	var result []UnrepliedTerminal
	for _, n := range nodes { // document order for determinism
		if !unreplied[n.id] {
			continue
		}
		title := n.title
		if title == "" {
			title = "(untitled)"
		}
		result = append(result, UnrepliedTerminal{
			ID:    n.id,
			Title: title,
			Issue: fmt.Sprintf("final '%s' is reachable from Start without any api_rpc_reply, while the process replies on other paths — an RPC caller (api_rpc) hangs until timeout on this path. Add a Reply node before the final", title),
		})
	}
	return result
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

	actionTypes := lintActionTypes

	var result []PassthroughEscalation
	for _, n := range nodes {
		if n.objType != 3 {
			continue
		}
		hasAction := false
		hasCondition := false
		goTarget := ""
		for _, lg := range n.logics {
			lgType, _ := lg["type"].(string)
			if actionTypes[lgType] {
				hasAction = true
				break
			}
			if lgType == "go_if_const" {
				hasCondition = true
			}
			if lgType == "go" {
				goTarget, _ = lg["to_node_id"].(string)
			}
		}
		// Condition escalations (retry IF: go_if_const branches + default go) and
		// timer escalations (Delay semaphors) route, they don't pass through —
		// the platform's new-format converter itself produces them as obj_type:3.
		if hasCondition || len(n.sems) > 0 {
			continue
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

// findLiteralReplyValues detects api_rpc_reply logics whose res_data (or its
// alternative spelling extra) carries literal non-string values. The Corezoid
// commit service cannot serialise them when the change comes through the API:
// the commit request gets no reply and push-process fails with the opaque
// "no response from server" — while both the JSON schema and the UI editor
// accept the very same scheme. Catching it in lint turns a dead-end hang into
// an actionable message.
//
// Only non-string values are flagged. Literal strings ("success", "API call
// error") are fine — the platform serialises them the same way as templates.
func findLiteralReplyValues(nodes []processNode) []LiteralReplyValue {
	var result []LiteralReplyValue
	for _, n := range nodes {
		for _, lg := range n.logics {
			if t, _ := lg["type"].(string); t != "api_rpc_reply" {
				continue
			}
			var fields []string
			for _, dataKey := range []string{"res_data", "extra"} {
				data, _ := lg[dataKey].(map[string]interface{})
				keys := make([]string, 0, len(data))
				for k := range data {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					if _, isString := data[k].(string); !isString {
						fields = append(fields, fmt.Sprintf("%s.%s = %s", dataKey, k, describeLiteral(data[k])))
					}
				}
			}
			if len(fields) == 0 {
				continue
			}
			displayTitle := n.title
			if displayTitle == "" {
				displayTitle = "(untitled)"
			}
			result = append(result, LiteralReplyValue{
				ID:     n.id,
				Title:  displayTitle,
				Fields: fields,
				Issue: fmt.Sprintf(
					"api_rpc_reply has literal non-string values (%s) — pushing this scheme hangs the server commit (\"no response from server\"). Set the value in an upstream api_code/set_param node and reference it as \"{{variable}}\" with the real type in res_data_type",
					strings.Join(fields, ", ")),
			})
		}
	}
	return result
}

// describeLiteral renders a res_data value for the lint message ([], {}, 0, true, null).
func describeLiteral(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(b)
	if len(s) > 40 {
		s = s[:37] + "..."
	}
	return s
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
			// count semaphors escalate via esc_node_id instead of to_node_id
			if eid, ok := sem["esc_node_id"].(string); ok && eid != "" {
				adj[e.id] = append(adj[e.id], eid)
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

	if len(result.LiteralReplyValues) > 0 {
		hasIssues = true
		sb.WriteString(fmt.Sprintf("\n=== API_RPC_REPLY LITERAL VALUES (%d) ===\n", len(result.LiteralReplyValues)))
		for _, lr := range result.LiteralReplyValues {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", lr.ID, lr.Title))
			sb.WriteString(fmt.Sprintf("  Issue: %s\n", lr.Issue))
		}
	}

	if len(result.SharedErrorClusters) > 0 {
		hasIssues = true
		sb.WriteString(fmt.Sprintf("\n=== SHARED ERROR CLUSTERS (%d) ===\n", len(result.SharedErrorClusters)))
		for _, sc := range result.SharedErrorClusters {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", sc.ID, sc.Title))
			sb.WriteString(fmt.Sprintf("  Fed by %d different nodes: %s\n", len(sc.Sources), strings.Join(sc.Sources, ", ")))
			sb.WriteString("  Issue: every failing node needs its OWN Reply/Error cluster — a neighbour's error must not join it (one node's error may fan through its own Condition into one Error terminal, but never across nodes)\n")
		}
	}

	if len(result.OldFormatNodes) > 0 {
		hasIssues = true
		sb.WriteString(fmt.Sprintf("\n=== OLD FORMAT NODES (%d) — UI will show \"Convert process to new format\" ===\n", len(result.OldFormatNodes)))
		for _, of := range result.OldFormatNodes {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", of.ID, of.Title))
			sb.WriteString(fmt.Sprintf("  Issue: %s\n", of.Issue))
		}
	}

	if len(result.UnrepliedTerminals) > 0 {
		hasIssues = true
		sb.WriteString(fmt.Sprintf("\n=== RPC PATHS WITHOUT REPLY (%d) ===\n", len(result.UnrepliedTerminals)))
		for _, ut := range result.UnrepliedTerminals {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", ut.ID, ut.Title))
			sb.WriteString(fmt.Sprintf("  Issue: %s\n", ut.Issue))
		}
	}

	if len(result.MissingDefaultGo) > 0 {
		hasIssues = true
		sb.WriteString(fmt.Sprintf("\n=== NODES WITHOUT DEFAULT GO (%d) — server rejects the deploy ===\n", len(result.MissingDefaultGo)))
		for _, mg := range result.MissingDefaultGo {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", mg.ID, mg.Title))
			sb.WriteString(fmt.Sprintf("  Issue: %s\n", mg.Issue))
		}
	}

	if !hasIssues {
		sb.WriteString("\nNo issues found.")
	} else {
		schemaIssues := 0
		if !result.SchemaValid {
			schemaIssues = 1
		}
		total := len(result.NoopConditions) + len(result.UnusedSetParams) + len(result.OrphanedNodes) + len(result.PassthroughEscalations) + len(result.LiteralReplyValues) + len(result.SharedErrorClusters) + len(result.OldFormatNodes) + len(result.UnrepliedTerminals) + len(result.MissingDefaultGo) + schemaIssues
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
