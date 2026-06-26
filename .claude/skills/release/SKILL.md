---
name: release
description: Project release helper for corezoid-ai-plugin. Prepares a new tagged release end-to-end. Use this skill whenever the user says "release", "релиз", "новый релиз", "сделай релиз", "выпусти версию", "bump version", "обновить версию", "tag a release", "/release", or anything that implies cutting a new version of this plugin. Walks the user through six explicit phases: (1) compare `main` with the latest git tag and summarise what changed, (2) ask which new version to publish, (3) draft a CHANGELOG.md entry in the existing format, (4) sync that version across all four manifest files (`.claude-plugin/marketplace.json`, `plugins/corezoid/.claude-plugin/plugin.json`, `plugins/corezoid/.codex-plugin/plugin.json`, `.agents/plugins/marketplace.json`), (5) show the user the full proposed change set and wait for explicit confirmation, (6) commit on the current branch and create the matching `vX.Y.Z` tag. Always use this skill instead of running release steps manually — it keeps the four manifests in lock-step, formats the changelog consistently, and prevents partial releases.
---

# Release skill

This skill prepares a new tagged release of `corezoid-ai-plugin`. It runs through six phases in order. Do not skip phases, and do not collapse them — the user expects to see and approve each one before the next begins.

## Why a dedicated skill

A release of this plugin touches four separate manifest files plus the changelog. Forgetting any one of them ships a broken or inconsistent release: marketplace listings disagree about the version, Codex installs the wrong build, or the changelog drifts from what was actually tagged. The single source of release truth for this repo is `RELEASE_CHECKLIST.md` — this skill executes that checklist programmatically so nothing is missed.

## The six phases

### Phase 1 — Inspect what changed since the last tag

Goal: understand and summarise everything that has landed since the previous release.

Run, in parallel:

```bash
git fetch --tags --quiet
git describe --tags --abbrev=0                         # the last released tag, e.g. v2.3.9
git log <last-tag>..HEAD --pretty=format:'%h %s'       # commit subjects since that tag
git diff --stat <last-tag>..HEAD                       # files touched, magnitude of change
git status --short                                     # uncommitted work that may need to ship in this release
```

Then read the actual diff for any commit subjects that are vague or for any non-trivial file changes — commit subjects alone often understate what shipped. Pay particular attention to:

- New skills under `plugins/corezoid/skills/` (added directories).
- MCP server changes in `plugins/corezoid/mcp-server/` (new tools, schema changes, breaking behaviour).
- Docs changes in `plugins/corezoid/docs/` and root-level docs (`README.md`, `SECURITY.md`, `CLAUDE.md`).
- CI / workflow changes in `.github/`.

If `git status` shows uncommitted changes, ask the user whether those should be part of this release or stay out of it. Don't assume — pending edits are sometimes the whole point of the release and sometimes unrelated work.

Output to the user: a short bulleted summary grouped by category, the kind of bullets that would end up in the changelog. Use the same vocabulary the existing CHANGELOG.md uses: `Feat:`, `Fix:`, `Docs:`, `Chore:`, `CI:`, `Security:`, `Refactor:`. Keep each bullet to one line.

### Phase 2 — Ask which new version to publish

Goal: get the new semantic version from the user.

Use `AskUserQuestion` with the **current** version pulled from `plugins/corezoid/.claude-plugin/plugin.json` and three suggested bumps:

- Patch (`X.Y.Z+1`) — bug fixes, docs, internal cleanup only.
- Minor (`X.Y+1.0`) — new skills, new MCP tools, additive features.
- Major (`X+1.0.0`) — breaking changes to manifests, MCP tool schemas, or skill contracts.

Recommend a level based on Phase 1 (e.g. if a new skill directory appeared, suggest a minor bump). Always allow the user to override with a custom version via the "Other" answer.

The version the user picks is the only version that should appear anywhere downstream in this skill run. Store it once and reuse it — do not re-derive it.

### Phase 3 — Draft the CHANGELOG.md entry

Goal: produce a new section at the top of `CHANGELOG.md`, matching the existing format exactly.

Read `CHANGELOG.md`. The format is rigid:

```
## [X.Y.Z]

- Feat: …
- Fix: …
- Docs: …
```

Rules:

- The new section goes immediately after the top-level `# Changelog` heading, **before** the previous version.
- Use the same category prefixes seen in prior entries. Don't invent new ones unless the change genuinely doesn't fit (e.g. `Refactor:` is fine, `Stuff:` is not).
- One bullet per logical change, not one bullet per commit. Squash related commits.
- Write in the past tense, imperative-ish style matching prior entries ("add", "fix", "remove" — not "added", "fixes").
- Drop trivia: bumps of internal version numbers, merge commits, formatting-only changes. The changelog is for users of the plugin, not for git archeologists.

Show the drafted entry to the user before writing it to disk. They will often want to reword a bullet or merge two of them — that's expected.

