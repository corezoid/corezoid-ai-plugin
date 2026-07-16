package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// mergeplan.go implements a 3-way, node-level merge of a Corezoid process
// scheme. When push detects the server moved since pull (conflict.go), this
// reconciles three versions:
//
//	base   — the scheme as it was pulled            (readAncestorScheme)
//	theirs — the current server scheme              (ExportProcess)
//	mine   — the local edited file                  (the push payload)
//
// All three arrive in the same .conv.json shape, so the only volatile fields
// are node id / x / y (a push regenerates every server node id) and UI extra.
// Nodes are matched across versions by a stable key (see matchKeys): the title
// for titled nodes, or obj_type + ordinal for untitled ones; link references
// inside logics and semaphors are normalised to the *key* of their target so a
// link survives id regeneration. This makes "a colleague changed node A"
// distinguishable from "I changed node B" — the former is grafted automatically,
// a node both sides changed is a genuine conflict left for the human.

// nodeClass is how one node (matched by key) reconciles across base/theirs/mine.
type nodeClass int

const (
	clsUnchanged          nodeClass = iota // same everywhere (or theirs==mine)
	clsTheirs                              // changed only on the server → graft theirs
	clsMine                                // changed only by me → keep mine
	clsConflict                            // changed on both sides, differently → human decides
	clsAddedTheirs                         // new on the server → graft
	clsAddedMine                           // new locally → keep
	clsAddedConflict                       // both added same title, different body → human decides
	clsDeletedTheirs                       // removed on the server, untouched by me → drop
	clsDeletedMine                         // removed locally, untouched on server → stays removed
	clsDeleteEditConflict                  // one side deleted, the other edited → human decides
)

// nodeCanon is a node reduced to a comparable form plus its original body.
type nodeCanon struct {
	Key       string // cross-version match key (see matchKeys)
	Title     string // display title (may be empty)
	ObjType   int
	Body      string         // canonical JSON of the semantic content (no id/x/y/extra)
	Raw       map[string]any // the original node, for materialisation
	Ambiguous bool           // true when the key collides (a genuine duplicate title)
}

// mergeNode is one node's classification (matched across versions by Key) and
// the material for the report.
type mergeNode struct {
	Key     string
	Title   string // display title (may be empty — use nodeLabel for output)
	ObjType int
	Class   nodeClass
	Detail  string // short human hint of what changed ("JS changed", "routing changed", "new node", ...)
	base    *nodeCanon
	theirs  *nodeCanon
	mine    *nodeCanon
}

// matchKeys returns, for each node in scheme order, the key used to match it
// across base/theirs/mine. Titled nodes key by title. Untitled nodes (a common
// shape for Start events and error finals) key by obj_type + their ordinal
// among untitled nodes of that type, so two untitled nodes are matched 1:1 by
// position instead of colliding on one empty-string key (which would flag every
// one of them as a false conflict). Node ids are unusable — the server
// regenerates them on every push.
func matchKeys(nodes []map[string]any) []string {
	keys := make([]string, len(nodes))
	ord := map[int]int{}
	for i, n := range nodes {
		if title, _ := n["title"].(string); title != "" {
			keys[i] = "t:" + title
		} else {
			ot := toInt(n["obj_type"])
			keys[i] = fmt.Sprintf("u:%d:%d", ot, ord[ot])
			ord[ot]++
		}
	}
	return keys
}

// nodeLabel renders a node for the human report: its quoted title, or a
// readable placeholder for an untitled node.
func nodeLabel(mn mergeNode) string {
	if mn.Title != "" {
		return quote(mn.Title)
	}
	return "(untitled " + objTypeName(mn.ObjType) + ")"
}

func objTypeName(ot int) string {
	switch ot {
	case 1:
		return "start"
	case 2:
		return "end"
	case 3:
		return "escalation"
	default:
		return "node"
	}
}

// mergePlan is the full reconciliation.
type mergePlan struct {
	Nodes     []mergeNode // every reconciled title, sorted
	Yours     []mergeNode // nodes only I changed/added/removed — what this push commits
	Grafts    []mergeNode // theirs-only changes safe to apply (edits, adds, deletes)
	Conflicts []mergeNode // nodes both sides changed — overlap needing a human
}

