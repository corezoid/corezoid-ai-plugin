package main

// The engine's behavioural invariants (I1–I10), ported from the skill's
// former test_layout.py and run over every synthetic fixture.

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// deepCopyNodes clones a fixture so a layout's extra/coordinate mutations
// don't leak between test cases.
func deepCopyNodes(t *testing.T, nodes []map[string]interface{}) []map[string]interface{} {
	t.Helper()
	out := make([]map[string]interface{}, len(nodes))
	for i, n := range nodes {
		out[i] = deepCopyMap(n)
	}
	return out
}

func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return deepCopyMap(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, it := range t {
			out[i] = deepCopyValue(it)
		}
		return out
	}
	return v
}

// overlapPairs lists intersecting node-box pairs using the REAL box sizes —
// the I2 hard requirement.
func overlapPairs(coords map[string]lpoint, g *layoutGraph) []string {
	var out []string
	for i, a := range g.ids {
		pa, ok := coords[a]
		if !ok {
			continue
		}
		ax0, ay0, ax1, ay1 := nodeBox(g.byID[a], pa.X, pa.Y)
		for _, b := range g.ids[i+1:] {
			pb, ok := coords[b]
			if !ok {
				continue
			}
			bx0, by0, bx1, by1 := nodeBox(g.byID[b], pb.X, pb.Y)
			if ax0 < bx1 && bx0 < ax1 && ay0 < by1 && by0 < ay1 {
				out = append(out, fmt.Sprintf("%s(%d,%d) x %s(%d,%d)", a, pa.X, pa.Y, b, pb.X, pb.Y))
			}
		}
	}
	return out
}

// TestLayoutInvariants_AllFixtures runs the property-style invariants over
// every fixture: I1 all nodes placed, I2 no overlaps, I3 determinism, I4
// within the ±10000 canvas.
func TestLayoutInvariants_AllFixtures(t *testing.T) {
	for _, fx := range allFixtures() {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			nodes := deepCopyNodes(t, fx.nodes)
			e := &layoutEngine{density: "medium"}
			coords, rep := e.computeLayout(nodes)

			// I1: every node received coordinates
			if len(coords) != len(nodes) {
				t.Errorf("I1: placed %d of %d nodes", len(coords), len(nodes))
			}
			// I2: zero box overlaps
			g := buildLayoutGraph(nodes)
			if pairs := overlapPairs(coords, g); len(pairs) > 0 {
				t.Errorf("I2: %d overlapping pairs (strategy %s), first: %s", len(pairs), rep.Strategy, pairs[0])
			}
			if rep.Overlaps != 0 {
				t.Errorf("I2: report claims %d overlaps", rep.Overlaps)
			}
			// I3: a second run over a fresh copy yields identical coordinates
			nodes2 := deepCopyNodes(t, fx.nodes)
			coords2, _ := (&layoutEngine{density: "medium"}).computeLayout(nodes2)
			if !reflect.DeepEqual(coords, coords2) {
				t.Errorf("I3: layout is not deterministic")
			}
			// I4: the whole rendered BOX must fit the canvas, not just the
			// pivot. Blocks are top-left pivoted, so a pivot comfortably inside
			// the limit can still hang its full height over the edge — mega's
			// pivots stop at y=9812 while its boxes reach y=9958.
			for _, id := range g.ids {
				p, ok := coords[id]
				if !ok {
					continue
				}
				x0, y0, x1, y1 := nodeBox(g.byID[id], p.X, p.Y)
				if x0 < -10000 || x1 > 10000 || y0 < -10000 || y1 > 10000 {
					t.Errorf("I4: %s box (%d,%d)-(%d,%d) leaves the ±10000 canvas", id, x0, y0, x1, y1)
					break
				}
			}
		})
	}
}

