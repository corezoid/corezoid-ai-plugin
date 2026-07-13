package main

// The layout-process tool: deterministic auto-arrangement of a process's node
// coordinates. Purely local (no API calls, no auth) — it rewrites only x/y and
// the extra.modeForm collapse flag; edges, logic, conv_id and aliases stay
// byte-for-byte intact, so a re-layout can never alter behaviour.

import (
	"context"
	"fmt"
	"strings"
)

func handleLayoutProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	_ = ctx
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	density := optStrArg(args, "density")
	if density == "" {
		density = "medium"
	}
	switch density {
	case "compact", "medium", "roomy":
	default:
		return fmt.Sprintf("Error: unknown density %q — use compact, medium or roomy.", density), true
	}
	dry := false
	if b, ok := args["dry"].(bool); ok {
		dry = b
	} else if ds, ok := args["dry"].(string); ok {
		// CLI mode passes args as strings
		dry = strings.EqualFold(ds, "true") || ds == "1"
	}

	doc, err := loadLayoutDoc(filePath)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	if len(doc.nodes) == 0 {
		return fmt.Sprintf("Nothing to lay out: %s has no nodes.", filePath), false
	}

	e := &layoutEngine{density: density}
	coords, rep := e.computeLayout(doc.nodes)

	var sb strings.Builder
	fmt.Fprintf(&sb, "strategy: %s  (%s)  density=%s\n", rep.Strategy, rep.Reason, density)
	fmt.Fprintf(&sb, "nodes=%d width=%dpx height=%dpx overlaps=%d collapsed=%d\n",
		rep.Nodes, rep.Width, rep.Height, rep.Overlaps, rep.Collapsed)
	if rep.Overlaps > 0 {
		fmt.Fprintf(&sb, "⚠ the layout still reports %d overlapping node pairs — please report this process shape\n", rep.Overlaps)
	}

	if dry {
		sb.WriteString("\nDRY RUN — the file was NOT modified. Planned placement:\n")
		sb.WriteString(doc.dryListing(coords))
		return sb.String(), false
	}

	changed := doc.applyCoords(coords)
	if err := doc.save(filePath); err != nil {
		return fmt.Sprintf("Error: layout computed but could not write %s: %v", filePath, err), true
	}
	fmt.Fprintf(&sb, "layout applied: %s (%d nodes, %d moved)\n", filePath, rep.Nodes, changed)
	sb.WriteString("Next: lint-process, then push-process.")
	return sb.String(), false
}
