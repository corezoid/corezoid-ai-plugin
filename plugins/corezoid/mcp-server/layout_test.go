package main

import (
	"testing"
)

// --- fixture helpers ---------------------------------------------------------

func goLogic(to string) map[string]interface{} {
	return map[string]interface{}{"type": "go", "to_node_id": to}
}

func condLogic(to string) map[string]interface{} {
	return map[string]interface{}{"type": "go_if_const", "to_node_id": to}
}

// node builds a node map. objType: 1=START, 2=END, 3=COND, 0=LOGIC.
func node(id string, objType float64, x, y float64, logics ...map[string]interface{}) map[string]interface{} {
	n := map[string]interface{}{
		"id":       id,
		"obj_type": objType,
		"x":        x,
		"y":        y,
	}
	if len(logics) > 0 {
		ls := make([]interface{}, len(logics))
		for i, l := range logics {
			ls[i] = l
		}
		n["condition"] = map[string]interface{}{"logics": ls}
	}
	return n
}

func scheme(nodes ...map[string]interface{}) map[string]interface{} {
	raw := make([]interface{}, len(nodes))
	for i, n := range nodes {
		raw[i] = n
	}
	return map[string]interface{}{"nodes": raw}
}

func xy(n map[string]interface{}) (float64, float64) {
	x, _ := n["x"].(float64)
	y, _ := n["y"].(float64)
	return x, y
}

func onGrid(v float64) bool { return int(v)%gridSnap == 0 }

// allRects returns the footprint of every node for overlap assertions.
func allRects(nodes ...map[string]interface{}) [][4]float64 {
	rs := make([][4]float64, 0, len(nodes))
	for _, n := range nodes {
		rs = append(rs, rectOf(n))
	}
	return rs
}

func anyIntersect(rs [][4]float64) bool {
	for i := 0; i < len(rs); i++ {
		for j := i + 1; j < len(rs); j++ {
			if rectsIntersect(rs[i], rs[j]) {
				return true
			}
		}
	}
	return false
}

// --- tests -------------------------------------------------------------------

func TestLayoutModeEnv(t *testing.T) {
	cases := []struct {
		env, want string
		set       bool
	}{
		{"off", "off", true},
		{"OFF", "off", true},
		{"  off  ", "off", true},
		{"full", "preserve", true}, // no full mode
		{"junk", "preserve", true},
		{"", "preserve", true},
		{"", "preserve", false}, // unset
	}
	for _, c := range cases {
		if c.set {
			t.Setenv("COREZOID_AUTOLAYOUT", c.env)
		} else {
			// t.Setenv restores after the test; explicitly clear for the unset case.
			t.Setenv("COREZOID_AUTOLAYOUT", "")
		}
		if got := layoutMode(); got != c.want {
			t.Errorf("layoutMode() with env %q = %q, want %q", c.env, got, c.want)
		}
	}
}

func TestApplyLayoutOffNoop(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "off")
	n := node("a", 0, 0, 0, goLogic("b"))
	b := node("b", 2, 0, 0)
	s := scheme(n, b)
	applyLayout(s, "process")
	for _, nd := range []map[string]interface{}{n, b} {
		x, y := xy(nd)
		if x != 0 || y != 0 {
			t.Errorf("off mode moved node %v to (%v,%v)", nd["id"], x, y)
		}
	}
}

func TestApplyLayoutAllNewLaysOut(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "")
	mk := func() (map[string]interface{}, []map[string]interface{}) {
		start := node("start", 1, 0, 0, goLogic("l1"))
		l1 := node("l1", 0, 0, 0, goLogic("l2"))
		l2 := node("l2", 0, 0, 0, goLogic("end"))
		end := node("end", 2, 0, 0)
		return scheme(start, l1, l2, end), []map[string]interface{}{start, l1, l2, end}
	}

	s, nodes := mk()
	applyLayout(s, "process")

	for _, nd := range nodes {
		x, y := xy(nd)
		if x == 0 && y == 0 {
			t.Errorf("node %v still at origin", nd["id"])
		}
		if x == 0 {
			t.Errorf("node %v has x==0 (expected >= spineX)", nd["id"])
		}
		if !onGrid(x) || !onGrid(y) {
			t.Errorf("node %v off grid: (%v,%v)", nd["id"], x, y)
		}
	}
	// Spine logic nodes sit in column 0 at x == spineX (600).
	for _, id := range []string{"l1", "l2"} {
		var found map[string]interface{}
		for _, nd := range nodes {
			if nd["id"] == id {
				found = nd
			}
		}
		if x, _ := xy(found); x != spineX {
			t.Errorf("spine node %s x = %v, want %d", id, x, spineX)
		}
	}

	// Determinism: a second independent run yields identical coordinates.
	s2, nodes2 := mk()
	applyLayout(s2, "process")
	for i := range nodes {
		x1, y1 := xy(nodes[i])
		x2, y2 := xy(nodes2[i])
		if x1 != x2 || y1 != y2 {
			t.Errorf("non-deterministic: node %v (%v,%v) vs (%v,%v)", nodes[i]["id"], x1, y1, x2, y2)
		}
	}
}

