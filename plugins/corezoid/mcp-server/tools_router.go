package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Domain routers exist to keep tools/list small.
//
// The registry used to advertise 72 tools in one 65 KB line — ~16k tokens of
// every session's context, and 136 bytes short of the 64 KiB line budget
// tools_registry_size_test.go guards, so no tool could be added without
// cutting another one's documentation (which is how 33cd553 dropped two
// operative push-process rules that 8c24756 had to restore).
//
// The CRUD-shaped domains are the cheapest thing to collapse: fifteen access
// tools spend 8 KB of schema to say "group id in, group out", and a skill
// mentions most of them once. So those domains are fronted by one router tool
// each: tools/list carries the router's action list, and the full argument
// schema of an action is served on demand — by `help`, and by the error a
// wrong call gets back.
//
// What is deliberately NOT collapsed: the tools that carry real decision
// weight in the model's hands (push-process, pull-process, lint-process,
// run-task, login, …). Their descriptions are the safety rules, and a nested
// {action, args} call is measurably easier to get wrong than a flat one.

// routerAction is one operation a router fronts. Action is the legacy tool
// name — unchanged on purpose: handlers, analytics, the CLI and every skill
// keep naming the same operation, only the call shape moves.
type routerAction struct {
	Action  string
	Summary string // one line; what tools/list says about this action
}

// toolRouter is a domain entry point: one tool in tools/list, many actions.
type toolRouter struct {
	Name        string
	Description string
	Actions     []routerAction
}