// buildMergePlan classifies every node across the three schemes.
func buildMergePlan(baseNodes, theirsNodes, mineNodes []map[string]any) mergePlan {
	base := canonicalizeNodes(baseNodes)
	theirs := canonicalizeNodes(theirsNodes)
	mine := canonicalizeNodes(mineNodes)

	keys := map[string]bool{}
	for k := range base {
		keys[k] = true
	}
	for k := range theirs {
		keys[k] = true
	}
	for k := range mine {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var plan mergePlan
	for _, key := range sorted {
		b, hasB := base[key]
		t, hasT := theirs[key]
		m, hasM := mine[key]
		mn := mergeNode{Key: key}
		// Display title / obj_type from whichever version has the node.
		for _, c := range []struct {
			ok bool
			nc nodeCanon
		}{{hasM, m}, {hasT, t}, {hasB, b}} {
			if c.ok {
				mn.Title = c.nc.Title
				mn.ObjType = c.nc.ObjType
				break
			}
		}
		if hasB {
			bb := b
			mn.base = &bb
		}
		if hasT {
			tt := t
			mn.theirs = &tt
		}
		if hasM {
			mm := m
			mn.mine = &mm
		}
		classify(&mn, hasB, hasT, hasM, b, t, m)
		plan.Nodes = append(plan.Nodes, mn)
		switch mn.Class {
		case clsMine, clsAddedMine, clsDeletedMine:
			plan.Yours = append(plan.Yours, mn)
		case clsTheirs, clsAddedTheirs, clsDeletedTheirs:
			plan.Grafts = append(plan.Grafts, mn)
		case clsConflict, clsAddedConflict, clsDeleteEditConflict:
			plan.Conflicts = append(plan.Conflicts, mn)
		}
	}
	return plan
}

// classify fills Class and Detail for one node following 3-way merge semantics.
// An ambiguous (duplicate-title) key on any side that differs is treated as a
// conflict — a wrong match must never silently corrupt logic.
func classify(mn *mergeNode, hasB, hasT, hasM bool, b, t, m nodeCanon) {
	ambiguous := (hasB && b.Ambiguous) || (hasT && t.Ambiguous) || (hasM && m.Ambiguous)

	switch {
	case hasB && hasT && hasM: // present everywhere
		bt := b.Body == t.Body
		bm := b.Body == m.Body
		tm := t.Body == m.Body
		switch {
		case bt && bm:
			mn.Class, mn.Detail = clsUnchanged, ""
		case !bt && bm:
			if ambiguous {
				mn.Class, mn.Detail = clsConflict, "duplicate title — cannot merge safely"
				return
			}
			mn.Class, mn.Detail = clsTheirs, describeChange(&b, &t)
		case bt && !bm:
			mn.Class, mn.Detail = clsMine, describeChange(&b, &m)
		default: // both changed
			if tm {
				mn.Class, mn.Detail = clsUnchanged, "" // same change on both sides
			} else if ambiguous {
				mn.Class, mn.Detail = clsConflict, "duplicate title — cannot merge safely"
			} else {
				mn.Class, mn.Detail = clsConflict, describeChange(&t, &m)
			}
		}
	case !hasB && hasT && hasM: // added on both sides
		if t.Body == m.Body {
			mn.Class, mn.Detail = clsAddedMine, ""
		} else {
			mn.Class, mn.Detail = clsAddedConflict, "both added a node with this title"
		}
	case !hasB && hasT && !hasM: // added only on the server
		mn.Class, mn.Detail = clsAddedTheirs, "new node"
	case !hasB && !hasT && hasM: // added only by me
		mn.Class, mn.Detail = clsAddedMine, "new node"
	case hasB && !hasT && hasM: // gone on the server
		if b.Body == m.Body {
			mn.Class, mn.Detail = clsDeletedTheirs, "removed on server"
		} else {
			mn.Class, mn.Detail = clsDeleteEditConflict, "you edited it; server deleted it"
		}
	case hasB && hasT && !hasM: // gone locally
		if b.Body == t.Body {
			mn.Class, mn.Detail = clsDeletedMine, "removed locally"
		} else {
			mn.Class, mn.Detail = clsDeleteEditConflict, "you deleted it; server edited it"
		}
	default: // hasB only, or none — gone on both sides
		mn.Class, mn.Detail = clsUnchanged, ""
	}
}

// describeChange names, in one phrase, what differs between two node versions —
// used only for the human report, never for the merge decision.
func describeChange(a, b *nodeCanon) string {
	if codeOf(a.Raw) != codeOf(b.Raw) {
		return "code/JS changed"
	}
	if routingOf(a.Raw) != routingOf(b.Raw) {
		return "routing changed"
	}
	if optionsOf(a.Raw) != optionsOf(b.Raw) {
		return "options changed"
	}
	if strings.TrimSpace(fmt.Sprint(a.Raw["description"])) != strings.TrimSpace(fmt.Sprint(b.Raw["description"])) {
		return "description changed"
	}
	return "node changed"
}

// canonicalizeNodes builds a matchKey→canon map. A key used by more than one
// node in the same scheme (only a genuine duplicate title can collide now) is
// flagged Ambiguous so the classifier refuses to merge it.
func canonicalizeNodes(nodes []map[string]any) map[string]nodeCanon {
	keys := matchKeys(nodes)
	idToKey := map[string]string{}
	for i, n := range nodes {
		if id, _ := n["id"].(string); id != "" {
			idToKey[id] = keys[i]
		}
	}
	out := map[string]nodeCanon{}
	counts := map[string]int{}
	for i, n := range nodes {
		key := keys[i]
		counts[key]++
		if _, exists := out[key]; exists {
			continue // keep the first occurrence's body; duplicates flagged below
		}
		title, _ := n["title"].(string)
		out[key] = nodeCanon{
			Key:     key,
			Title:   title,
			ObjType: toInt(n["obj_type"]),
			Body:    canonNodeBody(n, idToKey),
			Raw:     n,
		}
	}
	for key, cnt := range counts {
		if cnt > 1 {
			c := out[key]
			c.Ambiguous = true
			out[key] = c
		}
	}
	return out
}

// linkFields are the node-reference fields inside a logic entry; semLinkFields
// the same inside a semaphor. Values are rewritten to the target node's match
// key so a link is comparable across id regeneration.
var linkFields = []string{"to_node_id", "err_node_id", "go_to", "goto"}
var semLinkFields = []string{"to_node_id", "esc_node_id"}

// canonNodeBody renders a node's semantic content as canonical JSON: id/x/y and
// UI-only extra are dropped, options is parsed so formatting doesn't matter, and
// every link id is replaced by its target's match key. encoding/json sorts map
// keys, so equal content yields an identical string.
func canonNodeBody(node map[string]any, idToKey map[string]string) string {
	c := map[string]any{
		"obj_type":    toInt(node["obj_type"]),
		"title":       node["title"],
		"description": node["description"],
	}
	if opt := optionsOf(node); opt != "" {
		c["options"] = opt
	}
	if cond, ok := node["condition"].(map[string]any); ok {
		nc := map[string]any{}
		if logics, ok := cond["logics"].([]any); ok {
			nc["logics"] = canonList(logics, linkFields, idToKey)
		}
		if sems, ok := cond["semaphors"].([]any); ok {
			nc["semaphors"] = canonList(sems, semLinkFields, idToKey)
		}
		c["condition"] = nc
	}
	b, _ := json.Marshal(c)
	return string(b)
}

// canonList copies each entry of a logic/semaphor list, rewriting the given link
// fields from a node id to "@<target key>" (or "@?<id>" when the target is unknown).
func canonList(list []any, fields []string, idToKey map[string]string) []any {
	out := make([]any, 0, len(list))
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			out = append(out, e)
			continue
		}
		cp := map[string]any{}
		for k, val := range m {
			cp[k] = val
		}
		for _, f := range fields {
			if id, ok := cp[f].(string); ok && id != "" {
				if key, known := idToKey[id]; known {
					cp[f] = "@" + key
				} else {
					cp[f] = "@?" + id
				}
			}
		}
		out = append(out, cp)
	}
	return out
}

