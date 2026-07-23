package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Test scaffolding: build scheme node lists the same shape a parsed .conv.json
// yields (obj_type as float64, logics as []any of map[string]any).

func tLogic(fields map[string]any) map[string]any { return fields }

func tNode(id, title string, objType int, logics ...map[string]any) map[string]any {
	ls := make([]any, len(logics))
	for i, l := range logics {
		ls[i] = l
	}
	return map[string]any{
		"id":       id,
		"title":    title,
		"obj_type": float64(objType),
		"x":        float64(0),
		"y":        float64(0),
		"condition": map[string]any{
			"logics":    ls,
			"semaphors": []any{},
		},
	}
}

func tScheme(nodes ...map[string]any) []map[string]any { return nodes }

func tConv(nodes []map[string]any) string {
	anyNodes := make([]any, len(nodes))
	for i, n := range nodes {
		anyNodes[i] = n
	}
	doc := map[string]any{"scheme": map[string]any{"nodes": anyNodes}}
	b, _ := json.Marshal(doc)
	return string(b)
}

// classOf finds a title's class in a plan.
func classOf(plan mergePlan, title string) (nodeClass, bool) {
	for _, n := range plan.Nodes {
		if n.Title == title {
			return n.Class, true
		}
	}
	return 0, false
}

// mergedNode parses a materialised conv and returns the node with the title.
func mergedNode(t *testing.T, mergedJSON, title string) map[string]any {
	t.Helper()
	for _, n := range localSchemeNodes(mergedJSON) {
		if s, _ := n["title"].(string); s == title {
			return n
		}
	}
	return nil
}

func srcOf(node map[string]any) string {
	if node == nil {
		return ""
	}
	return codeOf(node)
}

// A shared 4-node base: Start → Compute(api_code) →(ok) Done / (err) Err.
func baseFourNode() []map[string]any {
	return tScheme(
		tNode("s0000000000000000000000a", "Start", 1, tLogic(map[string]any{"type": "go", "to_node_id": "c0000000000000000000000b"})),
		tNode("c0000000000000000000000b", "Compute", 0, tLogic(map[string]any{
			"type": "api_code", "src": "return 1;", "to_node_id": "d0000000000000000000000c", "err_node_id": "e0000000000000000000000d",
		})),
		tNode("d0000000000000000000000c", "Done", 2),
		tNode("e0000000000000000000000d", "Err", 2),
	)
}