// TestLayoutI5_TopDownFlow: the Start node sits in the top half of the diagram.
func TestLayoutI5_TopDownFlow(t *testing.T) {
	for _, fx := range allFixtures() {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			nodes := deepCopyNodes(t, fx.nodes)
			coords, _ := (&layoutEngine{density: "medium"}).computeLayout(nodes)
			startID := ""
			for _, n := range nodes {
				if nodeObjType(n) == 1 {
					startID = nodeStr(n, "id")
					break
				}
			}
			if startID == "" {
				t.Skip("fixture has no Start node")
			}
			var ys []int
			for _, p := range coords {
				ys = append(ys, p.Y)
			}
			sort.Ints(ys)
			median := ys[len(ys)/2]
			if coords[startID].Y > median {
				t.Errorf("I5: Start at y=%d is below the median %d", coords[startID].Y, median)
			}
		})
	}
}

// TestLayoutI7_AddNodeStable: adding a node mid-chain re-flows the layout
// without introducing overlaps.
func TestLayoutI7_AddNodeStable(t *testing.T) {
	nodes := deepCopyNodes(t, topoChain())
	e := &layoutEngine{density: "medium"}
	if _, rep := e.computeLayout(nodes); rep.Overlaps != 0 {
		t.Fatalf("baseline layout has overlaps")
	}

	// splice a new node after the 5th chain node
	g := buildLayoutGraph(nodes)
	anchor := nodes[5]
	next := g.primary[nodeStr(anchor, "id")]
	gen := &fixGen{seq: 9000}
	inserted := gen.code("inserted-step", next)
	setGo(anchor, nodeStr(inserted, "id"))
	nodes = append(nodes, inserted)

	coords, rep := (&layoutEngine{density: "medium"}).computeLayout(nodes)
	if len(coords) != len(nodes) {
		t.Fatalf("I7: inserted node not placed")
	}
	g2 := buildLayoutGraph(nodes)
	if pairs := overlapPairs(coords, g2); len(pairs) > 0 {
		t.Errorf("I7: %d overlaps after inserting a node, first: %s", len(pairs), pairs[0])
	}
	_ = rep
}

// TestLayoutI8_NoWastedAir: the gap between adjacent occupied rows must not
// exceed the configured row gap — a row of collapsed 48px squares may not
// reserve a full block-row of empty space.
func TestLayoutI8_NoWastedAir(t *testing.T) {
	gapV := layDensityGaps["medium"][0]
	for _, fx := range allFixtures() {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			nodes := deepCopyNodes(t, fx.nodes)
			coords, _ := (&layoutEngine{density: "medium"}).computeLayout(nodes)
			g := buildLayoutGraph(nodes)
			type box struct{ top, bot int }
			type row struct{ top, bot, lastTop int }
			var items []box
			for _, id := range g.ids {
				p, ok := coords[id]
				if !ok {
					continue
				}
				_, y0, _, y1 := nodeBox(g.byID[id], p.X, p.Y)
				items = append(items, box{y0, y1})
			}
			sort.Slice(items, func(i, j int) bool { return items[i].top < items[j].top })
			var clusters []row
			for _, b := range items {
				if len(clusters) > 0 && b.top-clusters[len(clusters)-1].lastTop <= 55 {
					last := &clusters[len(clusters)-1]
					if b.bot > last.bot {
						last.bot = b.bot
					}
					last.lastTop = b.top
				} else {
					clusters = append(clusters, row{b.top, b.bot, b.top})
				}
			}
			for i := 1; i < len(clusters); i++ {
				gap := clusters[i].top - clusters[i-1].bot
				if gap > gapV+4 {
					t.Errorf("I8: %dpx of air between rows %d..%d and %d..%d (limit %d)",
						gap, clusters[i-1].top, clusters[i-1].bot, clusters[i].top, clusters[i].bot, gapV)
				}
			}
		})
	}
}