### Phase 4 — Sync the version across all four manifests

Goal: the new version appears in exactly four places, all in agreement.

The files and the field to update:

| File | Field to edit |
| --- | --- |
| `plugins/corezoid/.claude-plugin/plugin.json` | top-level `"version"` |
| `plugins/corezoid/.codex-plugin/plugin.json` | top-level `"version"` |
| `.claude-plugin/marketplace.json` | `plugins[0].version` |
| `.agents/plugins/marketplace.json` | `plugins[0].version` |

Use the `Edit` tool with `old_string` containing the full `"version": "..."` line so the replacement is unambiguous. Do not rewrite the whole file. Do not touch any other field — license, description, paths must stay as-is.

After editing, verify with a single grep:

```bash
grep -n '"version"' \
  plugins/corezoid/.claude-plugin/plugin.json \
  plugins/corezoid/.codex-plugin/plugin.json \
  .claude-plugin/marketplace.json \
  .agents/plugins/marketplace.json
```

All four lines must show the new version. If any one disagrees, fix it before moving on — never proceed to commit with mismatched manifests.

Then validate that the JSON still parses:

```bash
python3 -m json.tool .claude-plugin/marketplace.json >/dev/null
python3 -m json.tool .agents/plugins/marketplace.json >/dev/null
python3 -m json.tool plugins/corezoid/.claude-plugin/plugin.json >/dev/null
python3 -m json.tool plugins/corezoid/.codex-plugin/plugin.json >/dev/null
```

### Phase 5 — Confirm with the user

Goal: nothing is committed without explicit go-ahead.

Show the user:

1. The new version number.
2. The CHANGELOG.md entry as it will be written.
3. A `git diff --stat` of all currently staged/unstaged changes (including any pre-existing edits from Phase 1 that they confirmed should ship).
4. The exact commit message and tag name you intend to create.

Then use `AskUserQuestion` with options like "Proceed with commit and tag" / "Let me edit something first". Do not proceed without an affirmative answer. If the user wants to tweak the changelog or change the version, loop back to the relevant phase rather than improvising in place.

### Phase 6 — Commit and tag

Goal: a single release commit plus a matching annotated git tag on the current branch.

Stage only the files this skill touched plus any files the user confirmed should ship:

```bash
git add CHANGELOG.md \
        plugins/corezoid/.claude-plugin/plugin.json \
        plugins/corezoid/.codex-plugin/plugin.json \
        .claude-plugin/marketplace.json \
        .agents/plugins/marketplace.json
# Plus any other files the user explicitly approved in Phase 1.
```

Avoid `git add -A` / `git add .` — there may be unrelated untracked files (samples, local docs, IDE state) that must not enter the release commit.

Commit message format (matches recent release commits in this repo):

```
chore(release): vX.Y.Z

- Brief one-line summary of the biggest change.
- Optional second bullet if there's another standout.
```

Use a HEREDOC for the commit so multiline formatting is preserved. Do not add Claude co-author trailers to release commits — these are public and authored by the maintainer.

Then create the tag:

```bash
git tag -a vX.Y.Z -m "Release vX.Y.Z"
```

Use the **annotated** form (`-a`) so `git describe` and GitHub Releases pick it up correctly. The unannotated `git tag vX.Y.Z` form will work but is inferior.

Do **not** push automatically. After the commit and tag are created, tell the user exactly what to run to push:

```bash
git push origin <current-branch>
git push origin vX.Y.Z
```

This deliberate pause is a safety net — pushing the tag triggers `release.yml` and a public GitHub Release, which is hard to undo. Let the user do that last step themselves.

## Things to watch for

- **Wrong branch.** Releases typically come from `main`. If the current branch is something else (e.g. `develop`), surface this in Phase 5 so the user can decide whether to merge first or release from where they are.
- **Tag already exists.** Before Phase 6, run `git tag -l vX.Y.Z`. If it returns the tag, stop — the version is already taken. Ask the user to pick a different one and loop back to Phase 2.
- **Unrelated untracked files.** The repo often has work-in-progress files (`comparison.html`, `plan.html`, sample `.conv.json` files, IDE config). These must not enter the release commit unless the user explicitly says they should.
- **Manifest drift in the diff.** If Phase 1 shows a manifest file already changed by a previous (incomplete) attempt, treat that as suspect — re-read all four manifests before Phase 4 and reconcile from a known state, don't blindly bump on top of a half-finished bump.
- **CHANGELOG.md already has an entry for the chosen version.** That means a previous release attempt was partially completed. Show the existing entry to the user and ask whether to extend it, replace it, or pick a different version.

## When the user wants a one-shot, no-questions release

If the user explicitly says something like "just release a patch, no questions" or "автоматический релиз патча", you can fold Phase 2 (pick patch bump) and Phase 5 (confirmation) into a single approval at the end, but never skip showing the proposed changelog entry and the proposed version. The four-manifest sync is non-negotiable regardless of speed mode.
