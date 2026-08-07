---
name: plan-to-issues
description: >
  Turns a work plan discussed in the current conversation into a structured set
  of GitHub issues in the corezoid/corezoid-ai-plugin repo: one parent epic
  issue plus one child issue per logical task, grouped by a milestone and
  typed with labels. Use whenever the user says "заведи задачи", "создай
  issues", "разбей план на задачи", "plan to issues", "план в issues",
  "нарежь план", "нарезать план", "заведи эпик", "create epic", "split plan
  into tasks", or asks to turn a plan / roadmap / TODO discussed in chat into
  tracked GitHub work items. Also trigger when the user pastes a plan and
  asks to "заведи это в гитхаб" / "put this in github" / "make issues out of
  this".
---

# Plan → GitHub Issues

Turn a plan already discussed in this conversation into a tracked set of
GitHub issues. **No file input, no URL fetching** — the plan lives in the
chat history above the invocation. Read it, split it, ship it.

Repo is fixed: `corezoid/corezoid-ai-plugin` (this project). All `gh` commands
run against the current repo — do not pass `--repo`.

## Non-negotiables

- **English only.** Every issue — title, body, labels, milestone title and
  description — MUST be written in English, regardless of the language the
  plan was discussed in. Translate the plan as you split it. This is
  non-negotiable: never publish Russian, Ukrainian, or any other language
  to GitHub. If a term is a project-specific proper noun (node type name,
  file path, CLI flag, tool name) keep it verbatim; everything else is
  English prose.
- **Create immediately.** The user has pre-authorised issue creation for this
  skill. Do not ask "shall I create these?" Do the work and report what was
  created.
- **Every child issue is self-contained** — a coding agent must be able to
  open the issue and start work with zero extra context. That means: goal,
  motivation, files/areas, acceptance criteria, verification, and links to
  related tasks are all inside the body.
- **Never invent tasks.** If the plan is vague on a point, mark it in the
  issue as `> ⚠ Assumption:` rather than silently filling gaps.
- **Never post secrets or internal URLs** into issue bodies. Redact if the
  chat history contains any.

## Workflow

### 1. Extract the plan from context

Scroll the current conversation for the plan. Signals: a numbered list of
steps, a bulleted roadmap, a "TODO / next steps" block, or a design outline
the user explicitly called a plan. If there are competing plans, use the
**most recent** one the user endorsed.

If you truly can't find a plan in the context, stop and ask the user to
paste it — do not fabricate.

### 2. Split into logical tasks

Each task should be:

- **Independently shippable** — mergeable as its own PR.
- **Bounded** — a coding agent can finish it in one focused sitting.
- **Ordered by dependency** — if B needs A merged first, list A first and
  record the dependency in B.

Aim for 3–10 child tasks. If the plan naturally produces 1–2, skip the epic
and just create standalone issues. If it produces 15+, group into phases and
make each phase its own child (with sub-bullets inside the body) rather than
one issue per micro-step.

### 3. Classify each task → label

Map the task's dominant nature to an existing repo label:

| Task nature | Label |
|---|---|
| New capability, new tool, new skill, new node type | `enhancement` |
| Fixing broken behaviour | `bug` |
| Docs, README, comments, examples | `documentation` |
| Refactor, cleanup, dead-code removal | `refactor` *(create if missing — see step 4)* |
| Test-only work | `tests` *(create if missing)* |

One primary label per issue. Add `ai` (existing repo label) to every issue
this skill creates, so they're easy to filter later.

### 4. Ensure labels exist

Before creating issues, ensure the labels you're about to apply exist. Run:

```bash
gh label list --limit 200 --json name -q '.[].name'
```

For each label you need that's missing, create it:

```bash
gh label create refactor --color "fbca04" --description "Code refactor / cleanup"
gh label create tests    --color "0e8a16" --description "Test-only changes"
```

Do NOT recreate existing labels; `gh label create` errors on duplicates.

### 5. Create the milestone

Name the milestone from the plan's headline goal, kept short (≤50 chars).
Example: "Convctl streaming refactor", "Marketplace publish v2".

```bash
gh api repos/:owner/:repo/milestones \
  --method POST \
  -f title="<milestone title>" \
  -f description="<one-line summary of the plan>" \
  -f state="open" \
  --jq '.number'
```

Capture the returned milestone number — child issues need it.

If a milestone with the same title already exists, reuse it: list with
`gh api repos/:owner/:repo/milestones -q '.[] | {number,title}'` and pick
the matching `number`.

