# Git Call Node

## Purpose

- Pulls and executes code from a Git repository.
- Allows for version-controlled, reusable code components across processes.
- Enables centralized code management with distributed execution.

## Runtime limits and the selection rule

Git Call is one of the most constrained nodes on the platform. Use it **only**
when a step needs a capability that native nodes plus a Code (`api_code`) node
cannot provide — a file to parse, an external library, cryptography, or a custom
runtime — and the work finishes comfortably within the observed runtime budget.
"One code block is easier to write" is not one of those capabilities.

Observed and configured constraints:

- **Execution deadline observed in hosted tests:** approximately 60 s wall-clock
  from when the task entered the node. Keep handlers comfortably below 50 s
  instead of relying on that exact cutoff; it is observed behavior, not a
  portable contract. Overruns produced `git_call_executing_error` /
  `usercode: timeout`. Inline cold and warm probes ended at the same point;
  custom images may have different startup overhead.
- **Default resources:** 50 MB RAM / 0.1 CPU from a pool shared by Git Call
  nodes. These values are super-admin-configurable. Shared capacity can cause
  contention under concurrency; a live concurrent probe observed one starved
  task.
- **No persistent local storage:** writable runtime paths such as `/tmp` are
  ephemeral. Git-repo mode and dependency installation need build-time network
  access; runtime network access depends on the handler.

Do not keep a Git Call invocation open for long-running loops, polling, retries,
or external waits. Represent those as process state transitions
(`condition`+`delay`) or resume through a callback. These alternatives avoid
the per-invocation Git Call deadline, while remaining subject to normal
platform/process limits. Use `api`/`api_rpc` for plain HTTP and `api_code` for
ordinary payload transforms.

> Note: a `time` semaphor is a process-level routing deadline that sends a task
> down the configured timeout path if the node remains active. It is independent
> of the container execution deadline and cannot extend a Git Call handler's
> runtime.

## Parameters

### Required

1. **Repository URL** (String)
   - Git repository address (HTTPS or SSH).
   - Example: `"repo": "https://github.com/example/repo.git"`
2. **Branch/Tag/Commit** (String)
   - Specific branch, tag, or commit to use.
   - Example: `"commit": "main"`
3. **Error Node ID** (String)
   - Specifies which node to route to if the Git call fails.
   - Example: `"err_node_id": "error_node_id"`

### Optional

1. **Language** (String)
   - Programming language of the code (e.g., "js" for JavaScript).
   - Example: `"lang": "js"`
2. **Version** (Number)
   - API version to use.
   - Example: `"version": 2`
3. **Routing timeout** (Object)
   - Process-level fallback using a time semaphor; it does not control or extend
     the Git Call container deadline.
   - Example: `{"type": "time", "value": 2, "dimension": "min", "to_node_id": "timeout_node_id"}`

## Error Handling

- Corezoid distinguishes between two types of errors:
  - **Hardware errors**: Infrastructure or network failures (retried automatically)
  - **Software errors**: Code syntax or runtime errors (routed to error handling)
- Error routing is configured through a Condition node that checks:
  - `__conveyor_git_call_return_type_error__`: Identifies error type ("hardware" or "software")
- Implement retry logic with a Delay node for transient hardware errors
- Configure timeout handling to prevent indefinitely stuck tasks

## Best Practices

- Use specific tags or commit hashes rather than branch names for stability
- Include proper error handling in your Git-hosted code
- Keep repository access credentials secure
- Test code changes thoroughly before updating the repository
- Consider caching for frequently used, stable code
- Use descriptive node titles that indicate the purpose of the Git call
- Implement comprehensive error handling with specific conditions for different error types
- Position error handling nodes to the right of the Git Call node
- Configure a routing timeout as a safety net, independently of the handler's
  execution budget

## Node Naming Guidelines

When creating Git Call nodes in your processes:

1. **Node Titles** should:

   - Clearly indicate what code is being executed (e.g., "Execute Payment Validation Script" rather
     than just "Git Call")
   - Reflect the purpose of the code execution in the context of your workflow
   - Be concise but descriptive enough to understand at a glance

2. **Node Descriptions** should:
   - Explain what the Git-hosted code does
   - Mention any important parameters being passed to the code
   - Document any specific error handling considerations
   - Include information about the expected output from the code

