package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestRouters_EveryCollapsedToolIsReachable is the invariant that makes the
// router layer safe: a definition that left tools/list must still be callable
// by exactly one action, or it has been documented into unreachability — the
// model can neither see it nor name it.
func TestRouters_EveryCollapsedToolIsReachable(t *testing.T) {
	covered := map[string]string{}
	for _, r := range toolRouters {
		for _, a := range r.Actions {
			if prev, dup := covered[a.Action]; dup {
				t.Errorf("action %q is fronted by both %s and %s — actions must be unique across routers",
					a.Action, prev, r.Name)
			}
			covered[a.Action] = r.Name
			if _, ok := collapsedToolByName[a.Action]; !ok {
				t.Errorf("%s action %q has no definition in collapsedToolRegistry", r.Name, a.Action)
			}
			if _, ok := toolHandlers[a.Action]; !ok {
				t.Errorf("%s action %q has no handler in toolHandlers", r.Name, a.Action)
			}
			if strings.TrimSpace(a.Summary) == "" {
				t.Errorf("%s action %q has no summary — tools/list would show a bare name", r.Name, a.Action)
			}
		}
	}
	for _, def := range collapsedToolRegistry {
		if _, ok := covered[def.Name]; !ok {
			t.Errorf("collapsed tool %q is not reachable through any router action", def.Name)
		}
	}
}

// TestRouters_AdvertisedSetIsCorePlusRouters guards the split itself: a tool
// must be either advertised on its own or collapsed, never both and never
// neither. Both would be a duplicate name in one list; neither is a tool that
// exists only in the handler table.
func TestRouters_AdvertisedSetIsCorePlusRouters(t *testing.T) {
	advertised := map[string]bool{}
	for _, tool := range toolRegistry {
		if advertised[tool.Name] {
			t.Errorf("duplicate entry %q in tools/list", tool.Name)
		}
		advertised[tool.Name] = true
	}
	for _, def := range collapsedToolRegistry {
		if advertised[def.Name] {
			t.Errorf("%q is both advertised and collapsed", def.Name)
		}
	}
	for name := range toolHandlers {
		_, isCollapsed := collapsedToolByName[name]
		if !advertised[name] && !isCollapsed {
			t.Errorf("handler %q is neither advertised in tools/list nor reachable through a router", name)
		}
	}
	for _, r := range toolRouters {
		if !advertised[r.Name] {
			t.Errorf("router %q is missing from tools/list", r.Name)
		}
		if _, clash := toolHandlers[r.Name]; clash {
			t.Errorf("router name %q collides with a handler name", r.Name)
		}
	}
}

// TestRouters_SchemaShape checks what a host actually validates against:
// action (required, enumerated), args, help — and nothing else, since a
// property left out of the schema is an argument the host may strip.
func TestRouters_SchemaShape(t *testing.T) {
	for _, def := range routerToolDefs() {
		schema, ok := def.InputSchema.(map[string]interface{})
		if !ok {
			t.Fatalf("%s: InputSchema is %T", def.Name, def.InputSchema)
		}
		props, _ := schema["properties"].(map[string]interface{})
		for _, want := range []string{"action", "args", "help"} {
			if _, ok := props[want]; !ok {
				t.Errorf("%s: schema has no %q property", def.Name, want)
			}
		}
		if len(props) != len(routerTopLevelArgs) {
			t.Errorf("%s: schema declares %d properties, but resolveRouterCall accepts %d top-level keys",
				def.Name, len(props), len(routerTopLevelArgs))
		}
		req := schemaRequiredList(schema["required"])
		if len(req) != 1 || req[0] != "action" {
			t.Errorf("%s: required is %v, want [action]", def.Name, req)
		}
		action, _ := props["action"].(map[string]interface{})
		enum, _ := action["enum"].([]string)
		if len(enum) != len(routerByName[def.Name].Actions) {
			t.Errorf("%s: action enum lists %d values for %d actions", def.Name, len(enum), len(routerByName[def.Name].Actions))
		}
		for _, name := range enum {
			if !strings.Contains(action["description"].(string), name+":") {
				t.Errorf("%s: action %q has no summary in the enum description", def.Name, name)
			}
		}
		// The schema has to survive the trip through JSON — a Go-typed value
		// that marshals into something a host cannot validate is invisible here
		// but fatal in a client.
		if _, err := json.Marshal(def); err != nil {
			t.Errorf("%s: does not marshal: %v", def.Name, err)
		}
	}
}

