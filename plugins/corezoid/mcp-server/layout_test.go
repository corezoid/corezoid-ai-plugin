package main

import (
	"fmt"
	"os"
	"testing"
)

func sampleApiNodes() []map[string]interface{} {
	return []map[string]interface{}{
		{"id": "a", "obj_type": float64(1), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{map[string]interface{}{"type": "go", "to_node_id": "b"}}, "semaphors": []interface{}{}}},
		{"id": "b", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{
				map[string]interface{}{"type": "api_rpc_reply", "err_node_id": "e"},
				map[string]interface{}{"type": "go", "to_node_id": "end"}}, "semaphors": []interface{}{}}},
		{"id": "end", "obj_type": float64(2), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{"logics": []interface{}{}, "semaphors": []interface{}{}}},
		{"id": "e", "obj_type": float64(2), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{"logics": []interface{}{}, "semaphors": []interface{}{}}},
	}
}

// sampleScheme wraps sampleApiNodes into a scheme map (nodes as []interface{},
// matching how the .conv.json round-trips through encoding/json).
func sampleScheme() map[string]interface{} {
	nodes := sampleApiNodes()
	raw := make([]interface{}, len(nodes))
	for i, n := range nodes {
		raw[i] = n
	}
	return map[string]interface{}{"nodes": raw}
}

func TestLayoutModeEnv(t *testing.T) {
	cases := []struct {
		set   bool
		val   string
		want  string
		label string
	}{
		{true, "off", "off", "off"},
		{true, "full", "full", "full"},
		{false, "", "preserve", "unset"},
		{true, "PRESERVE", "preserve", "PRESERVE (case-insensitive)"},
		{true, "  full  ", "full", "full (trimmed)"},
		{true, "junk", "preserve", "junk -> preserve"},
	}
	for _, c := range cases {
		if c.set {
			t.Setenv("COREZOID_AUTOLAYOUT", c.val)
		} else {
			t.Setenv("COREZOID_AUTOLAYOUT", "")
			os.Unsetenv("COREZOID_AUTOLAYOUT")
		}
		if got := layoutMode(); got != c.want {
			t.Errorf("layoutMode() [%s]=%q want %q", c.label, got, c.want)
		}
	}
}

func TestApplyLayoutOffNoop(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "off")
	scheme := sampleScheme()
	applyLayout(scheme, "process")
	for _, raw := range scheme["nodes"].([]interface{}) {
		n := raw.(map[string]interface{})
		if x, _ := n["x"].(float64); x != 0 {
			t.Errorf("mode off: node %v x=%v want 0 (unchanged)", n["id"], x)
		}
		if y, _ := n["y"].(float64); y != 0 {
			t.Errorf("mode off: node %v y=%v want 0 (unchanged)", n["id"], y)
		}
	}
}

// TestApplyLayoutAllNewDoesFull: a scheme where every node is at 0/0 (the
// sample fixture) gets the full clean layout even in the default preserve mode,
// because there is nothing placed to preserve. Coords match assignPositions.
func TestApplyLayoutAllNewDoesFull(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "") // preserve (default)
	os.Unsetenv("COREZOID_AUTOLAYOUT")
	scheme := sampleScheme()
	want := assignPositions(buildGraph(sampleApiNodes()), "api")

	applyLayout(scheme, "process")
	for _, raw := range scheme["nodes"].([]interface{}) {
		n := raw.(map[string]interface{})
		id, _ := n["id"].(string)
		x, _ := n["x"].(float64)
		y, _ := n["y"].(float64)
		if x == 0 && y == 0 {
			t.Errorf("all-new node %s was not placed", id)
		}
		if int(x) != want[id][0] || int(y) != want[id][1] {
			t.Errorf("node %s at (%v,%v) want full layout (%d,%d)", id, x, y, want[id][0], want[id][1])
		}
	}
}

