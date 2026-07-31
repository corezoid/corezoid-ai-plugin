package main

// Sugiyama-lite (layers + dummy nodes + median crossing minimization +
// priority coordinates) and the partitioned strategy built on it for
// LARGE/mesh processes where 1/2–2/3 of the nodes are error handling.

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Dummy vertices for long edges live in the same string namespace as real
// node ids, distinguished by a prefix no real id can carry.
const dummyPrefix = "\x00d:"

func isDummyVertex(v string) bool { return strings.HasPrefix(v, dummyPrefix) }

func dummyVertex(idx, layer int) string {
	return dummyPrefix + strconv.Itoa(idx) + ":" + strconv.Itoa(layer)
}

// crossingBIT counts already-seen edge targets by their position in the next
// layer. It lets crossing minimisation score a layer boundary in
// O((V+E) log V), rather than comparing every pair of edges.
type crossingBIT []int

func newCrossingBIT(n int) crossingBIT { return make(crossingBIT, n+1) }

func (b crossingBIT) add(pos int) {
	for i := pos + 1; i < len(b); i += i & -i {
		b[i]++
	}
}

// prefix returns the number of entries at positions <= pos.
func (b crossingBIT) prefix(pos int) int {
	total := 0
	for i := pos + 1; i > 0; i -= i & -i {
		total += b[i]
	}
	return total
}

// countCrossingsBetweenLayers counts proper crossings only: edges sharing a
// source or target are not crossings. Delaying BIT updates until all targets
// of one source have been queried prevents a source's own fan-out from being
// counted against itself.
func countCrossingsBetweenLayers(top, bottom []string, adj map[string][]string) int {
	if len(top) == 0 || len(bottom) == 0 {
		return 0
	}
	bottomPos := make(map[string]int, len(bottom))
	for i, u := range bottom {
		bottomPos[u] = i
	}
	bit := newCrossingBIT(len(bottom))
	seen := 0
	total := 0
	for _, u := range top {
		var targets []int
		for _, v := range adj[u] {
			if p, ok := bottomPos[v]; ok {
				targets = append(targets, p)
			}
		}
		for _, p := range targets {
			total += seen - bit.prefix(p)
		}
		for _, p := range targets {
			bit.add(p)
			seen++
		}
	}
	return total
}

// weightedMedianPosition is the Graphviz/Warfield median used during layer
// sweeps. With four or more neighbours it weights the two central positions
// by the spread on the opposite side, which is less jumpy for asymmetric
// fan-in/fan-out than a flat average.
func weightedMedianPosition(positions []int) (float64, bool) {
	if len(positions) == 0 {
		return 0, false
	}
	p := append([]int{}, positions...)
	sort.Ints(p)
	m := len(p) / 2
	if len(p)%2 == 1 {
		return float64(p[m]), true
	}
	if len(p) == 2 {
		return float64(p[0]+p[1]) / 2.0, true
	}
	left := p[m-1] - p[0]
	right := p[len(p)-1] - p[m]
	if left+right == 0 {
		return float64(p[m-1]+p[m]) / 2.0, true
	}
	return (float64(p[m-1]*right) + float64(p[m]*left)) / float64(left+right), true
}

func localLayerCrossings(layers [][]string, adj map[string][]string, r int) int {
	total := 0
	if r > 0 {
		total += countCrossingsBetweenLayers(layers[r-1], layers[r], adj)
	}
	if r+1 < len(layers) {
		total += countCrossingsBetweenLayers(layers[r], layers[r+1], adj)
	}
	return total
}

func localPrimarySpan(layers [][]string, primary map[edgePair]bool, r int) int {
	total := 0
	scoreBoundary := func(top, bottom []string) {
		topPos := make(map[string]int, len(top))
		bottomPos := make(map[string]int, len(bottom))
		for i, u := range top {
			topPos[u] = i
		}
		for i, u := range bottom {
			bottomPos[u] = i
		}
		for ed := range primary {
			a, aok := topPos[ed.from]
			b, bok := bottomPos[ed.to]
			if aok && bok {
				d := a - b
				if d < 0 {
					d = -d
				}
				total += d
			}
		}
	}
	if r > 0 {
		scoreBoundary(layers[r-1], layers[r])
	}
	if r+1 < len(layers) {
		scoreBoundary(layers[r], layers[r+1])
	}
	return total
}

