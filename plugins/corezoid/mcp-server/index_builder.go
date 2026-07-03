package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// reEnvVarUsage finds Corezoid environment variable references in string
// values inside a process. The @-prefix is required (the platform syntax);
// name characters are anything except closing bracket, whitespace, or brace.
var reEnvVarUsage = regexp.MustCompile(`\{\{env_var\[@([^\]\s\}]+)\]`)

// reConvRefUsage finds Corezoid cross-task reference syntax like
// {{conv[@config].ref[api_url]}} — where `conv[@X]` names a task by alias
// or ref. Captured group 1 is the alias/ref name without the @. Used to
// populate the `used_by` list for each entry in config_references.
var reConvRefUsage = regexp.MustCompile(`\{\{conv\[@([^\]\s\}]+)\]`)

// reConvRefField extends reConvRefUsage to also capture the `.ref[X]`
// field name that follows. This tells the config_references collector
// WHICH fields of a config are actually read by which processes — a
// grep-quality signal built into the index instead of asking every reader
// to grep the diagrams themselves. Group 1 = ref/alias name, group 2 =
// field name. Not every conv-ref has a trailing .ref[X] (some just check
// existence), so this regex is used *in addition to* reConvRefUsage, not
// instead of.
var reConvRefField = regexp.MustCompile(`\{\{conv\[@([^\]\s\}]+)\]\.ref\[([^\]\s\}]+)\]`)

// reProcessFileName matches "<conv_id>_<name>.conv.json" and captures the id.
var reProcessFileName = regexp.MustCompile(`^(\d+)_.*\.conv\.json$`)

// reFolderMarker matches "<id>_<name>.folder.json" or "<id>_<name>.stage.json".
var reFolderMarker = regexp.MustCompile(`^(\d+)_.*\.(folder|stage)\.json$`)

// reInstanceFileName matches "<instance_id>_<name>.instance.json".
var reInstanceFileName = regexp.MustCompile(`^(\d+)_.*\.instance\.json$`)

