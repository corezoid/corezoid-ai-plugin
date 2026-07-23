package main

// Auto-layout of Corezoid process nodes: assigns x/y neatly and without
// overlaps per the rules of docs/process/node-positioning-best-practices.md.
// A 1:1 Go port of the skill's former Python engine (layout_nodes.py).
//
// Two families of strategies, chosen automatically by analyzeLayout:
//
//  1. waterfall (layoutWaterfall) — chain/wings for simple tree-like
//     processes. The main flow is a vertical at x=500, y step 220; branches go
//     in columns around the axis; err clusters are pressed to the right;
//     IF/Delay get collapsed.
//  2. layered+error-rail (layoutPartitioned) — for LARGE/mesh processes where
//     1/2–2/3 of the nodes are error handling: business flow via Sugiyama-lite,
//     error clusters collapsed on a right rail, orphans in a grid.
//  3. waterfall+regions (layoutHybrid) — region composition: TABLE bundles of
//     isomorphic sibling pipelines and STAR fans are laid out as dedicated
//     aligned grids, the residual graph as a waterfall.
//
// Determinism: the engine never iterates a Go map where order affects
// placement — every such loop walks ids in scheme.nodes document order (the
// Python engine relied on dict insertion order; document order is the same
// thing made explicit).

import (
	"encoding/json"
	"sort"
)

const (
	layColX0            = 500
	layColStep          = 300
	layRowY0            = 100
	layRowStep          = 220
	layTimerExtra       = 120
	layCircleXOffset    = 100
	layCollapsedXOffset = 76 // a collapsed node is ~48px vs a 200px block → center on the axis
	layErrDX            = 300
)

// layDensityGaps maps a density mode to its (gapV, gapH) for the compaction
// pass. "roomy" is deliberately absent: it skips compaction and keeps the
// coarse block rhythm (presentation mode).
var layDensityGaps = map[string][2]int{
	"compact": {56, 72},
	"medium":  {90, 90},
}

type lpoint struct{ X, Y int }

// layoutEngine carries the per-run configuration so concurrent tool calls
// cannot interfere (the Python original used a module-level DENSITY global).
type layoutEngine struct {
	density string // "compact" | "medium" | "roomy"
}

// layoutGraph is the parsed edge structure of scheme.nodes. All slices are in
// document order; the maps are lookup-only.
type layoutGraph struct {
	nodes    []map[string]interface{}          // scheme.nodes, document order
	ids      []string                          // node ids, document order
	byID     map[string]map[string]interface{} //
	docIdx   map[string]int                    // id → index in nodes
	primary  map[string]string                 // single forward "go" edge ("" = none)
	branches map[string][]string               // go_if_const + extra semaphor targets
	errors   map[string][]string               // err_node_id targets (logics order)
}