// codeOf returns the JS/source of a node's first api_code logic ("" if none).
func codeOf(node map[string]any) string {
	cond, _ := node["condition"].(map[string]any)
	logics, _ := cond["logics"].([]any)
	for _, e := range logics {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "api_code" {
			continue
		}
		if src, ok := m["src"].(string); ok && src != "" {
			return src
		}
		if src, ok := m["code"].(string); ok {
			return src
		}
	}
	return ""
}

// routingOf returns a stable string of a node's outgoing links (by nothing but
// their raw ids — used only for change description, not identity).
func routingOf(node map[string]any) string {
	cond, _ := node["condition"].(map[string]any)
	var parts []string
	if logics, ok := cond["logics"].([]any); ok {
		for _, e := range logics {
			if m, ok := e.(map[string]any); ok {
				for _, f := range linkFields {
					if s, ok := m[f].(string); ok && s != "" {
						parts = append(parts, f+"="+s)
					}
				}
			}
		}
	}
	if sems, ok := cond["semaphors"].([]any); ok {
		for _, e := range sems {
			if m, ok := e.(map[string]any); ok {
				for _, f := range semLinkFields {
					if s, ok := m[f].(string); ok && s != "" {
						parts = append(parts, f+"="+s)
					}
				}
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// optionsOf parses a node's options (a JSON string or object) into canonical
// JSON so formatting differences don't read as changes ("" when absent/null).
func optionsOf(node map[string]any) string {
	raw, ok := node["options"]
	if !ok || raw == nil {
		return ""
	}
	var parsed any
	switch t := raw.(type) {
	case string:
		if t == "" {
			return ""
		}
		if json.Unmarshal([]byte(t), &parsed) != nil {
			return t // not JSON — compare literally
		}
	default:
		parsed = raw
	}
	b, _ := json.Marshal(parsed)
	return string(b)
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// placeholderID derives a stable 24-hex id for a grafted-new node from its
// title. The server reassigns every id on push, so any unique placeholder is
// fine; deriving it from the title keeps merges deterministic.
func placeholderID(title string) string {
	sum := sha1.Sum([]byte("merge:" + title))
	return hex.EncodeToString(sum[:])[:24]
}
