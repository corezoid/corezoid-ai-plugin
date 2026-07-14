package main

import (
	"archive/zip"
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

// findStageDir looks for a directory named *.stage up to maxDepth levels deep inside root.
func findStageDir(root string, maxDepth int) (string, error) {
	var found string
	err := walkDepth(root, 0, maxDepth, func(path string, d os.DirEntry) bool {
		if d.IsDir() && strings.HasSuffix(d.Name(), ".stage") {
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
		return fmt.Errorf("stage directory not found (*.stage) — " +
			"if COREZOID_WORK_DIR (or the MCP server launch directory) is $HOME " +
			"or another large directory, set it to a dedicated project folder and " +
			"restart Claude Code / the MCP server")
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
	err = formatJSONWithFallback(e, filePath)
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
			//fmt.Println("Downloaded folder", filepath.Join(filePath, f.Name()))
			err := formatJSONWithFallback(e, filepath.Join(filePath, f.Name()))
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
