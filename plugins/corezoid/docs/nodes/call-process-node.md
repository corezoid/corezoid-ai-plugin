# Call a Process Node

## Purpose

- Invokes another Process with the current task, optionally waiting for a response.
- Functions like a remote procedure call (RPC).
- Enables modular process design with reusable components.

## Parameters

### Required

1. **Target Process** (String/ID)
   - The Process to be called (specified by conv_id).
   - Example: `"conv_id": 1023395`
2. **Error Node ID** (String)
   - Specifies which node to route to if the process call fails.
   - Example: `"err_node_id": "error_node_id"`
3. **Extra** (Object)
   - Parameters to pass to the called process.
   - Example: `"extra": {"param1": "value1", "param2": 2}`
4. **Extra Type** (Object)
   - Data types for the parameters passed to the called process.
   - Example: `"extra_type": {"param1": "string", "param2": "number"}`

### Optional

1. **Group** (String)
   - Specifies how to handle multiple calls.
   - Default: "all" (waits for all responses)
   - Example: `"group": "all"`
2. **User ID** (Number)
   - User ID for authentication purposes.
   - Example: `"user_id": 56171`

## Key Fields and Their Limitations

### Node Structure Fields

1. **id** (String)

   - Must be a 24-character hexadecimal string
   - Must be unique within the process
   - Example: `"id": "c96fde3d26680cb057f3922a"`
   - Validation: Must match the pattern `^[0-9a-f]{24}$`

2. **obj_type** (Number)

   - Must be `0` for logic nodes
   - Example: `"obj_type": 0`
   - Validation: Must be exactly `0` for Call Process nodes



### Logic Fields

1. **type** (String)

   - Must be `"api_rpc"` (not "call_process")
   - Example: `"type": "api_rpc"`
   - Validation: Must be exactly `"api_rpc"`

2. **conv_id** (Number or String)

   - ID of the process to call
   - Can be a static numeric ID, an alias string, or a dynamic value from the task
   - Example: `"conv_id": 9876543` (numeric) or `"conv_id": "@payment-checkout"` (alias, if one exists)
   - Dynamic example: `"conv_id": "{{process_id}}"` (with quotes for JSON format)
   - Validation: Must be a valid process ID, `@alias`, or dynamic reference

3. **extra** (Object)

   - Parameters to pass to the called process
   - Must be included even if empty
   - Example: `"extra": {"param1": "value1"}`
   - Validation: Required field, cannot be omitted

4. **extra_type** (Object)

   - Data types for the parameters in `extra`
   - Must be included even if empty
   - Example: `"extra_type": {"param1": "string"}`
   - Validation: Required field, cannot be omitted

5. **err_node_id** (String)

   - Must reference a valid node ID in the process
   - Example: `"err_node_id": "c96fde3d26680cb057f3922b"`
   - Validation: Must be a 24-character hexadecimal string

6. **group** (String)
   - Controls how multiple calls are handled
   - Default: `"all"` (waits for all responses)
   - Example: `"group": "all"`
   - Validation: Must be one of the allowed values

## Implementation

The Call a Process Node uses the `api_rpc` type in its JSON configuration:

```json
{
  "id": "call_process_node",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_rpc",
        "conv_id": 9876543,
        "err_node_id": "error_node",
        "extra": {
          "param1": "value1",
          "param2": 2
        },
        "extra_type": {
          "param1": "string",
          "param2": "number"
        },
        "group": "all"
      },
      {
        "type": "go",
        "to_node_id": "next_node_id"
      }
    ],
    "semaphors": []
  },
  "title": "Call Process",
  "description": "Invoke another process",
  "x": 944,
  "y": 200,

  "extra": "{\"modeForm\":\"collapse\",\"icon\":\"\"}",
  "options": null
}
```

## Using Semaphores in Call Process Nodes

Call Process nodes support both time and count semaphores to implement timeouts and concurrency
control. For general information about semaphores in Corezoid, see the [Nodes README](README.md).