func TestResolveRouterCall_ResolvesActionAndArgs(t *testing.T) {
	got := resolveRouterCall("cz-access", map[string]interface{}{
		"action": "delete-group",
		"args":   map[string]interface{}{"group_id": 7},
	})
	if !got.Handled || got.IsError {
		t.Fatalf("handled=%v isError=%v text=%q", got.Handled, got.IsError, got.Text)
	}
	if got.Tool != "delete-group" {
		t.Errorf("tool = %q, want delete-group", got.Tool)
	}
	if got.Args["group_id"] != 7 {
		t.Errorf("args = %v, want group_id passed through untouched", got.Args)
	}
}

func TestResolveRouterCall_NonRouterIsNotHandled(t *testing.T) {
	if got := resolveRouterCall("push-process", map[string]interface{}{}); got.Handled {
		t.Errorf("push-process was handled by the router layer: %+v", got)
	}
}

// A missing args object is not an error: several actions take no arguments at
// all (list-variables, git-pull-context), and a host that omits an empty
// object must not be punished for it.
func TestResolveRouterCall_MissingArgsBecomesEmptyMap(t *testing.T) {
	got := resolveRouterCall("cz-variables", map[string]interface{}{"action": "list-variables"})
	if got.Tool != "list-variables" || got.IsError {
		t.Fatalf("got %+v", got)
	}
	if got.Args == nil || len(got.Args) != 0 {
		t.Errorf("args = %v, want an empty map", got.Args)
	}
}

// The CLI passes args as a string, and some hosts serialize nested objects the
// same way. Accepting it here is what keeps `convctl cz-access
// action=create-group args={"title":"x"}` working.
func TestResolveRouterCall_JSONStringArgs(t *testing.T) {
	got := resolveRouterCall("cz-access", map[string]interface{}{
		"action": "create-group",
		"args":   `{"title":"Ops"}`,
	})
	if got.IsError || got.Tool != "create-group" {
		t.Fatalf("got %+v", got)
	}
	if got.Args["title"] != "Ops" {
		t.Errorf("args = %v, want title=Ops", got.Args)
	}

	bad := resolveRouterCall("cz-access", map[string]interface{}{
		"action": "create-group",
		"args":   "title=Ops",
	})
	if !bad.IsError || bad.Tool != "" {
		t.Errorf("a non-JSON args string must be refused, got %+v", bad)
	}
	if !strings.Contains(bad.Text, "title") {
		t.Errorf("the refusal should carry the action's schema, got %q", bad.Text)
	}
}

// Every failure path has to teach the call how to fix itself — that text is
// the only documentation an action has left once it is out of tools/list.
func TestResolveRouterCall_ErrorsCarryTheActionList(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"no action":      {},
		"unknown action": {"action": "delete-everything"},
		"flat args":      {"action": "delete-group", "group_id": 7},
	}
	for name, args := range cases {
		got := resolveRouterCall("cz-access", args)
		if !got.Handled || !got.IsError || got.Tool != "" {
			t.Errorf("%s: got %+v, want a handled error that runs nothing", name, got)
			continue
		}
		if !strings.Contains(got.Text, "create-group") || !strings.Contains(got.Text, "list-shares") {
			t.Errorf("%s: error text does not list the router's actions: %q", name, got.Text)
		}
	}
}

func TestResolveRouterCall_UnknownActionPointsAtTheRightEntryPoint(t *testing.T) {
	// An action of a neighbouring router.
	got := resolveRouterCall("cz-access", map[string]interface{}{"action": "list-folders"})
	if !strings.Contains(got.Text, "cz-structure") {
		t.Errorf("want a pointer to cz-structure, got %q", got.Text)
	}
	// A tool that was never collapsed.
	got = resolveRouterCall("cz-tasks", map[string]interface{}{"action": "run-task"})
	if !strings.Contains(got.Text, "tool of its own") {
		t.Errorf("want run-task reported as a standalone tool, got %q", got.Text)
	}
	// A typo.
	got = resolveRouterCall("cz-access", map[string]interface{}{"action": "list-group"})
	if !strings.Contains(got.Text, "list-groups") {
		t.Errorf("want a near-miss suggestion, got %q", got.Text)
	}
}

// help must be inert: it describes an action, it never runs one.
func TestResolveRouterCall_HelpRunsNothing(t *testing.T) {
	got := resolveRouterCall("cz-variables", map[string]interface{}{
		"action": "delete-variable",
		"help":   true,
	})
	if !got.Handled || got.Tool != "" || got.IsError {
		t.Fatalf("got %+v, want inert help", got)
	}
	for _, want := range []string{"PERMANENTLY", "confirm", "apply", "Arguments (inside args)"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("help text is missing %q:\n%s", want, got.Text)
		}
	}
}

