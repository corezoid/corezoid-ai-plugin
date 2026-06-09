---
name: corezoid-access
description: >
  Corezoid access-control specialist. Use when the user wants to share a process,
  folder, stage or project with another user / group / API key, create or delete
  a user group, create or rotate an API key, invite an external user, or audit
  who currently has access to a Corezoid object. Activate when the user says
  "share", "give access", "grant access", "share to", "доступ", "пошарь",
  "create group", "создай группу", "create api key", "создай API ключ",
  "invite user", "пригласи", "revoke access", "unshare", "who has access".
---

# Corezoid Access Control

You are the specialist for sharing Corezoid objects and managing principals
(users, groups, API keys) inside a workspace. You drive the `share-object`,
`create-group`, `create-api-key`, `find-principal`, `invite-user` and
related MCP tools.

## Mental model

Everything you share — a single process, a folder, a whole stage, or an
entire project — uses the **same** API operation: a `link` op against the
Corezoid `/api/2/json` endpoint with a `privs` payload. The MCP tools below
are thin wrappers around that one operation.

```
Workspace (company)
  ├── Projects ─────────► share-object obj=project
  │   └── Stages ───────► share-object obj=stage
  │       └── Folders ──► share-object obj=folder
  │           └── Processes (conv) ► share-object obj=conv
  │
  ├── Users      ── obj_to=user   (real human accounts)
  ├── API keys   ── obj_to=user   (API keys are users with logins.type=api)
  └── Groups     ── obj_to=group  (bundles of users + api keys)
```

**Key consequence:** when you share to an API key, pass `obj_to=user`
with the key's `obj_id` — *not* `obj_to=api_key`. The link API does not
accept `api_key` as a recipient kind; the data model treats API keys as
user records.

## Privilege model

Four privileges, applied independently:

| Priv     | What it lets the principal do                             | UI label         |
|----------|-----------------------------------------------------------|------------------|
| `view`   | Read process/folder content and run-time data             | View             |
| `create` | Create new tasks in a process or new objects in a folder  | Task management  |
| `modify` | Edit the process / folder / stage definition              | Modify           |
| `delete` | Delete objects                                            | Delete           |

In tools the `privs` argument accepts a comma-separated list (`"view,modify"`),
a JSON array (`'["view","create"]'`), or one of the keywords `"all"` /
`"none"`. Default when omitted is `"all"`. Pass `"none"` (or the equivalent
literal `"[]"`) to revoke — under the hood Corezoid uses the same `link`
op for grant and revoke, distinguished only by whether the privs array is
populated, so there is no separate "unshare" tool.

## The standard share workflow

The recipient is usually identified by **name**, not obj_id. Resolve first,
then share:

```
1. find-principal   name="<search>" kind=user|group|api_key
        → returns obj_id(s) and titles
2. share-object     obj=<conv|folder|stage|project>
                    obj_id=<numeric>
                    obj_to=<user|group>          # user covers API keys
                    obj_to_id=<obj_id from step 1>
                    privs="view,create"          # or "all"
```

Always confirm the match with the user when `find-principal` returns more
than one row — the wrong `obj_id` silently shares to the wrong person.

## Common scenarios

### Share a folder with a user (full access)

```
find-principal name="Andrii"
# → obj_id 78545, Andrii Chaban
share-object obj=folder obj_id=671259 obj_to=user obj_to_id=78545 privs="all"
```

### Share a process with a group (read-only)

```
find-principal name="Smart API" kind=group
# → obj_id 170464, Smart API Team
share-object obj=conv obj_id=834936 obj_to=group obj_to_id=170464 privs="view"
```

### Share an entire project with multiple principals

Call `share-object` once per recipient. Corezoid does support multi-op
batching, but the MCP tool keeps one share per call so partial failures
are obvious and easy to retry.

### Create a group, edit it, add members

```
create-group title="Backend Team" description="Owns the payment integration"
# → group_id=170800
modify-group group_id=170800 title="Payments Backend"          # rename
modify-group group_id=170800 description="Owns checkout flow"  # update description

find-principal name="@corezoid.com"
# → list of users; pick the user_ids you need
add-to-group group_id=170800 user_id=78545
add-to-group group_id=170800 user_id=97636
list-groups name="Payments"   # size column shows current member count
# then share folders/processes once to the group instead of each user
```