// TestLayoutI8b_RowNeighboursNotGlued: adjacent boxes in one row keep a
// readable horizontal gap.
func TestLayoutI8b_RowNeighboursNotGlued(t *testing.T) {
	for _, fx := range allFixtures() {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			nodes := deepCopyNodes(t, fx.nodes)
			coords, _ := (&layoutEngine{density: "medium"}).computeLayout(nodes)
			g := buildLayoutGraph(nodes)
			type box struct{ x0, y0, x1, y1 int }
			var boxes []box
			for _, id := range g.ids {
				p, ok := coords[id]
				if !ok {
					continue
				}
				x0, y0, x1, y1 := nodeBox(g.byID[id], p.X, p.Y)
				boxes = append(boxes, box{x0, y0, x1, y1})
			}
			sort.Slice(boxes, func(i, j int) bool {
				if boxes[i].y0 != boxes[j].y0 {
					return boxes[i].y0 < boxes[j].y0
				}
				return boxes[i].x0 < boxes[j].x0
			})
			for i, a := range boxes {
				for _, b := range boxes[i+1:] {
					// same row = vertical overlap of boxes, b to the right
					if b.y0 < a.y1 && a.y0 < b.y1 && b.x0 >= a.x1 {
						if gap := b.x0 - a.x1; gap < 24 {
							t.Errorf("I8b: same-row boxes only %dpx apart", gap)
						}
						break
					}
				}
			}
		})
	}
}

// TestLayoutI6_Routing: a single business spine with dedicated one-owner error
// clusters stays a waterfall even when error nodes dominate the raw node
// count. Ownership, not errFrac alone, determines whether a global rail helps.
func TestLayoutI6_Routing(t *testing.T) {
	for _, fx := range allFixtures() {
		if fx.name != "errheavy" {
			continue
		}
		nodes := deepCopyNodes(t, fx.nodes)
		_, label, reason := (&layoutEngine{density: "medium"}).analyzeLayout(nodes)
		if label != "waterfall" {
			t.Errorf("I6: errheavy routed to %q (%s)", label, reason)
		}
	}
}

func TestLayoutI6c_SharedErrorMeshUsesLayeredRail(t *testing.T) {
	g := &fixGen{}
	errFinal := g.final("shared error", false)
	errTail, errHead := g.chainOf(11, "shared-error-step", nodeStr(errFinal, "id"))
	fin := g.final("done", true)
	biz, bizHead := g.chainOf(12, "business", nodeStr(fin, "id"))
	for _, n := range biz {
		nodeLogics(n)[0]["err_node_id"] = errHead
	}
	start := g.start(bizHead)
	nodes := append([]map[string]interface{}{start}, biz...)
	nodes = append(nodes, fin)
	nodes = append(nodes, errTail...)
	nodes = append(nodes, errFinal)

	topo := buildLayoutGraph(nodes).classifyErrors()
	if topo.Shared != 1 || topo.Dedicated != 0 {
		t.Fatalf("fixture ownership = %+v, want one shared root", topo)
	}
	_, label, reason := (&layoutEngine{density: "medium"}).analyzeLayout(nodes)
	if label != "layered+error-rail" {
		t.Fatalf("shared error mesh routed to %q (%s)", label, reason)
	}
}

// TestLayoutI6b_SmallMultiflowStaysWaterfall: a small process (<25 nodes)
// must stay waterfall even when extra entry points inflate the flow count.
func TestLayoutI6b_SmallMultiflowStaysWaterfall(t *testing.T) {
	gen := &fixGen{}
	fin := gen.final("Final", true)
	nodes := []map[string]interface{}{fin}
	var heads []string
	for i := 0; i < 4; i++ { // 4 independent entry chains → flows > 3
		br, head := gen.chainOf(3, fmt.Sprintf("flow%d", i), nodeStr(fin, "id"))
		nodes = append(nodes, br...)
		heads = append(heads, head)
	}
	nodes = append(nodes, gen.start(heads[0]))
	g := buildLayoutGraph(nodes)
	if flows := g.countForwardFlows(); flows <= 3 {
		t.Fatalf("fixture must be multi-flow, got %d flows", flows)
	}
	_, label, _ := (&layoutEngine{density: "medium"}).analyzeLayout(nodes)
	if label != "waterfall" {
		t.Errorf("I6b: small multi-flow routed to %q", label)
	}
}

