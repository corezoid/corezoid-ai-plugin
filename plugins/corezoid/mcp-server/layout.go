package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// layout.go is the canonical archetype-aware, layered node-layout engine. It
// produces straight connectors and zero overlaps via a graph model,
// weighted-longest-path ranks, deterministic column packing, adaptive vertical
// spacing, and grid snapping. (Originally prototyped in Python; the Go here is
// now the source of truth.)
//
// Determinism: nodes are iterated in insertion order (graph.order) and
// per-row node lists are sorted by id string. This is what makes the layout
// idempotent.

// edge is a directed connection between two nodes.
// kind is one of: "primary" (go / logic fall-through), "cond" (go_if_const),
// "error" (logic.err_node_id), "timeout" (semaphor.to_node_id).
type edge struct {
	src, dst, kind string
}

// graph is the decoded process scheme: node maps keyed by id, the insertion
// order of those ids, and the directed edges between them.
type graph struct {
	nodes map[string]map[string]interface{}
	order []string
	edges []edge
}

// kind returns the kind of the first edge src->dst, or "" if no such edge.
func (g *graph) kind(src, dst string) string {
	for _, e := range g.edges {
		if e.src == src && e.dst == dst {
			return e.kind
		}
	}
	return ""
}

// role maps obj_type to a role string: 1=START, 2=END, 3=COND, else LOGIC.
func (g *graph) role(id string) string {
	return roleOf(g.nodes[id])
}

func roleOf(n map[string]interface{}) string {
	if n == nil {
		return "LOGIC"
	}
	switch ot, _ := n["obj_type"].(float64); ot {
	case 1:
		return "START"
	case 2:
		return "END"
	case 3:
		return "COND"
	default:
		return "LOGIC"
	}
}

// logicsOf returns the condition.logics slice of a node as []interface{}.
func logicsOf(n map[string]interface{}) []interface{} {
	cond, _ := n["condition"].(map[string]interface{})
	if cond == nil {
		return nil
	}
	ls, _ := cond["logics"].([]interface{})
	return ls
}

// semaphorsOf returns the condition.semaphors slice of a node.
func semaphorsOf(n map[string]interface{}) []interface{} {
	cond, _ := n["condition"].(map[string]interface{})
	if cond == nil {
		return nil
	}
	ss, _ := cond["semaphors"].([]interface{})
	return ss
}

