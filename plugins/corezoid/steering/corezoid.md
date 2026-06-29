---
inclusion: always
---

# Corezoid — workspace guardrails

These rules apply to every interaction with the Corezoid MCP server in this
workspace. They mirror the always-on guardrails the `corezoid` skill carries
inside Claude Code / Codex installs; here they ship as a Kiro steering file
so Kiro applies them without depending on skill auto-routing.

## Process-JSON invariants (MANDATORY)

Every Corezoid process file is named `<ID>_<name>.conv.json` and **must**
satisfy these rules before any `push-process` call — violations cause the
deploy to fail server-side:

1. **Node IDs are 24-character hexadecimal strings.** Generate them with
   `openssl rand -hex 12` (or equivalent). Never reuse a node id across
   processes.
2. **`extra` ↔ `extra_type` parity.** Every key present in `extra` must have a
   matching key in `extra_type` with the correct type tag, and vice versa.
3. **Stringified JSON in `extra`.** Object values inside `extra` must be JSON
   strings (`"{\"key\":\"val\"}"`), not nested objects.
4. **`err_node_id` on fallible nodes.** `set_param`, `api_rpc`, `api_code`,
   `api_copy`, `db_call`, `git_call`, `api_sum`, `api_reply` all require
   `err_node_id` pointing at a real downstream node.
5. **Call-Process nodes are `type: "api_rpc"`** with `extra`/`extra_type` —
   never `data`/`data_type`.
6. **No hardcoded constants.** URLs, tokens, ids: every constant must be a
   Corezoid variable reference `{{env_var[@variable-name]}}` — never inline
   strings.
7. **`obj_type`**: process-level → 1=process, 0=folder.
   Node-level → 0=code/api, 1=start, 2=end, 3=condition.

## Tool routing

- Each Corezoid REST/MCP operation is exposed as one MCP tool whose name
  matches the documented operation id.
- For broader platform context — process design, review, deployment, state
  diagrams — the matching skill under `.kiro/skills/corezoid-*` carries the
  deep reference docs. Activate the relevant one when the user signals
  intent.

## Language policy

- Internal instructions, node ids, and JSON keys stay in English.
- Reply to the user **in the language they wrote in** (English, Ukrainian,
  Russian, …). Never hardcode a non-English sentence for Claude to say.

## Reporting platform-level mistakes

When the user signals a **platform-level mistake** (wrong node type, wrong
API choice, wrong process structure, missing required platform field), add a
single follow-up line offering to forward the report to the Corezoid team:

- Bug / broken behaviour → "Хотите сообщить о баге команде Corezoid?"
- Unexpected plugin choice → "Хотите сообщить об этом команде Corezoid?"
- Improvement suggestion → "Хотите отправить пожелание команде Corezoid?"

Do **not** add this line for normal business-logic iteration (renaming
fields, adjusting conditions, changing values).