func TestPreserveLeavesPlacedNodes(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "")
	start := node("start", 1, 700, 0, goLogic("l1"))
	l1 := node("l1", 0, 600, 200, goLogic("n"))
	n := node("n", 0, 0, 0) // new primary down-child of l1
	s := scheme(start, l1, n)
	applyLayout(s, "process")

	// Placed nodes untouched (byte-identical coords).
	if x, y := xy(start); x != 700 || y != 0 {
		t.Errorf("placed start moved to (%v,%v)", x, y)
	}
	if x, y := xy(l1); x != 600 || y != 200 {
		t.Errorf("placed l1 moved to (%v,%v)", x, y)
	}
	// New node lands directly below its parent.
	nx, ny := xy(n)
	if nx != 600 || ny != 400 {
		t.Errorf("new node n = (%v,%v), want (600,400)", nx, ny)
	}
}

func TestPreserveBranchRight(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "")
	// s has a primary 'go' to p (placed) and a branch 'go_if_const' to n (new),
	// so n is a branch target, not the down-child.
	s0 := node("s", 0, 600, 200, goLogic("p"), condLogic("n"))
	p := node("p", 0, 600, 400)
	n := node("n", 0, 0, 0)
	sc := scheme(s0, p, n)
	applyLayout(sc, "process")

	sx, sy := xy(s0)
	nx, ny := xy(n)
	if ny != sy {
		t.Errorf("branch node n y = %v, want same row as source %v", ny, sy)
	}
	if nx <= sx {
		t.Errorf("branch node n x = %v, want right of source %v", nx, sx)
	}
}

func TestPreserveNudgesRectOverlap(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "")
	start := node("start", 1, 700, 0, goLogic("l1"))
	l1 := node("l1", 0, 600, 200, goLogic("n"))
	// x is an off-grid placed node sitting where n's first target (600,400) would land.
	x := node("x", 0, 610, 410)
	n := node("n", 0, 0, 0) // new primary down-child of l1
	sc := scheme(start, l1, x, n)
	applyLayout(sc, "process")

	// The off-grid placed node must not move.
	if xx, xy2 := xy(x); xx != 610 || xy2 != 410 {
		t.Errorf("placed off-grid node x moved to (%v,%v)", xx, xy2)
	}
	// No rectangle overlaps after placement.
	if anyIntersect(allRects(start, l1, x, n)) {
		t.Errorf("rect overlap after preserve placement")
	}
	// n was nudged below the (600,400) slot it would otherwise take.
	if _, ny := xy(n); ny <= 400 {
		t.Errorf("new node not nudged past overlap: y=%v", ny)
	}
}

func TestPreservePushRoundTripUnchanged(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "")
	// A small, fully-placed process (every node has non-(0,0) coords, with
	// irregular values). Preserve mode must not move any of them.
	start := node("start", 1, 600, 0, goLogic("l1"))
	l1 := node("l1", 0, 640, 210, goLogic("end"))
	end := node("end", 2, 700, 400)
	nodes := []map[string]interface{}{start, l1, end}

	// Snapshot every node's coordinates before layout.
	type coord struct{ x, y float64 }
	before := map[string]coord{}
	for _, nd := range nodes {
		x, y := xy(nd)
		before[nd["id"].(string)] = coord{x, y}
	}

	s := scheme(start, l1, end)
	applyLayout(s, "process")

	// Every node must be byte-identical to its snapshot: nothing moved.
	for _, nd := range nodes {
		id := nd["id"].(string)
		x, y := xy(nd)
		if x != before[id].x || y != before[id].y {
			t.Errorf("placed node %s moved from (%v,%v) to (%v,%v)",
				id, before[id].x, before[id].y, x, y)
		}
	}
}

func TestApplyLayoutMalformed(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "")
	// Must not panic for any of these.

	// Empty nodes.
	applyLayout(map[string]interface{}{"nodes": []interface{}{}}, "process")

	// Missing nodes key entirely.
	applyLayout(map[string]interface{}{}, "process")

	// nil scheme.
	applyLayout(nil, "process")

	// Node missing condition, x as string, plus web_settings as an array.
	bad := map[string]interface{}{"id": "bad", "obj_type": float64(0), "x": "not-a-number"}
	missingCond := map[string]interface{}{"id": "mc", "obj_type": float64(0)}
	s := scheme(bad, missingCond)
	s["web_settings"] = []interface{}{1, 2, 3}
	applyLayout(s, "process")

	// nodes value of the wrong type.
	applyLayout(map[string]interface{}{"nodes": "oops"}, "process")
}

func TestSpineStraightCol0(t *testing.T) {
	t.Setenv("COREZOID_AUTOLAYOUT", "")
	// A spine A->B->C with branches hanging off each, all new -> baseLayout.
	start := node("start", 1, 0, 0, goLogic("a"))
	a := node("a", 0, 0, 0, goLogic("b"), condLogic("ba"))
	ba := node("ba", 0, 0, 0)
	b := node("b", 0, 0, 0, goLogic("c"), condLogic("bb"))
	bb := node("bb", 0, 0, 0)
	c := node("c", 0, 0, 0, goLogic("end"))
	end := node("end", 2, 0, 0)
	s := scheme(start, a, ba, b, bb, c, end)
	applyLayout(s, "process")

	for _, id := range []string{"a", "b", "c"} {
		var found map[string]interface{}
		for _, nd := range []map[string]interface{}{a, b, c} {
			if nd["id"] == id {
				found = nd
			}
		}
		if x, _ := xy(found); x != spineX {
			t.Errorf("spine node %s x = %v, want %d (col 0)", id, x, spineX)
		}
	}
}
