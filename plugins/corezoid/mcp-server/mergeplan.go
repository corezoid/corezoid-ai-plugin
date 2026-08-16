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
	Key       string
	Title     string // display title (may be empty — use nodeLabel for output)
	ObjType   int
	Class     nodeClass
	Detail    string // short human hint of what changed ("JS changed", "routing changed", "new node", ...)
	Ambiguous bool   // duplicate non-empty title on at least one side
	base      *nodeCanon
	theirs    *nodeCanon
	mine      *nodeCanon
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
	Nodes          []mergeNode  // every reconciled title, sorted
	Yours          []mergeNode  // nodes only I changed/added/removed — what this push commits
	Grafts         []mergeNode  // theirs-only changes safe to apply (edits, adds, deletes)
	Conflicts      []mergeNode  // nodes both sides changed — overlap needing a human
	FieldYours     []mergeField // process/scheme fields changed only locally
	FieldGrafts    []mergeField // process/scheme fields changed only on the server
	FieldConflicts []mergeField // process/scheme fields changed differently on both sides
}

type mergeFieldClass int

const (
	fieldYours mergeFieldClass = iota
	fieldGraft
	fieldConflict
)

type mergeFieldValue struct {
	Present bool
	Body    string
	Raw     any
}

// mergeField treats each process-level field (and each non-node scheme field)
// as one atomic value. This is deliberately conservative: concurrent edits to
// different members of the same params/web_settings value are reported as an
// overlap rather than guessed into a potentially invalid process contract.
type mergeField struct {
	Path   string
	Class  mergeFieldClass
	Detail string
	base   mergeFieldValue
	theirs mergeFieldValue
	mine   mergeFieldValue
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
		mn.Ambiguous = (hasB && b.Ambiguous) || (hasT && t.Ambiguous) || (hasM && m.Ambiguous)
		classify(&mn, hasB, hasT, hasM, b, t, m)
		plan.Nodes = append(plan.Nodes, mn)
	}
	promoteReferenceConflicts(&plan, theirsNodes, mineNodes)
	rebuildMergeBuckets(&plan)
	return plan
}

