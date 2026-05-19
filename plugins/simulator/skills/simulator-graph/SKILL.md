---
name: simulator-graph
description: >
  Simulator.Company graph structure specialist. Use when the user wants to build
  business process graphs, flowcharts, algorithms, create actors (nodes), manage
  links (edges) between actors, work with layers (visual views), search actors
  on layers, move actors between layers, or explore graph connections.
  Covers the full actor lifecycle (create, update, delete, search), all graph
  traversal operations, and FlowchartBlock diagram creation.
  Activate also when the user says "add to layer", "connect actors", "build a
  process flow", "link nodes", "organize on layer", "create algorithm",
  "draw flowchart", "add block to graph", "flowchart", "FlowchartBlock",
  "startStop", "predefinedProcess", "create process on graph", "digital twin",
  "build process diagram".
---

# Simulator.Company Graph Builder

You are a specialist in building graph-based business process structures in
Simulator.Company using the `simulator` MCP server.


---

## Step 0a — Read sys-forms.yaml (MANDATORY FIRST ACTION)

Before doing **anything** else, read the system forms catalog from the current working directory (not plugin directory):

```
Read: sys-forms.yaml
```

This file is a list of system forms. From it, extract and remember:

| Variable | How to find it | Used for |
|---|---|---|
| `graphFormId` | root entry where `title == "Graphs"` → `id` | Creating Graph actors |
| `layerFormId` | root entry where `title == "Layers"` → `id` | Creating Layer actors |
| `defaultFormId` | root entry where `title == "Default"` → `id` | Creating plain-text labels on a graph |
| Block template ids | entry where `title == "FlowchartBlock"` → `childs[]` → match by `title` → `id` | Creating flowchart nodes |

**Block template lookup** (find `id` in `FlowchartBlock.childs` by matching `title`):

| User asks for | Title to match in `FlowchartBlock.childs` | Default color |
|---|---|---|
| "Start" or "Stop" node | `"Start / Stop"` | `#4caf50` (green) |
| "Process" node | `"Process"` | `#2196f3` (blue) |
| "Decision" node | `"Decision"` | `#ff9800` (orange) |
| "Predefined Process" | `"Predefined Process"` | `#00bcd4` (cyan) |
| "Document" | `"Document"` | `#ff5722` (deep orange) |
| "API Call" | `"API Call"` | `#9c27b0` (purple) |
| Corezoid Start node | `"Corezoid Start"` | `#4caf50` (green) |
| AWS Lambda icon | `"Lambda"` | `#ff9800` (orange) |
| Plain text / label on graph | use `defaultFormId` (root entry `title == "Default"`) | _(no color needed)_ |

> **Rule:** Use the **child** form's `id` as `formId`, never the parent `FlowchartBlock` id.
>
> **Color rule:** Always pass the `color` field (hex string) when creating a flowchart block.
> Use the default from the table above, or apply the user's requested color.

---

### Step 0b — Layer ID (`layerId`)

1. Check whether a `layerId` (graph/layer actor ID) is already known — from the user's current message, conversation history, or session context.
2. **If `layerId` IS known** → **do not create a new layer**. Remember this value and call `getLayer(layerId=<known>)` during discovery to read the current state (existing nodes, edges, `laId`s). Skip any "create layer" step.
3. **If `layerId` is NOT known** → determine intent:
    - User explicitly asked to **create a new graph/layer** → create two actors and link them:
        1. `createActor(formId=<graphFormId>, body='{"title":"<name>"}')` → save as `graphId`
        2. `createActor(formId=<layerFormId>, body='{"title":"<name>"}')` → save as `layerId`
        3. `createLink(body='{"source":"<graphId>","target":"<layerId>"}')` — bind the graph to its layer
    - Intent is unclear → ask:
      > "Which graph (layer) should we use? Provide a Layer ID or should I create a new one?"
