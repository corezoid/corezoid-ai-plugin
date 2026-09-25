Loaded only when executing Phase 5 of `corezoid-gen-bot`. Assumes Phase 4 already built the orchestrator and `template_ids` is populated.

## Phase 5 — Generate one process per command

Per entry in PLAN.md `Commands`:

1. **Create the process on the server first, then pull it.** `push-process`
   **cannot create** a process: a local file with `obj_id: 0` is rejected with
   `Project or stage mismatch`, and once the process exists a push without a
   recorded baseline is refused too ("no pull baseline"). The working order is:
   ```
   create-process  process_name: /{command}  folder_id: {template_ids.bots_folder}
   pull-process    process_id: <the id it returned>      # records the baseline
   <write the generated scheme into the pulled file, keeping its obj_id>
   ```
   `adopt_existing` is **not** the flag for this — it declares you do not know
   what is on the server, whereas here you created the empty process seconds ago.
   Keep the filename `create-process`/`pull-process` produced; never rename it.

2. **Start from the right skeleton** — `bot_reply.skeleton.json` for a
   single reply, `bot_dialog.skeleton.json` for anything with a question. For a
   multi-step dialog repeat the ask → wait → keep → validate block per input;
   for a chained command splice in `rpc_call.nodes.json`; for a reply-less
   callee use its `_fire_and_forget` node; for a state read use `_state_read`.

3. **Wire the domain call** — `api_rpc` with `group: ""` and an explicit `extra`
   holding only the declared inputs, `extra_type` from the majority-voted types,
   a **`time` semaphore of at least 30 s** routed to the timeout node, and
   `@alias` in preference to a numeric `conv_id` (an alias survives a
   `deploy-stage` promotion; a numeric id does not).

4. **Namespace when the command makes 2+ calls.** An `api_rpc` callee's
   `res_data` merges into the caller's task at top level, and every process in
   this family replies with the same `{result, code}` envelope — so the second
   callee overwrites the first's verdict and the command branches on the wrong
   outcome. Follow every call with an **`api_code`** (not a `set_param` — lint
   cannot see a `data.result` read inside JavaScript and fails the file with
   `UNUSED SET_PARAM`) that copies the reply into namespaced keys and clears
   `result`/`code`. With exactly one call there is nothing to overwrite; read
   `{{result}}`/`{{code}}` directly and skip it.

5. **Forward every value the message needs — `group:""` sends nothing else.**
   The `api_copy` into Send Message carries **only the fields named in its
   `data`**, and Send Message resolves `{{someVar}}` in the localized text against
   **its own** task data. So a value the command computed but did not forward
   renders as the literal `{{someVar}}` in the chat. Two families of this:

   - **Values a text interpolates.** `balanceDone: "Ваш баланс: {{bonusAmount}}"`
     requires `"bonusAmount": "{{bonusAmount}}"` in the send node's `data`.
     Cross-check every `text_id` the command uses against the `{{...}}` in its
     Localization string and forward each one.
   - **`items` + `currentPage` for any dynamic attachment.** Without them
     `createDynamicAttachment` has nothing to render and the carousel arrives
     empty. Its gate is `items == "" && buttons == ""` → static path, so a command
     with **no rows must send `items` as the empty STRING, not `[]`**: an empty
     array takes the dynamic path and re-renders a static keyboard's `buttons` as a
     per-item pattern zero times, delivering a keyboard with no buttons at all.

   Neither failure is visible to `lint-process`, and neither is visible to a test
   that inspects the command's own task data — the value is present there and
   missing only on the far side of the `api_copy`. See Phase 7 L1.6.

6. **Substitute every `{{UPPER_CASE}}` placeholder** from PLAN.md and
   `template_ids`: `{{COMMAND}}`, `{{DOMAIN_CONV_ID}}`, `{{DOMAIN_TITLE}}`,
   `{{ARG_NAME}}`, `{{ARG_SOURCE}}`, `{{SUCCESS_TEXT_ID}}`,
   `{{ALTERNATE_TEXT_ID}}`, `{{ASK_TEXT_ID}}`, `{{ASK_ATTACHMENT_ID}}`,
   `{{ANSWER_REGEX}}`, `{{PROFILE_FIELD}}`, `{{SEND_MESSAGE_CONV_ID}}`,
   `{{ROUTER_CONV_ID}}`, `{{USER_PROFILE_CONV_ID}}`, `{{BOTS_FOLDER_ID}}`,
   `{{TEMPLATE_USER_ID}}`. Note that a `{{UPPER_CASE}}` inside an `api_code`
   `src` is also a generator-time substitution — Corezoid does **not**
   interpolate `{{...}}` in Code nodes. Grep for `{{[A-Z]` before pushing: a
   leftover placeholder deploys happily and fails at runtime.

   **Two of them are numeric — drop the surrounding quotes.** The skeletons
   ship `"parent_id": "{{BOTS_FOLDER_ID}}"` and
   `"user_id": "{{TEMPLATE_USER_ID}}"` quoted only so the template files stay
   parseable JSON. The schema declares the process's `parent_id` as
   `null|integer` and `api_copy`'s `user_id` as `integer`, so a plain textual
   substitution leaves a string and `push-process` fails schema validation on
   `parent_id` and on **every** `api_copy` node:

   ```
   - at '/parent_id': got string, want null or integer
   - at '/scheme/nodes/3/condition/logics/0/user_id': got string, want integer
   ```

   `force` does not bypass schema validation, so this has to be right before
   the first push. Every other placeholder above substitutes as a string:
   `conv_id` accepts both forms, and `text_id`/`attachment_id` are strings.