// promoteReferenceConflicts turns a one-sided node deletion into a
// delete-edit conflict when a surviving node on the OTHER side still links to
// the deleted target. Without this, materializeMerge would drop the target and
// the merged graph would carry a static link to a nonexistent node — a push
// that fails the server's reference check or silently deploys a wrong graph.
//
//   - Scenario A: theirs deletes Y and a node whose merged body comes from
//     mine still links to Y's mine-side id → clsDeletedTheirs is promoted so
//     mine's Y stays in the merge and the user resolves the reference by hand.
//   - Scenario B: mine deletes Y and a node whose merged body comes from
//     theirs (grafted) still links to Y's theirs-side id → clsDeletedMine is
//     promoted so theirs's referrer doesn't graft over a hole.
//
// A node's "effective side" in the merge is determined by its class: clsTheirs
// and clsAddedTheirs bodies come from theirs; everything else that survives
// (mine, unchanged, conflict, added-mine, added-conflict) comes from mine. A
// node that is being dropped from the merge contributes no surviving refs.
func promoteReferenceConflicts(plan *mergePlan, theirsNodes, mineNodes []map[string]any) {
	classByKey := make(map[string]nodeClass, len(plan.Nodes))
	for _, mn := range plan.Nodes {
		classByKey[mn.Key] = mn.Class
	}

	mineKeys := matchKeys(mineNodes)
	theirsKeys := matchKeys(theirsNodes)
	// Resolve a raw link target id (as it appears in a node body) to the
	// merge key it points at. Base/mine/theirs share ids for pre-existing
	// nodes, so a link surviving from either side identifies the same key.
	mineIDToKey := make(map[string]string, len(mineNodes))
	theirsIDToKey := make(map[string]string, len(theirsNodes))
	for i, n := range mineNodes {
		if id, _ := n["id"].(string); id != "" {
			mineIDToKey[id] = mineKeys[i]
		}
	}
	for i, n := range theirsNodes {
		if id, _ := n["id"].(string); id != "" {
			theirsIDToKey[id] = theirsKeys[i]
		}
	}
	resolveKey := func(id string) (string, bool) {
		if k, ok := mineIDToKey[id]; ok {
			return k, true
		}
		if k, ok := theirsIDToKey[id]; ok {
			return k, true
		}
		return "", false
	}

	// Any delete anywhere is a candidate for promotion.
	haveDeletes := false
	for _, mn := range plan.Nodes {
		if mn.Class == clsDeletedTheirs || mn.Class == clsDeletedMine {
			haveDeletes = true
			break
		}
	}
	if !haveDeletes {
		return
	}

	danglingA := map[string]bool{} // clsDeletedTheirs keys still referenced
	danglingB := map[string]bool{} // clsDeletedMine keys still referenced
	record := func(k string) {
		switch classByKey[k] {
		case clsDeletedTheirs:
			danglingA[k] = true
		case clsDeletedMine:
			danglingB[k] = true
		}
	}
	for i, n := range mineNodes {
		if !bodyComesFromMine(classByKey[mineKeys[i]]) {
			continue
		}
		for _, tgt := range collectLinkTargets(n, nil) {
			if k, ok := resolveKey(tgt); ok {
				record(k)
			}
		}
	}
	for i, n := range theirsNodes {
		if !bodyComesFromTheirs(classByKey[theirsKeys[i]]) {
			continue
		}
		for _, tgt := range collectLinkTargets(n, nil) {
			if k, ok := resolveKey(tgt); ok {
				record(k)
			}
		}
	}
	if len(danglingA) == 0 && len(danglingB) == 0 {
		return
	}

	for i := range plan.Nodes {
		mn := &plan.Nodes[i]
		switch {
		case mn.Class == clsDeletedTheirs && danglingA[mn.Key]:
			mn.Class = clsDeleteEditConflict
			mn.Detail = "server deleted this node; a link in the merged scheme still points at it"
		case mn.Class == clsDeletedMine && danglingB[mn.Key]:
			mn.Class = clsDeleteEditConflict
			mn.Detail = "you deleted this node; a link in the merged scheme still points at it"
		}
	}
}

// bodyComesFromMine reports whether a node with this class contributes its
// mine-side links to the final merge. clsTheirs and clsAddedTheirs come from
// theirs; the drop classes contribute nothing.
func bodyComesFromMine(c nodeClass) bool {
	switch c {
	case clsUnchanged, clsMine, clsConflict, clsAddedMine, clsAddedConflict, clsDeleteEditConflict:
		return true
	}
	return false
}

// bodyComesFromTheirs reports whether a theirs-side node contributes its
// theirs-side links to the final merge (grafted server nodes).
func bodyComesFromTheirs(c nodeClass) bool {
	return c == clsTheirs || c == clsAddedTheirs
}

// collectLinkTargets appends every static node-id reference in a node's
// logics/semaphors to out and returns the extended slice.
func collectLinkTargets(node map[string]any, out []string) []string {
	cond, ok := node["condition"].(map[string]any)
	if !ok {
		return out
	}
	out = appendListTargets(cond["logics"], linkFields, out)
	out = appendListTargets(cond["semaphors"], semLinkFields, out)
	return out
}

func appendListTargets(list any, fields []string, out []string) []string {
	arr, ok := list.([]any)
	if !ok {
		return out
	}
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		for _, f := range fields {
			tgt, ok := m[f].(string)
			if !ok || tgt == "" {
				continue
			}
			out = append(out, tgt)
		}
	}
	return out
}