// TestPreserveLeavesPlacedNodes: placed nodes keep their exact coords and the
// one new node (primary child of placed P) lands at (P.x, P.y+vstep).
func TestPreserveLeavesPlacedNodes(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "") // preserve
	os.Unsetenv("COREZOID_AUTOLAYOUT")

	// P (placed) --go--> N (new, 0/0). End placed too.
	nodes := []map[string]interface{}{
		{"id": "p", "obj_type": float64(0), "x": float64(600), "y": float64(180), "condition": map[string]interface{}{
			"logics": []interface{}{map[string]interface{}{"type": "go", "to_node_id": "n"}}, "semaphors": []interface{}{}}},
		{"id": "n", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{map[string]interface{}{"type": "go", "to_node_id": "z"}}, "semaphors": []interface{}{}}},
		{"id": "z", "obj_type": float64(2), "x": float64(600), "y": float64(900), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
	}
	raw := make([]interface{}, len(nodes))
	for i, n := range nodes {
		raw[i] = n
	}
	scheme := map[string]interface{}{"nodes": raw}
	applyLayout(scheme, "process")

	if nodes[0]["x"].(float64) != 600 || nodes[0]["y"].(float64) != 180 {
		t.Errorf("placed p moved: (%v,%v) want (600,180)", nodes[0]["x"], nodes[0]["y"])
	}
	if nodes[2]["x"].(float64) != 600 || nodes[2]["y"].(float64) != 900 {
		t.Errorf("placed z moved: (%v,%v) want (600,900)", nodes[2]["x"], nodes[2]["y"])
	}
	// Height-aware preserve: n drops below placed p by p's footprint + gap.
	// p is a normal logic node (height 150); gap for a 3-node process is the
	// floor 40. So target = snap(p.y + 150 + 40) = snap(180+190) = snap(370) = 380.
	if nodes[1]["x"].(float64) != 600 || nodes[1]["y"].(float64) != 380 {
		t.Errorf("new n at (%v,%v) want (600,380) = (p.x, snap(p.y+height(p)+gap))", nodes[1]["x"], nodes[1]["y"])
	}
}

