package main

import (
	"fmt"
	"sort"
	"testing"
)

// Quality ratchet. The invariants prove a layout is VALID (all nodes placed, no
// overlaps, in canvas); nothing proved it is READABLE. The engine already
// computes readability numbers in layoutReport and threw them away, so a change
// could keep every invariant green while tripling the crossings and turning
// dozens of edges into full-height diagonals — the golden diff would only say
// "231 of 231 nodes differ".
//
// These ceilings are the measured values at the time of writing, with no
// margin: a change that worsens any of them must be a deliberate, visible act.
//
// To update after an intentional trade-off, run:
//
//	go test -run TestLayoutQualityRatchet -v .
//
// and paste the emitted table — but justify each raised number in the commit
// message, because raising one is exactly the regression this guards.
type qualityCeiling struct {
	crossings   int
	longEdges   int
	upwardEdges int
	p95Span     int
}

var layoutQualityCeilings = map[string]qualityCeiling{
	// Re-baselined when the size model moved to the measured values in
	// docs/process/node-size-reference.md (Condition bodies were over-reserved
	// by 74-77px, output rows by 29px). Deliberate trade, recorded here because
	// the ratchet correctly refused it silently:
	//   improved: p95 edge span on ALL 12 fixtures (the point of the change),
	//             crossings on star/mega, long edges on star/mega/mix/tables2;
	//   worsened: crossings +2 fractal100, +1 mix; long edges +1 fractal;
	//             upward edges +5 mega.
	//
	// Raised again when compact() started reserving a cluster's real vertical
	// extent instead of just its tallest member (it could previously CREATE an
	// overlap — see TestCompactNeverCreatesOverlap). Correctness is not
	// negotiable, so the small p95 cost is accepted:
	//   improved: crossings 3261->3217 and upward edges 108->100 on mega;
	//   worsened: p95 span +256 mega, +28 loops, +8 fractal, +1 tables2.
	"chain":      {crossings: 0, longEdges: 15, upwardEdges: 0, p95Span: 3500},
	"combo":      {crossings: 45, longEdges: 26, upwardEdges: 13, p95Span: 1285},
	"errheavy":   {crossings: 0, longEdges: 0, upwardEdges: 0, p95Span: 290},
	"fractal":    {crossings: 102, longEdges: 32, upwardEdges: 0, p95Span: 2046},
	"fractal100": {crossings: 388, longEdges: 81, upwardEdges: 0, p95Span: 3865},
	"loops":      {crossings: 17, longEdges: 17, upwardEdges: 15, p95Span: 2716},
	"mega":       {crossings: 3217, longEdges: 254, upwardEdges: 100, p95Span: 12198},
	"mix":        {crossings: 523, longEdges: 120, upwardEdges: 34, p95Span: 8709},
	"star":       {crossings: 80, longEdges: 24, upwardEdges: 26, p95Span: 1300},
	"star4":      {crossings: 81, longEdges: 27, upwardEdges: 0, p95Span: 4413},
	"table3":     {crossings: 26, longEdges: 21, upwardEdges: 0, p95Span: 2052},
	"tables2":    {crossings: 34, longEdges: 17, upwardEdges: 13, p95Span: 1042},
}

func TestLayoutQualityRatchet(t *testing.T) {
	type row struct {
		name string
		got  qualityCeiling
	}
	var rows []row
	for _, fx := range allFixtures() {
		fx := fx
		nodes := deepCopyNodes(t, fx.nodes)
		_, rep := (&layoutEngine{density: "medium"}).computeLayout(nodes)
		got := qualityCeiling{
			crossings:   rep.Crossings,
			longEdges:   rep.LongEdges,
			upwardEdges: rep.UpwardEdges,
			p95Span:     rep.P95EdgeSpan,
		}
		rows = append(rows, row{fx.name, got})

		want, ok := layoutQualityCeilings[fx.name]
		if !ok {
			t.Errorf("%s: no quality ceiling recorded — add one (see the emitted table)", fx.name)
			continue
		}
		check := func(label string, got, max int) {
			if got > max {
				t.Errorf("%s: %s regressed to %d, ceiling is %d", fx.name, label, got, max)
			}
		}
		check("crossings", got.crossings, want.crossings)
		check("long edges", got.longEdges, want.longEdges)
		check("upward edges", got.upwardEdges, want.upwardEdges)
		check("p95 edge span", got.p95Span, want.p95Span)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	if testing.Verbose() {
		fmt.Println("current values (paste into layoutQualityCeilings to re-baseline):")
		for _, r := range rows {
			fmt.Printf("\t%q: {crossings: %d, longEdges: %d, upwardEdges: %d, p95Span: %d},\n",
				r.name, r.got.crossings, r.got.longEdges, r.got.upwardEdges, r.got.p95Span)
		}
	}
}