### Time Semaphore Example in Call Process Nodes

This example demonstrates how to implement a timeout for a Call Process node. If the called process
doesn't respond within 10 minutes, the task is routed to a timeout error node:

```json
{
  "id": "call_process_with_timeout",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_rpc",
        "conv_id": 1023395,
        "err_node_id": "error_condition_node",
        "extra": {},
        "extra_type": {},
        "group": "all"
      },
      {
        "type": "go",
        "to_node_id": "next_node_in_flow"
      }
    ],
    "semaphors": [
      {
        "type": "time",
        "value": 10,
        "dimension": "min",
        "to_node_id": "timeout_error_node"
      }
    ]
  },
  "title": "Call Process with Timeout",
  "description": "Calls Process 1023395 with a 10-minute timeout",
  "x": 576,
  "y": 200,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Explanation:**

- The `semaphors` array includes a time semaphore that activates when the task enters this node.
- If the called process doesn't respond within 10 minutes, the task is redirected to the
  `timeout_error_node`.
- This provides an important safeguard against processes that might hang or take too long to
  respond.
- The timeout path allows implementing fallback logic or error reporting when process calls exceed
  expected durations.

### Count Semaphore Example in API Call Nodes

This example demonstrates how to implement a rate limit using a count semaphore in an API Call node.
If the count threshold is reached, the task is routed to an escalation node:

```json
{
  "id": "api_call_with_rate_limit",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api",
        "url": "https://api.example.com/data",
        "method": "POST",
        "extra_headers": {},
        "max_threads": 5,
        "content_type": "application/json",
        "err_node_id": "error_node_id"
      },
      {
        "type": "go",
        "to_node_id": "next_node_id"
      }
    ],
    "semaphors": [
      {
        "type": "count",
        "value": 100,
        "esc_node_id": "rate_limit_node_id"
      }
    ]
  },
  "title": "API Call with Rate Limit",
  "description": "Calls external API with a rate limit of 100 concurrent requests",
  "x": 576,
  "y": 200,

  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Explanation:**

- The `semaphors` array includes a count semaphore that monitors the number of concurrent
  operations.
- If the count reaches the threshold of 100, new tasks are routed to the `rate_limit_node_id`.
- This provides a mechanism for implementing rate limiting and preventing API overload.
- The escalation path allows implementing alternative logic when the rate limit is reached.

## Configuration Example

This example demonstrates a Call Process Node configuration extracted from a real process. It shows
how to call another process (`conv_id: 1023395`) without passing specific parameters (`extra: {}`)
and includes the necessary error handling connection.

```json
{
  "id": "call_process_example", // Unique node ID (example uses "61d5499782ba963bce68a24c")
  "obj_type": 0, // Object type for Logic node
  "condition": {
    "logics": [
      {
        "type": "api_rpc", // Specifies this is a Call Process (RPC) logic block
        "err_node_id": "error_condition_node", // ID for error handling (example uses "61d5499782ba963bce68a253")
        "extra": {}, // Parameters to pass (empty in this example)
        "extra_type": {}, // Data types for parameters (empty as 'extra' is empty)
        "group": "all", // Wait for the called process to respond
        "conv_id": 1023395, // ID of the target Process to call
        "obj_to_id": null, // Not typically used for standard calls
        "user_id": 56171, // Internal user ID
        "convTitle": "Reply to process" // Optional title reference (may not always be present or used)
      },
      {
        "type": "go", // Logic block for the successful path after the called process replies
        "to_node_id": "next_node_in_flow" // ID of the next node (example uses "61d54971513aa04bc96877f4")
      }
    ],
    "semaphors": [] // No semaphores used in this node
  },
  "title": "Call Reply Process", // Descriptive title (example node had empty title)
  "description": "Calls Process 1023395 and waits for its reply.", // Optional description
  "x": 576, // X coordinate on canvas
  "y": 200, // Y coordinate on canvas

  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}", // UI settings
  "options": null // No specific options set
}
```

