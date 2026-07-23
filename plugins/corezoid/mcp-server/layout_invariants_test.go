package main

// The engine's behavioural invariants (I1–I10), ported from the skill's
// former test_layout.py and run over every synthetic fixture.

import (
	"fmt"
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
			// I4: coordinates within the platform canvas
			for id, p := range coords {
				if p.X < -10000 || p.X > 10000 || p.Y < -10000 || p.Y > 10000 {
					t.Errorf("I4: %s at (%d,%d) is outside ±10000", id, p.X, p.Y)
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
			var clusters []box
			for _, b := range items {
				if len(clusters) > 0 && b.top-clusters[len(clusters)-1].top <= 55 {
					if b.bot > clusters[len(clusters)-1].bot {
						clusters[len(clusters)-1].bot = b.bot
					}
				} else {
					clusters = append(clusters, b)
				}
			}
			for i := 1; i < len(clusters); i++ {
				gap := clusters[i].top - clusters[i-1].bot
				if gap > gapV+4 {
					t.Errorf("I8: %dpx of air between rows (limit %d)", gap, gapV)
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

// TestLayoutI6_Routing: error-heavy processes go through the partitioned
// (layered+error-rail) machinery.
func TestLayoutI6_Routing(t *testing.T) {
	for _, fx := range allFixtures() {
		if fx.name != "errheavy" {
			continue
		}
		nodes := deepCopyNodes(t, fx.nodes)
		_, label, reason := (&layoutEngine{density: "medium"}).analyzeLayout(nodes)
		if label != "layered+error-rail" {
			t.Errorf("I6: errheavy routed to %q (%s)", label, reason)
		}
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
	hubCx := coords[star.hub].X
	if !isCircle(g.byID[star.hub]) {
		hubCx += 100
	}
	mCx := coords[star.merge].X
	if !isCircle(g.byID[star.merge]) {
		mCx += 100
	}
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

// TestLayoutI11_CollapseMarksReachTheDocument: the hybrid strategy lays the
// residual graph out on a working copy; its collapse decisions must reach the
// REAL document, or nodes get the collapsed x-offset while rendering as full
// 200px blocks (a bug the Python original shipped with).
func TestLayoutI11_CollapseMarksReachTheDocument(t *testing.T) {
	var combo []map[string]interface{}
	for _, fx := range allFixtures() {
		if fx.name == "combo" {
			combo = deepCopyNodes(t, fx.nodes)
		}
	}
	e := &layoutEngine{density: "medium"}
	_, rep := e.computeLayout(combo)
	if rep.Strategy != "waterfall+regions" {
		t.Fatalf("combo routed to %q", rep.Strategy)
	}
	g := buildLayoutGraph(combo)
	// both dispatch hubs are pure IF routers in the RESIDUAL graph — after
	// the layout they must carry modeForm=collapse in the real document
	for _, n := range combo {
		title := nodeStr(n, "title")
		if title == "star route?" || title == "table route?" {
			if !isCollapsedNode(n) {
				t.Errorf("I11: %q placed with the collapsed offset but not collapsed in the document", title)
			}
		}
	}
	_ = g
}
