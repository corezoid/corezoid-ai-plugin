package main

// Constants controlling the build-project-index behavior. Threshold values are
// referenced from both the builder (populating graph_stats.high_fan_in/out) and
// from the describe-process helper (populating high_fan_in flag on identify
// results) so any change flows through both call sites automatically.
const (
	IndexSchemaVersion  = 1
	IndexHashHexLen     = 12
	IndexHighFanIn      = 5
	IndexHighFanOut     = 7
	IndexOutputDir      = ".corezoid"
	IndexMapFile        = "project-map.json"
	IndexQueriesFile    = "QUERIES.md"
	IndexConfigFile     = "index-config.json"
	IndexClaudeMdMarker = "corezoid-index"
)

// crossProcessLogicTypes is the canonical set of logic type strings that
// represent a cross-process reference in this codebase. Kept as a package-level
// helper so an accidental future addition (e.g. a new api_dispatch type)
// requires editing one place, not scattered switches.
var crossProcessLogicTypes = map[string]struct{}{
	"api_rpc":      {},
	"api_copy":     {},
	"api_get_task": {},
}

func isCrossProcessType(t string) bool {
	_, ok := crossProcessLogicTypes[t]
	return ok
}

// ProjectMap is the on-disk schema for .corezoid/project-map.json. Field order
// matches the schema documented in the TZ (Section 5) — do not reorder without
// bumping IndexSchemaVersion, since the field names are the public API used by
// jq queries in QUERIES.md and by consumers reading the file directly.
type ProjectMap struct {
	SchemaVersion     int                          `json:"schema_version"`
	GeneratedAt       string                       `json:"generated_at"`
	Root              string                       `json:"root"`
	WorkspaceID       string                       `json:"workspace_id,omitempty"`
	StageID           int                          `json:"stage_id,omitempty"`
	ProcessCount      int                          `json:"process_count"`
	StateStoreCount   int                          `json:"state_store_count"`
	InstanceCount     int                          `json:"instance_count"`
	Processes         map[string]*ProcessEntry     `json:"processes"`
	ByAlias           map[string]string            `json:"by_alias"`
	EnvVars           map[string]*EnvVarEntry      `json:"env_vars"`
	Edges             []Edge                       `json:"edges"`
	CallsIn           map[string][]string          `json:"calls_in"`
	UnresolvedTargets map[string][]string          `json:"unresolved_targets"`
	ExternalAPIs      map[string][]string          `json:"external_apis"`
	StateStores       map[string]*StateStoreEntry  `json:"state_stores"`
	Instances         map[string]*InstanceEntry    `json:"instances"`
	GraphStats        *GraphStats                  `json:"graph_stats"`
	SecurityHotspots  []SecurityHotspot            `json:"security_hotspots"`

	// ConfigReferences holds materialised contents of Simulator config-tasks
	// listed in IndexConfig.ConfigReferences.Tasks. Absent (omitempty) when
	// no fetch has been performed yet — this lets push-process rebuilds
	// preserve the block from the last pull-folder rather than serialising
	// an empty object on every save. See TZ #2 §7 for the reasoning: fetch
	// is expensive (network calls per ref), so it runs on pull, not push.
	ConfigReferences map[string]*ConfigReferenceEntry `json:"config_references,omitempty"`
}

// ConfigReferenceEntry summarises one configured ref name's usage across
// the local project. Everything here is derived from a pure local scan of
// .conv.json files — no network calls, no auth required, no values stored.
//
// To read the actual runtime values of the config, agents should call
// `list-node-tasks` on the LocalConvID process after building the index
// (see corezoid-index skill). Values are intentionally NOT stored in the
// index so that (a) no live tokens end up on disk, (b) agents always see
// fresh values rather than a stale snapshot from the last pull.
type ConfigReferenceEntry struct {
	// SourceRef is the ref name from index-config, e.g. "config".
	SourceRef string `json:"source_ref"`
	// UsedBy is the sorted, unique list of conv_ids that reference this
	// ref anywhere in their diagrams via {{conv[@ref]...}}.
	UsedBy []string `json:"used_by"`
	// ReadFields is the sorted, unique list of `.ref[X]` field names
	// observed across all diagrams — telling the reader which specific
	// fields of the config are consumed by the project.
	ReadFields []string `json:"read_fields"`
	// LocalConvID is populated when SourceRef resolves via _ALIASES_.json
	// to a conv_id present in the current project. Use this to call
	// `list-node-tasks` when you need the actual runtime values.
	LocalConvID string `json:"local_conv_id,omitempty"`
}

