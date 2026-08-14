package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
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
	return writeFileAtomically(ancestorPath(dir, procID), []byte(convJSON), 0644)
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

type corruptBaselineError struct {
	path string
	err  error
}

func (e *corruptBaselineError) Error() string { return fmt.Sprintf("parse %s: %v", e.path, e.err) }
func (e *corruptBaselineError) Unwrap() error { return e.err }

// readBaselines loads the folder's baseline sidecar. A missing file is a valid
// empty baseline; corrupt content is an error because silently treating it as
// empty would disable the concurrent-change gate.
func readBaselines(dir string) (map[string]baselineEntry, error) {
	m := map[string]baselineEntry{}
	path := filepath.Join(dir, baselineFileName)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, &corruptBaselineError{path: path, err: err}
	}
	if m == nil {
		m = map[string]baselineEntry{}
	}
	return m, nil
}

// writeBaseline upserts one process's baseline into the folder sidecar,
// preserving the other entries. The full read-modify-write is serialised both
// within this MCP process and across MCP processes, then committed atomically.
func writeBaseline(dir string, procID int, e baselineEntry) error {
	return writeBaselineLocked(dir, procID, e, false)
}

// writePulledBaseline may recover a corrupt sidecar because a successful pull
// has just produced a fresh authoritative snapshot. The corrupt file is kept
// beside it for diagnosis; non-pull writers never take this recovery path.
func writePulledBaseline(dir string, procID int, e baselineEntry) error {
	return writeBaselineLocked(dir, procID, e, true)
}

func writeBaselineLocked(dir string, procID int, e baselineEntry, repairCorrupt bool) error {
	path := filepath.Join(dir, baselineFileName)
	baselineWriteMu.Lock()
	defer baselineWriteMu.Unlock()

	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("lock baseline %s: %w", path, err)
	}
	defer func() { _ = lock.Unlock() }()

	m, err := readBaselines(dir)
	corruptBackup := ""
	if err != nil {
		var corrupt *corruptBaselineError
		if !repairCorrupt || !errors.As(err, &corrupt) {
			return err
		}
		backup := path + ".corrupt-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if renameErr := os.Rename(path, backup); renameErr != nil {
			return fmt.Errorf("archive corrupt baseline %s: %w", path, renameErr)
		}
		logger.Warn("pull: archived corrupt baseline %s as %s", path, backup)
		corruptBackup = backup
		m = map[string]baselineEntry{}
	}
	m[strconv.Itoa(procID)] = e
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := writeFileAtomically(path, append(b, '\n'), 0644); err != nil {
		if corruptBackup != "" {
			if restoreErr := os.Rename(corruptBackup, path); restoreErr != nil {
				return fmt.Errorf("%v; additionally failed to restore corrupt baseline: %w", err, restoreErr)
			}
		}
		return err
	}
	return nil
}

var baselineWriteMu sync.Mutex

// writeFileAtomically commits data through a same-directory temporary file so
// readers see either the old complete file or the new complete file.
func writeFileAtomically(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create parent for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := os.Chmod(tmpName, mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp for %s: %w", path, err)
	}
	renamed = true
	return nil
}

// lookupBaseline returns the recorded baseline for a process; ok is false when
// none exists (never pulled, or a pre-feature local file). A corrupt sidecar is
// returned as an error so push can fail closed instead of disabling the gate.
func lookupBaseline(dir string, procID int) (baselineEntry, bool, error) {
	m, err := readBaselines(dir)
	if err != nil {
		return baselineEntry{}, false, err
	}
	e, ok := m[strconv.Itoa(procID)]
	return e, ok, nil
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

// captureFolderBaselineSnapshot records process versions before a folder ZIP
// export starts. Capturing first is deliberate: a server commit during export
// must leave the downloaded file on an older baseline so the next push detects
// the race instead of silently accepting a mixed-time snapshot.
func captureFolderBaselineSnapshot(v *Executor, folderID int) map[int]baselineEntry {
	out := map[int]baselineEntry{}
	captureFolderChildren(v, folderID, out, map[int]bool{})
	return out
}

// captureWorkspaceBaselineSnapshot is the No Project equivalent: list the
// workspace root, capture top-level processes, then recurse through folders.
func captureWorkspaceBaselineSnapshot(v *Executor) map[int]baselineEntry {
	out := map[int]baselineEntry{}
	items, err := v.ListWorkspaceRoot()
	if err != nil {
		logger.Warn("pull-folder: baseline root list failed: %v", err)
		return out
	}
	visited := map[int]bool{}
	for _, item := range items {
		if err := v.checkCancel(); err != nil {
			break
		}
		switch item.ObjType {
		case "conv":
			captureProcessBaseline(v, item.ObjID, out)
		case "folder":
			captureFolderChildren(v, item.ObjID, out, visited)
		}
	}
	return out
}

func captureFolderChildren(v *Executor, folderID int, out map[int]baselineEntry, visited map[int]bool) {
	if folderID == 0 || visited[folderID] {
		return
	}
	visited[folderID] = true
	children, err := v.ListFolder(folderID)
	if err != nil {
		logger.Warn("pull-folder: baseline list failed for folder %d: %v", folderID, err)
		return
	}
	for _, child := range children {
		if err := v.checkCancel(); err != nil {
			return
		}
		switch child.Obj {
		case "conv":
			captureProcessBaseline(v, child.ObjID, out)
		case "folder":
			captureFolderChildren(v, child.ObjID, out, visited)
		}
	}
}

func captureProcessBaseline(v *Executor, procID int, out map[int]baselineEntry) {
	if procID == 0 {
		return
	}
	proc, err := v.GetProcessByID(procID)
	if err != nil {
		logger.Warn("pull-folder: baseline fetch failed for %d: %v", procID, err)
		return
	}
	out[procID] = baselineFromServer(proc)
}

// recordPulledBaselines pairs the pre-export snapshot with the files produced
// by that export. The pulled file itself becomes the merge ancestor. Processes
// absent from the pre-export snapshot intentionally get no baseline.
func recordPulledBaselines(root string, snapshot map[int]baselineEntry) int {
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
		// Only touch ancestor/baseline for processes this export actually
		// produced (i.e. present in the pre-export snapshot). Otherwise the
		// walk would rewrite the ancestor of an unrelated, locally-edited file
		// with its own WIP content, and a later 3-way merge would see
		// base == mine and silently drop the local edits.
		base, ok := snapshot[objID]
		if !ok {
			return nil
		}
		if aerr := writeAncestorScheme(filepath.Dir(path), objID, string(b)); aerr != nil {
			logger.Warn("pull-folder: ancestor write failed for %d: %v", objID, aerr)
		}
		if werr := writePulledBaseline(filepath.Dir(path), objID, base); werr != nil {
			logger.Warn("pull-folder: baseline write failed for %d: %v", objID, werr)
			return nil
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
