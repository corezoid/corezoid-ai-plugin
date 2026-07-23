package main

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// layout.go is a minimal, self-contained node-placement kernel used on the
// push path: any NEW node the caller added with placeholder coordinates
// (x==0 && y==0) is auto-placed so it never lands on top of the existing
// diagram. Two modes, chosen by applyLayout: baseLayout (every node new -> a
// deterministic layered placement of the whole scheme) and placeNewNodes (some
// nodes already placed -> keep them and slot only the new ones near their
// neighbours). Deterministic (insertion order + id sort) and panic-safe
// (comma-ok reads throughout).

// Fixed layout geometry. Everything is a multiple of gridSnap so snapping is a
// no-op for the values the kernel itself produces.
const (
	vStep     = 200 // vertical distance between stacked rows / nudge step
	lanePitch = 240 // horizontal distance between columns
	gridSnap  = 20  // grid the final coordinates snap to
	spineX    = 600 // x of column 0 (the spine)
	startOff  = 100 // extra x to centre a Start/End circle over the spine
)

// Fixed node footprints (px). Start/End render as a small circle with a CENTER
// pivot; every other node is a 200x150 box with a TOP-LEFT pivot.
const (
	circleSize = 56.0
	logicW     = 200.0
	logicH     = 150.0
)

// edge is a directed connection between two nodes. kind is one of "primary"
// (go / logic fall-through), "cond" (go_if_const), "error" (err_node_id),
// "timeout" (semaphor to_node_id).
type edge struct {
	src, dst, kind string
}

// graph is the decoded scheme: node maps keyed by id, their insertion order,
// and the directed edges between them.
type graph struct {
	nodes map[string]map[string]interface{}
	order []string
	edges []edge
}

// roleOf maps obj_type to a role: 1=START, 2=END, 3=COND, else LOGIC.
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

func (g *graph) role(id string) string { return roleOf(g.nodes[id]) }

func strField(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

// logicsOf returns the condition.logics slice of a node.
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

// coordOf returns a node's x/y as float64 (missing/non-float treated as 0) and
// whether the node is "new/unplaced" (both x and y are 0).
//
// (0,0) is the "unplaced/new" sentinel: a node with both coordinates at 0 is
// treated as new and will be positioned on push. The engine itself never
// assigns coordinates that low (the spine starts at x=600), so this is safe for
// engine-produced schemes; the one edge case is a node a user manually places
// at exactly (0,0), which will be treated as new and repositioned.
func coordOf(n map[string]interface{}) (x, y float64, isNew bool) {
	x, _ = n["x"].(float64)
	y, _ = n["y"].(float64)
	return x, y, x == 0 && y == 0
}

func snap(v float64, grid int) int {
	return int(math.Round(v/float64(grid))) * grid
}

// rectOf returns the axis-aligned box (x, y, w, h) in top-left form for a node,
// using FIXED footprints: Start/End are 56x56 circles (center pivot), all other
// nodes are 200x150 boxes (top-left pivot).
func rectOf(n map[string]interface{}) [4]float64 {
	x, _ := n["x"].(float64)
	y, _ := n["y"].(float64)
	switch roleOf(n) {
	case "START", "END":
		return [4]float64{x - circleSize/2, y - circleSize/2, circleSize, circleSize}
	default:
		return [4]float64{x, y, logicW, logicH}
	}
}

func rectsIntersect(a, b [4]float64) bool {
	return a[0] < b[0]+b[2] && b[0] < a[0]+a[2] && a[1] < b[1]+b[3] && b[1] < a[1]+a[3]
}

// buildGraph builds the node map (preserving insertion order) and the edge
// list. 'go' -> primary, 'go_if_const' -> cond, any other logic with a
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

// starts returns all START nodes in order, or the first node if none.
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

// downTarget picks, for each node, the ONE outgoing edge that is its vertical
// continuation — the first 'go'/primary edge if any, else the first
// 'go_if_const'/cond, else the first edge. All other out-edges are branches.
func (g *graph) downTarget() map[string]string {
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
			dt[s] = lst[0].dst // lst is non-empty by construction
		}
	}
	return dt
}

