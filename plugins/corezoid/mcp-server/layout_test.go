package main

import (
	"fmt"
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
// nodes. Reference (proto/layout.py): a=(700,0) b=(600,180) c=(600,360).
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
	if pos["b"] != [2]int{600, 180} {
		t.Errorf("b: want (600,180), got %v", pos["b"])
	}
	if pos["c"] != [2]int{600, 360} {
		t.Errorf("c: want (600,360), got %v", pos["c"])
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
// spacing above the small-process minimum (180), capped at base+60 (240).
// A long primary chain of N nodes places the bottom node at (N-1)*vStep.
// Reference (proto/layout.py): N=21 -> vStep=200, bottom y=4000;
// N=100 -> vStep=240 (capped), bottom y=23760.
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
	if small != 180 {
		t.Errorf("4-node chain vStep=%d want 180 (minimum)", small)
	}

	g21 := chain(21)
	if v := vStepOf(g21); v != 200 {
		t.Errorf("21-node chain vStep=%d want 200 (raised)", v)
	} else if v <= small {
		t.Errorf("21-node vStep %d must exceed 4-node vStep %d", v, small)
	}
	pos21 := assignPositions(g21, "default")
	if pos21["n20"][1] != 4000 {
		t.Errorf("21-node bottom y=%d want 4000", pos21["n20"][1])
	}

	// Cap: very large N must not exceed base+60 = 240.
	if v := vStepOf(chain(100)); v != 240 {
		t.Errorf("100-node chain vStep=%d want 240 (capped at base+60)", v)
	}
}