// BuildProjectIndex walks the project rooted at absRoot and produces a
// ProjectMap plus a list of non-fatal warnings. absRoot must exist. Missing
// optional files (_ALIASES_.json, _ENV_VARS_.json, *.instance.json) are not
// an error — the corresponding sections come back empty.
//
// The index is a pure local scan — no network calls, no Corezoid auth.
// config_references records which processes read which config refs and
// which fields they access, but stores no runtime values. Agents read
// live config values on demand via list-node-tasks when they need them.
func BuildProjectIndex(ctx context.Context, absRoot string) (*ProjectMap, []string, error) {
	if info, err := os.Stat(absRoot); err != nil || !info.IsDir() {
		return nil, nil, fmt.Errorf("project root %q is not a directory", absRoot)
	}

	var warnings []string

	pm := &ProjectMap{
		SchemaVersion:     IndexSchemaVersion,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Root:              absRoot,
		Processes:         map[string]*ProcessEntry{},
		ByAlias:           map[string]string{},
		EnvVars:           map[string]*EnvVarEntry{},
		Edges:             []Edge{},
		CallsIn:           map[string][]string{},
		UnresolvedTargets: map[string][]string{},
		ExternalAPIs:      map[string][]string{},
		StateStores:       map[string]*StateStoreEntry{},
		Instances:         map[string]*InstanceEntry{},
		SecurityHotspots:  []SecurityHotspot{},
	}

	// .env shape parsing is intentionally naive — we only need two keys and the
	// file format is a simple KEY=VALUE list, so a full dotenv library would be
	// dead weight here.
	pm.WorkspaceID, pm.StageID = readDotEnv(absRoot)

	// Optional files first — presence/absence determines what sections light up.
	aliasesFwd, aliasesRev, aliasWarn := readAliases(absRoot)
	if aliasWarn != "" {
		warnings = append(warnings, aliasWarn)
	}
	pm.ByAlias = flattenAliases(aliasesFwd)

	envVarsMeta, envVarWarn := readEnvVars(absRoot)
	if envVarWarn != "" {
		warnings = append(warnings, envVarWarn)
	}
	for name, desc := range envVarsMeta {
		pm.EnvVars[name] = &EnvVarEntry{Description: desc, UsedBy: []string{}}
	}

	// Two-pass process discovery. First pass collects title+conv_type+status
	// for every process (needed so edges can attach titles even when the
	// target hasn't been visited yet), plus records aliases, hashes, and
	// per-process raw call/env-var findings. Second pass resolves calls into
	// edges once every conv_id in the project is known.
	findings := map[string]*rawIndexFindings{}
	bc := newBreadcrumbCache()

	// Discovery walk. Skip .corezoid/ to avoid re-reading our own output when
	// running --check or a repeated build.
	err := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
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

		switch {
		case strings.HasSuffix(name, ".conv.json"):
			cid, entry, rf, warn, perr := indexConvJSON(absRoot, path, aliasesRev, bc)
			if perr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", path, perr))
				return nil
			}
			if warn != "" {
				warnings = append(warnings, warn)
			}
			if cid == "" || entry == nil {
				return nil
			}
			if existing, dup := pm.Processes[cid]; dup {
				warnings = append(warnings, fmt.Sprintf(
					"duplicate conv_id %s: %q shadows %q — only the last one is indexed",
					cid, entry.Path, existing.Path))
			}
			pm.Processes[cid] = entry
			findings[cid] = rf
			if entry.ConvType == "state" {
				pm.StateStoreCount++
				pm.StateStores[cid] = &StateStoreEntry{
					Title:     entry.Title,
					Aliases:   entry.Aliases,
					Path:      entry.Path,
					WrittenBy: []string{},
				}
			} else {
				pm.ProcessCount++
			}

		case reInstanceFileName.MatchString(name):
			iid, ie, ierr := indexInstanceJSON(absRoot, path)
			if ierr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", path, ierr))
				return nil
			}
			if ie == nil {
				return nil
			}
			pm.Instances[iid] = ie
			pm.InstanceCount++
			if len(ie.SecretFieldsPresent) > 0 {
				pm.SecurityHotspots = append(pm.SecurityHotspots, SecurityHotspot{
					Source:     "instance",
					InstanceID: iid,
					Title:      ie.Title,
					Path:       ie.Path,
					Fields:     append([]string(nil), ie.SecretFieldsPresent...),
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, warnings, fmt.Errorf("walking project tree: %v", err)
	}

	// Pass 2: resolve calls into edges. Numeric conv_ids that don't exist in
	// this project's inventory go to unresolved_targets, not edges, because
	// they refer to something outside the pulled scope and we can't attach a
	// title. Same for dynamic {{...}} — deliberately unresolvable at index
	// time.
	convRefUsage := map[string][]string{}           // ref-name → [conv_id, ...]
	convRefFieldsByRef := map[string][]string{}     // ref-name → [.ref[X] field, ...]
	for cid, rf := range findings {
		pe := pm.Processes[cid]
		for _, c := range rf.calls {
			target, ok := resolveTarget(c.targetRaw, aliasesFwd, pm.Processes)
			if !ok {
				pm.UnresolvedTargets[cid] = append(pm.UnresolvedTargets[cid],
					fmt.Sprintf("%s (%s)", c.targetRaw, c.ctype))
				continue
			}
			e := Edge{
				From:     cid,
				To:       target,
				Type:     c.ctype,
				Mode:     c.mode,
				ViaAlias: c.via,
			}
			pm.Edges = append(pm.Edges, e)
		}
		for _, url := range rf.externalURLs {
			pm.ExternalAPIs[url] = append(pm.ExternalAPIs[url], cid)
		}
		if rf.envVars != nil {
			for name := range rf.envVars {
				ev, ok := pm.EnvVars[name]
				if !ok {
					// Referenced but not declared in _ENV_VARS_.json (either
					// no such file, or the var is used elsewhere in the
					// workspace but not registered here). Track anyway so the
					// index answers "where is @X used" for undeclared vars.
					ev = &EnvVarEntry{Description: "", UsedBy: []string{}}
					pm.EnvVars[name] = ev
				}
				ev.UsedBy = append(ev.UsedBy, cid)
			}
		}
		for ref := range rf.convRefs {
			convRefUsage[ref] = append(convRefUsage[ref], cid)
		}
		for ref, fields := range rf.convRefFields {
			for f := range fields {
				convRefFieldsByRef[ref] = append(convRefFieldsByRef[ref], f)
			}
		}
		pe.DBCalls = uniqueSorted(rf.dbCalls)
		pe.EnvVarRefs = mapKeysSorted(rf.envVars)
		pe.HasReceiverNode = rf.receiver
		pm.SecurityHotspots = append(pm.SecurityHotspots, rf.hotspots...)
	}
	for k := range convRefUsage {
		convRefUsage[k] = uniqueSorted(convRefUsage[k])
	}
	for k := range convRefFieldsByRef {
		convRefFieldsByRef[k] = uniqueSorted(convRefFieldsByRef[k])
	}

	// Sort external_apis[]/env_vars[].used_by lists for stable output.
	for k := range pm.ExternalAPIs {
		pm.ExternalAPIs[k] = uniqueSorted(pm.ExternalAPIs[k])
	}
	for _, ev := range pm.EnvVars {
		ev.UsedBy = uniqueSorted(ev.UsedBy)
	}

	// Sort edges deterministically, then deduplicate. Duplicate edges arise
	// when multiple nodes in the same process call the same target (common
	// for shared error handlers). Without deduplication, jq consumers that
	// count outgoing edges via [.edges[] | select(.from == $id)] would
	// overcount. Deduplication happens after sort so identical edges are
	// adjacent and a single linear pass removes them.
	sort.Slice(pm.Edges, func(i, j int) bool {
		a, b := pm.Edges[i], pm.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Mode < b.Mode
	})
	deduped := pm.Edges[:0]
	for i, e := range pm.Edges {
		if i == 0 || e.From != pm.Edges[i-1].From || e.To != pm.Edges[i-1].To ||
			e.Type != pm.Edges[i-1].Type || e.Mode != pm.Edges[i-1].Mode {
			deduped = append(deduped, e)
		}
	}
	pm.Edges = deduped

	// Derive calls_in / state_stores.written_by from edges.
	// calls_out is removed — use jq '[.edges[] | select(.from == $id)]' instead.
	// calls_in is a sorted, deduplicated set of caller conv_ids.
	callInSet := map[string]map[string]struct{}{}
	for _, e := range pm.Edges {
		if callInSet[e.To] == nil {
			callInSet[e.To] = map[string]struct{}{}
		}
		callInSet[e.To][e.From] = struct{}{}
	}
	for cid, set := range callInSet {
		pm.CallsIn[cid] = mapKeysSorted(set)
	}
	// state_stores.written_by from edges into a state process via api_copy.
	for _, e := range pm.Edges {
		if e.Type != "api_copy" {
			continue
		}
		ss, ok := pm.StateStores[e.To]
		if !ok {
			continue
		}
		if !containsString(ss.WrittenBy, e.From) {
			ss.WrittenBy = append(ss.WrittenBy, e.From)
		}
	}
	for _, ss := range pm.StateStores {
		ss.WrittenBy = uniqueSorted(ss.WrittenBy)
	}

	// Attach unresolved_targets as a stable slice (map already, but dedupe).
	for k, list := range pm.UnresolvedTargets {
		pm.UnresolvedTargets[k] = uniqueSorted(list)
	}

	// Load per-project heuristic config for entry_point / orphaned.
	cfg, cfgWarn, cfgErr := LoadOrCreateIndexConfig(absRoot)
	if cfgErr != nil {
		warnings = append(warnings, "index-config.json: "+cfgErr.Error())
	}
	warnings = append(warnings, cfgWarn...)

	pm.GraphStats = computeGraphStats(pm, cfg)

	// Stable sort for security_hotspots so goldens don't flap.
	sort.Slice(pm.SecurityHotspots, func(i, j int) bool {
		a, b := pm.SecurityHotspots[i], pm.SecurityHotspots[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.NodeID < b.NodeID
	})

	// config_references — pure local scan. No network calls, no auth.
	// Records which processes read which config refs and which fields,
	// but stores no runtime values. Agents call list-node-tasks when
	// they need live values (see corezoid-index skill).
	if crMap := collectConfigReferences(cfg.ConfigReferences, pm,
		convRefUsage, convRefFieldsByRef); len(crMap) > 0 {
		pm.ConfigReferences = crMap
	}

	return pm, warnings, nil
}

// indexConvJSON parses one .conv.json file and returns the summary plus the
// raw per-process findings that pass 2 will resolve into edges.
// maxIndexFileBytes caps the size of individual .conv.json / .instance.json
// files read during indexing. Corezoid exports are typically 5–50 KB; a
// megabyte-scale file is almost certainly corrupt or not a real process.
const maxIndexFileBytes = 1 << 20 // 1 MiB

func indexConvJSON(root, path string, aliasesRev map[string][]string, bc breadcrumbCache) (string, *ProcessEntry, *rawIndexFindings, string, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > maxIndexFileBytes {
		return "", nil, nil, fmt.Sprintf("%s: file too large (%d bytes), skipping", path, info.Size()), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, "", err
	}
	var proc map[string]interface{}
	if err := json.Unmarshal(data, &proc); err != nil {
		return "", nil, nil, "", err
	}

	// conv_id from filename, not from JSON, so re-numbering during export
	// doesn't invalidate the graph.
	base := filepath.Base(path)
	m := reProcessFileName.FindStringSubmatch(base)
	if m == nil {
		return "", nil, nil, fmt.Sprintf("%s: filename does not match <id>_<name>.conv.json", path), nil
	}
	cid := m[1]

	title, _ := proc["title"].(string)
	convType, _ := proc["conv_type"].(string)
	if convType == "" {
		convType = "process"
	}
	status, _ := proc["status"].(string)

	info, statErr := os.Stat(path)
	if statErr != nil {
		return "", nil, nil, fmt.Sprintf("stat %s: %v", path, statErr), nil
	}
	rel, _ := filepath.Rel(root, path)

	entry := &ProcessEntry{
		Title:      title,
		ConvType:   convType,
		Status:     status,
		Path:       filepath.ToSlash(rel),
		Location:   buildBreadcrumb(root, path, bc),
		Aliases:    aliasesForConv(cid, aliasesRev),
		EnvVarRefs: []string{},
		Hash:  hashBytes(data),
		MTime: info.ModTime().UTC().Format(time.RFC3339Nano),
	}

	nodes, _ := getNodes(proc)
	entry.NodeCount = len(nodes)

	findings := &rawIndexFindings{
		envVars:       map[string]struct{}{},
		convRefs:      map[string]struct{}{},
		convRefFields: map[string]map[string]struct{}{},
	}

	for _, raw := range nodes {
		node, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		nodeID, _ := node["id"].(string)
		cond, _ := node["condition"].(map[string]interface{})
		logics := toMapSlice(cond["logics"])
		if len(logics) == 0 {
			continue
		}

		// Scan every string value in every logic for {{env_var[@...]}} — the
		// pattern can appear in any field (url, extra values, code node text,
		// etc.), and we don't want to enumerate every schema path here.
		walkStrings(logics, func(s string) {
			for _, mm := range reEnvVarUsage.FindAllStringSubmatch(s, -1) {
				if len(mm) >= 2 {
					findings.envVars[mm[1]] = struct{}{}
				}
			}
			for _, mm := range reConvRefUsage.FindAllStringSubmatch(s, -1) {
				if len(mm) >= 2 {
					findings.convRefs[mm[1]] = struct{}{}
				}
			}
			for _, mm := range reConvRefField.FindAllStringSubmatch(s, -1) {
				if len(mm) >= 3 {
					if findings.convRefFields[mm[1]] == nil {
						findings.convRefFields[mm[1]] = map[string]struct{}{}
					}
					findings.convRefFields[mm[1]][mm[2]] = struct{}{}
				}
			}
		})

		for _, lg := range logics {
			ltype, _ := lg["type"].(string)

			if ltype == "api_callback" {
				findings.receiver = true
			}

			if isCrossProcessType(ltype) {
				convRef, viaAlias := extractCrossProcessRef(lg)
				if convRef == "" {
					// Malformed reference — silently skip; not our job to
					// diagnose broken diagrams (that's lint-process/stage-scan).
					continue
				}
				call := rawCall{
					targetRaw: convRef,
					via:       viaAlias,
					ctype:     ltype,
				}
				if ltype == "api_copy" {
					call.mode, _ = lg["mode"].(string)
				}
				findings.calls = append(findings.calls, call)
			}

			if ltype == "api" {
				if url, _ := lg["url"].(string); url != "" {
					findings.externalURLs = append(findings.externalURLs, url)
				}
				// Scan extra_headers keys + extra field values under
				// secret-shaped names in api-type logics.
				if hdrs, ok := lg["extra_headers"].(map[string]interface{}); ok {
					hits := scanMapForSecrets(hdrs, true)
					if len(hits) > 0 {
						findings.hotspots = append(findings.hotspots, SecurityHotspot{
							Source: "diagram",
							ConvID: cid,
							NodeID: nodeID,
							Title:  title,
							Path:   filepath.ToSlash(rel),
							Fields: hits,
						})
					}
				}
				if extra, ok := lg["extra"].(map[string]interface{}); ok {
					hits := scanMapForSecrets(extra, true)
					if len(hits) > 0 {
						findings.hotspots = append(findings.hotspots, SecurityHotspot{
							Source: "diagram",
							ConvID: cid,
							NodeID: nodeID,
							Title:  title,
							Path:   filepath.ToSlash(rel),
							Fields: hits,
						})
					}
				}
			}

			if ltype == "db_call" {
				instanceID, _ := lg["instance_id"].(string)
				instanceName, _ := lg["instance_name"].(string)
				if instanceID != "" {
					if instanceName != "" {
						findings.dbCalls = append(findings.dbCalls, instanceID+" ("+instanceName+")")
					} else {
						findings.dbCalls = append(findings.dbCalls, instanceID)
					}
				}
			}
		}
	}

	return cid, entry, findings, "", nil
}

// rawIndexFindings is a private companion to ProcessEntry, holding data that
// only pass 2 (edge resolution) needs. Never serialized.
type rawIndexFindings struct {
	calls        []rawCall
	externalURLs []string
	dbCalls      []string
	envVars      map[string]struct{}
	// convRefs tracks {{conv[@name].ref[...]}} references — the name
	// (without @) is stored. Used by the config_references collector to
	// build the used_by list per configured ref without a second scan.
	convRefs map[string]struct{}
	// convRefFields tracks the .ref[X] field names extracted from
	// {{conv[@name].ref[X]}} usages: name → {X, ...}. Populated in the
	// same walkStrings pass as convRefs. The collector uses this to tell
	// the reader which specific fields of a config each process reads —
	// a signal that today requires a manual grep.
	convRefFields map[string]map[string]struct{}
	receiver      bool
	hotspots      []SecurityHotspot
}

type rawCall struct {
	targetRaw string
	via       string
	ctype     string
	mode      string
}

// extractCrossProcessRef pulls the conv_id from a cross-process logic block.
// Corezoid stores conv_id as either a number (float64 after json.Unmarshal), a
// string (when it's @alias or a dynamic expression), or as top-level "conv_id"
// vs nested under "extra" — we accept all layouts we've seen.
func extractCrossProcessRef(lg map[string]interface{}) (raw, viaAlias string) {
	tryVal := func(v interface{}) string {
		switch x := v.(type) {
		case float64:
			return fmt.Sprintf("%d", int(x))
		case string:
			return x
		case int:
			return fmt.Sprintf("%d", x)
		}
		return ""
	}
	if v, ok := lg["conv_id"]; ok {
		raw = tryVal(v)
	}
	if raw == "" {
		if extra, ok := lg["extra"].(map[string]interface{}); ok {
			raw = tryVal(extra["conv_id"])
		}
	}
	// Some payloads carry the alias in "short_name" — track it so the edge
	// records via_alias even if the resolution happens through _ALIASES_.json.
	if strings.HasPrefix(raw, "@") {
		viaAlias = raw
	} else if sn, ok := lg["short_name"].(string); ok && sn != "" {
		viaAlias = "@" + strings.TrimPrefix(sn, "@")
	}
	return raw, viaAlias
}

func resolveTarget(raw string, aliasesFwd map[string]string, processes map[string]*ProcessEntry) (string, bool) {
	if raw == "" {
		return "", false
	}
	// Dynamic expression — unresolvable at index time.
	if strings.Contains(raw, "{{") {
		return "", false
	}
	if strings.HasPrefix(raw, "@") {
		if cid, ok := aliasesFwd[raw]; ok {
			if _, present := processes[cid]; present {
				return cid, true
			}
		}
		key := strings.TrimPrefix(raw, "@")
		if cid, ok := aliasesFwd[key]; ok {
			if _, present := processes[cid]; present {
				return cid, true
			}
		}
		return "", false
	}
	// Numeric conv_id — only resolves if that process is in this project.
	if _, present := processes[raw]; present {
		return raw, true
	}
	return "", false
}

// hashBytes returns the first IndexHashHexLen hex characters of the SHA-1 sum
// of the given bytes. Deliberately computed on raw file bytes, not on the
// re-marshalled JSON, so the digest is stable regardless of how a JSON
// pretty-printer might reorder keys or whitespace. See TZ §5 hash definition.
func hashBytes(b []byte) string {
	sum := sha1.Sum(b)
	h := hex.EncodeToString(sum[:])
	if len(h) > IndexHashHexLen {
		h = h[:IndexHashHexLen]
	}
	return h
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func indexInstanceJSON(root, path string) (string, *InstanceEntry, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > maxIndexFileBytes {
		return "", nil, fmt.Errorf("file too large (%d bytes), skipping", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("reading instance file: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", nil, fmt.Errorf("parsing instance file: %w", err)
	}

	base := filepath.Base(path)
	m := reInstanceFileName.FindStringSubmatch(base)
	if m == nil {
		return "", nil, nil // filename doesn't match expected pattern — skip silently
	}
	iid := m[1]

	title, _ := raw["title"].(string)
	instanceType, _ := raw["instance_type"].(string)
	rel, _ := filepath.Rel(root, path)

	entry := &InstanceEntry{
		Title:        title,
		InstanceType: instanceType,
		Path:         filepath.ToSlash(rel),
	}

	// For .instance.json we scan by field name only (checkValue=false) — the
	// TZ (§4 п.7) treats the presence of a secret-shaped key as reason enough
	// to flag it, because instances typically hold real credentials.
	if payload, ok := raw["data"].(map[string]interface{}); ok {
		entry.SecretFieldsPresent = scanMapForSecrets(payload, false)
	}
	return iid, entry, nil
}

// walkStrings visits every string value nested inside v (arbitrary depth,
// through maps and slices) and calls f with each. Used to sweep for
// {{env_var[@...]}} references without hard-coding schema paths.
func walkStrings(v interface{}, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case []interface{}:
		for _, item := range x {
			walkStrings(item, f)
		}
	case []map[string]interface{}:
		for _, item := range x {
			walkStrings(item, f)
		}
	case map[string]interface{}:
		for _, item := range x {
			walkStrings(item, f)
		}
	}
}

func readDotEnv(root string) (workspaceID string, stageID int) {
	data, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		return "", 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		switch k {
		case "WORKSPACE_ID":
			workspaceID = v
		case "COREZOID_STAGE_ID":
			if n, err := parseInt(v); err == nil {
				stageID = n
			}
		}
	}
	return
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// readAliases loads _ALIASES_.json into two maps: forward "@name" -> conv_id,
// and reverse conv_id -> ["@name", ...]. Absence of the file is normal —
// returns empty maps, not an error.
//
// Real Corezoid exports write this file as a JSON **array** of alias records
// (one entry per link), not a flat name→id map:
//
//   [
//     {"short_name": "api-payments-create", "obj_to_id": 32883, "obj_to_type": "conv", ...},
//     {"short_name": "orphan-alias", "obj_to_id": null, ...},   // skip — no target
//     ...
//   ]
//
// Entries with a null / missing / non-numeric obj_to_id are aliases that were
// defined at the workspace level but never linked to a process, or link to
// something outside the exported scope. They must be silently skipped — the
// alias is real but unresolvable, so we can't emit an edge for it.
//
// For defence-in-depth (and to keep the pre-existing simple test fixtures
// working), we also accept a legacy flat-map shape. If neither parses, we
// return the JSON error as a warning and empty maps — the build continues
// treating the project as alias-less, which is the correct degraded mode.
func readAliases(root string) (map[string]string, map[string][]string, string) {
	path := filepath.Join(root, "_ALIASES_.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, map[string][]string{}, ""
	}
	if err != nil {
		return map[string]string{}, map[string][]string{}, "_ALIASES_.json unreadable: " + err.Error()
	}

	// Sniff the first non-whitespace byte — '[' means real array shape, '{'
	// means legacy map shape. Anything else is a malformed file.
	fwd := map[string]string{}
	rev := map[string][]string{}
	trim := strings.TrimLeft(string(data), " \t\r\n")
	switch {
	case strings.HasPrefix(trim, "["):
		var raw []map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return fwd, rev, "_ALIASES_.json malformed (array): " + err.Error()
		}
		for _, entry := range raw {
			// The alias key is short_name; title is a human-readable fallback
			// for legacy entries where short_name was left blank.
			alias := strings.TrimSpace(strAt(entry, "short_name"))
			if alias == "" {
				alias = strings.TrimSpace(strAt(entry, "title"))
			}
			if alias == "" {
				continue
			}
			convID := numberOrStringAsID(entry["obj_to_id"])
			if convID == "" {
				// obj_to_id null/missing → alias without a resolved target.
				// Legitimate export data — skip silently, no warning.
				continue
			}
			// obj_to_type != "conv" (e.g. "folder", "state") is still tracked
			// as an alias so by_alias lookups work, but callers won't find an
			// edge for it unless the target conv_id exists in Processes.
			key := "@" + strings.TrimPrefix(alias, "@")
			fwd[key] = convID
			rev[convID] = append(rev[convID], key)
		}
	case strings.HasPrefix(trim, "{"):
		// Legacy shape: {"@name": conv_id, ...}. Kept for test fixtures
		// written before we knew the real format; not seen in the wild.
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return fwd, rev, "_ALIASES_.json malformed (map): " + err.Error()
		}
		for k, v := range raw {
			convID := numberOrStringAsID(v)
			if convID == "" {
				continue
			}
			key := "@" + strings.TrimPrefix(k, "@")
			fwd[key] = convID
			rev[convID] = append(rev[convID], key)
		}
	default:
		return fwd, rev, "_ALIASES_.json is neither a JSON array nor an object — ignoring"
	}
	return fwd, rev, ""
}

// strAt safely returns m[key] as a string, "" if absent or wrong type.
func strAt(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// numberOrStringAsID accepts a JSON-decoded value and returns its string form
// if it's a positive integer (as float64 from encoding/json), a non-empty
// string, or an int. null / bool / empty string / negative / non-numeric all
// return "".
func numberOrStringAsID(v interface{}) string {
	switch x := v.(type) {
	case float64:
		if x <= 0 {
			return ""
		}
		return fmt.Sprintf("%d", int(x))
	case int:
		if x <= 0 {
			return ""
		}
		return fmt.Sprintf("%d", x)
	case string:
		if strings.TrimSpace(x) == "" {
			return ""
		}
		return x
	}
	return ""
}

// flattenAliases produces a lookup useful for by_alias output: both "@name"
// and "name" (without @) point to the same conv_id, so callers who forget the
// leading @ still resolve.
func flattenAliases(fwd map[string]string) map[string]string {
	out := map[string]string{}
	for alias, cid := range fwd {
		out[alias] = cid
		out[strings.TrimPrefix(alias, "@")] = cid
	}
	return out
}

func aliasesForConv(convID string, rev map[string][]string) []string {
	list, ok := rev[convID]
	if !ok {
		return []string{}
	}
	sorted := append([]string(nil), list...)
	sort.Strings(sorted)
	return sorted
}

// readEnvVars loads _ENV_VARS_.json into name -> description. The file format
// varies across Corezoid exports; we accept both {name: desc-string} and
// {name: {"description": "..."}} shapes. Values (env_var contents) are NEVER
// loaded — many env_vars are secrets themselves.
func readEnvVars(root string) (map[string]string, string) {
	path := filepath.Join(root, "_ENV_VARS_.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, ""
	}
	if err != nil {
		return map[string]string{}, "_ENV_VARS_.json unreadable: " + err.Error()
	}
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]string{}, "_ENV_VARS_.json malformed: " + err.Error()
	}
	out := map[string]string{}
	switch x := raw.(type) {
	case map[string]interface{}:
		for k, v := range x {
			desc := ""
			switch vv := v.(type) {
			case string:
				desc = "" // convention: string value = the secret; do NOT store
			case map[string]interface{}:
				if d, ok := vv["description"].(string); ok {
					desc = d
				}
			}
			out[k] = desc
		}
	case []interface{}:
		for _, item := range x {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			desc, _ := m["description"].(string)
			out[name] = desc
		}
	}
	return out, ""
}

// buildBreadcrumb walks up from a process file to root, reading the
// <id>_<name>.folder.json markers in each parent directory to build a
// slash-separated path such as "1234_root / 5678_billing / 9012_flows".
// Falls back to the directory basenames when no marker is found.
// breadcrumbCache maps an absolute directory path to its folder-marker name
// (or "" when none is present). Populated lazily during WalkDir so the same
// directory is only ReadDir'd once regardless of how many .conv.json files
// share it. Passed into buildBreadcrumb to eliminate O(files × depth)
// syscalls on large projects.
type breadcrumbCache map[string]string

func newBreadcrumbCache() breadcrumbCache { return make(breadcrumbCache) }

func (bc breadcrumbCache) nameForDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if name, ok := bc[abs]; ok {
		return name
	}
	name := folderMarkerName(dir)
	bc[abs] = name
	return name
}

