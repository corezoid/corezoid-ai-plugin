# Tidy-tree layout — full rules

Full text of every rule implemented by
`${CLAUDE_PLUGIN_ROOT}/skills/corezoid-node-layout/scripts/tidy_tree_layout.py`
(the opt-in alternative to the `layout-process` MCP tool). `SKILL.md` keeps only
the summary; read this file when the user disputes a placement, when a
`WARNING (rule N)` needs explaining, or when changing the script. Rule numbers
match the numbers in the script's comments and `WARNING`/`CHECK` messages.

The constants were measured from hand-arranged production router processes
(dispatcher-shaped backends with 90–200 nodes) and normalized to one value each;
the "Derivation" section at the end lists the raw ranges.

## Structure first: split by branching source, not by value

Decide this **before** running the script — it changes the graph itself, not
just its coordinates, and no layout can fix it afterwards.

**The test: what varies between the branches — the *source* being tested, or
just the *value* of the same source?**

- **Different source → a chain of single-condition routers.** Each branch tests
  a different field/button/event (`body.action == 'create'` vs a different
  button id vs an unrelated entity) and the branches don't reconverge into
  shared downstream logic. One `go_if_const` per router node; the node's
  default `go` leads to the *next* router — never to a step shared by several
  branches. The last router's default `go` is the "nothing matched" exit.
- **Same source, different values → keep one condition node.** The branches
  are mutually exclusive outcomes of one question and typically reconverge
  below (`Found?` yes/no, `Success?` / anything else). Splitting it into a
  chain would add hops without separating anything real.

**Why:** a chain of single-condition routers is what lets each branch get its
own exclusive lane with no shared state. One node with N unrelated
`go_if_const` targets couples N independent features: editing branch 3's
condition risks touching the node that also drives branches 1, 2, 4, 5.

**Per-branch error handling stays local.** Every `api_rpc`/`api_code` node gets
its own error Reply + End pair next to it — never shared with another branch or
another call. For an existing process that already shares them,
`--split-errors` (rule 11) does the split.

The router-vs-business-condition distinction cannot be recovered from topology
alone (both have the same JSON shape — see "Known gaps"); the script treats a
default-`go` edge as dispatcher spine only when *both* ends are router-shaped.

## The rules

1. **One trunk, in either axis.** Most processes get a vertical trunk. A
   **dispatcher** (Start's primary chain begins with 2+ router-shaped nodes —
   exactly one default `go` plus one or more `go_if_const` — linked by default
   `go`) gets a **horizontal trunk**: the router chain *is* the trunk and each
   router's matched branch hangs below it. Once a node's axis is set, its
   primary (largest-subtree) child continues on the same axis.
2. **Every branch gets its own exclusive lane — a DOWN column.** The lane's
   first node is **top-aligned with the node it branches from** and sits to its
   right. The lane's x is found by *packing*: the smallest offset at which none
   of the lane's nodes (including its stubs and return chains) come closer than
   `GAP` = 64px to anything already placed. The trunk counts as occupied over
   its whole height, so a lane never draws across the trunk's edges. Off a
   horizontal spine the matched branches hang **below** each router.
3. **No stagger without a real fork.** A node with exactly one outgoing edge
   (plain `go` or an `err_node_id` escalation) continues at offset 0 on the
   perpendicular axis — a perfect right angle, no diagonal drift.
4. **Only a condition with 2+ branches staggers.** The largest-subtree branch is
   primary and continues the column; every other non-stub branch becomes its
   own lane (rule 2). A condition counts as a real fork even when its default
   exit points at a node owned elsewhere — the matched branch still steps
   aside (a "staircase").
   - Tie on subtree size → the plain `go` is primary over `go_if_const`. For a
     router this is why "keep scanning" (default `go`) is the trunk and
     "matched, handle it" branches off.
   - `err_node_id` targets are never primary.
5. **Pivot correction.** Corezoid stores Start/End coordinates as their
   **center** and every other node as its **top-left corner**. The script
   computes centers uniformly and converts at the end by subtracting half of
   each node's own width (100px expanded, 28px collapsed), so an End
   center-aligns with whatever node precedes it.
