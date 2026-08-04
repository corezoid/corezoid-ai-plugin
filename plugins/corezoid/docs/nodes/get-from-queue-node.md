# Get from Queue Node

## Purpose

- Retrieves tasks from a designated **Queue** node for further processing.
- Enables asynchronous processing patterns and controlled task flow.
- Allows for decoupled task consumption from task production.

## Parameters

### Required

1. **Target Process** (String/ID)
   - The Process containing the Queue node.
   - Example: `"conv_id": 1023406`
   - Can be a dynamic value from the process task
   - Dynamic example: `"conv_id": "{{param1}}"` (with quotes for JSON format)
2. **Queue Node ID** (String)
   - ID of the specific Queue node to retrieve tasks from.
   - Example: `"node_id": "61d552d182ba963bce69adde"`
3. **Error Node ID** (String)
   - Specifies which node to route to if the get operation fails.
   - Example: `"err_node_id": "error_node_id"`

### Optional

1. **Order By** (String)
   - Sorting order for retrieving tasks (ASC or DESC).
   - Example: `"order_by": "ASC"`
2. **Batch size** (Number)
   - Number of tasks to retrieve at once.
3. **User ID** (Number)
   - User context for the operation.
   - Example: `"user_id": 56171`

## Error Handling

- Corezoid provides specific error parameters to handle different failure scenarios:
  - `__conveyor_get_task_return_type_error__`: Identifies error type ("hardware" or "software")
- Common error scenarios include:
  - Queue is empty (not treated as an error, but returns no tasks)
  - Hardware errors (infrastructure or system failures)
  - Access permission issues
  - Invalid queue node ID
- Implement retry logic with a Delay node for transient hardware errors
- Configure appropriate timeouts to prevent indefinite waiting

## Using Semaphores in Get from Queue Nodes

Get from Queue nodes support both time and count semaphores to implement timeouts and concurrency
control:

### Time Semaphores

Time semaphores can be used to implement timeouts for queue retrieval operations. If the retrieval
operation doesn't complete within the specified time, the task is routed to a timeout node:

```json
"semaphors": [
  {
    "type": "time",
    "value": 30,
    "dimension": "sec",
    "to_node_id": "retrieval_timeout_node_id"
  }
]
```

This provides a mechanism for handling retrieval operations that might take longer than expected.

### Count Semaphores

Count semaphores can be used to implement concurrency control for queue retrieval operations. If the
number of concurrent retrieval operations reaches the threshold, new tasks are routed to an
escalation node:

```json
"semaphors": [
  {
    "type": "count",
    "value": 50,
    "esc_node_id": "retrieval_limit_node_id"
  }
]
```

This can be used to prevent system overload when retrieving and processing tasks from queues.

## Best Practices

- Match the target Process ID and Queue node ID exactly
- Configure batch size carefully to optimize throughput
- Set appropriate polling intervals based on processing requirements
- Implement error handling for cases where the queue is empty
- Consider using timeouts to prevent indefinite waiting
- Monitor queue processing rates to ensure efficient task handling
- Position error handling nodes to the right of the Get from Queue node
- Implement comprehensive error handling with specific conditions for different error types
- Use descriptive node titles that indicate the purpose of the queue retrieval

## Node Naming Guidelines

When creating Get from Queue nodes in your processes:

1. **Node Titles** should:

   - Clearly indicate which queue is being accessed (e.g., "Get Tasks from Payment Queue" rather
     than just "Get from Queue")
   - Reflect the purpose of retrieving these tasks in the context of your workflow
   - Be concise but descriptive enough to understand at a glance

2. **Node Descriptions** should:
   - Explain what tasks are being retrieved and why
   - Mention any important processing that will happen to these tasks
   - Document any specific error handling considerations
   - Include information about batch size and ordering if relevant

Example of good naming:

- Title: "Process Pending Notifications"
- Description: "Retrieves pending notification tasks from the notification queue in batches of 10.
  Processes them in order of creation time (ASC)."

Example of poor naming:

