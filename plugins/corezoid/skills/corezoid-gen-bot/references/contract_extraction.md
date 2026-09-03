# Deriving what a Corezoid process consumes and produces

This is the analytical heart of the skill. The input is a bag of processes
somebody else wrote; the output is a manifest precise enough to generate a bot
command against. Work from the pulled `.conv.json`, not from the title and not
from what the user says the process does.

The method below is the one `simulator-app-generator` uses to build Smart Forms
over the same kind of backend, with the parts that differ for a bot called out.

## 0. Work from the file on disk — and know how old it is

`pull-process` writes `<ID>_<Title>.conv.json`. The `<ID>_` prefix is
load-bearing: `push-process` and `lint-process` recover the process id from the
filename and fail with a format error otherwise. **Never rename these files.**

The workspace is usually an already-pulled stage, so check for
`<id>_*.conv.json` under the stage root before pulling anything (SKILL.md §1.1)
— a re-pull costs a round-trip and **overwrites** the local file, taking any
unpushed edit with it.

The flip side: a reused file is a snapshot of unknown age, and everything below
is derived from it. Carry `source` (`local`/`pulled`) and the file's mtime into
the manifest. A process whose contract changed since that snapshot — a new
alternate outcome, a removed reply node, a renamed input — produces a command
that mis-branches at runtime, and nothing in the file itself reveals that.
Verifying freshness *is* pulling, so state the age rather than implying
currency, and re-pull the specific process when its contract is the one a
command leans on.

## 1. `params[]` is a hint, never the truth

Every process has a root `params[]`:

```jsonc
"params": [
  {"name":"phone",       "type":"string", "flags":["required","input"], "descr":"phone"},
  {"name":"bonusAmount", "type":"string", "flags":["output"],           "descr":"code :200"}
]
```

This is the **declared** contract and it drifts. In the reference set the
app-generator was built against, five of twelve processes declared *zero*
outputs while clearly replying with data, and one declared an output no reply
node ever sets. Use `params` as a cross-check and to recover the `required`
flag — derive the real contract from the graph.

## 2. Is the process callable at all? — the bot-specific gate

Answer this **before** extracting anything, because it decides which of three
completely different wirings the command gets. A Smart Form can paper over the
difference with a `/get` that returns nothing; a chat cannot — the user is
sitting there waiting for a sentence.

| Finding | Wiring | Consequence for the command |
|---|---|---|
| `conv_type: "process"` with ≥1 `api_rpc_reply` **reachable from Start** | `api_rpc` | Normal: call, read the reply, render it |
| `conv_type: "process"` with **no** reachable `api_rpc_reply` | `api_copy`, fire-and-forget | The bot cannot show a result. The command can only acknowledge ("заявка принята"). Say so in the plan — do not invent a confirmation the backend never sent |
| `conv_type: "state"` | not called at all | Read it inline: `{{conv[<id>].ref[<key>].<field>}}` in a `set_param`. A state diagram has no Start-to-Final path to call |
| `status` not `active` (paused) | nothing | An `api_rpc` into a paused process hangs until the semaphore fires. Report it and ask before wiring |

An `api_rpc` into a process with no reply node **hangs until the semaphore
fires** — which for a bot means the user waits the full 30 s and then gets an
error. This is the single most expensive misread in this skill, and it is
invisible in `params`.

## 3. Outputs — from `api_rpc_reply` nodes

Walk `scheme.nodes[].condition.logics[]` and collect every logic with
`type == "api_rpc_reply"`:

```jsonc
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data":      {"code":"200","result":"success","transactionList":"{{transactionList}}"},
  "res_data_type": {"code":"string","result":"string","transactionList":"array"},
  "throw_exception": false
}
```

- `mode` is `"key_value"` in practice; a `"keys"` array form exists — handle it
  if you see it.
- **Every value in `res_data` is a string**, even when `res_data_type` says
  `array`/`object`. A `"{{var}}"` value means "produced upstream"; a literal
  **with no `{{}}` in it** is a constant.