4. Once `layerId` is resolved (either from context or just created), use it in all subsequent `manageLayer`, `getLayer`, `searchLayerActors`, and edge calls.

---

## Step 1 — Create Actor From a System Form (NO `data` field!)

> **CRITICAL:** When `formId` points to a form found in `sys-forms.yaml`, the
> MCP server **auto-injects** the `data` field (shape, view, blockId, etc.)
> from the form definition. **Do not pass `data` yourself.** Passing it will
> overwrite the server-side injection and likely produce an unrenderable block.

```
// formId = id looked up from FlowchartBlock.childs where title == "Start / Stop"
createActor(
  formId=<startStopFormId>,
  body='{
    "title": "Start",
    "color": "#4caf50"
  }')
# → returns { "id": "<actorId>", "title": "Start", ... }
```

Only pass `data` when creating actors from **custom (non-system) forms**, where
the user defines their own field set.

---

## Core Concepts & Glossary

| Term | Description                                                                                                                               |
|---|-------------------------------------------------------------------------------------------------------------------------------------------|
| **Actor** | Graph node. Created from a Form template. Has id, title, status, data fields.                                                             |
| **Form** | Actor template/type. Defines fields and behavior. Lives in `sys-forms.yaml` (system forms) or workspace-specific custom forms.            |
| **FlowchartBlock** | Parent system form (found by `title == "FlowchartBlock"`) that groups all flowchart/algorithm block templates as its `childs`.            |
| **Graph / Layer** | A layer is an actor with the Layer system form. Visual canvas where actors are placed at (x, y) coordinates.                              |
| **laId** | Layer Actor ID. Assigned by `manageLayer` when you place an actor on a layer. Required as `laIdSource` / `laIdTarget` when drawing edges. |

---

## Actor Operations

### Create Actor
```
createActor(
  formId=<formIdFromSysForms>,    // child form id looked up from sys-forms.yaml
  body='{
    "title": "Process Step 1",
    "description": "First step in onboarding",
    "color": "#2196f3"
  }')
# Returns: { "id": "<actorId>", "title": "...", ... }
```
Notes:
- For forms found in `sys-forms.yaml` → **do not pass `data`** (server fills it).
- For custom forms → pass `data` matching the form's field definitions.
- Always include `color` (hex string). See the title → color table in Step 0a.

### Get Actor
```
getActor(actorId="<actorId>")
getActorByObjId(accId="<ws>", objType=1, objId=12345)
```

### Update Actor
```
updateActor(formId=<formId>, actorId="<actorId>", body='{"title": "Updated"}')
```

### Delete Actor
```
deleteActor(actorId="<actorId>")
deleteBulk(body='{"actorIds": ["<a1>", "<a2>"]}')
```

### Create a Plain-Text Label on a Graph

When the user wants to place free text (a label, annotation, comment) directly on the canvas — not a flowchart block — use the **Default** form (`defaultFormId`) and put the visible text in the `description` field. Do **not** pass `data`.

```
// formId = id from sys-forms.yaml where title == "Default" (root-level entry)
createActor(
  formId=<defaultFormId>,
  body='{
    "title":       "",
    "description": "This is the label text shown on the graph"
  }')
# → returns { "id": "<actorId>", ... }

// Place on layer as usual
manageLayer(
  layerId="<layerActorId>",
  body='[{"action":"create","data":{"id":"<actorId>","type":"node","position":{"x":<X>,"y":<Y>}}}]')
```

> **Rule:** The `description` field carries the visible text. `title` can be left empty or omitted.
> Do not pass `data` — the server auto-injects the rendering view from the Default form definition.

---

## Link Operations



### Create Link

> **After creating a link, always place it on the layer too** — otherwise the
> arrow won't appear on the graph.

