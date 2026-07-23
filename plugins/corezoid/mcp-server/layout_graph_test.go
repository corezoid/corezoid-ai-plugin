package main

import "testing"

func TestBuildLayoutGraph_EdgesAndOrder(t *testing.T) {
	nodes := topoStar()
	g := buildLayoutGraph(nodes)

	if len(g.ids) != len(nodes) {
		t.Fatalf("ids: got %d, want %d", len(g.ids), len(nodes))
	}
	for i, n := range nodes {
		if g.ids[i] != nodeStr(n, "id") {
			t.Fatalf("ids not in document order at %d", i)
		}
	}
	// the Start node has a single primary go edge to the hub
	start := nodes[0]
	if nodeObjType(start) != 1 {
		t.Fatalf("fixture: first node is not Start")
	}
	hubID := nodeStr(nodes[1], "id")
	if g.primary[nodeStr(start, "id")] != hubID {
		t.Fatalf("start primary: got %q, want hub %q", g.primary[nodeStr(start, "id")], hubID)
	}
	// the hub (dispatch cond) has 6 go_if_const branches + 1 default go
	hub := nodes[1]
	if got := len(g.branches[hubID]); got != 6 {
		t.Fatalf("hub branches: got %d, want 6", got)
	}
	if g.primary[hubID] == "" {
		t.Fatalf("hub must keep its default go as primary")
	}
	// every code node carries the err edge to the error final
	if len(g.errors[g.primary[hubID]]) != 1 {
		t.Fatalf("ray head must have one err edge")
	}
	_ = hub
}

func TestNodeBoxSizeAndPivot(t *testing.T) {
	g := &fixGen{}
	fin := g.final("Final", true) // circle, extra has modeForm collapse but circles win
	if w, h := nodeBoxSize(fin); w != 56 || h != 56 {
		t.Fatalf("final box: %dx%d, want 56x56", w, h)
	}
	x0, y0, x1, y1 := nodeBox(fin, 500, 100)
	if x0 != 472 || y0 != 72 || x1 != 528 || y1 != 128 {
		t.Fatalf("circle pivot must be centre: got (%d,%d,%d,%d)", x0, y0, x1, y1)
	}
	blk := g.code("step", "x")
	if w, h := nodeBoxSize(blk); w != 200 || h != 150 {
		t.Fatalf("block box: %dx%d, want 200x150", w, h)
	}
	collapseNode(blk)
	if w, h := nodeBoxSize(blk); w != 48 || h != 48 {
		t.Fatalf("collapsed box: %dx%d, want 48x48", w, h)
	}
	if s, ok := blk["extra"].(string); !ok || s == "" {
		t.Fatalf("collapseNode must keep extra as a JSON string, got %T", blk["extra"])
	}
	dl := g.delay("wait", "x")
	if _, h := nodeBoxSize(dl); h != 270 {
		t.Fatalf("timer block must be 270 tall")
	}
}

func TestFloorDivAndPyMod(t *testing.T) {
	cases := []struct{ a, b, div, mod int }{
		{7, 2, 3, 1},
		{-7, 2, -4, 1},
		{7, -2, -4, -1},
		{-7, -2, 3, -1},
		{-110, 110, -1, 0},
		{-115, 110, -2, 105},
	}
	for _, c := range cases {
		if got := floorDiv(c.a, c.b); got != c.div {
			t.Errorf("floorDiv(%d,%d)=%d, want %d", c.a, c.b, got, c.div)
		}
		if got := pyMod(c.a, c.b); got != c.mod {
			t.Errorf("pyMod(%d,%d)=%d, want %d", c.a, c.b, got, c.mod)
		}
	}
}

func TestExtraParsing(t *testing.T) {
	cases := []struct {
		extra     interface{}
		collapsed bool
	}{
		{`{"modeForm":"collapse","icon":""}`, true},
		{`{"modeForm":"expand"}`, false},
		{nil, false},
		{"not json", false},
		{map[string]interface{}{"modeForm": "collapse"}, true},
	}
	for i, c := range cases {
		n := map[string]interface{}{"id": "x", "extra": c.extra}
		if got := isCollapsedNode(n); got != c.collapsed {
			t.Errorf("case %d: isCollapsedNode=%v, want %v", i, got, c.collapsed)
		}
	}
}

// A count semaphor escalates via esc_node_id — the graph must carry it as an
// error edge so the escalation cluster is placed next to its owner instead of
// drifting to the orphan grid.
func TestBuildLayoutGraph_CountSemaphorEscIsErrEdge(t *testing.T) {
	mk := func(id, title string, objType int, logics []interface{}, sems []interface{}) map[string]interface{} {
		return map[string]interface{}{
			"id": id, "title": title, "obj_type": float64(objType),
			"condition": map[string]interface{}{"logics": logics, "semaphors": sems},
			"x": float64(0), "y": float64(0),
		}
	}
	goTo := func(to string) interface{} { return map[string]interface{}{"type": "go", "to_node_id": to} }
	nodes := []map[string]interface{}{
		mk("aaaaaaaaaaaaaaaaaaaaaa01", "Start", 1, []interface{}{goTo("aaaaaaaaaaaaaaaaaaaaaa02")}, nil),
		mk("aaaaaaaaaaaaaaaaaaaaaa02", "api call", 0,
			[]interface{}{
				map[string]interface{}{"type": "api", "err_node_id": "aaaaaaaaaaaaaaaaaaaaaa04"},
				goTo("aaaaaaaaaaaaaaaaaaaaaa05"),
			},
			[]interface{}{map[string]interface{}{"type": "count", "value": float64(500), "esc_node_id": "aaaaaaaaaaaaaaaaaaaaaa03"}}),
		mk("aaaaaaaaaaaaaaaaaaaaaa03", "Reply: throttled", 3, []interface{}{goTo("aaaaaaaaaaaaaaaaaaaaaa06")}, nil),
		mk("aaaaaaaaaaaaaaaaaaaaaa04", "Call Error", 2, nil, nil),
		mk("aaaaaaaaaaaaaaaaaaaaaa05", "Final", 2, nil, nil),
		mk("aaaaaaaaaaaaaaaaaaaaaa06", "Throttle Error", 2, nil, nil),
	}
	g := buildLayoutGraph(nodes)
	errs := g.errors["aaaaaaaaaaaaaaaaaaaaaa02"]
	if len(errs) != 2 {
		t.Fatalf("expected err_node_id + esc_node_id as error edges, got %v", errs)
	}
	found := false
	for _, e := range errs {
		if e == "aaaaaaaaaaaaaaaaaaaaaa03" {
			found = true
		}
	}
	if !found {
		t.Fatalf("esc_node_id target missing from error edges: %v", errs)
	}
}
