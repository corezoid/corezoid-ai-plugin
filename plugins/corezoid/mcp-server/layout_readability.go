package main

import (
	"math"
	"sort"
)

// layoutReadability contains inexpensive geometry signals used to compare
// layout revisions. Link rendering is orthogonal in the UI, so crossings are
// an approximation over node-centre segments; it is still stable and useful
// as a regression signal.
type layoutReadability struct {
	EdgeCrossings    int
	MaxDedicatedSpan int
	LongDedicated    int
	MaxEdgeSpan      int
	P95EdgeSpan      int
	LongEdges        int
	UpwardEdges      int
}

type layoutEdge struct {
	from string
	to   string
}

func nodeCenter(n map[string]interface{}, p lpoint) lpoint {
	x0, y0, x1, y1 := nodeBox(n, p.X, p.Y)
	return lpoint{(x0 + x1) / 2, (y0 + y1) / 2}
}

func strictSegmentCross(a, b, c, d lpoint) bool {
	orient := func(p, q, r lpoint) int64 {
		return int64(q.X-p.X)*int64(r.Y-p.Y) -
			int64(q.Y-p.Y)*int64(r.X-p.X)
	}
	abC, abD := orient(a, b, c), orient(a, b, d)
	cdA, cdB := orient(c, d, a), orient(c, d, b)
	return ((abC < 0 && abD > 0) || (abC > 0 && abD < 0)) &&
		((cdA < 0 && cdB > 0) || (cdA > 0 && cdB < 0))
}

func measureLayoutReadability(coords map[string]lpoint, g *layoutGraph) layoutReadability {
	var out layoutReadability
	var edges []layoutEdge
	seen := map[layoutEdge]bool{}
	for _, u := range g.ids {
		for _, v := range g.allOut(u) {
			edge := layoutEdge{u, v}
			if _, fromOK := coords[u]; !fromOK || seen[edge] {
				continue
			}
			if _, toOK := coords[v]; !toOK {
				continue
			}
			seen[edge] = true
			edges = append(edges, edge)
		}
	}
	// nodeCenter goes through nodeBox -> nodeBoxSize -> json.Unmarshal on
	// node.extra. Nothing moves while the metrics are measured, so resolve each
	// endpoint once instead of O(E^2) times in the crossing scan below.
	centers := make(map[string]lpoint, len(edges)*2)
	center := func(id string) lpoint {
		if c, ok := centers[id]; ok {
			return c
		}
		c := nodeCenter(g.byID[id], coords[id])
		centers[id] = c
		return c
	}
	for i, a := range edges {
		for _, b := range edges[i+1:] {
			if a.from == b.from || a.from == b.to || a.to == b.from || a.to == b.to {
				continue
			}
			a0 := center(a.from)
			a1 := center(a.to)
			b0 := center(b.from)
			b1 := center(b.to)
			if strictSegmentCross(a0, a1, b0, b1) {
				out.EdgeCrossings++
			}
		}
	}
	var spans []int
	for _, edge := range edges {
		from := center(edge.from)
		to := center(edge.to)
		span := int(math.Round(math.Hypot(float64(to.X-from.X), float64(to.Y-from.Y))))
		spans = append(spans, span)
		if span > out.MaxEdgeSpan {
			out.MaxEdgeSpan = span
		}
		if span > 3*layRowStep {
			out.LongEdges++
		}
		if to.Y < from.Y-layRowStep/2 {
			out.UpwardEdges++
		}
	}
	if len(spans) > 0 {
		sort.Ints(spans)
		idx := int(math.Ceil(float64(len(spans))*0.95)) - 1
		if idx < 0 {
			idx = 0
		}
		out.P95EdgeSpan = spans[idx]
	}

	errorOwners := map[string]map[string]bool{}
	mainFlow, _ := g.errClosure()
	for _, u := range g.ids {
		for _, root := range g.errors[u] {
			if mainFlow[root] {
				continue
			}
			if errorOwners[root] == nil {
				errorOwners[root] = map[string]bool{}
			}
			errorOwners[root][u] = true
		}
	}
	for root, owners := range errorOwners {
		if len(owners) != 1 {
			continue
		}
		for owner := range owners {
			op, ook := coords[owner]
			rp, rok := coords[root]
			if !ook || !rok {
				continue
			}
			oc := nodeCenter(g.byID[owner], op)
			rc := nodeCenter(g.byID[root], rp)
			span := int(math.Round(math.Hypot(float64(rc.X-oc.X), float64(rc.Y-oc.Y))))
			if span > out.MaxDedicatedSpan {
				out.MaxDedicatedSpan = span
			}
			if span > 3*layRowStep {
				out.LongDedicated++
			}
		}
	}
	return out
}