**Explanation:**

- **`type: "api_rpc"`**: Identifies the node's function as a remote procedure call.
- **`conv_id: 1023395`**: Specifies the target Process ID to be invoked.
- **`extra: {}` / `extra_type: {}`**: Indicates that no specific parameters are being passed to the
  called process in this instance. The current task's full data context is typically available
  unless filtered.
- **`err_node_id`**: Points to the Condition node that initiates error handling if the call fails
  (target process not found, access denied, error in called process, etc.).
- The `go` logic block defines the path for the task after the called process successfully executes
  and sends a reply via a "Reply to Process" node.

## Interaction

- The calling task is handed off to the Start node of the target Process.
- The local Process pauses until it receives the reply from a **Reply to Process** node.
- Response data from the called process is merged into the original task.

## Error Handling

- Corezoid provides specific error parameters to handle different failure scenarios:
  - `__conveyor_rpc_return_type_tag__`: Identifies specific error types
  - `__conveyor_rpc_reply_return_type_tag__`: For reply-related errors
  - `__conveyor_copy_task_result__`: Status of the process call operation
- Common error types include:
  - `rpc_task_fatal_error`: Critical failure in the called process
  - `rpc_reply_exception`: Exception thrown by the Reply node
  - `access_denied`: Permissions issue
  - `crash_api`: System-level failure
- Implement retry logic with a Delay node for transient failures

## Request-Response Pattern

When using Call a Process node, ensure the target process implements a Reply to Process node:

```
Calling Process:
Call Process Node → Process Response Handler

Called Process:
Start Node → Process Logic → Reply to Process Node → End Node
```

## Best Practices

- Always implement a timeout mechanism using a Delay node or semaphore to prevent indefinitely stuck
  tasks
- Use **Reply to Process** in the target for full request-response pattern
- Ensure the target process is properly deployed; otherwise tasks will fail or block
- Include comprehensive error handling with specific conditions for different error types
- Position error handling nodes to the right of the Call Process node
- Use parameter mapping to send only the necessary data
- Consider using **Copy Task** instead if you don't need a response
- Use descriptive node titles that indicate the purpose of the process call
- Use the correct type: `"type": "api_rpc"` (not "call_process")
- Always include the required `extra` and `extra_type` parameters for passing data
- Always include an `err_node_id` parameter pointing to a dedicated error node
- Note that all nodes with a `conv_id` parameter (including Call Process and Copy Process nodes) can
  use dynamic values with the format: `"conv_id": "{{param1}}"`

## Folder Structure Organization and Process Communication

When organizing processes into folders, it's important to consider how processes will communicate
with each other. Processes can call each other regardless of their folder location by using the
appropriate `conv_id` parameter, which can be either a static value or a dynamic value from the
process task.

### Folder Organization Best Practices

1. **Group Related Processes**: Place processes that frequently communicate with each other in the
   same folder or related subfolders
2. **Hierarchical Structure**: Use a folder hierarchy that reflects the logical organization of your
   processes:
   - Root-level folders (`parent_id: 0`) for major process categories
   - Child folders for subcategories or specific implementations
3. **Documentation**: Include clear descriptions for folders that explain what kind of processes
   they contain

### Cross-Folder Process Communication

Processes can call other processes in different folders using the `api_rpc` type, as shown in this
example:

```json
{
  "type": "api_rpc",
  "conv_id": 1646021, // ID of the process to call, regardless of folder location
  "err_node_id": "error_node_id",
  "extra": {
    "p1": "{{p1}}" // Pass parameters to the called process
  },
  "extra_type": {
    "p1": "string"
  },
  "group": "all"
}
```

### Process Reference Management

When calling processes across folders:

1. **Use Process IDs, Not Paths**: Reference processes by their unique ID (`conv_id`), not by their
   folder path
2. **Parameter Consistency**: Ensure that parameter names and types match exactly between the
   calling and called processes
