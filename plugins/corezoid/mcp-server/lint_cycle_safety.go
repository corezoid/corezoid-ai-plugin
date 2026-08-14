package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CycleAssessment struct {
	NodeIDs     []string
	NodeTitles  []string
	Bounded     bool
	BoundKind   string
	BoundDetail string
	HasDelay    bool
	Issue       string
}

type UnresolvedCallRisk struct {
	SourceProcessID    int
	SourceProcessTitle string
	NodeID             string
	NodeTitle          string
	LogicType          string
	ConvID             string
	Issue              string
}

type InterprocessCycleRisk struct {
	ProcessIDs    []int
	ProcessTitles []string
	Issue         string
}

type CycleSafetyReport struct {
	Mode                      string
	Cycles                    []CycleAssessment
	UnresolvedCalls           []UnresolvedCallRisk
	InterprocessCycles        []InterprocessCycleRisk
	ReachableCallGraph        []processCallGraphEntry
	CycleRiskFingerprint      string
	UnresolvedRiskFingerprint string
}

type safetyGraph struct {
	byID      map[string]processNode
	adj       map[string][]string
	pred      map[string][]string
	reachable map[string]bool
	order     map[string]int
	startID   string
}

func buildSafetyGraph(nodes []processNode) safetyGraph {
	g := safetyGraph{
		byID:      make(map[string]processNode, len(nodes)),
		adj:       make(map[string][]string, len(nodes)),
		pred:      make(map[string][]string, len(nodes)),
		reachable: make(map[string]bool, len(nodes)),
		order:     make(map[string]int, len(nodes)),
	}
	var startIDs []string
	for i, n := range nodes {
		g.byID[n.id] = n
		g.order[n.id] = i
		if n.objType == 1 {
			startIDs = append(startIDs, n.id)
			if g.startID == "" {
				g.startID = n.id
			}
		}
	}
	for _, n := range nodes {
		seen := map[string]bool{}
		add := func(to string) {
			if to == "" || seen[to] {
				return
			}
			if _, ok := g.byID[to]; !ok {
				return
			}
			seen[to] = true
			g.adj[n.id] = append(g.adj[n.id], to)
			g.pred[to] = append(g.pred[to], n.id)
		}
		for _, lg := range n.logics {
			to, _ := lg["to_node_id"].(string)
			add(to)
			errTo, _ := lg["err_node_id"].(string)
			add(errTo)
		}
		for _, sem := range n.sems {
			to, _ := sem["to_node_id"].(string)
			add(to)
			esc, _ := sem["esc_node_id"].(string)
			add(esc)
		}
	}
	if len(startIDs) > 0 {
		stack := append([]string(nil), startIDs...)
		for _, id := range startIDs {
			g.reachable[id] = true
		}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, to := range g.adj[id] {
				if !g.reachable[to] {
					g.reachable[to] = true
					stack = append(stack, to)
				}
			}
		}
	} else {
		for id := range g.byID {
			g.reachable[id] = true
		}
	}
	return g
}

func reachableProcessNodes(nodes []processNode) []processNode {
	g := buildSafetyGraph(nodes)
	if g.startID == "" {
		return nodes
	}
	out := make([]processNode, 0, len(g.reachable))
	for _, node := range nodes {
		if g.reachable[node.id] {
			out = append(out, node)
		}
	}
	return out
}

