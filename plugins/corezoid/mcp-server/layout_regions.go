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
	kind  string // "table" | "star" | "diamond"
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

// detectDiamondBundle finds a compact two-way fork/rejoin: a pure router with
// exactly two heads whose simple action chains reconverge within a few steps.
// TABLE/STAR intentionally start at 3/4 branches, so without this region the
// common "do work vs skip, then continue" shape gets stretched across the
// generic waterfall and its side branch drifts into error-handling columns.
func detectDiamondBundle(nodes []map[string]interface{}) *regionBundle {
	const maxDepth = 6
	g := buildLayoutGraph(nodes)

	distances := func(head string) map[string]int {
		out := map[string]int{head: 0}
		queue := []string{head}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			if out[u] >= maxDepth {
				continue
			}
			for _, v := range g.succs(u) {
				if old, seen := out[v]; seen && old <= out[u]+1 {
					continue
				}
				out[v] = out[u] + 1
				queue = append(queue, v)
			}
		}
		return out
	}

	simpleChain := func(head, merge string) ([]string, bool) {
		if head == merge {
			return []string{}, true
		}
		var chain []string
		seen := map[string]bool{}
		u := head
		for len(chain) <= maxDepth {
			if u == merge {
				return chain, true
			}
			if seen[u] {
				return nil, false
			}
			seen[u] = true
			chain = append(chain, u)
			next := g.succs(u)
			if len(next) != 1 {
				return nil, false
			}
			u = next[0]
		}
		return nil, false
	}

	var best *regionBundle
	bestSize := math.MaxInt
	for _, hub := range g.ids {
		if !isPureRouter(g.byID[hub]) {
			continue
		}
		heads := hubHeads(g, hub) // primary first, side branch second
		if len(heads) != 2 {
			continue
		}
		d0, d1 := distances(heads[0]), distances(heads[1])
		merge := ""
		bestMax, bestSum := math.MaxInt, math.MaxInt
		for _, candidate := range g.ids {
			a, oka := d0[candidate]
			b, okb := d1[candidate]
			if !oka || !okb || candidate == hub {
				continue
			}
			mx := a
			if b > mx {
				mx = b
			}
			sum := a + b
			if mx < bestMax || (mx == bestMax && sum < bestSum) {
				merge, bestMax, bestSum = candidate, mx, sum
			}
		}
		if merge == "" {
			continue
		}
		mainCol, ok0 := simpleChain(heads[0], merge)
		sideCol, ok1 := simpleChain(heads[1], merge)
		if !ok0 || !ok1 || len(mainCol)+len(sideCol) == 0 {
			continue
		}
		// Keep DIAMOND local. A large pair of subgraphs that merely share a
		// distant final is a tree/fractal, not a compact fork/rejoin region.
		size := len(mainCol) + len(sideCol)
		if size > maxDepth+1 || (len(mainCol) > 2 && len(sideCol) > 2) {
			continue
		}
		if best == nil || size < bestSize {
			best = &regionBundle{
				kind: "diamond", hub: hub, merge: merge,
				cols: [][]string{mainCol, sideCol},
			}
			bestSize = size
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

// detectRegions repeatedly finds a table (preferred), star or compact diamond
// bundle, collapses it to a virtual segment, and repeats on the residue.
// Returns the bundles and the reduced node list (a working copy).
func detectRegions(nodes []map[string]interface{}) ([]regionBundle, []map[string]interface{}) {
	work := deepCopyNodesJSON(nodes)
	var bundles []regionBundle
	for i := 0; i < 8; i++ { // safety cap on region count
		b := detectTableBundle(work)
		if b == nil {
			b = detectStarBundle(work)
		}
		if b == nil {
			b = detectDiamondBundle(work)
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

// clusterPlacement is one node of a dedicated error cluster, positioned
// relative to its owner (dx to the right, dy down from the owner's row).
type clusterPlacement struct {
	id     string
	dx, dy int
}

// nodeClusterStrip walks the owner's dedicated error cluster (its err targets
// plus their tails within members) as a compact staircase hugging the owner —
// the geometry distilled from hand-tuned production layouts: the entry sits
// layErrDX right of the owner and slightly BELOW its row (so the link leaves
// the row lane free), the primary chain steps down-right in small increments,
// and a branch that loops back to the flow (the retry Delay) stacks directly
// ABOVE its parent Condition, giving a short vertical link and a short return
// hop. Returns the placements plus the strip extent.
func nodeClusterStrip(g *layoutGraph, owner string, members map[string]bool) (out []clusterPlacement, w, h int) {
	const (
		stepX   = 150 // primary staircase advance (sized for a collapsed icon)
		stepY   = 30
		entryDY = 60 // below the owner's row, off the row lane
		loopDY  = -72
	)
	// Every vertical step below was written when layout collapsed cluster
	// members itself, so each one was a 48px icon and 30px/110px advances were
	// generous. Layout no longer changes modeForm: members now arrive at
	// whatever size the author left them, commonly a full 200x98+ block. A 30px
	// advance then buries the next node inside the previous one and leaves
	// resolveOverlaps to tear the strip apart afterwards — the "compact
	// staircase hugging its owner" is designed and then immediately destroyed.
	// Take the larger of the historical step and what the node actually needs.
	// The x steps are left alone: a 150px advance plus a cleared y is enough to
	// keep 200px boxes apart.
	advanceY := func(id string, want int) int {
		_, h := nodeBoxSize(g.byID[id])
		if need := h + 8; need > want { // 8 == resolveOverlaps' vgap
			return need
		}
		return want
	}
	seen := map[string]bool{}
	type item struct {
		id     string
		dx, dy int
	}
	var queue []item
	entryOff := entryDY
	for _, e := range g.errors[owner] {
		if members[e] && !seen[e] {
			seen[e] = true
			queue = append(queue, item{e, layErrDX, entryOff})
			entryOff += advanceY(e, 110)
		}
	}
	exitsToFlow := func(id string) bool {
		for _, v := range g.succs(id) {
			if !members[v] {
				return true
			}
		}
		return false
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		out = append(out, clusterPlacement{it.id, it.dx, it.dy})
		if x := it.dx + 250; x > w {
			w = x
		}
		_, bh := nodeBoxSize(g.byID[it.id])
		if b := it.dy + bh; b > h {
			h = b
		}
		belowOff := advanceY(it.id, 110)
		for _, v := range g.succs(it.id) {
			if !members[v] || seen[v] {
				continue
			}
			seen[v] = true
			switch {
			case v == g.primary[it.id]:
				queue = append(queue, item{v, it.dx + stepX, it.dy + advanceY(it.id, stepY)})
			case exitsToFlow(v):
				// the retry Delay: stacked above its Condition
				queue = append(queue, item{v, it.dx, it.dy - advanceY(v, -loopDY)})
			default:
				queue = append(queue, item{v, it.dx, it.dy + belowOff})
				belowOff += advanceY(v, 110)
			}
		}
	}
	return out, w, h
}

// nodeClusterStripFromEntry is nodeClusterStrip for a cluster addressed by
// its ENTRY node (the waterfall's multi-source clusters have no single owner).
func nodeClusterStripFromEntry(g *layoutGraph, entry string, members map[string]bool) (out []clusterPlacement, w, h int) {
	fake := &layoutGraph{
		nodes:    g.nodes,
		ids:      g.ids,
		byID:     g.byID,
		docIdx:   g.docIdx,
		primary:  g.primary,
		branches: g.branches,
		errors:   map[string][]string{"\x00entry": {entry}},
	}
	return nodeClusterStrip(fake, "\x00entry", members)
}

// splitSinkOwnership partitions a region's side sinks into per-column-node
// dedicated clusters (reachable from exactly ONE column node's error paths)
// and the truly shared remainder.
func splitSinkOwnership(g *layoutGraph, cols [][]string, sinks []string) (map[string][]string, []string) {
	sinkSet := map[string]bool{}
	for _, s := range sinks {
		sinkSet[s] = true
	}
	owners := map[string]map[string]bool{} // sink -> owner col nodes
	for _, c := range cols {
		for _, u := range c {
			var stack []string
			for _, e := range g.errors[u] {
				if sinkSet[e] {
					stack = append(stack, e)
				}
			}
			visited := map[string]bool{}
			for len(stack) > 0 {
				v := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if visited[v] || !sinkSet[v] {
					continue
				}
				visited[v] = true
				if owners[v] == nil {
					owners[v] = map[string]bool{}
				}
				owners[v][u] = true
				stack = append(stack, g.succs(v)...)
				stack = append(stack, g.errors[v]...)
			}
		}
	}
	pinned := map[string][]string{}
	var shared []string
	for _, s := range sinks { // sinks are sorted → deterministic
		os := owners[s]
		if len(os) == 1 {
			var owner string
			for u := range os {
				owner = u
			}
			pinned[owner] = append(pinned[owner], s)
		} else {
			shared = append(shared, s)
		}
	}
	return pinned, shared
}

// hybridScore is deliberately lexicographic: removing an edge crossing is
// worth more than shaving pixels from otherwise readable links. Keeping mask
// zero on an exact tie preserves the historical "alternative branch right"
// layout and prevents gratuitous churn in existing processes.
type hybridScore struct {
	crossings int
	upward    int
	long      int
	p95       int
	max       int
	dedicated int
}

func scoreHybrid(coords map[string]lpoint, nodes []map[string]interface{}) hybridScore {
	r := measureLayoutReadability(coords, buildLayoutGraph(nodes))
	return hybridScore{
		crossings: r.EdgeCrossings,
		upward:    r.UpwardEdges,
		long:      r.LongEdges,
		p95:       r.P95EdgeSpan,
		max:       r.MaxEdgeSpan,
		dedicated: r.MaxDedicatedSpan,
	}
}

func betterHybridScore(a, b hybridScore) bool {
	av := [...]int{a.crossings, a.upward, a.long, a.p95, a.max, a.dedicated}
	bv := [...]int{b.crossings, b.upward, b.long, b.p95, b.max, b.dedicated}
	for i := range av {
		if av[i] != bv[i] {
			return av[i] < bv[i]
		}
	}
	return false
}

func safeHybridAlternative(candidate, baseline hybridScore) bool {
	// A crossing reduction is not a win when it turns short local links into
	// long diagonals. Use the historical right-side orientation as a safety
	// baseline and admit only Pareto-safe alternatives.
	return candidate.upward <= baseline.upward &&
		candidate.long <= baseline.long &&
		candidate.p95 <= baseline.p95 &&
		candidate.max <= baseline.max &&
		candidate.dedicated <= baseline.dedicated
}

// layoutHybrid is the region composition: lay the residual graph out with the
// waterfall, then expand the bundles back as aligned grids. Compact DIAMOND
// regions may put their alternative chain on either side of the primary axis.
// For the small number of detected regions (capped by detectRegions), evaluate
// every orientation and keep the most readable deterministic result.
func (e *layoutEngine) layoutHybrid(nodes []map[string]interface{}) map[string]lpoint {
	bundles, _ := detectRegions(nodes)
	diamondCount := 0
	for _, b := range bundles {
		if b.kind == "diamond" && len(b.cols) == 2 && len(b.cols[0]) > 0 && len(b.cols[1]) > 0 {
			diamondCount++
		}
	}
	if diamondCount == 0 {
		return e.layoutHybridOriented(nodes, 0)
	}

	// The candidates are NOT cheap: each one is a full layout of the entire
	// process on a deep copy. Measured on the fractal fixtures: 4 diamonds =
	// 16 candidates = 227ms, but 8 diamonds = 256 candidates = 23 SECONDS.
	// That size sits inside the automatic push path (maxEngineLayoutNodes is
	// 250, and this strategy is gated to <=80 nodes), so a first push of such
	// a process would stall for half a minute. Node count is the wrong thing
	// to bound here — the exponent is.
	//
	// Enumerate orientations for at most maxSearchedDiamonds of them, in
	// detectRegions order (deterministic). The remainder keep the baseline
	// orientation, i.e. the historical alternative-branch-right layout, so the
	// result degrades to "good default" instead of becoming invalid.
	const maxSearchedDiamonds = 4
	searched := diamondCount
	if searched > maxSearchedDiamonds {
		searched = maxSearchedDiamonds
	}
	var bestMask uint
	var bestScore hybridScore
	var baseline hybridScore
	haveBest := false
	for mask := uint(0); mask < uint(1)<<searched; mask++ {
		candidateNodes := deepCopyNodesJSON(nodes)
		candidate := e.layoutHybridOriented(candidateNodes, mask)
		score := scoreHybrid(candidate, candidateNodes)
		if mask == 0 {
			baseline = score
		} else if !safeHybridAlternative(score, baseline) {
			continue
		}
		if !haveBest || betterHybridScore(score, bestScore) {
			bestMask, bestScore, haveBest = mask, score, true
		}
	}
	return e.layoutHybridOriented(nodes, bestMask)
}

func (e *layoutEngine) layoutHybridOriented(nodes []map[string]interface{}, diamondMask uint) map[string]lpoint {
	bundles, work := detectRegions(nodes)
	if len(bundles) == 0 {
		return e.layout(nodes)
	}
	diamondOrdinal := make([]int, len(bundles))
	nextDiamond := 0
	for i := range diamondOrdinal {
		diamondOrdinal[i] = -1
		if bundles[i].kind == "diamond" && len(bundles[i].cols) == 2 &&
			len(bundles[i].cols[0]) > 0 && len(bundles[i].cols[1]) > 0 {
			diamondOrdinal[i] = nextDiamond
			nextDiamond++
		}
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

		// Per-node error clusters stay WITH their owner: sinks reachable from
		// exactly one column node are pinned right of that node (the column
		// pitch widens to make room); only truly shared sinks keep the side
		// column. Cluster members collapse like every railed error node.
		pinned, sharedSinks := splitSinkOwnership(g, b.cols, b.sinks)
		clusterExtW := map[string]int{}
		clusterExtH := map[string]int{}
		maxClusterW := 0
		for owner, members := range pinned {
			mset := map[string]bool{}
			for _, m := range members {
				mset[m] = true
				// the whole dedicated cluster collapses, INCLUDING the named
				// Error final (hand-tuned production layouts collapse it too)
				collapseNode(g.byID[m])
			}
			_, w, h := nodeClusterStrip(g, owner, mset)
			clusterExtW[owner] = w
			clusterExtH[owner] = h
			if w > maxClusterW {
				maxClusterW = w
			}
		}
		colPitch := 200 + gapH + maxClusterW

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
						if h := clusterExtH[c[i]]; h > m {
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
					if ext := clusterExtH[u]; ext > bh {
						bh = ext
					}
					h += bh + gapV
				}
				if h > gridH {
					gridH = h
				}
			}
			xOff = map[int]int{}
			if b.kind == "diamond" {
				if len(b.cols) == 2 && (len(b.cols[0]) == 0 || len(b.cols[1]) == 0) {
					// "Do work or skip" needs no visual lane for the empty
					// branch. Keeping the sole action chain on the hub axis
					// avoids a gratuitous diagonal and, when several such
					// diamonds share a merge, prevents cumulative widening.
					xOff[0], xOff[1] = 0, 0
					widthCols = 1
				} else {
					// Main/default chain stays on the hub axis; the alternative
					// chain occupies an immediately adjacent column. The wrapper
					// chooses left/right from whole-layout readability metrics.
					xOff[0] = 0
					side := 1
					if ord := diamondOrdinal[bi]; ord >= 0 && diamondMask&(uint(1)<<ord) != 0 {
						side = -1
					}
					xOff[1] = side
					// A virtual empty left slot keeps axis arithmetic identical
					// to STAR and leaves the hub/merge visually centred.
					widthCols = 3
				}
			} else {
				// odd ray count: deepest on the axis, others alternating
				// outward; even count: no centre slot — pure ±1, ±2 pairs
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
		}

		ncols := widthCols
		if len(sharedSinks) > 0 {
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
		hx0, _, hx1, _ := nodeBox(g.byID[b.hub], hp.X, hp.Y)
		hubCx := (hx0 + hx1) / 2
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
				if members := pinned[u]; len(members) > 0 {
					mset := map[string]bool{}
					for _, m := range members {
						mset[m] = true
					}
					strip, _, _ := nodeClusterStrip(g, u, mset)
					for _, cp := range strip {
						mn := g.byID[cp.id]
						off := 0
						if isCircle(mn) {
							off = layCircleXOffset
						} else if isCollapsedNode(mn) {
							off = layCollapsedXOffset
						}
						coords[cp.id] = lpoint{int(colX) + cp.dx + off, int(yy) + cp.dy}
					}
				}
				if b.kind == "table" {
					yy += float64(rowH[ri] + gapV)
				} else {
					h2 := h
					if ext := clusterExtH[u]; ext > h2 {
						h2 = ext
					}
					yy += float64(h2 + gapV)
				}
			}
		}
		// keep the merge on the hub axis for STAR and DIAMOND silhouettes
		if b.kind == "star" || b.kind == "diamond" {
			mNode := g.byID[b.merge]
			mxNew := hubCx
			if !isCircle(mNode) {
				mw, _ := nodeBoxSize(mNode)
				mxNew = hubCx - mw/2
			}
			coords[b.merge] = lpoint{mxNew, coords[b.merge].Y}
		}
		// side sinks: extra column
		sx := left + float64(colPitch*widthCols)
		yy := float64(top)
		for _, u := range sharedSinks {
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