// TestLayoutI9_TableColumnsAligned: isomorphic sibling pipelines are drawn as
// a TABLE — every column's steps sit on the same rows with a uniform pitch.
func TestLayoutI9_TableColumnsAligned(t *testing.T) {
	for _, name := range []string{"table3", "tables2", "combo"} {
		name := name
		t.Run(name, func(t *testing.T) {
			var nodes []map[string]interface{}
			for _, fx := range allFixtures() {
				if fx.name == name {
					nodes = deepCopyNodes(t, fx.nodes)
				}
			}
			e := &layoutEngine{density: "medium"}
			fn, label, _ := e.analyzeLayout(nodes)
			if label != "waterfall+regions" {
				t.Fatalf("I9: %s routed to %q", name, label)
			}
			coords := fn(nodes)
			bundle := detectTableBundle(nodes)
			if bundle == nil {
				t.Fatalf("I9: bundle not detected after layout")
			}
			var ys0 []int
			for _, u := range bundle.cols[0] {
				ys0 = append(ys0, coords[u].Y)
			}
			var xs []int
			for _, c := range bundle.cols {
				xs = append(xs, coords[c[0]].X)
			}
			sort.Ints(xs)
			for _, c := range bundle.cols[1:] {
				var ys []int
				for _, u := range c {
					ys = append(ys, coords[u].Y)
				}
				if !reflect.DeepEqual(ys, ys0) {
					t.Errorf("I9: rows not aligned: %v vs %v", ys, ys0)
				}
			}
			pitches := map[int]bool{}
			for i := 1; i < len(xs); i++ {
				pitches[xs[i]-xs[i-1]] = true
			}
			if len(pitches) != 1 {
				t.Errorf("I9: column pitch not uniform: %v", pitches)
			}
		})
	}
}

// starOffsets extracts the ray-head offsets (in half-pitch units) around the
// hub axis, mirroring the Python assertion arithmetic.
func starOffsets(t *testing.T, nodes []map[string]interface{}, coords map[string]lpoint) []int {
	t.Helper()
	regions, _ := detectRegions(nodes)
	var star *regionBundle
	for i := range regions {
		if regions[i].kind == "star" {
			star = &regions[i]
			break
		}
	}
	if star == nil {
		t.Fatalf("star region not detected")
	}
	g := buildLayoutGraph(nodes)
	hx0, _, hx1, _ := nodeBox(g.byID[star.hub], coords[star.hub].X, coords[star.hub].Y)
	hubCx := (hx0 + hx1) / 2
	mx0, _, mx1, _ := nodeBox(g.byID[star.merge], coords[star.merge].X, coords[star.merge].Y)
	mCx := (mx0 + mx1) / 2
	if abs(mCx-hubCx) > 4 {
		t.Errorf("I10: merge off axis: %d vs %d", mCx, hubCx)
	}
	var offs []int
	for _, r := range star.cols {
		x0, _, x1, _ := nodeBox(g.byID[r[0]], coords[r[0]].X, coords[r[0]].Y)
		center := float64(x0+x1) / 2.0
		offs = append(offs, pyRound((center-float64(hubCx))/145.0))
	}
	sort.Ints(offs)
	return offs
}

func symmetric(offs []int) bool {
	neg := make([]int, len(offs))
	for i, o := range offs {
		neg[i] = -o
	}
	sort.Ints(neg)
	return reflect.DeepEqual(offs, neg)
}

// TestLayoutI10_StarSymmetricAndCombo: star rays hang symmetrically around
// the hub→merge axis; mixed star+table combos compose; an even ray count
// skips the centre slot.
func TestLayoutI10_StarSymmetricAndCombo(t *testing.T) {
	var combo, star4 []map[string]interface{}
	for _, fx := range allFixtures() {
		switch fx.name {
		case "combo":
			combo = deepCopyNodes(t, fx.nodes)
		case "star4":
			star4 = deepCopyNodes(t, fx.nodes)
		}
	}
	e := &layoutEngine{density: "medium"}
	fn, label, reason := e.analyzeLayout(combo)
	if label != "waterfall+regions" {
		t.Fatalf("I10: combo routed to %q", label)
	}
	if !strings.Contains(reason, "star(") || !strings.Contains(reason, "table(") {
		t.Errorf("I10: both kinds expected in reason: %s", reason)
	}
	coords := fn(combo)
	offs := starOffsets(t, combo, coords)
	if !symmetric(offs) {
		t.Errorf("I10: rays not symmetric: %v", offs)
	}

	fn4, label4, _ := e.analyzeLayout(star4)
	if label4 != "waterfall+regions" {
		t.Fatalf("I10: star4 routed to %q", label4)
	}
	coords4 := fn4(star4)
	offs4 := starOffsets(t, star4, coords4)
	if !symmetric(offs4) {
		t.Errorf("I10: even star not symmetric: %v", offs4)
	}
	for _, o := range offs4 {
		if o == 0 {
			t.Errorf("I10: even star must skip the centre slot: %v", offs4)
		}
	}
}

