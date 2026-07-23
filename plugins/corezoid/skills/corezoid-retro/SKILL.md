---
name: corezoid-retro
description: >
  Corezoid session retrospective specialist. Analyzes the finished (or ending)
  session of Corezoid work and turns what was learned into concrete,
  ready-to-apply updates — workspace CLAUDE.md entries, feedback reports to the
  Corezoid team, permission allowlist entries, personal memory. Routes each
  finding to the right scope and applies nothing without explicit user
  confirmation. The goal: every session makes the next one smarter. Use when
  the user says "retro", "retrospective", "what did we learn", "capture
  learnings", "update CLAUDE.md from this session", "session retrospective",
  "ретро", "что мы узнали", "зафиксируй выводы", or signals the session is
  wrapping up and wants to keep the takeaways. Do not trigger on a mere
  summary request that transfers nothing into files or configs.
---

# Run a Corezoid Session Retrospective

You are analyzing the current session to extract knowledge that should outlive
it. A finding is only valuable if a **future session would do something
differently** because of it. Everything else is a summary, not a finding.

## Step 1: Scope the session

Re-read the conversation and answer:

- What was the task, and what was the outcome (done / partially done / blocked)?
- Which Corezoid MCP tools were called, and which of them failed at least once?
- Where did the user intervene, correct, or redirect?

Do not ask the user these questions — derive the answers from the transcript.

## Step 2: Scan for high-signal moments

Corezoid sessions produce learnings in predictable places. Check each of these
explicitly — do not skip a row because it "probably didn't happen":

| # | Signal | What to extract |
|---|--------|-----------------|
| 1 | `push-process` / `lint-process` / `modify-*` failed, then a later attempt succeeded | The **delta** between the failing and the working attempt — this is the single richest signal |
| 2 | Real task data contradicted an assumed shape (a field was a JSON string, not an array; a param was absent on some tasks) | The actual shape, stated as a fact about that process/workspace |
| 3 | The user corrected the approach mid-flow ("don't hardcode that", "use the alias, not the ID") | The rule behind the correction, generalized one level up from the specific instance |
| 4 | The same process ID, folder ID, alias, or env-var name was looked up more than once | The stable fact worth pinning (ID ↔ name ↔ purpose) |
| 5 | A plugin skill's documented procedure did not match actual platform behavior | Which skill, which step, what actually happens |
| 6 | An MCP tool returned an error that required a workaround (not a user mistake) | Repro conditions + the workaround |
| 7 | The user was prompted for permission on read-only calls more than twice | The tool pattern to allowlist |

## Step 3: Route each finding

Every finding gets exactly one destination:

| Destination | What belongs there | How it is applied |
|---|---|---|
| **Workspace `CLAUDE.md`** (in the pulled project directory) | Workspace facts: process IDs and aliases, folder layout, env-var names, data shapes, team conventions ("responses are built from the localize process"). Committable — teammates inherit it. | Append under a `## Corezoid notes` section; create the file if absent |
| **Feedback to the Corezoid team** (via the `corezoid-feedback` skill) | Signal rows 5 and 6: a skill instruction that is wrong or incomplete, or an MCP tool bug. This is how the plugin itself improves. | Prepare the `problem` / `expected` / `tool` fields from the session; the feedback skill owns the payload preview, secret redaction, and final consent before `send-feedback` is called |
| **`settings.local.json`** | Signal row 7: permission allowlist entries for read-only tools | Add to `permissions.allow` |
| **Personal memory** (`~/.claude/CLAUDE.md` or the user's memory system if present) | The user's own preferences and machine-specific facts the team does not need | Follow the user's existing memory format if one is visible |

Routing rules:

- Workspace-specific vs universal is the first split: a fact about *this*
  workspace (an ID, a folder, a convention) goes to workspace CLAUDE.md; a
  fact about the *platform or plugin* goes to the Corezoid team via feedback.
- If an MCP bug (row 6) has a workaround, route the bug through feedback
  **and** add the workaround to workspace CLAUDE.md marked
  `(workaround — feedback ticket <ID>)` so it can be removed once fixed.
- A feedback finding still goes through this skill's confirmation checklist
  first (Step 5); on confirmation, hand off to `corezoid-feedback` — it asks
  for its own final consent before submitting. Never call `send-feedback`
  directly from this skill.
- When unsure between team (CLAUDE.md) and personal, prefer personal — a wrong
  personal note costs one person, a wrong team note misleads everyone.

## Step 4: Filter against existing knowledge

Before proposing a finding:

- Read the current workspace `CLAUDE.md` (and the user's memory index if
  visible). If the fact is already recorded, drop it — or propose a
  **correction** if the session proved the existing note wrong.
- Drop one-off facts: a task ID that failed today, an MR number, anything that
  lives in a ticket. The test: "will this matter in a session a month from
  now?"
- Drop anything derivable in under a minute from the pulled process JSON —
  knowledge that is cheap to re-derive is not worth storing.
- If the same problem was already reported via feedback this session, do not
  propose reporting it again.

## Step 5: Present the report

Output exactly this structure, then STOP and wait:

```
## Session in one line
<task and outcome in one sentence>

## What went well
- <1-3 specific points with evidence; if nothing non-trivial, say so>

## What went poorly
- <1-5 specific points with evidence; if nothing serious, say so>

## Findings — <N>

**1. <short title>** `→ <destination>`
<why: the specific session moment, with the tool call or quote>
<the exact content to be written — CLAUDE.md text, feedback problem/expected
fields, settings entry — ready to apply verbatim>

**2. ...**

## Not capturing
- <candidates that looked like findings but failed the Step 4 filters, one
  line each with the reason — this guards against knowledge bloat>
```

Then present a confirmation checklist:

```
☐ 1. <title> → workspace CLAUDE.md
☐ 2. <title> → feedback to Corezoid team
☐ Apply nothing
```

## Step 6: Apply confirmed findings only

- Apply **only** the items the user checked. Never write a file, submit
  feedback, or edit settings before explicit confirmation.
- When appending to an existing `CLAUDE.md`, read it first and match its
  style; never overwrite or reorder existing content.
- For feedback items, hand off to the `corezoid-feedback` skill with the
  prepared fields; report the returned ticket ID back to the user.
- After applying, list what was written where, in one line per item.

## Rules

- Zero findings is a valid outcome. Say "nothing worth capturing" rather than
  inventing a finding to fill the report.
- Findings must contain the verbatim content to write — never "we should
  document X" without the text of X.
- One fact per finding. If a finding needs the word "and", split it.
- Never store secrets, tokens, or API keys in any destination.
- The feedback route requires the `send-feedback` MCP tool (plugin ≥ 2.6.0);
  on older versions, present the finding text for manual reporting instead.
