package main

import "testing"

// TestWaterfallAlignsLoopBodyToEntryRow guards the loop-body alignment in
// placeComponent. Topology mirrors 02_parse_transcript (process 1891328): a
// straight spine reaches a fan-out check, a condition sends work into a
// three-step loop body, and the body's last step returns to the check.
//
// Placed as an ordinary branch the body starts one row BELOW the condition, so
// the return edge climbs an extra row and cuts diagonally back across the
// spine. The body's first row must instead sit ON the condition's row.
func TestWaterfallAlignsLoopBodyToEntryRow(t *testing.T) {
	g := &fixGen{}
	fe := g.final("error", false)
	g.err = nodeStr(fe, "id")
	parsed := g.final("Parsed", true)

	body3 := g.code("Increment Fan-Out Index", "") // rewired to the head below
	body2 := g.code("Dispatch to 03", nodeStr(body3, "id"))
	body1 := g.code("Prepare Participant Dispatch", nodeStr(body2, "id"))

	route := g.cond("Route: More Participants?",
		[]fixBranch{{"more", "true", nodeStr(body1, "id")}},
		nodeStr(parsed, "id"))
	head := g.code("Check Fan-Out Progress", nodeStr(route, "id"))
	setGo(body3, nodeStr(head, "id")) // the back edge closing the loop

	prelude, preludeHead := g.chainOf(6, "step", nodeStr(head, "id"))
	st := g.start(preludeHead)

	nodes := append([]map[string]interface{}{st}, prelude...)
	nodes = append(nodes, head, route, body1, body2, body3, parsed, fe)

	coords, rep := (&layoutEngine{density: "medium"}).computeLayout(nodes)

	yRoute := coords[nodeStr(route, "id")].Y
	yBody1 := coords[nodeStr(body1, "id")].Y
	// Before the fix the body head sat a full row lower; now it shares the row.
	if yBody1-yRoute >= layRowStep {
		t.Errorf("loop body head is %dpx below its entry row (want < %dpx): route y=%d, body y=%d",
			yBody1-yRoute, layRowStep, yRoute, yBody1)
	}

	// The return edge must not climb more rows than the body is deep.
	climb := coords[nodeStr(body3, "id")].Y - coords[nodeStr(head, "id")].Y
	if climb > 3*layRowStep {
		t.Errorf("loop return climbs %dpx (want <= %dpx)", climb, 3*layRowStep)
	}

	// The body is a side excursion: it must not sit in the spine column.
	xRoute := coords[nodeStr(route, "id")].X
	for _, n := range []map[string]interface{}{body1, body2, body3} {
		if x := coords[nodeStr(n, "id")].X; x == xRoute {
			t.Errorf("loop body node %q shares the spine column x=%d", nodeStr(n, "title"), x)
		}
	}

	if rep.Overlaps != 0 {
		t.Errorf("%d overlaps after loop alignment", rep.Overlaps)
	}
}