// TestLayoutI11_ModeFormsNeverChange: node expansion is user-owned visual
// state. Layout may change coordinates only, regardless of strategy-local
// preferences for compact routers or error clusters.
func TestLayoutI11_ModeFormsNeverChange(t *testing.T) {
	var combo []map[string]interface{}
	for _, fx := range allFixtures() {
		if fx.name == "combo" {
			combo = deepCopyNodes(t, fx.nodes)
		}
	}
	before := saveNodeExtras(combo)
	e := &layoutEngine{density: "medium"}
	coords, rep := e.computeLayout(combo)
	if rep.Strategy != "waterfall+regions" {
		t.Fatalf("combo routed to %q", rep.Strategy)
	}
	after := saveNodeExtras(combo)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("layout changed node extra/modeForm")
	}
	if pairs := overlapPairs(coords, buildLayoutGraph(combo)); len(pairs) > 0 {
		t.Fatalf("mode-preserving hybrid layout has overlaps: %s", pairs[0])
	}
	for _, n := range combo {
		if nodeStr(n, "title") == "star route?" || nodeStr(n, "title") == "table route?" {
			if isCollapsedNode(n) {
				t.Errorf("I11: %q changed from expanded to collapsed", nodeStr(n, "title"))
			}
		}
	}
}

func TestLayoutI12_DiamondKeepsMainAxisAndSideBranchLocal(t *testing.T) {
	g := &fixGen{}
	fin := g.final("done", true)
	merge := g.code("merge", nodeStr(fin, "id"))
	main, mainHead := g.chainOf(2, "main", nodeStr(merge, "id"))
	side, sideHead := g.chainOf(1, "side", nodeStr(merge, "id"))
	hub := g.cond("do optional work?", []fixBranch{{"run", "yes", sideHead}}, mainHead)
	pre, preHead := g.chainOf(18, "pre", nodeStr(hub, "id"))
	start := g.start(preHead)
	nodes := append([]map[string]interface{}{start}, pre...)
	nodes = append(nodes, hub)
	nodes = append(nodes, main...)
	nodes = append(nodes, side...)
	nodes = append(nodes, merge, fin)

	b := detectDiamondBundle(nodes)
	if b == nil {
		t.Fatal("compact two-way fork/rejoin was not detected as DIAMOND")
	}
	e := &layoutEngine{density: "medium"}
	fn, label, reason := e.analyzeLayout(nodes)
	if label != "waterfall+regions" || !strings.Contains(reason, "diamond(") {
		t.Fatalf("diamond routed to %q (%s)", label, reason)
	}
	coords := fn(nodes)
	graph := buildLayoutGraph(nodes)
	centerX := func(id string) int {
		p := coords[id]
		x0, _, x1, _ := nodeBox(graph.byID[id], p.X, p.Y)
		return (x0 + x1) / 2
	}
	hubX := centerX(nodeStr(hub, "id"))
	if dx := abs(centerX(mainHead) - hubX); dx > 4 {
		t.Errorf("main diamond chain is %dpx off the hub axis: hub=%d main=%d", dx, hubX, centerX(mainHead))
	}
	if centerX(sideHead) <= hubX {
		t.Errorf("side diamond chain must be immediately right of the hub")
	}
	if dx := abs(centerX(nodeStr(merge, "id")) - hubX); dx > 4 {
		t.Errorf("diamond merge is %dpx off the hub axis: hub=%d merge=%d", dx, hubX, centerX(nodeStr(merge, "id")))
	}
	if pairs := overlapPairs(coords, graph); len(pairs) > 0 {
		t.Fatalf("diamond layout has overlaps: %s", pairs[0])
	}
}

