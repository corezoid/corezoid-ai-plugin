package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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

// unzipFile extracts a ZIP archive to destDir using Go's archive/zip package.
// Applies fixMojibake to entry names to handle servers that double-encode UTF-8 filenames.
func unzipFile(src, destDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("failed to open zip %s: %w", src, err)
	}
	defer r.Close()

	for _, f := range r.File {
		name := fixMojibake(f.Name)
		// Guard against zip-slip: reject absolute paths and paths with ".." components.
		cleaned := filepath.Clean(name)
		if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("illegal zip path: %s", name)
		}
		destPath := filepath.Join(destDir, name)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open zip entry %s: %w", name, err)
		}
		out, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			return fmt.Errorf("failed to create file %s: %w", destPath, err)
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return fmt.Errorf("failed to extract %s: %w", name, copyErr)
		}
	}
	return nil
}

// findStageDir looks for the exported object's own directory up to maxDepth
// levels deep inside root: *.stage when exporting a stage root, *.folder when
// exporting a plain folder — the export zip wraps either one in an outer
// directory named after the server's own temp file (no recognizable suffix),
// so both suffixes have to be tried.
func findStageDir(root string, maxDepth int) (string, error) {
	var found string
	err := walkDepth(root, 0, maxDepth, func(path string, d os.DirEntry) bool {
		if d.IsDir() && (strings.HasSuffix(d.Name(), ".stage") || strings.HasSuffix(d.Name(), ".folder")) {
			found = path
			return true // stop
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
		return err
	}
	for _, e := range entries {
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

// moveContents moves all entries from src directory into dst directory.
func moveContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if err := os.Rename(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func downloadStageRecursively(e *Executor, folderID int, filePath string) error {
	if err := e.checkCancel(); err != nil {
		return err
	}
	// Try "folder" first (works for sub-folder IDs), fall back to "stage" for stage roots.
	data, err := e.PullZip(folderID, "folder")
	if err != nil {
		data, err = e.PullZip(folderID, "stage")
	}
	if err != nil {
		return fmt.Errorf("failed to PullZip: %w", err)
	}
	zipPath := filePath + "/stage.zip"
	err = os.WriteFile(zipPath, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}
	defer os.Remove(zipPath)
	if err = unzipFile(zipPath, filePath); err != nil {
		return fmt.Errorf("failed to unzip: %w", err)
	}

	// Unzip any inner zip files: stage_<id>_<id>.zip (may be nested)
	innerZipRe := regexp.MustCompile(`^stage_\d+_\d+\.zip$`)
	for {
		files, err := os.ReadDir(filePath)
		if err != nil {
			return fmt.Errorf("failed to read directory: %v", err)
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
		err = unzipFile(innerZip, filePath)
		os.Remove(innerZip)
		if err != nil {
			return fmt.Errorf("failed to unzip %s: %w", filepath.Base(innerZip), err)
		}
	}

	// Find .stage directory anywhere up to depth 2: <id>_<name>.stage
	stageDir, err := findStageDir(filePath, 2)
	if err != nil {
		return fmt.Errorf("failed to find stage dir: %v", err)
	}
	if stageDir == "" {
		return fmt.Errorf("stage directory not found (*.stage)")
	}
	// stagesDir is the parent of stageDir (needed for cleanup)
	stagesDir := filepath.Dir(stageDir)
	if stagesDir == filePath {
		stagesDir = ""
	}
	// Move all contents of stageDir into filePath
	if err = moveContents(stageDir, filePath); err != nil {
		return fmt.Errorf("failed to move files: %v", err)
	}
	// удалить stagesDir (родитель stageDir, если он не сам filePath)
	if stagesDir != "" {
		if err = os.RemoveAll(stagesDir); err != nil {
			return fmt.Errorf("failed to remove stages directory: %v", err)
		}
	} else {
		if err = os.RemoveAll(stageDir); err != nil {
			return fmt.Errorf("failed to remove stage directory: %v", err)
		}
	}
	// теперь везде где json файлы форматировать их через MarshalIndent
	err = renameFiles2Folders(filePath)
	if err != nil {
		return fmt.Errorf("failed to rename files: %v", err)
	}
	err = formatJSON(filePath)
	if err != nil {
		return fmt.Errorf("failed to format json: %v", err)
	}

	return nil
	//
	//fmt.Println("procInfo", procInfo)
	//if err != nil {
	//	logger.Error("Failed to PullFolder: %v", err)
	//	return
	//}
	//for _, p := range procInfo {
	//	convType, _ := p.(map[string]interface{})["conv_type"].(string)
	//	if convType != "process" {
	//		continue
	//	}
	//	// save to filePath
	//	data, err := json.MarshalIndent(p, "", "  ")
	//	if err != nil {
	//		logger.Error("Failed to json marshal process: %v", err)
	//		return
	//	}
	//	//data := []byte(UpdateTestJson)
	//	title, _ := p.(map[string]interface{})["title"].(string)
	//	objID := strconv.Itoa(int(p.(map[string]interface{})["obj_id"].(float64)))
	//	if title == "" {
	//		title = objID
	//	} else {
	//		title = title + "." + objID
	//	}
	//	err = os.WriteFile(filePath+"/"+title+".json", data, 0644)
	//	if err != nil {
	//		logger.Error("Failed to write file: %v", err)
	//		return
	//	}
	//	fmt.Println("Process saved to", filePath+"/"+title+".json")
	//}
}

// downloadRootFoldersRecursively exports everything the workspace's virtual
// root (obj_id 0) lists as a direct child, into filePath. obj_id 0 isn't a
// real folder/stage object — the export endpoint rejects it outright ("Object
// stage with id 0 does not exist") — so unlike downloadStageRecursively this
// can't fetch the whole subtree in one export call. Instead it lists the
// direct children and handles each by kind: real folders recurse through the
// normal per-folder export; conv items sitting loose at the root (not
// wrapped in any folder) are exported individually, same as pull-process.
//
// Returns the number of direct children found, so callers can tell "root is
// genuinely empty" apart from "something's wrong" (e.g. WORKSPACE_ID isn't
// set, so the request silently landed in the wrong company scope) — 0 is a
// legitimate result but a surprising one, and callers should say so instead
// of reporting a blanket success. aliasStatus always describes what
// happened with the aliases — found-and-written, genuinely none, or the
// fetch failed — so the caller can say so explicitly instead of leaving
// alias handling silent (which reads as "forgot to check" either way).
func downloadRootFoldersRecursively(ctx context.Context, filePath string) (count int, aliasStatus string, err error) {
	e := NewValidator(ctx, 0)
	children, err := e.ListFolder(0)
	if err != nil {
		return 0, "", fmt.Errorf("failed to list root folders: %w", err)
	}
	for _, c := range children {
		if err := e.checkCancel(); err != nil {
			return 0, "", err
		}
		switch c.Obj {
		case "folder":
			// Give each root-level folder its own named subdirectory so
			// siblings (e.g. two folders that each contain a process) don't
			// get merged together — downloadStageRecursively otherwise
			// unwraps folderID's own name and drops its contents straight
			// into filePath, which is only safe for a single, standalone pull.
			childDir := filepath.Join(filePath, fmt.Sprintf("%d_%s", c.ObjID, sanitizePathSegment(c.Title)))
			if err := os.MkdirAll(childDir, 0755); err != nil {
				return 0, "", fmt.Errorf("failed to create directory for folder %q (#%d): %w", c.Title, c.ObjID, err)
			}
			if err := downloadStageRecursively(e, c.ObjID, childDir); err != nil {
				return 0, "", fmt.Errorf("failed to pull folder %q (#%d): %w", c.Title, c.ObjID, err)
			}
		case "conv":
			if err := downloadRootConv(ctx, c.ObjID, filePath); err != nil {
				return 0, "", fmt.Errorf("failed to pull %q (#%d): %w", c.Title, c.ObjID, err)
			}
		}
	}

	if len(children) > 0 {
		aliasCount, aerr := downloadRootAliases(ctx, filePath)
		if aerr != nil {
			aliasStatus = fmt.Sprintf("aliases.json was not written — fetching aliases failed: %v. Re-run pull-folder(folder_id=0) to retry just the aliases.", aerr)
			logger.Warn("downloadRootFoldersRecursively: %s", aliasStatus)
		} else if aliasCount == 0 {
			aliasStatus = "no aliases exist in this workspace (aliases.json not written)."
		} else {
			aliasStatus = fmt.Sprintf("aliases.json written with %d alias(es).", aliasCount)
		}
	}

	return len(children), aliasStatus, nil
}

// downloadRootAliases writes aliases.json into destDir with every alias in
// the workspace, and returns how many it found. The "Aliases" entry the
// Corezoid UI shows alongside Folders is exactly this — the full
// company-wide list, not scoped to Folders — and the list-aliases API
// confirms that: stage_id/project_id in the request don't actually filter
// anything, every alias in the company comes back regardless. So this
// mirrors the UI 1:1 rather than trying to guess which aliases are
// "relevant" to what else got pulled.
func downloadRootAliases(ctx context.Context, destDir string) (int, error) {
	e := NewValidator(ctx, 0)
	aliases, err := e.ListAliases()
	if err != nil {
		return 0, fmt.Errorf("failed to list aliases: %w", err)
	}
	if len(aliases) == 0 {
		return 0, nil
	}

	data, err := json.MarshalIndent(aliases, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("failed to marshal aliases: %w", err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "aliases.json"), data, 0644); err != nil {
		return 0, err
	}
	return len(aliases), nil
}

// downloadRootConv exports a single conv (process or state diagram) that
// lives directly under the workspace root — not inside any folder — and
// writes it as JSON into destDir.
func downloadRootConv(ctx context.Context, processID int, destDir string) error {
	v := NewValidator(ctx, processID)
	procInfo1, err := v.ExportProcess()
	if err != nil {
		return err
	}
	var procInfo interface{}
	if arr, ok := procInfo1.([]interface{}); ok && len(arr) > 0 {
		procInfo = arr[0]
	} else {
		procInfo = procInfo1
	}
	data, err := json.MarshalIndent(procInfo, "", "  ")
	if err != nil {
		return err
	}

	fileName := fmt.Sprintf("%d.conv.json", processID)
	if m, ok := procInfo.(map[string]interface{}); ok {
		if title, _ := m["title"].(string); title != "" {
			fileName = fmt.Sprintf("%d_%s.conv.json", processID, sanitizePathSegment(title))
		}
	}
	return os.WriteFile(filepath.Join(destDir, fileName), data, 0644)
}

// sanitizePathSegment turns a Corezoid title into a single safe path segment:
// spaces become underscores and any path separator embedded in the title
// (some folders are legitimately named e.g. "Simulator / Smart Forms /
// Filament") is replaced too, so it can't split into nested directories.
func sanitizePathSegment(title string) string {
	safe := strings.ReplaceAll(title, " ", "_")
	safe = strings.ReplaceAll(safe, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	return safe
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
			newName := strings.ReplaceAll(dirName, ".folder", "")
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
				return fmt.Errorf("Failed to format json in directory: %v", err)
			}
			err = renameFiles2Folders(newPath)
			if err != nil {
				return fmt.Errorf("Failed to rename files in directory: %v", err)
			}
		} else {
			if filepath.Ext(f.Name()) != ".json" {
				continue
			}
			//if strings.Contains(f.Name(), ".folder.json") {
			//	//	 remove
			//	err := os.Remove(filepath.Join(filePath, f.Name()))
			//	if err != nil {
			//		return fmt.Errorf("failed to remove file: %v", err)
			//	}
			//}
			// Rename file if it follows pattern with numeric prefix
		}

	}
	return nil
}

func formatJSON(filePath string) error {
	// теперь везде где json файлы форматировать их через MarshalIndent
	files, err := os.ReadDir(filePath)
	if err != nil {
		return fmt.Errorf("Failed to read directory2: %v", err)
	}

	for _, f := range files {
		if f.IsDir() {
			//fmt.Println("Downloaded folder", filepath.Join(filePath, f.Name()))
			err := formatJSON(filepath.Join(filePath, f.Name()))
			if err != nil {
				return fmt.Errorf("Failed to format json in directory: %v", err)
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
			return fmt.Errorf("failed to unmarshal file: %v", err)
		}
		// везде где есть uuid в scheme.nodes объект удалить
		if nodes, ok := dataRsp.(map[string]interface{}); ok {
			if nodes, ok := nodes["scheme"].(map[string]interface{}); ok {
				if nodes1, ok := nodes["nodes"].([]interface{}); ok {
					for _, node := range nodes1 {
						if nodeMap, ok := node.(map[string]interface{}); ok {
							delete(nodeMap, "uuid")
						}
					}
				}
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
