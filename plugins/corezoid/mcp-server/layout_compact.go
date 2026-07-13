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
// is pushed down cascadingly. Boxes: block 200×(≤150), circle 56, collapsed
// 48. The horizontal gap is large (column readability), the vertical is small.
func resolveOverlaps(coords map[string]lpoint, g *layoutGraph, rowStep int) {
	const hgap, vgap = 30, 8
	blockH := rowStep - vgap - 2
	if blockH > 150 {
		blockH = 150
	}

	box := func(id string) (int, int, int, int) {
		p := coords[id]
		n := g.byID[id]
		if isCircle(n) {
			return p.X - 28, p.Y - 28, p.X + 28, p.Y + 28
		}
		if isCollapsedNode(n) {
			return p.X, p.Y, p.X + 48, p.Y + 48
		}
		return p.X, p.Y, p.X + 200, p.Y + blockH
	}

	var order []string
	for _, id := range g.ids {
		if _, ok := coords[id]; ok {
			order = append(order, id)
		}
	}
	sortYX := func() {
		sort.SliceStable(order, func(i, j int) bool {
			a, b := coords[order[i]], coords[order[j]]
			if a.Y != b.Y {
				return a.Y < b.Y
			}
			return a.X < b.X
		})
	}
	sortYX()
	for pass := 0; pass < len(order); pass++ {
		moved := false
		for i, a := range order {
			ax0, ay0, ax1, ay1 := box(a)
			for _, b := range order[i+1:] {
				bx0, by0, bx1, by1 := box(b)
				if ax0-hgap < bx1 && bx0 < ax1+hgap && ay0-vgap < by1 && by0 < ay1+vgap {
					p := coords[b]
					bump := 0
					if isCircle(g.byID[b]) {
						bump = 28
					}
					coords[b] = lpoint{p.X, ay1 + vgap + bump}
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
