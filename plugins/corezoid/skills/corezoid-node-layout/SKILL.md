---
name: corezoid-node-layout
description: >
  Auto-arrange the nodes of a Corezoid process into a clean, readable layout —
  a vertical top-to-bottom business flow with error handling railed off to the
  right and no overlapping nodes. Use it AUTOMATICALLY on every process YOU
  built or assembled, as the last step before push-process — never hand-place
  coordinates by eye (eyeballed grids overlap the moment nodes are taller than
  the step). Also reach for it whenever the user says a process is unreadable,
  tangled, ugly, a mess, or that nodes sit on top of each other, or asks to
  "arrange", "lay out", "tidy up", "make readable", "fix positions", "clean
  the diagram", "remove the overlaps" — or their equivalents in any language
  the user speaks. Do NOT silently
  re-arrange a process the user already positioned — see the "When you MAY
  re-layout" rules in the body. It changes x/y coordinates only — never
  collapse/expand state, edges, logic, conv_id, aliases or node types.
---

# Auto-Layout Corezoid Process Nodes

You make a process **readable**: business logic as a clear vertical spine,
error handling collected in a tidy right-hand rail, nothing overlapping. This is
the mechanical companion to the positioning rules in
`${CLAUDE_PLUGIN_ROOT}/docs/process/node-positioning-best-practices.md` — that
doc is the source of truth for *why*; this skill *does it* deterministically.

## When you MAY re-layout — and when you MUST NOT

A process's layout is part of how its author reads it. Re-flowing someone's
diagram without asking is destructive even though the logic is untouched — it
throws away a mental map they are used to. So the rule is about **authorship**,
not just readability:

- **A process YOU built this session, from scratch → always lay it out.** You
  own its positions; arrange it cleanly before `push-process`. No need to ask.

- **An existing / pulled / user-authored process → do NOT re-layout by
  default.** Preserve the author's `x`/`y`. This holds even if you just edited
  it — a user who is used to their arrangement must not find it rearranged.

- **When you add nodes to someone else's process** (an edit, not a rebuild):
  leave the new nodes at `x: 0, y: 0` — `push-process` auto-places them next
  to their graph neighbours while keeping every existing node where it was
  (preserve mode; see node-positioning-best-practices.md § Automatic
  Placement on Push). Do **not** run the whole-process auto-layout — that
  repositions everything. Full re-layout on a foreign process happens only on
  request.

