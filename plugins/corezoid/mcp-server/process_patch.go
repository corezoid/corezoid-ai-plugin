package main

// patch-process: point edits of a process scheme. A model that changes one
// node of a 60-node process used to resend the whole JSON (push-process
// content, hosted push) and get the whole deployed scheme back; with
// patch-process it names the nodes it changes and gets back what changed.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// nodePatchOp is one edit: set {node, set, unset}, add {value}, remove {node}.
type nodePatchOp struct {
	Op    string
	Node  string                 // a node id, or a title no other node has
	Set   map[string]interface{} // node keys to replace; logics/semaphors land in condition
	Unset []string
	Value map[string]interface{} // the whole new node of add
}

type processPatch struct {
	ops     []nodePatchOp
	process map[string]interface{} // process keys to replace: title, description, params
}

var (
	patchNodeKeys    = []string{"title", "description", "condition", "logics", "semaphors", "x", "y", "extra", "options", "obj_type"}
	patchProcessKeys = []string{"title", "description", "params"}
	patchOps         = []string{"set", "add", "remove"}
)

const maxPatchOps = 100

// parseProcessPatch reads ops and process from tool arguments and checks
// their shape, so a malformed patch is refused before anything is read.
func parseProcessPatch(args map[string]interface{}) (processPatch, error) {
	var p processPatch
	rawOps, _ := args["ops"].([]interface{})
	if proc, ok := args["process"].(map[string]interface{}); ok {
		for k := range proc {
			if !containsStr(patchProcessKeys, k) {
				return p, fmt.Errorf("process.%s cannot be patched; allowed: %s", k, strings.Join(patchProcessKeys, ", "))
			}
		}
		p.process = proc
	} else if args["process"] != nil {
		return p, fmt.Errorf("process must be an object of process keys (%s)", strings.Join(patchProcessKeys, ", "))
	}
	if len(rawOps) == 0 && len(p.process) == 0 {
		return p, fmt.Errorf("ops (1..%d node edits) or process is required", maxPatchOps)
	}
	if len(rawOps) > maxPatchOps {
		return p, fmt.Errorf("at most %d ops per call, got %d", maxPatchOps, len(rawOps))
	}
	for i, raw := range rawOps {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return p, fmt.Errorf("ops[%d] must be an object", i)
		}
		op := nodePatchOp{}
		op.Op, _ = m["op"].(string)
		op.Node, _ = m["node"].(string)
		op.Set, _ = m["set"].(map[string]interface{})
		op.Value, _ = m["value"].(map[string]interface{})
		if u, ok := m["unset"].([]interface{}); ok {
			for _, k := range u {
				s, _ := k.(string)
				op.Unset = append(op.Unset, s)
			}
		}
		if err := op.check(); err != nil {
			return p, fmt.Errorf("ops[%d] (%s): %v", i, op.Op, err)
		}
		p.ops = append(p.ops, op)
	}
	return p, nil
}

