package main

import "sort"

// computeGraphStats populates the derived graph-analysis fields of ProjectMap
// — fan_in/fan_out per conv_id, high-* lists over thresholds, orphaned and
// entry_point classification (via the heuristic in IndexConfig), and cycle
// detection using DFS with a coloring algorithm.
//
// State processes (conv_type == "state") are excluded from orphaned /
// entry_points entirely: an empty state_stores[cid].written_by is a
// legitimate read-only state (a lookup table, or a cache not yet populated) —
// not a signal of orphan status. This distinction is load-bearing for
// corezoid-project-review, which must not surface state stores as "dead
// code" candidates.
func computeGraphStats(pm *ProjectMap, cfg IndexConfig) *GraphStats {
	stats := &GraphStats{
		HighFanIn:   []string{},
		HighFanOut:  []string{},
		Orphaned:    []OrphanedInfo{},
		EntryPoints: []string{},
		Cycles:      [][]string{},
	}

	// fanIn/fanOut are computed locally — they're needed for the high-*
	// lists and orphaned classification, but are no longer serialised into
	// project-map.json (they're per-process counters derivable from edges
	// via jq and were the biggest contributor to file size).
	fanIn := make(map[string]int, len(pm.Processes))
	fanOut := make(map[string]int, len(pm.Processes))
	for cid := range pm.Processes {
		fanIn[cid] = 0
		fanOut[cid] = 0
	}
	for _, e := range pm.Edges {
		fanOut[e.From]++
		fanIn[e.To]++
	}

	for cid, n := range fanIn {
		if n > IndexHighFanIn {
			stats.HighFanIn = append(stats.HighFanIn, cid)
		}
	}
	for cid, n := range fanOut {
		if n > IndexHighFanOut {
			stats.HighFanOut = append(stats.HighFanOut, cid)
		}
	}
	sort.Strings(stats.HighFanIn)
	sort.Strings(stats.HighFanOut)

	// Entry-point / orphaned classification only for real processes with no
	// internal caller. See TZ §7 — this is a heuristic, not a diagnosis, so
	// callers must phrase output as "no internal caller, verify before
	// deletion" and not "dead code".
	for cid, entry := range pm.Processes {
		if entry.ConvType == "state" {
			continue
		}
		if fanIn[cid] != 0 {
			continue
		}
		if isEntryPoint(entry, cfg) {
			stats.EntryPoints = append(stats.EntryPoints, cid)
		} else {
			stats.Orphaned = append(stats.Orphaned, OrphanedInfo{
				ConvID:         cid,
				SuspiciousName: cfg.isSuspiciousName(entry.Title),
			})
		}
	}
	sort.Strings(stats.EntryPoints)
	sort.Slice(stats.Orphaned, func(i, j int) bool {
		return stats.Orphaned[i].ConvID < stats.Orphaned[j].ConvID
	})

	stats.Cycles = findCycles(pm)
	return stats
}

func isEntryPoint(entry *ProcessEntry, cfg IndexConfig) bool {
	if len(entry.Aliases) > 0 {
		return true
	}
	if entry.HasReceiverNode {
		return true
	}
	if cfg.isEntryPointName(entry.Title) {
		return true
	}
	if cfg.isEntryPointLocation(entry.Location) {
		return true
	}
	return false
}

// findCycles returns a slice of simple cycles in the call graph. Uses DFS
// with recursion-stack tracking and reports each cycle exactly once,
// normalised so the smallest lexicographic conv_id starts the cycle.
//
// The earlier globalDone optimisation was dropped: it caused missed cycles
// when a node was reachable via multiple paths (e.g. A→B→C→A exists, but
// A→B→D→C→A was not found because C was already in globalDone). Correctness
// beats a minor performance win for the project sizes we support.
func findCycles(pm *ProjectMap) [][]string {
	adj := map[string][]string{}
	for _, e := range pm.Edges {
		adj[e.From] = append(adj[e.From], e.To)
	}
	// Deterministic neighbour order so cycle enumeration is reproducible.
	for k := range adj {
		sort.Strings(adj[k])
		adj[k] = uniqueSorted(adj[k])
	}

	// Gather starting nodes in deterministic order.
	starts := mapKeysSorted(pm.Processes)

	seen := map[string]struct{}{}
	// Initialised (not nil) so JSON serialises an empty result as [], not null.
	out := [][]string{}

	var dfs func(node string, stack []string, onStack map[string]int)
	dfs = func(node string, stack []string, onStack map[string]int) {
		onStack[node] = len(stack)
		stack = append(stack, node)
		for _, nb := range adj[node] {
			if idx, in := onStack[nb]; in {
				// Back edge — cycle detected.
				cyc := append([]string(nil), stack[idx:]...)
				norm := normaliseCycle(cyc)
				key := ""
				for _, n := range norm {
					key += n + "|"
				}
				if _, dup := seen[key]; !dup {
					seen[key] = struct{}{}
					out = append(out, norm)
				}
				continue
			}
			dfs(nb, stack, onStack)
		}
		delete(onStack, node)
	}

	for _, s := range starts {
		dfs(s, nil, map[string]int{})
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	return out
}

// normaliseCycle rotates a cycle so its lexicographically smallest node comes
// first. Two rotations of the same cycle will produce identical output — the
// key used for dedup in findCycles depends on this.
func normaliseCycle(cyc []string) []string {
	if len(cyc) == 0 {
		return cyc
	}
	minIdx := 0
	for i := 1; i < len(cyc); i++ {
		if cyc[i] < cyc[minIdx] {
			minIdx = i
		}
	}
	out := make([]string, len(cyc))
	for i := range cyc {
		out[i] = cyc[(minIdx+i)%len(cyc)]
	}
	return out
}