// transposeLayer performs deterministic adjacent swaps while they strictly
// reduce crossings. A swap is rejected if it makes the primary go spine less
// straight; this keeps a secondary branch from winning at the expense of the
// business flow. Ties retain document/previous order.
func transposeLayer(layers [][]string, adj map[string][]string, primary map[edgePair]bool, r int, rightToLeft bool) bool {
	if r < 0 || r >= len(layers) || len(layers[r]) < 2 {
		return false
	}
	changed := false
	row := layers[r]
	for pass := 0; pass < len(row); pass++ {
		passChanged := false
		for step := 0; step < len(row)-1; step++ {
			i := step
			if rightToLeft {
				i = len(row) - 2 - step
			}
			beforeCross := localLayerCrossings(layers, adj, r)
			beforePrimary := localPrimarySpan(layers, primary, r)
			row[i], row[i+1] = row[i+1], row[i]
			afterCross := localLayerCrossings(layers, adj, r)
			afterPrimary := localPrimarySpan(layers, primary, r)
			if afterCross < beforeCross && afterPrimary <= beforePrimary {
				changed = true
				passChanged = true
			} else {
				row[i], row[i+1] = row[i+1], row[i]
			}
		}
		if !passChanged {
			break
		}
	}
	return changed
}

// layoutSugiyama computes coordinates for the given subset (only == nil →
// all nodes): DFS back-edge detection, longest-path layers, dummy chains for
// long edges, 12 weighted-median + adjacent-transpose sweeps keeping the best
// ordering, priority-method x-coordinates with straight channels for long
// edges, and Brandes–Köpf-style snapping of primary (go) chains. Mutates
// nodes: collapses pure IF/Delay.
func (e *layoutEngine) layoutSugiyama(nodes []map[string]interface{}, only map[string]bool) map[string]lpoint {
	if len(nodes) == 0 {
		return map[string]lpoint{}
	}
	g := buildLayoutGraph(nodes)

	var ids []string
	inSet := func(u string) bool { return only == nil || only[u] }
	for _, id := range g.ids {
		if inSet(id) {
			ids = append(ids, id)
		}
	}
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	out := map[string][]string{}
	for _, u := range ids {
		var vs []string
		for _, v := range g.allOut(u) {
			if idSet[v] {
				vs = append(vs, v)
			}
		}
		out[u] = vs
	}

	// --- back edges (DFS from Start, then from the rest) ---
	color := map[string]int{}
	back := map[edgePair]bool{}
	var order []string
	var roots []string
	for _, n := range nodes {
		id := nodeStr(n, "id")
		if nodeObjType(n) == 1 && idSet[id] {
			roots = append(roots, id)
		}
	}
	roots = append(roots, ids...)
	type frame struct {
		u  string
		vs []string
		i  int
	}
	for _, root := range roots {
		if color[root] != 0 {
			continue
		}
		stack := []frame{{root, out[root], 0}}
		color[root] = 1
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			adv := false
			for f.i < len(f.vs) {
				v := f.vs[f.i]
				f.i++
				if color[v] == 1 {
					back[edgePair{f.u, v}] = true
					continue
				}
				if color[v] == 0 {
					color[v] = 1
					stack = append(stack, frame{v, out[v], 0})
					adv = true
					break
				}
			}
			if !adv {
				color[f.u] = 2
				order = append(order, f.u)
				stack = stack[:len(stack)-1]
			}
		}
	}

	// --- layers: longest-path (without back edges) ---
	layer := map[string]int{}
	for i := len(order) - 1; i >= 0; i-- {
		u := order[i]
		if _, ok := layer[u]; !ok {
			layer[u] = 0
		}
		for _, v := range out[u] {
			if back[edgePair{u, v}] {
				continue
			}
			if lv, ok := layer[v]; !ok || lv < layer[u]+1 {
				layer[v] = layer[u] + 1
			}
		}
	}
	for _, u := range ids {
		if _, ok := layer[u]; !ok {
			layer[u] = 0
		}
	}
	maxLayer := 0
	for _, l := range layer {
		if l > maxLayer {
			maxLayer = l
		}
	}

	// --- forward edges (reverse back edges so they point down) + dummies ---
	var fedges []edgePair
	for _, u := range ids {
		for _, v := range out[u] {
			a, b := u, v
			if back[edgePair{u, v}] {
				a, b = v, u
			}
			if layer[a] == layer[b] {
				continue // intra-layer edges are ignored in the ordering
			}
			fedges = append(fedges, edgePair{a, b})
		}
	}

	L := make([][]string, maxLayer+1)
	for _, u := range ids {
		L[layer[u]] = append(L[layer[u]], u)
	}
	vLayer := map[string]int{}
	for _, u := range ids {
		vLayer[u] = layer[u]
	}
	dummyAdj := map[string][]string{}
	for _, u := range ids {
		dummyAdj[u] = nil
	}
	primarySegments := map[edgePair]bool{}
	dcount := 0
	for _, ed := range fedges {
		a, b := ed.from, ed.to
		la, lb := layer[a], layer[b]
		if lb < la {
			a, b = b, a
			la, lb = lb, la
		}
		if lb-la == 1 {
			dummyAdj[a] = append(dummyAdj[a], b)
			if g.primary[a] == b {
				primarySegments[edgePair{a, b}] = true
			}
			continue
		}
		isPrimary := g.primary[a] == b
		prev := a
		for r := la + 1; r < lb; r++ {
			d := dummyVertex(dcount, r)
			dcount++
			L[r] = append(L[r], d)
			vLayer[d] = r
			dummyAdj[prev] = append(dummyAdj[prev], d)
			if isPrimary {
				primarySegments[edgePair{prev, d}] = true
			}
			dummyAdj[d] = nil
			prev = d
		}
		dummyAdj[prev] = append(dummyAdj[prev], b)
		if isPrimary {
			primarySegments[edgePair{prev, b}] = true
		}
	}

	// incoming (for the upward median); iterate sources in a fixed order
	var vOrder []string
	vOrder = append(vOrder, ids...)
	for r := range L {
		for _, v := range L[r] {
			if isDummyVertex(v) {
				vOrder = append(vOrder, v)
			}
		}
	}
	into := map[string][]string{}
	for _, u := range vOrder {
		for _, v := range dummyAdj[u] {
			into[v] = append(into[v], u)
		}
	}

	posn := map[string]int{}
	for r := range L {
		for i, u := range L[r] {
			posn[u] = i
		}
	}

	crossings := func() int {
		tot := 0
		for r := 0; r < maxLayer; r++ {
			tot += countCrossingsBetweenLayers(L[r], L[r+1], dummyAdj)
		}
		return tot
	}

	medianKey := func(u string, ref map[string][]string) (float64, bool) {
		var ns []int
		for _, w := range ref[u] {
			if p, ok := posn[w]; ok {
				ns = append(ns, p)
			}
		}
		if len(ns) == 0 {
			return 0, false
		}
		return weightedMedianPosition(ns)
	}

	// --- ordering: median sweeps, keep the best ---
	snapshot := func() [][]string {
		cp := make([][]string, len(L))
		for r := range L {
			cp[r] = append([]string{}, L[r]...)
		}
		return cp
	}
	bestCr := crossings()
	best := snapshot()
	for sweep := 0; sweep < 12; sweep++ {
		var rng []int
		var ref map[string][]string
		if sweep%2 == 0 {
			for r := 1; r <= maxLayer; r++ {
				rng = append(rng, r)
			}
			ref = into
		} else {
			for r := maxLayer - 1; r >= 0; r-- {
				rng = append(rng, r)
			}
			ref = dummyAdj
		}
		for _, r := range rng {
			type keyed struct {
				k   float64
				has bool
				i   int
				u   string
			}
			row := L[r]
			ks := make([]keyed, len(row))
			for i, u := range row {
				k, has := medianKey(u, ref)
				ks[i] = keyed{k, has, i, u}
			}
			var fixed []keyed
			for _, t := range ks {
				if t.has {
					fixed = append(fixed, t)
				}
			}
			sort.SliceStable(fixed, func(a, b int) bool {
				if fixed[a].k != fixed[b].k {
					return fixed[a].k < fixed[b].k
				}
				return fixed[a].i < fixed[b].i
			})
			res := make([]string, 0, len(row))
			fi := 0
			for _, t := range ks {
				if !t.has {
					res = append(res, t.u)
				} else {
					res = append(res, fixed[fi].u)
					fi++
				}
			}
			L[r] = res
			for i, u := range res {
				posn[u] = i
			}
		}
		for _, r := range rng {
			if transposeLayer(L, dummyAdj, primarySegments, r, sweep%2 == 1) {
				for i, u := range L[r] {
					posn[u] = i
				}
			}
		}
		if cr := crossings(); cr < bestCr {
			bestCr = cr
			best = snapshot()
		}
	}
	L = best
	for r := range L {
		for i, u := range L[r] {
			posn[u] = i
		}
	}

	// --- coordinates: PRIORITY METHOD — dummies get +inf priority so long
	// edges keep straight vertical channels ---
	upNbr := map[string][]string{}
	dnNbr := map[string][]string{}
	for _, u := range vOrder {
		for _, v := range dummyAdj[u] {
			dnNbr[u] = append(dnNbr[u], v)
			upNbr[v] = append(upNbr[v], u)
		}
	}
	priority := func(u string) int {
		if isDummyVertex(u) {
			return 1 << 30
		}
		return len(upNbr[u]) + len(dnNbr[u])
	}

	x := map[string]float64{}
	for r := range L {
		for _, u := range L[r] {
			x[u] = float64(posn[u])
		}
	}

	alignPass := func(rng []int, ref map[string][]string) {
		for _, r := range rng {
			row := L[r]
			desired := map[string]float64{}
			for _, u := range row {
				ns := ref[u]
				if len(ns) == 0 {
					desired[u] = x[u]
					continue
				}
				sum := 0.0
				for _, w := range ns {
					sum += x[w]
				}
				desired[u] = sum / float64(len(ns))
			}
			byPrio := append([]string{}, row...)
			sort.SliceStable(byPrio, func(a, b int) bool { return priority(byPrio[a]) > priority(byPrio[b]) })
			rowIndex := map[string]int{}
			for i, u := range row {
				rowIndex[u] = i
			}
			for _, u := range byPrio {
				i := rowIndex[u]
				want := desired[u]
				lo := -1e18
				for j := i - 1; j >= 0; j-- {
					lo = x[row[j]] + float64(i-j)
					if priority(row[j]) >= priority(u) {
						break
					}
				}
				hi := 1e18
				for j := i + 1; j < len(row); j++ {
					hi = x[row[j]] - float64(j-i)
					if priority(row[j]) >= priority(u) {
						break
					}
				}
				x[u] = math.Min(math.Max(want, lo), hi)
			}
		}
	}

	var down, up []int
	for r := 1; r <= maxLayer; r++ {
		down = append(down, r)
	}
	for r := maxLayer - 1; r >= 0; r-- {
		up = append(up, r)
	}
	for it := 0; it < 10; it++ {
		if it%2 == 0 {
			alignPass(down, upNbr)
		} else {
			alignPass(up, dnNbr)
		}
	}
	// restore the min gap and the order
	for r := 0; r <= maxLayer; r++ {
		row := L[r]
		for i := 1; i < len(row); i++ {
			if x[row[i]] < x[row[i-1]]+1.0 {
				x[row[i]] = x[row[i-1]] + 1.0
			}
		}
	}

	// --- straighten primary chains (Brandes–Köpf-style snapping) ---
	primChild := map[string]string{}
	for _, u := range ids {
		for _, lg := range nodeLogics(g.byID[u]) {
			if nodeStr(lg, "type") == "go" {
				v := nodeStr(lg, "to_node_id")
				if lv, ok := layer[v]; ok && lv == layer[u]+1 {
					primChild[u] = v
				}
				break // first go logic decides, matched or not (1:1 port)
			}
		}
	}
	for sweep := 0; sweep < 3; sweep++ {
		for r := 1; r <= maxLayer; r++ {
			row := L[r]
			for i, v := range row {
				if isDummyVertex(v) {
					continue
				}
				want, found := 0.0, false
				for _, u := range upNbr[v] {
					if !isDummyVertex(u) && primChild[u] == v {
						want, found = x[u], true
						break
					}
				}
				if !found {
					continue
				}
				lo := -1e18
				if i > 0 {
					lo = x[row[i-1]] + 1.0
				}
				hi := 1e18
				if i+1 < len(row) {
					hi = x[row[i+1]] - 1.0
				}
				if lo <= want && want <= hi {
					x[v] = want
				}
			}
		}
	}

	// --- pixels ---
	rowStep, timerExtra := layRowStep, layTimerExtra
	timerRows := map[int]bool{}
	for _, u := range ids {
		if len(nodeSemaphors(g.byID[u])) > 0 {
			timerRows[layer[u]] = true
		}
	}
	naturalH := (maxLayer+1)*layRowStep + len(timerRows)*layTimerExtra
	if naturalH > 19000 {
		sc := 19000.0 / float64(naturalH)
		rowStep = int(float64(layRowStep) * sc)
		if rowStep < 155 {
			rowStep = 155
		}
		timerExtra = int(float64(layTimerExtra) * sc)
		if timerExtra < 0 {
			timerExtra = 0
		}
	}
	rowY := map[int]int{}
	yy := layRowY0
	for r := 0; r <= maxLayer; r++ {
		rowY[r] = yy
		yy += rowStep
		if timerRows[r] {
			yy += timerExtra
		}
	}

	coords := map[string]lpoint{}
	for _, u := range ids {
		n := g.byID[u]
		px := layColX0 + int(x[u]*float64(layColStep))
		if isCircle(n) {
			px += layCircleXOffset
		}
		coords[u] = lpoint{px, rowY[layer[u]]}
	}

	// collapse IF/Delay + centering
	for _, u := range ids {
		n := g.byID[u]
		if isPureRouter(n) {
			collapseNode(n)
			if !isCircle(n) && isCollapsedNode(n) {
				p := coords[u]
				coords[u] = lpoint{p.X + layCollapsedXOffset, p.Y}
			}
		}
	}

	// canvas: re-centre, do not clamp (see clampCoords)
	clampCoords(coords, rowStep, layColStep)
	return coords
}

