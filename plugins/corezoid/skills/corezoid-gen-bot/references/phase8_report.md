Loaded only when executing Phase 8 of `corezoid-gen-bot`, after Phase 7 verification passed.

## Phase 8 — APPROACH.md, then report

`APPROACH.md` next to PLAN.md, covering: the contract table; the architecture
(messenger → channel receiver → Main → Router → command process → `api_rpc` →
domain process, and back through `Send Message`); the command table
(`/command` → alias → processes → dialog steps → outcomes); where copy and
keyboards live and how to change them; the alias rule and what breaks when it is
violated; the namespacing rule; known limitations. Full derived contracts as an
appendix. No tokens, no chat ids of real people, no session-specific paths.

Then report, in one compact block:

- **Orchestrator** — `folder_url` and folder id.
- **Channels** — one line each. For every entry in `webhooks_url`, the URL the
  user must register with that platform by hand. Report exactly the channels the
  wizard listed — it says nothing about the ones it omitted, so do not claim
  those are already wired.
- **Commands** — table `/command | alias | processes | wiring | shape`.
- **Coverage** — handed-in processes used / total, and any left out with why.
- **Verification** — one row per command with the observed L2/L3 result, and the
  node any failing task parked at. Never report a command as working on the
  strength of a clean lint.
- **Dashboard** — `dashboard_url` if the wizard returned one.
- **What the user must do by hand.**
- **Next step** — `/edit-bot` for changes; a second `corezoid-gen-bot execute`
  would build a whole second orchestrator.

