package main

import "testing"

// ---- test fixtures ---------------------------------------------------------

func cleanNode(id string, objType int, logics, semaphors []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":       id,
		"obj_type": float64(objType),
		"condition": map[string]interface{}{
			"logics":    logics,
			"semaphors": semaphors,
		},
	}
}

func cleanLogic(typ, toNodeID, errNodeID string) map[string]interface{} {
	m := map[string]interface{}{"type": typ, "to_node_id": toNodeID}
	if errNodeID != "" {
		m["err_node_id"] = errNodeID
	}
	return m
}

func cleanSem(toNodeID string) map[string]interface{} {
	return map[string]interface{}{"to_node_id": toNodeID}
}

func cleanFindNode(nodes []map[string]interface{}, id string) map[string]interface{} {
	for _, n := range nodes {
		if nid, _ := n["id"].(string); nid == id {
			return n
		}
	}
	return nil
}

// ---- cleanDeleteAndCascade: err_node_id must be fixed up independently ----
// from to_node_id (regression for the "continue skips err_node_id cleanup"
// and "err_node_id never tries a go-successor" bugs).

func TestCleanDeleteAndCascade_RedirectsBothToAndErrNodeID(t *testing.T) {
	// A --ok(to=B, err=C)--> ...; B has a go-successor (D); C has none.
	nodeA := cleanNode("A", 3, []interface{}{cleanLogic("ok", "B", "C")}, nil)
	nodeB := cleanNode("B", 0, []interface{}{cleanLogic("go", "D", "")}, nil)
	nodeC := cleanNode("C", 2, nil, nil) // final, no way out
	nodeD := cleanNode("D", 2, nil, nil) // final, the live successor

	nodes := []map[string]interface{}{nodeA, nodeB, nodeC, nodeD}
	origNmap := cleanSnapshotNodes(nodes)
	toRemove := map[string]bool{"B": true, "C": true}

	var dropped []string
	result := cleanDeleteAndCascade(nodes, toRemove, origNmap, &dropped)

	if cleanFindNode(result, "B") != nil || cleanFindNode(result, "C") != nil {
		t.Fatalf("expected B and C to be removed, got: %+v", result)
	}
	a := cleanFindNode(result, "A")
	if a == nil {
		t.Fatal("node A was unexpectedly removed")
	}
	logics := cleanNodeLogics(a)
	if len(logics) != 1 {
		t.Fatalf("expected exactly 1 logic on A, got %d", len(logics))
	}
	logic := logics[0].(map[string]interface{})
	if got, _ := logic["to_node_id"].(string); got != "D" {
		t.Errorf("to_node_id: got %q, want %q (redirected to B's go-successor)", got, "D")
	}
	if _, hasErr := logic["err_node_id"]; hasErr {
		t.Errorf("err_node_id should have been dropped (C has no go-successor), got %v", logic["err_node_id"])
	}
	if len(dropped) != 1 {
		t.Errorf("expected 1 dropped err_node_id warning, got %d: %v", len(dropped), dropped)
	}
}

func TestCleanDeleteAndCascade_RedirectsErrNodeIDToGoSuccessor(t *testing.T) {
	// A --ok(to=D, err=C)-->; to_node_id already alive, only err_node_id (C)
	// is removed, and C has a live go-successor E.
	nodeA := cleanNode("A", 3, []interface{}{cleanLogic("ok", "D", "C")}, nil)
	nodeC := cleanNode("C", 0, []interface{}{cleanLogic("go", "E", "")}, nil)
	nodeD := cleanNode("D", 2, nil, nil)
	nodeE := cleanNode("E", 2, nil, nil)

	nodes := []map[string]interface{}{nodeA, nodeC, nodeD, nodeE}
	origNmap := cleanSnapshotNodes(nodes)
	toRemove := map[string]bool{"C": true}

	var dropped []string
	result := cleanDeleteAndCascade(nodes, toRemove, origNmap, &dropped)

	a := cleanFindNode(result, "A")
	if a == nil {
		t.Fatal("node A was unexpectedly removed")
	}
	logic := cleanNodeLogics(a)[0].(map[string]interface{})
	if got, _ := logic["err_node_id"].(string); got != "E" {
		t.Errorf("err_node_id: got %q, want %q (redirected to C's go-successor)", got, "E")
	}
	if len(dropped) != 0 {
		t.Errorf("expected no dropped-err warnings, got %v", dropped)
	}
}

