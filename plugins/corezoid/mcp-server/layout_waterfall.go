package main

// The "waterfall with wings" strategy for simple tree-like processes, and the
// forward-flow decomposition it is built on.

import (
	"math"
	"sort"
)

type edgePair struct{ from, to string }

// placeComponent lays out ONE weakly connected component (root + everything
// reachable from it via forward edges restricted to comp). Returns (flow,
// layer, col, back): layer starts at 0, col is signed (0 = axis, ±k
// branches). The component's err tails are not included here — they are
// placed by the global cluster pass.
func placeComponent(comp map[string]bool, root string, g *layoutGraph) (map[string]bool, map[string]int, map[string]int, map[edgePair]bool) {
	succs := func(u string) []string {
		var out []string
		if p, ok := g.primary[u]; ok && comp[p] {
			out = append(out, p)
		}
		for _, b := range g.branches[u] {
			if comp[b] {
				out = append(out, b)
			}
		}
		return out
	}

	// --- reachability + back edges (iterative DFS) ---
	flow := map[string]bool{root: true}
	back := map[edgePair]bool{}
	var post []string
	state := map[string]int{root: 1}
	type frame struct {
		u  string
		vs []string
		i  int
	}
	stack := []frame{{root, succs(root), 0}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		adv := false
		for f.i < len(f.vs) {
			v := f.vs[f.i]
			f.i++
			if state[v] == 1 {
				back[edgePair{f.u, v}] = true
				continue
			}
			if state[v] == 0 {
				state[v] = 1
				flow[v] = true
				stack = append(stack, frame{v, succs(v), 0})
				adv = true
				break
			}
		}
		if !adv {
			state[f.u] = 2
			post = append(post, f.u)
			stack = stack[:len(stack)-1]
		}
	}

	// --- layers: longest path over the DAG ---
	layer := map[string]int{root: 0}
	for i := len(post) - 1; i >= 0; i-- {
		u := post[i]
		for _, v := range succs(u) {
			if back[edgePair{u, v}] || !flow[v] {
				continue
			}
			if lv, ok := layer[v]; !ok || lv < layer[u]+1 {
				layer[v] = layer[u] + 1
			}
		}
	}
	for u := range flow {
		if _, ok := layer[u]; !ok {
			layer[u] = 0
		}
	}

	// --- columns ---
	col := map[string]int{}
	occ := map[int][][2]int{}
	fits := func(c, lo, hi int) bool {
		for _, ab := range occ[c] {
			if !(hi < ab[0] || lo > ab[1]) {
				return false
			}
		}
		return true
	}
	take := func(c, lo, hi int) { occ[c] = append(occ[c], [2]int{lo, hi}) }

	chainNodes := func(h string) []string {
		var out []string
		seen := map[string]bool{}
		u := h
		for flow[u] {
			if _, placed := col[u]; placed {
				break
			}
			if seen[u] {
				break
			}
			seen[u] = true
			out = append(out, u)
			nu, ok := g.primary[u]
			if !ok || seen[nu] {
				break
			}
			u = nu
		}
		return out
	}

	spanOf := func(ch []string) (int, int) {
		lo, hi := layer[ch[0]], layer[ch[0]]
		for _, x := range ch {
			if layer[x] < lo {
				lo = layer[x]
			}
			if layer[x] > hi {
				hi = layer[x]
			}
		}
		return lo, hi
	}

	mainChain := chainNodes(root)
	for _, u := range mainChain {
		col[u] = 0
	}
	mlo, mhi := spanOf(mainChain)
	take(0, mlo, mhi)

	queue := append([]string{}, mainChain...)
	seenQ := map[string]bool{}
	for _, u := range mainChain {
		seenQ[u] = true
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		pc := col[u]
		var bs []string
		for _, b := range g.branches[u] {
			if _, placed := col[b]; flow[b] && !placed {
				bs = append(bs, b)
			}
		}
		sort.SliceStable(bs, func(i, j int) bool {
			return len(chainNodes(bs[i])) > len(chainNodes(bs[j]))
		})
		side := -1
		for _, b := range bs {
			if _, placed := col[b]; placed {
				continue
			}
			ch := chainNodes(b)
			if len(ch) == 0 {
				continue
			}
			lo, hi := spanOf(ch)
			c, found := 0, false
			for k := 1; k <= len(comp)+1 && !found; k++ {
				for _, cand := range [2]int{pc + side*k, pc - side*k} {
					if fits(cand, lo, hi) {
						c, found = cand, true
						break
					}
				}
			}
			side = -side
			for _, x := range ch {
				col[x] = c
			}
			take(c, lo, hi)
			for _, x := range ch {
				if !seenQ[x] {
					queue = append(queue, x)
					seenQ[x] = true
				}
			}
		}
		if nxt, ok := g.primary[u]; ok {
			if _, placed := col[nxt]; placed && !seenQ[nxt] {
				queue = append(queue, nxt)
				seenQ[nxt] = true
			}
		}
	}

	// leftovers, shallowest first (stable on document order)
	leftovers := g.inDocOrder(flow)
	sort.SliceStable(leftovers, func(i, j int) bool { return layer[leftovers[i]] < layer[leftovers[j]] })
	for _, u := range leftovers {
		if _, placed := col[u]; placed {
			continue
		}
		ch := chainNodes(u)
		lo, hi := spanOf(ch)
		c := 1
		for !fits(c, lo, hi) {
			c++
		}
		for _, x := range ch {
			col[x] = c
		}
		take(c, lo, hi)
	}

	// merge nodes — under the deepest predecessor (waterfall)
	preds := map[string][]string{}
	var predOrder []string
	for _, u := range g.inDocOrder(flow) {
		for _, v := range succs(u) {
			if flow[v] && !back[edgePair{u, v}] {
				if _, seen := preds[v]; !seen {
					predOrder = append(predOrder, v)
				}
				preds[v] = append(preds[v], u)
			}
		}
	}
	type cellKey struct{ c, l int }
	cell := map[cellKey]string{}
	for _, u := range g.inDocOrder(flow) {
		cell[cellKey{col[u], layer[u]}] = u
	}
	for _, v := range predOrder {
		ps := preds[v]
		if len(ps) <= 1 {
			continue
		}
		bp := ps[0]
		for _, p := range ps[1:] {
			if layer[p] > layer[bp] || (layer[p] == layer[bp] && -abs(col[p]) > -abs(col[bp])) {
				bp = p
			}
		}
		best := col[bp]
		u, c := v, col[v]
		for flow[u] && c > best {
			tgt := cellKey{best, layer[u]}
			if _, taken := cell[tgt]; taken {
				break
			}
			delete(cell, cellKey{col[u], layer[u]})
			col[u] = best
			cell[tgt] = u
			nu, ok := g.primary[u]
			if !ok || !flow[nu] || len(preds[nu]) > 1 {
				break
			}
			// Python: col.get(nu, best) == best — an unplaced nu also breaks
			if cnu, placed := col[nu]; !placed || cnu == best {
				break
			}
			u = nu // NB: c deliberately keeps the ORIGINAL col[v] (1:1 port)
		}
	}
	return flow, layer, col, back
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// flowDecomposition splits the graph into forward flows: the main flow from
// Start plus every other forward component, with the error CLOSURE (nodes
// reachable only via err_node_id and their tails) excluded upfront.
type subflow struct {
	flow  map[string]bool
	layer map[string]int
	col   map[string]int
	back  map[edgePair]bool
}

func (g *layoutGraph) errClosure() (map[string]bool, map[string]bool) {
	mainFlow := map[string]bool{}
	var stack []string
	for _, n := range g.nodes {
		if nodeObjType(n) == 1 {
			id := nodeStr(n, "id")
			mainFlow[id] = true
			stack = append(stack, id)
		}
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, v := range g.succs(u) {
			if !mainFlow[v] {
				mainFlow[v] = true
				stack = append(stack, v)
			}
		}
	}
	errTargets := map[string]bool{}
	for _, id := range g.ids {
		for _, e := range g.errors[id] {
			errTargets[e] = true
		}
	}
	errClosure := map[string]bool{}
	for _, e := range g.inDocOrder(errTargets) {
		if !mainFlow[e] {
			stack = append(stack, e)
		}
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if errClosure[u] || mainFlow[u] {
			continue
		}
		errClosure[u] = true
		for _, v := range g.succs(u) {
			if !mainFlow[v] {
				stack = append(stack, v)
			}
		}
	}
	return mainFlow, errClosure
}

// pickRoot chooses the next component root among remaining: a Start node
// first, else the source with the most in-pool successors, else the
// lexicographically smallest id (Python's min(remaining)).
func (g *layoutGraph) pickRoot(remaining map[string]bool) string {
	indeg := func(u string) int {
		d := 0
		for _, w := range g.inDocOrder(remaining) {
			for _, s := range g.succs(w) {
				if s == u {
					d++
				}
			}
		}
		return d
	}
	var starts, sources []string
	for _, id := range g.inDocOrder(remaining) {
		if nodeObjType(g.byID[id]) == 1 {
			starts = append(starts, id)
		}
	}
	if len(starts) > 0 {
		return starts[0]
	}
	for _, id := range g.inDocOrder(remaining) {
		if indeg(id) == 0 {
			sources = append(sources, id)
		}
	}
	if len(sources) > 0 {
		best, bestN := sources[0], -1
		for _, s := range sources {
			n := 0
			for _, v := range g.succs(s) {
				if remaining[v] {
					n++
				}
			}
			if n > bestN {
				best, bestN = s, n
			}
		}
		return best
	}
	return minID(remaining)
}

// layout is the "waterfall with wings" strategy. It returns coordinates for
// every node and MUTATES nodes (extra.modeForm) for the collapse decisions.
func (e *layoutEngine) layout(nodes []map[string]interface{}) map[string]lpoint {
	if len(nodes) == 0 {
		return map[string]lpoint{}
	}
	g := buildLayoutGraph(nodes)
	_, errClosure := g.errClosure()

	// 1) collect all forward flows in local frames (error clusters excluded)
	var subflows []subflow
	remaining := map[string]bool{}
	for _, id := range g.ids {
		if !errClosure[id] {
			remaining[id] = true
		}
	}
	for guard := 0; len(remaining) > 0 && guard < len(g.ids)+5; guard++ {
		root := g.pickRoot(remaining)
		f, lay, cl, bk := placeComponent(remaining, root, g)
		if len(f) == 0 {
			delete(remaining, root)
			continue
		}
		lo := math.MaxInt
		for _, c := range cl {
			if c < lo {
				lo = c
			}
		}
		for u := range cl {
			cl[u] -= lo
		}
		subflows = append(subflows, subflow{f, lay, cl, bk})
		for u := range f {
			delete(remaining, u)
		}
	}

	// order: the Start flow(s) first, then by size descending (stable)
	hasStart := func(sf subflow) bool {
		for u := range sf.flow {
			if nodeObjType(g.byID[u]) == 1 {
				return true
			}
		}
		return false
	}
	sort.SliceStable(subflows, func(i, j int) bool {
		si, sj := 0, 0
		if !hasStart(subflows[i]) {
			si = 1
		}
		if !hasStart(subflows[j]) {
			sj = 1
		}
		if si != sj {
			return si < sj
		}
		return len(subflows[i].flow) > len(subflows[j].flow)
	})

	// 2) large flows — vertical bands; small fragments — a compact grid
	flow := map[string]bool{}
	layer := map[string]int{}
	col := map[string]int{}
	back := map[edgePair]bool{}
	const minorMax = 6
	const gapCols = 2
	var majors, minors []subflow
	for _, sf := range subflows {
		if len(sf.flow) > minorMax || hasStart(sf) {
			majors = append(majors, sf)
		} else {
			minors = append(minors, sf)
		}
	}

	colBase := 0
	maxCol := func() int {
		m, any := 0, false
		for _, c := range col {
			if !any || c > m {
				m, any = c, true
			}
		}
		return m
	}
	for _, sf := range majors {
		for u := range sf.flow {
			layer[u] = sf.layer[u]
			col[u] = colBase + sf.col[u]
			flow[u] = true
		}
		for e := range sf.back {
			back[e] = true
		}
		colBase = maxCol() + 1 + gapCols
	}

	if len(minors) > 0 {
		cellW, cellH := 0, 0
		for _, sf := range minors {
			for _, c := range sf.col {
				if c+2 > cellW {
					cellW = c + 2
				}
			}
			for _, l := range sf.layer {
				if l+2 > cellH {
					cellH = l + 2
				}
			}
		}
		gridCols := int(math.Ceil(math.Sqrt(float64(len(minors)))))
		if gridCols < 1 {
			gridCols = 1
		}
		if gridCols > len(minors) {
			gridCols = len(minors)
		}
		gx0 := colBase
		for i, sf := range minors {
			r, c := i/gridCols, i%gridCols
			cx := gx0 + c*cellW
			ly := r * cellH
			for u := range sf.flow {
				layer[u] = ly + sf.layer[u]
				col[u] = cx + sf.col[u]
				flow[u] = true
			}
			for e := range sf.back {
				back[e] = true
			}
		}
	}

	// err clusters: err targets outside flow + their tails
	errOnly := map[string]bool{}
	var stack []string
	for _, id := range g.ids {
		for _, e := range g.errors[id] {
			if !flow[e] {
				stack = append(stack, e)
			}
		}
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if errOnly[u] || flow[u] {
			continue
		}
		errOnly[u] = true
		for _, t := range g.succs(u) {
			if !flow[t] {
				stack = append(stack, t)
			}
		}
	}

	// nodes to be collapsed (needed before pixels — their x is optically
	// centered on the column axis)
	collapseSet := map[string]bool{}
	for _, u := range g.inDocOrder(flow) {
		if isPureRouter(g.byID[u]) {
			collapseSet[u] = true
		}
	}

	// --- 4. Pixels ---
	maxLayer := 0
	for _, u := range g.inDocOrder(flow) {
		if layer[u] > maxLayer {
			maxLayer = layer[u]
		}
	}
	timerRows := map[int]bool{}
	for _, u := range g.inDocOrder(flow) {
		if len(nodeSemaphors(g.byID[u])) > 0 {
			timerRows[layer[u]] = true
		}
	}
	rowStep, timerExtra := layRowStep, layTimerExtra
	naturalH := (maxLayer+1)*layRowStep + len(timerRows)*layTimerExtra
	const canvasH = 19000
	if naturalH > canvasH {
		scale := float64(canvasH) / float64(naturalH)
		rowStep = int(float64(layRowStep) * scale)
		if rowStep < 155 {
			rowStep = 155
		}
		timerExtra = int(float64(layTimerExtra) * scale)
		if timerExtra < 0 {
			timerExtra = 0
		}
	}
	rowY := map[int]int{}
	y := layRowY0
	for r := 0; r <= maxLayer; r++ {
		rowY[r] = y
		y += rowStep
		if timerRows[r] {
			y += timerExtra
		}
	}

	coords := map[string]lpoint{}
	for _, u := range g.inDocOrder(flow) {
		n := g.byID[u]
		x := layColX0 + col[u]*layColStep
		if isCircle(n) {
			x += layCircleXOffset
		} else if collapseSet[u] {
			x += layCollapsedXOffset
		}
		coords[u] = lpoint{x, rowY[layer[u]]}
	}

	// --- 5. Err clusters ---
	maxColV := maxCol()
	errSources := map[string][]string{}
	var errOrder []string
	for _, u := range g.ids {
		for _, eid := range g.errors[u] {
			if errOnly[eid] {
				if _, seen := errSources[eid]; !seen {
					errOrder = append(errOrder, eid)
				}
				errSources[eid] = append(errSources[eid], u)
			}
		}
	}
	for _, eid := range errOrder {
		if _, placed := coords[eid]; placed {
			continue
		}
		var ps []string
		for _, s := range errSources[eid] {
			if _, ok := coords[s]; ok {
				ps = append(ps, s)
			}
		}
		if len(ps) == 0 {
			continue
		}
		var ex, ey int
		switch {
		case len(ps) == 1:
			sp := coords[ps[0]]
			baseX := sp.X
			if isCircle(g.byID[ps[0]]) {
				baseX -= layCircleXOffset
			}
			ex, ey = baseX+layErrDX, sp.Y
		case len(ps) <= 3:
			// few sources: to the right at the median y — the edges fan in
			var ys []int
			psSet := map[string]bool{}
			for _, s := range ps {
				ys = append(ys, coords[s].Y)
				psSet[s] = true
			}
			sort.Ints(ys)
			ex = layColX0 + (maxColV+1)*layColStep
			ey = ys[len(ys)/2]
			for _, id := range g.ids {
				if psSet[id] {
					continue
				}
				if p, ok := coords[id]; ok && p.Y == ey && p.X < ex {
					ey += layRowStep / 2
					break
				}
			}
		default:
			// mass errors: bottom-right — all edges flow down-right
			ex = layColX0 + (maxColV+1)*layColStep
			maxY := math.MinInt
			for _, id := range g.ids {
				if p, ok := coords[id]; ok && p.Y > maxY {
					maxY = p.Y
				}
			}
			ey = maxY + layRowStep/2
		}
		// Place the whole cluster with the shared staircase geometry (see
		// nodeClusterStrip): entry right of the anchor and below the row
		// lane, primary chain stepping down-right, the retry Delay stacked
		// above its Condition. The whole cluster collapses, including the
		// named Error final.
		mset := map[string]bool{}
		var stack2 []string
		if errOnly[eid] {
			stack2 = append(stack2, eid)
		}
		for len(stack2) > 0 {
			u := stack2[len(stack2)-1]
			stack2 = stack2[:len(stack2)-1]
			if mset[u] {
				continue
			}
			if _, placed := coords[u]; placed {
				continue
			}
			mset[u] = true
			for _, v := range g.succs(u) {
				if errOnly[v] {
					stack2 = append(stack2, v)
				}
			}
		}
		if len(mset) == 0 {
			continue
		}
		// synthesize the walk from the cluster entry: nodeClusterStrip walks
		// from an owner's err targets, so hand it a virtual owner = the first
		// source (the entry is eid either way)
		strip, _, _ := nodeClusterStripFromEntry(g, eid, mset)
		for _, cp := range strip {
			cn := g.byID[cp.id]
			off := layCollapsedXOffset
			if isCircle(cn) {
				off = layCircleXOffset
			}
			collapseNode(cn)
			coords[cp.id] = lpoint{ex - layErrDX + cp.dx + off, ey + cp.dy - 60}
		}
	}

	// orphans — a separate column at the bottom
	orphanY := rowY[maxLayer] + layRowStep
	for _, id := range g.ids {
		if p, ok := coords[id]; ok && p.Y+layRowStep > orphanY {
			orphanY = p.Y + layRowStep
		}
	}
	for _, n := range nodes {
		id := nodeStr(n, "id")
		if _, ok := coords[id]; !ok {
			coords[id] = lpoint{layColX0 + (maxColV+2)*layColStep, orphanY}
			orphanY += layRowStep
		}
	}

	// --- 5b. Collapse "service" nodes (pure IF routers and pure Delays) ---
	for _, u := range g.inDocOrder(collapseSet) {
		collapseNode(g.byID[u])
	}

	// --- 5b. Sink success finals to the bottom of the diagram ---
	type sinkEntry struct {
		id    string
		preds []string
	}
	var sink []sinkEntry
	for _, n := range nodes {
		u := nodeStr(n, "id")
		if nodeObjType(n) != 2 || errClosure[u] || errOnly[u] {
			continue
		}
		if _, ok := coords[u]; !ok {
			continue
		}
		var preds []string
		for _, w := range g.ids {
			hit := false
			if g.primary[w] == u {
				hit = true
			}
			for _, b := range g.branches[w] {
				if b == u {
					hit = true
				}
			}
			if hit {
				if _, ok := coords[w]; ok {
					preds = append(preds, w)
				}
			}
		}
		if len(preds) > 0 {
			sink = append(sink, sinkEntry{u, preds})
		}
	}
	if len(sink) > 0 {
		sunk := map[string]bool{}
		for _, s := range sink {
			sunk[s.id] = true
		}
		bottom := 0
		for _, id := range g.ids {
			if p, ok := coords[id]; ok && !sunk[id] && p.Y > bottom {
				bottom = p.Y
			}
		}
		for _, s := range sink {
			deep := s.preds[0]
			for _, w := range s.preds[1:] {
				if coords[w].Y > coords[deep].Y {
					deep = w
				}
			}
			colX := coords[deep].X
			if isCircle(g.byID[deep]) {
				colX -= layCircleXOffset
			}
			coords[s.id] = lpoint{colX + layCircleXOffset, bottom + rowStep}
		}
	}

	// --- 6. Collisions (waterfall-local box model: collapse decisions are
	// keyed by collapseSet/errOnly, matching the Python inline resolver) ---
	const hgap, vgap = 30, 8
	blockH := rowStep - vgap - 2
	if blockH > 150 {
		blockH = 150
	}
	box := func(id string) (int, int, int, int) {
		p := coords[id]
		n := g.byID[id]
		if isCircle(n) {
			return p.X - 28, p.Y - 28, p.X + 28, p.Y + 28
		}
		if collapseSet[id] || errOnly[id] {
			return p.X, p.Y, p.X + 48, p.Y + 48
		}
		return p.X, p.Y, p.X + 200, p.Y + blockH
	}
	order := append([]string{}, g.ids...)
	sortYX := func() {
		sort.SliceStable(order, func(i, j int) bool {
			a, b := coords[order[i]], coords[order[j]]
			if a.Y != b.Y {
				return a.Y < b.Y
			}
			return a.X < b.X
		})
	}
	sortYX()
	for pass := 0; pass < len(nodes); pass++ {
		moved := false
		for i, a := range order {
			ax0, ay0, ax1, ay1 := box(a)
			_ = ax0
			for _, b := range order[i+1:] {
				bx0, by0, bx1, by1 := box(b)
				if ax0-hgap < bx1 && bx0 < ax1+hgap && ay0-vgap < by1 && by0 < ay1+vgap {
					p := coords[b]
					bump := 0
					if isCircle(g.byID[b]) {
						bump = 28
					}
					coords[b] = lpoint{p.X, ay1 + vgap + bump}
					moved = true
				}
			}
			if moved {
				break
			}
		}
		if !moved {
			break
		}
		sortYX()
	}

	// --- 6b. Density pass ---
	coords = e.compact(coords, g)

	// --- 7. Platform canvas: ±10000, centre tall/wide layouts ---
	maxYv, minYv := math.MinInt, math.MaxInt
	for _, p := range coords {
		if p.Y > maxYv {
			maxYv = p.Y
		}
		if p.Y < minYv {
			minYv = p.Y
		}
	}
	if maxYv > 9900 {
		shift := -floorDiv(maxYv+minYv, 2)
		shift -= pyMod(shift, layRowStep/2)
		for k, p := range coords {
			coords[k] = lpoint{p.X, p.Y + shift}
		}
	}
	maxXv, minXv := math.MinInt, math.MaxInt
	for _, p := range coords {
		if p.X > maxXv {
			maxXv = p.X
		}
		if p.X < minXv {
			minXv = p.X
		}
	}
	if maxXv > 9900 {
		shiftX := -floorDiv(maxXv+minXv, 2)
		shiftX -= pyMod(shiftX, layColStep/2)
		for k, p := range coords {
			coords[k] = lpoint{p.X + shiftX, p.Y}
		}
	}
	return coords
}

// countForwardFlows reports how many independent forward flows the
// decomposition yields — the marker for strategy selection.
func (g *layoutGraph) countForwardFlows() int {
	_, errClosure := g.errClosure()
	errTargets := map[string]bool{}
	for _, id := range g.ids {
		for _, e := range g.errors[id] {
			errTargets[e] = true
		}
	}
	rem := map[string]bool{}
	for _, id := range g.ids {
		if !errTargets[id] && !errClosure[id] {
			rem[id] = true
		}
	}
	flows := 0
	for guard := 0; len(rem) > 0 && guard < len(g.ids)+5; guard++ {
		root := g.pickRoot(rem)
		f, _, _, _ := placeComponent(rem, root, g)
		if len(f) == 0 {
			delete(rem, root)
			continue
		}
		if len(f) >= 3 { // a 1-2 node island is an orphan, not a flow
			flows++
		}
		for u := range f {
			delete(rem, u)
		}
	}
	return flows
}