func TestLayoutI12b_DiamondSkipBranchDoesNotConsumeAColumn(t *testing.T) {
	g := &fixGen{}
	fin := g.final("done", true)
	merge := g.code("merge", nodeStr(fin, "id"))
	side := g.code("optional work", nodeStr(merge, "id"))
	// The default route skips directly to merge; only the conditional branch
	// contains a visible node.
	hub := g.cond("do optional work?", []fixBranch{{"run", "yes", nodeStr(side, "id")}}, nodeStr(merge, "id"))
	pre, preHead := g.chainOf(18, "pre", nodeStr(hub, "id"))
	start := g.start(preHead)
	nodes := append([]map[string]interface{}{start}, pre...)
	nodes = append(nodes, hub, side, merge, fin)

	e := &layoutEngine{density: "medium"}
	bundle := detectDiamondBundle(nodes)
	if bundle == nil || len(bundle.cols) != 2 || len(bundle.cols[0]) != 0 || len(bundle.cols[1]) != 1 {
		t.Fatalf("skip diamond not detected with empty default branch: %#v", bundle)
	}
	coords := e.layoutHybrid(nodes)
	graph := buildLayoutGraph(nodes)
	centerX := func(id string) int {
		p := coords[id]
		x0, _, x1, _ := nodeBox(graph.byID[id], p.X, p.Y)
		return (x0 + x1) / 2
	}
	hubX := centerX(nodeStr(hub, "id"))
	if dx := abs(centerX(nodeStr(side, "id")) - hubX); dx > 4 {
		t.Errorf("sole visible diamond chain consumes a side column: hub=%d action=%d", hubX, centerX(nodeStr(side, "id")))
	}
	if dx := abs(centerX(nodeStr(merge, "id")) - hubX); dx > 4 {
		t.Errorf("skip diamond merge is %dpx off the hub axis", dx)
	}
	if pairs := overlapPairs(coords, graph); len(pairs) > 0 {
		t.Fatalf("skip diamond layout has overlaps: %s", pairs[0])
	}
}

func TestLayoutI13_DetachedComponentsStayBelowActiveFlow(t *testing.T) {
	g := &fixGen{}
	activeFinal := g.final("active done", true)
	active, activeHead := g.chainOf(6, "active", nodeStr(activeFinal, "id"))
	start := g.start(activeHead)

	archiveFinal := g.final("archived done", true)
	archive, _ := g.chainOf(8, "archived", nodeStr(archiveFinal, "id"))
	nodes := append([]map[string]interface{}{start}, active...)
	nodes = append(nodes, activeFinal)
	nodes = append(nodes, archive...)
	nodes = append(nodes, archiveFinal)

	coords := (&layoutEngine{density: "medium"}).layout(nodes)
	maxActiveY := coords[nodeStr(activeFinal, "id")].Y
	for _, n := range active {
		if y := coords[nodeStr(n, "id")].Y; y > maxActiveY {
			maxActiveY = y
		}
	}
	minArchiveY := coords[nodeStr(archiveFinal, "id")].Y
	for _, n := range archive {
		if y := coords[nodeStr(n, "id")].Y; y < minArchiveY {
			minArchiveY = y
		}
	}
	if minArchiveY <= maxActiveY {
		t.Fatalf("detached archive starts at y=%d, active flow ends at y=%d", minArchiveY, maxActiveY)
	}
	if pairs := overlapPairs(coords, buildLayoutGraph(nodes)); len(pairs) > 0 {
		t.Fatalf("detached layout has overlaps: %s", pairs[0])
	}
}

