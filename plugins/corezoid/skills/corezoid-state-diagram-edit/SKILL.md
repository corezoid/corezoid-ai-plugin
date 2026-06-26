---
name: corezoid-state-diagram-edit
description: >
  Corezoid state diagram editing specialist. Use when the user wants to modify,
  update, or fix an existing Corezoid state diagram — add or remove a state, change
  a transition, add a side effect on transition, fix the wiring of an api_callback
  state node, or rework transitions. Activate when the user says "edit a state
  diagram", "add a state", "remove a state", "change transitions", "fix state
  diagram", "update state machine", "поправить state diagram", "изменить состояния",
  or refers to modifying a .conv.json with conv_type "state".
---

# Edit an Existing Corezoid State Diagram

You are a specialist in modifying Corezoid **state diagrams** (`conv_type: "state"`) using the `corezoid` MCP server.

A state diagram is a long-lived data store; modifying it means changing the set of states, the data conditions that drive transitions between them, or the side effects performed on transition. The driver processes that read / write the diagram are usually edited separately via `/corezoid-edit`.

Read `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-overview.md` for a refresher on the model before editing.

---

## Identify the State Diagram (MANDATORY FIRST STEP)

**Before doing anything else**, resolve `PROCESS_PATH`:

1. Check whether the user already provided an identifier — a file path, state diagram name, or numeric id — in the current message or conversation history.
2. If no identifier is provided, ask:
   > "Please specify the state diagram — a file path (e.g. `1863140_User_Status.conv.json`), a name, or a state diagram id."
   Do **not** call any MCP tools until the user provides one.
3. If the user gives a **name or id**, search the local working directory for the matching `.conv.json` using `find` / `grep`.
4. Open the file and **confirm `"conv_type": "state"`** at the root. If `conv_type` is `"process"`, this is a regular process — hand off to `/corezoid-edit` instead.
5. Once `PROCESS_PATH` is confirmed, analyze the file before changing anything.

---

## Step 1: Analyze the State Diagram

Read the file and map out:

- The list of state nodes (every node with `obj_type: 0` whose first logic is `api_callback`). Note each state's `id`, `title`, and outbound `go_if_const` transitions.
- The Start node and which state it routes to (the initial state).
- Any helper nodes between states (Set Parameters, Code, Modify Task, Copy Task, Delay, Queue).
- Terminal nodes (`obj_type: 2`).

> 🔍 If you see `obj_type: 3` state nodes, you are looking at the legacy state-node format. The current Corezoid format uses `obj_type: 0` with `api_callback` as the first logic. Convert old nodes to the new format only if the user explicitly asks — otherwise leave them as-is and edit in place.

Make sure you also locate any **driver processes** that reference this state diagram (search the project for `conv[<sd_id>]`, `conv[@<alias>]`, and `api_copy` nodes with `conv_id: <sd_id>`). When editing, you may need to update those drivers too.

---

## Step 2: Plan the Edit

Categorise the change before touching JSON:

| Change type | What to touch |
|---|---|
| Add a new state | Insert a new `obj_type: 0` state node with `api_callback` + transitions + self-loop. Wire at least one inbound transition from an existing state's `go_if_const`. |
| Remove a state | Delete the node and re-route every inbound transition that pointed to it. Search the file for `to_node_id: "<deleted_id>"`. |
| Change a transition condition | Edit the `conditions` array of the relevant `go_if_const` in the source state. |
| Re-target a transition | Change `to_node_id` of the relevant `go_if_const`. |
| Add a side effect on transition | Insert a Modify Task / Copy Task / Set Parameters node between the source state and the target state. Update the source state's `go_if_const.to_node_id` to point at the new helper, and have the helper `go` to the original target. |
| Rename a state | Change `title` only — the `id` must stay the same to preserve drivers that reference it. |
| Add an alias | Hand off to `/corezoid-alias-manager`. |

---

## Step 3: Apply Changes

Edit `PROCESS_PATH` directly.

### Core rules

- Connect nodes only through `go` / `go_if_const` `to_node_id` fields.
- Every node that has `err_node_id` (Code, Set Parameters, Copy Task, Modify Task, Queue) must point at a real End: Error node — never to a state node.
- Node ids are 24-character hex: `^[0-9a-f]{24}$`. For new nodes, generate fresh ids; **never reuse** an old id, even if its node was deleted.
- Use descriptive `title` values — they are the state names visible on the canvas and dashboards.
- Layout: keep states roughly on the same `y` lane (≈ 400); increment `x` by ≈ 320–400 between adjacent states. Start at `y = 100`. End nodes at the bottom.

