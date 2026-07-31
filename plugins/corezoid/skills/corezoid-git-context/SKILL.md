---
name: corezoid-git-context
description: >
  Analyzes the current session and updates _ext/docs/*.md in the Corezoid git
  mirror for the active stage. Use after corezoid-edit, corezoid-create, or
  corezoid-review when the session was substantial (processes deployed, new
  external dependencies discovered, architectural decisions made, issues found
  or resolved). Activate when the user says "update context", "save context",
  "update docs", "push context", "зафіксуй контекст", "збережи контекст",
  "оновити документацію", "corezoid-git-context", or signals the session is
  wrapping up after real changes were made to Corezoid. Do NOT activate for
  purely informational sessions where nothing was deployed.
---

# Session Analysis — Update _ext/docs/*.md

You manage the documentation layer for the Corezoid stage in the git mirror.
Your only write surface is `_ext/docs/` inside the stage directory.
You use four MCP tools: `read-context-file`, `update-context-file`,
`git-pull-context`, `git-push-context`.

---

## Prerequisites — skip entirely if any condition is not met

Before doing anything, verify all of the following. If any check fails, log
one warning line and stop — do not continue to subsequent steps.

1. **Stage path known** — read `.env` in the current working directory and
   extract `COREZOID_GIT_STAGE_PATH`.
   - If the key is absent → "Git context not configured for this stage (COREZOID_GIT_STAGE_PATH missing). Skipping context update."
2. **Git credentials (optional — only needed for push)** — writing to `_ext/docs/`
   works locally regardless of whether git credentials are set. The `git-push-context`
   step at the end will silently skip if `API_LOGIN`, `API_SECRET`, or
   `COREZOID_GIT_URL` are missing — local commits are preserved and will be
   pushed when credentials become available.
   - Do **not** skip the entire context update just because credentials are absent.
3. **Session was substantial** — at least one of:
   - `create-process` or `push-process` was actually called (not just previewed);
   - a new external host/API/service appeared in a process that is not yet in `dependencies.md`;
   - an architectural decision was explicitly made (one approach chosen over another);
   - an issue was found or closed during the session.
   - If none of the above → "Session was informational only. No context update needed."

---

## Step 1 — Sync git context

Call `git-pull-context` with no arguments. This ensures `.git-context/` is
up to date before reading or writing any files. If the tool returns an error,
stop and report: "Could not sync git context: {error}. Skipping update."

---

## Step 2 — Read current state of all 5 files

Using `STAGE_PATH` = value of `COREZOID_GIT_STAGE_PATH` from `.env`:

Call `read-context-file` for each path below. A "not found" result is normal
— treat missing files as empty (no current content).

| File | Path |
|------|------|
| context.md | `{STAGE_PATH}/_ext/docs/context.md` |
| invariants.md | `{STAGE_PATH}/_ext/docs/invariants.md` |
| decisions.md | `{STAGE_PATH}/_ext/docs/decisions.md` |
| dependencies.md | `{STAGE_PATH}/_ext/docs/dependencies.md` |
| issues.md | `{STAGE_PATH}/_ext/docs/issues.md` |

Read **all 5 at once** before analysing — you need the full picture to avoid
duplication and to detect which existing entries need updating.

---

## Step 3 — Classify: which files to touch and how

Analyse the session transcript and the Corezoid diff (what was
created/changed/deployed) against the current file contents.

Typically only 1–2 files need changes. Apply the following rules per file:

### context.md
**What it is:** living description of the stage's business purpose and role.
**When to touch:** only if the stage's purpose or role genuinely changed.
**How:** replace the relevant sentence/paragraph in place. Not a journal —
do not append. If the file is missing and there is clear new context to
record, create it.

### invariants.md
**What it is:** constraints that must never be violated.
**When to touch:** new invariant discovered, or existing one proved wrong.
**How:** replace in place. Never create an empty placeholder.

### decisions.md
**What it is:** ADR-style log of architectural decisions.
**When to touch:** a real architectural decision was made (chose approach A
over B, accepted a trade-off, chose a pattern).
**How:** append a new entry in this format:
```
## {Short title} — {YYYY-MM-DD}
**Decision:** {what was decided}
**Rationale:** {why}
**Alternatives rejected:** {what and why not}
```
If an older decision is now superseded, add `**Superseded by:** {new title}`
to the old entry — do not silently rewrite it.

### dependencies.md
**What it is:** external APIs, services, queues this stage integrates with.
**When to touch:** a new external host/service appeared in the session.
**How:** check existing entries first — if the same service is already listed,
do not add a duplicate. If genuinely new, append:
```
- **{ServiceName}** — {URL or endpoint pattern} — {what it's used for}
```

### issues.md
**What it is:** known problems, limitations, TODOs.
**When to touch:** new issue found, or existing issue resolved.
**How:**
- New issue → append:
  ```
  - [ ] {description} — found {YYYY-MM-DD}
  ```
- Resolved issue → find the matching open entry and mark it:
  ```
  - [x] {description} — found {date}, resolved {YYYY-MM-DD}
  ```
  Replace the existing line in-place using `mode: replace` on the full file
  content, do not append a duplicate.

---

## Step 4 — Unified diff and confirmation

**Before writing anything**, present the full planned changes to the user
as a single markdown block:

```
## Proposed context updates

### {filename}.md — {replace | append | new file}
{show the new content or the changed section clearly}

### {filename2}.md — ...
...
```

Then ask: **"Apply these context updates and push to git? (yes/no)"**

- If **yes** → proceed to Step 5.
- If **no** → "Context update skipped. Your Corezoid changes are already
  deployed and unaffected." Stop here.

---

## Step 5 — Write files and push

For each file that needs updating:

1. Call `update-context-file` with:
   - `path`: `{STAGE_PATH}/_ext/docs/{filename}.md`
   - `content`: the full new file content (for replace) or the text to append
   - `mode`: `replace` for context/invariants/issues (full rewrite or targeted
     replace); `append` for decisions/dependencies when adding a new entry

   If the file does not exist yet, `mode: replace` with the full content
   creates it — the directory structure is created automatically.

2. After **all** files are written, call `git-push-context` once with a
   meaningful commit message, e.g.:
   `"docs: update _ext/docs/ after session — {brief summary of what changed}"`

   One push for the whole session, not one push per file.

---

## Error handling

| Situation | Action |
|-----------|--------|
| `read-context-file` returns "not found" | Treat as empty file, continue |
| `update-context-file` returns error | Log warning, skip that file, continue with others |
| `git-push-context` returns error | Log warning, report to user: "Changes written locally but push failed: {error}. Run git-push-context manually when git is available." |
| No files needed updating | "Session analysed — no context updates needed." |

---

## What this skill never does

- Never writes to `CLAUDE.md` — that file is owned by the git mirror bot.
- Never touches files outside `_ext/docs/` (no `_ext/meta/`, no root files).
- Never creates empty/placeholder files — only writes when there is real content.
- Never pushes more than once per session invocation.
- Never blocks or rolls back the main Corezoid task — context update is always optional.