// toolRouters is the single source of truth for the router layer.
// Every Action must name a tool in collapsedToolRegistry, and every collapsed
// tool must be reachable through exactly one action — tools_router_test.go
// enforces both directions.
var toolRouters = []toolRouter{
	{
		Name: "cz-access",
		Description: "Access control for the current workspace: sharing, groups, API keys, invites. " +
			"Pick an action, pass its arguments in args. Grants are consequential — confirm the principal with the user before granting or revoking.",
		Actions: []routerAction{
			{"share-object", "grant/revoke access to a process/folder/stage/project for a user, API key or group"},
			{"list-shares", "who currently has access to an object"},
			{"find-principal", "search users, groups and API keys by substring — use it to resolve a name to an id"},
			{"list-groups", "user groups in the workspace"},
			{"create-group", "create a user group"},
			{"modify-group", "rename a group / change its description"},
			{"delete-group", "delete a group (destructive)"},
			{"list-group-objects", "processes shared with a group"},
			{"add-to-group", "add a user or API-key user to a group"},
			{"remove-from-group", "remove a user from a group"},
			{"list-api-keys", "API keys in the workspace"},
			{"create-api-key", "create an API key (its secret is shown once)"},
			{"modify-api-key", "rename an API key / change its description"},
			{"delete-api-key", "delete an API key (destructive — breaks integrations using it)"},
			{"invite-user", "invite an email to the workspace and share one object with them in one call"},
		},
	},
	{
		Name: "cz-structure",
		Description: "Workspace structure: workspaces, projects, stages, folders, and moving objects between them. " +
			"Pick an action, pass its arguments in args. The move and immutability actions are dry-run first and require the confirm token they return.",
		Actions: []routerAction{
			{"list-workspaces", "workspaces (companies) available to the authenticated user"},
			{"list-projects", "projects in a workspace"},
			{"show-project", "one project's metadata and the stages visible to the caller"},
			{"create-project", "create a project, optionally with stages"},
			{"modify-project", "rename a project / change short_name or description"},
			{"delete-project", "move a project to Trash (destructive)"},
			{"list-stages", "stages (environments) of a project"},
			{"set-stage-immutable", "set/clear a stage's immutable flag (needs confirm)"},
			{"list-folders", "immediate children of a folder: subfolders, processes, state diagrams"},
			{"show-folder", "one folder's metadata: title, obj_type, parent"},
			{"create-folder", "create a folder inside a parent folder"},
			{"modify-folder", "rename a folder / change its description"},
			{"delete-folder", "move a folder to Trash (destructive)"},
			{"move-folder", "reparent a normal folder, keeping its id and descendants; apply=false dry-run first"},
			{"move-process", "reparent a process/state diagram, keeping its id and graph; apply=false dry-run first"},
		},
	},
	{
		Name: "cz-tasks",
		Description: "Inspect and edit tasks already inside a deployed process: read one, edit its data, delete it, list what sits in a node, replay its path, read node statistics. " +
			"Pick an action, pass its arguments in args. To CREATE a task use run-task.",
		Actions: []routerAction{
			{"show-task", "current state of one task (data, node_id, status) by task_id and/or ref"},
			{"modify-task", "change a task's data; deep_merge=true merges instead of replacing"},
			{"delete-task", "delete a task from a process (destructive)"},
			{"list-node-tasks", "tasks currently parked in a node"},
			{"list-task-history", "the node path a task has taken"},
			{"get-node-stat", "in/out counts for a node over a time range"},
		},
	},
	{
		Name: "cz-dashboards",
		Description: "Corezoid dashboards and their charts: create a dashboard, add/modify charts bound to process node metrics, read them back, arrange the grid. " +
			"Pick an action, pass its arguments in args.",
		Actions: []routerAction{
			{"create-dashboard", "create a dashboard for visualizing node metrics"},
			{"get-dashboard", "read a dashboard with its charts and series"},
			{"add-chart", "add a chart (column/pie/funnel/table) bound to node series"},
			{"modify-chart", "replace a chart's name, type and series (send the full series list)"},
			{"get-chart", "read one chart with its series"},
			{"set-dashboard-layout", "save chart positions on the dashboard grid"},
		},
	},
	{
		Name: "cz-variables",
		Description: "Environment variables (env_var) of the current stage — the only place constants like URLs, tokens and ids may live, referenced as {{env_var[@name]}}. " +
			"The stage comes from the workspace's <id>_<name>.stage.json marker; there is no stage argument. " +
			"Pick an action, pass its arguments in args. modify and delete are dry-run by default and need an explicit confirm token after showing the user the diff.",
		Actions: []routerAction{
			{"list-variables", "all variables of the stage: short_name, obj_id, type, title, value"},
			{"create-variable", "create a variable (name, description, value)"},
			{"modify-variable", "change value/title/data_type or rename; apply=false dry-run first, then apply=true + confirm"},
			{"delete-variable", "permanently delete a variable; apply=false dry-run first, then apply=true + confirm (destructive)"},
		},
	},
	{
		Name: "cz-snapshots",
		Description: "Manual server-state checkpoints of a process, independent of the automatic pre-push snapshot: take one before an experiment, list them, read one back for diffing, delete one. " +
			"Identify the process by EXACTLY ONE of process_path or process_id. Pick an action, pass its arguments in args.",
		Actions: []routerAction{
			{"create-snapshot", "checkpoint the process's current server state"},
			{"list-snapshots", "snapshots of a process, newest first"},
			{"get-snapshot", "node list of one snapshot, for diffing against the current process"},
			{"delete-snapshot", "delete a snapshot (destructive — the checkpoint is gone)"},
		},
	},
	{
		Name: "cz-git-context",
		Description: "The workspace's git mirror in .git-context/: pull it, read any file from it, write into its _ext/ area, push the result. " +
			"Only _ext/ is writable — everything else is generated from Corezoid. Pick an action, pass its arguments in args.",
		Actions: []routerAction{
			{"git-pull-context", "clone or pull the mirror into .git-context/"},
			{"read-context-file", "read a file from .git-context/"},
			{"update-context-file", "write or append a file inside _ext/"},
			{"git-push-context", "commit and push local _ext/ changes"},
		},
	},
}

// routerByName indexes toolRouters; actionRouter maps a collapsed tool name
// back to the router that fronts it (for error messages that have to name the
// right entry point).
// Indexed once at package-variable initialization rather than in init(), so
// anything else initialized as a var can rely on them (see the note on
// collapsedToolByName in tools_registry.go).
var (
	routerByName  = indexRouters()
	actionRouter  = indexActionOwners()
	routerActions = indexRouterActions()
)

func indexRouters() map[string]toolRouter {
	byName := make(map[string]toolRouter, len(toolRouters))
	for _, r := range toolRouters {
		byName[r.Name] = r
	}
	return byName
}

// indexActionOwners maps an action to the router that fronts it. Actions are
// unique across routers (tools_router_test.go enforces it), so a model that
// calls the right action on the wrong router can be told where it lives.
func indexActionOwners() map[string]string {
	owner := make(map[string]string)
	for _, r := range toolRouters {
		for _, a := range r.Actions {
			owner[a.Action] = r.Name
		}
	}
	return owner
}

func indexRouterActions() map[string]map[string]routerAction {
	byRouter := make(map[string]map[string]routerAction, len(toolRouters))
	for _, r := range toolRouters {
		actions := make(map[string]routerAction, len(r.Actions))
		for _, a := range r.Actions {
			actions[a.Action] = a
		}
		byRouter[r.Name] = actions
	}
	return byRouter
}

