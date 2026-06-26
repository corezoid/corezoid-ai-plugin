# Copy Task Node

## Purpose

- Duplicates the active task and forwards the copy to another Process, leaving the original task to
  continue locally.
- Enables parallel processing flows and task distribution.
- Creates independent task instances in separate processes.

## Parameters

### Required

1. **Target Process** (String/ID)
   - The Process that will receive the copied task.
   - Example: `"conv_id": 1023393` (numeric) or `"conv_id": "@send-notification"` (alias, if one exists)
   - Can be a dynamic value from the process task
   - Dynamic example: `"conv_id": "{{param1}}"` (with quotes for JSON format)
2. **Error Node ID** (String)
   - Specifies which node to route to if the task copy fails.
   - Example: `"err_node_id": "error_node_id"`

### Optional

1. **Mode** (String)
   - Specifies the copy mode, typically "create".
   - Example: `"mode": "create"`
2. **Data** (Object)
   - Key-value pairs to include in the copied task.
   - Example: `"data": {"fieldA": "{{a}}"}`
3. **Data Type** (Object)
   - Specifies the data types of the copied values.
   - Example: `"data_type": {"fieldA": "string"}`
4. **Group** (String)
   - Specifies how to handle multiple copies.
   - Must be "all" when using key/value data in the data parameter.
   - Must be empty string ("") when using empty object data.
   - Example: `"group": "all"`
5. **Reference** (String)
   - Custom reference ID for the new task.
   - Example: `"ref": ""`
6. **Send Parent Data** (Boolean)
   - Whether to include the current task's data in the copy.
   - Example: `"send_parent_data": false`

## Interaction

- Original task continues in the current process flow.
- The copied task starts at the target Process's **Start** node, proceeding independently.
- The copied task receives the specified data and/or parent data if configured.

## Error Handling

- Corezoid provides specific error parameters to handle different failure scenarios:
  - `__conveyor_copy_task_return_type_tag__`: Identifies specific error types
- Common error types include:
  - `not_unical_ref`: The specified reference already exists
  - `access_denied`: Insufficient permissions to copy to the target process
  - `copy_task_timeout`: The copy operation timed out
  - `copy_task_fatal_error`: Critical failure in the copy operation
  - `crash_api`: System-level failure
- Implement retry logic with a Delay node for transient failures like `crash_api`,
  `copy_task_timeout`, and `copy_task_fatal_error`
- Route permanent failures like `access_denied` and `not_unical_ref` to error handling nodes

## Using Semaphores in Copy Task Nodes

Copy Task nodes support both time and count semaphores to implement timeouts and concurrency
control:

### Time Semaphores

Time semaphores can be used to implement timeouts for task copy operations. If the copy operation
doesn't complete within the specified time, the task is routed to a timeout node:

```json
"semaphors": [
  {
    "type": "time",
    "value": 30,
    "dimension": "sec",
    "to_node_id": "copy_timeout_node_id"
  }
]
```

The `dimension` parameter can have the following values:

- `"sec"` - seconds
- `"min"` - minutes
- `"hour"` - hours
- `"day"` - days

This provides a mechanism for handling task copy operations that might take longer than expected,
especially when copying tasks to busy processes.

### Count Semaphores

Count semaphores can be used to implement concurrency control for task copy operations. If the
number of concurrent copies reaches the threshold, new tasks are routed to an escalation node:

```json
"semaphors": [
  {
    "type": "count",
    "value": 50,
    "esc_node_id": "copy_limit_node_id"
  }
]
```

This can be used to prevent system overload when copying tasks to multiple processes simultaneously.

## Best Practices

- Validate that the target Process is active and appropriate for receiving copies
- Use parameter mapping to avoid sending confidential data if not required
- Consider using **Call a Process** instead if you need a response from the target Process
- Ensure the target Process has appropriate error handling
- Use descriptive node titles that indicate the purpose of the copy operation
- Include comprehensive error handling with specific conditions for different error types
- Position error handling nodes to the right of the Copy Task node
- Generate unique references for each copied task to avoid conflicts

## Implementation Example

This example demonstrates a Copy Task Node configuration extracted from a real process, including
parameter mapping and error handling connections.