func TestMerge_Case1_TheirsModifiesJS(t *testing.T) {
	base := baseFourNode()
	mine := baseFourNode() // unchanged
	theirs := baseFourNode()
	// colleague changes the Compute JS
	theirs[1]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["src"] = "return 42;"

	plan := buildMergePlan(base, theirs, mine)
	if c, _ := classOf(plan, "Compute"); c != clsTheirs {
		t.Fatalf("Compute should be theirs-edit (mergeable), got %v", c)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("no conflicts expected, got %d", len(plan.Conflicts))
	}
	merged, err := materializeMerge(tConv(mine), plan, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if got := srcOf(mergedNode(t, merged, "Compute")); got != "return 42;" {
		t.Fatalf("merged Compute JS = %q, want the colleague's %q", got, "return 42;")
	}
}

func TestMerge_Case2_TheirsModifiesOptions(t *testing.T) {
	base := baseFourNode()
	mine := baseFourNode()
	theirs := baseFourNode()
	theirs[1]["options"] = `{"save_task":true}` // colleague toggles an option

	plan := buildMergePlan(base, theirs, mine)
	if c, _ := classOf(plan, "Compute"); c != clsTheirs {
		t.Fatalf("options change should be theirs-edit, got %v", c)
	}
	merged, _ := materializeMerge(tConv(mine), plan, theirs)
	if opt := optionsOf(mergedNode(t, merged, "Compute")); opt == "" {
		t.Fatalf("merged Compute should carry colleague's options, got empty")
	}
}

func TestMerge_Case3_TheirsModifiesRouting(t *testing.T) {
	base := baseFourNode()
	mine := baseFourNode()
	theirs := baseFourNode()
	// colleague reroutes Compute error to Done instead of Err
	theirs[1]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["err_node_id"] = "d0000000000000000000000c"

	plan := buildMergePlan(base, theirs, mine)
	if c, _ := classOf(plan, "Compute"); c != clsTheirs {
		t.Fatalf("routing change should be theirs-edit, got %v", c)
	}
	merged, _ := materializeMerge(tConv(mine), plan, theirs)
	// the merged Compute err link must resolve to mine's Done id
	n := mergedNode(t, merged, "Compute")
	logic := n["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)
	if logic["err_node_id"] != "d0000000000000000000000c" {
		t.Fatalf("merged err link not rewired to Done id, got %v", logic["err_node_id"])
	}
}

func TestMerge_Case4_TheirsAddsNode(t *testing.T) {
	base := baseFourNode()
	mine := baseFourNode()
	theirs := baseFourNode()
	// colleague inserts a Fraud Check node
	theirs = append(theirs, tNode("f000000000000000000000ff", "Fraud Check", 0,
		tLogic(map[string]any{"type": "go", "to_node_id": "d0000000000000000000000c"})))

	plan := buildMergePlan(base, theirs, mine)
	if c, _ := classOf(plan, "Fraud Check"); c != clsAddedTheirs {
		t.Fatalf("Fraud Check should be added-theirs, got %v", c)
	}
	merged, _ := materializeMerge(tConv(mine), plan, theirs)
	if mergedNode(t, merged, "Fraud Check") == nil {
		t.Fatalf("merged scheme must contain the colleague's new node")
	}
	// its link should be rewired to mine's Done id
	fc := mergedNode(t, merged, "Fraud Check")
	logic := fc["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)
	if logic["to_node_id"] != "d0000000000000000000000c" {
		t.Fatalf("new node link not rewired to merged Done id, got %v", logic["to_node_id"])
	}
}

func TestMerge_Case5_TheirsDeletesNode(t *testing.T) {
	base := baseFourNode()
	mine := baseFourNode()
	// colleague removes the Err node
	theirs := tScheme(base[0], base[1], base[2])

	plan := buildMergePlan(base, theirs, mine)
	if c, _ := classOf(plan, "Err"); c != clsDeletedTheirs {
		t.Fatalf("Err should be deleted-theirs, got %v", c)
	}
	merged, _ := materializeMerge(tConv(mine), plan, theirs)
	if mergedNode(t, merged, "Err") != nil {
		t.Fatalf("merged scheme must drop the server-deleted node")
	}
}

func TestMerge_Case6_BothEditSameNodeConflict(t *testing.T) {
	base := baseFourNode()
	mine := baseFourNode()
	theirs := baseFourNode()
	mine[1]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["src"] = "return 'mine';"
	theirs[1]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["src"] = "return 'theirs';"

	plan := buildMergePlan(base, theirs, mine)
	if c, _ := classOf(plan, "Compute"); c != clsConflict {
		t.Fatalf("Compute edited by both should be a conflict, got %v", c)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected exactly 1 conflict, got %d", len(plan.Conflicts))
	}
	merged, _ := materializeMerge(tConv(mine), plan, theirs)
	if got := srcOf(mergedNode(t, merged, "Compute")); got != "return 'mine';" {
		t.Fatalf("conflict node must keep MY version, got %q", got)
	}
}

func TestMerge_Case7_DisjointEditsAutoMerge(t *testing.T) {
	base := baseFourNode()
	mine := baseFourNode()
	theirs := baseFourNode()
	// I edit Compute; colleague edits a different node's routing (Start → add nothing, change Start target? use Done title change)
	mine[1]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["src"] = "return 'mine';"
	// colleague adds a node instead (disjoint)
	theirs = append(theirs, tNode("f000000000000000000000ff", "Audit Log", 0,
		tLogic(map[string]any{"type": "go", "to_node_id": "d0000000000000000000000c"})))

	plan := buildMergePlan(base, theirs, mine)
	if c, _ := classOf(plan, "Compute"); c != clsMine {
		t.Fatalf("Compute (only I changed) should be mine, got %v", c)
	}
	if c, _ := classOf(plan, "Audit Log"); c != clsAddedTheirs {
		t.Fatalf("Audit Log (only server added) should be added-theirs, got %v", c)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("disjoint edits must not conflict, got %d", len(plan.Conflicts))
	}
	merged, _ := materializeMerge(tConv(mine), plan, theirs)
	if got := srcOf(mergedNode(t, merged, "Compute")); got != "return 'mine';" {
		t.Fatalf("merged must keep my Compute edit, got %q", got)
	}
	if mergedNode(t, merged, "Audit Log") == nil {
		t.Fatalf("merged must include the colleague's added node")
	}
}

func TestMerge_Case8_Rename(t *testing.T) {
	base := baseFourNode()
	mine := baseFourNode()
	theirs := baseFourNode()
	// colleague renames "Done" → "Completed" (same body)
	theirs[2]["title"] = "Completed"

	plan := buildMergePlan(base, theirs, mine)
	// a rename reads as delete of the old title + add of the new one
	if c, _ := classOf(plan, "Done"); c != clsDeletedTheirs {
		t.Fatalf("renamed-away title should read as deleted-theirs, got %v", c)
	}
	if c, _ := classOf(plan, "Completed"); c != clsAddedTheirs {
		t.Fatalf("renamed-to title should read as added-theirs, got %v", c)
	}
}

func TestMerge_NoChangeIsClean(t *testing.T) {
	base := baseFourNode()
	plan := buildMergePlan(base, baseFourNode(), baseFourNode())
	if len(plan.Grafts) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf("identical schemes must yield no grafts/conflicts, got %d/%d",
			len(plan.Grafts), len(plan.Conflicts))
	}
}

func TestMerge_ReportBucketsAndOverlap(t *testing.T) {
	// I edit Compute; server edits Done (disjoint); we both edit Start (overlap).
	base := baseFourNode()
	mine := baseFourNode()
	theirs := baseFourNode()
	mine[1]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["src"] = "return 'mine';"
	theirs[2]["description"] = "server touched Done"
	mine[0]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["to_node_id"] = "d0000000000000000000000c"   // I reroute Start
	theirs[0]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["to_node_id"] = "e0000000000000000000000d" // server reroutes Start differently

	plan := buildMergePlan(base, theirs, mine)
	if len(plan.Yours) != 1 || plan.Yours[0].Title != "Compute" {
		t.Fatalf("Yours should be [Compute], got %v", titlesOf(plan.Yours))
	}
	if len(plan.Grafts) != 1 || plan.Grafts[0].Title != "Done" {
		t.Fatalf("Grafts should be [Done], got %v", titlesOf(plan.Grafts))
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].Title != "Start" {
		t.Fatalf("Conflicts should be [Start], got %v", titlesOf(plan.Conflicts))
	}
	report := formatMergePlan(plan)
	for _, want := range []string{
		"Your local edits", "\"Compute\"",
		"Server changed since your pull", "\"Done\"", "no overlap, mergeable",
		"Overlap", "\"Start\"", "you:", "server:",
		"1 local edit(s), 1 mergeable server change(s), 1 overlap",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestMerge_NoLocalEditsIsFlagged(t *testing.T) {
	// I changed nothing; the server added a node. Report should say "none".
	base := baseFourNode()
	mine := baseFourNode()
	theirs := append(baseFourNode(), tNode("f000000000000000000000ff", "New", 0))

	plan := buildMergePlan(base, theirs, mine)
	if len(plan.Yours) != 0 {
		t.Fatalf("expected no local edits, got %v", titlesOf(plan.Yours))
	}
	if !strings.Contains(formatMergePlan(plan), "(none — you changed no nodes") {
		t.Fatalf("report should flag that the user made no local edits:\n%s", formatMergePlan(plan))
	}
}

func TestMerge_ConflictOnlyWordsYoursClearly(t *testing.T) {
	// My only edit lands on a node the server also changed → Yours empty, but the
	// report must not claim "you changed no nodes".
	base := baseFourNode()
	mine := baseFourNode()
	theirs := baseFourNode()
	mine[1]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["src"] = "return 'mine';"
	theirs[1]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["src"] = "return 'theirs';"

	report := formatMergePlan(buildMergePlan(base, theirs, mine))
	if strings.Contains(report, "you changed no nodes") {
		t.Fatalf("must not claim no local edits when the edit is in the overlap:\n%s", report)
	}
	if !strings.Contains(report, "outside the overlap below") {
		t.Fatalf("expected the overlap-aware wording:\n%s", report)
	}
}

func titlesOf(ns []mergeNode) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Title
	}
	return out
}

// untitledScheme: untitled Start -> "Gate" -> two untitled ends. Real Corezoid
// processes routinely leave Start events and error finals untitled.
func untitledScheme() []map[string]any {
	return tScheme(
		tNode("u0000000000000000000001", "", 1, tLogic(map[string]any{"type": "go", "to_node_id": "u0000000000000000000002"})),
		tNode("u0000000000000000000002", "Gate", 0,
			tLogic(map[string]any{"type": "go_if_const", "to_node_id": "u0000000000000000000003"}),
			tLogic(map[string]any{"type": "go", "to_node_id": "u0000000000000000000004"})),
		tNode("u0000000000000000000003", "", 2),
		tNode("u0000000000000000000004", "", 2),
	)
}

func TestMerge_UntitledNodesNotFalseConflicts(t *testing.T) {
	base, mine, theirs := untitledScheme(), untitledScheme(), untitledScheme()
	// colleague changes only the titled Gate; the three untitled nodes are untouched
	theirs[1]["description"] = "gate touched by colleague"

	plan := buildMergePlan(base, theirs, mine)
	if len(plan.Conflicts) != 0 {
		t.Fatalf("untitled nodes must not become false conflicts, got %d conflict(s)", len(plan.Conflicts))
	}
	if len(plan.Nodes) != 4 {
		t.Fatalf("all 4 nodes (incl. 3 untitled) must be represented distinctly, got %d", len(plan.Nodes))
	}
	if c, _ := classOf(plan, "Gate"); c != clsTheirs {
		t.Fatalf("Gate should be theirs-edit, got %v", c)
	}
	// merge must keep every node, untitled ones included
	merged, err := materializeMerge(tConv(mine), plan, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(localSchemeNodes(merged)); n != 4 {
		t.Fatalf("merged scheme must keep all 4 nodes, got %d", n)
	}
}

func TestMerge_UntitledNodeEditIsMergeable(t *testing.T) {
	base, mine, theirs := untitledScheme(), untitledScheme(), untitledScheme()
	// colleague edits ONE untitled end (the first obj_type=2 node); I touch nothing
	theirs[2]["description"] = "first end, edited by colleague"

	plan := buildMergePlan(base, theirs, mine)
	if len(plan.Conflicts) != 0 {
		t.Fatalf("a one-sided untitled edit must be mergeable, not a conflict, got %d", len(plan.Conflicts))
	}
	if len(plan.Grafts) != 1 || plan.Grafts[0].ObjType != 2 || plan.Grafts[0].Title != "" {
		t.Fatalf("expected exactly one grafted untitled end, got %+v", plan.Grafts)
	}
}

func TestMerge_UntitledConflictShowsPositionNote(t *testing.T) {
	base, mine, theirs := untitledScheme(), untitledScheme(), untitledScheme()
	mine[2]["description"] = "end edited by me"
	theirs[2]["description"] = "end edited by colleague"

	plan := buildMergePlan(base, theirs, mine)
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].Title != "" {
		t.Fatalf("expected one untitled conflict, got %+v", plan.Conflicts)
	}
	if !strings.Contains(formatMergePlan(plan), "untitled nodes are matched by position") {
		t.Fatalf("expected the positional-matching note for an untitled conflict:\n%s", formatMergePlan(plan))
	}
}

func TestMerge_DuplicateTitleIsConflict(t *testing.T) {
	base := baseFourNode()
	mine := baseFourNode()
	theirs := baseFourNode()
	// two nodes share a title on the server, and the content differs from mine
	theirs = append(theirs, tNode("g000000000000000000000aa", "Compute", 0,
		tLogic(map[string]any{"type": "api_code", "src": "return 9;"})))
	theirs[1]["condition"].(map[string]any)["logics"].([]any)[0].(map[string]any)["src"] = "return 9;"

	plan := buildMergePlan(base, theirs, mine)
	if c, _ := classOf(plan, "Compute"); c != clsConflict {
		t.Fatalf("ambiguous duplicate title must be a conflict, not an auto-merge, got %v", c)
	}
}
