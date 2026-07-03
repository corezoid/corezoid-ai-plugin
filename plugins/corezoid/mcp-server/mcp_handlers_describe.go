package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// describeProcessResult is what handleDescribeProcess returns to the model.
// Kept small on purpose: this is the payload consumed at the MANDATORY
// "Identify the Process" step in corezoid-edit, so the fields are only the
// ones the model needs to make the go/no-go decision (blast radius, staleness)
// plus the path it uses to open the file.
type describeProcessResult struct {
	Found              bool     `json:"found"`
	ConvID             string   `json:"conv_id,omitempty"`
	Title              string   `json:"title,omitempty"`
	Path               string   `json:"path,omitempty"`
	Location           string   `json:"location,omitempty"`
	Aliases            []string `json:"aliases,omitempty"`
	CallsInCount       int      `json:"calls_in_count"`
	CallsIn            []string `json:"calls_in,omitempty"`
	HighFanIn          bool     `json:"high_fan_in"`
	IndexHash          string   `json:"index_hash,omitempty"`
	CurrentFileHash    string   `json:"current_file_hash,omitempty"`
	Stale              bool     `json:"stale"`
	IndexMissing       bool     `json:"index_missing"`
	Candidates         []describeCandidate `json:"candidates,omitempty"`
	Message            string   `json:"message,omitempty"`
}

type describeCandidate struct {
	ConvID string `json:"conv_id"`
	Title  string `json:"title"`
	Path   string `json:"path"`
}

// handleDescribeProcess resolves a process identifier — numeric conv_id,
// @alias, or a fuzzy title fragment — against .corezoid/project-map.json and
// returns everything corezoid-edit needs at its MANDATORY first step in one
// payload:
//   - path to the file on disk (for the edit step)
//   - calls_in count + a high_fan_in bool computed against IndexHighFanIn
//     (so the model doesn't have to remember the threshold or forget the check)
//   - index_hash + current_file_hash + stale bool (so the model sees staleness
//     as data, not as an instruction to "remember to check")
//
// The point is that these fields arrive as part of the mandatory step, not as
// a separate prompt to be honoured or forgotten. See TZ §10a.
//
// If the index is missing (index_missing=true), the caller must fall back to
// filesystem grep — the tool still tries to help by returning best-effort
// candidates from a filesystem scan when a title fragment is supplied.
func handleDescribeProcess(_ context.Context, args map[string]interface{}) (string, bool) {
	projectPath := optStrArg(args, "project_path")
	if projectPath == "" {
		projectPath = "."
	}
	safe, err := confineToWorkdir(projectPath)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	absRoot, err := filepath.Abs(safe)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	// Identifier: any one of these; the model calls us with whichever the
	// user provided.
	identifier := strings.TrimSpace(optStrArg(args, "identifier"))
	if identifier == "" {
		identifier = strings.TrimSpace(optStrArg(args, "process_id"))
	}
	if identifier == "" {
		identifier = strings.TrimSpace(optStrArg(args, "process_name"))
	}
	if identifier == "" {
		return "Error: identifier (process_id, process_name, or @alias) is required", true
	}

	res := describeProcessResult{}

	pmPath := filepath.Join(absRoot, IndexOutputDir, IndexMapFile)
	data, err := os.ReadFile(pmPath)
	if errors.Is(err, os.ErrNotExist) {
		res.IndexMissing = true
		// Best-effort candidate list from filesystem so the caller still has
		// something to work with. No calls_in / staleness info possible.
		res.Candidates = findCandidatesByFilesystem(absRoot, identifier)
		res.Message = "Project index not built yet. Run the corezoid-index skill (or MCP tool build-project-index) to enable blast-radius and staleness reporting."
		return marshalDescribe(res)
	}
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", pmPath, err), true
	}
	var pm ProjectMap
	if err := json.Unmarshal(data, &pm); err != nil {
		return fmt.Sprintf("Error parsing %s: %v", pmPath, err), true
	}

	// Resolution order: exact conv_id → alias (with or without @) → title
	// fragment (case-insensitive). Title fragments are last because they can
	// produce multiple matches, in which case we return the candidate list
	// and force the caller to disambiguate — never guess.
	convID := resolveIdentifier(identifier, &pm)
	if convID == "" {
		matches := matchByTitle(identifier, &pm)
		switch len(matches) {
		case 0:
			res.Message = fmt.Sprintf("No process resolves to identifier %q. Try a numeric conv_id, an @alias, or a substring of the title.", identifier)
			return marshalDescribe(res)
		case 1:
			convID = matches[0]
		default:
			for _, cid := range matches {
				res.Candidates = append(res.Candidates, describeCandidate{
					ConvID: cid,
					Title:  pm.Processes[cid].Title,
					Path:   pm.Processes[cid].Path,
				})
			}
			res.Message = fmt.Sprintf("Multiple processes match %q — ask the user which one.", identifier)
			return marshalDescribe(res)
		}
	}

	pe, ok := pm.Processes[convID]
	if !ok {
		res.Message = fmt.Sprintf("Resolved to conv_id %s but it is not in the index. The index may need rebuilding.", convID)
		return marshalDescribe(res)
	}

	res.Found = true
	res.ConvID = convID
	res.Title = pe.Title
	res.Path = pe.Path
	res.Location = pe.Location
	res.Aliases = append([]string(nil), pe.Aliases...)
	res.CallsIn = append([]string(nil), pm.CallsIn[convID]...)
	res.CallsInCount = len(res.CallsIn)
	res.HighFanIn = res.CallsInCount > IndexHighFanIn
	res.IndexHash = pe.Hash

	// Validate pe.Path — it comes from the on-disk project-map.json (which
	// a user could modify) and must not contain "../" path traversal.
	// confineToWorkdir rejects absolute paths and ".." escapes.
	if safePath, pathErr := confineToWorkdir(pe.Path); pathErr == nil && safePath != "" {
		if absPath := filepath.Join(absRoot, safePath); absPath != "" {
			if h, err := hashFile(absPath); err == nil {
				res.CurrentFileHash = h
				res.Stale = h != pe.Hash
			} else {
				// File missing → the index is definitely stale (or worse).
				res.Stale = true
			}
		}
	}

	// Compose a short human-readable summary that goes AFTER the JSON. The
	// model doesn't strictly need it — the JSON has everything — but for a
	// human debugging tool output it helps.
	return marshalDescribe(res)
}