```
// Step 1: create the logical link
createLink(
  body='{
    "source": "<actorA>",
    "target": "<actorB>"
  }')
# Returns: { "data": { "id": "<edgeId>" } } — save this edgeId

// Step 2: draw the edge on the layer (MANDATORY for visual graphs)
manageLayer(
  layerId="<layerActorId>",
  body='[{"action":"create","data":{"id":"<edgeId>","type":"edge","laIdSource":<laId A>,"laIdTarget":<laId B>}}]')
```

### Create Multiple Links (efficient batch)

Pass `layerId` and the server will automatically place all new edges on the layer —
no separate `manageLayer` call required.

```
massLink(
  layerId="<layerActorId>",
  body='[
    {"source": "<a>", "target": "<b>"},
    {"source": "<b>", "target": "<c>"}
  ]')
# → creates logical links AND draws them on the layer in one call
```

### Check / Update / Delete Links
```
existLink(body='{"source": "<a>", "target": "<b>"}')
updateLink(edgeId="<edgeId>", body='{"data": {"weight": 5}}')
deleteLink(edgeId="<edgeId>")
bulkDeleteLinks(body='{"edgeIds": ["<e1>", "<e2>"]}')
```

---

## Graph Traversal

```
getActorLinks(actorId="<actorId>")          // all edges (in + out) with link details
getLinkedActors(actorId="<actorId>")        // connected actors + link info
getLinked(actorId="<actorId>", type="children")   // "children" | "parents" | "all"
actorGlobalLayers(actorId="<actorId>")      // all layers containing this actor
```

---

## Layer Operations

### Get Layer
```
getLayer(layerId="<layerActorId>")
# Layers ARE actors (with the Layer system form). Returns nodes/edges and their laIds.
```

### Add Nodes to Layer (with positions)
```
manageLayer(
  layerId="<layerActorId>",
  body='[
    {"action":"create","data":{"id":"<actor1>","type":"node","position":{"x":100,"y":100}}},
    {"action":"create","data":{"id":"<actor2>","type":"node","position":{"x":300,"y":100}}}
  ]')
# Response carries laId for each placed element (nodesMap[i].laId) — needed for edges.
```

### Add Edge to Layer
```
manageLayer(
  layerId="<layerActorId>",
  body='[
    {"action":"create","data":{"id":"<edgeId>","type":"edge","laIdSource":<laId A>,"laIdTarget":<laId B>}}
  ]')
```

### Delete Node / Edge From Layer (view only — does not delete the actor)
```
manageLayer(
  layerId="<layerActorId>",
  body='[
    {"action":"delete","data":{"id":"<actor1>","type":"node"}},
    {"action":"delete","data":{"id":"<edgeId>","type":"edge"}}
  ]')
```

### Update Positions on Layer
```
layerActorsPosition(
  layerId="<layerActorId>",
  body='{"actors":[{"actorId":"<actor1>","x":150,"y":150}]}')
```

### Search / Filter Actors on Layer
```
searchLayerActors(layerId="<layerActorId>", query="onboarding")
getLayerActorsByFormId(layerId="<layerActorId>", formId=<formId>)
```

### Misc Layer Operations
```
exist(layerId="<layerActorId>", body='{"actorIds":["<a1>","<a2>"]}')
moveElements(sourceLayerId="<la>", targetLayerId="<lb>", body='{"actorIds":["<a1>"]}')
cleanLayer(layerId="<layerActorId>")   // remove all actors from the view (actors remain)
```

---

## FlowchartBlock: Step-by-Step Workflow

> **CRITICAL RULES**
> 1. Every link needs **TWO** calls: `createLink` (logical) + `manageLayer` with `type:"edge"` (visual). Missing the second call = invisible arrows.
> 2. **Do not** pass `data` when `formId` is a system form from `sys-forms.yaml`. The server auto-injects shape, size, and view from the form definition.

### Step 1 — Discovery