// TestPreserveBranchGoesRight: a new branch/error target of a placed source P
// lands at (P.x+pitch, P.y) (same row, right).
func TestPreserveBranchGoesRight(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "") // preserve
	os.Unsetenv("COREZOID_AUTOLAYOUT")

	// P (placed) --error--> E (new). P also has a placed primary child so E is
	// only reachable as a branch (error) target.
	nodes := []map[string]interface{}{
		{"id": "p", "obj_type": float64(0), "x": float64(600), "y": float64(180), "condition": map[string]interface{}{
			"logics": []interface{}{
				map[string]interface{}{"type": "api_rpc", "to_node_id": "ok", "err_node_id": "e"},
			}, "semaphors": []interface{}{}}},
		{"id": "ok", "obj_type": float64(2), "x": float64(600), "y": float64(360), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
		{"id": "e", "obj_type": float64(2), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
	}
	raw := make([]interface{}, len(nodes))
	for i, n := range nodes {
		raw[i] = n
	}
	scheme := map[string]interface{}{"nodes": raw}
	applyLayout(scheme, "process")

	if nodes[2]["x"].(float64) != 840 || nodes[2]["y"].(float64) != 180 {
		t.Errorf("new error e at (%v,%v) want (840,180) = (p.x+pitch, p.y)", nodes[2]["x"], nodes[2]["y"])
	}
}

// TestPreserveNudgesOnCollision: when a new node's target slot is already taken
// by a placed node, the new node is nudged down by vstep; no two nodes overlap.
func TestPreserveNudgesOnCollision(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "") // preserve
	os.Unsetenv("COREZOID_AUTOLAYOUT")

	// P (600,180) --go--> N (new). A placed node X sits at (600,360). With
	// height-aware spacing N's first slot is snap(180+150+40)=380, whose 150px
	// rect (380..530) still overlaps X's rect (360..510), so N is nudged down by
	// one step (height(N)+gap = 150+40 = 190) to (600,570) where it is clear.
	nodes := []map[string]interface{}{
		{"id": "p", "obj_type": float64(0), "x": float64(600), "y": float64(180), "condition": map[string]interface{}{
			"logics": []interface{}{map[string]interface{}{"type": "go", "to_node_id": "n"}}, "semaphors": []interface{}{}}},
		{"id": "n", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
		{"id": "x", "obj_type": float64(0), "x": float64(600), "y": float64(360), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
	}
	raw := make([]interface{}, len(nodes))
	for i, n := range nodes {
		raw[i] = n
	}
	scheme := map[string]interface{}{"nodes": raw}
	applyLayout(scheme, "process")

	if nodes[1]["x"].(float64) != 600 || nodes[1]["y"].(float64) != 570 {
		t.Errorf("new n at (%v,%v) want (600,570) after collision nudge", nodes[1]["x"], nodes[1]["y"])
	}
	// No two nodes share coordinates.
	seen := map[[2]float64]string{}
	for _, n := range nodes {
		k := [2]float64{n["x"].(float64), n["y"].(float64)}
		if prev, ok := seen[k]; ok {
			t.Errorf("nodes %s and %s share coords %v", prev, n["id"], k)
		}
		seen[k] = n["id"].(string)
	}
}

// TestPreserveNoRectOverlapOffGrid reproduces the adversarial-review defect:
// placed nodes sit at IRREGULAR/off-the-engine-grid coordinates so an exact-
// coordinate collision check misses the overlap, but the real rectangular
// footprints DO intersect.
//
//   - Vertical: P(600,180) --go--> N (new). A placed node X sits at (600,420)
//     (off the engine grid, and clear of P's own rect). N targets (600,360);
//     its 200x150 rect (y 360..510) overlaps X's rect (y 420..570) because
//     420 < 360+150. An exact-pivot check (360 != 420) would MISS this.
//   - Horizontal: P also --err--> E (new). E targets (840,180). A placed node Y
//     sits at (900,180); E's rect (x 840..1040) overlaps Y's rect (x 900..1100).
//     Exact-pivot (840 != 900) would miss it too.
//
// The placed nodes themselves do NOT overlap each other in the fixture, so a
// post-layout countOverlaps==0 isolates the new-vs-placed defect.
//
// After the preserve layout there must be ZERO overlapping rects, and the
// placed nodes must not have moved.
func TestPreserveNoRectOverlapOffGrid(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "") // preserve
	os.Unsetenv("COREZOID_AUTOLAYOUT")

	nodes := []map[string]interface{}{
		{"id": "p", "obj_type": float64(0), "x": float64(600), "y": float64(180), "condition": map[string]interface{}{
			"logics": []interface{}{
				map[string]interface{}{"type": "api_rpc", "to_node_id": "n", "err_node_id": "e"},
			}, "semaphors": []interface{}{}}},
		{"id": "n", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
		{"id": "e", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
		// placed obstacle below the primary parent (off the 180-grid, clear of p).
		{"id": "x", "obj_type": float64(0), "x": float64(600), "y": float64(420), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
		// placed obstacle to the right of the branch source (off the 240-pitch).
		{"id": "y", "obj_type": float64(0), "x": float64(900), "y": float64(180), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
	}
	raw := make([]interface{}, len(nodes))
	for i, n := range nodes {
		raw[i] = n
	}
	scheme := map[string]interface{}{"nodes": raw}

	placed := map[string][2]float64{
		"p": {600, 180},
		"x": {600, 420},
		"y": {900, 180},
	}

	applyLayoutMode(scheme, "process", "preserve")

	// Placed nodes must not have moved.
	for id, want := range placed {
		var got *map[string]interface{}
		for i := range nodes {
			if nid, _ := nodes[i]["id"].(string); nid == id {
				got = &nodes[i]
				break
			}
		}
		if got == nil {
			t.Fatalf("placed node %s vanished", id)
		}
		if gx, _ := (*got)["x"].(float64); gx != want[0] {
			t.Errorf("placed %s moved: x=%v want %v", id, gx, want[0])
		}
		if gy, _ := (*got)["y"].(float64); gy != want[1] {
			t.Errorf("placed %s moved: y=%v want %v", id, gy, want[1])
		}
	}

	// No two node rectangles may intersect.
	if c := countOverlaps(nodes); c != 0 {
		for _, n := range nodes {
			t.Logf("node %v at (%v,%v) rect=%v", n["id"], n["x"], n["y"], rectOf(n))
		}
		t.Errorf("preserve left %d overlapping rect pair(s); want 0", c)
	}
}

func TestApplyLayoutMalformed(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "")
	os.Unsetenv("COREZOID_AUTOLAYOUT")
	// Empty nodes slice must not panic.
	applyLayout(map[string]interface{}{"nodes": []interface{}{}}, "process")
	// Missing nodes key entirely.
	applyLayout(map[string]interface{}{}, "process")
	// A node missing its condition block must not panic.
	applyLayout(map[string]interface{}{
		"nodes": []interface{}{
			map[string]interface{}{"id": "a", "obj_type": float64(1)},
		},
	}, "process")
}

// TestEstimatedHeight checks the per-role footprint heights that drive the
// height-aware vertical spacing and rectOf.
func TestEstimatedHeight(t *testing.T) {
	circle := map[string]interface{}{"obj_type": float64(1)} // START
	end := map[string]interface{}{"obj_type": float64(2)}    // END
	cond := map[string]interface{}{"obj_type": float64(3)}   // COND
	logic := map[string]interface{}{"obj_type": float64(0), "condition": map[string]interface{}{
		"logics": []interface{}{}, "semaphors": []interface{}{}}}
	timer := map[string]interface{}{"obj_type": float64(0), "condition": map[string]interface{}{
		"logics":    []interface{}{},
		"semaphors": []interface{}{map[string]interface{}{"type": "time", "to_node_id": "x"}}}}

	cases := []struct {
		name string
		node map[string]interface{}
		want int
	}{
		{"start circle", circle, 56},
		{"end circle", end, 56},
		{"condition", cond, 120},
		{"timer logic", timer, 300},
		{"plain logic", logic, 150},
	}
	for _, c := range cases {
		if got := estimatedHeight(c.node); got != c.want {
			t.Errorf("estimatedHeight(%s)=%d want %d", c.name, got, c.want)
		}
	}
}

// TestHeightAwareRowsNoOverlapWithTimer builds START -> T (logic with a time
// semaphor, height 300) -> B (plain logic) so T and B share a column on
// successive ranks. The row holding B must start at least height(T)+gap below T,
// and there must be zero rect overlaps once positioned.
func TestHeightAwareRowsNoOverlapWithTimer(t *testing.T) {
	nodes := []map[string]interface{}{
		{"id": "s", "obj_type": float64(1), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{map[string]interface{}{"type": "go", "to_node_id": "ti"}}, "semaphors": []interface{}{}}},
		{"id": "ti", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics":    []interface{}{map[string]interface{}{"type": "go", "to_node_id": "b"}},
			"semaphors": []interface{}{map[string]interface{}{"type": "time", "to_node_id": "b"}}}},
		{"id": "b", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
	}
	g := buildGraph(nodes)
	pos := assignPositions(g, "default")

	// ti (timer) and b sit in the same column (the spine).
	if pos["ti"][0] != pos["b"][0] {
		t.Fatalf("ti and b must share a column: ti.x=%d b.x=%d", pos["ti"][0], pos["b"][0])
	}
	// b's row must start at least height(ti)=300 below ti's top (room for the
	// tall timer node, plus the inter-row gap). gap is the 3-node floor 40.
	gap := vGap(profileFor("default"), len(nodes))
	if got := pos["b"][1] - pos["ti"][1]; got < 300 {
		t.Errorf("b row starts %d below ti; want >= 300 (timer footprint)", got)
	} else if got < 300+gap-20 { // allow one grid-snap of slack on the gap
		t.Errorf("b row starts %d below ti; want ~300+gap=%d", got, 300+gap)
	}

	// Write positions back and assert zero rect overlaps with the height-aware
	// metric (ti's rect is now 300 tall).
	for _, n := range nodes {
		id, _ := n["id"].(string)
		n["x"] = float64(pos[id][0])
		n["y"] = float64(pos[id][1])
	}
	if c := countOverlaps(nodes); c != 0 {
		for _, n := range nodes {
			t.Logf("node %v at (%v,%v) rect=%v", n["id"], n["x"], n["y"], rectOf(n))
		}
		t.Errorf("height-aware layout left %d overlapping rect pair(s); want 0", c)
	}
}

func TestDetectArchetype(t *testing.T) {
	cases := []struct {
		conv   string
		logics []string
		want   string
	}{
		{"state", nil, "state"},
		{"process", []string{"api_callback"}, "receiver"},
		{"process", []string{"api_rpc_reply"}, "api"},
		{"process", []string{"api_rpc"}, "business"},
		{"process", []string{"api"}, "integration"},
		{"process", []string{"set_param"}, "default"},
	}
	for _, c := range cases {
		if got := detectArchetype(c.conv, c.logics); got != c.want {
			t.Errorf("detectArchetype(%q,%v)=%q want %q", c.conv, c.logics, got, c.want)
		}
	}
}

func TestBuildGraphEdgeKinds(t *testing.T) {
	g := buildGraph(sampleApiNodes())
	if g.kind("a", "b") != "primary" {
		t.Errorf("a->b kind=%q want primary", g.kind("a", "b"))
	}
	if g.kind("b", "end") != "primary" {
		t.Errorf("b->end kind=%q want primary", g.kind("b", "end"))
	}
	if g.kind("b", "e") != "error" {
		t.Errorf("b->e kind=%q want error", g.kind("b", "e"))
	}
	if g.role("a") != "START" || g.role("end") != "END" {
		t.Error("roles wrong")
	}
}

func TestAssignPositionsStraightAndGrid(t *testing.T) {
	g := buildGraph(sampleApiNodes())
	pos := assignPositions(g, "api")
	if pos["b"][0] != 600 {
		t.Errorf("logic b must sit on spine x=600, got %d", pos["b"][0])
	}
	if pos["a"][0] != pos["b"][0]+100 {
		t.Error("start centered +100 expected")
	}
	if pos["end"][0] != pos["b"][0]+100 {
		t.Error("success-end centered +100 expected")
	}
	if !(pos["b"][1] < pos["end"][1]) {
		t.Error("end must be below logic b")
	}
	if !(pos["e"][0] > pos["b"][0]) {
		t.Error("error e must be to the right of b")
	}
	if pos["e"][1] != pos["b"][1] {
		t.Error("error e must be on the SAME row as b (horizontal connector)")
	}
	for id, p := range pos {
		if p[0]%20 != 0 || p[1]%20 != 0 {
			t.Errorf("%s off-grid: %v", id, p)
		}
	}
}

func TestAssignPositionsIdempotent(t *testing.T) {
	g := buildGraph(sampleApiNodes())
	p1 := assignPositions(g, "api")
	p2 := assignPositions(g, "api")
	for id := range p1 {
		if p1[id] != p2[id] {
			t.Errorf("not idempotent at %s: %v vs %v", id, p1[id], p2[id])
		}
	}
}

// TestAssignPositionsEmptyAndSingle exercises the degenerate inputs: an empty
// scheme must not panic and yields no positions; a single Start node lands at
// its computed spine position. (Start at col 0 gets the +startOff centering, so
// x = SPINE_X + startOff = 700, y = 0.)
func TestAssignPositionsEmptyAndSingle(t *testing.T) {
	g := buildGraph(nil)
	pos := assignPositions(g, "default")
	if len(pos) != 0 {
		t.Errorf("empty input: want 0 positions, got %v", pos)
	}

	single := []map[string]interface{}{
		{"id": "s", "obj_type": float64(1), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
	}
	g2 := buildGraph(single)
	pos2 := assignPositions(g2, "default")
	if len(pos2) != 1 {
		t.Fatalf("single node: want 1 position, got %v", pos2)
	}
	if pos2["s"] != [2]int{700, 0} {
		t.Errorf("single Start node: want (700,0), got %v", pos2["s"])
	}
}

// TestAssignPositionsCyclic builds a→b→c with c's primary edge going back to b
// (a cycle). The DFS cycle-breaking in ranks must drop the back edge, leaving a
// finite DAG, so assignPositions terminates with on-grid coordinates for all
// nodes. With height-aware cumulative rows: a is a START circle (height 56) at
// rank 0; b,c are logic nodes (height 150) at ranks 1,2; gap for a 3-node
// process is the floor 40. rowTop[0]=0, rowTop[1]=0+56+40=96 -> snap 100,
// rowTop[2]=96+150+40=286 -> snap 280. So a=(700,0) b=(600,100) c=(600,280).
func TestAssignPositionsCyclic(t *testing.T) {
	nodes := []map[string]interface{}{
		{"id": "a", "obj_type": float64(1), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{map[string]interface{}{"type": "go", "to_node_id": "b"}}, "semaphors": []interface{}{}}},
		{"id": "b", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{map[string]interface{}{"type": "go", "to_node_id": "c"}}, "semaphors": []interface{}{}}},
		{"id": "c", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{map[string]interface{}{"type": "go", "to_node_id": "b"}}, "semaphors": []interface{}{}}},
	}
	g := buildGraph(nodes)
	pos := assignPositions(g, "default")
	if len(pos) != 3 {
		t.Fatalf("cyclic: want 3 positions, got %v", pos)
	}
	for id, p := range pos {
		if p[0]%20 != 0 || p[1]%20 != 0 {
			t.Errorf("%s off-grid: %v", id, p)
		}
	}
	if pos["a"] != [2]int{700, 0} {
		t.Errorf("a: want (700,0), got %v", pos["a"])
	}
	if pos["b"] != [2]int{600, 100} {
		t.Errorf("b: want (600,100), got %v", pos["b"])
	}
	if pos["c"] != [2]int{600, 280} {
		t.Errorf("c: want (600,280), got %v", pos["c"])
	}
}

// TestDetectArchetypePrecedence confirms the precedence order in
// detectArchetype: api_callback wins over api_rpc (-> receiver), and
// api_rpc_reply wins over api (-> api).
func TestDetectArchetypePrecedence(t *testing.T) {
	if got := detectArchetype("process", []string{"api_rpc", "api_callback"}); got != "receiver" {
		t.Errorf("api_callback must win over api_rpc: got %q want receiver", got)
	}
	if got := detectArchetype("process", []string{"api", "api_rpc_reply"}); got != "api" {
		t.Errorf("api_rpc_reply must win over api: got %q want api", got)
	}
}

// TestBuildGraphTimeoutEdge confirms a condition.semaphors entry with a
// to_node_id produces a "timeout" edge.
func TestBuildGraphTimeoutEdge(t *testing.T) {
	nodes := []map[string]interface{}{
		{"id": "a", "obj_type": float64(0), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics":    []interface{}{},
			"semaphors": []interface{}{map[string]interface{}{"type": "timeout", "to_node_id": "b"}}}},
		{"id": "b", "obj_type": float64(2), "x": float64(0), "y": float64(0), "condition": map[string]interface{}{
			"logics": []interface{}{}, "semaphors": []interface{}{}}},
	}
	g := buildGraph(nodes)
	if g.kind("a", "b") != "timeout" {
		t.Errorf("a->b kind=%q want timeout", g.kind("a", "b"))
	}
}

