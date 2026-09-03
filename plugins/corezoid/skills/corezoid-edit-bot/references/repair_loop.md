# The repair loop

An edit that fails almost never fails at push time. It fails as a symptom in a
chat, and the same symptom can come from four different layers. This table maps
what the user reports to where the defect actually is — read it before
re-reading the graph, because the most expensive mistake here is fixing the
layer you can see rather than the layer at fault.

## Symptom → cause

| Symptom | Cause, and where to fix it |
|---|---|
| `Command not found` | The alias is missing, misnamed, or points elsewhere. `^[a-z0-9][a-z0-9-]{2,}$`, and check `_ALIASES_.json` by `obj_to_id` — an `obj_to_id: null` row resolves to nothing (`invariants.md` §8) |
| A removed command still answers | Deleting or pausing the process does not free the alias, so the Router still dispatches to it. `invariants.md` §8 — this needs raw-API alias surgery, not another push |
| Task parked forever on the domain call | The callee is paused or reply-less. A contract misread, not a wiring bug: `/corezoid-gen-bot refresh`, then re-plan. And add the missing `time` semaphore (§6) so it fails in 30 s next time instead of never |
| Empty message delivered | The `text_id` is missing from Localization, or missing **for the user's language**. `User Profile.language` comes from the client locale and there is no fallback (§9). `serviceError` in particular is not shipped by the wizard |
| Every failure path delivers an empty message | `serviceError` was never seeded (§9) |
| Literal `{{var}}` in an otherwise correct message | The value was not forwarded in the send node's `data` (§1). Present on the command's task, absent on Send Message's |
| A `{{var}}` renders as an empty string | It *was* forwarded but the process never produces that key — check `success.keys` in `bot-contract.json` (§1) |
| A placeholder like `{{message.text}}` or `{{t'.key}}` reaches the chat verbatim | Wrong interpolation form (§3). `{{var}}` is flat-keys-only; `{{t'key}}` has no dot |
| **Every reply arrives twice**, the first copy showing literal `{{var}}` | `END -> Router` leaked a non-empty `text_id`, so the Router re-sent it without any forwarded values (§2) |
| Carousel arrives empty | `items` / `currentPage` not forwarded to Send Message (§1) |
| A *static* keyboard arrives with no buttons | The empty path sent `items: []` instead of `items: ""` (§1) |
| Keyboard renders on one channel only | The Attachments task is missing that channel's key — or the key is spelled `fbmessenger` where Send Message dispatches on `facebook` (§9) |
| A button tap is reported as an unknown command | An ask step used `inline_keyboard`; `callback_data` is parsed as a command. Use a reply `keyboard` (§4) |
| A button carries the wrong params | A literal `-` or `_` inside a payload value split it (§5) |
| Wrong branch after a domain call | Two calls sharing the `{result, code}` envelope — namespace both with an `api_code` (§7) |
| Bot answers "something went wrong" to a perfectly normal user | An alternate outcome (`402/registration`, `401/recovery`) mapped as an error. Those are control flow, not failure |
| The user's next message goes nowhere | A path exited without copying `/end` into the Router, so the System Diagram state stayed `active` (§2) |
| Reply is in the wrong language | That language is missing for this `text_id`; the client locale chose it (§9) |
| A multi-step dialog uses the wrong answer for a step | A later node read `{{message.text}}` instead of the key the Keep node stored — that reads the *latest* message |
| The change appears not to have landed at all | A template id read from PLAN.md was stale, or the pull was scoped wrongly and the ids came from the wrong tree. A stale id fails silently: the task simply never arrives |

Section numbers refer to `invariants.md`.

## Which layer to re-run after a fix

Cheapest first; stop at the first failure in a layer before moving on.

1. **Static** — `grep` for leftover `{{UPPER_CASE}}` and for tokens;
   `lint-process`; the Localization/Attachments key checks; the forwarding
   check; the `/end` check. Everything in §1–§3 and §9 is catchable here, and
   only here.
2. **The command alone** — `run-task` at the command process with the data the
   Router would supply. Proves the domain call, the namespacing and the
   branching. Does **not** prove the user sees anything.
3. **The rendered message** — `run-task` on **Send Message** with
   `{channel, chat_id, text_id, attachment_id}` plus the interpolated values,
   then assert on `data.text` and `reply_markup`. This is the only layer that
   catches §1 and §2. A synthetic `chat_id` fails the outbound call with
   `Bad Request: chat not found` — expected, because text and attachment resolve
   *before* the send.
4. **Through the Router** — the only layer that proves alias dispatch.
5. **A real client** — the only layer that proves the webhook, the token and the
   rendered keyboard.

## Diagnosis tools

- `list-task-history` — where a task actually went, when it did not go where you
  expected. First thing to reach for on a parked or misbranched task.
- `list-node-tasks` — what is sitting in a given node right now; useful for
  finding users stuck mid-dialog after a change.
- `show-task process_id: … ref: <channel>_<chat_id>` — the command's live task
  for one chat, and the way to read Localization and Attachments documents.
- `get-node-stat` — whether a node is being reached at all.

## When to stop

**Cap the loop at about three passes per defect.** Each pass should be driven by
the table above, not by re-reading the graph hoping something jumps out; if
three targeted fixes have not converged, the model of the failure is wrong and a
fourth guess will not find it.

Then stop and report precisely: what fails, at which layer, what you tried, what
you think the cause is, and what you would need in order to confirm it. A
truthful "7 of 8 commands pass, this one parks on the domain call and I believe
the callee is paused — here is the node and the evidence" is worth far more than
a loop that quietly gives up, and far more than a green report built on a clean
lint.

And separate the two kinds of finding when you report them: a defect in the
change you just made is yours to fix; a defect in a handed-in domain process is
a finding about **someone else's system**. Name the field you read before
calling one broken, and hand the fix to `corezoid:corezoid-edit` with the user's
agreement rather than editing it here.