func TestActionHelpText_CoversEveryAction(t *testing.T) {
	for _, r := range toolRouters {
		for _, a := range r.Actions {
			text := actionHelpText(r.Name, a.Action)
			if strings.Contains(text, "No schema registered") {
				t.Errorf("%s/%s: no schema behind the action", r.Name, a.Action)
				continue
			}
			if !strings.Contains(text, a.Action) {
				t.Errorf("%s/%s: help does not name the action", r.Name, a.Action)
			}
			def := collapsedToolByName[a.Action]
			schema, _ := def.InputSchema.(map[string]interface{})
			props, _ := schema["properties"].(map[string]interface{})
			for prop := range props {
				if !strings.Contains(text, prop) {
					t.Errorf("%s/%s: help omits argument %q", r.Name, a.Action, prop)
				}
			}
			for _, req := range schemaRequiredList(schema["required"]) {
				if !strings.Contains(text, req+" ") {
					t.Errorf("%s/%s: help omits required argument %q", r.Name, a.Action, req)
				}
			}
		}
	}
}

// The gating tables are keyed by real tool names, and the dispatcher resolves
// a router call before consulting them. This pins that relationship: a router
// must never itself appear in a gating table, or the softest action in it
// would set the bar for the whole domain.
func TestRouters_AreNotInAuthGatingTables(t *testing.T) {
	for _, r := range toolRouters {
		if isInSet(r.Name, noAuthTools) {
			t.Errorf("%s is listed in noAuthTools — every action behind it would skip auth", r.Name)
		}
		if isInSet(r.Name, tokenOnlyTools) {
			t.Errorf("%s is listed in tokenOnlyTools — its non-setup actions would skip workspace checks", r.Name)
		}
	}
	// And the collapsed tools that DO need the softer gates must still be
	// named there individually.
	for _, name := range []string{"list-workspaces", "list-projects", "list-stages", "create-project"} {
		if !isInSet(name, tokenOnlyTools) {
			t.Errorf("%s must stay in tokenOnlyTools — it is what the init flow uses before a workspace exists", name)
		}
	}
	for _, name := range []string{"git-pull-context", "git-push-context", "read-context-file", "update-context-file"} {
		if !isInSet(name, noAuthTools) {
			t.Errorf("%s must stay in noAuthTools — the git mirror has its own credentials", name)
		}
	}
}

// "help" and "list" are only shorthands for the action list while no router
// declares an action by those names; a real action must win, or declaring one
// would make it unreachable through its own router.
func TestResolveRouterCall_ActionNamesWinOverTheListShorthands(t *testing.T) {
	for _, shorthand := range []string{"help", "list"} {
		for _, r := range toolRouters {
			if _, clash := routerActions[r.Name][shorthand]; clash {
				t.Errorf("%s declares an action named %q — resolveRouterCall would have to choose, "+
					"and the shorthand path must be updated deliberately", r.Name, shorthand)
			}
		}
	}
	// With no clash, the shorthands answer with the action list and run nothing.
	for _, shorthand := range []string{"help", "list"} {
		got := resolveRouterCall("cz-snapshots", map[string]interface{}{"action": shorthand})
		if !got.Handled || got.Tool != "" || got.IsError {
			t.Errorf("%q: got %+v, want an inert action listing", shorthand, got)
		}
		if !strings.Contains(got.Text, "create-snapshot") {
			t.Errorf("%q: listing does not name the router's actions: %q", shorthand, got.Text)
		}
	}
}