### 6. Create child issues

For each task, in dependency order, create an issue with the full body
template below. Capture each returned issue number — the epic will link them.

```bash
gh issue create \
  --title "<type>: <short imperative title>" \
  --label "<primary-label>,ai" \
  --milestone <milestone-number> \
  --body-file /tmp/plan-to-issues-child-N.md
```

Title format: `<type>: <imperative summary>` where `<type>` is one of
`feat`, `fix`, `docs`, `refactor`, `test`, `chore` — matches the repo's
commit convention (see `.claude/skills/commit/SKILL.md`).

Write the body to a temp file first (bodies are long and multiline). Use
this template verbatim, filling every section — do not omit sections; if a
section is genuinely N/A, write `_None._` so the reader knows it was
considered.

```markdown
## Goal

<One-sentence outcome. What does "done" look like from the outside?>

## Context

<Why this task exists. Link back to the plan / conversation motivation.
If it's part of a larger effort, state that here. Include background a
fresh coding agent would not otherwise have.>

## Scope

**In scope:**
- <specific change 1>
- <specific change 2>

**Out of scope:**
- <thing that might look adjacent but is a separate task>

## Files / areas to touch

- `path/to/file.go` — <what changes here>
- `path/to/other.md` — <what changes here>

<If the exact files aren't known yet, list the directory or module and mark
it `> ⚠ Assumption: agent to confirm exact files during discovery.`>

## Acceptance criteria

- [ ] <observable behaviour 1>
- [ ] <observable behaviour 2>
- [ ] <docs / changelog updated if applicable>
- [ ] <existing tests still green>

## Verification

<Exact commands the agent should run before opening a PR. Prefer the
project's canonical commands from CLAUDE.md.>

```bash
cd plugins/corezoid/mcp-server
go build ./...
go vet ./...
go test -race ./...
```

## Dependencies

- Blocked by: #<issue-number-or-none>
- Blocks: #<issue-number-or-none>
- Related: #<issue-number-or-none>

## Notes

<Optional. Edge cases, gotchas, links to node-structures.md sections,
lint rules, prior PRs. Anything a coding agent would ask before starting.>
```

Fill `Blocked by` on later issues by referencing earlier child numbers you
already captured. `Blocks` and `Related` can be filled after all children
exist — patch the body in a second pass if needed:

```bash
gh issue edit <N> --body-file /tmp/plan-to-issues-child-N.md
```

### 7. Create the parent epic

After all children exist, create the parent epic. Its body is a task list of
child issues — GitHub renders `- [ ] #N` as a live tracked sub-issue.

```bash
gh issue create \
  --title "Epic: <plan headline>" \
  --label "enhancement,ai" \
  --milestone <milestone-number> \
  --body-file /tmp/plan-to-issues-epic.md
```

Epic body template:

```markdown
## Goal

<One-paragraph description of the plan and the outcome once all sub-tasks land.>

## Motivation

<Why now, who asked, what problem this solves. Pull from the conversation.>

## Sub-tasks

- [ ] #<child-1>
- [ ] #<child-2>
- [ ] #<child-3>

## Milestone

Tracked under milestone: **<milestone title>**.

## Notes

<Optional. Cross-cutting concerns, rollout order, feature flags, risk.>
```

### 8. Back-link children to the epic (best-effort)

For each child, append `Part of #<epic-number>` to the top of its body so
the linkage is visible on the child page too. Use `gh issue edit --body-file`.
Skip if this would double the work for a plan with only 2–3 children.

### 9. Report to the user

Output a compact summary — no fluff:

```
Epic: #<epic-number> <title>
Milestone: <title> (#<milestone-number>)
Children:
  #<n> <type>: <title>
  #<n> <type>: <title>
  ...
```

Include full URLs only if the user asked for them explicitly.

## Guardrails

- **Do not** open PRs, push branches, or run any code changes — this skill
  only files work items.
- **Do not** assign issues to users unless the user in this session named a
  specific GitHub handle to assign.
- **Do not** close or edit existing unrelated issues.
- **Do not** delete milestones or labels, even if they look stale.
- If `gh` returns an error (rate limit, permission, network), stop, report
  what succeeded and what didn't, and let the user decide whether to retry.
  Do not retry destructive operations silently.

## Cleanup

Delete `/tmp/plan-to-issues-*.md` scratch files after successful creation.
Leave them if any step failed, so a retry can reuse them.