### State node invariants (do not break these)

For every state node:

- `obj_type: 0`
- First logic is exactly `{ "type": "api_callback" }` (no extra fields)
- Every outbound transition is a `go_if_const` between the `api_callback` and the trailing `go`
- The final logic is `{ "type": "go", "to_node_id": "<self_id>" }` — the "stay here" fallback
- `extra` contains `"icon":"state"`
- No `err_node_id` on `api_callback`

If you add or modify transitions, the order matters — **first matching `go_if_const` wins**. Put more specific conditions first.

### Allowed logics

Only these logics may appear inside a state diagram. Adding anything else will fail validation on push.

| Allowed | Type |
|---|---|
| Start | `go` (`obj_type: 1`) |
| State | `api_callback` + `go_if_const`s + self-`go` |
| Condition | `go_if_const` |
| Code | `api_code` |
| Set Parameters | `set_param` |
| Copy Task (fan-out) | `api_copy` with `mode: "create"` |
| Modify Task (by ref) | `api_copy` with `mode: "modify"` |
| Delay | semaphor-only |
| Queue | `api_queue` |
| End | (`obj_type: 2`) |

**Forbidden:** `api`, `api_rpc`, `api_rpc_reply`, `db_call`, `git_call`, `api_sum`, `api_form`. If the user asks to add one of these, push back: move the side effect into the driver process and explain why.

### Common pitfalls

- Adding a state but forgetting to wire any inbound transition → unreachable state.
- Deleting a state but leaving a `go_if_const` somewhere with `to_node_id` pointing to the deleted id → push will fail with "unknown node".
- Reordering transitions accidentally: the **first matching `go_if_const` wins**, so order is semantically meaningful.
- Using `api_copy mode: "modify"` from inside the state diagram targeting its own ref — that re-triggers `api_callback` and can loop. Use `set_param` to update the current task in place instead.
- Forgetting to update the driver process when you rename a state and the driver compares against its name (e.g. `{{conv[…].ref[…].status}} == "Active"`). The state name is `title`; the driver compares against a stored **value**, not the title — confirm with the user which they're checking.

---

## Step 4: Deploy the Changes

**MANDATORY: Always push after any change — even if work is in-flight. Without push, the changes exist only on disk.**

Call MCP tool **`push-process`** with `process_path: "<PROCESS_PATH>"`.

If push fails:
- Re-read the file and confirm `"conv_type": "state"` is still at the root (a stray editor save or auto-format may have flipped it).
- Lint with **`lint-process`** to localise the issue.
- Confirm every state node still ends in `go → self_id`.
- Confirm no forbidden logic types were introduced.

After a successful push, notify the user:

> "State diagram updated. Refresh the Corezoid page to see the new states / transitions. Any tasks already parked in renamed states keep their `id` references intact, but tasks parked in **deleted** states are now stranded — check the workspace before deleting a populated state."

> ⚠️ **Live data warning:** Unlike regular processes, a state diagram usually has **live tasks parked in its states**. Deleting or restructuring a state can strand those tasks. Before deleting a state, ask the user whether they want to migrate parked tasks first (e.g. by modifying their `ref` so they transition out of the doomed state).

---

## Step 5 (optional): Update Driver Processes

If your edit changed the **observable interface** of the state diagram — added a new field that drivers should now read, renamed a field drivers compare against, or removed a state drivers used to detect — hand off to `/corezoid-edit` for each driver process that needs updating.

To find driver processes affected by the edit, search the project:

```
grep -rn "conv\[<sd_id>\]" .
grep -rn "conv_id\": <sd_id>" .
grep -rn "conv\[@<alias>\]" .
```

---

## Reference Documents

| Path | When to read |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-overview.md` | Concepts, allowed nodes, root structure |
| `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-node-structures.md` | Canonical JSON for every allowed node type |
| `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-process-interaction.md` | How driver processes read / create / modify state tasks |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-state-node.md` | Background and the `{{conv[...]}}` template |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/copy-task-node.md` | Error catalogue for `api_copy` |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/condition-node.md` | `go_if_const` reference |
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Variables (`{{env_var[@…]}}`) |

## Example Files

| Path | Description |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/samples/state-diagrams/user-status-state-diagram.conv.json` | Minimal two-state diagram (`Active` ⇄ `Inactive`) |
| `${CLAUDE_PLUGIN_ROOT}/samples/state-diagrams/user-status-driver-process.conv.json` | Companion driver process |