func policyProcessEntries(root string) []processEntry {
	var entries []processEntry
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Hidden directories contain plugin metadata, git mirrors, caches, or
			// snapshots rather than the deployable stage graph. Indexing a copied
			// .conv.json from one of them can manufacture duplicate IDs or false
			// inter-process cycles.
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(d.Name(), ".conv.json") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var meta struct {
			Title string `json:"title"`
			ObjID int    `json:"obj_id"`
		}
		if json.Unmarshal(data, &meta) != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		entries = append(entries, processEntry{Title: meta.Title, ObjID: meta.ObjID, Path: filepath.ToSlash(rel)})
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func reachableStronglyConnectedComponents(g safetyGraph) [][]string {
	index := 0
	indices := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]string
	var visit func(string)
	visit = func(v string) {
		indices[v] = index
		low[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range g.adj[v] {
			if !g.reachable[w] {
				continue
			}
			if _, ok := indices[w]; !ok {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && indices[w] < low[v] {
				low[v] = indices[w]
			}
		}
		if low[v] != indices[v] {
			return
		}
		var component []string
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		isCycle := len(component) > 1
		if len(component) == 1 {
			for _, to := range g.adj[component[0]] {
				if to == component[0] {
					isCycle = true
					break
				}
			}
		}
		if isCycle {
			sort.Slice(component, func(i, j int) bool { return g.order[component[i]] < g.order[component[j]] })
			out = append(out, component)
		}
	}
	ids := make([]string, 0, len(g.reachable))
	for id := range g.reachable {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return g.order[ids[i]] < g.order[ids[j]] })
	for _, id := range ids {
		if _, ok := indices[id]; !ok {
			visit(id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return g.order[out[i][0]] < g.order[out[j][0]] })
	return out
}

func subgraphHasCycle(g safetyGraph, component map[string]bool, removed map[string]bool) bool {
	color := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		if removed[id] {
			return false
		}
		color[id] = 1
		for _, to := range g.adj[id] {
			if !component[to] || removed[to] {
				continue
			}
			if color[to] == 1 {
				return true
			}
			if color[to] == 0 && visit(to) {
				return true
			}
		}
		color[id] = 2
		return false
	}
	for id := range component {
		if !removed[id] && color[id] == 0 && visit(id) {
			return true
		}
	}
	return false
}

func nodeHitsEveryCycle(g safetyGraph, component map[string]bool, nodeID string) bool {
	return !subgraphHasCycle(g, component, map[string]bool{nodeID: true})
}

func nodesHitEveryCycle(g safetyGraph, component map[string]bool, nodeIDs map[string]bool) bool {
	return len(nodeIDs) > 0 && !subgraphHasCycle(g, component, nodeIDs)
}

type dominatorSets struct {
	idom map[string]string
}

func (d dominatorSets) candidates(nodeID string) []string {
	if _, ok := d.idom[nodeID]; !ok {
		return nil
	}
	var reversed []string
	for id := nodeID; id != dominatorVirtualEntry; id = d.idom[id] {
		reversed = append(reversed, id)
		if next, ok := d.idom[id]; !ok || next == id {
			return nil
		}
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

const dominatorVirtualEntry = "\x00corezoid-policy-entry"

func graphDominators(g safetyGraph) dominatorSets {
	result := dominatorSets{idom: map[string]string{}}
	var starts []string
	for id, node := range g.byID {
		if g.reachable[id] && node.objType == 1 {
			starts = append(starts, id)
		}
	}
	if len(starts) == 0 {
		return result
	}
	sort.Slice(starts, func(i, j int) bool { return g.order[starts[i]] < g.order[starts[j]] })

	// Reverse postorder makes the iterative data-flow solution converge in one
	// pass for chains and quickly for ordinary reducible process graphs. The
	// previous random map order could need O(V) full passes on a chain.
	visited := map[string]bool{}
	var postorder []string
	var visit func(string)
	visit = func(id string) {
		if visited[id] || !g.reachable[id] {
			return
		}
		visited[id] = true
		for _, next := range g.adj[id] {
			visit(next)
		}
		postorder = append(postorder, id)
	}
	for _, start := range starts {
		visit(start)
	}
	for left, right := 0, len(postorder)-1; left < right; left, right = left+1, right-1 {
		postorder[left], postorder[right] = postorder[right], postorder[left]
	}
	order := map[string]int{dominatorVirtualEntry: 0}
	for index, id := range postorder {
		order[id] = index + 1
	}

	entry := map[string]bool{}
	for _, start := range starts {
		entry[start] = true
	}
	result.idom[dominatorVirtualEntry] = dominatorVirtualEntry

	intersect := func(left, right string) string {
		for left != right {
			for order[left] > order[right] {
				left = result.idom[left]
			}
			for order[right] > order[left] {
				right = result.idom[right]
			}
		}
		return left
	}

	changed := true
	for changed {
		changed = false
		for _, id := range postorder {
			var predecessors []string
			if entry[id] {
				predecessors = append(predecessors, dominatorVirtualEntry)
			}
			for _, predecessor := range g.pred[id] {
				if g.reachable[predecessor] {
					predecessors = append(predecessors, predecessor)
				}
			}
			newIDom := ""
			for _, predecessor := range predecessors {
				if _, known := result.idom[predecessor]; !known {
					continue
				}
				if newIDom == "" {
					newIDom = predecessor
				} else {
					newIDom = intersect(predecessor, newIDom)
				}
			}
			if newIDom != "" && result.idom[id] != newIDom {
				result.idom[id] = newIDom
				changed = true
			}
		}
	}
	return result
}

func graphCanReach(g safetyGraph, from, target string) bool {
	if from == "" || target == "" || !g.reachable[from] || !g.reachable[target] {
		return false
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == target {
			return true
		}
		for _, next := range g.adj[id] {
			if g.reachable[next] && !seen[next] {
				seen[next] = true
				stack = append(stack, next)
			}
		}
	}
	return false
}

func normalizeVariableRef(v interface{}) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "{{"), "}}"))
	}
	return s
}