```
1. Read sys-forms.yaml (from current working directory)
   → find FlowchartBlock entry → index childs by title
   → remember graphFormId (title="Graphs") and layerFormId (title="Layers")
   → look up specific block template ids by title (e.g. "Start / Stop", "Process", "Decision")

2. Resolve the layer (graph canvas):
   IF layerId is already known (from Step 0b or context):
     getLayer(layerId=<knownLayerId>)
     → read existing nodes/edges; remember actorId + laId for each
     → DO NOT create a new layer
   ELSE (user requested a new graph):
     createActor(formId=<graphFormId>, body='{"title": "<graph name>"}')
     → save returned actorId as graphId
     createActor(formId=<layerFormId>, body='{"title": "<graph name>"}')
     → save returned actorId as layerId
     createLink(body='{"source":"<graphId>","target":"<layerId>"}')
     → getLayer(layerId=<new layerId>)   // confirm empty state
```

### Step 2 — Create Each Block (repeat per node)

```
// 2a. Create the actor — formId is the CHILD form id from sys-forms.yaml; NO data field; always pass color
// Example: formId = id where title=="Start / Stop" in FlowchartBlock.childs
createActor(
  formId=<startStopFormId>,
  body='{"title": "Start", "color": "#4caf50"}')
→ save actorId from response

// 2b. Place node on layer → get laId (REQUIRED; block won't appear without this)
manageLayer(
  layerId="<graphActorId>",
  body='[
    {"action":"create","data":{"id":"<actorId>","type":"node","position":{"x":<X>,"y":<Y>}}}
  ]')
→ save laId from response.data.nodesMap[0].laId
```

### Step 3 — Create Links Between Blocks

**Preferred (batch):** pass `layerId` to `massLink` — the server places all edges on the
layer automatically, no extra call needed.

```
massLink(
  layerId="<graphActorId>",
  body='[
    {"source":"<actorA>","target":"<actorB>"},
    {"source":"<actorB>","target":"<actorC>"}
  ]')
# → edges created AND drawn on layer in one call
```

**Single link (fallback):** two calls are required.

```
// 3a. Create logical link
createLink(
  body='{"source":"<actorA>","target":"<actorB>"}')
→ save edgeId from response.data.id

// 3b. Draw edge on layer — use laIds from step 2b, not actorIds
manageLayer(
  layerId="<graphActorId>",
  body='[
    {"action":"create","data":{"id":"<edgeId>","type":"edge","laIdSource":<laId A>,"laIdTarget":<laId B>}}
  ]')
```

---

## Layout Algorithm — Auto Coordinate Calculation

**Never hardcode coordinates.** Always calculate using dagre/Sugiyama layout.

### Step 1 — Node Sizes by template title

```
SIZES = {
  "Start / Stop":         { w: 200, h: 50  },
  "Process":              { w: 200, h: 50  },
  "Predefined Process":   { w: 200, h: 100 },
  "Decision":             { w: 200, h: 100 },
  "Data":                 { w: 200, h: 60  },
  "Document":             { w: 200, h: 70  }
}
```

### Step 2 — Rank Assignment (BFS from start node)

```
rank[start] = 0
for each edge source → target:
  rank[target] = max(rank[target] ?? 0, rank[source] + 1)
```

### Step 3 — Group by Rank

```
ranks = { 0: [nodeA], 1: [nodeB, nodeC], 2: [nodeD, nodeE, nodeF], ... }
```

### Step 4 — Gaps

```
nodeSep(rank) = max(max_w_in_rank * 0.3, 60)    // horizontal gap between centers
rankSep(r)    = max(max_h_in_rank(r) * 1.2, 80) // vertical gap between rows
```

### Step 5 — Y Coordinates (top → down)

```
y[rank_0] = 0
y[rank_n] = y[rank_n-1] + max_h(rank_n-1)/2 + rankSep(rank_n-1) + max_h(rank_n)/2
```

### Step 6 — X Coordinates (center each row)

```
for each rank with N nodes:
  center_to_center = max_w_in_rank + nodeSep
  total_span       = (N - 1) * center_to_center
  x[node_i]        = -total_span / 2 + i * center_to_center
```