- **A literal that *contains* `{{...}}` is neither.** Corezoid interpolates
  `res_data` against the **callee's own task** before replying, so any
  placeholder the callee cannot resolve is replaced with an empty string — and
  the caller receives a hollowed-out value, not the template you read in the
  file. In the reference set `FAQ` replies with a literal JSON string carrying
  `{{t'LoyaltyAnswers}}` and friends (Smart Form locale keys that exist in no
  Corezoid task), so every `text` in that array arrives **empty**; a command that
  trusted the file rendered a list of bare URLs with no labels. The scheme shows
  the template, the live call shows the emptiness — **this is invisible to
  static reading and is one of the strongest arguments for a probe (§9).**
  Treat an embedded placeholder in a literal as a value the caller will never
  receive: resolve it from a sibling field in the same element that *is* real,
  derive it locally, or drop the field from what you render — but never render it.
- `throw_exception: true` marks the failure replies — an RPC caller sees these
  as an error on the calling node, routed to its `err_node_id`, **not** as data.
  So a command's error path is fed by these, and its `Map outcome` node never
  sees them. Note which ones exist, but do not map them to a `text_id`.

## 4. The envelope convention — three sets, not two

Processes in this family reply `{result, code, ...payload}` with
`result ∈ success | error | recovery | registration | …` and
`code ∈ 200 | 400 | 401 | 402 | 403`. Group the reply nodes:

| Set | Match | What the command does with it |
|---|---|---|
| **success** | `result:"success"` or a 2xx `code` | The union of its payload keys (minus `code`/`result`) **is the real output schema** — these are the `{{placeholders}}` the reply text may use |
| **alternate** | any other non-error `result` (`recovery`, `registration`, …) | A **control-flow outcome, not a failure** → its own `text_id`, and often its own follow-up question |
| **error** | `result:"error"` | Boilerplate → `serviceError` |

> **Alternate outcomes are the most commonly missed signal.** The reference auth
> process has 8 reply nodes: `200/success` (with `token`, `cardCode`,
> `bonusAmount`, `QR`), `402/registration`, `401/recovery`, and several
> `403/error`. A command that only reads the 200 branch silently drops the whole
> registration and recovery flows — and in a chat that shows up as the bot
> answering "something went wrong" to a perfectly normal new user.

This is what the `Map outcome` Code node in both skeletons is shaped around, and
why it has an `alternate` arm rather than a boolean ok/failed.

## 5. Types

Take types from `res_data_type`. They are **inconsistent between sibling reply
nodes of the same process** — `code` typed `"number"` in one node and
`"string"` in five others, `bonusAmount` both ways. **Majority-vote** and move
on; a conflict is not an error.

The vote matters here because it lands in the `api_rpc` node's `extra_type` when
an output of one process becomes the input of the next.

## 6. Array element shape

To render a list as a carousel you need the *element* structure, which
`res_data_type: "array"` does not give. Three tiers, in order:

1. **Reply-node `description`** — often holds a pretty-printed example
   response. Richest static source; measured availability in the reference set
   was 18 of 36 reply nodes, so treat it as opportunistic.
2. **Upstream trace** — follow the `{{var}}` back to the `api_code` /
   `set_param` / `api` that produced it. A Code node usually reveals the shape.
3. **Live probe** — `run-task`, user-gated (§9).

If all three fail, record `"elementShape": "unknown"` and either ask the user or
render the list as `{{name}} — {{value}}` pairs over whatever keys appear (the
`carouselPattern` attachment does exactly this).

## 7. Inputs

Real inputs = `params[flags ∋ input]` **∪** `{{placeholders}}` consumed before
being produced. **Payloads live in three different fields — scan all three**, or
you will report a process as input-free when it is not:

| Node | Payload field |
|---|---|
| `api_rpc`, `set_param` | `extra` / `extra_type` |
| `api_copy` | **`data` / `data_type`** — there is no `extra` field at all |
| `api` with `format:"raw"` | **`raw_body`** — a JSON *string*; `extra` is `{}` |

```jsonc
{"type":"api_rpc","conv_id":1760254,
 "extra":{"lat":"{{location_lat}}","lon":"{{location_lon}}","radius":"200000"}}
```

`location_lat` / `location_lon` are inputs; `radius` is a constant. Carry the
`required` flag from `params` where present.