// aggregateHints derives a router's annotations from the tools it fronts,
// worst case wins: a router is read-only only if every action is, and
// destructive as soon as one action is. Advertising anything softer would
// tell a client "no confirmation needed" for a call that can delete a group.
//
// The known cost, accepted deliberately: 21 read-only actions now sit behind
// an entry that does not claim readOnlyHint, and a host permission rule names
// a TOOL — so allowing cz-tasks allows delete-task along with show-task, where
// before a rule could allow show-task alone. Splitting each domain into read
// and write halves would restore both signals at roughly 6 KB and twice the
// entry points for a model to miss on; the collapse is only worth doing if it
// stays a collapse. Revisit if a host starts auto-approving by annotation, or
// if telemetry shows people writing allow rules for whole write domains.
func (r toolRouter) aggregateHints() *toolAnnotations {
	readOnly, idempotent := true, true
	destructive, openWorld := false, false
	for _, a := range r.Actions {
		def, ok := collapsedToolByName[a.Action]
		if !ok || def.Annotations == nil {
			// An action with no definition is a registry bug the tests catch;
			// until then assume the least safe answer.
			return toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld)
		}
		readOnly = readOnly && isHintTrue(def.Annotations.ReadOnlyHint)
		idempotent = idempotent && isHintTrue(def.Annotations.IdempotentHint)
		destructive = destructive || isHintTrue(def.Annotations.DestructiveHint)
		openWorld = openWorld || isHintTrue(def.Annotations.OpenWorldHint)
	}
	return toolHints(readOnly, destructive, idempotent, openWorld)
}

func isHintTrue(b *bool) bool { return b != nil && *b }

// toolDef renders the router as the single mcpTool that reaches tools/list.
// The action list lives in the enum's description as one line per action:
// same information as 15 separate tool entries, a twentieth of the bytes.
func (r toolRouter) toolDef() mcpTool {
	enum := make([]string, 0, len(r.Actions))
	lines := make([]string, 0, len(r.Actions))
	for _, a := range r.Actions {
		enum = append(enum, a.Action)
		lines = append(lines, a.Action+": "+a.Summary)
	}
	return mcpTool{
		Name:        r.Name,
		Description: r.Description + " Unsure about an action's arguments? Call it with help=true — that returns its full schema and runs nothing.",
		Annotations: r.aggregateHints(),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        enum,
					"description": strings.Join(lines, ". "),
				},
				"args": map[string]interface{}{
					"type":        "object",
					"description": "Arguments of the chosen action, as an object. Omit for actions that take none. Unknown keys are rejected, so use help=true when unsure.",
				},
				"help": map[string]interface{}{
					"type":        "boolean",
					"description": "Return the action's full argument schema instead of running it. Nothing is executed and no state changes.",
				},
			},
			"required": []string{"action"},
		},
	}
}

// routerToolDefs returns the router entries for tools/list, in declaration
// order.
func routerToolDefs() []mcpTool {
	defs := make([]mcpTool, 0, len(toolRouters))
	for _, r := range toolRouters {
		defs = append(defs, r.toolDef())
	}
	return defs
}

// routerCallResult is what resolveRouterCall reports back to the dispatcher.
// Handled=false means "not a router call, carry on"; Tool=="" with Handled
// means the call was answered here (help text, or a routing error) and must
// not reach a handler.
type routerCallResult struct {
	Handled bool
	Tool    string
	Args    map[string]interface{}
	Text    string
	IsError bool
}

// routerTopLevelArgs are the only keys a router call may carry; anything else
// is almost always an action argument that forgot to go inside args.
var routerTopLevelArgs = map[string]bool{"action": true, "args": true, "help": true}

