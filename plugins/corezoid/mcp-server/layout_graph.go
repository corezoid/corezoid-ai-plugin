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
//     existing collapse/expand state is preserved.
//  2. layered+error-rail (layoutPartitioned) — for LARGE/mesh processes where
//     1/2–2/3 of the nodes are error handling: business flow via Sugiyama-lite,
//     error clusters on a right rail, orphans in a grid.
//  3. waterfall+regions (layoutHybrid) — region composition: TABLE bundles of
//     isomorphic sibling pipelines, STAR fans and compact DIAMOND forks are
//     laid out with dedicated geometry, the residual graph as a waterfall.
//
// Determinism: the engine never iterates a Go map where order affects
// placement — every such loop walks ids in scheme.nodes document order (the
// Python engine relied on dict insertion order; document order is the same
// thing made explicit).

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
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
	// layMaxRailGap bounds how far a single error cluster may pull the
	// right-hand rail forward to stay aligned with a distant owner. Beyond
	// this, the rail stays packed instead of opening a multi-thousand-pixel
	// gap to chase an owner that sits far down the main flow.
	layMaxRailGap = layRowStep * 3
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

type errorTopology struct {
	Roots     int
	Dedicated int // roots referenced by exactly one business node
	Shared    int // roots referenced by two or more business nodes
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

// isConditionNode identifies a rendered Condition by its actual wiring, not
// obj_type. Production Corezoid JSON uses obj_type=0 for go_if_const routers
// and obj_type=3 for escalation/reply-style nodes, so obj_type alone labels
// both families incorrectly for sizing purposes.
func isConditionNode(n map[string]interface{}) bool {
	for _, lg := range nodeLogics(n) {
		if nodeStr(lg, "type") == "go_if_const" {
			return true
		}
	}
	return false
}

func hasLogicType(n map[string]interface{}, typ string) bool {
	for _, lg := range nodeLogics(n) {
		if nodeStr(lg, "type") == typ {
			return true
		}
	}
	return false
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
	setNodeMode(n, "collapse")
}

func expandNode(n map[string]interface{}) {
	setNodeMode(n, "expand")
}

const layoutPreserveModeKey = "__corezoid_layout_preserve_mode"

func setNodeMode(n map[string]interface{}, mode string) {
	if preserve, _ := n[layoutPreserveModeKey].(bool); preserve {
		return
	}
	extra := nodeExtraMap(n)
	extra["modeForm"] = mode
	b, err := json.Marshal(extra)
	if err != nil {
		return
	}
	n["extra"] = string(b)
}

// nodeBoxSize is the real visual size (w, h) of a node — the single source of
// truth for spacing decisions. Collapsed IF/Delay/err squares and Start/Final
// circles are a fixed 56x56 regardless of content; every other node is 200
// wide with a height that grows with its content (see estimatedExpandedHeight)
// — it never shrinks below, or overlaps past, what the platform actually
// renders.
func nodeBoxSize(n map[string]interface{}) (int, int) {
	if isCircle(n) {
		return 56, 56
	}
	if isCollapsedNode(n) {
		// Measured against the live UI (Corezoid v6.12) as a fixed 56x56 icon,
		// independent of title/branch/block count — all of that is hidden
		// while collapsed. Kept at the pre-existing 48 here (spacing decisions
		// only, not a rendering claim): several strategies key row pitch and
		// x-centering off this exact figure, and reconciling those is a
		// separate, purely-cosmetic change from the overlap fix this function
		// exists for.
		return 48, 48
	}
	return 200, estimatedExpandedHeight(n)
}

// estimatedExpandedHeight approximates the rendered height (px) of an
// EXPANDED node. The width never varies (always 200); the height grows for
// three independent, stackable reasons, each measured against the live
// Corezoid UI (v6.12) by editing a real node and reading its rendered
// getBoundingClientRect():
//
//   - Every extra output branch adds its own row, ~54-56px: an err_node_id on
//     any logic entry, a count semaphore ("Alert if the number of tasks in
//     the node queue reaches..."), or a time semaphore ("Maximum interval, for
//     which the task stays in the node..."). Measured deltas: +54px for the
//     first, +52px for the second, on top of a ~82px base for a LOGIC node
//     with a single branch (136px total).
//   - Every go_if_const row in a Condition node adds ~28-29px. Measured: 151px
//     (2 rows) -> 180px (3 rows, +29) -> 208px (4 rows, +28).
//   - A title that doesn't fit the ~200px-wide header on one line adds
//     ~16px per wrapped line (measured: +80px for a title that wrapped from
//     1 to 6 lines).
//
// The constants below now follow docs/process/node-size-reference.md, which
// records the sizes read from the live SVG DOM on 2026-07-29. The earlier
// values were deliberately conservative guesses, and that conservatism was not
// free: it over-reserved 74-77px on EVERY Condition node and 29px on every
// output row, which is a large part of the "too much air between nodes"
// complaint. Over-estimating is still the safer direction than under-, so where
// the reference gives a range these round up — but they no longer round up from
// a guess.
//
// Deliberately NOT changed to the measured value:
//   - the collapsed box stays 48 (see nodeBoxSize) because layCollapsedXOffset
//     and several row pitches are derived from that exact figure;
//   - semaphore output rows keep the conservative 56. The reference measured
//     the increment for an Error row only and explicitly warns that time and
//     count semaphore rows must be measured separately.
const (
	logicBaseH        = 98 // measured: expanded action baseline is 200x98
	condBaseH         = 93 // so 2 go_if_const rows == 151, 3 == 180, 4 == 209 (measured 208)
	perErrRowH        = 27 // measured: the outer rect grows 27px per Error row
	perSemaphoreRowH  = 56 // NOT measured — conservative until a live sample exists
	perConditionRowH  = 29 // measured: 151 -> 180 (+29) -> 208 (+28)
	perTitleLineH     = 16 // measured: +16px per additional wrapped title line
	titleCharsPerLine = 16 // conservative wrap width for the ~200px header
)

func estimatedExpandedHeight(n map[string]interface{}) int {
	errRows := 0
	for _, lg := range nodeLogics(n) {
		if nodeStr(lg, "err_node_id") != "" {
			errRows++
		}
	}
	semaphoreRows := len(nodeSemaphors(n))

	base := logicBaseH
	if isConditionNode(n) {
		base = condBaseH
		condRows := 0
		for _, lg := range nodeLogics(n) {
			if nodeStr(lg, "type") == "go_if_const" {
				condRows++
			}
		}
		base += condRows * perConditionRowH
	}
	// Output rows stack independently of the node body: a Condition with a
	// timer/error output is taller than the same Condition without one.
	base += errRows*perErrRowH + semaphoreRows*perSemaphoreRowH

	if lines := titleWrapLines(nodeStr(n, "title")); lines > 1 {
		base += (lines - 1) * perTitleLineH
	}
	return base
}

// titleWrapLines estimates how many lines a node's title wraps to inside the
// fixed-width header (width never grows — the title wraps instead). Rune
// count (not byte length) so Cyrillic/Ukrainian titles, common on this
// platform, aren't over-counted.
func titleWrapLines(title string) int {
	n := utf8.RuneCountInString(title)
	if n <= titleCharsPerLine {
		return 1
	}
	return (n + titleCharsPerLine - 1) / titleCharsPerLine
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
	g.promoteTerminalAwareBranches()
	return g
}

func isErrorFinal(n map[string]interface{}) bool {
	if nodeObjType(n) != 2 {
		return false
	}
	if strings.EqualFold(nodeStr(nodeExtraMap(n), "icon"), "error") {
		return true
	}
	title := strings.ToLower(nodeStr(n, "title"))
	return strings.Contains(title, "error") || strings.Contains(title, "failed")
}

type branchProfile struct {
	success    bool
	errorFinal bool
	business   int
}

func (g *layoutGraph) profileBranch(start string) branchProfile {
	var out branchProfile
	seen := map[string]bool{}
	queue := []string{start}
	for len(queue) > 0 && len(seen) <= len(g.ids) {
		u := queue[0]
		queue = queue[1:]
		if seen[u] {
			continue
		}
		seen[u] = true
		n := g.byID[u]
		if nodeObjType(n) == 2 {
			if isErrorFinal(n) {
				out.errorFinal = true
			} else {
				out.success = true
			}
			continue
		}
		if nodeObjType(n) != 3 && !isPureRouter(n) {
			out.business++
		}
		for _, v := range g.succs(u) {
			if !seen[v] {
				queue = append(queue, v)
			}
		}
	}
	return out
}

// promoteTerminalAwareBranches keeps the conventional go-on-axis rule, with
// one structural exception: a default go that only terminates in an error
// final must not displace a conditional branch that reaches the real success
// continuation.
func (g *layoutGraph) promoteTerminalAwareBranches() {
	for _, u := range g.ids {
		n := g.byID[u]
		if !isConditionNode(n) {
			continue
		}
		old := g.primary[u]
		if old == "" {
			continue
		}
		oldProfile := g.profileBranch(old)
		if oldProfile.success || !oldProfile.errorFinal {
			continue
		}
		best, bestBusiness := "", oldProfile.business
		for _, lg := range nodeLogics(n) {
			if nodeStr(lg, "type") != "go_if_const" {
				continue
			}
			candidate := nodeStr(lg, "to_node_id")
			profile := g.profileBranch(candidate)
			if profile.success && profile.business > bestBusiness {
				best, bestBusiness = candidate, profile.business
			}
		}
		if best == "" {
			continue
		}
		g.primary[u] = best
		for i, v := range g.branches[u] {
			if v == best {
				g.branches[u][i] = old
				break
			}
		}
	}
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

// classifyErrors describes ownership at the error-cluster entry points. A
// process with many dedicated one-owner clusters is still one readable
// business flow and should not become a global error mesh merely because those
// small clusters make up >30% of all nodes.
func (g *layoutGraph) classifyErrors() errorTopology {
	mainFlow, _ := g.errClosure()
	sources := map[string]map[string]bool{}
	var roots []string
	for _, u := range g.ids {
		for _, e := range g.errors[u] {
			// An error/escalation path may deliberately rejoin the active
			// business flow (for example, a non-blocking failure). That target
			// is a merge point, not an error-cluster root.
			if mainFlow[e] {
				continue
			}
			if sources[e] == nil {
				sources[e] = map[string]bool{}
				roots = append(roots, e)
			}
			sources[e][u] = true
		}
	}
	out := errorTopology{Roots: len(roots)}
	for _, e := range roots {
		switch len(sources[e]) {
		case 1:
			out.Dedicated++
		default:
			out.Shared++
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
