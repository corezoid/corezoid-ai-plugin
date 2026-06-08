# Set State Node

## Purpose

- Updates or assigns a "state" for the current task, useful for state diagrams or status tracking.
- Enables state-based workflows and status management.

## Parameters

### Required

1. **State Name** (String)
   - Identifier for the state.

### Optional

1. **Additional parameters** (Key-Value)
   - Stored in the state record.

## Error Handling

- Invalid or locked state references cause node errors.
- Frequent or unnecessary state changes can degrade performance.

## Best Practices

- Use a consistent naming convention for states
- Combine with Condition or other node logic for advanced state-driven flows
- Monitor state transitions for unexpected patterns
- Consider using states for reporting and analytics
- Use descriptive state names that clearly indicate the task's status
- Avoid excessive state changes that could impact performance

## Node Naming Guidelines

When creating Set State nodes in your processes:

1. **Node Titles** should:

   - Clearly indicate the state being set (e.g., "Set Order to Processing" rather than just "Set
     State")
   - Reflect the status change in the context of your workflow
   - Be concise but descriptive enough to understand at a glance

2. **Node Descriptions** should:
   - Explain what state is being set and why
   - Mention any important parameters being stored with the state
   - Document any specific workflow considerations
   - Include information about what this state means in the process

Example of good naming:

- Title: "Mark Order as Shipped"
- Description: "Sets order state to 'shipped' and records shipping_date and tracking_number.
  Triggers notification to customer."

Example of poor naming:

- Title: "Set State"
- Description: "Changes status"

Meaningful titles and descriptions make processes more maintainable, easier to troubleshoot, and
more accessible to other team members.

## Context: State Diagrams

The Set State Node is primarily used within **State Diagrams**, a specific type of Corezoid process
(`"conv_type": "state"`). State diagrams are optimized for implementing state automata, where tasks
persist in non-final nodes representing specific states.

**Key characteristics of State Diagrams:**

- **Purpose:** Ideal for managing lifecycles (e.g., customer status, order progress) or creating
  simple task storage mechanisms.
- **Persistence:** Tasks remain in Set State nodes until explicitly moved by another process or
  logic.
- **Allowed Nodes:** Only a subset of nodes can be used: Start, Code, Copy Task, Modify Task, Set
  State, Delay, Queue, Set Parameters, End.
- **Transitions:** Logic within the state diagram or external processes typically trigger
  transitions between states.

## Configuration Example (in a State Diagram)

This example demonstrates a Set State Node configuration extracted from a real state diagram process
(`1023399_User_states.conv.json`). It represents the "Active" state in a user lifecycle.

```json
{
  "id": "61d55218513aa04bc969791a", // Unique node ID representing the "Active" state
  "obj_type": 3, // Object type for State node
  "condition": {
    "logics": [
      // Logic defining transitions *out* of this state
      {
        "type": "go_if_const", // Conditional transition
        "to_node_id": "64d3b1f2513aa04e113022a6", // Target state: "Blocked"
        "conditions": [
          {
            "param": "a", // Check parameter 'a' in the task data
            "const": "1", // If 'a' equals 1...
            "fun": "eq", // ...using equals comparison...
            "cast": "number" // ...treating 'a' as a number.
          }
        ]
      },
      {
        "type": "go", // Default transition (if condition above is false)
        "to_node_id": "61d55237513aa04bc9697cb1" // Target state: "Inactive"
      }
    ],
    "semaphors": [] // No semaphores used in this state node
  },
  "title": "Active", // The name of the state this node represents
  "description": "", // Optional description of the state
  "x": 600, // X coordinate on canvas
  "y": 204, // Y coordinate on canvas
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}", // UI settings
  "options": null // No specific options set
}
```

**Explanation:**

- **`obj_type: 3`**: Identifies this as a State node within a state diagram.
- **`title: "Active"`**: This is the **State Name**. Tasks entering this node are considered to be
  in the "Active" state.
- **`condition.logics`**: Defines the rules for transitioning _out_ of the "Active" state.
  - The `go_if_const` logic checks if the task parameter `a` equals `1`. If true, the task
    transitions to the "Blocked" state (`64d3b1f2513aa04e113022a6`).
  - The `go` logic defines the default transition. If `a` is not `1`, the task transitions to the
    "Inactive" state (`61d55237513aa04bc9697cb1`).
- Tasks remain in this "Active" state node until one of the transition conditions is met (triggered
  by modifications to the task, often from another process interacting with this state diagram).
- Unlike nodes in regular processes, the primary function here is to _represent_ the state itself,
  with the logic defining how to _leave_ that state.

## Accessing Data from State Diagram Nodes

Once a task is stored within a Set State node (representing a specific state in a State Diagram),
you can retrieve the task's data (or specific parameters within it) from other processes or nodes
(like Set Parameters) using a special syntax.

**Syntax Template:**

```
{{conv[<StateDiagramID>].ref[<TaskReference>].<ParameterName>}}
```

**Components:**

1.  **`conv`**: A required keyword indicating you are accessing data from a State Diagram.
2.  **`<StateDiagramID>`**: Identifies the State Diagram process containing the desired state/task.
    This can be:
    - A **Static Process ID** (e.g., `1023399`).
    - A **Variable** holding the Process ID (e.g., `{{process_id_variable}}`).
    - An **Alias Name** assigned to the State Diagram (e.g., `@user-states`).
3.  **`.ref`**: A required keyword indicating you want to reference a specific task within that
    State Diagram.
4.  **`<TaskReference>`**: Identifies the specific task stored in one of the State Diagram's nodes.
    This can be:
    - A **Static Task Reference ID** (e.g., `12345`).
    - A **Variable** holding the Task Reference ID (e.g., `{{task_ref_variable}}`).
5.  **`.<ParameterName>`** (Optional): The specific parameter you want to retrieve from the task's
    data.
    - If omitted, the entire task data object is returned.
    - If included (e.g., `.amount_owed`), only the value of that specific parameter is returned.

**Examples:**

- **Get the entire task object using static IDs:**
  ```
  {{conv[1023399].ref[12345]}}
  ```
- **Get a specific parameter (`amount_owed`) using static IDs:**
  ```
  {{conv[1023399].ref[12345].amount_owed}}
  ```
- **Get a specific parameter using variables for Process ID and Task Reference:**
  ```
  {{conv[{{my_process_id}}].ref[{{my_task_ref}}].amount_owed}}
  ```
- **Get the entire task object using a State Diagram alias and a variable Task Reference:**
  ```
  {{conv[@user-states].ref[{{my_task_ref}}]}}
  ```
  Here `@user-states` is a Corezoid alias pointing to the State Diagram process. Aliases are
  managed via the `corezoid-alias-manager` skill.

This syntax allows other parts of your system to interact with and retrieve data persisted within
State Diagrams.
