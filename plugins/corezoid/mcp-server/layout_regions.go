package main

// Region detection and composition: TABLE bundles of isomorphic sibling
// pipelines and STAR fans are laid out as dedicated aligned grids, the
// residual graph as a waterfall.

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

type regionBundle struct {
	kind  string // "table" | "star"
	hub   string
	merge string
	cols  [][]string // table columns or star rays (rays depth-sorted desc)
	sinks []string   // side sinks fed only by the columns (sorted by id)
}

// columnChain follows the single forward path from head until stop (the merge
// candidate). Returns the chain, or nil when the branch is not chain-shaped.
func columnChain(head string, g *layoutGraph, stop string) []string {
	var chain []string
	seen := map[string]bool{}
	cur := head
	for cur != "" && cur != stop {
		if seen[cur] {
			break // loop back to own column — chain ends where it re-enters
		}
		seen[cur] = true
		chain = append(chain, cur)
		nxt, ok := g.primary[cur]
		if !ok {
			break
		}
		cur = nxt
		if cur == stop {
			break // a 65-node column ending exactly at the merge is valid
		}
		if len(chain) > 64 {
			return nil
		}
	}
	return chain
}

// columnSignature is the structural signature of one column: the sequence of
// logic-type sets plus, per node, where its side edges point (inside the
// column = LOOP, to the merge = MERGE, elsewhere = EXIT). Encoded as a string
// so it can key a map.
func columnSignature(chain []string, g *layoutGraph, colset map[string]bool, stop string) string {
	var sb strings.Builder
	for _, u := range chain {
		n := g.byID[u]
		kindSet := map[string]bool{}
		for _, lg := range nodeLogics(n) {
			kindSet[nodeStr(lg, "type")] = true
		}
		var kinds []string
		for k := range kindSet {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		var marks []string
		for _, b := range g.branches[u] {
			switch {
			case colset[b]:
				marks = append(marks, "LOOP")
			case b == stop:
				marks = append(marks, "MERGE")
			default:
				marks = append(marks, "EXIT")
			}
		}
		sort.Strings(marks)
		sb.WriteString(strings.Join(kinds, ","))
		sb.WriteString("|")
		sb.WriteString(strings.Join(marks, ","))
		sb.WriteString("|")
		if len(nodeSemaphors(n)) > 0 {
			sb.WriteString("T")
		}
		sb.WriteString(";")
	}
	return sb.String()
}

// hubHeads lists a hub's distinct forward targets (primary + branches),
// first-occurrence order.
func hubHeads(g *layoutGraph, hub string) []string {
	var heads []string
	seen := map[string]bool{}
	var raw []string
	if p, ok := g.primary[hub]; ok {
		raw = append(raw, p)
	}
	raw = append(raw, g.branches[hub]...)
	for _, h := range raw {
		if h != "" && !seen[h] {
			seen[h] = true
			heads = append(heads, h)
		}
	}
	return heads
}

// firstMerge finds the FIRST node (in BFS first-seen order — deterministic)
// reachable from at least need of the heads, excluding the heads themselves.
func firstMerge(g *layoutGraph, heads []string, need int) string {
	var order []string
	counts := map[string]int{}
	headSet := map[string]bool{}
	for _, h := range heads {
		headSet[h] = true
	}
	for _, h := range heads {
		seen := map[string]bool{}
		queue := []string{h}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			if seen[u] {
				continue
			}
			seen[u] = true
			if _, ok := counts[u]; !ok {
				order = append(order, u)
			}
			counts[u]++
			queue = append(queue, g.succs(u)...)
		}
	}
	for _, u := range order {
		if counts[u] >= need && !headSet[u] {
			return u
		}
	}
	return ""
}

