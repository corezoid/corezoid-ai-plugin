package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"
)

// fixMojibake fixes filenames that were double-encoded: the server encodes a UTF-8
// Cyrillic name to bytes, then re-encodes each byte as a Latin-1 code point back into
// UTF-8, producing garbage like "Ð¿Ð¾Ð³Ð¾Ð´Ð°" instead of "погода".
// To reverse: cast each rune back to a byte, then validate the result as UTF-8.
func fixMojibake(s string) string {
	runes := []rune(s)
	bs := make([]byte, len(runes))
	for i, r := range runes {
		if r > 0xFF {
			return s // contains non-Latin-1 rune — not mojibake
		}
		bs[i] = byte(r)
	}
	if utf8.Valid(bs) && string(bs) != s {
		return string(bs)
	}
	return s
}

// windowsFileNames selects the Windows file-name rules in portableEntryPath.
// A variable rather than a constant so tests on other OSes can exercise them.
var windowsFileNames = runtime.GOOS == "windows"

// maxFileNameBytes caps a single path component. 255 bytes is the limit on
// Linux and macOS, and stays within the 255 UTF-16 units Windows allows, since
// no character takes more UTF-16 units than UTF-8 bytes.
const maxFileNameBytes = 255

// windowsReservedNames are device names Windows refuses as a file stem,
// whatever the extension ("CON.json" is as invalid as "CON").
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// exportNameSuffixes are the multi-dot suffixes Corezoid's exporter puts on
// entry names. Truncation keeps them intact, because file discovery and the
// .folder/.stage/.project unwrapping both match on them.
var exportNameSuffixes = []string{
	".conv.json", ".folder.json", ".stage.json", ".project.json", ".dashboard.json",
	".folder", ".stage", ".project",
}

// portableEntryPath rewrites a zip entry name so every component can be
// created on the local file system. Corezoid builds entry names from object
// titles, so a process called `say "hi"` arrives as `123_say "hi".conv.json`,
// which Windows rejects. On Windows, the characters it reserves (<>:"|?* and
// control characters) become "_", trailing dots and spaces are trimmed, and
// device names (CON, NUL, …) get a "_" appended. On every OS, a component
// longer than maxFileNameBytes is shortened at the end of its title, keeping
// the "<id>_" prefix that ties the file to its object and the export suffix.
//
// Other OSes keep those characters as they are, so existing workspaces there
// do not see their files renamed on the next pull.
func portableEntryPath(name string, windows bool) string {
	if windows {
		// On Windows filepath.Join treats a backslash as a separator, so a
		// backslash in an entry name already split it; keep that behaviour.
		name = strings.ReplaceAll(name, `\`, "/")
	}
	parts := strings.Split(name, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			continue
		}
		parts[i] = portableComponent(p, windows)
	}
	return strings.Join(parts, "/")
}

func portableComponent(p string, windows bool) string {
	if windows {
		var b strings.Builder
		b.Grow(len(p))
		for _, r := range p {
			if r < 0x20 || strings.ContainsRune(`<>:"|?*`, r) {
				b.WriteByte('_')
			} else {
				b.WriteRune(r)
			}
		}
		p = strings.TrimRight(b.String(), ". ")
		stem, rest, hasDot := strings.Cut(p, ".")
		if windowsReservedNames[strings.ToUpper(strings.TrimRight(stem, " "))] {
			p = stem + "_"
			if hasDot {
				p += "." + rest
			}
		}
		if p == "" {
			p = "_"
		}
	}
	if len(p) > maxFileNameBytes {
		suffix := filepath.Ext(p)
		for _, s := range exportNameSuffixes {
			if strings.HasSuffix(p, s) {
				suffix = s
				break
			}
		}
		if len(suffix) >= maxFileNameBytes {
			suffix = ""
		}
		stem := p[:len(p)-len(suffix)]
		limit := maxFileNameBytes - len(suffix)
		// Back off to a rune boundary so the name stays valid UTF-8.
		for limit > 0 && !utf8.RuneStart(stem[limit]) {
			limit--
		}
		stem = stem[:limit]
		if windows {
			stem = strings.TrimRight(stem, ". ")
		}
		p = stem + suffix
	}
	return p
}

