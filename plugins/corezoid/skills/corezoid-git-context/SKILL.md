---
name: corezoid-git-context
description: >
  Explicitly updates _ext/docs/ files for the current Corezoid stage in the
  git mirror. Use when the user wants to view or update stage documentation:
  business context, external dependencies, architectural decisions, invariants,
  or known issues. Activate when the user says "обнови документацию стейджа",
  "update stage docs", "what's in _ext", "show context", "add to decisions",
  "record an issue", "document dependencies", or asks to write anything to
  _ext/docs/. Also activate after any edit/create/review session when the user
  says "save what we learned" or "update the docs".
---

# Corezoid Git Context Manager

You manage the `_ext/docs/` documentation layer for the current stage in the
Corezoid git mirror. These files accumulate knowledge across sessions and are
included in `CLAUDE.md` automatically via a server-side hook.

## File map

| File | Purpose |
|---|---|
| `_ext/docs/context.md` | Business purpose of this stage — what it does, why it exists |
| `_ext/docs/dependencies.md` | External APIs, services, queues, databases this stage integrates with |
| `_ext/docs/decisions.md` | Architectural decisions (ADR-style): what was chosen and why |
| `_ext/docs/invariants.md` | Constraints that must not be violated (rate limits, ordering, field formats) |
| `_ext/docs/issues.md` | Known bugs, TODOs, tech debt items |

---

## Step 1: Read current state

Call `read-context-file` for each of the five files. Note which exist and what they contain.

If the user asked for a specific file only, read that one.

---

## Step 2: Determine what to update

Based on:
- The user's explicit instruction ("add this to decisions.md")
- The current conversation context (what was just done in this session)
- What is already in the files (do not duplicate)

Decide which files need to be created or appended.

---

## Step 3: Generate content

Write in plain Markdown. Follow these conventions per file:

### `context.md`
One paragraph describing the stage's business purpose. Focus on WHAT and WHY,
not HOW. Example:
```
## Stage context
This stage handles the Smart Form Prompt Node feature — a Corezoid-native AI
assistant that enriches incoming form submissions with LLM-generated suggestions
before routing them to downstream processes.
```

### `dependencies.md`
Bullet list. Each entry: service name, what it's used for, the env_var holding the URL/key.
```
## External dependencies
- **OpenAI API** — LLM completions for prompt enrichment (`{{env_var[@openai-url]}}`)
- **Simulator.Company** — actor creation and form submission (`{{env_var[@simulator-url]}}`)
```

### `decisions.md`
ADR-style entries with date. Each entry: decision, rationale, alternatives considered.
```
## [2026-07-07] Use api_rpc for all sub-process calls
**Decision:** All inter-process calls use `api_rpc` (synchronous), not fire-and-forget.
**Why:** The caller needs the result to decide the next routing step.
**Alternatives:** `api_copy` (async) — rejected because response is needed immediately.
```

### `invariants.md`
Bullet list of hard constraints.
```
## Invariants
- `ref` must be unique per form submission — duplicate refs cause silent task merge
- The Escalation process (379055) must always be called before the Final node
- Do not hardcode model names — use `{{env_var[@llm-model]}}` to allow hot-swap
```

### `issues.md`
Numbered list. Each entry: severity, description, date found.
```
## Known issues
1. **[MEDIUM 2026-07-07]** Retry logic missing on OpenAI 429 — tasks may fail silently under load
2. **[LOW 2026-07-07]** TODO: add `context.md` description for the Configuration folder
```

---

## Step 4: Apply updates

For each file that needs updating:
```
update-context-file(
  path="_ext/docs/<file>.md",
  content="<new content>",
  mode="append"   // or "replace" if rewriting the whole file
)
```

Use `mode="replace"` only when restructuring the entire file. Default to `append`.

---

## Step 5: Push

Call `git-push-context` to commit and push all changes.

If push returns 403 (server-side push not yet enabled):
> "Changes saved locally in `.git-context/`. They will be pushed automatically when server-side write access is enabled."

---

## Notes

- Never fabricate dependencies or decisions not visible in the processes or stated by the user
- If the user asks to "view" without updating, skip Steps 3–5
- Read `CLAUDE.md` of the stage for broader context if needed (it embeds `_ext/docs/` content)
