package main

import (
	"encoding/json"
	"strconv"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// IndexConfig holds the per-project heuristic keyword lists used by the
// entry_point / suspicious_name classification. Kept in
// .corezoid/index-config.json so teams working with non-English process names
// (Cyrillic in particular) can override the defaults without recompiling.
type IndexConfig struct {
	EntryPointNamePrefixes    []string `json:"entry_point_name_prefixes"`
	EntryPointLocationKeywords []string `json:"entry_point_location_keywords"`
	SuspiciousNameKeywords    []string `json:"suspicious_name_keywords"`

	// ConfigReferences is an explicit allow-list of Simulator tasks whose
	// contents get materialised into project-map.json's config_references
	// section. Contents that are not on this list are never fetched — that
	// is the mechanism that keeps client-data tasks out of the on-disk
	// index. See ConfigReferencesConfig for the field-level masking rules.
	ConfigReferences ConfigReferencesConfig `json:"config_references"`
}

// ConfigReferenceTask names one Simulator task to fetch during build.
// `ref` is the Simulator identifier used to call the API; `label` is the
// key under which the flattened contents appear in
// project-map.json's config_references{}.
type ConfigReferenceTask struct {
	Ref   string `json:"ref"`
	Label string `json:"label"`
}

// ConfigReferencesConfig controls both which tasks to fetch and how to
// redact leaf field values before writing them to project-map.json.
//
// Masking priority (applied per leaf field):
//   1. name ∈ NeverMaskFieldNames  → value kept, no matter what
//   2. name ∈ MaskFieldNames OR any of MaskFieldNamePatterns matches (i-flag) → value stripped
//   3. otherwise                    → value kept
//
// This lets a team scope-in one specific field ("secret_id_that_is_actually_public")
// while keeping the default mask permissive.
type ConfigReferencesConfig struct {
	Tasks                 []ConfigReferenceTask `json:"tasks"`
	MaskFieldNames        []string              `json:"mask_field_names"`
	MaskFieldNamePatterns []string              `json:"mask_field_name_patterns"`
	NeverMaskFieldNames   []string              `json:"never_mask_field_names"`
}

func defaultIndexConfig() IndexConfig {
	return IndexConfig{
		EntryPointNamePrefixes:     []string{"api", "page", "viewmodel", "webhook"},
		EntryPointLocationKeywords: []string{"api"},
		SuspiciousNameKeywords:     []string{"test", "tmp", "temp", "old", "draft", "backup", "copy of", "wip", "deprecated"},

		// Preseeded with common Simulator config-task refs. NOT universally
		// safe — see TZ §4 note: if a project uses one of these refs for
		// client data, the team must remove that row from
		// index-config.json before the first build. The indexer does not
		// distinguish "config" from "data" by content.
		ConfigReferences: ConfigReferencesConfig{
			Tasks: []ConfigReferenceTask{
				{Ref: "config", Label: "config"},
				{Ref: "c", Label: "c"},
				{Ref: "dev", Label: "dev"},
				{Ref: "prod", Label: "prod"},
				{Ref: "test", Label: "test"},
				{Ref: "accounts", Label: "accounts"},
				{Ref: "gmail", Label: "gmail"},
				{Ref: "simulator", Label: "simulator"},
				{Ref: "corezoid", Label: "corezoid"},
				{Ref: "jira", Label: "jira"},
			},
			MaskFieldNames:        []string{"token", "apiToken", "jwt", "secret", "password", "liteLLMToken"},
			// Patterns match the same names as reSecretFieldName in index_secrets.go
			// so hotspot detection and config masking cover the same field names.
			MaskFieldNamePatterns: []string{"token", "secret", "password", "api_key", "apikey", "private_key"},
			NeverMaskFieldNames:   []string{},
		},
	}
}

// LoadOrCreateIndexConfig reads .corezoid/index-config.json if present,
// filling any missing / malformed keys from defaults. If the file doesn't
// exist yet, writes a fresh default file and returns the defaults. This is the
// one file in the .corezoid/ set that isn't purely derived — it embodies a
// team choice about naming heuristics, so it must survive rebuilds.
//
// warnings is populated with human-readable diagnostics (bad key type,
// unreadable file) so the tool can surface them in its output without
// failing the whole build.
func LoadOrCreateIndexConfig(projectRoot string) (IndexConfig, []string, error) {
	cfg := defaultIndexConfig()
	var warnings []string

	dir := filepath.Join(projectRoot, IndexOutputDir)
	path := filepath.Join(dir, IndexConfigFile)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return cfg, warnings, err
		}
		if err := writeIndexConfig(path, cfg); err != nil {
			return cfg, warnings, err
		}
		return cfg, warnings, nil
	}
	if err != nil {
		warnings = append(warnings, "index-config.json unreadable: "+err.Error()+" — using defaults for this run")
		return cfg, warnings, nil
	}

	// Existing file — parse leniently. Per-key fallback: a malformed key
	// should degrade only that key, not the whole heuristic. Missing keys are
	// kept at their defaults.
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		warnings = append(warnings, "index-config.json is not valid JSON: "+err.Error()+" — using defaults for this run")
		return cfg, warnings, nil
	}

	if v, ok := raw["entry_point_name_prefixes"]; ok {
		if list, ok := stringList(v); ok {
			cfg.EntryPointNamePrefixes = list
		} else {
			warnings = append(warnings, "index-config.json: entry_point_name_prefixes must be an array of strings — using default")
		}
	}
	if v, ok := raw["entry_point_location_keywords"]; ok {
		if list, ok := stringList(v); ok {
			cfg.EntryPointLocationKeywords = list
		} else {
			warnings = append(warnings, "index-config.json: entry_point_location_keywords must be an array of strings — using default")
		}
	}
	if v, ok := raw["suspicious_name_keywords"]; ok {
		if list, ok := stringList(v); ok {
			cfg.SuspiciousNameKeywords = list
		} else {
			warnings = append(warnings, "index-config.json: suspicious_name_keywords must be an array of strings — using default")
		}
	}

	if v, ok := raw["config_references"]; ok && v != nil {
		if obj, ok := v.(map[string]interface{}); ok {
			// Sub-keys use the same per-key fallback pattern as the
			// top-level: one bad sub-key → default for that sub-key + warning,
			// the rest of the block still loads from the file.
			//
			// A `null` value at any sub-key is treated as "key intentionally
			// omitted, use default without warning" — the same interpretation
			// a missing key gets. json.Marshal of a Go struct with nil
			// slices produces `"tasks": null`, so a config file written
			// programmatically from a partially-populated struct doesn't
			// generate warning noise on the next load.
			cr := defaultIndexConfig().ConfigReferences

			if tv, ok := obj["tasks"]; ok && tv != nil {
				if tasks, warn := parseConfigTasks(tv); warn == "" {
					cr.Tasks = tasks
				} else {
					warnings = append(warnings, "index-config.json: config_references.tasks — "+warn+"; using default list")
				}
			}
			if mv, ok := obj["mask_field_names"]; ok && mv != nil {
				if list, ok := stringList(mv); ok {
					cr.MaskFieldNames = list
				} else {
					warnings = append(warnings, "index-config.json: config_references.mask_field_names must be an array of strings — using default")
				}
			}
			if mv, ok := obj["mask_field_name_patterns"]; ok && mv != nil {
				if list, ok := stringList(mv); ok {
					cr.MaskFieldNamePatterns = list
				} else {
					warnings = append(warnings, "index-config.json: config_references.mask_field_name_patterns must be an array of strings — using default")
				}
			}
			if mv, ok := obj["never_mask_field_names"]; ok && mv != nil {
				if list, ok := stringList(mv); ok {
					cr.NeverMaskFieldNames = list
				} else {
					warnings = append(warnings, "index-config.json: config_references.never_mask_field_names must be an array of strings — using default")
				}
			}
			cfg.ConfigReferences = cr
		} else {
			warnings = append(warnings, "index-config.json: config_references must be an object — using default")
		}
	}

	return cfg, warnings, nil
}

