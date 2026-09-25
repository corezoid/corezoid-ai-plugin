Loaded when writing PLAN.md in Phase 3 of `corezoid-gen-bot`. This is the exact schema Phase 3 must produce.

### PLAN.md schema

````markdown
---
bot_slug: {bot_slug}
description: {one paragraph — who talks to the bot and what for}
channels: [{telegram|viber|fbmessenger|abc}, …]
tokens_supplied: {true|false}          # never the tokens themselves
lang: {en|uk|ru}
locales: [{lang}, …]
corezoid:
  workspace_id: {id}
  project_id: {id|null}
  stage_id: {id|null}
sources:                                # the handed-in processes
  folder_id: {id|null}
  process_ids: [{id}, …]
manifest: .corezoid-gen-bot/bot-contract.json
orchestrator:                           # empty until Phase 4 has run
  folder_id: {id|null}
  folder_url: {url|null}
  obj_id: {wizard job id|null}
  webhooks_url: {[{channel,url}]|null}
  dashboard_url: {url|null}
  built_at: {ISO-8601|null}
template_ids:                           # read from the pulled mirror in Phase 4
  main: {id|null}
  router: {id|null}
  send_message: {id|null}
  system_diagram: {id|null}
  localization: {id|null}
  attachments: {id|null}
  user_profile: {id|null}
  bots_folder: {id|null}
  user_id: {id|null}                    # the user_id the wizard stamped on its own api_copy nodes
probed: [{process_id}, …]               # user-authorised live probes
extraction_timestamp: {ISO-8601}
---

## Contract summary
| Process | Callable | Inputs | success keys | alternate | Side effects | Nodes |
|---|---|---|---|---|---|---|
{one row per handed-in process}

## Coverage
| Process | Command | Called by | Wiring |
|---|---|---|---|
{one row per handed-in process — every one of them}

## Commands
One entry per bot process to generate:
- `/{command}` — alias `{command}`; skeleton: {bot_reply|bot_dialog}
  - calls: `{process_id} {title}` via {api_rpc|api_copy}{, then `{process_id}` …}
  - dialog steps: 1. {ask text_id} → input `{name}` (validate `{regex}`) …
  - argument sources: {name: ask|chat_id|channel|profile:<field>|const:<v>|chain:<pid>.<key>}
  - outcomes: success → `{text_id}` using {payload keys}; alternate {result/code} → `{text_id}`; error → `serviceError`
  - namespacing: {required (2+ calls) | not needed (single call)}
  - attachments: {attachment_id list}
  - confirm step: {yes — side effects {likely|unknown} | no}

## Localization keys
One line per `text_id` with the text per locale. Reuse the template's
`mainMenu`, `commandNotFound`, `timeout`, `serviceError`, `selectError`.

## Attachments
One line per `attachment_id`: the type per channel and the button payloads,
including the `mainKeyboard` additions — one button per command.

## Menu wiring
Which commands appear on `mainKeyboard` and in `mainMenu`, in order.

## Skipped processes
`{process_id} {title} — {reason}`.

## Reference materials consulted
`- {kind}: {short label} — {how it shaped the plan}`, or `- none supplied`.

## Known unknowns / risks
Unknown array shapes, `unknown` side effects wired behind a confirm, paused
processes, `params` disagreements, dead declared inputs, anything extraction
could not resolve. If none, write `- none`.
````