// TestAssignPositionsAdaptiveVStep confirms the n>8 branch raises vertical
// spacing above the small-process minimum, capped at the adaptive ceiling.
//
// Height-aware update: the measured n00->n01 distance is now the START circle's
// footprint (56) + the inter-row gap (vGap = adaptiveVStep-150, floored at 40,
// snapped to 20), NOT the old uniform vStep. gap is 40 for n<=8, 60 at n=21
// (snap(200-150)=snap(50)=60), 100 at n=100 (snap(240-150)=snap(90)=100). So the
// circle->logic step is snap(56+gap): 4-node -> snap(96)=100, 21-node ->
// snap(116)=120, 100-node -> snap(156)=160. These still rise with N and the gap
// caps with the adaptive ceiling, which is exactly what this test guards.
//
// The chain bottom node (rank N-1) sits at the cumulative rowTop: rank 0 is the
// circle row (56), ranks 1..N-1 are logic rows (150) each + gap. Derived from the
// algorithm: N=21 -> bottom y=4100; N=100 -> bottom y=24660.
func TestAssignPositionsAdaptiveVStep(t *testing.T) {
	chain := func(N int) *graph {
		nodes := make([]map[string]interface{}, 0, N)
		for i := 0; i < N; i++ {
			id := fmt.Sprintf("n%02d", i)
			objType := float64(0)
			cond := map[string]interface{}{"semaphors": []interface{}{}}
			if i == 0 {
				objType = 1
			}
			if i < N-1 {
				cond["logics"] = []interface{}{map[string]interface{}{"type": "go", "to_node_id": fmt.Sprintf("n%02d", i+1)}}
			} else {
				objType = 2
				cond["logics"] = []interface{}{}
			}
			nodes = append(nodes, map[string]interface{}{
				"id": id, "obj_type": objType, "x": float64(0), "y": float64(0), "condition": cond})
		}
		return buildGraph(nodes)
	}

	vStepOf := func(g *graph) int {
		pos := assignPositions(g, "default")
		return pos["n01"][1] - pos["n00"][1]
	}

	small := vStepOf(chain(4))
	if small != 100 {
		t.Errorf("4-node chain circle->logic step=%d want 100 (snap(56+40))", small)
	}

	g21 := chain(21)
	if v := vStepOf(g21); v != 120 {
		t.Errorf("21-node chain step=%d want 120 (snap(56+60), raised)", v)
	} else if v <= small {
		t.Errorf("21-node step %d must exceed 4-node step %d", v, small)
	}
	pos21 := assignPositions(g21, "default")
	if pos21["n20"][1] != 4100 {
		t.Errorf("21-node bottom y=%d want 4100", pos21["n20"][1])
	}

	// Cap: very large N's gap must not exceed the adaptive ceiling, so the
	// circle->logic step caps at snap(56+100)=160.
	if v := vStepOf(chain(100)); v != 160 {
		t.Errorf("100-node chain step=%d want 160 (gap capped)", v)
	}
}
