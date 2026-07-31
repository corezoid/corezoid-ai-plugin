package main

import "testing"

func TestMeasureLayoutReadabilityCountsStrictCrossing(t *testing.T) {
	g := &fixGen{}
	b := g.code("b", "")
	a := g.code("a", nodeStr(b, "id"))
	d := g.code("d", "")
	c := g.code("c", nodeStr(d, "id"))
	nodes := []map[string]interface{}{a, b, c, d}
	coords := map[string]lpoint{
		nodeStr(a, "id"): {0, 0},
		nodeStr(b, "id"): {300, 300},
		nodeStr(c, "id"): {300, 0},
		nodeStr(d, "id"): {0, 300},
	}

	got := measureLayoutReadability(coords, buildLayoutGraph(nodes))
	if got.EdgeCrossings != 1 {
		t.Fatalf("crossings=%d, want 1", got.EdgeCrossings)
	}
	if got.MaxEdgeSpan < 420 || got.P95EdgeSpan != got.MaxEdgeSpan {
		t.Fatalf("unexpected edge spans: max=%d p95=%d", got.MaxEdgeSpan, got.P95EdgeSpan)
	}
	if got.LongEdges != 0 || got.UpwardEdges != 0 {
		t.Fatalf("short downward fixture reported long/upward edges: %+v", got)
	}
}

func TestMeasureLayoutReadabilityTracksDedicatedErrorSpan(t *testing.T) {
	g := &fixGen{}
	errRoot := g.code("error", "")
	owner := g.code("owner", "", nodeStr(errRoot, "id"))
	nodes := []map[string]interface{}{owner, errRoot}
	coords := map[string]lpoint{
		nodeStr(owner, "id"):   {0, 0},
		nodeStr(errRoot, "id"): {300, 0},
	}

	got := measureLayoutReadability(coords, buildLayoutGraph(nodes))
	if got.MaxDedicatedSpan < 300 || got.MaxDedicatedSpan > 305 {
		t.Fatalf("max dedicated error span=%d, want approximately 300", got.MaxDedicatedSpan)
	}
	if got.LongDedicated != 0 {
		t.Fatalf("300px error edge incorrectly classified as long")
	}
}

func TestMeasureLayoutReadabilityTracksUpwardAndLongEdges(t *testing.T) {
	g := &fixGen{}
	target := g.code("target", "")
	source := g.code("source", nodeStr(target, "id"))
	nodes := []map[string]interface{}{source, target}
	coords := map[string]lpoint{
		nodeStr(source, "id"): {0, 1000},
		nodeStr(target, "id"): {0, 0},
	}

	got := measureLayoutReadability(coords, buildLayoutGraph(nodes))
	if got.UpwardEdges != 1 || got.LongEdges != 1 || got.MaxEdgeSpan != 1000 {
		t.Fatalf("upward/long metrics incorrect: %+v", got)
	}
}
