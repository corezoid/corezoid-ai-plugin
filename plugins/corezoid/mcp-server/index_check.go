package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StaleReport describes the delta between the on-disk index and the current
// state of the project's .conv.json files. All three slices carry conv_ids as
// strings. Empty slices in all three positions means the index is fresh.
type StaleReport struct {
	Changed []string `json:"changed"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// CheckIndexFreshness answers the "is the index stale relative to disk?"
// question without rebuilding it. Cost model per TZ §8:
//   1. size+mtime match → skip (no hash read)
//   2. size or mtime differs → recompute hash, compare with stored hash
//   3. only hash differs → mark stale
//
// A bare mtime change is deliberately NOT enough to declare stale:
// `git checkout` and `pull-folder` both stamp mtimes without changing bytes,
// so a "by mtime only" check false-fires on every rebase. The two-step
// filter buys us "fast in the common case, correct in the edge case".
//
// Returns (report, false) when there is no index yet (removed=nil,
// added=every .conv.json on disk) and (nil, true, nil) is not used — errors
// are surfaced through the error return.
func CheckIndexFreshness(projectRoot string) (*StaleReport, error) {
	rpt := &StaleReport{}
	path := filepath.Join(projectRoot, IndexOutputDir, IndexMapFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// No index yet — every conv.json on disk is "added" and there's
		// nothing to be removed or changed.
		conv, err := listConvFiles(projectRoot)
		if err != nil {
			return nil, err
		}
		for cid := range conv {
			rpt.Added = append(rpt.Added, cid)
		}
		rpt.Added = uniqueSorted(rpt.Added)
		return rpt, nil
	}
	if err != nil {
		return nil, err
	}
	var pm ProjectMap
	if err := json.Unmarshal(data, &pm); err != nil {
		return nil, fmt.Errorf("parsing existing project-map.json: %v", err)
	}

	current, err := listConvFiles(projectRoot)
	if err != nil {
		return nil, err
	}

	// Removed: in index but no longer on disk.
	for cid := range pm.Processes {
		if _, ok := current[cid]; !ok {
			rpt.Removed = append(rpt.Removed, cid)
		}
	}
	// Added: on disk but not in index.
	for cid := range current {
		if _, ok := pm.Processes[cid]; !ok {
			rpt.Added = append(rpt.Added, cid)
		}
	}
	// Changed: on disk and in index, but content differs. Two-phase check.
	for cid, cur := range current {
		prev, ok := pm.Processes[cid]
		if !ok {
			continue
		}
		info, err := os.Stat(cur)
		if err != nil {
			continue
		}
		mtime := info.ModTime().UTC().Format(time.RFC3339Nano)
		if mtime == prev.MTime {
			continue // fast path: mtime unchanged → assume content unchanged
		}
		h, err := hashFile(cur)
		if err != nil {
			continue
		}
		if h != prev.Hash {
			rpt.Changed = append(rpt.Changed, cid)
		}
	}

	rpt.Changed = uniqueSorted(rpt.Changed)
	rpt.Added = uniqueSorted(rpt.Added)
	rpt.Removed = uniqueSorted(rpt.Removed)
	return rpt, nil
}

// listConvFiles walks the project and returns a conv_id -> absolute path map.
// Skips .corezoid/, .git/, node_modules/ for the same reason the builder does.
func listConvFiles(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == IndexOutputDir || base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".conv.json") {
			return nil
		}
		m := reProcessFileName.FindStringSubmatch(name)
		if m == nil {
			return nil
		}
		out[m[1]] = path
		return nil
	})
	return out, err
}

// FormatStaleReport renders a StaleReport as text suitable for MCP tool
// output. Deliberately terse — this is a check, not a full rebuild.
func FormatStaleReport(r *StaleReport) string {
	if r == nil {
		return "Index freshness: unknown"
	}
	total := len(r.Changed) + len(r.Added) + len(r.Removed)
	if total == 0 {
		return "Index freshness: up-to-date"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Index freshness: %d file(s) diverged\n", total)
	if len(r.Changed) > 0 {
		fmt.Fprintf(&sb, "  Changed (%d): %s\n", len(r.Changed), strings.Join(r.Changed, ", "))
	}
	if len(r.Added) > 0 {
		fmt.Fprintf(&sb, "  Added (%d): %s\n", len(r.Added), strings.Join(r.Added, ", "))
	}
	if len(r.Removed) > 0 {
		fmt.Fprintf(&sb, "  Removed (%d): %s\n", len(r.Removed), strings.Join(r.Removed, ", "))
	}
	sb.WriteString("Run build-project-index (or the corezoid-index skill) to refresh.")
	return sb.String()
}
