package main

import (
	"fmt"
	"sort"
	"strings"
)

// A push sends every node of the process in one modify request, and the server
// answers a bad one with a short "invalid parameters" — sometimes with no
// message at all — that names neither the node nor the field. The failed op's
// index is the only pointer to the culprit, so the error is enriched from the
// op that was sent: which node (by the id in the local file and its title),
// what its logics carried, what lint finds wrong with it, and the usual causes.

// rejectedDeployOpContext describes a rejected node or params op for the
// error message. Empty for every other op: their callers' errors already say
// what they were doing.
func (v *Executor) rejectedDeployOpContext(op map[string]any) string {
	switch op["obj"] {
	case "node":
		return v.rejectedNodeContext(op)
	case "conv_params":
		return rejectedParamsContext(op)
	}
	return ""
}

func (v *Executor) rejectedNodeContext(op map[string]any) string {
	serverID, _ := op["obj_id"].(string)
	title, _ := op["title"].(string)
	logics := opMapSlice(op["logics"])

	local := v.localNodeID(serverID)

	var sb strings.Builder
	where := "node " + local
	if title != "" {
		where += fmt.Sprintf(" (%q)", title)
	}
	if local != serverID {
		where += fmt.Sprintf(", server id %s", serverID)
	}
	fmt.Fprintf(&sb, "\nRejected: %s.", where)

	var sent []string
	for _, lg := range logics {
		t, _ := lg["type"].(string)
		keys := make([]string, 0, len(lg))
		for k := range lg {
			if k != "type" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		sent = append(sent, fmt.Sprintf("%s{%s}", t, strings.Join(keys, ", ")))
	}
	if len(sent) > 0 {
		fmt.Fprintf(&sb, "\nLogics sent: %s.", strings.Join(sent, ", "))
	}

	node := processNode{id: local, title: title, logics: logics}
	for _, f := range findIncompleteCallProcess([]processNode{node}) {
		fmt.Fprintf(&sb, "\nFound: %s.", f.Issue)
	}
	for _, f := range findUnderspecifiedAPINodes([]processNode{node}) {
		fmt.Fprintf(&sb, "\nFound: %s.", f.Issue)
	}
	for _, f := range findRpcReplyMismatches([]processNode{node}) {
		fmt.Fprintf(&sb, "\nFound: api_rpc_reply %s.", f.Issue)
	}

	sb.WriteString("\nTypical causes: a Call Process (api_rpc) logic without extra/extra_type objects, or with an extra key that has no extra_type entry; " +
		"an extra/res_data key without its *_type twin (and vice versa); an object value in extra not given as a stringified JSON string; " +
		"a node that can fail with no err_node_id; a logic field of the wrong type (see docs/nodes/ for the node's canonical shape). " +
		"Fix this node and push again — the other nodes in the request were not the cause.")
	return sb.String()
}

func rejectedParamsContext(op map[string]any) string {
	var sb strings.Builder
	sb.WriteString("\nRejected: the process params (the parameter descriptions at the top level of the .conv.json).")
	for _, f := range findIncompleteParams(op["params"]) {
		fmt.Fprintf(&sb, "\nFound: %s.", f.Issue)
	}
	fmt.Fprintf(&sb, "\nTypical causes: a params element missing one of %s (empty values \"\" and [] are accepted, absent keys are not); "+
		"a type outside string/number/boolean/object/array; a flag outside required/input/output/auto-clear. Use \"params\": [] when the process declares no parameters.",
		strings.Join(paramRequiredKeys, ", "))
	return sb.String()
}

// localNodeID maps a server node id back to the id in the local file, which
// differs for a node this push created. Falls back to the id it was given.
func (v *Executor) localNodeID(serverID string) string {
	for local, info := range v.NodeIDMap {
		if info.ServerID == serverID {
			return local
		}
	}
	return serverID
}

// opMapSlice reads logics from an op, which ModifyNodes builds as
// []map[string]interface{} and a decoded payload holds as []interface{}.
func opMapSlice(v interface{}) []map[string]interface{} {
	if ms, ok := v.([]map[string]interface{}); ok {
		return ms
	}
	return toMapSlice(v)
}