// The help renderer walks schemas as they come off JSON as well as in their
// Go-literal form — a collapsed tool loaded from a file, or a schema that
// spells its type as ["string", "null"], must still print a readable line.
func TestHelpRendererHandlesEverySchemaSpelling(t *testing.T) {
	typeCases := []struct {
		name string
		prop map[string]interface{}
		want string
	}{
		{"go string", map[string]interface{}{"type": "integer"}, "integer"},
		{"go slice", map[string]interface{}{"type": []string{"string", "null"}}, "string|null"},
		{"json slice", map[string]interface{}{"type": []interface{}{"integer", "null"}}, "integer|null"},
		{"absent", map[string]interface{}{}, "any"},
	}
	for _, tc := range typeCases {
		if got := schemaTypeLabel(tc.prop); got != tc.want {
			t.Errorf("%s: schemaTypeLabel = %q, want %q", tc.name, got, tc.want)
		}
	}

	enumCases := []struct {
		name string
		prop map[string]interface{}
		want string
	}{
		{"go slice", map[string]interface{}{"enum": []string{"raw", "json"}}, ", one of: raw, json"},
		{"json slice", map[string]interface{}{"enum": []interface{}{"raw", 2}}, ", one of: raw, 2"},
		{"absent", map[string]interface{}{}, ""},
	}
	for _, tc := range enumCases {
		if got := enumLabel(tc.prop); got != tc.want {
			t.Errorf("%s: enumLabel = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRouterActionArgs_RejectsNonObjects(t *testing.T) {
	if _, err := routerActionArgs(42); err == nil {
		t.Error("a number should not be accepted as args")
	}
	got, err := routerActionArgs("   ")
	if err != nil || len(got) != 0 {
		t.Errorf("blank args string should read as no arguments, got %v, %v", got, err)
	}
	if _, err := routerActionArgs(`["a"]`); err == nil {
		t.Error("a JSON array should not be accepted as args")
	}
}

// help is a documentation flag, so every ambiguity in it must resolve toward
// documentation. A bare bool assertion read help:"true" as false and ran the
// action — i.e. asking delete-group for its schema deleted the group. The
// schema says boolean; hosts and models do not always agree.
func TestResolveRouterCall_NonBooleanHelpNeverExecutes(t *testing.T) {
	asksForHelp := []interface{}{true, "true", "True", " true ", "yes", "on", 1, 1.0, float64(2), []string{"?"}}
	for _, v := range asksForHelp {
		got := resolveRouterCall("cz-access", map[string]interface{}{
			"action": "delete-group",
			"args":   map[string]interface{}{"group_id": 7},
			"help":   v,
		})
		if got.Tool != "" || got.IsError {
			t.Errorf("help=%#v: got Tool=%q IsError=%v — a documentation request must never reach a handler",
				v, got.Tool, got.IsError)
		}
		if !strings.Contains(got.Text, "Arguments (inside args)") {
			t.Errorf("help=%#v: expected the action's schema, got %q", v, got.Text)
		}
	}

	runsTheAction := []interface{}{nil, false, "false", "False", "", "  ", "no", "off", 0, 0.0}
	for _, v := range runsTheAction {
		args := map[string]interface{}{
			"action": "delete-group",
			"args":   map[string]interface{}{"group_id": 7},
		}
		if v != nil {
			args["help"] = v
		}
		got := resolveRouterCall("cz-access", args)
		if got.Tool != "delete-group" || got.IsError {
			t.Errorf("help=%#v: got Tool=%q IsError=%v — an explicit negative must run the action",
				v, got.Tool, got.IsError)
		}
	}
}

// A routing miss is the cost of having collapsed a domain, and help is the
// signal that a one-line summary was not enough — both are answered without
// reaching a handler, so both have to be reported explicitly or they look like
// calls that never happened.
func TestHandleToolCall_RouterOnlyOutcomesReachAnalytics(t *testing.T) {
	prevCh, prevEnabled := analyticsCh, analyticsEnabled.Load()
	analyticsCh = make(chan AnalyticsEvent, 8)
	analyticsEnabled.Store(true)
	t.Cleanup(func() {
		analyticsCh, _ = prevCh, prevEnabled
		analyticsEnabled.Store(prevEnabled)
	})

	cases := []struct {
		name    string
		args    map[string]interface{}
		isError bool
	}{
		{"unknown action", map[string]interface{}{"action": "delete-everything"}, true},
		{"no action", map[string]interface{}{}, true},
		{"flat args", map[string]interface{}{"action": "delete-group", "group_id": 7}, true},
		{"help", map[string]interface{}{"action": "delete-group", "help": true}, false},
	}
	for _, tc := range cases {
		handleToolCall(context.Background(), "cz-access", tc.args)
		select {
		case e := <-analyticsCh:
			if e.Tool != "cz-access" {
				t.Errorf("%s: event tool = %q, want the router name", tc.name, e.Tool)
			}
			if e.IsError != tc.isError {
				t.Errorf("%s: isError = %v, want %v", tc.name, e.IsError, tc.isError)
			}
			if tc.isError && e.ErrorType != errorTypeRouterMiss {
				t.Errorf("%s: error_type = %q, want %q", tc.name, e.ErrorType, errorTypeRouterMiss)
			}
		default:
			t.Errorf("%s: no analytics event was emitted", tc.name)
		}
	}
}