// parseConfigTasks accepts either a list of task objects
// ([{"ref":"...", "label":"..."}]) or a bare string list (["config", ...]),
// so hand-editing the file stays forgiving — a person writing just the ref
// strings and expecting `label` to default to `ref` is a plausible mistake
// worth accommodating.
func parseConfigTasks(v interface{}) ([]ConfigReferenceTask, string) {
	arr, ok := v.([]interface{})
	if !ok {
		return nil, "not an array"
	}
	out := make([]ConfigReferenceTask, 0, len(arr))
	for i, item := range arr {
		switch x := item.(type) {
		case string:
			if strings.TrimSpace(x) == "" {
				continue
			}
			out = append(out, ConfigReferenceTask{Ref: x, Label: x})
		case map[string]interface{}:
			ref, _ := x["ref"].(string)
			label, _ := x["label"].(string)
			if strings.TrimSpace(ref) == "" {
				return nil, "entry " + strconv.Itoa(i) + " missing ref"
			}
			if strings.TrimSpace(label) == "" {
				label = ref
			}
			out = append(out, ConfigReferenceTask{Ref: ref, Label: label})
		default:
			return nil, "entry " + strconv.Itoa(i) + " is not an object or string"
		}
	}
	return out, ""
}

func writeIndexConfig(path string, cfg IndexConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func stringList(v interface{}) ([]string, bool) {
	arr, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// shouldMaskField applies the priority-ordered masking rule from
// ConfigReferencesConfig's doc comment. The check is case-insensitive
// throughout — Simulator field naming is inconsistent (mix of camelCase,
// snake_case, PascalCase in different tasks), so lower-casing both sides
// is the only robust choice.
func (c ConfigReferencesConfig) shouldMaskField(fieldName string) bool {
	lower := strings.ToLower(fieldName)
	for _, n := range c.NeverMaskFieldNames {
		if strings.ToLower(n) == lower {
			return false
		}
	}
	for _, n := range c.MaskFieldNames {
		if strings.ToLower(n) == lower {
			return true
		}
	}
	for _, pat := range c.MaskFieldNamePatterns {
		if pat == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(pat)) {
			return true
		}
	}
	return false
}

// isEntryPointName returns true if the process title starts with one of the
// configured entry-point prefixes (case-insensitive).
func (c IndexConfig) isEntryPointName(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	for _, p := range c.EntryPointNamePrefixes {
		p = strings.ToLower(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// isEntryPointLocation returns true if the breadcrumb path contains any of the
// configured location keywords (case-insensitive substring match, since real
// exports use both "API" and "api"-suffixed folder names).
func (c IndexConfig) isEntryPointLocation(location string) bool {
	lower := strings.ToLower(location)
	for _, k := range c.EntryPointLocationKeywords {
		k = strings.ToLower(k)
		if k == "" {
			continue
		}
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// isSuspiciousName returns true if the process title contains one of the
// suspicious keywords as a whole word or a colon/underscore/dash-separated
// segment. A bare substring match would false-fire on names like
// "test_result_ok" containing "old" — that's why we split on non-word chars.
func (c IndexConfig) isSuspiciousName(title string) bool {
	lower := strings.ToLower(title)
	for _, kw := range c.SuspiciousNameKeywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw == "" {
			continue
		}
		if containsWholeToken(lower, kw) {
			return true
		}
	}
	return false
}

// containsWholeToken checks whether needle appears in haystack as a full
// alphanumeric token (bounded by non-alphanumeric on both sides, or by string
// edges). Both inputs are expected lowercase.
func containsWholeToken(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	// Multi-word needles ("copy of") — bounded substring match is enough,
	// tokenising by whitespace inside the needle would break them.
	if strings.ContainsAny(needle, " -_") {
		idx := 0
		for {
			pos := strings.Index(haystack[idx:], needle)
			if pos < 0 {
				return false
			}
			start := idx + pos
			end := start + len(needle)
			if (start == 0 || !isAlnum(rune(haystack[start-1]))) &&
				(end == len(haystack) || !isAlnum(rune(haystack[end]))) {
				return true
			}
			idx = start + 1
			if idx >= len(haystack) {
				return false
			}
		}
	}
	// Single-token needle — tokenise haystack and check exact matches.
	tokens := strings.FieldsFunc(haystack, func(r rune) bool { return !isAlnum(r) })
	for _, tok := range tokens {
		if tok == needle {
			return true
		}
	}
	return false
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
