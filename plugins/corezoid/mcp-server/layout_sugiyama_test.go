package main

import (
	"fmt"
	"math"
	"testing"
)

func TestWeightedMedianPosition(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want float64
		ok   bool
	}{
		{name: "empty", ok: false},
		{name: "single", in: []int{7}, want: 7, ok: true},
		{name: "pair", in: []int{2, 8}, want: 5, ok: true},
		{name: "odd unsorted", in: []int{9, 1, 4}, want: 4, ok: true},
		// Weighted median: left spread=2, right spread=7, therefore the
		// result is pulled from the flat 2.5 median towards position 2.
		{name: "asymmetric even", in: []int{10, 0, 3, 2}, want: 20.0 / 9.0, ok: true},
		{name: "zero spread", in: []int{3, 3, 3, 3}, want: 3, ok: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := weightedMedianPosition(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok=%v, want %v", ok, tc.ok)
			}
			if ok && math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("median=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestCountCrossingsBetweenLayers(t *testing.T) {
	tests := []struct {
		name   string
		top    []string
		bottom []string
		adj    map[string][]string
		want   int
	}{
		{
			name: "parallel",
			top:  []string{"a", "b"}, bottom: []string{"x", "y"},
			adj:  map[string][]string{"a": {"x"}, "b": {"y"}},
			want: 0,
		},
		{
			name: "one crossing",
			top:  []string{"a", "b"}, bottom: []string{"x", "y"},
			adj:  map[string][]string{"a": {"y"}, "b": {"x"}},
			want: 1,
		},
		{
			name: "shared source is not a crossing",
			top:  []string{"a"}, bottom: []string{"x", "y"},
			adj:  map[string][]string{"a": {"y", "x"}},
			want: 0,
		},
		{
			name: "shared target is not a crossing",
			top:  []string{"a", "b"}, bottom: []string{"x"},
			adj:  map[string][]string{"a": {"x"}, "b": {"x"}},
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := countCrossingsBetweenLayers(tc.top, tc.bottom, tc.adj); got != tc.want {
				t.Fatalf("crossings=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestTransposeLayerReducesCrossingsDeterministically(t *testing.T) {
	layers := [][]string{{"a", "b"}, {"y", "x"}}
	adj := map[string][]string{"a": {"x"}, "b": {"y"}}

	if !transposeLayer(layers, adj, nil, 1, false) {
		t.Fatal("expected an improving adjacent swap")
	}
	if got := fmt.Sprint(layers[1]); got != "[x y]" {
		t.Fatalf("bottom order=%s, want [x y]", got)
	}
	if got := countCrossingsBetweenLayers(layers[0], layers[1], adj); got != 0 {
		t.Fatalf("crossings=%d after transpose, want 0", got)
	}

	// A second run is a no-op: strict improvement and stable ties make the
	// optimizer idempotent.
	if transposeLayer(layers, adj, nil, 1, false) {
		t.Fatal("already optimal layer changed on the second run")
	}
}

func TestTransposeLayerPreservesPrimarySpine(t *testing.T) {
	layers := [][]string{{"a", "b"}, {"x", "y"}}
	adj := map[string][]string{
		"a": {"y", "x"},
		"b": {"x"},
	}
	primary := map[edgePair]bool{{from: "a", to: "x"}: true}

	// Swapping x/y removes the secondary crossing, but it moves the primary
	// a->x edge off its straight column, so Corezoid business-flow semantics
	// win and the swap is rejected.
	if transposeLayer(layers, adj, primary, 1, false) {
		t.Fatal("transpose moved the primary spine for a secondary crossing")
	}
	if got := fmt.Sprint(layers[1]); got != "[x y]" {
		t.Fatalf("bottom order=%s, want preserved [x y]", got)
	}
}

// TestErrRailCapsGapForDistantOwner: the right-hand error rail normally sits
// beside the row of the owners that trigger it (layout_sugiyama.go's
// layoutPartitioned, "2) error clusters on the right rail"). When two owners
// groups are many layers apart on the main spine, chasing each group's row
// verbatim opens a multi-thousand-pixel gap in the rail. layMaxRailGap caps
// that: this test builds a 20-node chain whose first/second and last/penultimate
// nodes feed two SHARED error finals, far apart in rank, and asserts the
// two error nodes land close together instead of mirroring the ~20*rowStep
// distance between their owners.
func TestErrRailCapsGapForDistantOwner(t *testing.T) {
	g := &fixGen{}
	fin := g.final("Final", true)
	errA := g.final("Error A", false)
	errB := g.final("Error B", false)

	const chainLen = 20
	ids := make([]string, chainLen)
	nodes := []map[string]interface{}{fin, errA, errB}
	nxt := nodeStr(fin, "id")
	for i := chainLen; i >= 1; i-- {
		var err string
		switch i {
		case 1, 2:
			err = nodeStr(errA, "id")
		case chainLen - 1, chainLen:
			err = nodeStr(errB, "id")
		}
		n := g.code(fmt.Sprintf("n%d", i), nxt, err)
		ids[i-1] = nodeStr(n, "id")
		nodes = append(nodes, n)
		nxt = nodeStr(n, "id")
	}
	st := g.start(ids[0])
	nodes = append(nodes, st)

	e := &layoutEngine{density: "medium"}
	coords := e.layoutPartitioned(nodes)

	ay := coords[nodeStr(errA, "id")].Y
	by := coords[nodeStr(errB, "id")].Y
	gap := by - ay
	if gap < 0 {
		gap = -gap
	}
	// The owners (n1, n20) are ~20 layers apart, i.e. ~20*layRowStep=4400px on
	// the main spine. Without the cap, the rail would mirror that distance.
	maxAllowed := layMaxRailGap + 400 // cluster extent + slack, well under 4400
	if gap > maxAllowed {
		t.Errorf("error clusters %dpx apart, want <= %dpx — rail-gap cap not applied", gap, maxAllowed)
	}

	if n := countOverlaps(coords, buildLayoutGraph(nodes)); n != 0 {
		t.Errorf("%d overlaps after capped rail placement", n)
	}
}

func TestPartitionedPinsDedicatedErrorClustersToOwner(t *testing.T) {
	nodes := deepCopyNodes(t, topoErrheavy())
	g := buildLayoutGraph(nodes)
	coords := (&layoutEngine{density: "medium"}).layoutPartitioned(nodes)

	for _, owner := range g.ids {
		op, ok := coords[owner]
		if !ok {
			continue
		}
		for _, root := range g.errors[owner] {
			rp, ok := coords[root]
			if !ok {
				t.Fatalf("error root %s was not placed", root)
			}
			// The entry itself is one local column to the right; centering the
			// collapsed square adds layCollapsedXOffset.
			if dx := rp.X - op.X; dx < 0 || dx > layErrDX+layCollapsedXOffset+40 {
				t.Errorf("dedicated root %s is %dpx from owner %s; want one local column", root, dx, owner)
			}
			if dy := rp.Y - op.Y; dy < 0 || dy > layRowStep {
				t.Errorf("dedicated root %s is %dpx below owner %s; want a local row", root, dy, owner)
			}
		}
	}
	if n := countOverlaps(coords, buildLayoutGraph(nodes)); n != 0 {
		t.Fatalf("%d overlaps after dedicated-cluster pinning", n)
	}
}