3. **Error Handling**: Implement comprehensive error handling for cross-folder process calls
4. **Dependency Documentation**: Clearly document which processes call other processes to maintain a
   readable dependency structure
5. **Version Control**: Consider including version numbers in process titles to track process
   evolution across folders

### Folder JSON Format

When representing folders in JSON format, use the following structure:

```json
[
  {
    "obj_type": 0, // 0 indicates a folder
    "obj_id": 612585, // Unique identifier for the folder
    "parent_id": 0, // 0 for root folders, another folder's obj_id for child folders
    "title": "parent", // Folder name
    "description": "", // Optional description

  },
  {
    "obj_type": 1, // 1 indicates a process
    "obj_id": 1646020, // Unique identifier for the process
    "parent_id": 612585, // The obj_id of the parent folder
    "title": "process-1", // Process name
    "description": "",
    "status": "active",
    "params": [],
    "ref_mask": true,
    "conv_type": "process",
    "scheme": {
      // Process scheme with nodes
    },

  }
]
```

Key points about folder ZIP format:

- Folders and processes are organized in a clear structure within the ZIP file
- Folders have `obj_type: 0`, processes have `obj_type: 1`
- Each process is defined as an object, not an array
- The `parent_id` field establishes the hierarchical relationship
- Root folders have `parent_id: 0`
- Child folders and processes reference their parent folder's `obj_id`

### Example: Parent-Child Folder Process Communication

The following structure demonstrates how processes in different folders can communicate:

```
parent (folder)
├── process-1 (calls process-2 using api_rpc)
└── child (folder)
    └── process-2 (replies using api_rpc_reply)
```

Process-1 uses a Call Process node to invoke Process-2, even though Process-2 is in a child folder:

```json
// In Process-1
{
  "type": "api_rpc",
  "conv_id": 1646021, // ID of Process-2
  "err_node_id": "67f89a46513aa034c8894522",
  "extra": {
    "p1": "{{p1}}" // Pass parameters to Process-2
  },
  "extra_type": {
    "p1": "string"
  }
}
```

Process-2 uses a Reply to Process node to return data back to Process-1:

```json
// In Process-2
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": {
    "p1_Echo": "{{p1}}" // Return data back to Process-1
  },
  "res_data_type": {
    "p1_Echo": "string"
  },
  "throw_exception": false
}
```

## Related Documentation

- [Reply to Process Node](reply-to-process-node.md) - For implementing the response side
- [Error Handling](../process/error-handling.md) - Comprehensive error handling strategies

## Node Patterns

### Basic Call Process Pattern

```
Start Node → Call Process Node → Process Response Handler → End Node
```

### Call Process with Error Handling Pattern

```
                                 ┌─── [hardware error] ──→ Delay Node ──→ Retry Call
                                 │
Start Node → Call Process Node ──┼─── [access denied] ──→ Error End Node
                                 │
                                 └─── [success] ──→ Continue Process Flow
```

### Call Process with Timeout Pattern

```
Start Node → Call Process Node → Delay Node (timeout) → Error Handler
                 │                    ↑
                 └────────────────────┘
```

## Node Naming Guidelines

When creating Call Process nodes in your processes:

1. **Node Titles** should:

   - Clearly indicate which process is being called (e.g., "Call Payment Validation Process" rather
     than just "Call Process")
   - Reflect the purpose of the process call in the context of your workflow
   - Be concise but descriptive enough to understand at a glance

2. **Node Descriptions** should:
   - Explain why this process is being called
   - Mention any important parameters being passed
   - Document any specific error handling considerations
   - Include information about the expected response

Example of good naming:

- Title: "Validate Customer Address"
- Description: "Calls the address validation process with customer_id and address data. Returns
  validation_result with status and errors if any."

Example of poor naming:

- Title: "RPC Call"
- Description: "Calls another process"

Meaningful titles and descriptions make processes more maintainable, easier to troubleshoot, and
more accessible to other team members.
