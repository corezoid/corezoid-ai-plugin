Loaded only when executing Step 6 of `corezoid-edit-bot` (or Step 6b, deploy mode), after the change has been pushed.

## Step 6: Verify

Run the CHANGE.md verification checklist, in this order, stopping at the first
failure. `references/repair_loop.md` maps a symptom to the layer at fault.

1. `grep -rnE '\{\{[A-Z_]+\}\}'` over touched processes — empty.
2. `grep -rniE 'bot[0-9]{6,}:|page_access_token|viber_token|abc_token'` over
   the whole tree including PLAN.md/CHANGE.md — empty.
3. `lint-process` clean on every touched process.
4. `show-task process_id: {template_ids.localization} ref: localization` — every
   `text_id` any touched process
   references exists in **every language the wizard seeded**, and every
   `{{placeholder}}` in it is a key the sending process actually produces
   (`success.keys` in `bot-contract.json`). Also: `{{t'key}}` with no dot, and
   no dotted `{{a.b}}` anywhere — the interpolation regex is `/{{\w+}}/ig` and
   `\w` excludes `.`, so a dotted placeholder reaches the chat verbatim.
5. `show-task process_id: {template_ids.attachments} ref: <attachment_id>` per
   touched attachment — one key per channel in `channels`,
   remembering the naming asymmetry: the wizard's messenger key is
   `fbmessenger`, but Send Message dispatches on `channel == "facebook"`, so
   attachment keys and `go_if_const` conditions use `facebook`. A check written
   against `fbmessenger` passes while the channel renders nothing.
6. **Forwarding check — the one nothing else catches.** For every Send Message
   `api_copy` in every touched process, take the `text_id` it sends, look up
   that string in Localization, and confirm each `{{var}}` in it is a key of
   **that node's `data`**; then confirm every node whose `attachment_id` is a
   dynamic pattern also forwards `items` and `currentPage`. Do this as a script
   over the schemes, not by eye — `show-task` returns hundreds of lines of
   template copy with no way to diff it against the graph. Two shapes need care:
   a node sending the literal `{{text_id}}` can carry any `text_id` a Code node
   assigns, so union them (and take a ternary's strings from after the `?`); and
   where the union is wider than a branch can reach, forward the extra values
   anyway — it costs a few fields and keeps the invariant true if a branch is
   ever repointed.
7. **`/end` check.** Every `END -> Router` `api_copy` carries `text_id: ""`
   **and** a non-empty `attachment_id`, or the reply is delivered twice — the
   second copy stripped of every interpolated value
   (`references/invariants.md` §2).
8. **The command on its own** — `run-task` at the touched command with
   `{"channel":"telegram","chat_id":"<test id>","message":{"type":"text","text":"/<command>"}}`.
   Assert the domain `api_rpc` returned, the namespaced keys are populated where
   CHANGE.md says namespacing is required, `text_id` was set, and the task
   reached `END -> Router`. A dialog parks on its wait node here **by design** —
   that is the `api_callback` working; assert it *reached* the node, then deliver
   the answer the way the Router does (an `api_copy` `mode:"modify"` on ref
   `<channel>_<chat_id>`, or `modify-task` on the same ref) and confirm it
   advances. A task parked forever on the domain call means the callee is paused
   or reply-less: that is a contract misread, not a wiring bug — go back to
   `/corezoid-gen-bot refresh`. Inspect with `list-task-history` when a task did
   not go where you expected.
9. **Render the message the way the user receives it.** Step 8 proves the
   command computed the right things; it does **not** prove the user sees them.
   The task data at `Done` will happily show
   `bonusAmount: "0.00", text_id: "balanceDone"` while the delivered message
   reads `Your card has {{bonusAmount}} bonus points.`, because the defect lives
   in the `api_copy`'s `data` block. So per distinct `text_id` this change
   touched, `run-task` on **Send Message** itself with
   `{channel, chat_id, text_id, attachment_id}` plus the values the text
   interpolates, and assert on `data.text` and `reply_markup`. With a synthetic
   `chat_id` the Telegram call fails with `Bad Request: chat not found` — that is
   expected and fine, because the text and attachment are resolved *before* the
   send. **For a `copy` or `keyboard` change this is the primary test**, not the
   Router run.
10. **The smoke test from CHANGE.md**, observed, not assumed:
    ```
    run-task  process_path: <Router path>  wait_sec: 60 \
              data: {"channel":"telegram","chat_id":"<test chat id>","command":"/order-status","message":{"type":"text","text":"/order-status"}}
    ```
    This is the only layer that proves the alias dispatch. `Command not found`
    means the alias is wrong or missing. Then `show-task` on the command process
    (ref `telegram_<chat_id>`) to confirm it started. Walk every alternate
    outcome you can trigger — those are what a happy-path-only test misses.
11. **Regression**, per `regression_scope`: at least one command this change did
    not touch, or **every** command for a shared Localization/Attachments key or
    a `template-edit` — that is what "shared by all commands and all four
    channels" means.
12. **Live check** where the user can do one. `run-task` proves the graph; only a
    real client proves the webhook, the token and the rendered keyboard.

A clean lint is not a passing test. Never report a command as working without an
observed run behind it.

### Step 6b: deploy mode

```
deploy-stage  project_id: <int>  source_stage_id: <int>  target_stage_id: <int> \
              company_id: "<workspace id, string>"
              # apply defaults to false — dry run, shows the diff and conflicts
```

Show the user the diff. Only then, with their confirmation of the exact
source→target:

```
deploy-stage  … apply: true  confirm: "<source_stage_id>-><target_stage_id>"
```

Destructive, and irreversible on an immutable target. After it lands, re-run the
Step 6 smoke test **against the target stage**: aliases and env vars are
stage-scoped and are not migrated by the merge, the target's Localization and
Attachments tasks are runtime data that a scheme merge does not carry, and the
domain processes the bot calls may not exist there at all. A command carrying a
**numeric** `conv_id` promotes into a stage where that id means something else
or nothing — which is why domain calls use `@alias`, and why this check exists.