func marshalDescribe(res describeProcessResult) (string, bool) {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	return string(b), false
}

func resolveIdentifier(id string, pm *ProjectMap) string {
	// Numeric conv_id.
	if _, err := strconv.Atoi(id); err == nil {
		if _, ok := pm.Processes[id]; ok {
			return id
		}
	}
	// @alias: flattenAliases in index_builder.go stores both "@name" and "name"
	// (without @), so two lookups cover all forms. The third redundant lookup
	// `pm.ByAlias["@"+TrimPrefix(id, "@")]` is intentionally omitted.
	if cid, ok := pm.ByAlias[id]; ok {
		return cid
	}
	if cid, ok := pm.ByAlias[strings.TrimPrefix(id, "@")]; ok {
		return cid
	}
	return ""
}

// matchByTitle returns conv_ids whose title contains all words in fragment.
// For single-word queries, this is equivalent to the old Contains check.
// For multi-word phrases (e.g. "accounts actor"), every word must appear in
// the title in any order — this prevents multi-word action+object phrases
// from matching nothing when the words are non-adjacent.
func matchByTitle(fragment string, pm *ProjectMap) []string {
	words := strings.Fields(strings.ToLower(fragment))
	if len(words) == 0 {
		return nil
	}
	var matches []string
	for cid, pe := range pm.Processes {
		lower := strings.ToLower(pe.Title)
		allMatch := true
		for _, w := range words {
			if !strings.Contains(lower, w) {
				allMatch = false
				break
			}
		}
		if allMatch {
			matches = append(matches, cid)
		}
	}
	return uniqueSorted(matches)
}

// findCandidatesByFilesystem is the fallback path when there is no index and
// the caller supplied a title fragment. Scans .conv.json filenames and
// returns matches — no calls_in / staleness info can be derived without the
// index, so this is deliberately less useful than the indexed path.
func findCandidatesByFilesystem(root, fragment string) []describeCandidate {
	var out []describeCandidate
	// Try exact numeric conv_id first as a filename prefix.
	if _, err := strconv.Atoi(fragment); err == nil {
		re := regexp.MustCompile("^" + regexp.QuoteMeta(fragment) + `_.*\.conv\.json$`)
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			if re.MatchString(info.Name()) {
				rel, _ := filepath.Rel(root, path)
				out = append(out, describeCandidate{
					ConvID: fragment,
					Title:  "",
					Path:   filepath.ToSlash(rel),
				})
			}
			return nil
		})
		return out
	}
	// Fragment match against filename basename (a coarse approximation of
	// title match without loading every file).
	lower := strings.ToLower(fragment)
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".conv.json") {
			return nil
		}
		if !strings.Contains(strings.ToLower(name), lower) {
			return nil
		}
		m := reProcessFileName.FindStringSubmatch(name)
		if m == nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, describeCandidate{
			ConvID: m[1],
			Title:  "",
			Path:   filepath.ToSlash(rel),
		})
		return nil
	})
	return out
}
