package main

import "testing"

// nodeClusterStrip's staircase constants (stepY=30, belowOff=110) were written
// when layout collapsed cluster members itself, so every member was a 48px
// icon. Layout no longer touches modeForm, so a member arrives at whatever size
// its author left it — commonly a full 200x98+ block. With a flat 30px advance
// the strip buries each node inside the previous one and resolveOverlaps has to
// tear the designed shape apart afterwards.
//
// The fixtures cannot catch this: their error finals are authored collapsed, so
// the historical constants happen to be correct for them. This test uses
// EXPANDED members on purpose.
func TestClusterStripSeparatesExpandedMembers(t *testing.T) {
	g := &fixGen{}
	fin := g.final("done", true)

	// A dedicated cluster: owner --err--> reply --go--> errFinal, where the
	// reply node is a full expanded block rather than a collapsed icon.
	errFinal := g.code("write failure log", "")
	errFinal["extra"] = `{"modeForm":"expand","icon":""}`
	reply := g.code("reply to caller", nodeStr(errFinal, "id"))
	reply["extra"] = `{"modeForm":"expand","icon":""}`
	owner := g.code("optional write", nodeStr(fin, "id"), nodeStr(reply, "id"))
	start := g.start(nodeStr(owner, "id"))

	nodes := []map[string]interface{}{start, owner, reply, errFinal, fin}
	graph := buildLayoutGraph(nodes)
	members := map[string]bool{
		nodeStr(reply, "id"):    true,
		nodeStr(errFinal, "id"): true,
	}

	placed, _, _ := nodeClusterStrip(graph, nodeStr(owner, "id"), members)
	if len(placed) != 2 {
		t.Fatalf("expected both cluster members placed, got %d", len(placed))
	}

	// The strip is relative to the owner; absolute origin does not matter.
	box := func(p clusterPlacement) (int, int, int, int) {
		return nodeBox(graph.byID[p.id], p.dx, p.dy)
	}
	for i, a := range placed {
		ax0, ay0, ax1, ay1 := box(a)
		for _, b := range placed[i+1:] {
			bx0, by0, bx1, by1 := box(b)
			if ax0 < bx1 && bx0 < ax1 && ay0 < by1 && by0 < ay1 {
				t.Errorf("cluster members %q and %q overlap before the resolver runs: (%d,%d)-(%d,%d) vs (%d,%d)-(%d,%d)",
					nodeStr(graph.byID[a.id], "title"), nodeStr(graph.byID[b.id], "title"),
					ax0, ay0, ax1, ay1, bx0, by0, bx1, by1)
			}
		}
	}
}