7. **Assign unique node ids** — 24 hex characters, unique within the process.
   The skeletons ship readable placeholders (`aa0000…01`) which are valid but
   collide across processes; regenerate per process. `push-process` rewrites
   them canonically anyway.

8. **Push:**
   ```
   layout-process  process_path: <path>     # tidy coordinates; rewrites only x/y
   lint-process    process_path: <path>     # must be clean of deploy-blocking findings
   push-process    process_path: <path>
   ```
   Fix every deploy-blocking finding in the design. Do **not** pass
   `force=true`: the structural findings this generator can plausibly trip
   (missing default `go`, a shared error cluster, a sub-30 s Delay, an
   `err_node_id` pointing at an `obj_type:0` node, a self-referencing
   `api_copy`) describe a graph the server rejects, and `force` does not bypass
   them.

   **Expect the first push of each new command to be blocked on the snapshot,
   and expect that to be normal.** `push-process` takes a pre-push snapshot, and
   `CreateSnapshot` fails **deterministically for a process that has never been
   deployed** — there is no version to snapshot. The message reads like a
   transient platform fault ("please contact support"), so it invites a retry
   that cannot succeed. Retry once to rule out a real outage, then pass
   `allow_no_snapshot=true` for that push only, having first confirmed from the
   recorded baseline that the version being overwritten has **zero nodes** (it
   does: `create-process` made it seconds earlier). Say so in the report. Every
   *subsequent* push of the same process snapshots normally — if one of those is
   blocked, that is a genuine outage and the answer is to wait, not to waive.

9. **Create the alias — this is the wiring, not a nicety.**
   ```
   create-alias  process_path: <path>  short_name: {command without the slash}
   ```
   Without it the Router's `@{{commandAlias}}` resolves to nothing and the user
   gets `commandNotFound`. Verify the `short_name` is exactly the command minus
   its slash, matches `^[a-z0-9][a-z0-9-]{2,}$`, and collides with nothing already
   in the stage. **`_ALIASES_.json` at the stage root is the full list** — read it
   rather than guessing; a fresh orchestrator ships ~60 aliases.

   While you are in that file, **check the aliases you plan to *call* too.** An
   alias whose name matches a handed-in process may point somewhere else entirely:
   in the reference workspace `promolist`, `recovery-password` and
   `registration-verify-success` all resolved to deprecated `..._old/API:` copies,
   not to the processes the user handed in. Match on `obj_to_id`, never on the
   name, before using `@alias` for a domain call.

10. **End with `text_id: ""` and a non-empty `attachment_id`, or the reply is
   sent twice.** The Router's `/end` handler decides whether *it* also sends:

   | `text_id` | `attachment_id` | Router does |
   |---|---|---|
   | `""` | `""` | sends `mainMenu` |
   | set | `""` | sends `text_id`, adds `mainKeyboard` |
   | set | set | sends `text_id` |
   | `""` | set | **closes the state and sends nothing** |

   `group:"all"` forwards the whole task, so a command that set its own
   `data.text_id` leaks it into `/end` and the Router sends that text a second
   time — and the Router's send passes only
   `channel`/`chat_id`/`text_id`/`attachment_id`, so the duplicate arrives with no
   interpolated value and no `items`. The generated node must therefore be:
   ```jsonc
   {"type":"api_copy","conv_id":<router_id>,"mode":"create","group":"all",
    "data":{"command":"/end","text_id":"","attachment_id":"mainKeyboard"}}
   ```
   The wizard's sample bots take the **other** half of this contract: `/NPS`,
   `/sampleSurvey`, `/changeLanguage` and friends send no message of their own and
   hand `text_id` to the Router. That path is unavailable to any command that needs
   `items`/`currentPage` or an interpolated value, because the Router's send cannot
   carry them — which is why generated commands send their own message and silence
   the Router. (`/exchangeRates`, the one dynamic-attachment sample, dodges the
   issue by never copying `/end` at all and staying `active` forever. Do not copy
   that: it leaves the chat wedged.)

11. **Re-read the file after pushing.** The server regenerates node ids and
   rewrites the local file. Reference nodes by title, never by a remembered id.

