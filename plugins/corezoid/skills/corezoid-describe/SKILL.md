---
name: corezoid-describe
description: >
  Updates or creates the description of a Corezoid process, folder, or project
  without touching its logic. Use when the user says "update description",
  "add description", "describe this process", "set description for folder",
  "describe folder", "обнови описание", "добавь описание", "опиши процесс",
  "задай описание папки", or asks to document an object briefly.
  Also use when editing/creating is complete and the user wants to separately
  describe what was built.
---

# Update Process / Folder / Project Description

You are a specialist in writing clear, factual descriptions for Corezoid objects.

## Step 1: Identify the Object

Determine what needs to be described:
- **Process** — user provides a file path, process name, or process ID
- **Folder** — user provides a folder name, folder ID, or path
- **Project** — user provides a project name or project ID

If not clear, ask:
> "Which process, folder, or project should I describe? Provide a file path, name, or ID."

---

## Step 2: Read the Current State

**For a process:**
1. Open and read the `.conv.json` file
2. Note: current `description` (if any), process title, node titles, logic types, external APIs called, declared `params`
3. Also consider any changes the user described in the conversation — they are the primary source of intent

**For a folder:**
1. Resolve `folder_id` using the priority order below (Step 2a)
2. List `.conv.json` files inside the folder (use `find` Bash tool)
3. Read titles and existing descriptions of contained processes to understand the folder's scope

**Step 2a — Resolve folder_id (for folder targets):**

Use the first available source in this order:
1. **Explicit ID** — user provided a numeric folder ID → use it directly
2. **Process context** — a `.conv.json` is known → read its `parent_id` field → that is the `folder_id`
3. **Name search** — user provided only a folder name → call `list-folders(folder_id=0)` and find the matching entry by title
4. **Not found** — name search returns no match → skip the folder description update silently (do not error)

**For a project:**
1. Call `list-projects` or `show-project` to get current metadata
2. Inspect the top-level folder structure to understand what the project covers

---

## Step 3: Generate the Description

### Process description

Write 1–2 sentences:
- **Sentence 1** — what the process does: verb + action + subject.
  - *"Calls the Stripe API to create a payment session and returns the checkout URL."*
  - *"Validates the incoming webhook signature and routes the event to the correct handler process."*
  - *"Creates a new user record in the Simulator platform and returns the actor ID."*
- **Sentence 2** (optional) — key inputs, outputs, or notable behaviour:
  - *"Requires `amount` and `currency`; on error returns a structured error object."*

Rules:
- Start with a verb in third person: *Calls*, *Creates*, *Validates*, *Routes*, *Aggregates*, *Sends*, *Fetches*
- Name the external service, Corezoid object type, or business action specifically
- Do NOT write *"This process…"*, *"The purpose of this…"*, or *"This skill…"*
- Keep under 200 characters
- If the current description is already accurate and recent, say so — do not update for the sake of updating

### Folder description

Write 1 sentence: *"Contains [what kind of processes] for [what purpose/system]."*

Examples:
- *"Contains Stripe payment integration processes (checkout, refund, webhook handling)."*
- *"Contains internal user management processes for the CRM project."*

### Project description

Write 1–2 sentences describing the project's overall purpose and the main systems or workflows it covers.

---

## Step 4: Apply the Description

**For a process:**
1. Update the `description` field at the root of the `.conv.json` file
2. Call MCP tool **`push-process`** with `process_path`
3. Confirm: *"Description updated and deployed."*

**For a folder:**
Use the `folder_id` resolved in Step 2a. If it was not resolved (source 4 — not found), skip this step.
Call MCP tool **`modify-folder`** with `folder_id` and `description`.

**For a project:**
Call MCP tool **`modify-project`** with `company_id`, `project_id`, and `description`.

---

## Notes

- If the user describes what changed (*"I just added a retry node"*), incorporate that context into the description
- Never fabricate process behaviour not visible in the JSON or stated by the user
- For processes that already have a good description, confirm with the user before overwriting