6. **modeForm by role, never by `obj_type`.** `obj_type: 3` only means "some
   `err_node_id` points here" (the server sets it), so it says nothing about
   importance. **Collapsed:**
   - Start / End circles; error stubs (rule 9);
   - **result checks** — a pure condition (only `go`/`go_if_const`), not on the
     dispatcher spine, with a branch into an **error** Reply (Reply → error End).
     A check whose branches lead only into success Replies stays expanded;
   - **every Set Parameters node** (only `set_param` + `go`) unless it forks into
     a continuation plus an error exit to a *different* node (`err_node_id` ==
     `go` target is not a fork);
   - exception: a Set Parameters titled `init` is never collapsed;
   - bare `go`-only relays reached only by error edges (`… failed - continue`).

   **Expanded:** everything else — routers, `api_rpc`/`api_code`/`api` calls,
   business conditions, `init`, and the **success Reply**.
7. **Shared business nodes stay shared.** A plain node referenced from two
   branches is owned by the first branch that reaches it; the other reference
   draws a longer line. Duplication is only for dead-end error clusters
   (rule 11).
8. **Canvas is ±10000 per axis.** The drawing is centered on (0,0). If it does
   not fit (`WARNING: canvas span exceeds …`), **do not shrink the constants** —
   decompose the process into sub-processes (Call a Process boundaries).
9. **Error stubs use a corner hook.** An *error stub* is a Reply node
   (`api_rpc_reply`) whose only exit is a `go` into an error End (`obj_type: 2`,
   icon not `success`). Whenever a main node leads into one — by `err_node_id`,
   a condition branch, or as its only child — it is not laid out as a lane:

   | What | Stored coords |
   |---|---|
   | Reply | `x = main.x + main_width + 56` (+256 from an expanded main), `y = main.y` — top edges aligned |
   | End: Error | `x = reply.x + 28`, `y = reply.y + 88` — straight below the reply |

   The Reply is always collapsed. A Reply whose `go` leads anywhere else (a
   success End, back into the flow) is not a stub. Off a RIGHT-running lane
   whose right side is taken, the reply goes **below** the main node instead.
   Constants: `STUB_GAP_X`, `STUB_FINAL_DY`.
   - **9b. Collapsed main node → inline.** A corner hook off a 56px node would
     land on the next trunk node, so the stub runs on the same row: reply
     `x = main.x + 72`, End `x = reply.x + 140`, `y = reply.y + 28`.
     Constants: `STUB_INLINE_GAP`, `STUB_INLINE_FINAL_DX`.
10. **Column step is content-based.** The next node starts `GAP` = 64px below
    the **bottom** of the previous one. Heights use the plugin's measured formula
    (`docs/process/node-positioning-best-practices.md`): expanded = 98, +27 for
    an Error row, +27 per `go_if_const` row, +16 per extra wrapped title line
    (~24 chars/line, an estimate); collapsed = 56. A row with side branches
    takes at least their band; with 2+ hooks off one node the next node starts
    `GAP` below the last hook's End. If the UI renders a node taller than the
    estimate, fix the formula, not `GAP`.
11. **`--split-errors`: every source gets its own dead-end error cluster.**
    Opt-in; runs before layout and changes the graph.
    - **What is split:** a node reached from 2+ different sources (`err_node_id`,
      `go`, `go_if_const` and time-semaphor edges all count) whose closure is a
      dead-end error cluster: only `api_rpc_reply`/`go`/`go_if_const`, no
      semaphors, no nested `err_node_id`, every exit an End whose icon is not
      `success`. Anything that leads back into the flow is not split (rule 7).
    - **Who keeps the original:** the source whose title the cluster's reply
      already names (text after `—` in `Reply error — build payload`), else the
      first source found from Start. Every other source gets a copy.
    - **Copies:** logic 1:1; a reply copy is titled `Reply error — <source
      title>`; new 24-hex ids. Repeats to a fixpoint; one `split:` line per copy.
12. **The merge point stays on the trunk; optional work goes into a lane.** Tree
    ownership is by shortest path from Start (BFS): a node belongs to its
    shallowest predecessor. So when a condition either runs an optional chain
    or skips straight to M, M stays on the trunk and the chain becomes a lane.
    M is pushed down to the row of the lowest lane node that leads into it, so
    the return edge is a straight horizontal line.
13. **Loop body = its own column; the trunk continues after the whole body.**
    The loop head (`Next item?`) stays on the trunk, its matched branch is a
    lane, and the trunk's next node goes on the row of the body's lowest node.
    Packing is deterministic, so all loop bodies of a process line up.
