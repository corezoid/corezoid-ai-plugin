package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// merge_apply.go turns a mergePlan into a materialised, pushable scheme and
// renders the human report. The merged file is always built from *mine* so my
// layout and untouched nodes are preserved; a colleague's non-conflicting
// changes are grafted in, their deletes honoured, and nodes we both changed are
// left as mine and listed for manual resolution.

// materializeMerge produces a merged conv JSON: mine, plus theirs-only edits,
// adds and deletes. Node identities (ids) come from mine where a node survives,
// and a fresh placeholder id for a grafted-new node; every grafted link is
// rewired from theirs' id-space to the merged id-space by target title.
func materializeMerge(mineConv string, plan mergePlan, theirsNodes []map[string]any) (string, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(mineConv), &doc); err != nil {
		return "", fmt.Errorf("merge: parse local file: %w", err)
	}
	scheme, ok := doc["scheme"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("merge: local file has no scheme object")
	}
	mineNodesRaw, _ := scheme["nodes"].([]any)
	mineNodes := make([]map[string]any, 0, len(mineNodesRaw))
	for _, raw := range mineNodesRaw {
		if n, ok := raw.(map[string]any); ok {
			mineNodes = append(mineNodes, n)
		}
	}
	mineKeys := matchKeys(mineNodes)
	theirsKeys := matchKeys(theirsNodes)

	theirsIDToKey := map[string]string{}
	for i, n := range theirsNodes {
		if id, _ := n["id"].(string); id != "" {
			theirsIDToKey[id] = theirsKeys[i]
		}
	}

	byKey := map[string]mergeNode{}
	for _, mn := range plan.Nodes {
		byKey[mn.Key] = mn
	}

	// merged key→id: mine's ids, minus server-deletes, plus new for server-adds.
	keyToID := map[string]string{}
	for i, n := range mineNodes {
		if id, _ := n["id"].(string); id != "" {
			keyToID[mineKeys[i]] = id
		}
	}
	for _, mn := range plan.Nodes {
		switch mn.Class {
		case clsDeletedTheirs:
			delete(keyToID, mn.Key)
		case clsAddedTheirs:
			keyToID[mn.Key] = placeholderID(mn.Key)
		}
	}

	var merged []any
	// Preserve mine's node order for everything that survives.
	for i, n := range mineNodes {
		mn, known := byKey[mineKeys[i]]
		if !known {
			merged = append(merged, n)
			continue
		}
		switch mn.Class {
		case clsDeletedTheirs:
			// drop — server removed it and I did not touch it
		case clsTheirs:
			merged = append(merged, graftEditedNode(mn.theirs.Raw, n, theirsIDToKey, keyToID))
		default:
			merged = append(merged, n) // keep mine (mine-edit / conflict / unchanged / mine-add)
		}
	}
	// Append nodes the server added.
	for _, mn := range plan.Nodes {
		if mn.Class != clsAddedTheirs {
			continue
		}
		merged = append(merged, graftNewNode(mn.theirs.Raw, keyToID[mn.Key], theirsIDToKey, keyToID))
	}

	scheme["nodes"] = merged
	doc["scheme"] = scheme
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("merge: marshal result: %w", err)
	}
	return string(out), nil
}

// graftEditedNode takes the server's version of a node that exists in mine but
// keeps mine's id and canvas position, then rewires the server's links.
func graftEditedNode(theirs, mine map[string]any, theirsIDToKey, keyToID map[string]string) map[string]any {
	g := deepCopyNode(theirs)
	g["id"] = mine["id"]
	if x, ok := mine["x"]; ok {
		g["x"] = x
	}
	if y, ok := mine["y"]; ok {
		g["y"] = y
	}
	rewireLinks(g, theirsIDToKey, keyToID)
	return g
}

// graftNewNode places a server-added node under a fresh placeholder id.
func graftNewNode(theirs map[string]any, newID string, theirsIDToKey, keyToID map[string]string) map[string]any {
	g := deepCopyNode(theirs)
	g["id"] = newID
	rewireLinks(g, theirsIDToKey, keyToID)
	return g
}

func deepCopyNode(n map[string]any) map[string]any {
	b, _ := json.Marshal(n)
	var c map[string]any
	_ = json.Unmarshal(b, &c)
	return c
}

