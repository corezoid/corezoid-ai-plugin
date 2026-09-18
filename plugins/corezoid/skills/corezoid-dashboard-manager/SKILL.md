---
name: corezoid-dashboard-manager
description: >
  Creates and manages Corezoid dashboards — adds charts (column, pie, funnel, table), binds metrics
  to process nodes, configures real-time mode, and sets up drill-down linking between dashboards.
  Activate whenever a user asks to create a dashboard, add a chart, visualize process metrics,
  set up reporting for a Corezoid process, configure real-time monitoring, or asks what
  dashboards or charts exist. Also activate when the user wants to show task counts, completion
  rates, error rates, or any other process statistics visually in Corezoid.
---

# Corezoid Dashboard Manager

## How to call these tools

Every operation in this skill is an **action of the single `cz-dashboards` MCP tool** —
the individual names below are action strings, not tools of their own:

```
cz-dashboards {"action": "add-chart", "args": {"dashboard_id": 1234, "name": "Errors", "chart_type": "column", "series": "[…]"}}
```

Arguments always go inside `args`; the shorthand used in the examples below — `add-chart(dashboard_id=1234, …)` — means exactly that call. When unsure about an action's
arguments, call `cz-dashboards {"action": "<action>", "help": true}` — it returns the
full schema and runs nothing.

## What dashboards are

A Corezoid dashboard visualizes **task counters in process nodes** — it shows how many tasks
are accumulated in (or have passed through) specific nodes. The data comes directly from
running processes, not from external databases.

Key implication: processes must be deployed and have tasks flowing through them for data to appear.

---

## Actions of `cz-dashboards`

| Action | Purpose |
|--------|---------|
| `create-dashboard` | Create a new dashboard, returns `obj_id` (= dashboard_id) |
| `get-dashboard` | Get dashboard details including all charts and series |
| `add-chart` | Add a chart to a dashboard, returns `obj_id` (hex chart ID) |
| `get-chart` | Get a single chart with its series |
| `modify-chart` | Modify an existing chart — always provide full series array |
| `set-dashboard-layout` | Save chart positions on the grid — **required** to make charts visible |

`pull-process` is a tool of its own (not an action): call it directly to pull the
process JSON and find the node IDs a series points at.

---

## Workflow: Create a dashboard with charts

### Step 1 — Identify the process (MANDATORY FIRST STEP)

**Before doing anything else**, resolve the target process:

1. Check whether the user already provided a process identifier — a file path, process name, or process ID — in the current message or conversation history.
2. If no identifier is provided, ask:

   > "Which process(es) do you want to monitor? You can provide a file path (e.g. `123_payment.conv.json`), a process name, or a process ID."

   Do **not** call any MCP tools until the user provides an identifier.
3. If the user gave a **name or ID** (not a file path), search the local working directory for the matching `.conv.json` file using the `find` or `grep` Bash tools (the project is already pulled locally).
4. Once the file path is known and the file exists locally, open and read it to find node IDs in `scheme.nodes[].id` — note the IDs of nodes you want to measure. End nodes are best as primary metric sources.

   If the process is not available locally, fall back to:
   ```
   pull-process(process_id=<PROC_ID>)
   ```

5. Also clarify with the user (if not already clear):
   - Which nodes represent the metrics they care about?
   - What kind of visualization: comparison (column), proportions (pie), sequential drop-off (funnel), or tabular (table)?

### Step 2 — Create the dashboard

```
create-dashboard(title="Payment Monitoring", description="Real-time payment flow metrics")
```

Note the returned `dashboard_id` — needed for adding charts.

### Step 3 — Add charts

One chart per visualization. The `add-chart` action returns a hex `obj_id` for the chart — save it for `modify-chart` calls.

```
add-chart(
  dashboard_id=<dashboard_id>,
  name="Payment Outcomes",
  chart_type="column",
  series='[{"conv_id": 123456, "node_id": "507f1f77bcf86cd799439016", "title": "Success"}, {"conv_id": 123456, "node_id": "507f1f77bcf86cd799439017", "title": "Error"}]'
)
```

Chart types:

| Type | When to use |
|------|------------|
| `column` | Comparing values across nodes or time periods |
| `pie` | Showing how tasks split across outcomes |
| `funnel` | Visualizing sequential drop-off through a flow |
| `table` | Tabular display of metric values |

> **Critical:** Use `column` (NOT `bar`) — `bar` is not a valid Corezoid chart type.

### Step 4 — Verify series after creation