14. **A return chain goes LEFT on the row it leaves from.** A short tree chain
    (≤3 nodes, `RETURN_CHAIN_MAX`) whose last node jumps back to the loop head
    (an ancestor) is laid out right-to-left: first node's right edge = lane
    left − 64, each next one 64px further left.
15. **A bypass sits between two rows.** A collapsed skip handler whose only exit
    is the next trunk node (`Call X` --err--> `X failed — skip` → `Call Y`) sits
    *between* the rows: `top = prev.bottom + 64`, right edge flush with the
    column (`BYPASS_DX` = 144); the next row starts 64px below it.
16. **A stub goes LEFT when the right side is taken** by a lane on the same row.
    Mirror of 9b for a collapsed node; for an expanded node the corner hook
    mirrors to `reply.x = node.x − 112`.
17. **2+ stubs off one collapsed check are stacked outside the column:**
    `reply.x = column.x + 256`, `reply.y = node.y + i·92` (`STUB_STACK_DY`),
    Ends inline (+140/+28).
18. **Success Reply → End straight below**, center-aligned: End center =
    `(reply.x + 100, reply.top + height + 64 + 28)`.
19. **Incremental mode — new nodes in an existing process.** With
    `--only-new <before.conv.json>` (new = ids absent from the pre-edit copy) or
    `--nodes=id1,id2` the script:
    - keeps every existing node's `x`/`y` **and modeForm** (the tidy geometry is
      computed with the existing nodes' actual sizes);
    - sets modeForm of new nodes by role (rule 6);
    - places each new node at the same offset from its nearest **existing**
      tree ancestor (the anchor) as the full layout would — centering, hooks,
      lanes all follow rules 1–18;
    - if existing nodes hang below the insertion, pushes them (and everything
      past them on that axis) down — or right on a dispatcher spine — by just
      enough to restore the gap; never pulls anything closer (`shift:` line);
      the anchor's own next node is pushed too when a new node lands between them;
    - a new side lane is pulled back left to the first free column (`compact:`);
    - prints `new:` per placed node and `WARNING` on overlaps with existing nodes.

    `--nodes=` combines with `--only-new` (union): list existing nodes whose
    edges or role changed in this edit (e.g. a Set Parameters rewired by rule
    21) so they are re-placed and get role modeForm too. Verified by removing
    nodes from a laid-out process and re-inserting them: the result matched the
    full layout exactly.
20. **Success step + fallback = one row of alternatives.** When a node's success
    child A and its error fallback B are both collapsed, have no children of
    their own and both lead only into the same node M, they sit side by side:
    A left, B right, `PAIR_GAP` = 16px, the pair centered on the column; M
    starts `GAP` below the row and belongs to A (the success path), even when
    BFS met it via B first. Also covers two condition branches that rejoin at
    one node. A lone fallback without a success sibling is a rule-15 bypass.
