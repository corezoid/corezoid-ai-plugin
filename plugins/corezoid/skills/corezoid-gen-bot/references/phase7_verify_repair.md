Loaded only when executing Phase 7 of `corezoid-gen-bot`. Assumes Phases 4–6 already ran — the orchestrator is built, commands generated, tasks seeded.

## Phase 7 — Verify, and repair

Four layers, cheapest first. Stop at the first failure in a layer before moving
on.

### L1 — Static

1. `grep -rnE '\{\{[A-Z_]+\}\}'` over generated processes — empty.
2. `grep -rniE 'bot[0-9]{6,}:|page_access_token|viber_token|abc_token'` over the
   whole tree including PLAN.md and APPROACH.md — empty.
3. `lint-process` clean on every generated process.
4. `show-task ref: localization` — every `text_id` a generated process
   references exists in every locale in `locales`, and every `{{placeholder}}`
   in a success text is a key in that process's `success.keys`. A missing key
   renders as an empty message or literal `{{key}}`, not an error.
5. `show-task` per `attachment_id` — one key per channel in `channels`.
6. **Forwarding check — the one L2/L3 cannot see.** For every Send Message
   `api_copy` in every generated process, take the `text_id` it sends, look up
   that string in Localization, and confirm **each `{{var}}` in it is a key of
   that node's `data`**. Keep the payload you seeded in Phase 6 in a local file
   and run this as a script over the generated schemes: `show-task` returns the
   whole Localization document (hundreds of lines of template copy) with no way
   to diff it against the schemes, so doing this by eye is
   how a missing forward survives once a bot has more than a couple of
   commands. Two shapes need care in such a script: a node
   that sends the literal `{{text_id}}` can carry **any** `text_id` a Code node
   assigns, so union them (and take a ternary's strings from after the `?`, or
   you will read a condition's string as a text_id); and where the union is wider
   than a branch can actually reach, forward the extra values anyway rather than
   arguing the branch is safe — it costs a few fields and keeps the invariant
   true if a branch is ever repointed. Then confirm every node whose `attachment_id` is a
   dynamic pattern also forwards `items` and `currentPage`. A value that is
   present on the command's task but absent from the `data` block renders as a
   literal `{{var}}`, and L2/L3 will still pass because they read the command's
   task, not the message.
7. **`/end` check.** Every `END -> Router` `api_copy` carries
   `text_id: ""` **and** a non-empty `attachment_id`, or the reply is delivered
   twice (Phase 5 §10).

### L2 — The command process on its own

`run-task` straight at the generated command, with the data the Router would
supply:

```jsonc
{"channel":"telegram","chat_id":"<test chat id>","message":{"type":"text","text":"/balance"}}
```

Assert: the domain `api_rpc` was reached and returned; the namespaced keys are
populated where PLAN.md says namespacing is required; `text_id` was set to the
mapped value; the task reached `END -> Router`. Inspect with
`list-task-history` when it did not.

> **A dialog command will park on its wait node here, by design.** That is the
> `api_callback` doing its job, not a failure. Assert it *reached* the wait
> node, then deliver the answer the way the Router does — an `api_copy`
> `mode:"modify"` into the command process on ref `<channel>_<chat_id>` — and
> confirm it advances. `modify-task` on that ref does the same job.

> **L2 proves the command computed the right things; it does NOT prove the user
> sees them.** The task data at `Done` will happily show
> `bonusAmount: "0.00", text_id: "balanceDone"` while the delivered message reads
> `Your card has {{bonusAmount}} bonus points.`, because the defect lives in the
> `api_copy`'s `data` block. To close that gap, **render one message per distinct
> `text_id` the way Send Message does**: `run-task` on Send Message itself with
> `{channel, chat_id, text_id, attachment_id}` plus the values the text
> interpolates, then read `data.text` off the resulting task. With a synthetic
> `chat_id` the Telegram call fails with `Bad Request: chat not found` — that is
> fine and expected, because the text and attachment are resolved *before* the
> send. Assert on `data.text` and on `reply_markup`.

### L3 — Through the Router

This is the layer that proves the alias dispatch, and nothing else does:

```jsonc
{"channel":"telegram","chat_id":"<test chat id>","command":"/balance",
 "message":{"type":"text","text":"/balance"}}
```

`run-task` on the Router with `wait_sec: 60`. Reaching `Command not found`
means the alias is wrong or missing. Then `show-task` on the command process
(ref `telegram_<chat_id>`) to confirm it started.

Walk **every** command, and every alternate outcome you can trigger — those are
the ones a happy-path-only test misses.

### L4 — A real client

Send the command from a real Telegram/Viber client where the user can.
`run-task` proves the graph; only a real client proves the webhook, the token
and the rendered keyboard.

### The repair loop

| Symptom | Layer at fault |
|---|---|
| `Command not found` | missing or misnamed alias (Phase 5 §9) |
| Task parked forever on the domain call | callee is paused or reply-less — contract misread, go back to Phase 2 §1 |
| Empty message delivered | `text_id` missing from Localization, or missing for the active locale |
| Literal `{{key}}` in the chat | success text uses a key the process never sends |
| Wrong branch after a domain call | shared `result`/`code` not namespaced (Phase 5 §4) |
| Bot answers "something went wrong" to a normal user | alternate outcome mapped as error (Phase 2 §3) |
| User's next message goes nowhere | a path exits without copying `/end` into the Router |
| Keyboard renders on one channel only | Attachments task missing that channel's key |
| **Every reply arrives twice**, the first copy showing literal `{{var}}` | `END -> Router` leaked `text_id`; the Router re-sent it (Phase 5 §10) |
| Literal `{{var}}` in an otherwise correct message | the value was not forwarded in the send node's `data` (Phase 5 §5) |
| Carousel arrives empty | `items`/`currentPage` not forwarded to Send Message |
| A *static* keyboard arrives with no buttons | empty path sent `items: []` instead of `items: ""` |
| Button label appears as an unknown command | an ask step used `inline_keyboard`; use a reply `keyboard` so the label arrives as `message.text` |
| Reply is in the wrong language | `User Profile.language` comes from the client locale; that language is missing for this `text_id` |

Re-run from L1 after every fix. **Cap the loop at about three passes per
defect.** If it is not converging, stop and report precisely what fails, what
you tried, and what you think the cause is. A truthful "7 of 8 commands pass,
this one doesn't and here's why" is worth far more than a loop that quietly
gives up.