After creating a chart, call the `get-chart` action to verify that `series` is populated. If it's empty, use `modify-chart` to add the series.

```
get-chart(chart_id=<hex_obj_id>, dashboard_id=<dashboard_id>)
```

If `series` is empty, call the `modify-chart` action with the full series array.

### Step 5 — Save the dashboard layout (MANDATORY)

**Charts are invisible until the grid layout is saved.** After all charts are created and have series, call the `set-dashboard-layout` action:

```
set-dashboard-layout(
  dashboard_id=<dashboard_id>,
  grid='[
    {"chart_id":"<hex1>","x":0,"y":0,"width":6,"height":4},
    {"chart_id":"<hex2>","x":6,"y":0,"width":6,"height":4},
    {"chart_id":"<hex3>","x":0,"y":4,"width":12,"height":4}
  ]'
)
```

Grid layout rules:
- Grid is **12 columns wide**
- `x` + `width` must not exceed 12
- Standard chart: `width: 6, height: 4` (two charts per row)
- Wide chart: `width: 12, height: 4` (full row)
- Charts stack vertically by incrementing `y` (use the previous row's `height` as the next `y`)
- `chart_id` is the hex `obj_id` returned by `add-chart`

### Step 6 — Advise on real-time mode

Real-time mode works ONLY for these node types:
- **End nodes** (`obj_type: 2`) — tasks that finished the process
- **Waiting for Callback** — tasks waiting for external HTTP callback
- **Delay** — tasks paused for a time period
- **Set State** — tasks in a named state

Intermediate nodes (Code, API Call, Condition) pass tasks through instantly — real-time shows 0.

---

## Modifying charts — full payload required

When modifying a chart, **always include the full `series` array**. Partial updates are NOT
supported — omitting any field returns a validation error.

- `chart_id` — hex string from `add-chart` response (`obj_id`)
- `chart_type` sets `obj_type` in the API — must be `"column"`, `"pie"`, `"funnel"`, or `"table"`

```
modify-chart(
  chart_id="6a043a89e552e86e908941aa",
  dashboard_id=136542,
  name="Updated Chart Title",
  chart_type="column",
  series='[{"conv_id": 123456, "node_id": "507f1f77bcf86cd799439016", "title": "Success"}]'
)
```

---

## Dashboard grid layout

Charts are positioned on a grid. Use `width` and `height` fields (NOT `w`/`h`) — using
`w`/`h` causes a validation error. Standard sizes: `width: 6, height: 4`.

---

## Funnel chart — node ordering matters

For funnel charts, add metrics in the **same order as the process flow**:

```json
[
  {"conv_id": 123, "node_id": "start-node-id", "title": "Started"},
  {"conv_id": 123, "node_id": "mid-node-id",   "title": "Processed"},
  {"conv_id": 123, "node_id": "final-node-id", "title": "Completed"}
]
```

The funnel visualizes drop-off from each step to the next.

---

## Best practices

- Use **End nodes** as primary metric sources — they reliably accumulate completed tasks
- For error rate charts: add both Final (success) and Error end nodes as metrics on one column chart
- Name metrics descriptively — labels appear directly on charts
- Create separate dashboards: one for real-time ops monitoring, one for historical reporting
- Group all metrics from one business flow (e.g., payment processing) on a single dashboard
- Always verify `series` after chart creation — empty series means the chart won't render
- Always call the `set-dashboard-layout` action after all charts are ready — charts are invisible without it
- For drill-down: create the high-level dashboard first, then the detail dashboard, then link charts

---

## Dashboard operations reference

| Goal | Tool / operation |
|------|-----------------|
| Create dashboard | `create-dashboard` → returns `obj_id` (dashboard_id) |
| View dashboard + charts list | `get-dashboard(dashboard_id=...)` |
| Add chart | `add-chart` → returns `obj_id` (hex chart ID) |
| Get chart details + series | `get-chart(chart_id=<hex>, dashboard_id=...)` |
| Modify chart | `modify-chart(chart_id=<hex>, dashboard_id=..., ...)` |
| **Make charts visible** | `set-dashboard-layout(dashboard_id=..., grid='[...]')` |
| Get node IDs for series | `pull-process` → read `scheme.nodes[].id` |

**ID types to remember:**
- `dashboard_id` — integer (e.g. `136542`)
- `chart_id` — hex string (e.g. `"6a043a89e552e86e908941aa"`)
- `node_id` in series — 24-char hex string from `scheme.nodes[].id`