// clampCoords RE-CENTRES a tall/wide layout on the origin once its extent
// passes ±9900, aligning the shift to a half-step so coordinates stay "round".
//
// Despite the name it does not clamp: it only translates, so a layout whose
// span already exceeds the canvas (roughly 125+ layers, since the row-step
// scaling in layoutSugiyama bottoms out at 155px) still ends up with nodes
// beyond ±10000. That is deliberate — the node schema allows ±100000 because
// real processes legitimately reach about ±25500 — but do not read this
// function as a guarantee that coordinates fit the ±10000 view.
func clampCoords(coords map[string]lpoint, rowStep, colStep int) {
	maxY, minY := math.MinInt, math.MaxInt
	maxX, minX := math.MinInt, math.MaxInt
	for _, p := range coords {
		if p.Y > maxY {
			maxY = p.Y
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.X < minX {
			minX = p.X
		}
	}
	if len(coords) == 0 {
		return
	}
	if maxY > 9900 || minY < -9900 {
		half := rowStep / 2
		if half < 1 {
			half = 1
		}
		sh := -floorDiv(maxY+minY, 2)
		sh -= pyMod(sh, half)
		for k, p := range coords {
			coords[k] = lpoint{p.X, p.Y + sh}
		}
	}
	if maxX > 9900 || minX < -9900 {
		sh := -floorDiv(maxX+minX, 2)
		sh -= pyMod(sh, colStep/2)
		for k, p := range coords {
			coords[k] = lpoint{p.X + sh, p.Y}
		}
	}
}

// layoutPartitioned is the pipeline for LARGE/mesh processes: main flow via
// Sugiyama, error clusters collapsed on a clean right rail aligned with their
// source rows, orphans in a bottom grid.
func (e *layoutEngine) layoutPartitioned(nodes []map[string]interface{}) map[string]lpoint {
	if len(nodes) == 0 {
		return map[string]lpoint{}
	}
	g := buildLayoutGraph(nodes)
	mainFlow, _ := g.errClosure()

	var starts []string
	for _, n := range nodes {
		if nodeObjType(n) == 1 {
			starts = append(starts, nodeStr(n, "id"))
		}
	}

	// error closure roots with their sources, in document order
	errRef := map[string][]string{}
	errRefSeen := map[string]map[string]bool{}
	var errRoots []string
	for _, u := range g.ids {
		for _, eid := range g.errors[u] {
			if !mainFlow[eid] {
				if _, seen := errRef[eid]; !seen {
					errRoots = append(errRoots, eid)
					errRefSeen[eid] = map[string]bool{}
				}
				if !errRefSeen[eid][u] {
					errRefSeen[eid][u] = true
					errRef[eid] = append(errRef[eid], u)
				}
			}
		}
	}
	errc := map[string]bool{}
	var stack []string
	for _, eid := range errRoots {
		if !mainFlow[eid] {
			stack = append(stack, eid)
		}
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if errc[u] || mainFlow[u] {
			continue
		}
		errc[u] = true
		for _, v := range g.succs(u) {
			if !mainFlow[v] {
				stack = append(stack, v)
			}
		}
	}

	// 1) main flow via Sugiyama
	var coords map[string]lpoint
	if len(mainFlow) > 1 {
		coords = e.layoutSugiyama(nodes, mainFlow)
	} else {
		coords = map[string]lpoint{}
		if len(starts) > 0 {
			coords[starts[0]] = lpoint{layColX0, layRowY0}
		}
	}

	collapse := func(u string) {
		n := g.byID[u]
		if n == nil || isCircle(n) {
			return
		}
		collapseNode(n)
	}

	// 2) Dedicated one-owner error clusters stay pinned to their owner. Only
	// genuinely shared clusters go to the global right rail.
	placed := map[string]bool{}
	clusterOf := func(root string) []string {
		var seq []string
		seen := map[string]bool{}
		queue := []string{root}
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			if seen[x] || !errc[x] {
				continue
			}
			seen[x] = true
			seq = append(seq, x)
			for _, v := range g.succs(x) {
				if errc[v] && !seen[v] {
					queue = append(queue, v)
				}
			}
		}
		return seq
	}
	rootMembers := map[string][]string{}
	memberRoots := map[string]int{}
	for _, root := range errRoots {
		seq := clusterOf(root)
		rootMembers[root] = seq
		for _, u := range seq {
			memberRoots[u]++
		}
	}
	localRoot := map[string]bool{}
	localByOwner := map[string]map[string]bool{}
	// Dozens of owner-local strips on the same Sugiyama layer compete for the
	// same horizontal cells. At that scale a packed rail is the more local
	// visual system overall, even though each individual root has one owner.
	useLocalClusters := !(len(nodes) > 100 && len(errRoots) >= 20)
	for _, root := range errRoots {
		if !useLocalClusters {
			continue
		}
		if len(errRef[root]) != 1 {
			continue
		}
		private := true
		for _, u := range rootMembers[root] {
			if memberRoots[u] != 1 {
				private = false
				break
			}
		}
		if !private {
			continue
		}
		owner := errRef[root][0]
		localRoot[root] = true
		if localByOwner[owner] == nil {
			localByOwner[owner] = map[string]bool{}
		}
		for _, u := range rootMembers[root] {
			localByOwner[owner][u] = true
		}
	}

	for _, owner := range g.ids {
		members := localByOwner[owner]
		op, ok := coords[owner]
		if !ok || len(members) == 0 {
			continue
		}
		baseX := op.X
		switch {
		case isCircle(g.byID[owner]):
			baseX -= layCircleXOffset
		case isCollapsedNode(g.byID[owner]):
			baseX -= layCollapsedXOffset
		}
		strip, _, _ := nodeClusterStrip(g, owner, members)
		for _, cp := range strip {
			n := g.byID[cp.id]
			collapse(cp.id)
			off := 0
			switch {
			case isCircle(n):
				off = layCircleXOffset
			case isCollapsedNode(n):
				off = layCollapsedXOffset
			}
			coords[cp.id] = lpoint{baseX + cp.dx + off, op.Y + cp.dy}
			placed[cp.id] = true
		}
	}

	srcRow := func(s string) int {
		if p, ok := coords[s]; ok {
			return p.Y
		}
		return 0
	}
	railX0 := layColX0 + layColStep
	if len(coords) > 0 {
		maxCx := math.MinInt
		for _, p := range coords {
			if p.X > maxCx {
				maxCx = p.X
			}
		}
		railX0 = maxCx + layColStep
	}
	rootsSorted := append([]string{}, errRoots...)
	sort.SliceStable(rootsSorted, func(i, j int) bool {
		mi, mj := math.MaxInt, math.MaxInt
		for _, s := range errRef[rootsSorted[i]] {
			if r := srcRow(s); r < mi {
				mi = r
			}
		}
		for _, s := range errRef[rootsSorted[j]] {
			if r := srcRow(s); r < mj {
				mj = r
			}
		}
		if mi == math.MaxInt {
			mi = 0
		}
		if mj == math.MaxInt {
			mj = 0
		}
		return mi < mj
	})
	// railNextY keeps the rail monotone: several owners often share one row
	// (parallel rays), and without a cursor their clusters would pile up at
	// the same y — more than the overlap resolver's pass budget can untangle.
	// It also caps how far AHEAD a cluster may jump: normally a cluster sits
	// beside the row of the owner that triggers it (traceable at a glance),
	// but when owners are scattered across a large flow that "chase the
	// owner" rule alone opens multi-thousand-pixel gaps in the rail. Past
	// layMaxRailGap the rail stays packed instead — a slightly looser
	// owner-alignment for that one cluster beats a canyon of empty rail.
	railNextY := math.MinInt
	haveRail := false
	for _, eid := range rootsSorted {
		if localRoot[eid] || !errc[eid] || placed[eid] {
			continue
		}
		var srcs []string
		for _, s := range errRef[eid] {
			if _, ok := coords[s]; ok {
				srcs = append(srcs, s)
			}
		}
		if len(srcs) == 0 {
			continue
		}
		sy := math.MaxInt
		for _, s := range srcs {
			if coords[s].Y < sy {
				sy = coords[s].Y
			}
		}
		if sy < railNextY {
			sy = railNextY
		} else if haveRail && sy > railNextY+layMaxRailGap {
			sy = railNextY + layMaxRailGap
		}
		haveRail = true
		colI, rowOff, maxRowOff := 0, 0, 0
		for _, u := range rootMembers[eid] {
			if placed[u] {
				continue
			}
			collapse(u)
			circ := isCircle(g.byID[u])
			cx := railX0 + layErrDX*colI
			off := 0
			if circ {
				off = layCircleXOffset
			}
			coords[u] = lpoint{cx + off, sy + rowOff}
			placed[u] = true
			nxts := 0
			for _, v := range g.succs(u) {
				if errc[v] {
					nxts++
				}
			}
			if nxts == 1 {
				colI++
			} else {
				colI = 0
				rowOff += layRowStep / 2
			}
			if rowOff > maxRowOff {
				maxRowOff = rowOff
			}
		}
		railNextY = sy + maxRowOff + layRowStep/2
	}

	// 3) orphans — a compact grid at the bottom, in the right zone
	var rest []string
	for _, n := range nodes {
		id := nodeStr(n, "id")
		if _, ok := coords[id]; !ok {
			rest = append(rest, id)
		}
	}
	if len(rest) > 0 {
		gy := layRowY0
		for _, p := range coords {
			if p.Y > gy {
				gy = p.Y
			}
		}
		gy += layRowStep
		gcols := int(math.Ceil(math.Sqrt(float64(len(rest)))))
		if gcols < 1 {
			gcols = 1
		}
		for i, u := range rest {
			collapse(u)
			r, c := i/gcols, i%gcols
			coords[u] = lpoint{railX0 + c*layErrDX, gy + r*(layRowStep/2)}
		}
	}

	resolveOverlaps(coords, g, layRowStep)
	coords = e.compact(coords, g)
	clampCoords(coords, layRowStep, layColStep)
	return coords
}
