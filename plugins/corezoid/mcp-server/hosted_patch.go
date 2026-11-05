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
	"description": "Node edits, applied in order, all or none: {op: \"set\", node, set: {title|description|condition|logics|semaphors|x|y|extra|options|obj_type: value}, unset?: [description|extra|options]} (logics/semaphors replace that node's condition list); {op: \"add\", value: {title, obj_type, condition: {logics, semaphors}, ...}} (no id: one is made); {op: \"remove\", node}. node is a node id or a title no other node has. A logic or semaphore left pointing at a missing node is refused — repoint it in the same patch.",
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
	data, _, err := exportProcessJSON(v)
	if err != nil {
		return fmt.Sprintf("Error fetching process: %v", err), true
	}
	doc, err := decodeProcessDoc(data)
	if err != nil {
		return fmt.Sprintf("Error: the exported process is not a JSON object: %v", err), true
	}
	touched, err := applyProcessPatch(doc, patch)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	content := string(out)
	src := patchPushSource{hostedPushSource: hostedPushSource{base: &b, content: content}, touched: changedTitles(doc, touched)}
	result, isErr := pushProcessCore(ctx, v, procID, content, args, src)
	if isErr && strings.Contains(result, "merge=true") {
		result += "\n\nOn the hosted server merge=true is not available: pull-process again (outline) and redo the patch on what is there now — or pass overwrite_server_change=true to drop the server change."
	}
	return result, isErr
}

// changedTitles maps changed node references to their titles: the server gives
// every node a new id on deploy, the title is what finds it afterwards.
func changedTitles(doc map[string]interface{}, refs []string) []string {
	nodes, _ := schemeNodes(doc)
	var out []string
	for _, r := range refs {
		if i, err := findNode(nodes, r); err == nil {
			if t, _ := nodes[i]["title"].(string); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// patchPushSource is the hosted push source with a compact answer: the new
// base and the outline of the changed nodes, not the whole deployed scheme.
type patchPushSource struct {
	hostedPushSource
	touched []string
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
	fmt.Fprintf(&b, "Deployed: %d nodes. ", len(nodes))
	if len(p.touched) > 0 {
		b.WriteString("Changed nodes with their server IDs:\n")
		b.WriteString(processOutline(doc, p.touched))
	} else {
		b.WriteString("No node changed.\n")
	}
	return nil, b.String()
}