```json
{
  "id": "copy_task_node_example", // Unique node ID (example uses "61d548f082ba963bce688f41")
  "obj_type": 0, // Object type for Logic node
  "condition": {
    "logics": [
      {
        "type": "api_copy", // Specifies this is a Copy Task logic block
        "mode": "create", // Mode for creating a new task
        "data": {
          // Data to be included in the copied task
          "fieldA": "{{a}}" // Maps parameter 'a' from the current task to 'fieldA' in the new task
        },
        "data_type": {
          // Specifies the data type for the mapped parameter
          "fieldA": "string"
        },
        "group": "all", // Required when using 'data' mapping
        "ref": "", // Optional reference for the new task (empty in this example)
        "send_parent_data": false, // Do not include the entire parent task data in the copy
        "err_node_id": "error_condition_node", // ID of the node for error handling (example uses "61d548f082ba963bce688f44")
        "conv_id": 1023393, // ID of the target Process to receive the copied task
        "obj_to_id": null, // Not typically used for standard copy operations
        "user_id": 56171 // Internal user ID associated with the operation
      },
      {
        "type": "go", // Logic block for the successful path of the original task
        "to_node_id": "next_node_in_flow" // ID of the next node for the original task (example uses "61d548d782ba963bce688c62")
      }
    ],
    "semaphors": [] // Optional semaphores for implementing timeouts or concurrency control
  },
  "title": "Copy Task to Destination", // Descriptive title (example node had empty title)
  "description": "Copies the task, mapping parameter 'a' to 'fieldA', to Process 1023393.", // Optional description
  "x": 576, // X coordinate on canvas
  "y": 200, // Y coordinate on canvas

  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}", // UI settings
  "options": null // No specific options set
}
```

**Explanation:**

- **`type: "api_copy"`**: Identifies the node's function.
- **`conv_id: 1023393`**: Specifies the target Process ID where the copy will be sent.
- **`data` / `data_type`**: Define which parameters are copied and their types. Here, `{{a}}`
  dynamically takes the value of parameter `a` from the current task.
- **`group: "all"`**: Necessary when specifying data mappings in the `data` object.
- **`send_parent_data: false`**: Prevents the entire original task body from being copied.
- **`err_node_id`**: Points to the Condition node that initiates error handling if the copy
  operation fails.
- The original task continues to the `next_node_in_flow` after successfully initiating the copy.

## Node Patterns

### Basic Copy Task Pattern

```
Start Node → Process Logic → Copy Task Node → Continue Process Flow
                                   │
                                   └─── [copied task] ──→ Target Process
```

### Copy Task with Error Handling Pattern

```
                                 ┌─── [hardware error] ──→ Delay Node ──→ Retry Copy
                                 │
Start Node → Copy Task Node ─────┼─── [access denied] ──→ Error End Node
                                 │
                                 └─── [success] ──→ Continue Process Flow
```

### Parallel Processing Pattern

```
                    ┌─── Copy Task (Process A) ──→ Continue Main Flow
                    │
Start Node ─────────┼─── Copy Task (Process B) ──→ Continue Main Flow
                    │
                    └─── Copy Task (Process C) ──→ Continue Main Flow
```

## Default Escalation Pattern

When creating a Copy Task node in the Corezoid interface, the system automatically generates an
escalation pattern to handle errors. This pattern consists of:

1. **Condition Node** - Evaluates the type of error:

   - Checks `__conveyor_copy_task_return_type_error__` for "hardware" or "software" errors
   - Checks `__conveyor_copy_task_return_type_tag__` for specific error tags (e.g., "access_denied")
   - Routes tasks to appropriate handling paths

2. **Delay Node** - For hardware errors (connection issues, timeouts):

   - Implements a retry mechanism with configurable delay (default: 30 seconds)
   - Routes back to the original Copy Task node after the delay

3. **Error End Node** - For software errors (access denied, reference conflicts):
   - Marks the task as failed
   - Provides error details for debugging

The escalation pattern is automatically positioned to the right of the Copy Task node:

```
                                 ┌─── [hardware error] ──→ Delay Node ──→ Back to Copy Task
                                 │
Copy Task Node ──→ Condition Node ─┤
                                 │
                                 └─── [software error] ──→ Error End Node
```

To create this pattern automatically:

1. Select the Copy Task node
2. Click on the error message that says "Node must be connected to an error-handling node"
3. Click "Create escalation nodes" button in the node properties panel

## Node Naming Guidelines

When creating Copy Task nodes in your processes:

1. **Node Titles** should:

   - Clearly indicate what process is being called (e.g., "Copy to Notification Process" rather than
     just "Copy Task")
   - Reflect the purpose of the task copy in the context of your workflow
   - Be concise but descriptive enough to understand at a glance

2. **Node Descriptions** should:
   - Explain why this task is being copied to another process
   - Mention any important parameters being passed
   - Document any specific error handling considerations
   - Include information about what happens to both the original and copied tasks

Example of good naming:

- Title: "Send Order to Fulfillment"
- Description: "Copies order data to the fulfillment process while continuing payment processing.
  Passes order_id, items, and shipping details."

Example of poor naming:

- Title: "Copy"
- Description: "Sends data to another process"

Meaningful titles and descriptions make processes more maintainable, easier to troubleshoot, and
more accessible to other team members.

For additional examples of the Copy Task Node, see the node patterns above.