### Examples

- **3 Start/Stop blocks in a row** (w=200, nodeSep=60, c2c=260, span=520) → x = [-260, 0, 260]
- **6 Process blocks in a row** (w=200, nodeSep=60, c2c=260, span=1300) → x = [-650, -390, -130, 130, 390, 650]
- **Start/Stop(h=50) → Process(h=50)** → rankSep=80, y[rank_1] = 0+25+80+25 = 130
- **Process(h=50) → Decision(h=100)** → rankSep=80, y[rank_2] = 130+25+80+50 = 285

---

## Complete Example: Build a Flowchart

```
# 0. Read sys-forms.yaml (current working directory) and extract form ids
Read sys-forms.yaml
# → graphFormId     = id where title=="Graphs"
# → layerFormId     = id where title=="Layers"
# → startStopFormId = id in FlowchartBlock.childs where title=="Start / Stop"
# → processFormId   = id in FlowchartBlock.childs where title=="Process"
# → decisionFormId  = id in FlowchartBlock.childs where title=="Decision"

# 1. Resolve the layer (see Step 0b — use existing if layerId is already known)
#    CASE A: layerId already known → use it directly
getLayer(layerId="<known layerId>")
# → read existing nodes/edges, remember laIds — do NOT create a new layer

#    CASE B: new graph requested → create Graph + Layer actors and link them
# createActor(formId=<graphFormId>, body='{"title":"My Graph"}')   # → graphId
# createActor(formId=<layerFormId>, body='{"title":"My Graph"}')   # → layerId
# createLink(body='{"source":"<graphId>","target":"<layerId>"}')
# getLayer(layerId=<layerId>)   // confirm empty state

# 2. Create flowchart blocks — NO data field, server auto-injects shape; always pass color
createActor(formId=<startStopFormId>, body='{"title":"Start",    "color":"#4caf50"}')  # → actor_start
createActor(formId=<processFormId>,   body='{"title":"Validate", "color":"#2196f3"}')  # → actor_validate
createActor(formId=<decisionFormId>,  body='{"title":"OK?",      "color":"#ff9800"}')  # → actor_decision
createActor(formId=<startStopFormId>, body='{"title":"Stop",     "color":"#4caf50"}')  # → actor_stop

# 3. Place blocks on the layer (positions from layout algorithm above)
manageLayer(
  layerId="<graphActorId>",
  body='[
    {"action":"create","data":{"id":"actor_start",   "type":"node","position":{"x":0,  "y":0  }}},
    {"action":"create","data":{"id":"actor_validate","type":"node","position":{"x":0,  "y":130}}},
    {"action":"create","data":{"id":"actor_decision","type":"node","position":{"x":0,  "y":285}}},
    {"action":"create","data":{"id":"actor_stop",    "type":"node","position":{"x":0,  "y":440}}}
  ]')
# Save each returned laId (laStart, laValidate, laDecision, laStop)

# 4. Create logical links AND place them on the layer in one call
#    Pass layerId → server auto-calls manageLayer after creating the edges
massLink(
  layerId="<graphActorId>",
  body='[
    {"source":"actor_start",    "target":"actor_validate"},
    {"source":"actor_validate", "target":"actor_decision"},
    {"source":"actor_decision", "target":"actor_stop"}
  ]')
# → edges are created AND drawn on the layer automatically (no step 5 needed)
```

---

## Financial Operations

### Accounts

```
getAccounts(actorId="<actor>")
getAccount(accountId="<acc>")
getAccountsByCurAccName(actorId="<actor>", currencyId="<cur>", nameId="<name>")
getChildrenAccounts(actorId="<actor>")

createAccounts(actorId="<actor>", body='{"nameId":"<name>","currencyId":"<cur>","accountType":"default"}')
postAccounts( body='{"accountName":"Balance","currencyName":"USD"}')   // pair (debit+credit)
setAmount(accountId="<acc>", body='{"amount":1000}')
blockAccount(actorId="<actor>", body='{"nameId":"<name>","currencyId":"<cur>","type":"default","status":"blocked"}')

delAccounts(actorId="<actor>", currencyId="<cur>", nameId="<name>", accountType="default")
```