// sideSinks computes the fixpoint of nodes fed ONLY by the given column nodes
// (over forward AND err edges) — a shared DLQ, the columns' escalations and
// their tails. Returned sorted by id (Python's sorted(sinks)).
func sideSinks(g *layoutGraph, colnodes map[string]bool, merge string) []string {
	feeders := map[string]map[string]bool{}
	for _, u := range g.ids {
		for _, v := range append(g.succs(u), g.errors[u]...) {
			if feeders[v] == nil {
				feeders[v] = map[string]bool{}
			}
			feeders[v][u] = true
		}
	}
	sinks := map[string]bool{}
	for grew := true; grew; {
		grew = false
		for _, u := range g.ids {
			fs := feeders[u]
			if sinks[u] || colnodes[u] || u == merge || len(fs) == 0 {
				continue
			}
			all := true
			for f := range fs {
				if !colnodes[f] && !sinks[f] {
					all = false
					break
				}
			}
			if all {
				sinks[u] = true
				grew = true
			}
		}
	}
	var out []string
	for s := range sinks {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// detectTableBundle finds the largest table bundle: >=3 isomorphic sibling
// pipelines from one hub reconverging on one merge. Returns nil when absent.
func detectTableBundle(nodes []map[string]interface{}) *regionBundle {
	g := buildLayoutGraph(nodes)
	var best *regionBundle
	bestSize := 0
	for _, hub := range g.ids {
		heads := hubHeads(g, hub)
		if len(heads) < 3 {
			continue
		}
		merge := firstMerge(g, heads, 3)
		if merge == "" {
			continue
		}
		groups := map[string][][]string{}
		var sigOrder []string
		for _, h := range heads {
			chain := columnChain(h, g, merge)
			if len(chain) < 2 {
				continue
			}
			colset := map[string]bool{}
			for _, u := range chain {
				colset[u] = true
			}
			sig := columnSignature(chain, g, colset, merge)
			if _, seen := groups[sig]; !seen {
				sigOrder = append(sigOrder, sig)
			}
			groups[sig] = append(groups[sig], chain)
		}
		for _, sig := range sigOrder {
			cols := groups[sig]
			if len(cols) < 3 {
				continue
			}
			size := 0
			for _, c := range cols {
				size += len(c)
			}
			if best == nil || size > bestSize {
				best = &regionBundle{kind: "table", hub: hub, merge: merge, cols: cols}
				bestSize = size
			}
		}
	}
	if best == nil {
		return nil
	}
	colnodes := map[string]bool{}
	for _, c := range best.cols {
		for _, u := range c {
			colnodes[u] = true
		}
	}
	best.sinks = sideSinks(g, colnodes, best.merge)
	return best
}

// detectStarBundle finds a STAR region: a hub fanning into >=4 chain-shaped
// rays of varying depth that reconverge on one merge.
func detectStarBundle(nodes []map[string]interface{}) *regionBundle {
	g := buildLayoutGraph(nodes)
	var best *regionBundle
	bestSize := 0
	for _, hub := range g.ids {
		heads := hubHeads(g, hub)
		if len(heads) < 4 {
			continue
		}
		merge := firstMerge(g, heads, len(heads))
		if merge == "" {
			continue
		}
		var rays [][]string
		ok := true
		for _, h := range heads {
			chain := columnChain(h, g, merge)
			if len(chain) == 0 {
				ok = false
				break
			}
			rays = append(rays, chain)
		}
		if !ok || len(rays) < 4 {
			continue
		}
		size := 0
		for _, r := range rays {
			size += len(r)
		}
		if best == nil || size > bestSize {
			best = &regionBundle{kind: "star", hub: hub, merge: merge, cols: rays}
			bestSize = size
		}
	}
	if best == nil {
		return nil
	}
	raynodes := map[string]bool{}
	for _, r := range best.cols {
		for _, u := range r {
			raynodes[u] = true
		}
	}
	best.sinks = sideSinks(g, raynodes, best.merge)
	// deepest rays first (stable)
	sort.SliceStable(best.cols, func(i, j int) bool { return len(best.cols[i]) > len(best.cols[j]) })
	return best
}

// deepCopyNodesJSON clones scheme.nodes through a JSON round trip (the
// Python engine's json.loads(json.dumps(...)) — region detection rewires
// edges and must not touch the caller's document).
func deepCopyNodesJSON(nodes []map[string]interface{}) []map[string]interface{} {
	b, err := json.Marshal(nodes)
	if err != nil {
		return nil
	}
	var out []map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// detectRegions repeatedly finds a table (preferred) or star bundle, collapses
// it to a virtual segment, and repeats on the residue. Returns the bundles and
// the reduced node list (a working copy).
func detectRegions(nodes []map[string]interface{}) ([]regionBundle, []map[string]interface{}) {
	work := deepCopyNodesJSON(nodes)
	var bundles []regionBundle
	for i := 0; i < 8; i++ { // safety cap on region count
		b := detectTableBundle(work)
		if b == nil {
			b = detectStarBundle(work)
		}
		if b == nil {
			break
		}
		hidden := map[string]bool{}
		for _, c := range b.cols {
			for _, u := range c {
				hidden[u] = true
			}
		}
		for _, s := range b.sinks {
			hidden[s] = true
		}
		bundles = append(bundles, *b)
		var kept []map[string]interface{}
		for _, n := range work {
			if !hidden[nodeStr(n, "id")] {
				kept = append(kept, n)
			}
		}
		for _, n := range kept {
			if nodeStr(n, "id") != b.hub {
				continue
			}
			for _, lg := range nodeLogics(n) {
				if hidden[nodeStr(lg, "to_node_id")] {
					lg["to_node_id"] = b.merge
				}
			}
			for _, sm := range nodeSemaphors(n) {
				if hidden[nodeStr(sm, "to_node_id")] {
					sm["to_node_id"] = b.merge
				}
			}
		}
		work = kept
	}
	return bundles, work
}

// layoutHybrid is the region composition: lay the residual graph out with the
// waterfall, then expand the bundles back as aligned grids.
func (e *layoutEngine) layoutHybrid(nodes []map[string]interface{}) map[string]lpoint {
	bundles, work := detectRegions(nodes)
	if len(bundles) == 0 {
		return e.layout(nodes)
	}

	coords := e.layout(work)
	g := buildLayoutGraph(nodes)

	// The residual layout ran on a working COPY: its collapse decisions
	// (extra.modeForm on err clusters and pure routers) must reach the real
	// document, or nodes get the collapsed x-offset while still rendering as
	// full 200px blocks. (This propagation fixes a bug the Python original
	// shipped with.)
	workByID := map[string]map[string]interface{}{}
	for _, n := range work {
		workByID[nodeStr(n, "id")] = n
	}
	for _, n := range nodes {
		if w := workByID[nodeStr(n, "id")]; w != nil {
			if isCollapsedNode(w) && !isCollapsedNode(n) {
				collapseNode(n)
			}
		}
	}

	gapV, gapH := 90, 90
	if gaps, ok := layDensityGaps[e.density]; ok {
		gapV, gapH = gaps[0], gaps[1]
	}

	collapseIfs := func(us map[string]bool) {
		for _, u := range g.inDocOrder(us) {
			if isPureRouter(g.byID[u]) {
				collapseNode(g.byID[u])
			}
		}
	}

	// expand bundles in reverse discovery order (outermost first: each
	// expansion makes vertical AND horizontal room before placing)
	for bi := len(bundles) - 1; bi >= 0; bi-- {
		b := bundles[bi]
		colnodes := map[string]bool{}
		for _, c := range b.cols {
			for _, u := range c {
				colnodes[u] = true
			}
		}
		collapseIfs(colnodes)
		colPitch := 200 + gapH

		var gridH int
		var rowH []int
		var xOff map[int]int
		var widthCols int
		if b.kind == "table" {
			depth := 0
			for _, c := range b.cols {
				if len(c) > depth {
					depth = len(c)
				}
			}
			rowH = make([]int, depth)
			for i := 0; i < depth; i++ {
				m := 0
				for _, c := range b.cols {
					if i < len(c) {
						if _, h := nodeBoxSize(g.byID[c[i]]); h > m {
							m = h
						}
					}
				}
				rowH[i] = m
			}
			for _, h := range rowH {
				gridH += h
			}
			gridH += gapV * depth
			xOff = map[int]int{}
			for ci := range b.cols {
				xOff[ci] = ci
			}
			widthCols = len(b.cols)
		} else {
			for _, r := range b.cols {
				h := 0
				for _, u := range r {
					_, bh := nodeBoxSize(g.byID[u])
					h += bh + gapV
				}
				if h > gridH {
					gridH = h
				}
			}
			// odd ray count: deepest on the axis, others alternating outward;
			// even count: no centre slot — pure ±1, ±2 pairs
			xOff = map[int]int{}
			side, mag := 1, 0
			startCi := 0
			if len(b.cols)%2 == 1 {
				xOff[0] = 0
				startCi = 1
			}
			for ci := startCi; ci < len(b.cols); ci++ {
				if side > 0 {
					mag++
				}
				xOff[ci] = side * mag
				side = -side
			}
			maxAbs := 0
			for _, v := range xOff {
				if abs(v) > maxAbs {
					maxAbs = abs(v)
				}
			}
			widthCols = 2*maxAbs + 1
		}

		ncols := widthCols
		if len(b.sinks) > 0 {
			ncols++
		}
		hp := coords[b.hub]
		_, hubH := nodeBoxSize(g.byID[b.hub])
		top := hp.Y + hubH + gapV
		mp := coords[b.merge]

		// vertical room: everything at/below the merge row moves down
		if need := (top + gridH) - mp.Y; need > 0 {
			for _, id := range g.ids {
				if p, ok := coords[id]; ok && p.Y >= mp.Y {
					coords[id] = lpoint{p.X, p.Y + need}
				}
			}
		}

		// horizontal room: parallel wings occupying the region band step aside
		hubCx := hp.X
		if !isCircle(g.byID[b.hub]) {
			hubCx += 100
		}
		totalW := colPitch*ncols - gapH
		left := float64(hubCx) - float64(colPitch*widthCols-gapH)/2.0
		spanL, spanR := left-float64(gapH), left+float64(totalW)+float64(gapH)
		for _, id := range g.ids {
			if id == b.hub {
				continue
			}
			p, ok := coords[id]
			if !ok || !(top <= p.Y && p.Y < top+gridH) {
				continue
			}
			x0, _, x1, _ := nodeBox(g.byID[id], p.X, p.Y)
			if float64(x0) < spanR && float64(x1) > spanL {
				if float64(x0+x1)/2.0 <= float64(hubCx) {
					coords[id] = lpoint{p.X - int(spanR-float64(x0)+float64(gapH)), p.Y}
				} else {
					coords[id] = lpoint{p.X + int(spanR-float64(x0)+float64(gapH)), p.Y}
				}
			}
		}

		// place the region's columns/rays
		axisCol := float64(widthCols-1) / 2.0
		for ci, chain := range b.cols {
			var colX float64
			if b.kind == "table" {
				colX = left + float64(ci*colPitch)
			} else {
				colX = left + (axisCol+float64(xOff[ci]))*float64(colPitch)
			}
			yy := float64(top)
			for ri, u := range chain {
				n := g.byID[u]
				_, h := nodeBoxSize(n)
				switch {
				case isCircle(n):
					coords[u] = lpoint{int(colX + 100), int(yy + float64(h)/2)}
				case isCollapsedNode(n):
					coords[u] = lpoint{int(colX + 76), int(yy)}
				default:
					coords[u] = lpoint{int(colX), int(yy)}
				}
				if b.kind == "table" {
					yy += float64(rowH[ri] + gapV)
				} else {
					yy += float64(h + gapV)
				}
			}
		}
		// keep the merge on the hub axis for the star silhouette
		if b.kind == "star" {
			mNode := g.byID[b.merge]
			mxNew := hubCx
			if !isCircle(mNode) {
				mxNew = hubCx - 100
			}
			coords[b.merge] = lpoint{mxNew, coords[b.merge].Y}
		}
		// side sinks: extra column
		sx := left + float64(colPitch*widthCols)
		yy := float64(top)
		for _, u := range b.sinks {
			n := g.byID[u]
			_, h := nodeBoxSize(n)
			off := 0
			if isCircle(n) {
				off = 100
			} else if isCollapsedNode(n) {
				off = 76
			}
			bump := 0
			if isCircle(n) {
				bump = h / 2
			}
			coords[u] = lpoint{int(sx) + off, int(yy) + bump}
			yy += float64(h + gapV)
		}
	}

	// finale: clamp first (scaling can re-introduce contact), then resolve,
	// then the density pass, then recentre
	maxY, minY := math.MinInt, math.MaxInt
	for _, p := range coords {
		if p.Y > maxY {
			maxY = p.Y
		}
		if p.Y < minY {
			minY = p.Y
		}
	}
	if len(coords) > 0 && maxY-minY > 19600 {
		sc := 19600.0 / float64(maxY-minY)
		mid := float64(maxY+minY) / 2.0
		for k, p := range coords {
			// The explicit float64 conversion forces IEEE rounding of the
			// product BEFORE the addition: without it the compiler may emit a
			// fused multiply-add on arm64 but not amd64, and the one-ulp
			// difference cascades into different integer pixels per platform
			// (caught by the golden files in CI).
			coords[k] = lpoint{p.X, int(mid + float64((float64(p.Y)-mid)*sc))}
		}
	}
	resolveOverlaps(coords, g, layRowStep)
	coords = e.compact(coords, g)
	for _, axis := range []int{1, 0} {
		maxV, minV := math.MinInt, math.MaxInt
		for _, p := range coords {
			v := p.X
			if axis == 1 {
				v = p.Y
			}
			if v > maxV {
				maxV = v
			}
			if v < minV {
				minV = v
			}
		}
		if maxV > 9900 || minV < -9900 {
			sh := -floorDiv(maxV+minV, 2)
			for k, p := range coords {
				if axis == 1 {
					coords[k] = lpoint{p.X, p.Y + sh}
				} else {
					coords[k] = lpoint{p.X + sh, p.Y}
				}
			}
		}
	}
	return coords
}