Two traps before you generate a dialog question for an input:

- **A declared input can be dead.** If `{{thatParam}}` appears in no node, the
  value comes from somewhere else — typically a state read like
  `{{conv[<id>].ref[SessionData].Session}}`, which is one *workspace-wide*
  session rather than a per-user token. Drop it from the manifest and record
  why. Asking a messenger user for it is asking for something that is ignored.
- **`{{conv[<id>].ref[<key>].<field>}}` is data, not input** — except for a
  nested inner placeholder (`…ref[Cities].{{cityCode}}`), where the inner one
  *is* an input.

### 7a. Inputs the bot already has — do not ask for these

Before turning an input into a question, check whether the orchestrator already
holds it. Every command runs with `channel` and `chat_id` on the task, and
`User Profile` (ref `<channel>_<chat_id>`) carries `language`, `user_name`,
`country`, plus anything an earlier command wrote there.

| Input looks like | Source |
|---|---|
| user id / chat id / external id | `{{chat_id}}` |
| channel / platform | `{{channel}}` |
| language / locale | `{{conv[<user_profile_id>].ref[{{channel}}_{{chat_id}}].language}}` |
| name | `…ref[…].user_name` |
| a token or card code obtained by an earlier command | whatever that command wrote to User Profile |

A dialog that asks for a value the bot can read is a worse bot, and a dialog
that asks for a credential is a security defect: never generate a question for a
token, password, or API key.

### 7b. Reusing what a previous run stored — and invalidating it

A value the bot *asked for once* can be written to User Profile and reused, so
the second use of a command asks nothing. Read it in a `set_param`, gate the ask
behind a `go_if_const`, and the question disappears for returning users:

```jsonc
// two conditions inside ONE go_if_const are ANDed: everything present -> ask nothing
{"conditions":[{"cast":"string","const":"","fun":"not_eq","param":"prof_ident"},
               {"cast":"string","const":"","fun":"not_eq","param":"prof_secret"}],
 "to_node_id":"<use both, call straight away>","type":"go_if_const"}
```

Gate a *format-sensitive* value on its own regex rather than `not_eq ""`, so a
half-written profile field does not reach the domain process.

> **A reused value that stops working locks the user out permanently, precisely
> because the command no longer asks.** This is the failure mode of the whole
> pattern and it is not hypothetical: in the reference set a stale stored card
> password came back as **`401/recovery`, not `403/error`**, so an outcome map
> that only cleared on "error" left the dead value in place, replied "restore
> your password" forever, and gave the user no way to retype the correct one.
>
> The rule that survives contact with a real backend: **a stored credential that
> did not produce a success is unusable, whatever reason the backend gave.**
> Clear it, say so, and re-ask. Carry a flag (`usedStored`) from the read node so
> the outcome map can tell "the saved value failed" from "the user mistyped it" —
> they need different copy. Do not try to enumerate which codes mean "stale":
> that is exactly the guess that fails.
>
> One exception is worth coding explicitly: a "no such account" outcome
> (`402/registration` here) cannot be fixed by re-asking a password. Clear the
> stored value and route to the sign-up command instead of into a re-ask loop.

Every command that *changes* the stored value must keep it in sync, or the next
command reuses a value it already knows is dead. Two directions to wire:
a command that makes the backend **replace** the credential clears the stored
copy (the new value is one the bot was never told), and a command that is
**issued** a credential saves it.

**Storing a credential is a security decision, not a UX one — surface it.** The
value sits in a state-diagram document in plaintext, readable by anyone with
workspace access, and it lets the bot authenticate as that user unattended.
Scrubbing it from the *task* after the write is hygiene, not protection. Say this
plainly in the plan, note that there is no session expiry unless you build one,
and let the owner decide.

## 8. Side-effect classification

**Side effects are not statically obvious.** In the reference set `Send complain`
and `Send calback mailing` look inert at this layer but fan out via `api_copy`
to a mailing process, and `Cashback Categories` sends Telegram/Viber messages.
Classify each process `likely | unlikely | unknown` from:

- `api_copy` presence,
- mutating verbs in the **URL path or callee name** of an outbound call
  (`create`, `set`, `send`, `register`, `update`, `delete`, `pay`),
