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

	theirsIDToTitle := map[string]string{}
	for _, n := range theirsNodes {
		id, _ := n["id"].(string)
		title, _ := n["title"].(string)
		if id != "" {
			theirsIDToTitle[id] = title
		}
	}

	byTitle := map[string]mergeNode{}
	for _, mn := range plan.Nodes {
		byTitle[mn.Title] = mn
	}

	// merged title→id: mine's ids, minus server-deletes, plus new for server-adds.
	titleToID := map[string]string{}
	for _, raw := range mineNodesRaw {
		n, _ := raw.(map[string]any)
		title, _ := n["title"].(string)
		id, _ := n["id"].(string)
		if title != "" && id != "" {
			titleToID[title] = id
		}
	}
	for _, mn := range plan.Nodes {
		switch mn.Class {
		case clsDeletedTheirs:
			delete(titleToID, mn.Title)
		case clsAddedTheirs:
			titleToID[mn.Title] = placeholderID(mn.Title)
		}
	}

	var merged []any
	// Preserve mine's node order for everything that survives.
	for _, raw := range mineNodesRaw {
		n, _ := raw.(map[string]any)
		title, _ := n["title"].(string)
		mn, known := byTitle[title]
		if !known {
			merged = append(merged, n)
			continue
		}
		switch mn.Class {
		case clsDeletedTheirs:
			// drop — server removed it and I did not touch it
		case clsTheirs:
			merged = append(merged, graftEditedNode(mn.theirs.Raw, n, theirsIDToTitle, titleToID))
		default:
			merged = append(merged, n) // keep mine (mine-edit / conflict / unchanged / mine-add)
		}
	}
	// Append nodes the server added.
	for _, mn := range plan.Nodes {
		if mn.Class != clsAddedTheirs {
			continue
		}
		merged = append(merged, graftNewNode(mn.theirs.Raw, titleToID[mn.Title], theirsIDToTitle, titleToID))
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
func graftEditedNode(theirs, mine map[string]any, theirsIDToTitle, titleToID map[string]string) map[string]any {
	g := deepCopyNode(theirs)
	g["id"] = mine["id"]
	if x, ok := mine["x"]; ok {
		g["x"] = x
	}
	if y, ok := mine["y"]; ok {
		g["y"] = y
	}
	rewireLinks(g, theirsIDToTitle, titleToID)
	return g
}

// graftNewNode places a server-added node under a fresh placeholder id.
func graftNewNode(theirs map[string]any, newID string, theirsIDToTitle, titleToID map[string]string) map[string]any {
	g := deepCopyNode(theirs)
	g["id"] = newID
	rewireLinks(g, theirsIDToTitle, titleToID)
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
func rewireLinks(node map[string]any, theirsIDToTitle, titleToID map[string]string) {
	cond, ok := node["condition"].(map[string]any)
	if !ok {
		return
	}
	rewireList(cond["logics"], linkFields, theirsIDToTitle, titleToID)
	rewireList(cond["semaphors"], semLinkFields, theirsIDToTitle, titleToID)
}

func rewireList(listRaw any, fields []string, theirsIDToTitle, titleToID map[string]string) {
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
			if title, known := theirsIDToTitle[id]; known {
				if newID, ok := titleToID[title]; ok {
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
			fmt.Fprintf(&sb, "  %s %-26s %s\n", changeSign(y.Class), quote(y.Title), y.Detail)
		}
	}

	sb.WriteString("\nServer changed since your pull:\n")
	if len(plan.Grafts) == 0 {
		sb.WriteString("  (no mergeable server node changes — see the overlap below)\n")
	}
	for _, g := range plan.Grafts {
		fmt.Fprintf(&sb, "  %s %-26s %s — no overlap, mergeable\n", changeSign(g.Class), quote(g.Title), g.Detail)
	}

	if len(plan.Conflicts) > 0 {
		sb.WriteString("\n⚠ Overlap — you and the server BOTH changed these node(s):\n")
		for _, c := range plan.Conflicts {
			fmt.Fprintf(&sb, "  ⚠ %-26s you: %s / server: %s\n",
				quote(c.Title), sideDetail(c.base, c.mine), sideDetail(c.base, c.theirs))
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