// rewireLinks translates every link field of a node from theirs' id-space to
// the merged id-space (theirs id → target title → merged id). A link whose
// target no longer exists in the merge is left as-is so lint surfaces it rather
// than the merge hiding it.
func rewireLinks(node map[string]any, theirsIDToKey, keyToID map[string]string) {
	cond, ok := node["condition"].(map[string]any)
	if !ok {
		return
	}
	rewireList(cond["logics"], linkFields, theirsIDToKey, keyToID)
	rewireList(cond["semaphors"], semLinkFields, theirsIDToKey, keyToID)
}

func rewireList(listRaw any, fields []string, theirsIDToKey, keyToID map[string]string) {
	list, ok := listRaw.([]any)
	if !ok {
		return
	}
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		for _, f := range fields {
			id, ok := m[f].(string)
			if !ok || id == "" {
				continue
			}
			if key, known := theirsIDToKey[id]; known {
				if newID, ok := keyToID[key]; ok {
					m[f] = newID
				}
			}
		}
	}
}

// formatMergePlan renders the reconciliation as three short buckets — what YOU
// changed, what the SERVER changed, and where they OVERLAP — so the reader can
// see at a glance whether their own edits collide with the concurrent change.
func formatMergePlan(plan mergePlan) string {
	if len(plan.Yours) == 0 && len(plan.Grafts) == 0 && len(plan.Conflicts) == 0 {
		return "No node-level differences — only the version/metadata moved.\n"
	}
	var sb strings.Builder

	sb.WriteString("Your local edits (what this push would commit):\n")
	if len(plan.Yours) == 0 {
		if len(plan.Conflicts) > 0 {
			sb.WriteString("  (none outside the overlap below — your edit(s) landed on a node the server also changed)\n")
		} else {
			sb.WriteString("  (none — you changed no nodes; the server just has newer content)\n")
		}
	} else {
		for _, y := range plan.Yours {
			fmt.Fprintf(&sb, "  %s %-26s %s\n", changeSign(y.Class), nodeLabel(y), y.Detail)
		}
	}

	sb.WriteString("\nServer changed since your pull:\n")
	if len(plan.Grafts) == 0 {
		sb.WriteString("  (no mergeable server node changes — see the overlap below)\n")
	}
	for _, g := range plan.Grafts {
		fmt.Fprintf(&sb, "  %s %-26s %s — no overlap, mergeable\n", changeSign(g.Class), nodeLabel(g), g.Detail)
	}

	if len(plan.Conflicts) > 0 {
		sb.WriteString("\n⚠ Overlap — you and the server BOTH changed these node(s):\n")
		untitledConflict := false
		for _, c := range plan.Conflicts {
			fmt.Fprintf(&sb, "  ⚠ %-26s you: %s / server: %s\n",
				nodeLabel(c), sideDetail(c.base, c.mine), sideDetail(c.base, c.theirs))
			if c.Title == "" {
				untitledConflict = true
			}
		}
		if untitledConflict {
			sb.WriteString("    note: untitled nodes are matched by position; inserting/removing an untitled node\n")
			sb.WriteString("    on one side can shift that match and surface a false overlap — give nodes titles to avoid this.\n")
		}
	} else {
		sb.WriteString("\n✓ No overlap — none of the server's changes touch a node you edited.\n")
	}

	fmt.Fprintf(&sb, "\nSummary: %d local edit(s), %d mergeable server change(s), %d overlap/conflict(s).\n",
		len(plan.Yours), len(plan.Grafts), len(plan.Conflicts))
	return sb.String()
}

// changeSign maps a class to a +/-/~ marker (add / delete / edit).
func changeSign(c nodeClass) string {
	switch c {
	case clsAddedTheirs, clsAddedMine, clsAddedConflict:
		return "+"
	case clsDeletedTheirs, clsDeletedMine:
		return "-"
	default:
		return "~"
	}
}

// sideDetail describes one side of a conflict relative to the ancestor: an
// added node (no ancestor), a removed node (side absent), or the kind of edit.
func sideDetail(base, side *nodeCanon) string {
	if side == nil {
		return "removed"
	}
	if base == nil {
		return "added"
	}
	return describeChange(base, side)
}

func quote(s string) string { return "\"" + s + "\"" }