- **The user explicitly asks** ("tidy this up", "fix the positions", "make it
  readable" — in any language) → re-layout the whole process. This is the one
  case where rearranging a foreign process is wanted.

- **The process is genuinely unreadable** (heavy node overlaps, a tangled mess)
  → don't silently fix it. **Offer**: briefly say it's hard to read and ask if
  they want it re-arranged. Re-layout only after a clear yes. If they decline,
  leave it exactly as is.

When in doubt, ask before touching an existing layout — a wrong guess costs the
user their familiarity; asking costs one sentence.

## Workflow (once the rules above say you may)

Run the layout **after** the process JSON is finalized and **before**
`push-process`:

1. finish building the process (nodes + edges correct),
2. call the **`layout-process`** MCP tool on the `.conv.json`,
3. `lint-process`, then `push-process`.

The tool rewrites the file in place, touching only `x`/`y`. The user's existing
`extra.modeForm` state is immutable: expanded nodes stay expanded and collapsed
nodes stay collapsed. `extra`, edges, logic, `conv_id`, aliases and node types
are left intact.

## How to run

Call the MCP tool (no auth needed — it works entirely on the local file):

```
layout-process(process_path="path/to/NNN_name.conv.json")
```

Preview without writing (the result lists the planned coordinates):

```
layout-process(process_path="path/to/NNN_name.conv.json", dry=true)
```

Control the spacing with `density="compact"|"medium"|"roomy"` (default
`medium`). The density pass re-spaces rows and columns from the nodes' REAL
sizes — a row of collapsed 48px IF-squares stops reserving a full block-row
of air, so a process fits on screen without zooming out. `roomy` skips the
pass and keeps the coarse block-sized rhythm (useful for presentations).

The same tool is available from a shell via the server's CLI mode:

```bash
sh "${CLAUDE_PLUGIN_ROOT}/mcp-server/run.sh" layout-process process_path=<file> dry=true
```

The engine lives inside the plugin's Go MCP server — no extra runtime, no
network, no server call.

## What it does (the layout rules it enforces)

The engine picks a strategy automatically (the result reports which one and why):

- **Small and tree-like processes** (fewer than ~25 nodes, or one main flow
  with branches) → a vertical "waterfall": the happy path runs straight down
  the central column, branches fan out to the sides (longer branches nearer
  the centre — a hub with rays reads as a star, cascaded IFs as a tree), and
  each error cluster is pinned tight to the right of the node it protects.
  Small processes always get the waterfall: the layered machinery below only
  pays off at scale, and on a small graph its spine drifts sideways.

- **Region composition** — big processes routinely combine shapes, so region
  detection runs in a loop and each region gets its own geometry while the
  rest of the graph keeps the waterfall:
  - **TABLE**: a dispatcher fanning into 3+ structurally identical pipelines
    that reconverge (one sync pipeline per entity type is the canonical case)
    → parallel columns with row-aligned steps, and the shared tail (a DLQ,
    the columns' error clusters) in a side column;
  - **STAR / sun**: a hub fanning into 4+ chain-shaped rays of *varying*
    depth that reconverge → rays hang symmetrically around the hub→merge
    axis, deepest nearest the axis (a fir-tree silhouette);
  - **DIAMOND**: a compact two-way fork/rejoin → the primary branch stays on
    the hub axis, the short optional branch sits immediately to its right,
    and the merge returns to the original axis;
  - several regions in sequence — two tables, a star followed by a table —
    compose cleanly (each expansion makes its own room).

- **Large mesh processes** (many independent flows, lots of error handling)
  → the graph is split into (1) the **business flow**, laid out as a clean
  layered top-to-bottom spine with edge-crossings minimised, (2) **dedicated
  error clusters**, pinned locally beside their single owner, (3) genuinely
  **shared error clusters**, placed on a clean right rail using their existing
  collapse/expand state, and (4)
  unreachable historical components, packed into a separate zone below the
  active Start flow. A high error-node percentage alone does not select this
  strategy; ownership topology does.

- **Recovery lanes** — an `err_node_id` target is not automatically terminal
  error handling. If it enters a substantial compensating pipeline or rejoins
  the active flow, that pipeline is anchored beside its owner/merge as business
  logic while retaining its current mode. Callback entrypoints are treated like Start
  roots; a component goes to the archive zone only when it has no attachment
  to the active graph in either direction.

Both strategies guarantee: **no node overlaps**, top-to-bottom flow with
Start at the top and the deepest success Final sunk to the bottom of the active
flow (early success exits remain local), every node's existing collapse/expand
state preserved, and the diagram re-centred on the origin so it sits in the
middle of the canvas instead of drifting off one edge.

Note what that last point is *not*: there is no hard ±10000 clamp. The vertical
step does shrink for deep processes, but it has a floor, so a very deep process
(roughly 125+ layers) legitimately extends past ±10000. That is fine — the node
schema allows ±100000 precisely because real processes reach about ±25500.

**Example** — a typical result on a freshly built process:

```
strategy: waterfall  (21 nodes, 4 flow(s), 5% error-handling nodes)  density=medium  engine=recovery-v6-preserve-modes
nodes=21 width=1000px height=4070px overlaps=0 collapsed=0
readability: estimated-crossings=0 max-dedicated-error-span=376px long-dedicated-errors=0
edges: max-span=420px p95-span=310px long=0 upward=0
layout applied: 042_payment.conv.json (21 nodes, 21 moved)
Next: lint-process, then push-process.
```

`overlaps=0` is the number to check; anything else means a bug worth reporting.
Full re-layout is idempotent: running it again over its own output must preserve
the same coordinates. Every input `extra.modeForm` must be identical after the
run; `collapsed=0` and `mode-passes=1` are mandatory. The engine revision makes
stale runtime tests visible.

## Honest limits

Some graphs cannot be made beautiful because the *graph itself* is tangled — a
node called from a dozen places is an unavoidable fan of edges no layout can
remove. When a process is this big and knotty, the real fix is to **split it
into smaller, repeatable sub-processes**; the layout only makes an unavoidable
monster as readable as it can be, never magically simple.

## Verifying a change to the engine

The engine's test suite lives with the server code
(`plugins/corezoid/mcp-server/layout_*_test.go` — synthetic topologies:
chains, stars, loops, fractals, tables, and an error-heavy process):

```bash
cd "${CLAUDE_PLUGIN_ROOT}/mcp-server" && go test -run 'TestLayout' ./...
```

It asserts the invariants that make a layout clean: all nodes placed, **zero
overlaps**, deterministic output (so adding a node re-flows rather than piles
up), coordinates within canvas, top-to-bottom flow, correct strategy routing,
table/star/diamond region geometry, dedicated-vs-shared error ownership,
detached component zoning, local early exits, and golden coordinate files that
freeze every fixture against unintended churn.
