# Node Positioning Best Practices

## Overview

Starting from plugin v2, the MCP server runs the archetype-aware `applyLayout` engine
(`plugins/corezoid/mcp-server/layout.go`) automatically on every `push-process`.

**Safe by default — preserve mode.** The engine only places *new* nodes: nodes whose `x` and `y`
are both `0`. Already-positioned nodes are never moved. This means a model (or human) can set
precise coordinates for any node and those positions will survive every subsequent push untouched.
A process built from scratch has all nodes at `0/0`, so it still receives a full clean layout on
the first push — there is nothing to preserve.

The engine snaps to the grid, guarantees zero overlaps (validated on 596 real processes), and
keeps connectors straight. This document describes the conventions it applies so that humans and
the model understand the resulting layout and can control it when needed.

## Node Dimensions

Corezoid nodes have specific dimensions that the layout engine accounts for when positioning them:

1. **Start and End Nodes**

   - Shape: Circle
   - Dimensions: 56px × 56px
   - Radius: 28px
   - Pivot Point: Center of the node

2. **Standard Nodes (without escalation or error links)**

   - Width: 200px
   - Minimum Height: 150px
   - Actual height varies based on node content
   - Pivot Point: Top-left corner

3. **Nodes with a Timer (Semaphor / Delay)**

   - Width: 200px
   - Height: approximately **2× the standard height** of the same node type
   - A timer semaphor adds a visible timer block below the node body, roughly doubling the rendered height
   - The engine uses a fixed vStep that already accommodates a single-semaphor node; if a node has
     multiple timers consider opting out and positioning manually
   - Pivot Point: Top-left corner

4. **Nodes with Escalation or Error Links**

   - Width: 200px
   - Minimum Height: 125px
   - Pivot Point: Top-left corner

5. **Condition Nodes**
   - With single rule:
     - Width: 200px
     - Minimum Height: 110px
   - With AND operator:
     - Width: 200px
     - Minimum Height: 140px
   - With 2 OR rules:
     - Width: 200px
     - Minimum Height: 160px
   - Pivot Point: Top-left corner

## Pivot Points and Their Impact on Positioning

Understanding the pivot point location for different node types is crucial for proper node alignment:

1. **Pivot Point Definition**

   - The pivot point is the reference point used for node positioning
   - The X,Y coordinates of a node refer to its pivot point position
   - Different node types have different pivot point locations

2. **Start/End Nodes (Circular Nodes)**

   - Pivot point is at the center of the circle
   - When positioning Start/End nodes in line with other nodes, this center-based pivot must be
     considered

3. **All Other Node Types**

   - Pivot point is at the top-left corner
   - When aligning these nodes with Start/End nodes, proper offsets must be applied

4. **Alignment Adjustment for Start/End Nodes (spine column only)**
   - The engine shifts Start/End nodes by **+100px on X** when they sit in spine column 0, centering
     their 56px circle over the 200px-wide column
   - Example: spine nodes are at X=600; a Start/End in the spine is placed at X=700

## Auto-Layout Engine

### Coordinate Model

All coordinates snap to a **20px grid**. The spine column starts at X=600 (a constant named
`spineX` in `layout.go`). Additional columns step right by a fixed **240px pitch** (the `lanePitch`
parameter), so column 0 → X=600, column 1 → X=840, column 2 → X=1080, and so on.

Vertical position is determined by a node's **rank** (row number), multiplied by the adaptive
`vStep` (see Spacing below), starting from Y=0.

### Flow Direction

The happy path reads **TOP → BOTTOM**. Each node's rank (row) equals its longest weighted path from
a Start node, where:

- The **down edge** (the chosen vertical continuation of a node) costs **+1 row**.
- All other out-edges — branch conditions, `err_node_id` targets, semaphor timeout targets — cost
  **+0 rows**, so the branch target lands on the same row as its source.

The down edge is selected in priority order:
1. A `go` / primary logic edge, if any.
2. Otherwise the first `go_if_const` / condition edge.
3. Otherwise the first edge of any kind.

### Column Assignment (Spine and Branches)

- **Main flow (spine):** nodes that inherit a down-edge from their parent keep their parent's column
  (column 0 for the root chain), stepping straight downward.
- **Branch targets:** a node that is reached via a branch/error/timeout edge — not the chosen down
  edge of its source — is placed in the **first free column strictly to the right** of its source's
  column, on the **same row** as that source. It then runs its own vertical sub-chain downward in
  that column.
- **Column reuse:** column slots are assigned independently per row, so the total canvas width equals
  the width of the busiest single row, not the sum of all branches.

### Start/End Centering

A Start or End node in spine column 0 is shifted **+100px on X** to center its 56px circle over the
200px-wide column. Nodes in non-zero columns are not shifted (the engine only adjusts pivot
alignment for the spine).

### Spacing (Adaptive)

| Parameter | Value | Notes |
|-----------|-------|-------|
| Horizontal column pitch (`lanePitch`) | **240px** | Constant; keeps wide processes compact |
| Vertical step floor (`vStep` min) | **180px** | Logic node 150px tall + circle-intrusion margin |
| Vertical step cap (`vStep` max) | **240px** | `180 + 60` |
| Grid | **20px** | All coordinates rounded to nearest 20px |

