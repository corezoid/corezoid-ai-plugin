package main

// Hosted patch-process and the compact hosted pull: a hosted caller has no
// files, so it used to read and resend the whole process JSON for every edit.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

var patchOpsSchema = map[string]interface{}{
	"type":        "array",
	"description": "Node edits, applied in order, all or none: {op: \"set\", node, set: {title|description|condition|logics|semaphors|x|y|extra|options|obj_type: value}, unset?: [description|extra|options]} (logics/semaphors replace that node's condition list); {op: \"add\", value: {title, obj_type, condition: {logics, semaphors}, ...}} (no id: one is made); {op: \"remove\", node}. node is a node id or a title no other node has. A logic or semaphore left pointing at a missing node is refused — repoint it in the same patch. So is a node that keeps its id but changes kind (action ↔ condition, obj_type): a later stage merge fails on it — remove it and add a new node.",
	"items": map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"op":    map[string]interface{}{"type": "string", "enum": patchOps},
			"node":  map[string]interface{}{"type": "string"},
			"set":   map[string]interface{}{"type": "object"},
			"unset": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"value": map[string]interface{}{"type": "object"},
		},
		"required": []string{"op"},
	},
}

var patchProcessSchema = map[string]interface{}{
	"type":        "object",
	"description": "Process keys to replace: title, description, params.",
}

// hostedPullView answers the compact pull: the outline, the named nodes, or both.
func hostedPullView(doc map[string]interface{}, args map[string]interface{}) (string, bool, error) {
	refs, err := stringList(args, "nodes")
	if err != nil {
		return "", false, err
	}
	outline := boolishArg(args, "outline")
	if !outline && len(refs) == 0 {
		return "", false, nil
	}
	var b strings.Builder
	if outline {
		nodes, _ := schemeNodes(doc)
		fmt.Fprintf(&b, "Outline, %d nodes (id, title, kind, logic types, to/err targets):\n", len(nodes))
		b.WriteString(processOutline(doc, nil))
	}
	if len(refs) > 0 {
		picked, err := selectNodes(doc, refs)
		if err != nil {
			return "", false, err
		}
		b.WriteString("\nNodes:\n")
		b.WriteString(picked)
		b.WriteString("\n")
	}
	return b.String(), true, nil
}

func stringList(args map[string]interface{}, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be a list of node ids or titles", key)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s must be a list of node ids or titles", key)
		}
		out = append(out, s)
	}
	return out, nil
}

// hostedPatchProcess applies the ops to the server's current scheme and
// deploys it through pushProcessCore, answering only what changed.
func hostedPatchProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	procID, err := intArg(args, "process_id")
	if err != nil || procID <= 0 {
		return "Error: process_id must be a positive integer", true
	}
	patch, err := parseProcessPatch(args)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	tok := optStrArg(args, "base")
	if tok == "" {
		return "Error: base is required — the token pull-process returned; it is what refuses a process someone changed since you read it", true
	}
	tokID, b, err := parseHostedBaseToken(tok)
	if err != nil {
		return "Error: " + err.Error() + " — pass the base token exactly as pull-process returned it", true
	}
	if tokID != procID {
		return fmt.Sprintf("Error: base token is for process #%d, not #%d", tokID, procID), true
	}
	v := NewValidator(ctx, procID)
	if msg := hostedCheckPushStage(v, procID); msg != "" {
		return msg, true
	}
	if !boolishArg(args, "discard_draft") {
		if msg := hostedDraftGuard(v, procID); msg != "" {
			return msg, true
		}
	}
	data, _, err := exportProcessJSON(v)
	if err != nil {
		return fmt.Sprintf("Error fetching process: %v", err), true
	}
	doc, err := decodeProcessDoc(data)
	if err != nil {
		return fmt.Sprintf("Error: the exported process is not a JSON object: %v", err), true
	}
	res, err := applyProcessPatch(doc, patch)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	content := string(out)
	src := patchPushSource{hostedPushSource: hostedPushSource{base: &b, content: content}, changed: res.changed, added: addedTitles(doc, res.added), addedCount: len(res.added), removed: res.removed}
	result, isErr := pushProcessCore(ctx, v, procID, content, args, src)
	if isErr && strings.Contains(result, "merge=true") {
		result += "\n\nOn the hosted server merge=true is not available. The ops were applied to the server's current scheme, so overwrite_server_change=true deploys them on top of that change (it drops nothing the server has now) — pass it only after pull-process outline=true shows the change is one to keep; otherwise redo the patch on what is there now."
	}
	return result, isErr
}