func numericLiteral(v interface{}) (float64, bool) {
	var value float64
	switch n := v.(type) {
	case float64:
		value = n
	case int:
		value = float64(n)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, false
		}
		value = f
	default:
		return 0, false
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func comparisonLogics(n processNode) (map[string]interface{}, string, bool) {
	var conditional map[string]interface{}
	defaultTarget := ""
	conditionalCount := 0
	defaultCount := 0
	unsupported := false
	for _, lg := range n.logics {
		t, _ := lg["type"].(string)
		switch t {
		case "go_if_const":
			conditional = lg
			conditionalCount++
		case "go":
			defaultTarget, _ = lg["to_node_id"].(string)
			defaultCount++
		default:
			unsupported = true
		}
	}
	return conditional, defaultTarget, !unsupported && conditionalCount == 1 && defaultCount == 1 && conditional != nil && defaultTarget != ""
}

func singleCondition(logic map[string]interface{}) (map[string]interface{}, bool) {
	conditions := toMapSlice(logic["conditions"])
	if len(conditions) != 1 {
		return nil, false
	}
	return conditions[0], true
}

func isLessFun(fun string) bool {
	switch strings.ToLower(fun) {
	case "less", "less_or_eq", "lt", "le", "lte":
		return true
	default:
		return false
	}
}

func isLessOrEqualFun(fun string) bool {
	switch strings.ToLower(fun) {
	case "less_or_eq", "le", "lte":
		return true
	default:
		return false
	}
}

func isGreaterFun(fun string) bool {
	switch strings.ToLower(fun) {
	case "more", "more_or_eq", "gt", "ge", "gte":
		return true
	default:
		return false
	}
}

func isStrictGreaterFun(fun string) bool {
	switch strings.ToLower(fun) {
	case "more", "gt":
		return true
	default:
		return false
	}
}

func isIncrementExpression(v interface{}, variable string) bool {
	s, ok := v.(string)
	if !ok || variable == "" {
		return false
	}
	clean := strings.ToLower(strings.ReplaceAll(s, " ", ""))
	clean = strings.ReplaceAll(clean, "{{", "")
	clean = strings.ReplaceAll(clean, "}}", "")
	if !strings.Contains(clean, "$.math(") {
		return false
	}
	q := regexp.QuoteMeta(strings.ToLower(variable))
	re := regexp.MustCompile(`^\$\.math\((?:` + q + `\+([1-9][0-9]*)|([1-9][0-9]*)\+` + q + `)\)$`)
	m := re.FindStringSubmatch(clean)
	if len(m) == 3 {
		return m[1] != "" || m[2] != ""
	}
	return false
}

func setParamValues(n processNode, variable string) []interface{} {
	var values []interface{}
	for _, lg := range n.logics {
		if t, _ := lg["type"].(string); t != "set_param" {
			continue
		}
		extra, _ := lg["extra"].(map[string]interface{})
		v, ok := extra[variable]
		if ok {
			values = append(values, v)
		}
	}
	return values
}

func nodeMayWriteVariableOpaque(n processNode, variable string) bool {
	for _, logic := range n.logics {
		t, _ := logic["type"].(string)
		switch t {
		case "", "go", "go_if_const", "set_param", "api_queue", "api_rpc_reply":
			continue
		case "api":
			response, _ := logic["response"].(map[string]interface{})
			for _, destination := range response {
				if normalizeVariableRef(destination) == variable {
					return true
				}
			}
		default:
			// Code, Call Process, DB/Form/Callback/Get Task, Git Call, Copy
			// Task, Sum, and unknown future actions may merge data into the
			// current task. Without an exact output map, none can prove that a
			// loop guard variable remains monotonic or a deadline remains stable.
			return true
		}
	}
	return false
}

func safeAssignmentsAfterInitializer(g safetyGraph, component map[string]bool, initializerID, guardID, variable string, valid func(interface{}) bool) bool {
	for id, node := range g.byID {
		if component[id] || !g.reachable[id] || !graphCanReach(g, initializerID, id) || !graphCanReach(g, id, guardID) {
			continue
		}
		if nodeMayWriteVariableOpaque(node, variable) {
			return false
		}
		for _, value := range setParamValues(node, variable) {
			if !valid(value) {
				return false
			}
		}
	}
	return true
}

func countBoundForCycle(g safetyGraph, ids []string, policy CycleSafetyPolicy, dominators dominatorSets) (string, bool) {
	component := map[string]bool{}
	for _, id := range ids {
		component[id] = true
	}
	for _, guardID := range ids {
		logic, defaultTarget, ok := comparisonLogics(g.byID[guardID])
		if !ok {
			continue
		}
		cond, ok := singleCondition(logic)
		if !ok {
			continue
		}
		if cast, _ := cond["cast"].(string); cast != "number" {
			continue
		}
		variable := normalizeVariableRef(cond["param"])
		limit, ok := numericLiteral(cond["const"])
		if !ok || variable == "" || limit < 1 {
			continue
		}
		fun, _ := cond["fun"].(string)
		fun = strings.ToLower(fun)
		conditionalTarget, _ := logic["to_node_id"].(string)
		continuesWhenTrue := component[conditionalTarget] && !component[defaultTarget]
		exitsWhenTrue := !component[conditionalTarget] && component[defaultTarget]
		if !(continuesWhenTrue && isLessFun(fun)) && !(exitsWhenTrue && isGreaterFun(fun)) {
			continue
		}
		if !nodeHitsEveryCycle(g, component, guardID) {
			continue
		}
		effectiveLimit := math.Ceil(limit)
		if (continuesWhenTrue && isLessOrEqualFun(fun)) || (exitsWhenTrue && isStrictGreaterFun(fun)) {
			effectiveLimit = math.Floor(limit) + 1
		}
		if effectiveLimit > float64(policy.MaxIterations) {
			continue
		}

		incrementNodes := map[string]bool{}
		unsafeAssignment := false
		for _, id := range ids {
			if nodeMayWriteVariableOpaque(g.byID[id], variable) {
				unsafeAssignment = true
			}
			for _, value := range setParamValues(g.byID[id], variable) {
				if isIncrementExpression(value, variable) {
					incrementNodes[id] = true
				} else {
					unsafeAssignment = true
				}
			}
		}
		if unsafeAssignment || !nodesHitEveryCycle(g, component, incrementNodes) {
			continue
		}

		initialized := false
		for _, candidate := range dominators.candidates(guardID) {
			if component[candidate] {
				continue
			}
			values := setParamValues(g.byID[candidate], variable)
			if len(values) == 0 {
				continue
			}
			validInitializerValue := func(value interface{}) bool {
				initial, literal := numericLiteral(value)
				return literal && initial >= 0 && initial < limit
			}
			validInitializer := true
			for _, value := range values {
				if !validInitializerValue(value) {
					validInitializer = false
					break
				}
			}
			if validInitializer && safeAssignmentsAfterInitializer(g, component, candidate, guardID, variable, validInitializerValue) {
				initialized = true
				break
			}
		}
		if !initialized {
			continue
		}
		return fmt.Sprintf("counter %q has a finite limit of %s", variable, strconv.FormatFloat(effectiveLimit, 'f', -1, 64)), true
	}
	return "", false
}

func currentTimeExpression(value interface{}) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	clean := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	clean = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(clean, "{{"), "}}"))
	return clean == "$.unixtime" || clean == "$.unixtime()" || clean == "root.change_time"
}