// unzipFile extracts a ZIP archive to destDir using Go's archive/zip package.
// Applies fixMojibake to entry names to handle servers that double-encode UTF-8
// filenames, then portableEntryPath so object titles with characters the local
// file system rejects still produce a file.
//
// An entry whose directory or file still cannot be created is skipped rather
// than fatal: one oddly named process must not cost the user the rest of the
// folder. Skipped entries are logged and returned so the caller can report
// them. The security rejections below stay fatal.
//
// Two separate containment checks run per entry, because they stop two
// different things. The ".." / absolute-path check rejects hostile names
// inside the ARCHIVE. The symlink checks reject a hostile (or merely
// surprising) link that already exists in the DESTINATION tree: os.Create
// follows symlinks, so without them an entry named "docs/x.json" writes
// wherever "docs" happens to point, and a plain name whose file is already a
// symlink overwrites the link's target. Neither is reachable through the
// archive-name check, since the name itself is perfectly ordinary.
func unzipFile(src, destDir string) (skipped []string, err error) {
	r, err := zip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip %s: %w", src, err)
	}
	defer r.Close()

	// Create the root before resolving it. Every caller today writes the zip
	// into destDir first, so it always exists — but the previous implementation
	// created it implicitly via MkdirAll on the first entry's parent, and
	// resolving a path that does not exist is an error. Without this line a
	// caller that stops pre-creating the directory would get "failed to resolve
	// extraction root" instead of an extraction.
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create extraction root %s: %w", destDir, err)
	}

	// Resolve the root once. The workspace itself may legitimately sit under a
	// symlink — /tmp -> /private/tmp on macOS is the common case — so every
	// check below compares a resolved path against a resolved root. Comparing
	// raw strings would reject those ordinary setups.
	rootReal, err := filepath.EvalSymlinks(destDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve extraction root %s: %w", destDir, err)
	}

	skip := func(name string, cause error) {
		logger.Warn("unzip: skipped %s: %v", name, cause)
		skipped = append(skipped, fmt.Sprintf("%s (%v)", name, cause))
	}

	for _, f := range r.File {
		// Sanitize before the zip-slip check: trimming a trailing space can
		// turn ".. " into "..", and the check must see the final name.
		name := portableEntryPath(fixMojibake(f.Name), windowsFileNames)
		// Guard against zip-slip: reject absolute paths and paths with ".." components.
		cleaned := filepath.Clean(name)
		if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
			return skipped, fmt.Errorf("illegal zip path: %s", name)
		}
		destPath := filepath.Join(destDir, name)

		// Check the deepest ancestor that already exists BEFORE creating
		// anything: MkdirAll through a symlinked directory would happily
		// create the missing components on the far side of the link, so
		// checking afterwards would report an escape we had already made.
		if err := ensureInsideRoot(filepath.Dir(destPath), rootReal); err != nil {
			return skipped, fmt.Errorf("illegal zip path %s: %w", name, err)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				skip(name, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			skip(name, err)
			continue
		}

		// The parent is now real directories all the way down, but the leaf
		// itself can still be a pre-existing symlink pointing anywhere.
		if fi, lerr := os.Lstat(destPath); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return skipped, fmt.Errorf("illegal zip path %s: %s already exists as a symlink; refusing to write through it", name, destPath)
		}

		rc, err := f.Open()
		if err != nil {
			return skipped, fmt.Errorf("failed to open zip entry %s: %w", name, err)
		}
		out, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			skip(name, err)
			continue
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return skipped, fmt.Errorf("failed to extract %s: %w", name, copyErr)
		}
	}
	return skipped, nil
}

// ensureInsideRoot reports an error unless dir — with every symlink resolved —
// is rootReal or lives beneath it.
//
// dir need not exist yet: the check walks up to the closest ancestor that does
// and resolves that instead. Everything below an existing, contained ancestor
// is created by MkdirAll as real directories, so containment of the ancestor
// implies containment of the whole path.
func ensureInsideRoot(dir, rootReal string) error {
	probe := dir
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if real != rootReal && !strings.HasPrefix(real, rootReal+string(os.PathSeparator)) {
				return fmt.Errorf("%s resolves to %s, outside the extraction root %s", probe, real, rootReal)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to resolve %s: %w", probe, err)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			// Walked past the filesystem root without finding anything that
			// exists. Cannot happen for a path under destDir, which we already
			// resolved, but returning an error beats looping forever.
			return fmt.Errorf("no existing ancestor of %s to resolve", dir)
		}
		probe = parent
	}
}

