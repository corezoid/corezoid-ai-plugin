package main

import (
	"os"
	"strings"
	"testing"
)

// compact() must never CREATE an overlap. cluster1D chains transitively — three
// 48px icons spaced 50px apart are one "row" spanning 148px — but squeeze used
// to reserve only the row's TALLEST member (48+gap) before placing the next
// row. The next row was then pulled up into the lower members of the previous
// one, undoing work resolveOverlaps had already done.
//
// This is reachable in production: computeLayout re-runs resolveOverlaps after
// compact and masks it, but layoutSugiyama and layoutHybrid both end with
// compact → clampCoords and no re-resolve.
func TestCompactNeverCreatesOverlap(t *testing.T) {
	g := &fixGen{}
	// One generator so every node gets a distinct id.
	mk := func(x, y int) map[string]interface{} {
		n := g.code("step", "")
		n["extra"] = `{"modeForm":"collapse","icon":""}`
		n["x"], n["y"] = float64(x), float64(y)
		return n
	}
	// y keys 100/150/200 chain into one cluster (each step 50 <= the tol of 55);
	// the node at 300 is its own cluster and shares a column with the one at 200.
	nodes := []map[string]interface{}{mk(500, 100), mk(800, 150), mk(1100, 200), mk(1100, 300)}

	graph := buildLayoutGraph(nodes)
	coords := map[string]lpoint{}
	for _, n := range nodes {
		coords[nodeStr(n, "id")] = lpoint{int(n["x"].(float64)), int(n["y"].(float64))}
	}
	if before := countOverlaps(coords, graph); before != 0 {
		t.Fatalf("fixture is already overlapping (%d pairs) — it cannot prove anything", before)
	}

	out := (&layoutEngine{density: "compact"}).compact(coords, graph)
	if after := countOverlaps(out, graph); after != 0 {
		t.Errorf("compact() created %d overlapping pair(s)", after)
		for _, n := range nodes {
			id := nodeStr(n, "id")
			t.Logf("  %s: y %v -> %d", id[:8], n["y"], out[id].Y)
		}
	}
}

// A duplicate node id used to be accepted silently: the engine keys every map
// by id, so one node vanished, the report claimed a phantom overlap, and the
// survivor was laid out as if the file were smaller than it is.
func TestLoadLayoutDocRejectsDuplicateNodeIDs(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/dup.conv.json"
	const doc = `{
  "obj_type": 1,
  "title": "dup",
  "scheme": {
    "nodes": [
      {"id": "aaaaaaaaaaaaaaaaaaaaaaaa", "obj_type": 1, "condition": {"logics": [], "semaphors": []}, "x": 0, "y": 0},
      {"id": "aaaaaaaaaaaaaaaaaaaaaaaa", "obj_type": 2, "condition": {"logics": [], "semaphors": []}, "x": 0, "y": 0}
    ],
    "web_settings": [[], []]
  }
}
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadLayoutDoc(path)
	if err == nil {
		t.Fatal("duplicate node ids were accepted; layout would silently drop a node")
	}
	if !strings.Contains(err.Error(), "duplicate node id") {
		t.Fatalf("error does not name the problem: %v", err)
	}
}