- an outbound reply nothing downstream reads,
- title/description keywords (send, mail, register, recovery, complain, notify).

> **The HTTP method is a weak signal on its own — do not classify on it.** These
> backends POST to read: the reference `Transactions history` fetches its rows
> with `api POST …/chatbot/getTransactions` and mutates nothing. Read the path
> and the payload: `getTransactions` is a read, `setClientFields` is not.

**Score only the nodes reachable from Start.** Corezoid never prunes orphans, so
BFS over `to_node_id` + `err_node_id` + `semaphors[].to_node_id`. In the
reference set `Cashback Categories` has **126 nodes and 6 reachable** — the
other 120 are dead chatbot senders; scored over the whole bag it looks `likely`,
scored over the reachable set it is `unlikely` and probes safely. The same walk
stops you deriving outcomes from unreachable reply nodes.

Record `"nodes": "<reachable> of <total> reachable"` and flag a lopsided ratio —
it usually means the process was repurposed and its `params` describe the old
job.

This classification gates two things: probing (§9) and **whether the command
needs a confirmation step**. A `likely` or `unknown` process may only be reached
after the user has explicitly confirmed in the dialog. Never treat `unlikely` as
proof of safety.

## 9. Ground-truth probing — opt-in, user-gated

`run-task(process_path, data)` runs a task on the deployed process and waits for
a final node, so it returns the process's **real** reply — the only reliable way
to resolve an unknown array shape or an undocumented envelope.

**Never probe automatically.** Present the side-effect classification, let the
user pick which processes are safe to call with test data, and probe only those.
Calling a process blind can send a real SMS, email or Telegram message to a real
customer, or burn a real card number.

Feed anything learned back into the manifest before designing commands.

## 10. The manifest

Write `.corezoid-gen-bot/bot-contract.json`:

```jsonc
{
  "processes": [{
    "id": 1760349,
    "title": "Authorization + Bonus amount",
    "path": "689413_API2/1760349_Authorization_+_Bonus_amount.conv.json",
    "source": "local",
    "fileMtime": "2026-08-28T14:02:11Z",
    "alias": "authorization",
    "convType": "process",
    "status": "active",
    "callable": "api_rpc",
    "inputs":  [{"name":"phone","type":"string","required":true,"source":"ask"},
                {"name":"cardPassword","type":"string","required":true,"source":"ask"}],
    "outcomes": {
      "success":   {"code":"200","keys":{"token":"string","cardCode":"string",
                                         "bonusAmount":"number","QR":"string"}},
      "alternate": [{"code":"402","result":"registration"},
                    {"code":"401","result":"recovery"}],
      "error":     [{"code":"403"}]
    },
    "arrays": {},
    "nodes": "23 of 25 reachable",
    "sideEffects": "unknown",
    "declaredParamsMatch": false,
    "deadDeclaredInputs": []
  }]
}
```

`source` on each input is the bot-specific field: `ask` (a dialog question),
`chat_id` / `channel`, `profile:<field>`, `const:<value>`, or
`chain:<processId>.<key>` when it comes from an earlier call in the same
command.

## 11. Report before designing

Show a contract table, and **explicitly flag**:

- processes with **no reachable `api_rpc_reply`** — nothing to render,
- processes that are **paused**,
- arrays with **unknown element shape**,
- the **side-effect classification** and the **reachable/total ratio** wherever
  it is lopsided,
- every place `params` disagreed with the reply nodes
  (`declaredParamsMatch: false`), including **declared inputs no node consumes**
  — but only after the `group:"all"` check in §7, because a naive occurrence count
  reports live required inputs as dead and the generated command would then drop a
  required argument,
- **which contracts came from a reused local file, and how old it is** — a
  finding about a process is only as current as the snapshot it was read from.

**State the evidence for any claim you make about the backend.** Saying "this
process is broken" is a finding about someone else's system, and the
payload-carrier traps in §7 make it easy to get wrong — an `api_copy` read
through `extra` looks like it sends nothing. Before reporting a defect, confirm
you read the right field (`data` for `api_copy`, `raw_body` for a raw `api`) and
name the field you read.