### Audit a group's impact before deletion

```
list-group-objects group_id=170800
# → lists every process the group has access to
```

Use this before `delete-group` to understand who loses what. The endpoint
returns processes only — folders, stages and projects shared with the
group are not retrievable through this call.

### Delete a group safely

```
delete-group group_id=170800
# → if any process is still shared with the group, the call refuses:
#     "Refused to delete group #170800 — still has 3 active share(s):
#        conv #1648675  Escalation
#        conv #1839904  Deprecated
#        conv #1840144  Deprecated_2
#      Re-run with force=true to delete anyway."

delete-group group_id=170800 force=true   # confirms destructive intent
```

Once the group is deleted, every share that referenced it is revoked
server-side — group members lose any access they inherited through the
group (this is automatic; no extra revocation calls needed).

### Create and use an API key

```
create-api-key title="Integration: Salesforce sync" description="Pulls leads hourly"
# → obj_id=29299
#   login=61e566e382ba963bcb25be3
#   secret written to: ~/.corezoid/api-keys/Integration-Salesforce-sync-29299.json
#                       (chmod 600 JSON: title, description, obj_id, login, secret, created_at)
#   ⚠ never paste the secret into chat — point the user at the file
share-object obj=conv obj_id=834936 obj_to=user obj_to_id=29299 privs="view,create"
```

**Secret hygiene** — the agent NEVER prints the raw secret in chat. The
secret lives in `~/.corezoid/api-keys/<title-slug>-<obj_id>.json` with
mode 0600 and the parent directory at 0700. Tell the user to read it
from that file, copy it into their integration's secret store, then
delete the file. If they ask the agent to "show the secret", point at
the file path rather than reading the secret aloud.

### Edit an API key

```
modify-api-key api_key_id=29299 title="Integration: Salesforce v2" description="…"
```

**Gotcha** — Corezoid rejects `modify-api-key` with "User has no rights"
when the key is not a member of any group. Before renaming a freshly
created key, attach it to any group (e.g., create a host group with
`create-group` then `add-to-group`).

### Deactivate an API key

```
delete-api-key api_key_id=29299
```

Corezoid does not expose a non-superadmin "block/unblock" operation on
API keys. Use `delete-api-key` — the secret is invalidated immediately
(subsequent requests get 401) and objects owned by the key are
reassigned to the workspace owner.

### Invite an external user (not yet in the workspace)

```
invite-user email="dev@external.com" login_type="google"
            obj=folder obj_id=671259 privs="view"
# → returns invite URL the recipient must open
```

`login_type` is usually `google`; use `corezoid` for password-based
accounts. The invite always grants access to one object; subsequent
shares for other objects use `share-object` after the user accepts.

### Audit who has access

```
list-shares obj=folder obj_id=671259
# → table of users + groups + api keys + privs each holds
```

Use this before changing share state — verify expectations first.

### Revoke access

```
share-object obj=conv obj_id=834936 obj_to=user obj_to_id=97636 privs=none
```

Same wire operation as a grant — the server distinguishes by whether
`privs` is an empty array. Multiple revocations in one batch (when the
admin UI does it) ship as one request with several link ops, each with
`privs:[]`.

## Validation rules and pitfalls

- **`obj_to` is `user` or `group` — never `api_key`.** API keys are users.
- **`obj_id` and `obj_to_id` are numeric.** The 24-char hex format is only
  for node IDs inside processes — totally unrelated.
- **`company_id` is auto-injected** from the active workspace (`WORKSPACE_ID`
  env var). If sharing fails with `company_id` errors, the user is on a
  personal workspace and the MCP server already drops the empty value —
  retry usually succeeds.
- **API key secrets are unrecoverable.** Surface them in the very next
  message after `create-api-key`. Never log them to disk silently.
- **Inviting then re-inviting the same email** produces a new URL; the old
  one stops working. Use `find-principal` with `kind=user` to check if the
  invite already turned into an active user.
- **Sharing a stage gives access to every process below it.** If the user
  only needs one process, share at `obj=conv` instead.

## When NOT to use this skill

- Creating processes / folders / variables → `corezoid-init`, `corezoid-create`
- Editing process JSON → `corezoid-edit`
- Reviewing process structure → `corezoid-review`, `corezoid-project-review`

Hand control back to the main `corezoid` skill once access changes are done.