The vertical step adapts to process size:

```
vStep = min(180 + 60, 180 + 20 * floor(max(0, nodes − 8) / 12))
```

- For processes with 8 or fewer nodes: `vStep = 180px` (minimum, tightest layout).
- For every 12 nodes above 8, vStep grows by 20px until it reaches the 240px cap.
- The formula guarantees no vertical overlap: 180px ≥ 150px logic height + 28px circle radius margin.
- The 240px horizontal pitch guarantees no horizontal overlap: 240px ≥ 200px logic width + 28px
  circle-intrusion margin from an adjacent Start/End.

### Archetype Detection

The engine detects one of five archetypes from the logic types present in the process. Currently,
all archetypes share the same spacing profile (the minima above). The detection exists so that
per-archetype tuning can be added in the future without changing the routing logic.

| Archetype | Detection rule |
|-----------|---------------|
| `state` | Process `conv_type` is `"state"` |
| `receiver` | Has a node with logic type `api_callback` |
| `api` | Has a node with logic type `api_rpc_reply` |
| `business` | Has a node with logic type `api_rpc` |
| `integration` | Has a node with logic type `api` |
| `default` | None of the above |

Rules are evaluated in the order shown; the first match wins.

### Determinism

The layout is a **pure function** of the graph topology and node count. Re-pushing the same process
produces byte-for-byte identical coordinates. Nodes are iterated in insertion order and per-row
lists are sorted by node ID, so the output is stable even if the JSON key order changes.

## Before / After Example

A minimal process with a Start, a Validate step, an Error branch, and a Reply+End, laid out by the
engine (8 nodes or fewer → `vStep = 180`, `lanePitch = 240`, spine at X=600):

```
Row 0  (Y=0):    Start         X=700, Y=0      ← +100 for center pivot
Row 1  (Y=180):  Validate      X=600, Y=180    ← spine col 0
                 Error         X=840, Y=180    ← branch: same row, col 1
Row 2  (Y=360):  Reply         X=600, Y=360    ← spine continues down
                 Error-End     X=840, Y=360    ← error sub-chain continues down col 1
Row 3  (Y=540):  End           X=700, Y=540    ← +100 for center pivot
```

Connector shape: **Validate → Reply** is a straight vertical line. **Validate → Error** is a
straight horizontal line. Each branch sub-chain then runs vertically in its own column — no diagonal
connectors anywhere.

ASCII sketch (column headers = canvas X values):

```
        600        840
         |          |
Y=0    [Start@700]
         |
Y=180  [Validate]--[Error]
         |              |
Y=360  [Reply]    [Error-End]
         |
Y=540  [End@700]
```

## Controlling the Layout Mode

The engine only ever writes `x` and `y` on nodes. It never changes logics, semaphors, extra fields,
IDs, or edges.

Mode is controlled exclusively by the `COREZOID_AUTOLAYOUT` environment variable (case-insensitive,
trimmed). Set it before starting the MCP server. Three values are recognised:

| Value | Behaviour |
|-------|-----------|
| _(unset or anything else)_ | **`preserve`** (default) — only new (0/0) nodes are positioned; existing coordinates are kept |
| `off` | Layout is disabled entirely; `push-process` leaves all coordinates exactly as they are in the source file |
| `full` | All nodes are re-laid-out on every push, overwriting existing coordinates |

Examples:

```
# Disable layout entirely:
COREZOID_AUTOLAYOUT=off

# Force a full re-tidy on every push:
COREZOID_AUTOLAYOUT=full
```

> **Note:** there is no per-process flag. On the real Corezoid platform `web_settings` is always
> an array (`[[], []]`), never an object, so a property-based flag cannot be stored there.
> Use the env var or the `layout-process` tool instead.

### On-demand full re-layout: the `layout-process` tool

To apply a full archetype-aware re-layout to a single process without changing the global env var,
use the `layout-process` MCP tool:

```
layout-process  process_path: "<PROCESS_PATH>"
```

This overwrites every node's `x`/`y` with freshly computed positions (same engine as `full` mode)
and saves the file. Use it whenever you want to tidy an existing process that has drifted from the
clean spine layout — for example after many incremental edits. It does not push; run `push-process`
afterwards to deploy the re-tidied layout.

## Edge Connections and Routing

Corezoid renders edges as smooth Bezier curves. The auto-layout engine positions nodes so that:

- **Spine edges** (down the happy path) produce near-vertical straight connectors.
- **Branch/error/timeout edges** produce near-horizontal connectors because the target lands on the
  same row as its source, one column to the right.
- No diagonal connectors appear under the standard layout.

For edge routing best practices when opting out and positioning manually:

- Prefer connections from the bottom port of one node to the top port of the next for clean
  vertical flows.
- Use the right port for error and branch targets when placing them horizontally.
- Maintain the 240px column pitch and 180px row pitch as minimum spacings to stay within the
  overlap-safe zone.

## Related Documentation

- [Converting Algorithms to Effective Processes](algorithm-to-process-guide.md)
- [Execution Algorithm](execution-algorithm.md) - How processes are executed