// rebuildMergeBuckets recomputes Yours/Grafts/Conflicts from plan.Nodes. Used
// after promoteReferenceConflicts reclassifies nodes so the buckets reflect
// the final classification.
func rebuildMergeBuckets(plan *mergePlan) {
	plan.Yours = plan.Yours[:0]
	plan.Grafts = plan.Grafts[:0]
	plan.Conflicts = plan.Conflicts[:0]
	for _, mn := range plan.Nodes {
		switch mn.Class {
		case clsMine, clsAddedMine, clsDeletedMine:
			plan.Yours = append(plan.Yours, mn)
		case clsTheirs, clsAddedTheirs, clsDeletedTheirs:
			plan.Grafts = append(plan.Grafts, mn)
		case clsConflict, clsAddedConflict, clsDeleteEditConflict:
			plan.Conflicts = append(plan.Conflicts, mn)
		}
	}
}

var ignoredMergeRootFields = map[string]bool{
	"obj_id":                 true,
	"change_time":            true,
	"create_time":            true,
	"last_confirmed_version": true,
	"version":                true,
	"commits":                true,
	"uuid":                   true,
}

// addProcessFields extends a node merge plan with all process-level fields and
// non-node scheme fields. Identity/version metadata is intentionally excluded:
// those values describe the server object and are not deployable user edits.
func addProcessFields(plan *mergePlan, baseConv, theirsConv, mineConv string) error {
	base, err := processMergeFields(baseConv)
	if err != nil {
		return fmt.Errorf("parse merge ancestor fields: %w", err)
	}
	theirs, err := processMergeFields(theirsConv)
	if err != nil {
		return fmt.Errorf("parse server fields: %w", err)
	}
	mine, err := processMergeFields(mineConv)
	if err != nil {
		return fmt.Errorf("parse local fields: %w", err)
	}

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

	for _, path := range sorted {
		b := base[path]
		t := theirs[path]
		m := mine[path]
		bt := equalMergeFieldValue(b, t)
		bm := equalMergeFieldValue(b, m)
		tm := equalMergeFieldValue(t, m)
		if bt && bm {
			continue
		}
		mf := mergeField{Path: path, base: b, theirs: t, mine: m}
		switch {
		case !bt && bm:
			mf.Class, mf.Detail = fieldGraft, describeFieldChange(b, t)
			plan.FieldGrafts = append(plan.FieldGrafts, mf)
		case bt && !bm:
			mf.Class, mf.Detail = fieldYours, describeFieldChange(b, m)
			plan.FieldYours = append(plan.FieldYours, mf)
		case tm:
			// Both sides made the same edit. Mine already contains it, so no
			// graft is needed; list it as a local edit for an honest report.
			mf.Class, mf.Detail = fieldYours, describeFieldChange(b, m)+" (same on server)"
			plan.FieldYours = append(plan.FieldYours, mf)
		default:
			mf.Class, mf.Detail = fieldConflict, "changed differently on both sides"
			plan.FieldConflicts = append(plan.FieldConflicts, mf)
		}
	}
	return nil
}

func processMergeFields(conv string) (map[string]mergeFieldValue, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(conv), &doc); err != nil {
		return nil, err
	}
	out := map[string]mergeFieldValue{}
	for key, value := range doc {
		if key == "scheme" || ignoredMergeRootFields[key] {
			continue
		}
		out["process."+key] = newMergeFieldValue(value)
	}
	if scheme, ok := doc["scheme"].(map[string]any); ok {
		for key, value := range scheme {
			if key == "nodes" {
				continue
			}
			out["scheme."+key] = newMergeFieldValue(value)
		}
	}
	return out, nil
}

func newMergeFieldValue(raw any) mergeFieldValue {
	b, _ := json.Marshal(raw)
	return mergeFieldValue{Present: true, Body: string(b), Raw: raw}
}

func equalMergeFieldValue(a, b mergeFieldValue) bool {
	return a.Present == b.Present && (!a.Present || a.Body == b.Body)
}

func describeFieldChange(base, side mergeFieldValue) string {
	switch {
	case !base.Present && side.Present:
		return "added"
	case base.Present && !side.Present:
		return "removed"
	default:
		return "changed"
	}
}

func mergeConflictCount(plan mergePlan) int {
	return len(plan.Conflicts) + len(plan.FieldConflicts)
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