// addedTitles finds added nodes after deploy, where the server has replaced the
// ids they were given: by title when the title is theirs alone. The others are
// only counted.
func addedTitles(doc map[string]interface{}, ids []string) []string {
	nodes, _ := schemeNodes(doc)
	count := map[string]int{}
	for _, n := range nodes {
		t, _ := n["title"].(string)
		count[t]++
	}
	var out []string
	for _, id := range ids {
		if i, err := findNode(nodes, id); err == nil {
			if t, _ := nodes[i]["title"].(string); t != "" && count[t] == 1 {
				out = append(out, t)
			}
		}
	}
	return out
}

// hostedDraftGuard refuses to deploy over changes someone left undeployed in
// the editor: every deploy starts by deleting the process's pending version
// (ProcessJSON → DeleteVersion), so that work would vanish without a word.
// A process the server confirms was never deployed has nothing to lose.
// discard_draft=true deploys anyway. Fail-closed: a state that cannot be read
// — an error, a process missing from its folder — is not deployed over.
func hostedDraftGuard(v *Executor, procID int) string {
	retry := "; retry, or pass discard_draft=true to deploy over them"
	info, err := v.ShowProcessLifecycle(procID)
	if err != nil {
		return fmt.Sprintf("Error: could not read process #%d to check for undeployed changes (%v)%s", procID, err, retry)
	}
	proc, err := v.GetProcessByID(procID)
	if err != nil {
		return fmt.Sprintf("Error: could not read process #%d to check for undeployed changes (%v)%s", procID, err, retry)
	}
	if p, _ := proc["proc"].(string); p != "ok" {
		return fmt.Sprintf("Error: could not read process #%d to check for undeployed changes (%v)%s", procID, proc["description"], retry)
	}
	// Only a confirmed "no commits" counts: some installations send
	// commits.version and no last_confirmed_version for a deployed process.
	if commitsConfirmedEmpty(proc) {
		return ""
	}
	children, err := v.ListFolder(info.ParentObjID)
	if err != nil {
		return fmt.Sprintf("Error: could not list folder #%d to check process #%d for undeployed changes (%v)%s", info.ParentObjID, procID, err, retry)
	}
	for _, c := range children {
		if c.Obj != "conv" || c.ObjID != procID {
			continue
		}
		if draftPending(c) {
			return fmt.Sprintf("Error: process #%d has changes made in the editor that are not deployed (draft version %d). Deploying deletes them. Ask the user: deploy them first in Corezoid, or pass discard_draft=true to drop them.", procID, c.Version)
		}
		return ""
	}
	return fmt.Sprintf("Error: process #%d is not in the listing of its folder #%d, so its undeployed changes cannot be checked%s", procID, info.ParentObjID, retry)
}

// draftPending: the folder list shows the process undeployed with a draft version.
func draftPending(c FolderChild) bool {
	return !c.IsDeployed && c.Version > 0
}

// patchPushSource is the hosted push source with a compact answer: the new
// base and the outline of the changed nodes, not the whole deployed scheme.
type patchPushSource struct {
	hostedPushSource
	changed    []string // ids: the server keeps an existing node's id
	added      []string // titles: the server gives an added node a new id
	addedCount int
	removed    int
}

func (p patchPushSource) afterDeploy(_ context.Context, v *Executor) ([]string, string) {
	if v.ProcessID == 0 {
		return nil, ""
	}
	var b strings.Builder
	if proc, err := v.GetProcessByID(v.ProcessID); err == nil {
		fmt.Fprintf(&b, "base: %s\n", hostedBaseToken(v.ProcessID, baselineFromServer(proc)))
	} else {
		fmt.Fprintf(&b, "base: unavailable (%v) — pull-process again before the next edit.\n", err)
	}
	data, _, err := exportProcessJSON(v)
	if err != nil {
		fmt.Fprintf(&b, "The deployed scheme could not be read back (%v) — pull-process outline=true before the next edit.", err)
		return nil, b.String()
	}
	doc, err := decodeProcessDoc(data)
	if err != nil {
		return nil, b.String()
	}
	nodes, _ := schemeNodes(doc)
	fmt.Fprintf(&b, "Deployed: %d nodes", len(nodes))
	if p.removed > 0 {
		fmt.Fprintf(&b, ", %d removed", p.removed)
	}
	b.WriteString(".\n")
	if refs := append(append([]string{}, p.changed...), p.added...); len(refs) > 0 {
		b.WriteString("Changed and added nodes with their server IDs:\n")
		b.WriteString(processOutline(doc, refs))
	}
	if n := p.addedCount - len(p.added); n > 0 {
		fmt.Fprintf(&b, "%d added node(s) without a title of their own: read pull-process outline=true for their server IDs.\n", n)
	}
	return nil, b.String()
}