Example of good naming:

- Title: "Calculate Risk Score"
- Description: "Executes risk assessment algorithm from the risk-models repository. Processes
  customer data and returns risk_score and risk_factors."

Example of poor naming:

- Title: "Git Call"
- Description: "Runs code from Git"

Meaningful titles and descriptions make processes more maintainable, easier to troubleshoot, and
more accessible to other team members.

## Configuration Example

This example demonstrates a Git Call Node configuration extracted from a real process. It shows how
to specify the repository, commit, language, and error handling connections. It also includes a
timeout semaphore.

```json
{
  "id": "git_call_example", // Unique node ID (example uses "61d5467082ba963bce684b6c")
  "obj_type": 0, // Object type for Logic node
  "condition": {
    "logics": [
      {
        // Note: Type is 'api_git' based on default config, though example JSON used 'git_call'
        "type": "api_git",
        "version": 2, // API version
        "lang": "js", // Language of the script (JavaScript)
        "repo": "https://github.com/fresco117/test.git", // URL of the Git repository
        "commit": "main", // Branch/tag/commit hash to use
        "err_node_id": "error_condition_node" // ID for error handling (example uses "61d5467082ba963bce684b70")
        // Parameters like 'code', 'script', 'log' seen in example JSON are often context-specific or less common
      },
      {
        "type": "go", // Logic block for the successful path
        "to_node_id": "success_node_id" // ID of the next node on success (example uses "61d54669513aa04bc96822e2")
      }
    ],
    "semaphors": [
      // Semaphores for controlling execution
      {
        "type": "time", // Timeout semaphore
        "value": 2, // Routing timeout value
        "dimension": "min", // Timeout unit (minutes)
        "to_node_id": "timeout_error_node" // Node to go to on timeout (example uses "61d5467082ba963bce684b6f")
      }
    ]
  },
  "title": "Execute Script from Git", // Descriptive title (example node had empty title)
  "description": "Pulls and executes the main branch script from the fresco117/test repository.", // Optional description
  "x": 452, // X coordinate on canvas
  "y": 176, // Y coordinate on canvas
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}", // UI settings
  "options": null // No specific options set
}
```

**Explanation:**

- **`type: "api_git"`**: Identifies the node's function (using the type from the default config
  example).
- **`repo`, `commit`, `lang`, `version`**: Specify the source code location and execution
  parameters.
- **`err_node_id`**: Points to the Condition node for handling errors during Git operations or code
  execution.
- The `go` logic block defines the path after successful execution.
- The `semaphors` array includes a process-level fallback that routes the task
  to `timeout_error_node` if the node remains active for 2 minutes. It does not
  allow the handler to execute for 2 minutes.

## Default Configuration with Escalation Nodes

When creating a Git Call node in the Corezoid interface, the system automatically generates the
following default configuration:

```json
{
  "id": "git_call_node_id",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_git",
        "repo": "",
        "commit": "main",
        "lang": "js",
        "version": 2,
        "err_node_id": "error_node_id"
      }
    ],
    "semaphors": [] // Optional semaphores for implementing timeouts or concurrency control
  },
  "title": "Git Call",
  "description": "",
  "modeForm": "expand",
  "active": true
}
```

The default escalation pattern for Git Call nodes consists of:

1. **Condition Node** - Evaluates the type of error:

   - Checks `__conveyor_git_call_return_type_error__` for "hardware" or "software" errors
   - Routes tasks to appropriate handling paths

2. **Delay Node** - For hardware errors (connection issues, Git repository access problems):

   - Implements a retry mechanism with configurable delay (default: 30 seconds)
   - Routes back to the original Git Call node after the delay

3. **Error End Node** - For software errors (code syntax errors, runtime errors):
   - Marks the task as failed
   - Provides error details for debugging

The escalation pattern is automatically positioned to the right of the Git Call node:

```
                            ┌─── [hardware error] ──→ Delay Node ──→ Back to Git Call
                            │
Git Call Node ──→ Condition Node ─┤
                            │
                            └─── [software error] ──→ Error End Node
```

To create this pattern automatically:

1. Select the Git Call node
2. Click on the error message that says "Node must be connected to an error-handling node"
3. Click "Create escalation nodes" button in the node properties panel