func nodeLogics(n map[string]interface{}) []map[string]interface{} {
	cond, _ := n["condition"].(map[string]interface{})
	if cond == nil {
		return nil
	}
	raw, _ := cond["logics"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func nodeSemaphors(n map[string]interface{}) []map[string]interface{} {
	cond, _ := n["condition"].(map[string]interface{})
	if cond == nil {
		return nil
	}
	raw, _ := cond["semaphors"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func nodeStr(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}

// nodeObjType reads obj_type tolerating float64 (plain decode) and
// json.Number (UseNumber decode).
func nodeObjType(n map[string]interface{}) int {
	switch v := n["obj_type"].(type) {
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	case int:
		return v
	}
	return 0
}

func isCircle(n map[string]interface{}) bool {
	t := nodeObjType(n)
	return t == 1 || t == 2
}

// nodeExtraMap parses the node's extra field. In the wild extra is a JSON
// STRING ("{\"modeForm\":\"collapse\"}"), may be an object, null or absent;
// malformed content is treated as empty (mirrors the Python `except` guard).
func nodeExtraMap(n map[string]interface{}) map[string]interface{} {
	switch v := n["extra"].(type) {
	case string:
		if v == "" {
			return map[string]interface{}{}
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(v), &m); err != nil || m == nil {
			return map[string]interface{}{}
		}
		return m
	case map[string]interface{}:
		return v
	}
	return map[string]interface{}{}
}

func isCollapsedNode(n map[string]interface{}) bool {
	return nodeExtraMap(n)["modeForm"] == "collapse"
}

// collapseNode sets extra.modeForm="collapse", preserving sibling extra keys
// and writing the field back in the string shape the platform emits.
func collapseNode(n map[string]interface{}) {
	extra := nodeExtraMap(n)
	extra["modeForm"] = "collapse"
	b, err := json.Marshal(extra)
	if err != nil {
		return
	}
	n["extra"] = string(b)
}

// nodeBoxSize is the real visual size (w, h) of a node — the single source of
// truth for spacing decisions. Blocks 200x150 (timer blocks are taller),
// collapsed IF/Delay/err squares 48, Start/Final circles 56.
func nodeBoxSize(n map[string]interface{}) (int, int) {
	if isCircle(n) {
		return 56, 56
	}
	if isCollapsedNode(n) {
		return 48, 48
	}
	if len(nodeSemaphors(n)) > 0 {
		return 200, 270
	}
	return 200, 150
}

// nodeBox is the canvas box (x0, y0, x1, y1) honouring pivots: circles are
// centre-pivoted, everything else is top-left.
func nodeBox(n map[string]interface{}, x, y int) (int, int, int, int) {
	w, h := nodeBoxSize(n)
	if isCircle(n) {
		return x - w/2, y - h/2, x + w/2, y + h/2
	}
	return x, y, x + w, y + h
}

// buildLayoutGraph reconstructs the edge structure from scheme.nodes.
func buildLayoutGraph(nodes []map[string]interface{}) *layoutGraph {
	g := &layoutGraph{
		nodes:    nodes,
		ids:      make([]string, 0, len(nodes)),
		byID:     make(map[string]map[string]interface{}, len(nodes)),
		docIdx:   make(map[string]int, len(nodes)),
		primary:  make(map[string]string, len(nodes)),
		branches: make(map[string][]string, len(nodes)),
		errors:   make(map[string][]string, len(nodes)),
	}
	for i, n := range nodes {
		id := nodeStr(n, "id")
		g.ids = append(g.ids, id)
		g.byID[id] = n
		g.docIdx[id] = i
	}
	for _, n := range nodes {
		id := nodeStr(n, "id")
		var gos, conds []string
		var errs []string
		for _, lg := range nodeLogics(n) {
			to := nodeStr(lg, "to_node_id")
			switch nodeStr(lg, "type") {
			case "go":
				if _, ok := g.byID[to]; ok {
					gos = append(gos, to)
				}
			case "go_if_const":
				if _, ok := g.byID[to]; ok {
					conds = append(conds, to)
				}
			}
			if e := nodeStr(lg, "err_node_id"); e != "" {
				if _, ok := g.byID[e]; ok {
					errs = append(errs, e)
				}
			}
		}
		var sems []string
		for _, s := range nodeSemaphors(n) {
			if to := nodeStr(s, "to_node_id"); to != "" {
				if _, ok := g.byID[to]; ok {
					sems = append(sems, to)
				}
			}
			// count semaphors escalate via esc_node_id — an error edge, not a
			// flow edge: the escalation cluster belongs next to its owner.
			if esc := nodeStr(s, "esc_node_id"); esc != "" {
				if _, ok := g.byID[esc]; ok {
					errs = append(errs, esc)
				}
			}
		}
		main := ""
		if len(gos) > 0 {
			main = gos[len(gos)-1]
		} else if len(sems) > 0 {
			main, sems = sems[0], sems[1:]
		}
		if main != "" {
			g.primary[id] = main
		}
		br := append([]string{}, conds...)
		for _, s := range sems {
			if s != "" && s != main {
				br = append(br, s)
			}
		}
		g.branches[id] = br
		g.errors[id] = errs
	}
	return g
}

// succs is the forward successors: the primary go edge plus branches.
func (g *layoutGraph) succs(u string) []string {
	var out []string
	if p, ok := g.primary[u]; ok {
		out = append(out, p)
	}
	return append(out, g.branches[u]...)
}

// allOut is succs plus error edges (used by the sugiyama layering, where err
// edges also push handlers down).
func (g *layoutGraph) allOut(u string) []string {
	out := g.succs(u)
	for _, e := range g.errors[u] {
		if _, ok := g.byID[e]; ok {
			out = append(out, e)
		}
	}
	return out
}

// inDocOrder returns the members of a set in scheme.nodes document order —
// the engine's universal deterministic iteration for what Python did with
// dict/set iteration.
func (g *layoutGraph) inDocOrder(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for _, id := range g.ids {
		if set[id] {
			out = append(out, id)
		}
	}
	return out
}

// minID returns the lexicographically smallest member (Python's min(set)).
func minID(set map[string]bool) string {
	best := ""
	for id := range set {
		if best == "" || id < best {
			best = id
		}
	}
	return best
}

// floorDiv is Python's // for ints: floor division (Go's / truncates toward
// zero, which differs on negative operands — live risk on the ±10000 canvas).
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// pyMod is Python's % (result has the sign of the divisor).
func pyMod(a, b int) int {
	m := a % b
	if m != 0 && ((a < 0) != (b < 0)) {
		m += b
	}
	return m
}

// isPureRouter reports whether a node is a pure IF router (only go_if_const +
// go logics) or a pure Delay (only a time semaphore) — the collapse-to-small
// rule shared by all strategies.
func isPureRouter(n map[string]interface{}) bool {
	types := map[string]bool{}
	for _, lg := range nodeLogics(n) {
		types[nodeStr(lg, "type")] = true
	}
	within := func(allowed ...string) bool {
		ok := map[string]bool{}
		for _, a := range allowed {
			ok[a] = true
		}
		for t := range types {
			if !ok[t] {
				return false
			}
		}
		return true
	}
	if types["go_if_const"] && within("go", "go_if_const") {
		return true
	}
	if len(nodeSemaphors(n)) > 0 && within("go") {
		return true
	}
	return false
}

// sortByDoc sorts ids by document order (a stable deterministic base order).
func (g *layoutGraph) sortByDoc(ids []string) {
	sort.SliceStable(ids, func(i, j int) bool { return g.docIdx[ids[i]] < g.docIdx[ids[j]] })
}