- Title: "Get Queue"
- Description: "Gets tasks"

Meaningful titles and descriptions make processes more maintainable, easier to troubleshoot, and
more accessible to other team members.

## Accessing Retrieved Task Data

When a task is successfully dequeued, the Corezoid platform does **not** merge the task's fields
flat into the current task's root `data` object. The dequeued data is placed under the system
field `__queue_task_data__`:

```
__queue_task_data__
├── <field_1>
├── <field_2>
└── ...
```

### Referencing fields in subsequent nodes

Use the `{{__queue_task_data__.<field_name>}}` template syntax in Set Parameters nodes, Condition
nodes, Code nodes, and API Call nodes that follow the Get from Queue node:

```
{{__queue_task_data__.session_id}}
{{__queue_task_data__.amount}}
{{__queue_task_data__.user_email}}
```

### Copying fields into the root data object

If you need the retrieved fields at the root level, add a **Set Parameters** node immediately after
the Get from Queue node and copy each field explicitly:

| Parameter name | Value                                 |
| -------------- | ------------------------------------- |
| `session_id`   | `{{__queue_task_data__.session_id}}`  |
| `amount`       | `{{__queue_task_data__.amount}}`      |

### Empty queue

When the queue is empty, `__queue_task_data__` is absent. Guard against this in subsequent nodes
with a **Condition** node that checks whether the field exists before attempting to use it.

## Configuration Example

This example demonstrates a Get from Queue Node configuration extracted from a real process. It
shows how to retrieve tasks from a specific Queue node in another process.

```json
{
  "id": "get_queue_example", // Unique node ID (example uses "61d552e8513aa04bc969936b")
  "obj_type": 0, // Object type for Logic node
  "condition": {
    "logics": [
      {
        "type": "api_get_task", // Specifies this is a Get from Queue logic block
        "err_node_id": "error_condition_node", // ID for error handling (example uses "61d552e8513aa04bc9699371")
        "order_by": "ASC", // Retrieve tasks in ascending order (oldest first)
        "conv_id": 1023406, // ID of the target Process containing the Queue node
        "obj_to_id": null, // Not typically used for Get from Queue
        "user_id": 56171, // Internal user ID
        "node_id": "queue_node_id_in_target" // ID of the Queue node within Process 1023406 (example uses "61d552d182ba963bce69adde")
        // Note: Batch size is not specified, likely defaults to 1
      },
      {
        "type": "go", // Logic block for the path after successfully retrieving a task (or if queue is empty)
        "to_node_id": "next_node_in_flow" // ID of the next node (example uses "61d552dd82ba963bce69af8a")
      }
    ],
    "semaphors": [] // Optional semaphores for implementing timeouts or concurrency control
  },
  "title": "Get Task from Processing Queue", // Descriptive title (example node had empty title)
  "description": "Retrieves the oldest task (ASC order) from the queue node 'queue_node_id_in_target' in Process 1023406.", // Optional description
  "x": 392, // X coordinate on canvas
  "y": 200, // Y coordinate on canvas
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}", // UI settings
  "options": null // No specific options set
}
```

**Explanation:**

- **`type: "api_get_task"`**: Identifies the node's function.
- **`conv_id: 1023406`**: Specifies the Process ID where the source Queue node resides.
- **`node_id: "queue_node_id_in_target"`**: Crucially identifies the specific Queue node within the
  target process from which to retrieve tasks.
- **`order_by: "ASC"`**: Determines the retrieval order (Ascending, i.e., First-In, First-Out).
- **`err_node_id`**: Points to the Condition node for handling errors (e.g., hardware failure,
  access denied). Note that an empty queue is typically not considered an error by this node itself
  but might be handled by subsequent logic.
- The `go` logic block defines the path taken after the retrieval attempt. The retrieved task's data
  is **not** merged flat into the root `data` object. Instead, it is placed under the system field
  `__queue_task_data__` (see [Accessing Retrieved Task Data](#accessing-retrieved-task-data) below).
  If the queue was empty, the task proceeds and `__queue_task_data__` is absent.