func buildBreadcrumb(root, filePath string, cache breadcrumbCache) string {
	absRoot, _ := filepath.Abs(root)
	dir := filepath.Dir(filePath)
	var parts []string
	for {
		abs, _ := filepath.Abs(dir)
		if abs == absRoot {
			break
		}
		if abs == filepath.Dir(abs) {
			break
		}
		name := cache.nameForDir(dir)
		if name == "" {
			name = filepath.Base(dir)
		}
		parts = append([]string{name}, parts...)
		dir = filepath.Dir(dir)
	}
	return strings.Join(parts, " / ")
}

func folderMarkerName(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if reFolderMarker.MatchString(e.Name()) {
			return strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".folder.json"), ".stage.json")
		}
	}
	return ""
}

// uniqueSorted deduplicates and sorts a []string, allocating a new slice.
// Deterministic output matters for golden tests and jq queries.
func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	set := map[string]struct{}{}
	for _, s := range in {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func mapKeysSorted[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Secret detection (moved from index_secrets.go)
// ---------------------------------------------------------------------------

// reSecretFieldName matches any field/header name that plausibly contains a
// secret. Case-insensitive substring — "sessionToken", "api_key_2",
// "PrivateKeyPem" all match; "session_id" doesn't. Kept as substring, not
// whole-token, because real Corezoid diagrams use camelCase, snake_case, and
// composite names interchangeably.
var reSecretFieldName = regexp.MustCompile(`(?i)pass|password|secret|token|apikey|api_key|private_key`)

// looksLikeSecretValue is a second-pass filter used only on values found under
// secret-shaped names in diagrams (never on values from _ENV_VARS_.json, which
// we never even read, and never on values from .instance.json — those are only
// checked by field name, per TZ §4 п.7). The intent is to reduce noise from
// names like "next_step_password_reset" whose fields hold references or
// display text, not secrets. If the value under a secret-shaped name is a
// Corezoid template expression, empty, short, or has whitespace/URL shape, it
// is very unlikely to be a real secret and the hotspot is suppressed.
//
// This filter is intentionally conservative: false negatives (missed real
// secrets) are worse than false positives (extra hotspots), but a noisy
// security_hotspots list is ignored in the same way a noisy linter is.
func looksLikeSecretValue(v interface{}) bool {
	s, ok := v.(string)
	if !ok {
		// Non-string values (numbers, objects) — treat as secret to be safe;
		// we still won't write the value anywhere, only mark the hit.
		return true
	}
	trim := strings.TrimSpace(s)
	if trim == "" {
		return false
	}
	// Corezoid variable expression — resolves at runtime, not a hardcoded secret.
	if strings.HasPrefix(trim, "{{") && strings.HasSuffix(trim, "}}") {
		return false
	}
	// Too short to be a meaningful token/key.
	if len(trim) < 12 {
		return false
	}
	// Contains whitespace — display text, not a token.
	if strings.ContainsAny(trim, " \t\n") {
		return false
	}
	return true
}

// scanMapForSecrets walks a map's top-level string keys and returns the names
// of any key whose name matches the secret pattern. If checkValue is true, the
// value is additionally passed through looksLikeSecretValue and only names
// whose value shape confirms the suspicion are kept. Values are never returned.
func scanMapForSecrets(m map[string]interface{}, checkValue bool) []string {
	if len(m) == 0 {
		return nil
	}
	var hits []string
	for k, v := range m {
		if !reSecretFieldName.MatchString(k) {
			continue
		}
		if checkValue && !looksLikeSecretValue(v) {
			continue
		}
		hits = append(hits, k)
	}
	return hits
}

// ---------------------------------------------------------------------------
// Config references collector (moved from index_config_references.go)
// ---------------------------------------------------------------------------

// collectConfigReferences produces a per-ref summary of who reads which
// configured ref. The local half (used_by, read_fields, local_conv_id)
// always populates from the diagram scan. When a state-fetcher is
// supplied AND the ref resolves to a local state-store, the collector
// also fetches the store's current task contents through the fetcher
// and attaches them under Tasks with masking applied per cfg.
//
// The scan surfaces:
//   - used_by: conv_ids that mention `{{conv[@ref]...}}` in their logics
//   - read_fields: the `.ref[X]` field names observed after the ref, so
//     a reader sees which fields of the config are actually consumed
//   - local_conv_id: when the ref resolves via _ALIASES_.json to a
//     conv_id that is a local .conv.json in the project (typically a
//     state diagram), the caller can jump directly to it
//   - tasks: (only when fetcher != nil AND local_conv_id resolves) the
//     current contents of the state-store, with secret-shaped fields
//     masked in-place
//
// If the ref resolves to a local state-store, the corresponding
// pm.StateStores entry is extended with ReadBy / ReadFields so the
// state_stores section carries both writer info (existing WrittenBy) and
// reader info (new).
//
// Refs configured in the allow-list but never referenced anywhere in the
// project are omitted from the returned map. This keeps the section
// signal-heavy: 0 usages means "not part of the local flow", not "we
// bothered checking".
func collectConfigReferences(cfg ConfigReferencesConfig, pm *ProjectMap,
	usageByRef map[string][]string, fieldsByRef map[string][]string) map[string]*ConfigReferenceEntry {

	// Effective allow-list = configured tasks ∪ every local state-store's
	// alias. Rationale: a state-store IS a config source by construction
	// (other processes read/write it via {{conv[@name].ref[X]}}), and
	// forcing every project to add its state-store alias to the default
	// 10-entry seed list would silently blank out configs whose alias
	// doesn't happen to be in the seed. Auto-inclusion means the section
	// tracks every state-store the project already declares, without
	// per-project hand-editing.
	tasks := effectiveConfigRefTasks(cfg.Tasks, pm)
	if len(tasks) == 0 {
		return nil
	}
	out := map[string]*ConfigReferenceEntry{}
	for _, task := range tasks {
		if task.Ref == "" || task.Label == "" {
			continue
		}
		usedBy := usageByRef[task.Ref]
		readFields := fieldsByRef[task.Ref]
		// Skip refs no diagram references — leaving them in would clutter
		// the index with zero-usage stubs equivalent to "not present".
		if len(usedBy) == 0 && len(readFields) == 0 {
			continue
		}
		if usedBy == nil {
			usedBy = []string{}
		}
		if readFields == nil {
			readFields = []string{}
		}
		entry := &ConfigReferenceEntry{
			SourceRef:  task.Ref,
			UsedBy:     usedBy,
			ReadFields: readFields,
		}
		// Alias resolution. by_alias holds both "@name" and "name" (see
		// flattenAliases in index_builder.go) — try both without hunting
		// for the exact leading-@ convention this ref uses.
		if cid, ok := pm.ByAlias[task.Ref]; ok {
			if _, present := pm.Processes[cid]; present {
				entry.LocalConvID = cid
			}
		} else if cid, ok := pm.ByAlias["@"+task.Ref]; ok {
			if _, present := pm.Processes[cid]; present {
				entry.LocalConvID = cid
			}
		}
		// Cross-populate state_stores when the ref resolves to a local
		// state store. Non-state processes with the same conv_id are
		// deliberately skipped here — the state_stores section is
		// state-specific by design and mixing in regular processes would
		// muddle the semantics.
		if entry.LocalConvID != "" {
			if ss, ok := pm.StateStores[entry.LocalConvID]; ok {
				ss.ReadBy = mergeSorted(ss.ReadBy, usedBy)
				ss.ReadFields = mergeSorted(ss.ReadFields, readFields)
			}
		}
		out[task.Label] = entry
	}
	return out
}

// effectiveConfigRefTasks unions the user-configured allow-list with every
// alias attached to a local state-store process. The user's list wins on
// label conflicts (they may have chosen a specific display label for a
// ref), and duplicates by ref are dropped. Deterministic order for stable
// output: configured tasks first (in their config order), then
// auto-added state-store aliases in conv_id order.
func effectiveConfigRefTasks(configured []ConfigReferenceTask, pm *ProjectMap) []ConfigReferenceTask {
	out := make([]ConfigReferenceTask, 0, len(configured))
	seenRef := map[string]bool{}
	for _, t := range configured {
		if t.Ref == "" || t.Label == "" {
			continue
		}
		out = append(out, t)
		seenRef[t.Ref] = true
	}
	// Deterministic order: sort state-store conv_ids so re-runs produce
	// byte-identical output.
	convIDs := make([]string, 0, len(pm.StateStores))
	for cid := range pm.StateStores {
		convIDs = append(convIDs, cid)
	}
	sortStrings(convIDs)
	for _, cid := range convIDs {
		ss := pm.StateStores[cid]
		if ss == nil {
			continue
		}
		for _, alias := range ss.Aliases {
			name := stripAtPrefix(alias)
			if name == "" || seenRef[name] {
				continue
			}
			out = append(out, ConfigReferenceTask{Ref: name, Label: name})
			seenRef[name] = true
		}
	}
	return out
}

func stripAtPrefix(s string) string {
	if len(s) > 0 && s[0] == '@' {
		return s[1:]
	}
	return s
}

// sortStrings sorts a string slice in place.
func sortStrings(ss []string) { sort.Strings(ss) }

// mergeSorted merges two string slices into a sorted, deduplicated result.
// Used when a state-store entry may already have data from a previous
// state_stores-pass and needs to accumulate more.
func mergeSorted(a, b []string) []string {
	if len(a) == 0 {
		return uniqueSorted(b)
	}
	if len(b) == 0 {
		return uniqueSorted(a)
	}
	combined := make([]string, 0, len(a)+len(b))
	combined = append(combined, a...)
	combined = append(combined, b...)
	return uniqueSorted(combined)
}
