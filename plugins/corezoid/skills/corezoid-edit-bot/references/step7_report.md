Loaded only when executing Step 7 of `corezoid-edit-bot`, after Step 6 verification passed.

## Step 7: Update PLAN.md and report

1. **Apply `plan_impact`** — edit PLAN.md's `Commands`, `Coverage`,
   `Localization keys`, `Attachments`, `Menu wiring`, `locales` and
   `template_ids` to match what now exists, and `bot-contract.json` if this
   change re-derived a contract. PLAN.md describes the live orchestrator; a
   stale plan makes the next edit guess, and a stale `Coverage` table is how a
   process quietly stops being served.
2. Leave CHANGE.md in place as the record of this change. The next `plan` run
   overwrites it.
3. Report in one compact block:
   - What changed — processes created/pushed/paused/deleted, aliases created,
     tasks written, env vars touched.
   - **Aliases left dangling**, and what still resolves because of it.
   - **Smoke-test results** — one row per command exercised, with the observed
     outcome and, for anything red, the node the task parked at. Include the
     Send Message render for every touched `text_id`.
   - Regression result, and its scope.
   - Any waived gate (`allow_no_snapshot`, `overwrite_server_change`) and why.
   - Anything the user must do by hand (delete an alias through the API,
     register a webhook, translate a string you could not).
   - `folder_url` for convenience.