func strField(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

// buildGraph ports build_graph: builds the node map (preserving order) and the
// edge list. 'go' -> primary, 'go_if_const' -> cond, any other logic with a
// to_node_id -> primary fall-through, err_node_id -> error, semaphor
// to_node_id -> timeout. Edges whose dst is not a known node are dropped.
func buildGraph(nodes []map[string]interface{}) *graph {
	g := &graph{
		nodes: make(map[string]map[string]interface{}, len(nodes)),
		order: make([]string, 0, len(nodes)),
	}
	for _, n := range nodes {
		id, _ := n["id"].(string)
		if _, seen := g.nodes[id]; !seen {
			g.order = append(g.order, id)
		}
		g.nodes[id] = n
	}
	for _, nid := range g.order {
		n := g.nodes[nid]
		for _, raw := range logicsOf(n) {
			l, _ := raw.(map[string]interface{})
			if l == nil {
				continue
			}
			t := strField(l, "type")
			dst := strField(l, "to_node_id")
			if t == "go" && dst != "" {
				g.edges = append(g.edges, edge{nid, dst, "primary"})
			} else if t == "go_if_const" && dst != "" {
				g.edges = append(g.edges, edge{nid, dst, "cond"})
			} else if dst != "" {
				g.edges = append(g.edges, edge{nid, dst, "primary"}) // api_rpc/api/etc fall-through
			}
			if eid := strField(l, "err_node_id"); eid != "" {
				g.edges = append(g.edges, edge{nid, eid, "error"})
			}
		}
		for _, raw := range semaphorsOf(n) {
			s, _ := raw.(map[string]interface{})
			if s == nil {
				continue
			}
			if dst := strField(s, "to_node_id"); dst != "" {
				g.edges = append(g.edges, edge{nid, dst, "timeout"})
			}
		}
	}
	// Drop edges whose dst is not a known node.
	kept := g.edges[:0]
	for _, e := range g.edges {
		if _, ok := g.nodes[e.dst]; ok {
			kept = append(kept, e)
		}
	}
	g.edges = kept
	return g
}

// collectLogicTypes gathers the distinct logic types across all nodes, in
// first-seen order. (detectArchetype only checks membership, so order is not
// semantically important, but first-seen order is deterministic.)
func collectLogicTypes(nodes []map[string]interface{}) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, n := range nodes {
		for _, raw := range logicsOf(n) {
			l, _ := raw.(map[string]interface{})
			if l == nil {
				continue
			}
			if t := strField(l, "type"); t != "" && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// detectArchetype ports detect_archetype. "state" if convType=="state",
// otherwise classified by the presence of specific logic types.
func detectArchetype(convType string, logicTypes []string) string {
	if convType == "state" {
		return "state"
	}
	has := map[string]bool{}
	for _, t := range logicTypes {
		has[t] = true
	}
	if has["api_callback"] {
		return "receiver"
	}
	if has["api_rpc_reply"] {
		return "api"
	}
	if has["api_rpc"] {
		return "business"
	}
	if has["api"] {
		return "integration"
	}
	return "default"
}

// profile holds the per-archetype layout parameters that the Go layout uses.
// The proto carries err_dx/err_dy too, but assign_positions never reads them,
// so the Go port omits them. vStep/lanePitch are the overlap-safe minima.
type profile struct {
	vStep, lanePitch, grid, startOff int
}

// profileFor returns the layout profile for an archetype. In the current proto
// PROFILES table every archetype shares the same minima (vStep=180,
// lanePitch=240, grid=20, startOff=100), so a single profile is returned for
// all archetypes (matching proto/layout.py exactly).
func profileFor(archetype string) profile {
	return profile{vStep: 180, lanePitch: 240, grid: 20, startOff: 100}
}

const spineX = 600

// starts ports _starts: all START nodes in order, or the first node if none.
func (g *graph) starts() []string {
	var s []string
	for _, nid := range g.order {
		if g.role(nid) == "START" {
			s = append(s, nid)
		}
	}
	if len(s) == 0 && len(g.order) > 0 {
		return []string{g.order[0]}
	}
	return s
}

// downTarget ports _down_target: for each node pick the ONE outgoing edge that
// is its vertical continuation — the 'go'/primary edge if any, else the first
// 'go_if_const'/cond, else the first edge. All other out-edges are branches.
func (g *graph) downTarget() map[string]string {
	// Preserve per-source edge order (matches Python defaultdict(list) append).
	order := []string{}
	out := map[string][]edge{}
	for _, e := range g.edges {
		if _, ok := out[e.src]; !ok {
			order = append(order, e.src)
		}
		out[e.src] = append(out[e.src], e)
	}
	dt := map[string]string{}
	for _, s := range order {
		lst := out[s]
		var goD, condD string
		haveGo, haveCond := false, false
		for _, e := range lst {
			if e.kind == "primary" && !haveGo {
				goD, haveGo = e.dst, true
			}
			if e.kind == "cond" && !haveCond {
				condD, haveCond = e.dst, true
			}
		}
		switch {
		case haveGo:
			dt[s] = goD
		case haveCond:
			dt[s] = condD
		default:
			// lst is non-empty by construction: s is only a key in out because
			// at least one edge with src==s was appended.
			dt[s] = lst[0].dst
		}
	}
	return dt
}

// ranks ports _ranks: row = weighted longest path from Start. The chosen down
// edge costs 1 row; every branch edge costs 0. Cycle-safe via DFS coloring
// (back edges to GRAY nodes are dropped) leaving a DAG, then topological
// relaxation for the longest path.
func (g *graph) ranks(dt map[string]string) map[string]int {
	type succEdge struct {
		dst string
		w   int
	}
	succ := map[string][]succEdge{}
	for _, e := range g.edges {
		w := 0
		if dt[e.src] == e.dst {
			w = 1
		}
		succ[e.src] = append(succ[e.src], succEdge{e.dst, w})
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	for _, nid := range g.order {
		color[nid] = white
	}
	dag := map[string][]succEdge{}

	var visit func(u string)
	visit = func(u string) {
		color[u] = gray
		for _, se := range succ[u] {
			if color[se.dst] == gray {
				continue // back edge -> drop (breaks the cycle)
			}
			dag[u] = append(dag[u], se)
			if color[se.dst] == white {
				visit(se.dst)
			}
		}
		color[u] = black
	}
	for _, s := range g.starts() {
		if color[s] == white {
			visit(s)
		}
	}
	for _, nid := range g.order {
		if color[nid] == white {
			visit(nid)
		}
	}

	indeg := map[string]int{}
	for _, nid := range g.order {
		indeg[nid] = 0
	}
	for _, u := range g.order {
		for _, se := range dag[u] {
			indeg[se.dst]++
		}
	}
	rank := map[string]int{}
	for _, nid := range g.order {
		rank[nid] = 0
	}
	queue := []string{}
	for _, nid := range g.order {
		if indeg[nid] == 0 {
			queue = append(queue, nid)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, se := range dag[u] {
			if rank[u]+se.w > rank[se.dst] {
				rank[se.dst] = rank[u] + se.w
			}
			indeg[se.dst]--
			if indeg[se.dst] == 0 {
				queue = append(queue, se.dst)
			}
		}
	}
	return rank
}

func snap(v float64, grid int) int {
	return int(math.Round(v/float64(grid))) * grid
}

// assignPositions ports assign_positions: the full layered layout. Returns a
// map id -> [2]int{x, y}.
func assignPositions(g *graph, archetype string) map[string][2]int {
	p := profileFor(archetype)
	grid := p.grid

	// Adaptive vertical spacing; horizontal pitch stays at the compact minimum.
	// Python: v_step = min(base+60, base + 20*max(0,(n-8)//12)).
	// (n-8) is only used inside max(0, ...), so for n<8 the term is clamped to
	// 0 and Python's floor-division sign behaviour never matters.
	n := len(g.nodes)
	extra := 0
	if n > 8 {
		extra = (n - 8) / 12
	}
	vStepUnsnapped := p.vStep + 20*extra
	if maxStep := p.vStep + 60; vStepUnsnapped > maxStep {
		vStepUnsnapped = maxStep
	}
	vStep := snap(float64(vStepUnsnapped), grid)
	lanePitch := p.lanePitch

	dt := g.downTarget()
	rank := g.ranks(dt)

	parents := map[string][]string{}
	for _, nid := range g.order {
		parents[nid] = nil
	}
	for _, e := range g.edges {
		parents[e.dst] = append(parents[e.dst], e.src)
	}

	// Group nodes by rank, preserving insertion order within a rank.
	byRank := map[int][]string{}
	rankKeys := []int{}
	for _, nid := range g.order {
		r := rank[nid]
		if _, ok := byRank[r]; !ok {
			rankKeys = append(rankKeys, r)
		}
		byRank[r] = append(byRank[r], nid)
	}
	sort.Ints(rankKeys)

	col := map[string]int{}
	lowestFree := func(taken map[int]bool, start int) int {
		c := start
		for taken[c] {
			c++
		}
		return c
	}

	for _, r := range rankKeys {
		taken := map[int]bool{}
		type chainEntry struct {
			nid string
			ic  int
		}
		var chain []chainEntry
		var others []string

		rowNodes := append([]string(nil), byRank[r]...)
		sort.Strings(rowNodes) // stable by id

		for _, nid := range rowNodes {
			// down-parents: parents whose chosen down edge is this node, already placed.
			var downpar []string
			for _, s := range parents[nid] {
				if dt[s] == nid {
					if _, placed := col[s]; placed {
						downpar = append(downpar, s)
					}
				}
			}
			if len(downpar) > 0 {
				chain = append(chain, chainEntry{nid, col[downpar[0]]}) // inherit parent column
			} else {
				others = append(others, nid)
			}
		}

		// Place chain entries sorted by (inherited column, id).
		sort.SliceStable(chain, func(i, j int) bool {
			if chain[i].ic != chain[j].ic {
				return chain[i].ic < chain[j].ic
			}
			return chain[i].nid < chain[j].nid
		})
		for _, ce := range chain {
			c := lowestFree(taken, ce.ic)
			col[ce.nid] = c
			taken[c] = true
		}
		// Place branch/root nodes strictly right of their source.
		for _, nid := range others {
			var placed []int
			for _, s := range parents[nid] {
				if c, ok := col[s]; ok {
					placed = append(placed, c)
				}
			}
			base := 0
			if len(placed) > 0 {
				minC := placed[0]
				for _, c := range placed[1:] {
					if c < minC {
						minC = c
					}
				}
				base = minC + 1
			}
			c := lowestFree(taken, base)
			col[nid] = c
			taken[c] = true
		}
	}

	pos := map[string][2]int{}
	for _, nid := range g.order {
		x := float64(spineX + col[nid]*lanePitch)
		y := float64(rank[nid] * vStep)
		if role := g.role(nid); (role == "START" || role == "END") && col[nid] == 0 {
			x += float64(p.startOff) // centre Start/Final circle over the spine
		}
		pos[nid] = [2]int{snap(x, grid), snap(y, grid)}
	}
	return pos
}

// layoutMode resolves the layout mode from the COREZOID_AUTOLAYOUT env var
// (case-insensitive, trimmed). It is the ONLY source of mode control:
//   - "off"     -> do nothing.
//   - "full"    -> lay out ALL nodes (full re-tidy of the whole scheme).
//   - otherwise -> "preserve" (the DEFAULT, including when unset): keep placed
//     nodes exactly where they are and only position newly-added nodes.
func layoutMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COREZOID_AUTOLAYOUT"))) {
	case "off":
		return "off"
	case "full":
		return "full"
	default:
		return "preserve"
	}
}

// coordOf returns a node's x/y as float64 (missing/non-float treated as 0), and
// whether the node is "new/unplaced" (both x and y are 0).
func coordOf(n map[string]interface{}) (x, y float64, isNew bool) {
	x, _ = n["x"].(float64)
	y, _ = n["y"].(float64)
	return x, y, x == 0 && y == 0
}

// applyLayout positions nodes of a process scheme in place. It is the single
// integration point used by the push path (see fixStruct in main.go).
//
// SAFE BY DEFAULT: the mode comes from layoutMode() (env-only).
//   - off:      no-op.
//   - full:     archetype-aware full layout of every node (overwrites all x/y).
//   - preserve: keep every already-placed node exactly where it is and slot
//     ONLY the new (0/0) nodes in near their neighbours. A scheme where every
//     node is new (e.g. a process built from scratch) gets the full clean
//     layout — there is nothing to preserve.
//
// Malformed input is handled gracefully (no panic): comma-ok type assertions
// throughout, and a missing/empty nodes list is a no-op.
//
// convType is the top-level conv_type of the loaded .conv.json; pass "" if it
// cannot be determined (detectArchetype then classifies purely by logic types).
func applyLayout(scheme map[string]interface{}, convType string) {
	applyLayoutMode(scheme, convType, layoutMode())
}

// applyLayoutMode is the explicit-mode core of applyLayout: it positions nodes
// using the caller-supplied mode ("off"|"full"|"preserve") instead of reading
// the COREZOID_AUTOLAYOUT env var. applyLayout is the env-driven wrapper; the
// layout-process tool and the layout-check harness call this directly to force
// a "full" re-layout without mutating global env state.
func applyLayoutMode(scheme map[string]interface{}, convType, mode string) {
	if scheme == nil {
		return
	}
	if mode == "off" {
		return
	}

	rawNodes, ok := scheme["nodes"].([]interface{})
	if !ok || len(rawNodes) == 0 {
		return
	}
	nodes := make([]map[string]interface{}, 0, len(rawNodes))
	for _, raw := range rawNodes {
		if n, ok := raw.(map[string]interface{}); ok {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return
	}

	// A node is new/unplaced when both x and y are 0.
	allNew := true
	for _, n := range nodes {
		if _, _, isNew := coordOf(n); !isNew {
			allNew = false
			break
		}
	}

	if mode == "full" || allNew {
		// Full pipeline: re-tidy every node onto the spine.
		types := collectLogicTypes(nodes)
		archetype := detectArchetype(convType, types)
		g := buildGraph(nodes)
		pos := assignPositions(g, archetype)
		for _, n := range nodes {
			id, _ := n["id"].(string)
			if p, ok := pos[id]; ok {
				n["x"] = float64(p[0])
				n["y"] = float64(p[1])
			}
		}
		return
	}

	// preserve: mixed placed + new -> only position the new nodes.
	placeNewNodes(nodes)
}

// placeNewNodes slots each just-added node (x==0 && y==0) into the existing
// manual layout near its graph neighbours, WITHOUT moving any placed node and
// without overlap. Connectors are kept straight: a new primary child goes
// directly below its parent, a new branch/error/reply target goes to the right
// of its source, a new parent of a placed primary child goes directly above it.
func placeNewNodes(nodes []map[string]interface{}) {
	g := buildGraph(nodes)
	dt := g.downTarget()

	parents := map[string][]string{}
	for _, nid := range g.order {
		parents[nid] = nil
	}
	for _, e := range g.edges {
		parents[e.dst] = append(parents[e.dst], e.src)
	}

	// Adaptive vertical spacing — same formula assignPositions uses.
	p := profileFor("default")
	grid := p.grid
	n := len(nodes)
	extra := 0
	if n > 8 {
		extra = (n - 8) / 12
	}
	vStepUnsnapped := p.vStep + 20*extra
	if maxStep := p.vStep + 60; vStepUnsnapped > maxStep {
		vStepUnsnapped = maxStep
	}
	vstep := snap(float64(vStepUnsnapped), grid)
	const pitch = 240

	type xy struct{ x, y int }
	placed := map[string]xy{}
	occupied := map[xy]bool{}
	for _, nd := range nodes {
		id, _ := nd["id"].(string)
		x, y, isNew := coordOf(nd)
		if isNew {
			continue
		}
		sx, sy := snap(x, grid), snap(y, grid)
		placed[id] = xy{sx, sy}
		occupied[xy{sx, sy}] = true
	}

	// Process new nodes in topological rank order so a new node whose parent is
	// also new is placed after its parent; ties by id for determinism.
	rank := g.ranks(dt)
	var newIDs []string
	for _, nd := range nodes {
		if _, _, isNew := coordOf(nd); isNew {
			id, _ := nd["id"].(string)
			newIDs = append(newIDs, id)
		}
	}
	sort.SliceStable(newIDs, func(i, j int) bool {
		if rank[newIDs[i]] != rank[newIDs[j]] {
			return rank[newIDs[i]] < rank[newIDs[j]]
		}
		return newIDs[i] < newIDs[j]
	})

	for _, id := range newIDs {
		var target xy
		switch {
		case func() bool {
			// 1. N is the primary down-child of some placed parent P.
			for _, s := range parents[id] {
				if dt[s] == id {
					if pp, ok := placed[s]; ok {
						target = xy{pp.x, pp.y + vstep}
						return true
					}
				}
			}
			return false
		}():
		case func() bool {
			// 2. N has any placed parent P (branch/error/reply target).
			for _, s := range parents[id] {
				if pp, ok := placed[s]; ok {
					target = xy{pp.x + pitch, pp.y}
					return true
				}
			}
			return false
		}():
		case func() bool {
			// 3. N has a placed primary down-child C.
			if c := dt[id]; c != "" {
				if pc, ok := placed[c]; ok {
					target = xy{pc.x, pc.y - vstep}
					return true
				}
			}
			return false
		}():
		default:
			// 4. Fallback.
			target = xy{spineX, 0}
		}

		target = xy{snap(float64(target.x), grid), snap(float64(target.y), grid)}
		for occupied[target] {
			target.y += vstep
		}

		// Write back onto the node and register it as placed.
		for _, nd := range nodes {
			if nid, _ := nd["id"].(string); nid == id {
				nd["x"] = float64(target.x)
				nd["y"] = float64(target.y)
				break
			}
		}
		placed[id] = target
		occupied[target] = true
	}
}

// --- layout-check corpus parity harness (mirrors proto/validate.py) ----------
//
// Real Corezoid node footprints: Start/End are 56px circles with a CENTER
// pivot; all other nodes are 200x150 with a TOP-LEFT pivot. Modelling this lets
// columns sit as tight as the real shapes allow.
const (
	layoutCircle = 56.0
	layoutLogicW = 200.0
	layoutLogicH = 150.0
)

// rectOf returns the axis-aligned box (x, y, w, h) in top-left form for a node,
// mirroring proto/validate.py rect_of.
func rectOf(n map[string]interface{}) [4]float64 {
	x, _ := n["x"].(float64)
	y, _ := n["y"].(float64)
	switch roleOf(n) {
	case "START", "END":
		return [4]float64{x - layoutCircle/2, y - layoutCircle/2, layoutCircle, layoutCircle}
	default:
		return [4]float64{x, y, layoutLogicW, layoutLogicH}
	}
}

func rectsIntersect(a, b [4]float64) bool {
	return a[0] < b[0]+b[2] && b[0] < a[0]+a[2] && a[1] < b[1]+b[3] && b[1] < a[1]+a[3]
}

// countOverlaps counts overlapping pairs among the node rects.
func countOverlaps(nodes []map[string]interface{}) int {
	rects := make([][4]float64, 0, len(nodes))
	for _, n := range nodes {
		rects = append(rects, rectOf(n))
	}
	c := 0
	for i := 0; i < len(rects); i++ {
		for j := i + 1; j < len(rects); j++ {
			if rectsIntersect(rects[i], rects[j]) {
				c++
			}
		}
	}
	return c
}

// runLayoutCheck walks dir for *.conv.json, applies the layout (forced on) to
// each, counts remaining overlaps, prints "files=N overlaps_after=T" and
// returns a non-zero exit code if T>0. Mirrors proto/validate.py's pass
// criterion (overlaps_after==0 on the corpus).
func runLayoutCheck(dir string) int {
	// The harness forces a FULL layout (via applyLayoutMode below) so it always
	// exercises the engine on every node — preserve mode would leave already-
	// placed nodes untouched and not test the packing.
	var files []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".conv.json") {
			files = append(files, path)
		}
		return nil
	})

	n := 0
	total := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(data, &doc); err != nil {
			continue
		}
		scheme, _ := doc["scheme"].(map[string]interface{})
		if scheme == nil {
			continue
		}
		rawNodes, _ := scheme["nodes"].([]interface{})
		if len(rawNodes) == 0 {
			continue
		}
		convType, _ := doc["conv_type"].(string)
		applyLayoutMode(scheme, convType, "full")

		nodes := make([]map[string]interface{}, 0, len(rawNodes))
		for _, raw := range rawNodes {
			if nm, ok := raw.(map[string]interface{}); ok {
				nodes = append(nodes, nm)
			}
		}
		total += countOverlaps(nodes)
		n++
	}

	fmt.Printf("files=%d overlaps_after=%d\n", n, total)
	if total > 0 {
		return 1
	}
	return 0
}