func TestLayoutI14_EarlySuccessFinalRemainsLocal(t *testing.T) {
	g := &fixGen{}
	deepFinal := g.final("deep done", true)
	main, mainHead := g.chainOf(8, "main", nodeStr(deepFinal, "id"))
	earlyFinal := g.final("already done", true)
	hub := g.cond("already processed?", []fixBranch{{"yes", "yes", nodeStr(earlyFinal, "id")}}, mainHead)
	start := g.start(nodeStr(hub, "id"))
	nodes := append([]map[string]interface{}{start, hub, earlyFinal}, main...)
	nodes = append(nodes, deepFinal)

	coords := (&layoutEngine{density: "medium"}).layout(nodes)
	hubY := coords[nodeStr(hub, "id")].Y
	earlyY := coords[nodeStr(earlyFinal, "id")].Y
	deepY := coords[nodeStr(deepFinal, "id")].Y
	if earlyY >= deepY {
		t.Fatalf("early final was sunk to y=%d instead of staying near hub y=%d (deep final y=%d)", earlyY, hubY, deepY)
	}
	if earlyY-hubY > 2*layRowStep {
		t.Fatalf("early final is %dpx below its router; expected a local exit", earlyY-hubY)
	}
	if pairs := overlapPairs(coords, buildLayoutGraph(nodes)); len(pairs) > 0 {
		t.Fatalf("early-final layout has overlaps: %s", pairs[0])
	}
}

func TestLayoutI15_SecondaryCallbackStaysBesideStartFlow(t *testing.T) {
	g := &fixGen{}
	fin := g.final("done", true)
	worker := g.code("worker", nodeStr(fin, "id"))
	start := g.start(nodeStr(worker, "id"))
	callback := g.code("callback entry", nodeStr(worker, "id"))
	nodeLogics(callback)[0]["type"] = "api_callback"
	nodes := []map[string]interface{}{start, callback, worker, fin}

	coords, rep := (&layoutEngine{density: "medium"}).computeLayout(nodes)
	if rep.Overlaps != 0 {
		t.Fatalf("callback layout overlaps")
	}
	cb := nodeCenter(callback, coords[nodeStr(callback, "id")])
	wk := nodeCenter(worker, coords[nodeStr(worker, "id")])
	if span := int(math.Round(math.Hypot(float64(wk.X-cb.X), float64(wk.Y-cb.Y)))); span > 2*layRowStep {
		t.Fatalf("callback entry is %dpx from its shared worker", span)
	}
	if cb.Y >= wk.Y {
		t.Fatalf("callback entry must sit above the worker: callback=%d worker=%d", cb.Y, wk.Y)
	}
}

func TestLayoutI16_SubstantialErrorBranchIsRecoveryLane(t *testing.T) {
	g := &fixGen{}
	mainFinal := g.final("completed", true)
	mainNext := g.code("main next", nodeStr(mainFinal, "id"))
	partialFinal := g.final("partial failed", false)
	recoveryTail := g.code("write partial state", nodeStr(partialFinal, "id"))
	recoveryMid := g.code("prune partial payload", nodeStr(recoveryTail, "id"))
	recoveryRoot := g.code("prepare partial update", nodeStr(recoveryMid, "id"))
	recoveryRoot["obj_type"] = 3
	owner := g.code("mirror write", nodeStr(mainNext, "id"), nodeStr(recoveryRoot, "id"))
	start := g.start(nodeStr(owner, "id"))
	nodes := []map[string]interface{}{
		start, owner, mainNext, mainFinal,
		recoveryRoot, recoveryMid, recoveryTail, partialFinal,
	}

	graph := buildLayoutGraph(nodes)
	_, terminal, recovery := graph.errPartitions()
	if terminal[nodeStr(recoveryRoot, "id")] || !recovery[nodeStr(recoveryRoot, "id")] {
		t.Fatal("substantial err-entered pipeline was not classified as recovery")
	}
	coords, rep := (&layoutEngine{density: "medium"}).computeLayout(nodes)
	if rep.Overlaps != 0 {
		t.Fatalf("recovery layout overlaps")
	}
	ownerCenter := nodeCenter(owner, coords[nodeStr(owner, "id")])
	rootCenter := nodeCenter(recoveryRoot, coords[nodeStr(recoveryRoot, "id")])
	if span := int(math.Round(math.Hypot(float64(rootCenter.X-ownerCenter.X), float64(rootCenter.Y-ownerCenter.Y)))); span > 3*layRowStep {
		t.Fatalf("recovery entry is %dpx from its owner", span)
	}
	if isCollapsedNode(recoveryRoot) || isCollapsedNode(recoveryMid) {
		t.Fatal("recovery business nodes must remain expanded")
	}
}

