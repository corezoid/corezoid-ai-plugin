package main

import (
	"encoding/json"
	"os"
	"testing"
)

// TestLayeredRailPile is a regression guard for the monotone rail cursor.
// testdata/layered_rail_pile.conv.json is a real 139-node two-level routed
// process (region -> tier -> ray, each ray with a retry cluster) whose ray
// owners all land on one layer. Before the cursor, every cluster seeded at that
// single row on the error rail and 41 node pairs overlapped; the synthetic
// star/table fixtures cannot reach this path because region detection bundles
// their isomorphic rays into the hybrid strategy instead of the layered rail.
func TestLayeredRailPile(t *testing.T) {
	raw, err := os.ReadFile("testdata/layered_rail_pile.conv.json")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse testdata: %v", err)
	}
	rawNodes := doc["scheme"].(map[string]interface{})["nodes"].([]interface{})
	nodes := make([]map[string]interface{}, 0, len(rawNodes))
	for _, n := range rawNodes {
		nodes = append(nodes, n.(map[string]interface{}))
	}

	eng := &layoutEngine{density: "medium"}
	_, strategy, _ := eng.analyzeLayout(nodes)
	if strategy != "layered+error-rail" {
		t.Fatalf("fixture must exercise the layered rail; got strategy %q", strategy)
	}

	coords, rep := eng.computeLayout(nodes)
	if rep.Overlaps != 0 {
		g := buildLayoutGraph(nodes)
		t.Fatalf("layered rail must not overlap: report=%d pairs=%v",
			rep.Overlaps, overlapPairs(coords, g))
	}
}
