package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// baselineFileName is the per-folder sidecar recording, for each pulled
// process, the server version it was pulled at. push-process compares the live
// server version against this baseline to detect that someone else changed the
// process since the local copy was pulled (a lost-update / concurrent-edit
// conflict). Node ids are regenerated on every push, so the baseline is a
// server VERSION identity (change_time + last confirmed version), never node ids.
const baselineFileName = ".corezoid-baseline.json"

// ancestorDirName holds a copy of each process's scheme exactly as it was
// pulled — the common ancestor for a 3-way merge. When push detects the server
// moved, the merge engine (mergeplan.go) diffs base (this) vs theirs (a fresh
// export) vs mine (the local file) to tell a colleague's edit apart from mine
// and graft the non-conflicting ones. Kept as one file per process id so a big
// scheme never bloats the small version sidecar. Add to .gitignore.
const ancestorDirName = ".corezoid-baseline"

// ancestorPath is where a process's pulled-at scheme copy lives.
func ancestorPath(dir string, procID int) string {
	return filepath.Join(dir, ancestorDirName, strconv.Itoa(procID)+".json")
}

// writeAncestorScheme stores the pulled conv JSON as the merge ancestor.
// Best-effort: callers treat a failure as "3-way merge unavailable", never a
// pull failure.
func writeAncestorScheme(dir string, procID int, convJSON string) error {
	sub := filepath.Join(dir, ancestorDirName)
	if err := os.MkdirAll(sub, 0755); err != nil {
		return err
	}
	return os.WriteFile(ancestorPath(dir, procID), []byte(convJSON), 0644)
}

// readAncestorScheme returns the stored ancestor conv JSON; ok is false when
// none was recorded (pre-feature file, or capture failed at pull time).
func readAncestorScheme(dir string, procID int) (string, bool) {
	b, err := os.ReadFile(ancestorPath(dir, procID))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// baselineEntry is one process's pulled-at version identity.
type baselineEntry struct {
	ChangeTime int64 `json:"change_time"`         // server process last-modified (advances on every server commit)
	Version    int64 `json:"version"`             // last_confirmed_version (fallback: commits.version)
	PulledAt   int64 `json:"pulled_at,omitempty"` // when this baseline was recorded (unix, for diagnostics)
}

// readBaselines loads the folder's baseline sidecar. A missing or corrupt file
// yields an empty map — the caller treats "no baseline" as "cannot verify",
// never as an error.
func readBaselines(dir string) map[string]baselineEntry {
	m := map[string]baselineEntry{}
	b, err := os.ReadFile(filepath.Join(dir, baselineFileName))
	if err != nil {
		return m
	}
	_ = json.Unmarshal(b, &m) // corrupt content leaves m empty, which is the safe default
	return m
}

// writeBaseline upserts one process's baseline into the folder sidecar,
// preserving the other entries.
func writeBaseline(dir string, procID int, e baselineEntry) error {
	m := readBaselines(dir)
	m[strconv.Itoa(procID)] = e
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, baselineFileName)
	return os.WriteFile(path, append(b, '\n'), 0644)
}

// lookupBaseline returns the recorded baseline for a process; ok is false when
// none exists (never pulled, or a pre-feature local file).
func lookupBaseline(dir string, procID int) (baselineEntry, bool) {
	e, ok := readBaselines(dir)[strconv.Itoa(procID)]
	return e, ok
}

// baselineFromServer extracts the version identity of a process from a
// GetProcessByID (list conv) response. Prefers last_confirmed_version; falls
// back to commits.version. PulledAt is stamped now.
func baselineFromServer(proc map[string]any) baselineEntry {
	e := baselineEntry{PulledAt: time.Now().Unix()}
	if ct, ok := proc["change_time"].(float64); ok {
		e.ChangeTime = int64(ct)
	}
	if lcv, ok := proc["last_confirmed_version"].(float64); ok && lcv > 0 {
		e.Version = int64(lcv)
	} else if commits, ok := proc["commits"].(map[string]any); ok {
		if v, ok := commits["version"].(float64); ok {
			e.Version = int64(v)
		}
	}
	return e
}

// captureFolderBaselines records a pull baseline for every *.conv.json under
// root (a folder pull writes a ZIP export that carries no version metadata, so
// each file's current server version is fetched here). Best-effort: per-file
// failures are logged, never fatal. Returns how many baselines were recorded.
func captureFolderBaselines(v *Executor, root string) int {
	n := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".conv.json") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var doc map[string]any
		if json.Unmarshal(b, &doc) != nil {
			return nil
		}
		f, ok := doc["obj_id"].(float64)
		if !ok || int(f) == 0 {
			return nil
		}
		objID := int(f)
		proc, gerr := v.GetProcessByID(objID)
		if gerr != nil {
			logger.Warn("pull-folder: baseline fetch failed for %d: %v", objID, gerr)
			return nil
		}
		if werr := writeBaseline(filepath.Dir(path), objID, baselineFromServer(proc)); werr != nil {
			logger.Warn("pull-folder: baseline write failed for %d: %v", objID, werr)
			return nil
		}
		// The freshly pulled file content is the merge ancestor.
		if aerr := writeAncestorScheme(filepath.Dir(path), objID, string(b)); aerr != nil {
			logger.Warn("pull-folder: ancestor write failed for %d: %v", objID, aerr)
		}
		n++
		return nil
	})
	return n
}

// serverMovedSince reports whether the server's current version has advanced
// past the recorded baseline — i.e. someone committed a change since the pull.
// change_time is the primary signal (it advances on every server commit);
// version is the tiebreak when timestamps collide within a second.
func serverMovedSince(base baselineEntry, current baselineEntry) bool {
	if current.ChangeTime != base.ChangeTime {
		return current.ChangeTime > base.ChangeTime
	}
	return current.Version != base.Version
}
