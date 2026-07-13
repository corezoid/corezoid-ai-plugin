package main

// File I/O for the layout engine: load a .conv.json preserving numeric
// fidelity, write back coordinate/extra changes matching the source file's
// formatting so the diff shows only what the layout touched.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// layoutDoc is a decoded .conv.json plus what the writer needs to reproduce
// the source formatting.
type layoutDoc struct {
	root            map[string]interface{}
	nodes           []map[string]interface{} // scheme.nodes, document order
	indent          string
	trailingNewline bool
}

var layIndentRe = regexp.MustCompile(`(?m)^([ \t]+)\S`)

// loadLayoutDoc reads and decodes a process file. Numbers are decoded as
// json.Number so untouched fields (obj_id, conv_id, user_id...) round-trip
// without float drift.
func loadLayoutDoc(path string) (*layoutDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root map[string]interface{}
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	scheme, _ := root["scheme"].(map[string]interface{})
	if scheme == nil {
		return nil, fmt.Errorf("%s has no scheme object — is it a process file?", path)
	}
	rawNodes, _ := scheme["nodes"].([]interface{})
	nodes := make([]map[string]interface{}, 0, len(rawNodes))
	for _, it := range rawNodes {
		if m, ok := it.(map[string]interface{}); ok {
			nodes = append(nodes, m)
		}
	}
	indent := "  "
	if m := layIndentRe.FindSubmatch(raw); m != nil {
		indent = string(m[1])
	}
	return &layoutDoc{
		root:            root,
		nodes:           nodes,
		indent:          indent,
		trailingNewline: bytes.HasSuffix(raw, []byte("\n")),
	}, nil
}

// applyCoords writes the computed coordinates into the nodes (in memory).
// Returns how many nodes actually moved.
func (d *layoutDoc) applyCoords(coords map[string]lpoint) int {
	changed := 0
	for _, n := range d.nodes {
		p, ok := coords[nodeStr(n, "id")]
		if !ok {
			continue
		}
		oldX, oldY := coordInt(n["x"]), coordInt(n["y"])
		if oldX != p.X || oldY != p.Y {
			changed++
		}
		n["x"] = p.X
		n["y"] = p.Y
	}
	return changed
}

func coordInt(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	case int:
		return t
	}
	return 0
}

// save re-marshals the document. The repo's canonical on-disk form (what
// pull-process and push-process emit) is MarshalIndent with two spaces; we
// match the SOURCE file's indentation and trailing newline so a re-layout of
// a canon file diffs only in the coordinate/extra lines.
func (d *layoutDoc) save(path string) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Default HTML escaping ON: the repo canon (pull-process, fixStruct) writes
	// & < > as \u0026 \u003c \u003e — disabling it would rewrite every such
	// line on re-layout and break the diff-only-x/y/extra property.
	enc.SetIndent("", d.indent)
	if err := enc.Encode(d.root); err != nil {
		return err
	}
	out := buf.Bytes() // Encoder appends a trailing newline
	if !d.trailingNewline {
		out = bytes.TrimRight(out, "\n")
	}
	return os.WriteFile(path, out, 0644)
}

// dryListing renders the per-node placement preview (the --dry output of the
// original engine): "(  500,  100)  [start] Start".
func (d *layoutDoc) dryListing(coords map[string]lpoint) string {
	var sb strings.Builder
	for _, n := range d.nodes {
		p := coords[nodeStr(n, "id")]
		kind := "node"
		switch nodeObjType(n) {
		case 1:
			kind = "start"
		case 2:
			kind = "final"
		case 3:
			kind = "esc"
		}
		fmt.Fprintf(&sb, "  (%5d,%5d)  [%-5s] %s\n", p.X, p.Y, kind, nodeStr(n, "title"))
	}
	return sb.String()
}
