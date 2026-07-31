# Corezoid Node Size Reference

This document is the geometry reference for automatic process layout. It
separates three concepts that are easy to confuse:

- `obj_type` defines the structural role of a node (`0`, `1`, `2`, or `3`);
- the first meaningful `condition.logics[].type` defines the rendered node
  kind (`api`, `api_code`, `api_queue`, and so on);
- `extra.modeForm` and node content determine the final rendered dimensions.

Consequently, there is no single size for `obj_type: 0` or `obj_type: 3`.

## Measurement environment

The verified values below were read from the live SVG DOM with
`getBoundingClientRect()` and the unscaled SVG `width` / `height` attributes:

- Corezoid UI: `v6.12.0`;
- processes: `1891385`, `1891386`, and `1891397`;
- measurement date: `2026-07-29`;
- browser zoom does not affect the recorded SVG dimensions.

The confidence column has the following meaning:

- **measured** — this exact state and size was present in the live UI;
- **shared renderer** — the type uses the same action-node renderer as a
  measured type, but this exact type/state was not present in the sample;
- **pending** — a dedicated live fixture is still required.

## Structural `obj_type` values

| `obj_type` | Structural role | Visual size rule |
|---:|---|---|
| `0` | Business-flow/action node | Determined by `logic.type`, mode, title, and output rows |
| `1` | Start | Fixed circle, `56×56` |
| `2` | Final success/error | Fixed circle, `56×56` |
| `3` | Escalation/error-flow node | Same renderer as its `logic.type`; commonly collapsed to `56×56` |

## Node-kind matrix

`200×98 + content` means a 200px-wide expanded action node whose height grows
according to the modifier table below. Every non-terminal kind renders as
`56×56` when collapsed.

| Corezoid UI node | Structural `obj_type` | Discriminator | Collapsed | Expanded baseline | Confidence |
|---|---:|---|---:|---:|---|
| Start | `1` | structural role | `56×56` | not applicable | measured |
| End: Success | `2` | `extra.icon: success` | `56×56` | not applicable | measured |
| End: Error | `2` | `extra.icon: error` | `56×56` | not applicable | measured |
| Condition | `0`; `3` only as an escalation target | `go_if_const` | `56×56` | see Condition table | measured |
| Code | `0` or `3` | `api_code` | `56×56` | `200×98 + content` | measured |
| API Call | `0` or `3` | `api` | `56×56` | `200×98 + content` | shared renderer |
| Copy Task | `0` or `3` | `api_copy`, create/copy mode | `56×56` | `200×98 + content` | measured |
| Modify Task | `0` or `3` | `api_copy`, modify mode | `56×56` | `200×98 + content` | measured |
| Set Parameters | `0` or `3` | `set_param` | `56×56` | `200×98 + content` | measured |
| Sum | `0` or `3` | `api_sum` | `56×56` | `200×98 + content` | shared renderer |
| Queue | `0` or `3` | `api_queue` | `56×56` | `200×98 + content` | measured (`200×98`) |
| Get from Queue | `0` or `3` | `api_get_task` | `56×56` | `200×98 + content` | shared renderer |
| Waiting for Callback | `0` or `3` | `api_callback` | `56×56` | `200×98 + content` | collapsed measured; expanded shared |
| Call a Process | `0` or `3` | `api_rpc` | `56×56` | `200×98 + content` | measured |
| Reply to Process | `0` or `3` | `api_rpc_reply` | `56×56` | `200×98 + content` | shared renderer |
| Database Call | `0` or `3` | `db_call` | `56×56` | `200×98 + content` | shared renderer |
| GIT Call | `0` or `3` | `api_git` | `56×56` | `200×98 + content` | shared renderer |
| Form | `0` or `3` | `api_form` | `56×56` | content-dependent | pending |
| Delay / Timer | `0` or `3` | no action logic; `time` semaphore | `56×56` | content-dependent | pending |
| Count semaphore | modifier, not a separate `obj_type` | `count` semaphore | inherited | adds an output row | pending |
| Plain transition / escalation stub | `0` or `3` | only `go` | `56×56` in sampled processes | content-dependent if expanded | collapsed measured |

## Verified expanded action sizes

These values demonstrate that action-node height is content-driven rather than
type-driven.

| Shape in live SVG | Size |
|---|---:|
| Standard action header, no visible error/semaphore row | `200×98` |
| Standard title and one `Error` output row | `200×125` |
| One `Error` row and one additional wrapped title line | `200×141` |
| One `Error` row and two additional wrapped title lines | `200×157` |

For the measured standard action renderer:

```text
height = 98
       + 27 × visible output rows
       + 16 × additional wrapped title lines
```

The `27px` output-row increment is the change in the outer node rectangle. The
row's inner SVG rectangle is `36px` high and overlaps the surrounding padding,
so adding `36px` to the outer height would be incorrect.

This formula is verified for the sampled `Error` output row. Time and count
semaphore rows must be measured separately before assuming the same increment.

## Verified expanded Condition sizes

| Visible `go_if_const` rows | Size | Delta |
|---:|---:|---:|
| 2 | `200×151` | baseline sample |
| 3 | `200×180` | `+29` |
| 4 | `200×208` | `+28` |

A safe approximation for the sampled range is:

```text
height = 151 + round(28.5 × (condition rows - 2))
```

Title wrapping and error/semaphore outputs are additive and require the same
content inspection as action nodes.

## Current layout-engine differences

The current implementation in `mcp-server/layout_graph.go` intentionally used
conservative estimates. Live measurement shows where those estimates now
create avoidable whitespace:

| Geometry component | Live UI v6.12 | Current engine | Effect |
|---|---:|---:|---|
| Collapsed non-terminal | `56×56` | `48×48` | underestimates collision box |
| Expanded action baseline | `200×98` | `200×90` | underestimates empty action |
| Error output row | `+27` | `+56` | overestimates error-heavy nodes |
| Additional title line | `+16` | `+18` | slightly overestimates long titles |
| Condition with 2 rows | `200×151` | `200×225` | overestimates by `74px` |
| Condition with 3 rows | `200×180` | `200×255` | overestimates by `75px` |
| Condition with 4 rows | `200×208` | `200×285` | overestimates by `77px` |

The table is deliberately descriptive. Changing the engine constants should be
done in a separate patch with updated golden layouts, overlap tests, and a live
visual check.

## Remaining measurement fixture

To close all pending cells without changing a real process, create one
temporary measurement process containing:

1. every action type with a short title and no optional output;
2. the same type with one error output;
3. Delay with one and two time semaphores;
4. a node with one count semaphore;
5. Form with representative field counts;
6. expanded Condition nodes with 1–6 rows;
7. titles wrapping to 1–6 lines.

Record the outer `rect.e_resize` width/height, not the browser-scaled bounding
box. The fixture can then be deleted after its measurements are committed here.