func (op nodePatchOp) check() error {
	switch op.Op {
	case "set":
		if op.Node == "" || len(op.Set)+len(op.Unset) == 0 {
			return fmt.Errorf("set needs node (an id or title) and set or unset")
		}
		for k := range op.Set {
			if !containsStr(patchNodeKeys, k) {
				return fmt.Errorf("set.%s is not a node key you can change; allowed: %s (id is the server's)", k, strings.Join(patchNodeKeys, ", "))
			}
		}
		// condition holds logics and semaphors: with both in one set the result
		// would hang on map order, condition dropping the list or the list
		// landing in the condition that is then replaced.
		if _, ok := op.Set["condition"]; ok {
			for _, k := range []string{"logics", "semaphors"} {
				if _, both := op.Set[k]; both {
					return fmt.Errorf("set.condition and set.%s in one set: condition replaces logics and semaphors together — send condition alone, or logics/semaphors alone", k)
				}
			}
		}
		for _, k := range op.Unset {
			if k != "description" && k != "extra" && k != "options" {
				return fmt.Errorf("unset.%s refused: only description, extra and options may be dropped", k)
			}
		}
	case "add":
		if op.Value == nil {
			return fmt.Errorf("add needs value: the whole node {title, obj_type, condition: {logics, semaphors}, ...}")
		}
		if _, ok := op.Value["title"].(string); !ok {
			return fmt.Errorf("value.title (string) is required")
		}
		if _, ok := op.Value["obj_type"]; !ok {
			return fmt.Errorf("value.obj_type is required: 0 code/api, 1 start, 2 end, 3 condition")
		}
	case "remove":
		if op.Node == "" {
			return fmt.Errorf("remove needs node (an id or title)")
		}
	default:
		return fmt.Errorf("unknown op %q; allowed: %s", op.Op, strings.Join(patchOps, ", "))
	}
	return nil
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// schemeNodes returns doc.scheme.nodes as objects.
func schemeNodes(doc map[string]interface{}) ([]map[string]interface{}, error) {
	scheme, _ := doc["scheme"].(map[string]interface{})
	raw, ok := scheme["nodes"].([]interface{})
	if scheme == nil || (!ok && scheme["nodes"] != nil) {
		return nil, fmt.Errorf("the process has no scheme.nodes list")
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for i, n := range raw {
		m, ok := n.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("scheme.nodes[%d] is not an object", i)
		}
		out = append(out, m)
	}
	return out, nil
}

func setSchemeNodes(doc map[string]interface{}, nodes []map[string]interface{}) {
	scheme, _ := doc["scheme"].(map[string]interface{})
	list := make([]interface{}, len(nodes))
	for i, n := range nodes {
		list[i] = n
	}
	scheme["nodes"] = list
}

// findNode resolves a node reference: an id first, else a unique title.
func findNode(nodes []map[string]interface{}, ref string) (int, error) {
	var byTitle []int
	for i, n := range nodes {
		if id, _ := n["id"].(string); id == ref {
			return i, nil
		}
		if t, _ := n["title"].(string); t == ref {
			byTitle = append(byTitle, i)
		}
	}
	switch len(byTitle) {
	case 1:
		return byTitle[0], nil
	case 0:
		return -1, fmt.Errorf("no node has id or title %q; nodes: %s", ref, nodeList(nodes))
	}
	ids := make([]string, len(byTitle))
	for i, k := range byTitle {
		ids[i], _ = nodes[k]["id"].(string)
	}
	return -1, fmt.Errorf("%d nodes are titled %q (%s): name the node by its id", len(byTitle), ref, strings.Join(ids, ", "))
}

func nodeList(nodes []map[string]interface{}) string {
	var parts []string
	for i, n := range nodes {
		if i == 40 {
			parts = append(parts, fmt.Sprintf("… (%d in all)", len(nodes)))
			break
		}
		id, _ := n["id"].(string)
		t, _ := n["title"].(string)
		parts = append(parts, fmt.Sprintf("%s %q", id, t))
	}
	return strings.Join(parts, ", ")
}

// patchResult is what a patch touched: ids of the nodes it changed (the
// server keeps them on deploy), ids it gave the nodes it added (the server
// replaces them), and how many it removed.
type patchResult struct {
	changed, added []string
	removed        int
}

func (r patchResult) all() []string { return append(append([]string{}, r.changed...), r.added...) }

// applyProcessPatch applies the ops in order, then refuses a scheme where a
// logic or semaphore points at a node that is not there. On an error doc may
// be partly changed: callers drop it, so nothing is written or deployed.
func applyProcessPatch(doc map[string]interface{}, p processPatch) (patchResult, error) {
	for k, v := range p.process {
		doc[k] = v
	}
	var r patchResult
	nodes, err := schemeNodes(doc)
	if err != nil {
		if len(p.ops) == 0 {
			return r, nil
		}
		return r, err
	}
	before := map[string]string{}
	for _, n := range nodes {
		if id, _ := n["id"].(string); id != "" {
			before[id] = nodeKindOf(n)
		}
	}
	for i, op := range p.ops {
		ref, err := applyNodeOp(&nodes, op)
		if err != nil {
			return patchResult{}, fmt.Errorf("ops[%d] (%s): %v; nothing was changed", i, op.Op, err)
		}
		switch {
		case op.Op == "remove":
			r.removed++
			r.changed = without(r.changed, ref)
			r.added = without(r.added, ref)
		case op.Op == "add":
			r.added = append(r.added, ref)
		case !containsStr(r.changed, ref) && !containsStr(r.added, ref):
			r.changed = append(r.changed, ref)
		}
	}
	if err := checkNodeKinds(before, nodes); err != nil {
		return patchResult{}, fmt.Errorf("%v; nothing was changed", err)
	}
	if err := checkNodeLinks(nodes); err != nil {
		return patchResult{}, fmt.Errorf("%v; nothing was changed", err)
	}
	setSchemeNodes(doc, nodes)
	return r, nil
}

func without(list []string, v string) []string {
	out := list[:0]
	for _, s := range list {
		if s != v {
			out = append(out, s)
		}
	}
	return out
}

func applyNodeOp(nodes *[]map[string]interface{}, op nodePatchOp) (string, error) {
	switch op.Op {
	case "remove":
		i, err := findNode(*nodes, op.Node)
		if err != nil {
			return "", err
		}
		id, _ := (*nodes)[i]["id"].(string)
		*nodes = append((*nodes)[:i], (*nodes)[i+1:]...)
		return id, nil
	case "add":
		n := map[string]interface{}{}
		for k, v := range op.Value {
			n[k] = v
		}
		id, _ := n["id"].(string)
		if id == "" {
			id = newNodeID()
			n["id"] = id
		}
		if _, err := findNode(*nodes, id); err == nil {
			return "", fmt.Errorf("a node with id %s already exists", id)
		}
		if _, ok := n["condition"]; !ok {
			n["condition"] = map[string]interface{}{"logics": []interface{}{}, "semaphors": []interface{}{}}
		}
		*nodes = append(*nodes, n)
		return id, nil
	}
	i, err := findNode(*nodes, op.Node)
	if err != nil {
		return "", err
	}
	n := (*nodes)[i]
	for k, v := range op.Set {
		if k == "logics" || k == "semaphors" {
			cond, _ := n["condition"].(map[string]interface{})
			if cond == nil {
				cond = map[string]interface{}{}
				n["condition"] = cond
			}
			cond[k] = v
			continue
		}
		n[k] = v
	}
	for _, k := range op.Unset {
		delete(n, k)
	}
	id, _ := n["id"].(string)
	return id, nil
}

// linkFields are the transition fields of a logic and of a semaphore, as
// lint's findBrokenLinks reads them.
var nodeLinkFields = map[string][]string{
	"logics":    {"to_node_id", "err_node_id", "go_to", "goto"},
	"semaphors": {"to_node_id", "esc_node_id"},
}

// checkNodeLinks names the first logic or semaphore whose static target node
// is missing — what lint blocks at push, said where the patch made it. Dynamic
// targets ({{...}}, @alias) resolve at deploy and are left alone, as in lint.
func checkNodeLinks(nodes []map[string]interface{}) error {
	ids := map[string]bool{}
	for _, n := range nodes {
		if id, _ := n["id"].(string); id != "" {
			ids[id] = true
		}
	}
	for _, n := range nodes {
		cond, _ := n["condition"].(map[string]interface{})
		for _, part := range []string{"logics", "semaphors"} {
			items, _ := cond[part].([]interface{})
			for j, it := range items {
				m, _ := it.(map[string]interface{})
				for _, key := range nodeLinkFields[part] {
					if target, static := isStaticNodeID(m[key]); static && !ids[target] {
						id, _ := n["id"].(string)
						title, _ := n["title"].(string)
						return fmt.Errorf("node %s %q %s[%d].%s points to %s, which is not in the process: repoint it in the same patch", id, title, part, j, key, target)
					}
				}
			}
		}
	}
	return nil
}

// nodeKindOf is what a node is: its obj_type (Stub Mode 4 counts as 0 — it is
// switched on and off) and, from its logics, condition (go_if_const) or action
// (any other non-go logic). A node with only go, or none, has no shape of its
// own, so only its obj_type is compared.
func nodeKindOf(n map[string]interface{}) string {
	objType := fmt.Sprint(n["obj_type"])
	if objType == "4" {
		objType = "0"
	}
	shape := ""
	cond, _ := n["condition"].(map[string]interface{})
	logics, _ := cond["logics"].([]interface{})
	for _, it := range logics {
		m, _ := it.(map[string]interface{})
		switch t, _ := m["type"].(string); t {
		case "", "go":
		case "go_if_const":
			shape = "condition"
		default:
			if shape == "" {
				shape = "action"
			}
		}
	}
	return objType + "/" + shape
}

// checkNodeKinds refuses a node that keeps its id but changes kind — set on an
// action that makes it a condition, or remove and add again under the same id.
// The process runs, but a later stage merge fails with "referenced node …
// does not exist" (corezoid-edit, Common mistakes).
func checkNodeKinds(before map[string]string, nodes []map[string]interface{}) error {
	for _, n := range nodes {
		id, _ := n["id"].(string)
		was, ok := before[id]
		if !ok {
			continue
		}
		now := nodeKindOf(n)
		if kindsDiffer(was, now) {
			title, _ := n["title"].(string)
			return fmt.Errorf("node %s %q changes kind (%s → %s) while keeping its id: it pushes and runs, but a later stage merge fails with \"referenced node … does not exist\". Remove it and add the new node without an id (one is made), and repoint every logic that led to it in the same patch", id, title, kindLabel(was), kindLabel(now))
		}
	}
	return nil
}

func kindsDiffer(was, now string) bool {
	wt, ws, _ := strings.Cut(was, "/")
	nt, ns, _ := strings.Cut(now, "/")
	return wt != nt || (ws != "" && ns != "" && ws != ns)
}

func kindLabel(k string) string {
	t, shape, _ := strings.Cut(k, "/")
	label := "obj_type " + t
	if shape != "" {
		label += " " + shape
	}
	return label
}

// newNodeID is a 24-hex id like Corezoid's own; the server assigns its own on deploy.
func newNodeID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// nodeOutline is a node in a line: what it is and where it goes, not its logic bodies.
type nodeOutline struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Kind   string   `json:"kind"`
	Logics []string `json:"logics,omitempty"`
	To     []string `json:"to,omitempty"`
	Err    []string `json:"err,omitempty"`
	Esc    []string `json:"esc,omitempty"`
}