// ---- cleanSnapshotNodes: origNmap must not alias live in-place mutation ---
// (regression for pass-through detection being permanently dead code).

func TestCleanRemovePassThrough_DetectsConditionalAfterCascadeMutation(t *testing.T) {
	// X originally has two branches: go_if_const -> Y (Y gets removed with no
	// successor) and go -> Z. After cleanDeleteAndCascade, X is left with a
	// single "go" logic — the pass-through signature. cleanRemovePassThrough
	// must still recognize that X *originally* had a conditional branch by
	// consulting an untouched snapshot, not the live (already-mutated) node.
	nodeX := cleanNode("X", 0, []interface{}{
		cleanLogic("go_if_const", "Y", ""),
		cleanLogic("go", "Z", ""),
	}, nil)
	nodeY := cleanNode("Y", 2, nil, nil) // final, no successor -> logic dropped
	nodeZ := cleanNode("Z", 2, nil, nil)
	nodeW := cleanNode("W", 3, []interface{}{cleanLogic("ok", "X", "")}, nil) // caller of X

	nodes := []map[string]interface{}{nodeW, nodeX, nodeY, nodeZ}
	origNmap := cleanSnapshotNodes(nodes) // must be taken BEFORE cascade mutates X

	toRemove := map[string]bool{"Y": true}
	var dropped []string
	nodes = cleanDeleteAndCascade(nodes, toRemove, origNmap, &dropped)

	x := cleanFindNode(nodes, "X")
	if x == nil {
		t.Fatal("node X was unexpectedly removed by cascade")
	}
	if logics := cleanNodeLogics(x); len(logics) != 1 {
		t.Fatalf("precondition failed: expected X to be reduced to 1 logic after cascade, got %d", len(logics))
	}

	nodes, ptCount := cleanRemovePassThrough(nodes, origNmap)
	if ptCount != 1 {
		t.Fatalf("expected 1 pass-through removal, got %d — hadConditional check is not seeing the pre-mutation state", ptCount)
	}
	if cleanFindNode(nodes, "X") != nil {
		t.Error("node X should have been removed as a pass-through node")
	}
	w := cleanFindNode(nodes, "W")
	if w == nil {
		t.Fatal("node W was unexpectedly removed")
	}
	if got, _ := cleanNodeLogics(w)[0].(map[string]interface{})["to_node_id"].(string); got != "Z" {
		t.Errorf("W's logic: got to_node_id=%q, want %q (redirected past removed pass-through X)", got, "Z")
	}
}

// ---- start-node protection --------------------------------------------------
// (regression for the empty/start-less scheme being silently written out).

func TestCleanDeleteAndCascade_NeverCascadesStartNode(t *testing.T) {
	// S is the start node; its only logic targets R, which is removed with no
	// go-successor. S would end up with 0 logics and 0 semaphors — the exact
	// shape the cascade step deletes for obj_type 0/3 — but obj_type=1 (start)
	// must be exempt.
	nodeS := cleanNode("S", 1, []interface{}{cleanLogic("go", "R", "")}, nil)
	nodeR := cleanNode("R", 2, nil, nil)

	nodes := []map[string]interface{}{nodeS, nodeR}
	origNmap := cleanSnapshotNodes(nodes)
	toRemove := map[string]bool{"R": true}

	var dropped []string
	result := cleanDeleteAndCascade(nodes, toRemove, origNmap, &dropped)

	s := cleanFindNode(result, "S")
	if s == nil {
		t.Fatal("start node S was removed by cascade — a process cannot be pushed without a start node")
	}
	if len(cleanNodeLogics(s)) != 0 {
		t.Errorf("expected S's dangling logic to R to be dropped, got %v", cleanNodeLogics(s))
	}
}