21. **A string-only Set Parameters never owns an error path.** Its
    `err_node_id` goes exactly where its `go` goes ("ignore and continue") — no
    `Reply error` + End cluster of its own, no error exit that jumps further
    down the flow (that skips steps — a real bug).
    - **Scope:** only Set Parameters whose keys are all `string` (`extra_type`).
      A string key never fails (missing source → `""`), so its error path is dead
      weight. A key of type `object`/`array`/`number` fails on a missing or
      malformed source — there the error exit is a real check and may keep its
      own cluster; the script skips such nodes (and rule 6 treats them as forks).
    - The script prints `WARNING (rule 21)` per violating node;
      `--fix-setparam-errors` rewires the error exit to the `go` target and
      deletes cluster nodes nobody references any more (`fix:` / `removed
      orphan` lines — a structural change, so on an existing process only as
      part of an edit the user asked for).
    - `CHECK (rule 21)`: a fallback that rejoins *further down* than its success
      step does. Sometimes intended (the skipped nodes need the step's data), so
      it is a question, not an error.
    - When writing nodes by hand, apply this directly. The `go` target then also
      receives an error edge, so it must be `obj_type: 3` (the fix flag sets it).
22. **A horizontal row is packed, and the router chain always stays the row.**
    On a dispatcher spine the default `go` to the next router is primary even
    when a matched branch is bigger. Each row node is placed at the first x
    where it *and everything hanging below it* clear what is already placed by
    `GAP` — a contour check, not a bounding box, so a deep branch far below does
    not push the next router away.
23. **A row turns below its predecessor, and a run of business checks is a row
    too.**
    - Start sits above the first router, centered; the spine runs right from it.
    - **Check row:** 3+ *gates* on the primary chain with at most one other node
      between two of them. A gate is an expanded pure condition whose other
      branches either end inside their own subtree (reject / block + reply) or
      are a fix-up of ≤2 real nodes that rejoins. A check that opens a bigger
      optional block is not a gate (rule 12 applies).
    - The row starts at the first gate's predecessor (the corner), directly
      below the column; after the last gate its primary child starts a column to
      the right, top-aligned.
    - A bypass of a row node sits between it and the next node, `GAP` right and
      `BYPASS_ROW_DY` = 80 below the row top.
    - The script prints `row: …` for every check row it finds.
24. **The merge node of a step and its fallback belongs to the success step** —
    rule 20's re-own also applies when the step forks, as long as the merge
    node is the step's `go`.
25. **A hook flips left when a lane node merges into its node on the same row**
    (the hook would sit on that horizontal merge edge). A lane that merely
    passes by does not flip anything.
26. **A collapsed below-leaf of a row node moves down to the row of the node it
    merges into**, if that row is free (straight edge).
27. **Every End has an icon.** `error` when the title contains an error-like
    word, otherwise `success`; never empty (an empty icon renders like a success
    End). The word list is `error`, `fail`, `fehler`, `erreur`, `errore`,
    `erro`, plus the Russian and Ukrainian stems for "error" (written as `\u`
    escapes in the source); extend `_ERROR_END` for other languages. The script
    prints `WARNING (rule 27)`; `--fix-final-icons` sets the icon (structure and
    coordinates untouched).

`--vertical` turns the horizontal rows off (rules 1, 22, 23): one vertical
trunk even for a dispatcher. Use it only when the user asks for it.

## Derivation (condensed)

Hand-arranged layouts were compared with the script's output edge by edge
(offset of each node from its tree parent — absolute coordinates are useless,
one moved node shifts everything after it). Structure (side, row, axis, merge
ownership, modeForm) was adopted; spacing noise was normalized:

| What | Hand-placed values | Normalized |
|---|---|---|
| vertical gap in a column (bottom → top) | mostly 63–67 (outliers 27–79) | `GAP` 64 |
| side lane on the same row | 64–88 | `GAP` 64 |
| router → next router on a spine | 290–434 | packed, `GAP` 64 (was a fixed 700) |
| loop body's first node vs loop head | 156–204px above | top-aligned |
| inline stub End | dx 132–160 | 140 |
| bypass node x | column +126…+138 | +144 (flush right) |
| trunk step across a bypass | 183–193 | 64 + 56 + 64 = 184 |
| stacked stubs off one check | x +256, dy 92 | +256 / 92 |
| success Reply → End | dy 168–220, dx 100 | dy 190, dx 100 |
| error Reply → error End | dx 28, dy 88 | pivot correction, half of 56 |
| bypass on a horizontal row | +64 right, +79 below | `BYPASS_ROW_DY` 80 |
| success + fallback pair | 36 | `PAIR_GAP` 16 (same as 9b) |

The role-based modeForm rule (6) reproduced every expand/collapse choice of a
139-node hand layout; the earlier `obj_type`-based rule missed 31. On a 93-edge
dispatcher re-laid out by hand, 84 edges came within 40px of the hand layout;
the rest was the author's inconsistent wider row spacing.

**Noise to ignore when diffing a hand edit against a push:** the server
recomputes `obj_type` 0↔3 from incoming error edges, the UI shifts saved nodes
by ±1px, and the code editor re-indents `api_code` sources.

## Known gaps

- **Router vs business condition is told apart by shape only.** An ordinary
  2-way business condition has the same JSON shape as a dispatcher router; the
  spine is assumed only when both ends of a default-`go` edge are router-shaped.
  The "different source vs same value" distinction is a design-time decision
  ("Structure first"), not something the script can infer.
- **The check-row gate criterion** (rule 23) was derived from one block of
  checks; revisit it on the next process with a long run of checks.
- Not tested on a condition with 3+ branches that test *different sources* —
  the router-chain structure should prevent that shape in new processes, but
  the script does not enforce it.
- Non-tree (shared) edges are owned by whichever branch reaches them first,
  which depends on JSON order; on processes with many shared nodes a few lines
  may be longer than necessary.
