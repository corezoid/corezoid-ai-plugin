package main

// The layout engine facade: strategy pre-analysis and the single entry point
// the tool (and, later, the push hook of PR #37) calls.

import (
	"fmt"
	"math"
	"strings"
)

const layoutEngineRevision = "recovery-v6-preserve-modes"

// layoutReport is what the engine tells the caller about a run.
type layoutReport struct {
	Strategy    string // "waterfall" | "layered+error-rail" | "waterfall+regions"
	Reason      string
	Nodes       int
	Width       int
	Height      int
	Overlaps    int // must be 0 after a successful layout
	Collapsed   int // retained for report compatibility; always zero
	Crossings   int // estimated centre-line crossings; a stable regression signal
	MaxErrSpan  int // longest direct edge to a dedicated error root, in pixels
	LongErrors  int // dedicated error edges longer than three normal rows
	MaxEdgeSpan int // longest edge of any kind, in pixels
	P95EdgeSpan int // 95th percentile of all edge spans
	LongEdges   int // all edges longer than three normal rows
	UpwardEdges int // edges returning upward by more than half a row
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
	errTopo := g.classifyErrors()

	if total >= 25 {
		if regions, _ := detectRegions(nodes); len(regions) > 0 {
			var parts []string
			// DIAMOND is intentionally a local signal. On a very large graph
			// one tiny fork/rejoin is not enough evidence to abandon layered
			// placement for the other hundred-plus nodes.
			// A secondary callback/recovery entry can appear as one extra
			// forward component even though it is anchored to the same
			// business flow.
			preferRegions := flows <= 4 && total <= 80
			for _, r := range regions {
				switch r.kind {
				case "table":
					preferRegions = true
					depth := 0
					for _, c := range r.cols {
						if len(c) > depth {
							depth = len(c)
						}
					}
					parts = append(parts, fmt.Sprintf("table(%dx%d)", len(r.cols), depth))
				case "star":
					preferRegions = true
					parts = append(parts, fmt.Sprintf("star(%d rays)", len(r.cols)))
				case "diamond":
					parts = append(parts, fmt.Sprintf("diamond(%d+%d)",
						len(r.cols[0]), len(r.cols[1])))
				}
			}
			// A tiny incidental diamond inside a large many-flow mesh should
			// not route the entire process away from Sugiyama. TABLE/STAR are
			// broad structural signals; DIAMOND is local and wins only when
			// the surrounding graph is itself a small set of active flows.
			if preferRegions {
				return e.layoutHybrid, "waterfall+regions",
					fmt.Sprintf("%d nodes, %d flows, %d shared/%d dedicated error roots, regions: %s",
						total, flows, errTopo.Shared, errTopo.Dedicated, strings.Join(parts, ", "))
			}
		}
	}

	// A high raw error-node fraction is not by itself a mesh signal: a long
	// single business spine with one tiny dedicated error cluster per action
	// remains most readable as a waterfall with owner-local clusters. Route by
	// error OWNERSHIP instead. Shared roots create a genuine fan-in rail;
	// dedicated-dominant errors stay local unless the business graph itself
	// has many independent flows.
	dedicatedDominant := errTopo.Roots > 0 &&
		errTopo.Dedicated*2 >= errTopo.Roots &&
		errTopo.Shared <= 1
	// At very large scale even individually-owned handlers form a second
	// visual system; the layered engine can pack dozens of same-row clusters
	// more predictably than waterfall side wings.
	veryLargeErrorFanout := total > 100 && errTopo.Roots >= 20
	if total >= 25 && (flows > 3 || veryLargeErrorFanout || (errFrac > 0.30 && !dedicatedDominant)) {
		return e.layoutPartitioned, "layered+error-rail",
			fmt.Sprintf("%d flows, %.0f%% error nodes, %d shared/%d dedicated roots",
				flows, errFrac*100, errTopo.Shared, errTopo.Dedicated)
	}
	return e.layout, "waterfall",
		fmt.Sprintf("%d nodes, %d flow(s), %.0f%% error nodes, %d shared/%d dedicated roots",
			total, flows, errFrac*100, errTopo.Shared, errTopo.Dedicated)
}

// savedNodeExtra preserves the caller's exact visual state. computeLayout also
// blocks strategy-local mode changes, then restores every extra value as a
// second line of defence before finishing geometry against the original sizes.
type savedNodeExtra struct {
	present bool
	value   interface{}
}

func cloneLayoutValue(v interface{}) interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, item := range x {
			out[k] = cloneLayoutValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, item := range x {
			out[i] = cloneLayoutValue(item)
		}
		return out
	default:
		return x
	}
}

func saveNodeExtras(nodes []map[string]interface{}) []savedNodeExtra {
	saved := make([]savedNodeExtra, len(nodes))
	for i, n := range nodes {
		value, present := n["extra"]
		saved[i] = savedNodeExtra{present: present, value: cloneLayoutValue(value)}
	}
	return saved
}

func restoreNodeExtras(nodes []map[string]interface{}, saved []savedNodeExtra) {
	for i, n := range nodes {
		if i >= len(saved) || !saved[i].present {
			delete(n, "extra")
			continue
		}
		n["extra"] = cloneLayoutValue(saved[i].value)
	}
}

// computeLayout runs the engine over scheme.nodes: picks a strategy, computes
// coordinates and returns the run report. The caller's collapse/expand state
// is an invariant: layout changes coordinates only.
func (e *layoutEngine) computeLayout(nodes []map[string]interface{}) (map[string]lpoint, layoutReport) {
	if e.density == "" {
		e.density = "medium"
	}
	originalExtras := saveNodeExtras(nodes)
	for _, n := range nodes {
		n[layoutPreserveModeKey] = true
	}
	fn, label, reason := e.analyzeLayout(nodes)
	coords := fn(nodes)

	// Never leak a strategy's temporary modeForm decisions into the process.
	// Re-run the finishing geometry with the original rendered sizes so an
	// expanded node cannot overlap a neighbour placed as if it were collapsed.
	for _, n := range nodes {
		delete(n, layoutPreserveModeKey)
	}
	restoreNodeExtras(nodes, originalExtras)
	g0 := buildLayoutGraph(nodes)
	resolveOverlaps(coords, g0, layRowStep)
	coords = e.compact(coords, g0)
	resolveOverlaps(coords, g0, layRowStep)
	clampCoords(coords, layRowStep, layColStep)

	// Final polish: nodes should not sit on link lines (best effort — every
	// nudge is pre-validated to not create a box overlap, so no re-resolve).
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
	readability := measureLayoutReadability(coords, g)
	rep.Crossings = readability.EdgeCrossings
	rep.MaxErrSpan = readability.MaxDedicatedSpan
	rep.LongErrors = readability.LongDedicated
	rep.MaxEdgeSpan = readability.MaxEdgeSpan
	rep.P95EdgeSpan = readability.P95EdgeSpan
	rep.LongEdges = readability.LongEdges
	rep.UpwardEdges = readability.UpwardEdges
	rep.Collapsed = 0
	return coords, rep
}
