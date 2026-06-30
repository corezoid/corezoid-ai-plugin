package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

	// Patch any process files whose scheme.nodes is empty. This happens when
	// a process was imported from a file but never deployed (e.g. it referenced
	// a non-existent sub-process). export_process only exports deployed content,
	// so the ZIP contains empty scheme.nodes for those processes. We recover the
	// node list from the get_process API, which always returns the full stored
	// (uncommitted) node set — matching what the Corezoid UI displays.
	if patchErr := patchEmptyNodesInDir(e, filePath); patchErr != nil {
		logger.Warn("pull-folder: patch empty nodes partially failed: %v", patchErr)
		// Non-fatal: we still return any successfully patched files.
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

// processIDFromJSON extracts the numeric process ID stored in the "obj_id"
// field of a parsed process JSON map. Returns 0 when the field is absent or
// not a number.
func processIDFromJSON(proc interface{}) int {
	m, ok := proc.(map[string]interface{})
	if !ok {
		return 0
	}
	if f, ok := m["obj_id"].(float64); ok {
		return int(f)
	}
	return 0
}

// patchEmptyNodesInDir walks the directory tree rooted at dir, finds process
// JSON files whose scheme.nodes array is empty, and refills scheme.nodes from
// the get_process API via FetchDraftNodes.
//
// Background: export_process only includes deployed node data. Processes that
// were imported from a file but never deployed (e.g. because they contained a
// reference to a non-existent sub-process) arrive with an empty scheme.nodes
// in the ZIP. The get_process API always returns the full uncommitted node
// list, so we can recover the nodes here and write them back into the file.
//
// Errors for individual files are logged as warnings and do not abort the
// walk — callers receive a non-nil error only when the directory itself
// cannot be read.
func patchEmptyNodesInDir(v *Executor, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if subErr := patchEmptyNodesInDir(v, path); subErr != nil {
				logger.Warn("patchEmptyNodesInDir: error in %s: %v", path, subErr)
			}
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var procJSON interface{}
		if unmarshalErr := json.Unmarshal(data, &procJSON); unmarshalErr != nil {
			continue
		}
		if !schemeNodesEmpty(procJSON) {
			continue // nodes already present — nothing to do
		}

		// Determine the process ID: prefer the obj_id field in the JSON,
		// fall back to the leading numeric prefix in the filename.
		procID := processIDFromJSON(procJSON)
		if procID == 0 {
			base := filepath.Base(path)
			if m := reProcessIDFromFilename.FindStringSubmatch(base); m != nil {
				procID, _ = strconv.Atoi(m[1])
			}
		}
		if procID == 0 {
			logger.Warn("patchEmptyNodesInDir: cannot determine process ID for %s, skipping", path)
			continue
		}

		logger.Warn("patchEmptyNodesInDir: process %d (%s) has empty scheme.nodes; "+
			"fetching draft nodes via get_process", procID, entry.Name())

		nodes, fetchErr := v.FetchDraftNodes(procID)
		if fetchErr != nil || len(nodes) == 0 {
			logger.Warn("patchEmptyNodesInDir: draft fallback for process %d failed: %v", procID, fetchErr)
			continue
		}

		procMap, ok := procJSON.(map[string]interface{})
		if !ok {
			continue
		}
		scheme, _ := procMap["scheme"].(map[string]interface{})
		if scheme == nil {
			scheme = make(map[string]interface{})
			procMap["scheme"] = scheme
		}
		scheme["nodes"] = nodes

		patched, marshalErr := json.MarshalIndent(procMap, "", "  ")
		if marshalErr != nil {
			logger.Warn("patchEmptyNodesInDir: failed to marshal patched process %d: %v", procID, marshalErr)
			continue
		}
		if writeErr := os.WriteFile(path, patched, 0644); writeErr != nil {
			logger.Warn("patchEmptyNodesInDir: failed to write patched file %s: %v", path, writeErr)
			continue
		}
		logger.Warn("patchEmptyNodesInDir: injected %d draft nodes into process %d (%s)",
			len(nodes), procID, entry.Name())
	}
	return nil
}