// resolveRouterCall maps a router invocation onto the collapsed tool it
// fronts. Every failure path answers with the information needed to fix the
// call — the action list, or the action's full schema — because that text is
// the only documentation a model has for an action once it left tools/list.
func resolveRouterCall(name string, args map[string]interface{}) routerCallResult {
	r, ok := routerByName[name]
	if !ok {
		return routerCallResult{}
	}

	if stray := strayRouterArgs(r, args); stray != "" {
		return routerCallResult{Handled: true, IsError: true, Text: stray}
	}

	action, _ := args["action"].(string)
	action = strings.TrimSpace(action)
	wantHelp := helpRequested(args["help"])

	_, isRealAction := routerActions[r.Name][action]
	// "help" and "list" are conveniences for "show me what this router does",
	// but a real action always wins — otherwise adding an action by either
	// name would silently make it unreachable.
	if !isRealAction && (action == "" || action == "help" || action == "list") {
		return routerCallResult{Handled: true, IsError: action == "", Text: r.actionListText()}
	}
	if !isRealAction {
		return routerCallResult{Handled: true, IsError: true, Text: r.unknownActionText(action)}
	}
	if wantHelp {
		return routerCallResult{Handled: true, Text: actionHelpText(r.Name, action)}
	}

	actionArgs, err := routerActionArgs(args["args"])
	if err != nil {
		return routerCallResult{Handled: true, IsError: true,
			Text: fmt.Sprintf("Error: %s in %s action %q. %s", err, r.Name, action, actionHelpText(r.Name, action))}
	}
	return routerCallResult{Handled: true, Tool: action, Args: actionArgs}
}

// strayRouterArgs catches the flat call — {"group_id": 7} instead of
// {"action": "...", "args": {"group_id": 7}} — and says where the keys go,
// rather than letting unknownArgsError report them against the router.
func strayRouterArgs(r toolRouter, args map[string]interface{}) string {
	var stray []string
	for k := range args {
		if !routerTopLevelArgs[k] {
			stray = append(stray, k)
		}
	}
	if len(stray) == 0 {
		return ""
	}
	sort.Strings(stray)
	return fmt.Sprintf("Error: %s takes only action, args and help at the top level. "+
		"Move these inside args: %s — i.e. call it as {\"action\": \"<action>\", \"args\": {…}}.\n%s",
		r.Name, strings.Join(stray, ", "), r.actionListText())
}

// helpRequested reads the help flag the way a caller means it, not the way the
// schema wishes it were typed. A bare `.(bool)` assertion reads help:"true" as
// FALSE and runs the action — so asking delete-group for its schema deletes the
// group. coerceCLIArgs carries the same lesson from the other direction, where
// a string `apply=true` degraded into a silent dry-run; here the failure opens
// toward execution, which is the worse half.
//
// So: anything present that is not an explicit negative asks for help. An
// unrecognised value resolves to help as well — a caller who typed something
// odd into a documentation flag gets documentation, never a destructive call.
func helpRequested(v interface{}) bool {
	switch h := v.(type) {
	case nil:
		return false
	case bool:
		return h
	case string:
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "", "false", "0", "no", "off":
			return false
		}
		return true
	case float64: // JSON numbers decode as float64
		return h != 0
	case int:
		return h != 0
	default:
		return true
	}
}

// routerActionArgs normalizes the args payload. A JSON string is accepted
// because that is what the CLI passes (args='{"title":"x"}') and what some
// MCP hosts do to nested objects.
func routerActionArgs(v interface{}) (map[string]interface{}, error) {
	switch a := v.(type) {
	case nil:
		return map[string]interface{}{}, nil
	case map[string]interface{}:
		return a, nil
	case string:
		if strings.TrimSpace(a) == "" {
			return map[string]interface{}{}, nil
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(a), &parsed); err != nil {
			return nil, fmt.Errorf("args is a string that is not a JSON object (%v)", err)
		}
		return parsed, nil
	default:
		return nil, fmt.Errorf("args must be an object, got %T", v)
	}
}

// coerceRouterCLIArgs applies the CLI's string→type conversion to the args of
// a router call, so `convctl cz-tasks action=modify-task args='{"deep_merge":
// "true"}'` behaves like the flat `convctl modify-task deep_merge=true` it
// replaces. Without it the CLI's own escape hatch stopped at the router: the
// top-level keys were coerced and the action's arguments were not.
//
// CLI only. Over MCP the same mistake is refused by argTypeError rather than
// guessed at — see the note there on why coercion is the wrong answer when the
// transport already has a boolean type.
func coerceRouterCLIArgs(tool string, args map[string]interface{}) error {
	r, isRouter := routerByName[tool]
	if !isRouter {
		return nil
	}
	action, _ := args["action"].(string)
	action = strings.TrimSpace(action)
	if _, known := routerActions[r.Name][action]; !known {
		return nil // the router itself will explain what is wrong
	}
	actionArgs, err := routerActionArgs(args["args"])
	if err != nil {
		return nil // likewise: resolveRouterCall reports this with the schema
	}
	if err := coerceCLIArgs(action, actionArgs); err != nil {
		return err
	}
	args["args"] = actionArgs
	return nil
}

