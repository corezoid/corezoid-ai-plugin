package main

import (
	"encoding/json"
	"testing"
)

func cNode(title string, objType int, x, y float64) map[string]any {
	return map[string]any{
		"title":     title,
		"obj_type":  float64(objType),
		"x":         x,
		"y":         y,
		"condition": map[string]any{"logics": []any{}, "semaphors": []any{}},
	}
}

func cConv(nodes []map[string]any) string {
	anyNodes := make([]any, len(nodes))
	for i, n := range nodes {
		anyNodes[i] = n
	}
	b, _ := json.Marshal(map[string]any{"scheme": map[string]any{"nodes": anyNodes}})
	return string(b)
}

func coordOfTitle(convJSON, title string) (float64, float64) {
	for _, n := range schemeNodesFromConv(convJSON) {
		if t, _ := n["title"].(string); t == title {
			x, _ := n["x"].(float64)
			y, _ := n["y"].(float64)
			return x, y
		}
	}
	return -1, -1
}

func TestAnyNodeUnplaced(t *testing.T) {
	all0 := cConv([]map[string]any{cNode("A", 1, 0, 0), cNode("B", 2, 0, 0)})
	if !anyNodeUnplaced(all0) {
		t.Fatal("all-at-origin scheme must report an unplaced node")
	}
	some := cConv([]map[string]any{cNode("A", 1, 100, 100), cNode("B", 2, 0, 0)})
	if !anyNodeUnplaced(some) {
		t.Fatal("a single unplaced node must be detected (partial loss)")
	}
	allPlaced := cConv([]map[string]any{cNode("A", 1, 100, 100), cNode("B", 2, 100, 300)})
	if anyNodeUnplaced(allPlaced) {
		t.Fatal("a fully placed scheme must report no unplaced node")
	}
	if anyNodeUnplaced(cConv(nil)) {
		t.Fatal("empty scheme must report no unplaced node")
	}
}

func TestRehydrate_FillsFromServerByTitle(t *testing.T) {
	// local lost every coordinate; server has the real layout
	local := cConv([]map[string]any{cNode("Start", 1, 0, 0), cNode("Work", 0, 0, 0), cNode("Done", 2, 0, 0)})
	server := []map[string]any{cNode("Start", 1, 100, 100), cNode("Work", 0, 100, 300), cNode("Done", 2, 100, 500)}

	out, n := rehydrateCoords(local, server)
	if n != 3 {
		t.Fatalf("expected 3 coordinates restored, got %d", n)
	}
	if x, y := coordOfTitle(out, "Work"); x != 100 || y != 300 {
		t.Fatalf("Work not restored: got (%v,%v)", x, y)
	}
	if anyNodeUnplaced(out) {
		t.Fatal("after re-hydrate no node should remain unplaced (baseLayout won't trigger)")
	}
}

func TestRehydrate_PartialLoss(t *testing.T) {
	// Only B lost its coordinates; A is intact; C is genuinely new (not on the
	// server). Re-hydrate must restore B, keep A, and leave C unplaced.
	local := cConv([]map[string]any{cNode("A", 1, 100, 100), cNode("B", 0, 0, 0), cNode("C", 0, 0, 0)})
	server := []map[string]any{cNode("A", 1, 100, 100), cNode("B", 0, 100, 300)} // C absent on server
	out, n := rehydrateCoords(local, server)
	if n != 1 {
		t.Fatalf("only the partially-lost node B should be restored, got %d", n)
	}
	if x, y := coordOfTitle(out, "B"); x != 100 || y != 300 {
		t.Fatalf("B not restored: (%v,%v)", x, y)
	}
	for _, nd := range schemeNodesFromConv(out) {
		if nd["title"] == "C" && (nd["x"].(float64) != 0 || nd["y"].(float64) != 0) {
			t.Fatalf("genuinely-new node C must stay unplaced, got (%v,%v)", nd["x"], nd["y"])
		}
	}
}

func TestRehydrate_UntitledByOrdinal(t *testing.T) {
	// two untitled end nodes, matched by obj_type + position
	local := cConv([]map[string]any{cNode("", 1, 0, 0), cNode("", 2, 0, 0), cNode("", 2, 0, 0)})
	server := []map[string]any{cNode("", 1, 50, 50), cNode("", 2, 50, 250), cNode("", 2, 400, 250)}
	out, n := rehydrateCoords(local, server)
	if n != 3 {
		t.Fatalf("expected 3 untitled coords restored, got %d", n)
	}
	nodes := schemeNodesFromConv(out)
	if nodes[2]["x"].(float64) != 400 {
		t.Fatalf("second untitled end must take the 2nd server position (x=400), got %v", nodes[2]["x"])
	}
}

func TestRehydrate_NeverOverwritesPlaced(t *testing.T) {
	local := cConv([]map[string]any{cNode("A", 1, 700, 700), cNode("B", 2, 0, 0)})
	server := []map[string]any{cNode("A", 1, 100, 100), cNode("B", 2, 100, 300)}
	out, n := rehydrateCoords(local, server)
	if n != 1 {
		t.Fatalf("only the unplaced node should be filled, got %d", n)
	}
	if x, _ := coordOfTitle(out, "A"); x != 700 {
		t.Fatalf("placed node A must keep its local coord 700, got %v", x)
	}
}

func TestRehydrate_AmbiguousTitleNotImposed(t *testing.T) {
	// two server nodes share a title → ambiguous → don't impose
	local := cConv([]map[string]any{cNode("Dup", 0, 0, 0), cNode("Dup", 0, 0, 0)})
	server := []map[string]any{cNode("Dup", 0, 100, 100), cNode("Dup", 0, 100, 300)}
	_, n := rehydrateCoords(local, server)
	if n != 0 {
		t.Fatalf("ambiguous duplicate title must not be imposed, got %d filled", n)
	}
}

func TestRehydrate_ServerUnplacedNothingToFill(t *testing.T) {
	local := cConv([]map[string]any{cNode("A", 1, 0, 0)})
	server := []map[string]any{cNode("A", 1, 0, 0)} // server also has no layout
	_, n := rehydrateCoords(local, server)
	if n != 0 {
		t.Fatalf("nothing to restore when server is also unplaced, got %d", n)
	}
}