func TestLayoutI17_RepeatedRunIsIdempotent(t *testing.T) {
	var nodes []map[string]interface{}
	for _, fx := range allFixtures() {
		if fx.name == "combo" {
			nodes = deepCopyNodes(t, fx.nodes)
			break
		}
	}
	// Reproduce a freshly authored document rather than starting from a
	// previous layout's collapse decisions.
	for _, n := range nodes {
		if !isCircle(n) {
			expandNode(n)
		}
	}
	engine := &layoutEngine{density: "medium"}
	first, _ := engine.computeLayout(nodes)
	second, _ := engine.computeLayout(nodes)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("layout(layout(process)) drifted")
	}
}

func TestLayoutI18_NonBlockingRecoveryBridgePreservesMode(t *testing.T) {
	g := &fixGen{}
	fin := g.final("done", true)
	merge := g.code("continue after non-blocking failure", nodeStr(fin, "id"))
	bridge := g.code("record non-blocking failure", nodeStr(merge, "id"))
	bridge["obj_type"] = 3
	owner := g.code("optional write", nodeStr(merge, "id"), nodeStr(bridge, "id"))
	start := g.start(nodeStr(owner, "id"))
	nodes := []map[string]interface{}{start, owner, bridge, merge, fin}
	for _, n := range nodes {
		if !isCircle(n) {
			expandNode(n)
		}
	}

	(&layoutEngine{density: "medium"}).computeLayout(nodes)
	if isCollapsedNode(bridge) {
		t.Fatal("layout collapsed an expanded non-blocking recovery bridge")
	}
}

func TestLayoutI19_AllStrategiesPreserveEveryExtra(t *testing.T) {
	for _, fixture := range allFixtures() {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			nodes := deepCopyNodes(t, fixture.nodes)
			before := saveNodeExtras(nodes)
			coords, rep := (&layoutEngine{density: "medium"}).computeLayout(nodes)
			after := saveNodeExtras(nodes)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("%s layout changed extra/modeForm", rep.Strategy)
			}
			if pairs := overlapPairs(coords, buildLayoutGraph(nodes)); len(pairs) > 0 {
				t.Fatalf("%s mode-preserving layout overlaps: %s", rep.Strategy, pairs[0])
			}

			// The check above is necessary but not sufficient: computeLayout
			// restores every extra from a snapshot after the strategy runs, so
			// it passes even if the strategies mutate modeForm freely — it
			// exercises restoreNodeExtras, not the layoutPreserveModeKey early
			// return in setNodeMode. Run the chosen strategy with the sentinel
			// and WITHOUT the restore, so only the guard can keep modes intact.
			// (Measured while writing this: without the sentinel the strategies
			// collapse 6 extra nodes on errheavy and 21 on mega.)
			strategyNodes := deepCopyNodes(t, fixture.nodes)
			guardedBefore := saveNodeExtras(strategyNodes)
			for _, n := range strategyNodes {
				n[layoutPreserveModeKey] = true
			}
			fn, label, _ := (&layoutEngine{density: "medium"}).analyzeLayout(strategyNodes)
			fn(strategyNodes)
			for _, n := range strategyNodes {
				delete(n, layoutPreserveModeKey)
			}
			if !reflect.DeepEqual(guardedBefore, saveNodeExtras(strategyNodes)) {
				t.Fatalf("strategy %s mutated extra/modeForm despite the preserve sentinel", label)
			}
		})
	}

	// Missing extra must remain missing; the internal preservation sentinel
	// must never leak into the serialized process document.
	g := &fixGen{}
	fin := g.final("done", true)
	start := g.start(nodeStr(fin, "id"))
	delete(start, "extra")
	nodes := []map[string]interface{}{start, fin}
	(&layoutEngine{density: "medium"}).computeLayout(nodes)
	if _, exists := start["extra"]; exists {
		t.Fatal("layout created extra on a node that did not have it")
	}
	if _, leaked := start[layoutPreserveModeKey]; leaked {
		t.Fatal("layout preservation sentinel leaked into process JSON")
	}
}
