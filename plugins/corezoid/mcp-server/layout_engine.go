package main

// The layout engine facade: strategy pre-analysis and the single entry point
// the tool (and, later, the push hook of PR #37) calls.

import (
	"fmt"
	"math"
	"strings"
)

// layoutReport is what the engine tells the caller about a run.
type layoutReport struct {
	Strategy  string // "waterfall" | "layered+error-rail" | "waterfall+regions"
	Reason    string
	Nodes     int
	Width     int
	Height    int
	Overlaps  int // must be 0 after a successful layout
	Collapsed int // nodes whose extra.modeForm the engine set to collapse
}

// analyzeLayout is the quick structural pre-analysis → strategy selection
// WITHOUT user hints. Signals: the fraction of error-handler nodes, the
// number of independent forward flows and (for >=25 nodes) detected regions.
func (e *layoutEngine) analyzeLayout(nodes []map[string]interface{}) (func([]map[string]interface{}) map[string]lpoint, string, string) {
	total := len(nodes)
	if total < 1 {
		total = 1
	}
	g := buildLayoutGraph(nodes)
	_, errClosure := g.errClosure()
	errFrac := float64(len(errClosure)) / float64(total)
	flows := g.countForwardFlows()

	if total >= 25 {
		if regions, _ := detectRegions(nodes); len(regions) > 0 {
			var parts []string
			for _, r := range regions {
				if r.kind == "table" {
					depth := 0
					for _, c := range r.cols {
						if len(c) > depth {
							depth = len(c)
						}
					}
					parts = append(parts, fmt.Sprintf("table(%dx%d)", len(r.cols), depth))
				} else {
					parts = append(parts, fmt.Sprintf("star(%d rays)", len(r.cols)))
				}
			}
			return e.layoutHybrid, "waterfall+regions",
				fmt.Sprintf("%d nodes, regions: %s", total, strings.Join(parts, ", "))
		}
	}

	// The layered machinery pays off only at scale: on a small process its
	// spine drifts sideways and reads worse than a plain waterfall, even when
	// several loops inflate the flow count. Below ~25 nodes always waterfall.
	if total >= 25 && (flows > 3 || errFrac > 0.30) {
		return e.layoutPartitioned, "layered+error-rail",
			fmt.Sprintf("%d independent flows, %.0f%% error-handling nodes", flows, errFrac*100)
	}
	return e.layout, "waterfall",
		fmt.Sprintf("%d nodes, %d flow(s), %.0f%% error-handling nodes", total, flows, errFrac*100)
}

// computeLayout runs the engine over scheme.nodes: picks a strategy, computes
// coordinates, mutates the nodes' extra.modeForm for collapse decisions, and
// returns the coordinates plus the run report. This is the stable seam for
// other callers (e.g. a place-new-nodes push hook).
func (e *layoutEngine) computeLayout(nodes []map[string]interface{}) (map[string]lpoint, layoutReport) {
	if e.density == "" {
		e.density = "medium"
	}
	collapsedBefore := 0
	for _, n := range nodes {
		if isCollapsedNode(n) {
			collapsedBefore++
		}
	}
	fn, label, reason := e.analyzeLayout(nodes)
	coords := fn(nodes)

	// Final polish: nodes should not sit on link lines (best effort — every
	// nudge is pre-validated to not create a box overlap, so no re-resolve).
	g0 := buildLayoutGraph(nodes)
	if resolveNodeEdgeOverlaps(coords, g0) > 0 {
		clampCoords(coords, layRowStep, layColStep)
	}

	rep := layoutReport{Strategy: label, Reason: reason, Nodes: len(coords)}
	minX, maxX := math.MaxInt, math.MinInt
	minY, maxY := math.MaxInt, math.MinInt
	for _, p := range coords {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}
	if len(coords) > 0 {
		rep.Width = maxX - minX
		rep.Height = maxY - minY
	}
	g := buildLayoutGraph(nodes)
	rep.Overlaps = countOverlaps(coords, g)
	collapsedAfter := 0
	for _, n := range nodes {
		if isCollapsedNode(n) {
			collapsedAfter++
		}
	}
	rep.Collapsed = collapsedAfter - collapsedBefore
	return coords, rep
}
