package main

import (
	"encoding/json"
	"fmt"
)

// coords.go keeps push from destroying a laid-out process. On every push,
// fixStruct -> applyLayout re-lays-out the WHOLE scheme when every node looks
// unplaced (x==0 && y==0), and otherwise re-places any single unplaced node. An
// edit that dropped node coordinates (e.g. the scheme was rebuilt without x/y),
// fully or partially, therefore silently moves nodes. Before layout runs we
// re-hydrate any lost coordinate from the process's current server version,
// matched by node identity, so an existing arrangement is preserved and only
// genuinely-new nodes (absent on the server) get placed.

// schemeNodesFromConv parses scheme.nodes out of a conv JSON string.
func schemeNodesFromConv(convJSON string) []map[string]any {
	var doc map[string]any
	if json.Unmarshal([]byte(convJSON), &doc) != nil {
		return nil
	}
	return schemeNodesFromDoc(doc)
}

// schemeNodesFromDoc pulls scheme.nodes out of an already-parsed conv document.
func schemeNodesFromDoc(doc map[string]any) []map[string]any {
	scheme, ok := doc["scheme"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := scheme["nodes"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// nodePlaced reports whether a node carries a real (non-origin) position.
func nodePlaced(n map[string]any) bool {
	x, _ := n["x"].(float64)
	y, _ := n["y"].(float64)
	return x != 0 || y != 0
}

// anyNodeUnplaced is true when at least one node sits at (0,0)/has no
// coordinates. Any unplaced node means a coordinate may have been lost (fully
// or partially), so it is worth checking the server for a saved position before
// layout runs. Genuinely-new nodes (absent on the server) simply won't match
// and stay unplaced for normal placement.
func anyNodeUnplaced(convJSON string) bool {
	for _, n := range schemeNodesFromConv(convJSON) {
		if !nodePlaced(n) {
			return true
		}
	}
	return false
}

// coordMatchKeys returns, per node in scheme order, a cross-version match key:
// the title for titled nodes, or obj_type + ordinal for untitled ones (Start
// events and error finals are frequently untitled). Node ids are unusable — the
// server regenerates them on every push.
func coordMatchKeys(nodes []map[string]any) []string {
	keys := make([]string, len(nodes))
	ord := map[int]int{}
	for i, n := range nodes {
		if t, _ := n["title"].(string); t != "" {
			keys[i] = "t:" + t
		} else {
			ot := coordObjType(n)
			keys[i] = fmt.Sprintf("u:%d:%d", ot, ord[ot])
			ord[ot]++
		}
	}
	return keys
}

func coordObjType(n map[string]any) int {
	switch v := n["obj_type"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// rehydrateCoords fills missing x/y on local nodes from the server scheme,
// matched by key. Returns the updated conv JSON and how many nodes were
// restored. Best-effort and safe: it only fills a coordinate the local file
// lacks, only from a server node that actually has a position, and skips
// ambiguous (duplicate) keys so a wrong match can never move a node.
func rehydrateCoords(localJSON string, serverNodes []map[string]any) (string, int) {
	var doc map[string]any
	if json.Unmarshal([]byte(localJSON), &doc) != nil {
		return localJSON, 0
	}
	localNodes := schemeNodesFromDoc(doc)
	if len(localNodes) == 0 || len(serverNodes) == 0 {
		return localJSON, 0
	}

	skeys := coordMatchKeys(serverNodes)
	count := map[string]int{}
	for _, k := range skeys {
		count[k]++
	}
	type xy struct{ x, y any }
	pos := map[string]xy{}
	for i, n := range serverNodes {
		k := skeys[i]
		if count[k] > 1 { // ambiguous — never impose a coordinate
			continue
		}
		if nodePlaced(n) {
			pos[k] = xy{n["x"], n["y"]}
		}
	}

	lkeys := coordMatchKeys(localNodes)
	filled := 0
	for i, n := range localNodes {
		if nodePlaced(n) {
			continue
		}
		if p, ok := pos[lkeys[i]]; ok {
			n["x"] = p.x
			n["y"] = p.y
			filled++
		}
	}
	if filled == 0 {
		return localJSON, 0
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return localJSON, 0
	}
	return string(b), filled
}

// rehydrateCoordsFromServer fetches the process's current server scheme and
// re-hydrates lost coordinates into localJSON. Never fails a push — a fetch
// error just means no re-hydration.
func rehydrateCoordsFromServer(v *Executor, localJSON string) (string, int) {
	raw, err := v.ExportProcess()
	if err != nil {
		logger.Warn("rehydrate: could not export server scheme: %v", err)
		return localJSON, 0
	}
	obj := raw
	if arr, ok := raw.([]any); ok && len(arr) > 0 {
		obj = arr[0]
	}
	m, ok := obj.(map[string]any)
	if !ok {
		return localJSON, 0
	}
	server := schemeNodesFromDoc(m)
	if len(server) == 0 {
		return localJSON, 0
	}
	return rehydrateCoords(localJSON, server)
}
