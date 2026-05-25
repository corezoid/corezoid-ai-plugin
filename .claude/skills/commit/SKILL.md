---
name: commit
description: >
  Project commit helper for corezoid-ai-plugin. Use instead of the generic commit
  skill whenever the user says "/commit", "commit", "закоммить", "сделай коммит",
  or asks to commit changes in this repository.
---

# Commit with auto version bump

This project follows semver. **Every commit must include a version bump** in:
- `plugins/corezoid/.claude-plugin/plugin.json`
- `.claude-plugin/marketplace.json`

## Step 1 — Inspect changes

Run in parallel:
- `git diff HEAD` — see what changed
- `git status` — see untracked files
- `git log -5 --oneline` — see recent commit style

## Step 2 — Determine bump type

| Change type | Bump |
|---|---|
| New feature, new tool, new skill, new node type | **minor** (2.3.x → 2.4.0) |
| Bug fix, doc update, text fix, refactor | **patch** (2.3.x → 2.3.x+1) |
| Breaking change, major redesign | **major** (2.x.x → 3.0.0) |

Read current version from `plugins/corezoid/.claude-plugin/plugin.json`.

## Step 3 — Update versions

Update the `"version"` field in **both** files to the new version:
- `plugins/corezoid/.claude-plugin/plugin.json`
- `.claude-plugin/marketplace.json`

## Step 3.5 — Update CHANGELOG.md

Prepend a new entry at the top of `CHANGELOG.md` (right after the `# Changelog` heading) following the existing format:

```
## [<new_version>]

- <bullet summarising what changed — one line per logical change>
```

Derive the bullets from the staged diff: group related changes into a single readable line each. Keep it concise — one sentence per item.

## Step 3.6 — Update README.md

Review the diff for changes that affect README content and update the relevant sections in `README.md`:

| What changed | What to update in README.md |
|---|---|
| New or removed skill (`SKILL.md` added/deleted) | **What it does** table — add/remove the row |
| New or removed MCP tool | **MCP Tools** table — add/remove the row; **Architecture** code block if the tool group changes |
| New plugin directory or top-level file | **Project structure** code block |
| Breaking change to install/config | **Installation** or **Configuration** section |

If none of the above apply (e.g. pure bug fix, refactor, doc-only change inside an existing skill), skip this step — do not touch README.md.

## Step 4 — Stage and commit

Stage all modified files (including the two version files and `CHANGELOG.md`) and create a commit.

Follow the project's commit message convention:
```
<type>(<scope>): <short description>, bump to <new_version>
```

Types: `feat`, `fix`, `docs`, `chore`, `refactor`

End the commit message with:
```
Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
```