// ranks computes each node's row as the forward longest path from a Start over
// the chosen down edge (weight 1) and every branch edge (weight 0). Cycle-safe:
// DFS coloring drops back edges (to GRAY nodes), then a topological relaxation
// over the resulting DAG finds the longest path.
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
	queue := []string{}
	for _, nid := range g.order {
		rank[nid] = 0
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

// parentsOf builds the reverse adjacency (dst -> sources).
func parentsOf(g *graph) map[string][]string {
	parents := map[string][]string{}
	for _, nid := range g.order {
		parents[nid] = nil
	}
	for _, e := range g.edges {
		parents[e.dst] = append(parents[e.dst], e.src)
	}
	return parents
}

// baseLayout is a simple deterministic layered placement for a from-scratch
// process (every node new). The primary down-chain from Start is the spine in
// column 0, stepping straight down one row per rank; every other node is packed
// into the lowest free column to the RIGHT of its source, on its own rank row.
// Start/End circles in column 0 are centred over the spine with +startOff.
// Per-rank nodes are processed in id order for determinism.
func baseLayout(nodes []map[string]interface{}) {
	g := buildGraph(nodes)
	dt := g.downTarget()
	rank := g.ranks(dt)
	parents := parentsOf(g)

	// Group node ids by rank.
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
	for _, r := range rankKeys {
		taken := map[int]bool{}
		lowestFree := func(start int) int {
			c := start
			for taken[c] {
				c++
			}
			return c
		}
		rowNodes := append([]string(nil), byRank[r]...)
		sort.Strings(rowNodes)

		// Split into chain nodes (primary down-child of an already-placed
		// parent -> inherit its column, keeping the spine straight) and others.
		type chainEntry struct {
			nid string
			ic  int
		}
		var chain []chainEntry
		var others []string
		for _, nid := range rowNodes {
			inherited := -1
			for _, s := range parents[nid] {
				if dt[s] == nid {
					if c, ok := col[s]; ok {
						inherited = c
						break
					}
				}
			}
			if inherited >= 0 {
				chain = append(chain, chainEntry{nid, inherited})
			} else {
				others = append(others, nid)
			}
		}
		// Spine (col 0) first so it always re-claims column 0.
		sort.SliceStable(chain, func(i, j int) bool {
			if chain[i].ic != chain[j].ic {
				return chain[i].ic < chain[j].ic
			}
			return chain[i].nid < chain[j].nid
		})
		for _, ce := range chain {
			c := lowestFree(ce.ic)
			col[ce.nid] = c
			taken[c] = true
		}
		// Branch/other nodes: lowest free column right of the leftmost placed
		// parent (base 0 when no parent is placed yet, e.g. Start).
		for _, nid := range others {
			base := 0
			seen := false
			for _, s := range parents[nid] {
				if c, ok := col[s]; ok {
					if !seen || c+1 < base {
						base = c + 1
						seen = true
					}
				}
			}
			c := lowestFree(base)
			col[nid] = c
			taken[c] = true
		}
	}

	for _, nid := range g.order {
		n := g.nodes[nid]
		x := float64(spineX + col[nid]*lanePitch)
		y := float64(rank[nid] * vStep)
		if role := roleOf(n); (role == "START" || role == "END") && col[nid] == 0 {
			x += startOff
		}
		n["x"] = float64(snap(x, gridSnap))
		n["y"] = float64(snap(y, gridSnap))
	}
}

// placeNewNodes slots each just-added node (x==0 && y==0) into the existing
// manual layout near its graph neighbours, WITHOUT moving any placed node and
// without overlap. Connectors stay straight: a new primary down-child goes
// directly below its parent, a new branch/error/reply target goes to the right
// of its source on the same row, a new parent of a placed down-child goes
// directly above it. If the target rect intersects any placed rect the new node
// is nudged down by vStep until it is clear. Returns one placement note per
// newly-placed node so the caller can tell the user where each landed.
func placeNewNodes(nodes []map[string]interface{}) []string {
	g := buildGraph(nodes)
	dt := g.downTarget()
	parents := parentsOf(g)
	rank := g.ranks(dt)

	type xy struct{ x, y int }
	placed := map[string]xy{}
	var placedRects [][4]float64
	// rectAt builds nd's footprint at (x,y) preserving its role (obj_type).
	rectAt := func(nd map[string]interface{}, x, y int) [4]float64 {
		return rectOf(map[string]interface{}{
			"obj_type": nd["obj_type"],
			"x":        float64(x),
			"y":        float64(y),
		})
	}

	nodeByID := map[string]map[string]interface{}{}
	for _, nd := range nodes {
		if id, _ := nd["id"].(string); id != "" {
			nodeByID[id] = nd
		}
	}

	// Register every already-placed node as fixed.
	for _, nd := range nodes {
		id, _ := nd["id"].(string)
		x, y, isNew := coordOf(nd)
		if isNew {
			continue
		}
		sx, sy := snap(x, gridSnap), snap(y, gridSnap)
		placed[id] = xy{sx, sy}
		placedRects = append(placedRects, rectAt(nd, sx, sy))
	}

	// New nodes, processed in (rank, id) order so a new node whose parent is
	// also new is placed after its parent.
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

	var notes []string
	for _, id := range newIDs {
		var target xy
		set := false
		// 1. primary down-child of a placed parent -> below the parent.
		for _, s := range parents[id] {
			if dt[s] == id {
				if pp, ok := placed[s]; ok {
					target, set = xy{pp.x, pp.y + vStep}, true
					break
				}
			}
		}
		// 2. any placed parent -> right of the source, same row.
		if !set {
			for _, s := range parents[id] {
				if pp, ok := placed[s]; ok {
					target, set = xy{pp.x + lanePitch, pp.y}, true
					break
				}
			}
		}
		// 3. placed primary down-child -> directly above it.
		if !set {
			if c := dt[id]; c != "" {
				if pc, ok := placed[c]; ok {
					target, set = xy{pc.x, pc.y - vStep}, true
				}
			}
		}
		// 4. fallback: top of the spine.
		if !set {
			target = xy{spineX, 0}
		}

		target = xy{snap(float64(target.x), gridSnap), snap(float64(target.y), gridSnap)}

		nd := nodeByID[id]
		intersectsPlaced := func(x, y int) bool {
			cand := rectAt(nd, x, y)
			for _, r := range placedRects {
				if rectsIntersect(cand, r) {
					return true
				}
			}
			return false
		}
		// Nudge down until the candidate clears every placed rect. Terminates:
		// placed rects are finite, the step is strictly positive; the guard cap
		// is belt-and-suspenders.
		for guard := 0; intersectsPlaced(target.x, target.y) && guard <= len(nodes); guard++ {
			target.y += vStep
		}

		if nd != nil {
			nd["x"] = float64(target.x)
			nd["y"] = float64(target.y)
		}
		placed[id] = target
		placedRects = append(placedRects, rectAt(nd, target.x, target.y))
		notes = append(notes, fmt.Sprintf("layout: placed new node %s at (%d, %d)", id, target.x, target.y))
	}
	return notes
}

// layoutMode resolves the layout mode from the COREZOID_AUTOLAYOUT env var
// (case-insensitive, trimmed): "off" -> "off"; anything else, including unset,
// -> "preserve" (the default). There is no "full" mode.
func layoutMode() string {
	if strings.ToLower(strings.TrimSpace(os.Getenv("COREZOID_AUTOLAYOUT"))) == "off" {
		return "off"
	}
	return "preserve"
}

// applyLayout positions the nodes of a process scheme IN PLACE on the push
// path. It is the single integration point used by fixStruct in main.go.
//
//   - COREZOID_AUTOLAYOUT=off -> no-op.
//   - every node new (x==0 && y==0) -> baseLayout the whole scheme.
//   - otherwise -> placeNewNodes: keep placed nodes, slot only the new ones.
//
// Malformed input is handled gracefully (comma-ok throughout); a missing/empty
// nodes list is a no-op. web_settings is never read. convType is accepted for
// forward-compatibility but the lean kernel does not use it.
//
// Returns placement notes for the caller to surface to the user: one note per
// newly-placed node in the preserve path (placeNewNodes), and a single summary
// note in the from-scratch path (baseLayout) — per-node notes there would be
// noise. Returns nil when nothing was placed or the layout is disabled.
func applyLayout(scheme map[string]interface{}, convType string) []string {
	_ = convType
	if scheme == nil || layoutMode() == "off" {
		return nil
	}
	rawNodes, ok := scheme["nodes"].([]interface{})
	if !ok || len(rawNodes) == 0 {
		return nil
	}
	nodes := make([]map[string]interface{}, 0, len(rawNodes))
	for _, raw := range rawNodes {
		if n, ok := raw.(map[string]interface{}); ok {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return nil
	}

	allNew := true
	for _, n := range nodes {
		if _, _, isNew := coordOf(n); !isNew {
			allNew = false
			break
		}
	}
	if allNew {
		baseLayout(nodes)
		return []string{fmt.Sprintf("layout: positioned %d new nodes", len(nodes))}
	}
	return placeNewNodes(nodes)
}