// ProcessEntry describes a single .conv.json process. Cross-process calls are
// NOT stored here — they live in ProjectMap.Edges. This is a deliberate
// invariant: the graph is stored once and calls_in/calls_out/state_stores
// derive from it, so an inconsistency between "raw calls per process" and
// "resolved edges" can't happen by construction.
type ProcessEntry struct {
	Title           string   `json:"title"`
	ConvType        string   `json:"conv_type"`
	Status          string   `json:"status"`
	Path            string   `json:"path"`
	Location        string   `json:"location"`
	Aliases         []string `json:"aliases"`
	DBCalls         []string `json:"db_calls"`
	EnvVarRefs      []string `json:"env_var_refs"`
	HasReceiverNode bool     `json:"has_receiver_node"`
	NodeCount       int      `json:"node_count"`
	Hash            string   `json:"hash"`
	MTime           string   `json:"mtime"`
}

// Edge is one directed link between processes. type is one of api_rpc,
// api_copy, api_get_task (see crossProcessLogicTypes). mode is only populated
// for api_copy (create/modify/delete). via_alias is populated when the target
// was referenced by @alias and resolved through _ALIASES_.json.
// Titles are omitted — resolve via .processes[$id].title to avoid duplication.
type Edge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Type     string `json:"type"`
	Mode     string `json:"mode,omitempty"`
	ViaAlias string `json:"via_alias,omitempty"`
}

type EnvVarEntry struct {
	Description string   `json:"description"`
	UsedBy      []string `json:"used_by"`
}

type StateStoreEntry struct {
	Title     string   `json:"title"`
	Aliases   []string `json:"aliases"`
	Path      string   `json:"path"`
	// WrittenBy — processes that mutate this state via api_copy edges
	// (derived from the graph; existing behaviour).
	WrittenBy []string `json:"written_by"`
	// ReadBy — processes that read this state via {{conv[@alias]...}}
	// references, populated only for state stores whose alias appears in
	// the index-config config_references allow-list. Complementary to
	// WrittenBy: together they answer "who writes, who reads".
	ReadBy []string `json:"read_by,omitempty"`
	// ReadFields — the specific `.ref[X]` field names observed across
	// diagram scans, so consumers see which parts of the state each
	// reader actually consumes.
	ReadFields []string `json:"read_fields,omitempty"`
}

type InstanceEntry struct {
	Title                string   `json:"title"`
	InstanceType         string   `json:"instance_type"`
	Path                 string   `json:"path"`
	SecretFieldsPresent  []string `json:"secret_fields_present"`
}

type OrphanedInfo struct {
	ConvID          string `json:"conv_id"`
	SuspiciousName  bool   `json:"suspicious_name"`
}

type GraphStats struct {
	HighFanIn   []string        `json:"high_fan_in"`
	HighFanOut  []string        `json:"high_fan_out"`
	Orphaned    []OrphanedInfo  `json:"orphaned"`
	EntryPoints []string        `json:"entry_points"`
	Cycles      [][]string      `json:"cycles"`
}

// SecurityHotspot is one location where a secret-shaped field name was
// detected. Source is "instance" or "diagram". For "instance" source,
// InstanceID and Path are populated; for "diagram" source, ConvID/NodeID/Path
// are populated. Fields lists the offending field names — NEVER their values.
type SecurityHotspot struct {
	Source      string   `json:"source"`
	InstanceID  string   `json:"instance_id,omitempty"`
	ConvID      string   `json:"conv_id,omitempty"`
	NodeID      string   `json:"node_id,omitempty"`
	Title       string   `json:"title"`
	Path        string   `json:"path"`
	Fields      []string `json:"fields"`
}