func operandIsCurrentTime(g safetyGraph, component map[string]bool, operand string) bool {
	if currentTimeExpression(operand) {
		return true
	}
	if operand == "" {
		return false
	}
	refreshNodes := map[string]bool{}
	for id := range component {
		if nodeMayWriteVariableOpaque(g.byID[id], operand) {
			return false
		}
		values := setParamValues(g.byID[id], operand)
		if len(values) == 0 {
			continue
		}
		allCurrentTime := true
		for _, value := range values {
			if !currentTimeExpression(value) {
				allCurrentTime = false
				break
			}
		}
		if allCurrentTime {
			refreshNodes[id] = true
		} else {
			return false
		}
	}
	return nodesHitEveryCycle(g, component, refreshNodes)
}

func deadlineDurationSeconds(value interface{}, maxDuration int) (int, bool) {
	if absolute, ok := numericLiteral(value); ok {
		delta := absolute - float64(time.Now().Unix())
		if delta <= 0 {
			return 0, true
		}
		if delta > float64(maxDuration) {
			return 0, false
		}
		return int(math.Ceil(delta)), true
	}
	s, ok := value.(string)
	if !ok {
		return 0, false
	}
	clean := strings.ToLower(strings.ReplaceAll(s, " ", ""))
	clean = strings.ReplaceAll(clean, "{{", "")
	clean = strings.ReplaceAll(clean, "}}", "")
	now := `(?:\$\.unixtime(?:\(\))?|root\.change_time)`
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^\$\.math\(` + now + `\+([0-9]+)\)$`),
		regexp.MustCompile(`^\$\.math\(([0-9]+)\+` + now + `\)$`),
	}
	for _, re := range patterns {
		match := re.FindStringSubmatch(clean)
		if len(match) != 2 {
			continue
		}
		duration, err := strconv.Atoi(match[1])
		return duration, err == nil && duration <= maxDuration
	}
	return 0, false
}

func deadlineIsStable(g safetyGraph, component map[string]bool, guardID, operand string, policy CycleSafetyPolicy, dominators dominatorSets) (int, bool) {
	if _, ok := numericLiteral(operand); ok {
		return deadlineDurationSeconds(operand, policy.MaxDurationSeconds)
	}
	if operand == "" || operandIsCurrentTime(g, component, operand) {
		return 0, false
	}
	for id := range component {
		if len(setParamValues(g.byID[id], operand)) > 0 {
			return 0, false
		}
		if nodeMayWriteVariableOpaque(g.byID[id], operand) {
			return 0, false
		}
	}
	for _, candidate := range dominators.candidates(guardID) {
		if component[candidate] {
			continue
		}
		values := setParamValues(g.byID[candidate], operand)
		if len(values) == 0 {
			continue
		}
		validDeadlineValue := func(value interface{}) bool {
			_, bounded := deadlineDurationSeconds(value, policy.MaxDurationSeconds)
			return bounded
		}
		maxProven := 0
		allBounded := true
		for _, value := range values {
			duration, bounded := deadlineDurationSeconds(value, policy.MaxDurationSeconds)
			if !bounded {
				allBounded = false
				break
			}
			if duration > maxProven {
				maxProven = duration
			}
		}
		if allBounded && safeAssignmentsAfterInitializer(g, component, candidate, guardID, operand, validDeadlineValue) {
			return maxProven, true
		}
	}
	return 0, false
}

func deadlineBoundForCycle(g safetyGraph, ids []string, policy CycleSafetyPolicy, dominators dominatorSets) (string, bool) {
	component := map[string]bool{}
	for _, id := range ids {
		component[id] = true
	}
	for _, guardID := range ids {
		logic, defaultTarget, ok := comparisonLogics(g.byID[guardID])
		if !ok {
			continue
		}
		cond, ok := singleCondition(logic)
		if !ok {
			continue
		}
		if cast, _ := cond["cast"].(string); cast != "number" {
			continue
		}
		fun, _ := cond["fun"].(string)
		lhs := normalizeVariableRef(cond["param"])
		rhs := normalizeVariableRef(cond["const"])
		conditionalTarget, _ := logic["to_node_id"].(string)
		continuesWhenTrue := component[conditionalTarget] && !component[defaultTarget]
		exitsWhenTrue := !component[conditionalTarget] && component[defaultTarget]

		lhsNow := operandIsCurrentTime(g, component, lhs)
		rhsNow := operandIsCurrentTime(g, component, rhs)
		deadline := ""
		beforeDeadline := false
		expired := false
		switch {
		case lhsNow && !rhsNow:
			deadline = rhs
			beforeDeadline = isLessFun(fun)
			expired = isGreaterFun(fun)
		case rhsNow && !lhsNow:
			deadline = lhs
			beforeDeadline = isGreaterFun(fun)
			expired = isLessFun(fun)
		default:
			continue
		}
		if !(continuesWhenTrue && beforeDeadline) && !(exitsWhenTrue && expired) {
			continue
		}
		if !nodeHitsEveryCycle(g, component, guardID) {
			continue
		}
		duration, bounded := deadlineIsStable(g, component, guardID, deadline, policy, dominators)
		if !bounded {
			continue
		}
		return fmt.Sprintf("every iteration is bounded by deadline %q within %d seconds", deadline, duration), true
	}
	return "", false
}

func cycleDelayCoverage(g safetyGraph, ids []string) bool {
	component := map[string]bool{}
	delays := map[string]bool{}
	for _, id := range ids {
		component[id] = true
	}
	for _, id := range ids {
		node := g.byID[id]
		// A time semaphore attached to API/Call/Code is a timeout, not proof
		// that every retry waits. Only a dedicated semaphore-only Delay node is
		// accepted as pacing.
		if len(node.logics) != 0 {
			continue
		}
		hasCycleEdge := false
		allCycleEdgesDelayed := true
		for _, sem := range node.sems {
			to, _ := sem["to_node_id"].(string)
			esc, _ := sem["esc_node_id"].(string)
			if !component[to] && !component[esc] {
				continue
			}
			hasCycleEdge = true
			if t, _ := sem["type"].(string); t != "time" {
				allCycleEdgesDelayed = false
				break
			}
			if n, ok := numericLiteral(sem["value"]); ok {
				multiplier := float64(0)
				switch strings.ToLower(strings.TrimSpace(fmt.Sprint(sem["dimension"]))) {
				case "sec", "second", "seconds":
					multiplier = 1
				case "min", "minute", "minutes":
					multiplier = 60
				case "hour", "hours":
					multiplier = 3600
				case "day", "days":
					multiplier = 86400
				}
				if multiplier == 0 || n*multiplier < 30 {
					allCycleEdgesDelayed = false
					break
				}
			} else {
				// Dynamic values remain unproven because they may resolve to now or
				// to the past.
				allCycleEdgesDelayed = false
				break
			}
		}
		if hasCycleEdge && allCycleEdgesDelayed {
			delays[id] = true
		}
	}
	return nodesHitEveryCycle(g, component, delays)
}

func cycleHasExternalAction(g safetyGraph, ids []string) bool {
	external := map[string]bool{
		"api": true, "api_rpc": true, "api_copy": true, "api_git": true,
		"git_call": true, "db_call": true,
	}
	for _, id := range ids {
		for _, lg := range g.byID[id].logics {
			t, _ := lg["type"].(string)
			if isActiveStubNode(g.byID[id]) && t == "api_rpc" {
				continue
			}
			if external[t] {
				return true
			}
		}
	}
	return false
}

func assessCycle(g safetyGraph, ids []string, policy CycleSafetyPolicy, dominators dominatorSets) CycleAssessment {
	a := CycleAssessment{NodeIDs: append([]string(nil), ids...)}
	for _, id := range ids {
		title := g.byID[id].title
		if title == "" {
			title = "(untitled)"
		}
		a.NodeTitles = append(a.NodeTitles, title)
	}
	a.HasDelay = cycleDelayCoverage(g, ids)
	if detail, ok := countBoundForCycle(g, ids, policy, dominators); ok {
		a.Bounded = true
		a.BoundKind = "count"
		a.BoundDetail = detail
	} else if detail, ok := deadlineBoundForCycle(g, ids, policy, dominators); ok {
		a.Bounded = true
		a.BoundKind = "deadline"
		a.BoundDetail = detail
	} else {
		a.Issue = "no finite counter or stable deadline guard was proven on every path around the cycle"
		return a
	}
	if policy.RequireRetryDelay && a.BoundKind == "deadline" && !a.HasDelay {
		a.Bounded = false
		a.Issue = "a deadline bound exists, but the cycle has no Delay on every iteration; wall-clock duration alone does not bound tact consumption"
	} else if policy.RequireRetryDelay && cycleHasExternalAction(g, ids) && !a.HasDelay {
		a.Bounded = false
		a.Issue = "a finite bound exists, but an external-call retry cycle has no Delay on every iteration"
	}
	return a
}

func isProcessInvocation(logic map[string]interface{}) bool {
	t, _ := logic["type"].(string)
	if t == "api_rpc" {
		return true
	}
	if t != "api_copy" {
		return false
	}
	mode, _ := logic["mode"].(string)
	return mode != "modify"
}

func staticProcessTarget(logic map[string]interface{}) (int, bool) {
	if !isProcessInvocation(logic) {
		return 0, false
	}
	switch value := logic["conv_id"].(type) {
	case float64:
		maxInt := int(^uint(0) >> 1)
		if value <= 0 || value != math.Trunc(value) || value >= float64(maxInt) {
			return 0, false
		}
		return int(value), true
	case int:
		return value, value > 0
	case string:
		value = strings.TrimSpace(value)
		if value == "" || strings.HasPrefix(value, "@") || strings.Contains(value, "{{") {
			return 0, false
		}
		target, err := strconv.Atoi(value)
		return target, err == nil && target > 0
	default:
		return 0, false
	}
}

func hasExplicitProcessScope(logic map[string]interface{}) bool {
	for _, field := range []string{"project_id", "stage_id"} {
		switch value := logic[field].(type) {
		case float64:
			if value != 0 {
				return true
			}
		case int:
			if value != 0 {
				return true
			}
		case string:
			if strings.TrimSpace(value) != "" && strings.TrimSpace(value) != "0" {
				return true
			}
		}
	}
	return false
}

type localProcessInspection struct {
	Count       int
	Inspectable bool
}

func findUnresolvedCallRisks(nodes []processNode, local map[int]localProcessInspection, currentID int) []UnresolvedCallRisk {
	var out []UnresolvedCallRisk
	if currentID > 0 {
		local[currentID] = localProcessInspection{Count: 1, Inspectable: true}
	}
	for _, n := range nodes {
		if isActiveStubNode(n) {
			continue
		}
		for _, lg := range n.logics {
			if !isProcessInvocation(lg) {
				continue
			}
			t, _ := lg["type"].(string)
			convID := fmt.Sprint(lg["conv_id"])
			issue := ""
			switch {
			case hasExplicitProcessScope(lg):
				issue = "an explicit project_id or stage_id targets a scope outside the locally indexed stage; the target call graph cannot be matched safely"
			case strings.Contains(convID, "{{"):
				issue = "the runtime target cannot be inspected statically; a recursive call chain may consume additional tacts and exhaust the project budget"
			case strings.HasPrefix(strings.TrimSpace(convID), "@"):
				issue = "the alias target is not resolved by the local export; a recursive call chain cannot be excluded statically"
			default:
				target, static := staticProcessTarget(lg)
				inspection, found := local[target]
				if static && !found {
					issue = fmt.Sprintf("target process %d is absent from the local export; a recursive call chain cannot be excluded statically", target)
				} else if static && inspection.Count > 1 {
					issue = fmt.Sprintf("target process %d appears in multiple local exports; its call graph is ambiguous and recursion cannot be excluded statically", target)
				} else if static && !inspection.Inspectable {
					issue = fmt.Sprintf("target process %d has no readable process graph in the local export; recursion cannot be excluded statically", target)
				} else if !static {
					issue = "the process target cannot be resolved statically; a recursive call chain cannot be excluded"
				}
			}
			if issue == "" {
				continue
			}
			title := n.title
			if title == "" {
				title = "(untitled)"
			}
			out = append(out, UnresolvedCallRisk{
				NodeID: n.id, NodeTitle: title, LogicType: t, ConvID: convID,
				Issue: issue,
			})
		}
	}
	return out
}

type inspectableProcessGraph struct {
	ID      int
	Title   string
	Nodes   []processNode
	Count   int
	IsValid bool
}

func loadInspectableProcessGraphs(root string) map[int]inspectableProcessGraph {
	graphs := map[int]inspectableProcessGraph{}
	for _, entry := range policyProcessEntries(root) {
		if entry.ObjID <= 0 {
			continue
		}
		graph := graphs[entry.ObjID]
		graph.ID = entry.ObjID
		graph.Count++
		if graph.Title == "" {
			graph.Title = entry.Title
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err == nil {
			var proc map[string]interface{}
			if json.Unmarshal(data, &proc) == nil {
				if rawNodes, nodeErr := getNodes(proc); nodeErr == nil {
					graph.Nodes = parseProcessNodes(rawNodes)
					graph.IsValid = true
				}
			}
		}
		graphs[entry.ObjID] = graph
	}
	for id, graph := range graphs {
		if graph.Count != 1 {
			graph.IsValid = false
			graph.Nodes = nil
			graphs[id] = graph
		}
	}
	return graphs
}

func findTransitiveUnresolvedCallRisks(nodes []processNode, root string, currentID int, currentTitle string) ([]UnresolvedCallRisk, []processCallGraphEntry) {
	graphs := loadInspectableProcessGraphs(root)
	graphs[currentID] = inspectableProcessGraph{
		ID: currentID, Title: currentTitle, Nodes: nodes, Count: 1, IsValid: true,
	}
	queue := []int{currentID}
	visited := map[int]bool{}
	local := make(map[int]localProcessInspection, len(graphs))
	for id, graph := range graphs {
		local[id] = localProcessInspection{Count: graph.Count, Inspectable: graph.IsValid}
	}
	var out []UnresolvedCallRisk
	var evidence []processCallGraphEntry
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		graph := graphs[id]
		if !graph.IsValid {
			continue
		}
		targets := staticProcessTargets(graph.Nodes)
		evidence = append(evidence, processCallGraphEntry{ID: id, Title: graph.Title, Targets: targets})
		for _, risk := range findUnresolvedCallRisks(reachableProcessNodes(graph.Nodes), local, id) {
			risk.SourceProcessID = id
			risk.SourceProcessTitle = graph.Title
			out = append(out, risk)
		}
		for _, target := range targets {
			if targetGraph, ok := graphs[target]; ok && targetGraph.IsValid && !visited[target] {
				queue = append(queue, target)
			}
		}
	}
	return out, evidence
}

type processCallGraphEntry struct {
	ID      int
	Title   string
	Targets []int
}

func staticProcessTargets(nodes []processNode) []int {
	seen := map[int]bool{}
	var targets []int
	for _, n := range reachableProcessNodes(nodes) {
		if isActiveStubNode(n) {
			continue
		}
		for _, logic := range n.logics {
			if hasExplicitProcessScope(logic) {
				continue
			}
			target, ok := staticProcessTarget(logic)
			if ok && !seen[target] {
				seen[target] = true
				targets = append(targets, target)
			}
		}
	}
	sort.Ints(targets)
	return targets
}

func findInterprocessCycleRisks(root string, currentID int, currentTitle string, currentNodes []processNode) []InterprocessCycleRisk {
	entries := map[int]processCallGraphEntry{}
	for _, entry := range policyProcessEntries(root) {
		if entry.ObjID == 0 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err != nil {
			continue
		}
		var proc map[string]interface{}
		if json.Unmarshal(data, &proc) != nil {
			continue
		}
		rawNodes, err := getNodes(proc)
		if err != nil {
			continue
		}
		targets := staticProcessTargets(parseProcessNodes(rawNodes))
		existing := entries[entry.ObjID]
		if existing.ID == 0 {
			existing = processCallGraphEntry{ID: entry.ObjID, Title: entry.Title}
		}
		if existing.Title == "" {
			existing.Title = entry.Title
		}
		seen := map[int]bool{}
		for _, target := range existing.Targets {
			seen[target] = true
		}
		for _, target := range targets {
			if !seen[target] {
				existing.Targets = append(existing.Targets, target)
				seen[target] = true
			}
		}
		sort.Ints(existing.Targets)
		entries[entry.ObjID] = existing
	}
	if currentTitle == "" {
		currentTitle = entries[currentID].Title
	}
	entries[currentID] = processCallGraphEntry{ID: currentID, Title: currentTitle, Targets: staticProcessTargets(currentNodes)}
	return interprocessCycleRisks(entries, currentID)
}

// interprocessCycleRisks finds every reachable recursive component in O(V+E)
// with one Tarjan SCC pass. Enumerating simple paths is exponential even on a
// DAG and can hang lint/push for ordinary branching process-call graphs.
func interprocessCycleRisks(entries map[int]processCallGraphEntry, currentID int) []InterprocessCycleRisk {
	reachable := map[int]bool{currentID: true}
	queue := []int{currentID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, target := range entries[id].Targets {
			if !reachable[target] {
				reachable[target] = true
				queue = append(queue, target)
			}
		}
	}
	components := reachableProcessCallSCCs(entries, reachable)
	risks := make([]InterprocessCycleRisk, 0, len(components))
	for _, component := range components {
		found := processCallCyclePath(entries, component)
		if len(found) == 0 {
			continue
		}
		titles := make([]string, 0, len(found))
		for _, id := range found {
			title := entries[id].Title
			if title == "" {
				title = fmt.Sprintf("process %d", id)
			}
			titles = append(titles, title)
		}
		risks = append(risks, InterprocessCycleRisk{
			ProcessIDs: found, ProcessTitles: titles,
			Issue: "static Call Process or Copy Task create targets form a recursive process chain whose total tact cost is not locally bounded",
		})
	}
	return risks
}

func reachableProcessCallSCCs(entries map[int]processCallGraphEntry, reachable map[int]bool) [][]int {
	index := 0
	indices := map[int]int{}
	low := map[int]int{}
	onStack := map[int]bool{}
	var stack []int
	var out [][]int
	var visit func(int)
	visit = func(id int) {
		indices[id] = index
		low[id] = index
		index++
		stack = append(stack, id)
		onStack[id] = true
		for _, next := range entries[id].Targets {
			if !reachable[next] {
				continue
			}
			if _, seen := indices[next]; !seen {
				visit(next)
				if low[next] < low[id] {
					low[id] = low[next]
				}
			} else if onStack[next] && indices[next] < low[id] {
				low[id] = indices[next]
			}
		}
		if low[id] != indices[id] {
			return
		}
		var component []int
		for {
			next := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[next] = false
			component = append(component, next)
			if next == id {
				break
			}
		}
		cyclic := len(component) > 1
		if len(component) == 1 {
			for _, next := range entries[component[0]].Targets {
				if next == component[0] {
					cyclic = true
					break
				}
			}
		}
		if cyclic {
			sort.Ints(component)
			out = append(out, component)
		}
	}

	ids := make([]int, 0, len(reachable))
	for id := range reachable {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if _, seen := indices[id]; !seen {
			visit(id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// processCallCyclePath returns one concrete edge-valid cycle for an SCC. The
// SCC itself is the risk unit; one representative path keeps reports stable
// without enumerating the potentially exponential set of simple cycles.
func processCallCyclePath(entries map[int]processCallGraphEntry, component []int) []int {
	if len(component) == 0 {
		return nil
	}
	inComponent := make(map[int]bool, len(component))
	for _, id := range component {
		inComponent[id] = true
	}
	start := component[0]
	for _, next := range entries[start].Targets {
		if !inComponent[next] {
			continue
		}
		if next == start {
			return []int{start, start}
		}
		if tail, ok := processCallPath(entries, next, start, inComponent, map[int]bool{}); ok {
			return append([]int{start}, tail...)
		}
	}
	return nil
}

func processCallPath(entries map[int]processCallGraphEntry, current, target int, allowed, visited map[int]bool) ([]int, bool) {
	if current == target {
		return []int{target}, true
	}
	if visited[current] || !allowed[current] {
		return nil, false
	}
	visited[current] = true
	for _, next := range entries[current].Targets {
		if !allowed[next] {
			continue
		}
		if tail, ok := processCallPath(entries, next, target, allowed, visited); ok {
			return append([]int{current}, tail...), true
		}
	}
	return nil, false
}

func safetyFingerprint(category string, value interface{}) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(append([]byte(category+"\n"), b...))
	return "sha256:" + fmt.Sprintf("%x", sum[:])
}

func analyzeCycleSafety(proc map[string]interface{}, nodes []processNode, policy EffectiveProjectPolicy) *CycleSafetyReport {
	if policy.CycleSafety.Mode == policyModeOff {
		return nil
	}
	g := buildSafetyGraph(nodes)
	dominators := graphDominators(g)
	report := &CycleSafetyReport{Mode: policy.CycleSafety.Mode}
	for _, ids := range reachableStronglyConnectedComponents(g) {
		report.Cycles = append(report.Cycles, assessCycle(g, ids, policy.CycleSafety, dominators))
	}
	currentID := 0
	switch id := proc["obj_id"].(type) {
	case float64:
		currentID = int(id)
	case int:
		currentID = id
	}
	currentTitle, _ := proc["title"].(string)
	report.UnresolvedCalls, report.ReachableCallGraph = findTransitiveUnresolvedCallRisks(nodes, policy.Root, currentID, currentTitle)
	report.InterprocessCycles = findInterprocessCycleRisks(policy.Root, currentID, currentTitle, nodes)
	var riskyCycles []CycleAssessment
	for _, cycle := range report.Cycles {
		if !cycle.Bounded {
			riskyCycles = append(riskyCycles, cycle)
		}
	}
	if report.Mode == policyModeStrict && len(riskyCycles)+len(report.InterprocessCycles) > 0 {
		report.CycleRiskFingerprint = safetyFingerprint("cycle-risk", struct {
			Cycles       []CycleAssessment
			Interprocess []InterprocessCycleRisk
			CallGraph    []processCallGraphEntry
			Scheme       interface{}
			ProcessID    interface{}
			Policy       CycleSafetyPolicy
		}{riskyCycles, report.InterprocessCycles, report.ReachableCallGraph, proc["scheme"], proc["obj_id"], policy.CycleSafety})
	}
	if report.Mode == policyModeStrict && len(report.UnresolvedCalls) > 0 {
		report.UnresolvedRiskFingerprint = safetyFingerprint("unresolved-call-risk", struct {
			Calls     []UnresolvedCallRisk
			CallGraph []processCallGraphEntry
			Scheme    interface{}
			ProcessID interface{}
			Policy    CycleSafetyPolicy
		}{report.UnresolvedCalls, report.ReachableCallGraph, proc["scheme"], proc["obj_id"], policy.CycleSafety})
	}
	return report
}

func cycleRiskCount(report *CycleSafetyReport) int {
	if report == nil {
		return 0
	}
	count := len(report.InterprocessCycles)
	for _, cycle := range report.Cycles {
		if !cycle.Bounded {
			count++
		}
	}
	return count
}