// actionListText is the router's own documentation: every action with its
// one-liner. Returned when a call names no action, asks for help, or names an
// action that does not exist.
func (r toolRouter) actionListText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s actions (call as {\"action\": \"<action>\", \"args\": {…}}; add help=true for an action's full schema):\n", r.Name)
	for _, a := range r.Actions {
		fmt.Fprintf(&b, "  %s — %s\n", a.Action, a.Summary)
	}
	return strings.TrimRight(b.String(), "\n")
}

// unknownActionText names the closest match when there is one — a model that
// called "list-group" or reached for an action of a neighbouring router
// should not have to guess twice.
func (r toolRouter) unknownActionText(action string) string {
	var hint string
	if other, ok := actionRouter[action]; ok {
		hint = fmt.Sprintf(" Action %q belongs to %s.", action, other)
	} else if _, ok := toolHandlers[action]; ok {
		// A core tool that was never collapsed — pull-process, run-task and
		// friends. They are still their own entries in tools/list, so say so
		// instead of offering a near-miss inside this router.
		hint = fmt.Sprintf(" %q is a tool of its own — call it directly, not through a router.", action)
	} else if near := nearestAction(r, action); near != "" {
		hint = fmt.Sprintf(" Did you mean %q?", near)
	}
	return fmt.Sprintf("Error: unknown action %q for %s.%s\n%s", action, r.Name, hint, r.actionListText())
}

// nearestAction returns the action sharing the longest prefix with the typo,
// if that prefix is long enough to be meaningful. Deliberately cruder than an
// edit distance: it only has to beat "no hint at all".
func nearestAction(r toolRouter, action string) string {
	best, bestLen := "", 4
	for _, a := range r.Actions {
		n := commonPrefixLen(a.Action, action)
		if n > bestLen {
			best, bestLen = a.Action, n
		}
	}
	return best
}

func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// actionHelpText renders one action's full contract: the description and
// argument schema the tool carried before it was collapsed. This is where
// that documentation moved — out of every session's tools/list, into the
// answer to a call that actually needs it.
func actionHelpText(router, action string) string {
	def, ok := collapsedToolByName[action]
	if !ok {
		return fmt.Sprintf("No schema registered for action %q.", action)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s action %q\n\n%s\n", router, action, def.Description)

	schema, _ := def.InputSchema.(map[string]interface{})
	props, _ := schema["properties"].(map[string]interface{})
	required := map[string]bool{}
	for _, k := range schemaRequiredList(schema["required"]) {
		required[k] = true
	}
	if len(props) == 0 {
		fmt.Fprintf(&b, "\nArguments: none. Call it as {\"action\": %q}.\n", action)
		return b.String()
	}

	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)

	b.WriteString("\nArguments (inside args):\n")
	for _, k := range names {
		p, _ := props[k].(map[string]interface{})
		fmt.Fprintf(&b, "  %s (%s)%s%s\n", k, schemaTypeLabel(p), requiredLabel(required[k]), enumLabel(p))
		if d, _ := p["description"].(string); d != "" {
			fmt.Fprintf(&b, "      %s\n", d)
		}
	}
	if anyOf, ok := schema["anyOf"].([]map[string]interface{}); ok && len(anyOf) > 0 {
		b.WriteString("\nExactly one of these argument sets must be supplied: ")
		groups := make([]string, 0, len(anyOf))
		for _, alt := range anyOf {
			groups = append(groups, strings.Join(schemaRequiredList(alt["required"]), "+"))
		}
		b.WriteString(strings.Join(groups, " | ") + "\n")
	}
	fmt.Fprintf(&b, "\nCall it as {\"action\": %q, \"args\": {…}}.\n", action)
	return b.String()
}

func requiredLabel(req bool) string {
	if req {
		return ", required"
	}
	return ""
}

// schemaTypeLabel renders a property's declared type, including the
// ["string", "null"] form the process-target properties use.
func schemaTypeLabel(p map[string]interface{}) string {
	switch t := p["type"].(type) {
	case string:
		return t
	case []string:
		return strings.Join(t, "|")
	case []interface{}:
		parts := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "|")
	}
	return "any"
}

func enumLabel(p map[string]interface{}) string {
	switch e := p["enum"].(type) {
	case []string:
		return ", one of: " + strings.Join(e, ", ")
	case []interface{}:
		parts := make([]string, 0, len(e))
		for _, v := range e {
			parts = append(parts, fmt.Sprint(v))
		}
		return ", one of: " + strings.Join(parts, ", ")
	}
	return ""
}
