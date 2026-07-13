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
  re-layout" rules in the body. It only changes x/y coordinates and collapses
  IF/Delay/error nodes — never edges, logic, conv_id, aliases or node types.
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

The tool rewrites the file in place, touching only `x`/`y` and the `extra`
`modeForm` flag (collapsing pure IF / Delay / error nodes into small nodes).
Edges, logic, `conv_id`, aliases and node types are left byte-for-byte intact,
so the change is behaviour-safe (it only affects appearance).

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
  - several regions in sequence — two tables, a star followed by a table —
    compose cleanly (each expansion makes its own room).

- **Large mesh processes** (many independent flows, lots of error handling)
  → the graph is split into (1) the **business flow**, laid out as a clean
  layered top-to-bottom spine with edge-crossings minimised, (2) **error
  clusters** (escalation → reply → error-final, reachable only via
  `err_node_id`), collapsed and collected in a clean **right rail** aligned to
  each source row, and (3) unreachable **orphans**, packed into a small grid at
  the bottom.

Both strategies guarantee: **no node overlaps**, top-to-bottom flow with
Start at the top and success Finals sunk to the bottom of the diagram (even
when a loop exits mid-flow), IF/Delay/error nodes collapsed for compactness,
and every coordinate inside Corezoid's ±10000 canvas (the vertical step
shrinks automatically for very deep processes).

**Example** — a typical result on a freshly built process:

```
strategy: waterfall  (21 nodes, 4 flow(s), 5% error-handling nodes)  density=medium
nodes=21 width=1000px height=4070px overlaps=0 collapsed=3
layout applied: 042_payment.conv.json (21 nodes, 21 moved)
Next: lint-process, then push-process.
```

`overlaps=0` is the number to check; anything else means a bug worth reporting.

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
table/star region geometry, and golden coordinate files that freeze every
fixture against unintended churn.