// findStageDir looks for the root content directory inside an extracted Corezoid
// ZIP up to maxDepth levels deep. Matches *.stage, *.folder, and *.project —
// Corezoid uses different suffixes depending on the exported obj_type.
func findStageDir(root string, maxDepth int) (string, error) {
	var found string
	err := walkDepth(root, 0, maxDepth, func(path string, d os.DirEntry) bool {
		if d.IsDir() {
			name := d.Name()
			if strings.HasSuffix(name, ".stage") ||
				strings.HasSuffix(name, ".folder") ||
				strings.HasSuffix(name, ".project") {
				found = path
				return true // stop
			}
		}
		return false
	})
	return found, err
}

func walkDepth(dir string, depth, maxDepth int, fn func(string, os.DirEntry) bool) error {
	if depth > maxDepth {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Skip directories that the OS refuses to list (e.g. macOS .Trash when
		// the MCP server is launched from $HOME). Returning nil lets the caller
		// continue scanning other siblings instead of aborting.
		if os.IsPermission(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		// Skip hidden/system directories (names starting with ".") such as
		// .Trash, .Spotlight-V100, .TemporaryItems on macOS. The *.stage
		// directories we are looking for never begin with a dot, so this
		// filter is safe and avoids permission errors at $HOME depth.
		if e.IsDir() && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if fn(p, e) {
			return nil
		}
		if e.IsDir() {
			if err := walkDepth(p, depth+1, maxDepth, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// moveContents moves all entries from src directory into dst directory,
// removing any pre-existing entry at the destination first so a re-pull
// wins over stale contents from a previous fetch.
func moveContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if _, statErr := os.Lstat(dstPath); statErr == nil {
			if err := os.RemoveAll(dstPath); err != nil {
				return fmt.Errorf("clear stale %s: %w", dstPath, err)
			}
		}
		if err := os.Rename(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

// exportWrapperDirRe matches every top-level wrapper directory Corezoid's
// exporter emits — one per object type. Despite the ".zip" suffix these are
// DIRECTORIES, not files. Stage pulls only produce "stage_*" wrappers;
// workspace-root pulls ("No Project" mode) also emit "folder_*", "conv_*",
// and "dashboard_*" wrappers, one per exported top-level object.
var exportWrapperDirRe = regexp.MustCompile(`^(stage|folder|conv|dashboard)_\d+_\d+\.zip$`)

// hoistZipWrapperDirs unwraps every top-level wrapper directory in root
// (see exportWrapperDirRe). Each wrapper's contents are moved up to root;
// existing entries at the destination are removed first so a fresh pull
// always wins over stale data.
func hoistZipWrapperDirs(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || !exportWrapperDirRe.MatchString(e.Name()) {
			continue
		}
		wrapper := filepath.Join(root, e.Name())
		inner, err := os.ReadDir(wrapper)
		if err != nil {
			return fmt.Errorf("read wrapper %s: %w", wrapper, err)
		}
		for _, ie := range inner {
			src := filepath.Join(wrapper, ie.Name())
			dst := filepath.Join(root, ie.Name())
			if _, statErr := os.Lstat(dst); statErr == nil {
				if err := os.RemoveAll(dst); err != nil {
					return fmt.Errorf("clear stale %s: %w", dst, err)
				}
			}
			if err := os.Rename(src, dst); err != nil {
				return fmt.Errorf("hoist %s -> %s: %w", src, dst, err)
			}
		}
		if err := os.RemoveAll(wrapper); err != nil {
			return fmt.Errorf("remove wrapper %s: %w", wrapper, err)
		}
	}
	return nil
}

// downloadWorkspaceRootRecursively downloads every top-level object (folder,
// conv, dashboard) in the current workspace and extracts each into filePath.
// Used when the user picked "No Project" during login — the workspace has no
// stage root, so we mirror what the Corezoid UI's root view shows straight
// into the workspace directory.
//
// The returned list names archive entries that could not be written locally
// (see unzipFile); the rest of the workspace is still saved.
func downloadWorkspaceRootRecursively(e *Executor, filePath string) ([]string, error) {
	if err := e.checkCancel(); err != nil {
		return nil, err
	}

	items, err := e.ListWorkspaceRoot()
	if err != nil {
		return nil, fmt.Errorf("failed to list workspace root: %w", err)
	}
	if len(items) == 0 {
		return nil, nil
	}

	urls, err := e.BatchExportSchemes(items)
	if err != nil {
		return nil, fmt.Errorf("failed to batch export workspace root: %w", err)
	}

	var skipped []string
	for i, url := range urls {
		if err := e.checkCancel(); err != nil {
			return skipped, err
		}
		data, err := e.fetchDownload(url)
		if err != nil {
			return skipped, fmt.Errorf("failed to download %s #%d: %w", items[i].ObjType, items[i].ObjID, err)
		}
		zipPath := filepath.Join(filePath, fmt.Sprintf(".root_%s_%d.zip", items[i].ObjType, items[i].ObjID))
		if err := os.WriteFile(zipPath, data, 0644); err != nil {
			return skipped, fmt.Errorf("failed to write zip: %w", err)
		}
		s, unzipErr := unzipFile(zipPath, filePath)
		skipped = append(skipped, s...)
		os.Remove(zipPath)
		if unzipErr != nil {
			return skipped, fmt.Errorf("failed to unzip %s #%d: %w", items[i].ObjType, items[i].ObjID, unzipErr)
		}
	}

	// Corezoid's exporter wraps each top-level object in a
	// {stage,folder,conv,dashboard}_<id>_<ts>.zip wrapper directory. Unwrap
	// them so folder / conv / dashboard files land directly at filePath.
	if err := hoistZipWrapperDirs(filePath); err != nil {
		return skipped, fmt.Errorf("failed to unwrap workspace-root archives: %w", err)
	}

	// Some archives contain inner *.zip files that also need extracting.
	// Loop until none remain (they can nest).
	innerZipRe := exportWrapperDirRe
	for {
		files, err := os.ReadDir(filePath)
		if err != nil {
			return skipped, fmt.Errorf("failed to read directory: %w", err)
		}
		var innerZip string
		for _, f := range files {
			if !f.IsDir() && innerZipRe.MatchString(f.Name()) {
				innerZip = filepath.Join(filePath, f.Name())
				break
			}
		}
		if innerZip == "" {
			break
		}
		s, err := unzipFile(innerZip, filePath)
		skipped = append(skipped, s...)
		os.Remove(innerZip)
		if err != nil {
			return skipped, fmt.Errorf("failed to unzip %s: %w", filepath.Base(innerZip), err)
		}
	}

	if err := renameFiles2Folders(filePath); err != nil {
		return skipped, fmt.Errorf("failed to rename files: %w", err)
	}
	if err := formatJSONWithFallback(e, filePath); err != nil {
		return skipped, fmt.Errorf("failed to format json: %w", err)
	}
	return skipped, nil
}

// downloadStageRecursively pulls folderID into filePath. The returned list
// names archive entries that could not be written locally (see unzipFile);
// the rest of the folder is still saved.
func downloadStageRecursively(e *Executor, folderID int, filePath string) ([]string, error) {
	if err := e.checkCancel(); err != nil {
		return nil, err
	}
	// If folderID matches the configured stage root, we know it's a stage —
	// no need to probe. Otherwise try folder first (common case for sub-folders),
	// then stage and project as fallbacks.
	var objTypes []string
	if e.StageID != 0 && folderID == e.StageID {
		objTypes = []string{"stage"}
	} else {
		objTypes = []string{"folder", "stage", "project"}
	}
	var data []byte
	var err error
	for _, objType := range objTypes {
		data, err = e.PullZip(folderID, objType)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to PullZip: %w", err)
	}
	zipPath := filePath + "/stage.zip"
	err = os.WriteFile(zipPath, data, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %v", err)
	}
	defer os.Remove(zipPath)
	skipped, err := unzipFile(zipPath, filePath)
	if err != nil {
		return skipped, fmt.Errorf("failed to unzip: %w", err)
	}

	// Unzip any inner zip files: stage_<id>_<id>.zip (may be nested)
	innerZipRe := regexp.MustCompile(`^stage_\d+_\d+\.zip$`)
	for {
		files, err := os.ReadDir(filePath)
		if err != nil {
			return skipped, fmt.Errorf("failed to read directory: %v", err)
		}
		var innerZip string
		for _, f := range files {
			if !f.IsDir() && innerZipRe.MatchString(f.Name()) {
				innerZip = filepath.Join(filePath, f.Name())
				break
			}
		}
		if innerZip == "" {
			break
		}
		s, err := unzipFile(innerZip, filePath)
		skipped = append(skipped, s...)
		os.Remove(innerZip)
		if err != nil {
			return skipped, fmt.Errorf("failed to unzip %s: %w", filepath.Base(innerZip), err)
		}
	}

	// Hoist contents out of any wrapper *directory* that Corezoid exports
	// wrap around the real stage (e.g. "stage_671255_1785764880374.zip/" —
	// note it is a directory, not a file, because Corezoid's exporter names
	// the top-level entry after the download filename). Left in place, these
	// wrappers accumulate on every re-pull because findStageDir/hoist below
	// prefer the stale .stage that lives directly at filePath and skip the
	// wrapper next to it. Doing this before findStageDir guarantees the
	// freshly-extracted stage lands at filePath, overwriting any prior copy.
	if err := hoistZipWrapperDirs(filePath); err != nil {
		return skipped, fmt.Errorf("failed to unwrap stage archive: %w", err)
	}

	// Find .stage directory anywhere up to depth 2: <id>_<name>.stage
	stageDir, err := findStageDir(filePath, 2)
	if err != nil {
		return skipped, fmt.Errorf("failed to find stage dir: %v", err)
	}
	if stageDir == "" {
		return skipped, fmt.Errorf("stage directory not found (*.stage / *.folder / *.project) — " +
			"if COREZOID_WORK_DIR (or the MCP server launch directory) is $HOME " +
			"or another large directory, set it to a dedicated project folder and " +
			"restart Claude Code / the MCP server")
	}
	// Flatten the stage into filePath: move the contents of the
	// <id>_<name>.{stage,folder,project} directory up so subfolders and the
	// stage marker JSON land directly at the workspace root. moveContents
	// overwrites any stale entry at the destination — a re-pull always wins.
	// Then drop the (now empty) wrapper along with any intermediate directory
	// the exporter placed above it.
	if stageDir != filePath {
		if err := moveContents(stageDir, filePath); err != nil {
			return skipped, fmt.Errorf("failed to flatten stage into workspace: %v", err)
		}
		wrapper := stageDir
		for wrapper != filePath {
			parent := filepath.Dir(wrapper)
			if err := os.RemoveAll(wrapper); err != nil {
				return skipped, fmt.Errorf("failed to remove stage wrapper: %v", err)
			}
			if parent == filePath {
				break
			}
			wrapper = parent
		}
	}
	// renameFiles2Folders strips the .folder/.project/.stage suffix from
	// every directory under filePath, including any nested stage the user
	// previously pulled at a different depth.
	err = renameFiles2Folders(filePath)
	if err != nil {
		return skipped, fmt.Errorf("failed to rename files: %v", err)
	}
	err = formatJSONWithFallback(e, filePath)
	if err != nil {
		return skipped, fmt.Errorf("failed to format json: %v", err)
	}

	return skipped, nil
}

func renameFiles2Folders(filePath string) error {
	// теперь везде где json файлы форматировать их через MarshalIndent
	files, err := os.ReadDir(filePath)
	if err != nil {
		return fmt.Errorf("Failed to read directory2: %v", err)
	}

	for _, f := range files {
		if f.IsDir() {
			dirName := f.Name()
			// Strip every Corezoid export suffix — the workspace root is
			// itself the stage root now, so nested directories should carry
			// only the plain "<id>_<name>" form.
			newName := strings.TrimSuffix(dirName, ".folder")
			newName = strings.TrimSuffix(newName, ".project")
			newName = strings.TrimSuffix(newName, ".stage")
			newPath := filepath.Join(filePath, newName)
			if newName != dirName {
				oldPath := filepath.Join(filePath, dirName)
				if _, err := os.Stat(newPath); err == nil {
					if err = os.RemoveAll(newPath); err != nil {
						return fmt.Errorf("failed to remove existing directory %s: %v", newPath, err)
					}
				}
				err = os.Rename(oldPath, newPath)
				if err != nil {
					return fmt.Errorf("failed to rename directory: %v", err)
				}
			}
			err = formatJSON(newPath)
			if err != nil {
				return fmt.Errorf("Failed to format json in directory %s: %v", newPath, err)
			}
			err = renameFiles2Folders(newPath)
			if err != nil {
				return fmt.Errorf("Failed to rename files in directory: %v", err)
			}
		}

	}
	return nil
}

func formatJSON(filePath string) error {
	return formatJSONWithFallback(nil, filePath)
}

// formatJSONWithFallback formats all JSON files under filePath, pretty-printing
// them and stripping uuid fields. When e is non-nil, it also applies a fallback
// for undeployed processes whose scheme.nodes is empty: it fetches nodes via the
// list API (GetProcessNodes) and patches the file in place before writing.
func formatJSONWithFallback(e *Executor, filePath string) error {
	// теперь везде где json файлы форматировать их через MarshalIndent
	files, err := os.ReadDir(filePath)
	if err != nil {
		return fmt.Errorf("Failed to read directory2: %v", err)
	}

	for _, f := range files {
		if f.IsDir() {
			subDir := filepath.Join(filePath, f.Name())
			//fmt.Println("Downloaded folder", subDir)
			err := formatJSONWithFallback(e, subDir)
			if err != nil {
				return fmt.Errorf("Failed to format json in directory %s: %v", subDir, err)
			}
		}
		if filepath.Ext(f.Name()) != ".json" {
			continue
		}
		filePath1 := filepath.Join(filePath, f.Name())
		dataJson, err := os.ReadFile(filePath1)
		if err != nil {
			return fmt.Errorf("Failed to read file: %v", err)
		}
		var dataRsp any
		err = json.Unmarshal(dataJson, &dataRsp)
		if err != nil {
			return fmt.Errorf("failed to unmarshal file %s: %v", filePath1, err)
		}
		// везде где есть uuid в scheme.nodes объект удалить
		if procMap, ok := dataRsp.(map[string]interface{}); ok {
			if schemeMap, ok := procMap["scheme"].(map[string]interface{}); ok {
				// nodes1 is nil when the key is absent and empty when the ZIP
				// contains "nodes": [] — both mean "no nodes", so treat them the
				// same via len() and trigger the fallback in either case, matching
				// ExportProcess.
				nodes1, _ := schemeMap["nodes"].([]interface{})
				if len(nodes1) == 0 && e != nil {
					// Fallback for undeployed processes: scheme.nodes is absent or empty
					// (export_process returns "nodes": [] for imported-but-never-deployed
					// processes). Look up the process ID from the file and fetch nodes via
					// the list API.
					if rawID, ok := procMap["obj_id"].(float64); ok && rawID > 0 {
						procID := int(rawID)
						fallback := &Executor{
							Ctx:         e.Ctx,
							ProcessID:   procID,
							Token:       e.Token,
							APIUrl:      e.APIUrl,
							WorkspaceID: e.WorkspaceID,
							StageID:     e.StageID,
							Debug:       e.Debug,
							NodeIDMap:   make(map[string]NodeInfo),
						}
						fallbackNodes, ferr := fallback.GetProcessNodes()
						if ferr == nil && len(fallbackNodes) > 0 {
							nodes1 = fallbackNodes
							schemeMap["nodes"] = fallbackNodes
							logger.Info("pull-folder: used fallback nodes for undeployed process %d (%d nodes)", procID, len(fallbackNodes))
						}
					}
				}
				// Strip uuid from every node, including fallback nodes fetched via
				// the list API (pull-folder always writes uuid-free files).
				for _, node := range nodes1 {
					if nodeMap, ok := node.(map[string]interface{}); ok {
						delete(nodeMap, "uuid")
					}
				}
				procMap["scheme"] = schemeMap
				dataRsp = procMap
			}
		}

		// и в корне
		if d, ok := dataRsp.(map[string]interface{}); ok {
			delete(d, "uuid")
		}

		dataRspBin, err := json.MarshalIndent(dataRsp, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal file: %v", err)
		}
		err = os.WriteFile(filePath1, dataRspBin, 0644)
		if err != nil {
			return fmt.Errorf("failed to write file: %v", err)
		}
	}
	return nil
}