var nodeKinds = map[string]string{"0": "node", "1": "start", "2": "end", "3": "condition"}

func outlineOf(n map[string]interface{}) nodeOutline {
	o := nodeOutline{}
	o.ID, _ = n["id"].(string)
	o.Title, _ = n["title"].(string)
	o.Kind = nodeKinds[fmt.Sprint(n["obj_type"])]
	if o.Kind == "" {
		o.Kind = fmt.Sprint(n["obj_type"])
	}
	cond, _ := n["condition"].(map[string]interface{})
	for _, part := range []string{"logics", "semaphors"} {
		items, _ := cond[part].([]interface{})
		for _, it := range items {
			m, _ := it.(map[string]interface{})
			if t, _ := m["type"].(string); t != "" && part == "logics" {
				o.Logics = append(o.Logics, t)
			}
			for _, k := range []string{"to_node_id", "go_to", "goto"} {
				if t, _ := m[k].(string); t != "" {
					o.To = append(o.To, t)
				}
			}
			if t, _ := m["err_node_id"].(string); t != "" {
				o.Err = append(o.Err, t)
			}
			if t, _ := m["esc_node_id"].(string); t != "" {
				o.Esc = append(o.Esc, t)
			}
		}
	}
	return o
}

// processOutline lists every node (or, with only, the named ones) one per line.
func processOutline(doc map[string]interface{}, only []string) string {
	nodes, _ := schemeNodes(doc)
	want := map[string]bool{}
	for _, r := range only {
		want[r] = true
	}
	var b strings.Builder
	for _, n := range nodes {
		o := outlineOf(n)
		if len(only) > 0 && !want[o.ID] && !want[o.Title] {
			continue
		}
		line, _ := json.Marshal(o)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// selectNodes returns the full JSON of the named nodes, refusing a name that
// matches none (so a typo does not read as "the node is empty").
func selectNodes(doc map[string]interface{}, refs []string) (string, error) {
	nodes, err := schemeNodes(doc)
	if err != nil {
		return "", err
	}
	var picked []map[string]interface{}
	seen := map[int]bool{}
	for _, r := range refs {
		i, err := findNode(nodes, r)
		if err != nil {
			return "", err
		}
		if !seen[i] {
			seen[i] = true
			picked = append(picked, nodes[i])
		}
	}
	data, err := json.MarshalIndent(picked, "", "  ")
	return string(data), err
}

// handlePatchProcess edits a pulled process file in place; push-process deploys it.
func handlePatchProcess(_ context.Context, args map[string]interface{}) (string, bool) {
	patch, err := parseProcessPatch(args)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	if !strings.HasSuffix(filePath, ".conv.json") {
		return fmt.Sprintf("Error: process_path must name a .conv.json file (got %q); patch-process edits process files only", filePath), true
	}
	if err := processFileMissingError(filePath); err != nil {
		return "Error: " + err.Error(), true
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	doc, err := decodeProcessDoc(raw)
	if err != nil {
		return fmt.Sprintf("Error: %s is not a process JSON object: %v", filePath, err), true
	}
	res, err := applyProcessPatch(doc, patch)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	if err := os.WriteFile(filePath, out, 0644); err != nil {
		return "Error writing " + filePath + ": " + err.Error(), true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Patched %s: %d op(s)%s", filePath, len(patch.ops), processKeysNote(patch))
	if res.removed > 0 {
		fmt.Fprintf(&b, ", %d node(s) removed", res.removed)
	}
	b.WriteString(". Not deployed — run push-process on it.\n")
	if all := res.all(); len(all) > 0 {
		b.WriteString("Changed and added nodes:\n")
		b.WriteString(processOutline(doc, all))
	}
	return b.String(), false
}

func processKeysNote(p processPatch) string {
	if len(p.process) == 0 {
		return ""
	}
	keys := make([]string, 0, len(p.process))
	for k := range p.process {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return " and process " + strings.Join(keys, ", ")
}

// decodeProcessDoc keeps numbers exact (ids and limits beyond 2^53).
func decodeProcessDoc(raw []byte) (map[string]interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]interface{}
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("empty document")
	}
	return doc, nil
}
