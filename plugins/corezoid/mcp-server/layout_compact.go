package main

// The density pass and the overlap resolver — the finishing passes shared by
// every strategy.

import (
	"math"
	"sort"
)

// pyRound is Python's round(): banker's rounding (half to even), which
// differs from math.Round on exact .5 values.
func pyRound(f float64) int {
	return int(math.RoundToEven(f))
}

type clusterItem struct {
	key     float64
	id      string
	size    int
	itemIdx int // insertion order — the stable-sort base
}

// cluster1D groups items into clusters where consecutive sorted keys differ
// by <= tol. Returns clusters as (minKey, members).
type cluster struct {
	lo      float64
	members []clusterItem
}

func cluster1D(items []clusterItem, tol float64) []cluster {
	sorted := append([]clusterItem{}, items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
	var out []cluster
	for _, it := range sorted {
		if len(out) > 0 {
			last := &out[len(out)-1]
			if it.key-last.members[len(last.members)-1].key <= tol {
				last.members = append(last.members, it)
				continue
			}
		}
		out = append(out, cluster{lo: it.key, members: []clusterItem{it}})
	}
	// lo = min key of the cluster (members are key-sorted, so it is the first)
	for i := range out {
		out[i].lo = out[i].members[0].key
	}
	return out
}

// compact is the content-aware re-spacing (density) pass. Rows and columns
// keep their order and internal alignment; the pitch between adjacent
// rows/columns is CAPPED at what the actual node sizes need (max box + gap) —
// and never expanded. So a row of collapsed 48px squares stops reserving a
// full block-row of air, while intentionally tight fractional spacing (the
// layered strategy's sub-column offsets) is left alone. Deterministic, so
// adding a node still just nudges everything apart instead of reshuffling.
func (e *layoutEngine) compact(coords map[string]lpoint, g *layoutGraph) map[string]lpoint {
	gaps, ok := layDensityGaps[e.density]
	if !ok || len(coords) == 0 {
		return coords
	}
	gapV, gapH := gaps[0], gaps[1]
	out := make(map[string]lpoint, len(coords))
	for k, v := range coords {
		out[k] = v
	}

	squeeze := func(axis string, tol float64, gap int) {
		var items []clusterItem
		idx := 0
		for _, id := range g.ids { // document order — the deterministic base
			p, ok := out[id]
			if !ok {
				continue
			}
			x0, y0, x1, y1 := nodeBox(g.byID[id], p.X, p.Y)
			if axis == "y" {
				items = append(items, clusterItem{key: float64(y0), id: id, size: y1 - y0, itemIdx: idx})
			} else {
				items = append(items, clusterItem{key: float64(x0+x1) / 2.0, id: id, size: x1 - x0, itemIdx: idx})
			}
			idx++
		}
		clusters := cluster1D(items, tol)
		if len(clusters) < 2 {
			return
		}
		ext := make([]int, len(clusters))
		for i, c := range clusters {
			if axis == "y" {
				// cluster1D chains transitively, so a "row" can span far more
				// than its tallest member: three 48px icons spaced 50px apart
				// are one cluster spanning 148px. Reserving only the tallest
				// member here would let the NEXT row be pulled up into this
				// one's lower members — compact would create the very overlap
				// the pipeline just resolved. Reserve the cluster's real extent.
				bottom := c.lo
				for _, it := range c.members {
					if b := it.key + float64(it.size); b > bottom {
						bottom = b
					}
				}
				ext[i] = int(bottom - c.lo)
				continue
			}
			m := 0
			for _, it := range c.members {
				if it.size > m {
					m = it.size
				}
			}
			ext[i] = m
		}
		prevNewLo, prevOldLo := clusters[0].lo, clusters[0].lo
		moved := map[string]float64{}
		for _, it := range clusters[0].members {
			moved[it.id] = 0
		}
		for i := 1; i < len(clusters); i++ {
			oldLo := clusters[i].lo
			oldPitch := oldLo - prevOldLo
			var needed float64
			if axis == "y" {
				needed = float64(ext[i-1] + gap) // tops: prev height + gap
			} else {
				needed = float64(ext[i-1]+ext[i])/2.0 + float64(gap) // centres: half-widths
			}
			newPitch := math.Min(oldPitch, needed)
			newLo := prevNewLo + newPitch
			d := newLo - oldLo
			for _, it := range clusters[i].members {
				moved[it.id] = d
			}
			prevNewLo, prevOldLo = newLo, oldLo
		}
		for _, id := range g.ids {
			d, ok := moved[id]
			if !ok {
				continue
			}
			p := out[id]
			if axis == "y" {
				out[id] = lpoint{p.X, p.Y + pyRound(d)}
			} else {
				out[id] = lpoint{p.X + pyRound(d), p.Y}
			}
		}
	}

	squeeze("y", 55, gapV)
	squeeze("x", 40, gapH)
	return out
}

// resolveOverlaps is the rectangular overlap resolver: an intersecting node
// is pushed down cascadingly. Boxes come from nodeBox — the same
// content-aware footprint (circle 56, collapsed 56, expanded 200×variable)
// used everywhere else, so a tall expanded node (many branches, a wrapped
// title, several Condition rows) reserves the room it actually needs instead
// of the historical flat 150px guess. The horizontal gap is large (column
// readability), the vertical is small. rowStep is unused now that box height
// is content-derived rather than row-pitch-derived; kept for call-site
// compatibility.
func resolveOverlaps(coords map[string]lpoint, g *layoutGraph, rowStep int) {
	const hgap, vgap = 30, 8
	_ = rowStep

	// A node's width, height and circle-ness are content-derived (obj_type,
	// extra.modeForm, title wrapping, branch count) and none of them change
	// here — only Y moves. The pair scan below is O(n^3) in the worst case, so
	// calling nodeBox per comparison re-ran json.Unmarshal on node.extra
	// millions of times on a large process (a ~230-node graph spent most of the
	// layout in isCollapsedNode). Measure every box once up front and carry the
	// working coordinates in the same slice, leaving the hot loop pure integer
	// arithmetic.
	type boxed struct {
		id     string
		x, y   int
		w, h   int
		circle bool
	}
	order := make([]boxed, 0, len(g.ids))
	for _, id := range g.ids {
		p, ok := coords[id]
		if !ok {
			continue
		}
		n := g.byID[id]
		w, h := nodeBoxSize(n)
		order = append(order, boxed{id: id, x: p.X, y: p.Y, w: w, h: h, circle: isCircle(n)})
	}
	box := func(e boxed) (int, int, int, int) {
		if e.circle {
			return e.x - e.w/2, e.y - e.h/2, e.x + e.w/2, e.y + e.h/2
		}
		return e.x, e.y, e.x + e.w, e.y + e.h
	}
	sortYX := func() {
		sort.SliceStable(order, func(i, j int) bool {
			if order[i].y != order[j].y {
				return order[i].y < order[j].y
			}
			return order[i].x < order[j].x
		})
	}
	sortYX()
	for pass := 0; pass < len(order); pass++ {
		moved := false
		for i := range order {
			ax0, ay0, ax1, ay1 := box(order[i])
			for j := i + 1; j < len(order); j++ {
				b := &order[j]
				bx0, by0, bx1, by1 := box(*b)
				if ax0-hgap < bx1 && bx0 < ax1+hgap && ay0-vgap < by1 && by0 < ay1+vgap {
					bump := 0
					if b.circle {
						bump = 28
					}
					b.y = ay1 + vgap + bump
					coords[b.id] = lpoint{b.x, b.y}
					moved = true
				}
			}
			if moved {
				break
			}
		}
		if !moved {
			break
		}
		sortYX()
	}
}

// countOverlaps counts intersecting node-box pairs — the honesty metric for
// the tool report (must be zero after a successful layout).
func countOverlaps(coords map[string]lpoint, g *layoutGraph) int {
	type rect struct{ x0, y0, x1, y1 int }
	var boxes []rect
	for _, id := range g.ids {
		p, ok := coords[id]
		if !ok {
			continue
		}
		// The real box model (incl. 270px timer blocks) — the same one the I2
		// invariant asserts, so the user-facing metric cannot under-report.
		x0, y0, x1, y1 := nodeBox(g.byID[id], p.X, p.Y)
		boxes = append(boxes, rect{x0, y0, x1, y1})
	}
	total := 0
	for i := 0; i < len(boxes); i++ {
		for j := i + 1; j < len(boxes); j++ {
			a, b := boxes[i], boxes[j]
			if a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1 {
				total++
			}
		}
	}
	return total
}

// segIntersectsRect reports whether the segment (x1,y1)-(x2,y2) crosses the
// rectangle (rx0,ry0,rx1,ry1).
func segIntersectsRect(x1, y1, x2, y2, rx0, ry0, rx1, ry1 float64) bool {
	// trivial reject by bounding box
	if math.Max(x1, x2) < rx0 || math.Min(x1, x2) > rx1 ||
		math.Max(y1, y2) < ry0 || math.Min(y1, y2) > ry1 {
		return false
	}
	// either endpoint inside
	inside := func(x, y float64) bool { return x >= rx0 && x <= rx1 && y >= ry0 && y <= ry1 }
	if inside(x1, y1) || inside(x2, y2) {
		return true
	}
	// segment-segment intersection with each rectangle side
	cross := func(ax, ay, bx, by, cx, cy float64) float64 {
		return (bx-ax)*(cy-ay) - (by-ay)*(cx-ax)
	}
	segSeg := func(ax, ay, bx, by, cx, cy, dx, dy float64) bool {
		d1 := cross(cx, cy, dx, dy, ax, ay)
		d2 := cross(cx, cy, dx, dy, bx, by)
		d3 := cross(ax, ay, bx, by, cx, cy)
		d4 := cross(ax, ay, bx, by, dx, dy)
		return ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
			((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0))
	}
	return segSeg(x1, y1, x2, y2, rx0, ry0, rx1, ry0) ||
		segSeg(x1, y1, x2, y2, rx1, ry0, rx1, ry1) ||
		segSeg(x1, y1, x2, y2, rx1, ry1, rx0, ry1) ||
		segSeg(x1, y1, x2, y2, rx0, ry1, rx0, ry0)
}

// resolveNodeEdgeOverlaps nudges nodes OFF link lines — best effort, by
// design: a node lying on a link between two OTHER nodes reads as if the link
// passes through it. Only SHORT local links are considered (long links are
// drawn as curves around content by the UI anyway, and straight-line
// avoidance for them would shred the layout). A nudge is accepted only when
// it clears every considered link WITHOUT creating a node-box overlap;
// otherwise the node stays where it is. Returns how many nodes moved.
func resolveNodeEdgeOverlaps(coords map[string]lpoint, g *layoutGraph) int {
	type seg struct{ a, b string }
	var edges []seg
	for _, u := range g.ids {
		for _, v := range g.succs(u) {
			edges = append(edges, seg{u, v})
		}
		for _, e := range g.errors[u] {
			edges = append(edges, seg{u, e})
		}
	}
	center := func(id string, p lpoint) (float64, float64) {
		x0, y0, x1, y1 := nodeBox(g.byID[id], p.X, p.Y)
		return float64(x0+x1) / 2, float64(y0+y1) / 2
	}
	const margin = 6
	const localSpan = 2.2 * layRowStep // only short links; long ones curve around

	hitsAnyEdge := func(id string, p lpoint) bool {
		bx0, by0, bx1, by1 := nodeBox(g.byID[id], p.X, p.Y)
		for _, e := range edges {
			if e.a == id || e.b == id {
				continue
			}
			pa, ok := coords[e.a]
			if !ok {
				continue
			}
			pb, ok := coords[e.b]
			if !ok {
				continue
			}
			ax, ay := center(e.a, pa)
			bx, by := center(e.b, pb)
			if math.Abs(ay-by) > localSpan && math.Abs(ax-bx) > localSpan {
				continue
			}
			if math.Hypot(bx-ax, by-ay) > 2.5*localSpan {
				continue
			}
			if segIntersectsRect(ax, ay, bx, by,
				float64(bx0-margin), float64(by0-margin), float64(bx1+margin), float64(by1+margin)) {
				return true
			}
		}
		return false
	}
	overlapsAnyNode := func(id string, p lpoint) bool {
		x0, y0, x1, y1 := nodeBox(g.byID[id], p.X, p.Y)
		for _, o := range g.ids {
			if o == id {
				continue
			}
			po, ok := coords[o]
			if !ok {
				continue
			}
			ox0, oy0, ox1, oy1 := nodeBox(g.byID[o], po.X, po.Y)
			if x0 < ox1+24 && ox0 < x1+24 && y0 < oy1+8 && oy0 < y1+8 {
				return true
			}
		}
		return false
	}

	// Prefer widening a local row over opening a new vertical hole in the
	// business flow. The larger horizontal fallbacks matter for region
	// layouts, where the first two offsets may still land inside a neighbour.
	candidates := [][2]int{
		{60, 0}, {-60, 0}, {120, 0}, {-120, 0}, {180, 0}, {-180, 0}, {240, 0}, {-240, 0},
		{0, 70}, {0, 110}, {60, 70}, {-60, 70},
	}
	// A shared column is the strongest reading cue this layout has: the primary
	// spine AND every side branch (a loop body, an error strip) are drawn as
	// vertical columns. Nudging a node sideways to dodge a link line trades
	// that cue for a much weaker one — a line passing under a box stays
	// traceable, a column that jogs mid-flow does not. Loop returns are the
	// worst case: they sweep back across the very column they belong to, so
	// without this guard the return displaces either the condition that owns
	// the loop or a node in the loop body.
	//
	// So a node already sharing its x with a neighbour on its primary chain is
	// left where it is. Alignment is sampled from the incoming coordinates, the
	// arrangement the strategy deliberately produced. Genuinely isolated nodes
	// have no column to protect and are still free to move. This pass already
	// treats "leave it where it is" as an acceptable outcome when no candidate
	// is clean; column members simply always take that branch.
	primaryPred := make(map[string]string, len(g.ids))
	for _, u := range g.ids {
		if v := g.primary[u]; v != "" {
			primaryPred[v] = u
		}
	}
	columnAligned := make(map[string]bool, len(g.ids))
	for _, u := range g.ids {
		pu, ok := coords[u]
		if !ok {
			continue
		}
		for _, v := range [2]string{g.primary[u], primaryPred[u]} {
			if v == "" {
				continue
			}
			if pv, ok := coords[v]; ok && pv.X == pu.X {
				columnAligned[u] = true
				break
			}
		}
	}

	moved := 0
	for _, id := range g.ids {
		p, ok := coords[id]
		if !ok || columnAligned[id] || !hitsAnyEdge(id, p) {
			continue
		}
		for _, c := range candidates {
			np := lpoint{p.X + c[0], p.Y + c[1]}
			if !hitsAnyEdge(id, np) && !overlapsAnyNode(id, np) {
				coords[id] = np
				moved++
				break
			}
		}
		// no clean spot found → leave it; an unavoidable crossing beats chaos
	}
	return moved
}