### Currencies & Account Names

```
getCurrencies()
createCurrency( body='{"name":"USD"}')
getAccountNames()
createAccountName( body='{"name":"Balance","abbreviation":"BAL"}')
```

---

## ⚠️ Missing Tools — Gaps vs Old MCP Server

These tools from the previous MCP server have **no equivalent** in the current
swagger-generated server:

| Old Tool | Status | Notes |
|---|---|---|
| `simulator_validate_link` | **Missing** | `existLink` only reports whether a link already exists. |
| `simulator_get_graph_layer_paginated` | **Missing** | Use `getLayer` (returns full layer at once). |
| Reactions (comment, sign, done, rating, reject, freeze) | **Missing** | Not exposed. |
| File upload / attachment | **Missing** | Not exposed. |
| Transfers (`createTransfer`, `getTransfer`, `postTransfersFilter`, …) | **Missing** | Not present in current swagger. |

---

---

## Key Rules

- **Always read `sys-forms.yaml` from the current working directory first** (Step 0a) to look up all form ids by title. Never hardcode numeric form ids.
- **For plain text / labels on a graph** use `formId = defaultFormId` (root entry `title == "Default"`) and put the visible text in `description`. Do **not** pass `data`; do **not** use a FlowchartBlock child form.
- **If `layerId` is already known** (from user message, conversation history, or session context) → **use it directly, never create a new layer**. Call `getLayer(layerId=<known>)` to read current state and continue from there.
- **For system forms (anything in `sys-forms.yaml`), do NOT pass `data`** — the server auto-injects shape/view/blockId from the form definition. Pass only `title`, `description`, `color`, etc.
- **Always pass `color`** (hex string) when creating an actor — use the default from the title→color table in Step 0a or apply the user's requested color.
- **Every link needs TWO calls to appear on the graph**: `createLink` (logical) + `manageLayer` with `type:"edge"` (visual). Missing the second call = invisible arrows.
- Use the **child** form's id as `formId`, not the parent's (e.g. the id of "Start / Stop" child, not the id of "FlowchartBlock" parent).
- `laId` ≠ `actorId`. `laId` is assigned by `manageLayer` when you place an actor on a layer; reuse it for edges.
- `cleanLayer` only removes actors from the view; the actors themselves still exist.
- Prefer `massLink` over many `createLink` calls — it's atomic and efficient.
- Space actors ~200–300 px apart when laying out; use the layout algorithm rather than hardcoded coordinates.
- `searchActors` searches globally across the workspace; `searchLayerActors` / `getLayerActorsByFormId` scope to a single layer.

---

## Reference Documents

Use the `Read` tool to load these files when you need more detail:

| Path | When to read |
|---|---|
| `sys-forms.yaml` (current working directory) | **Always read first (Step 0a)** — catalog of all system forms with numeric ids. Look up `graphFormId`, `layerFormId`, and all FlowchartBlock child template ids here. |
| `$CLAUDE_PLUGIN_ROOT/docs/entities/actors.md` | Full actor property list and types |
| `$CLAUDE_PLUGIN_ROOT/docs/entities/links.md` | Link/edge properties and type system |
| `$CLAUDE_PLUGIN_ROOT/docs/entities/layers.md` | Layer types (tree, graph, process, dashboard) and behavior |
| `$CLAUDE_PLUGIN_ROOT/docs/user-flows/graph-functionality.md` | Complete graph building walkthrough with test scenarios |
| `$CLAUDE_PLUGIN_ROOT/docs/user-flows/actor-graph-management.md` | Managing actors on graphs — practical patterns |
