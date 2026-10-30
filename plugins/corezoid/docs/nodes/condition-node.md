# Condition Node

## Purpose

- Evaluates parameter-based conditions to branch tasks into different paths based on data values.
- Creates decision points in the Process flow.
- Enables error handling and routing based on system parameters.

## Parameters

### Required

1. **Condition Expression** (Object)
   - Logical expression that evaluates to true or false.
   - Supports specific comparison functions (see Allowed Functions below).
   - Can check parameter existence, value types, and content.
   - Example:
     ```json
     {
       "conditions": [
         {
           "param": "a",
           "const": "1",
           "fun": "eq",
           "cast": "number"
         }
       ]
     }
     ```

### Allowed Functions

The following functions are allowed in condition expressions:

1. **eq** - Equal to
2. **not_eq** - Not equal to
3. **less** - Less than
4. **more** - Greater than
5. **less_or_eq** - Less than or equal to
6. **more_or_eq** - Greater than or equal to
7. **regexp** - Regular expression match

> **Important**: Only these functions are allowed in condition expressions. Using other functions
> (such as "not_in") will result in validation errors during process upload.

### Optional

1. **Default Path** (Node ID)
   - Where to route tasks if no conditions match.
   - Example: `"to_node_id": "default_node_id"`
2. **Multiple Conditions** (Array)
   - Sequence of conditions to evaluate in order.
   - Example: Multiple entries in the "logics" array

### Templates and missing keys (verified)

- **Nested paths need braces in `param`.** `"param": "{{customer.id}}"` resolves the path; a bare
  `"param": "customer.id"` does not (it is compared as an empty value). A bare top-level name
  (`"param": "status"`) works.
- **A missing key or element equals `""`.** `{{a.id}}` when `a` is `{}` or absent, and
  `{{items[0]}}` when `items` is `[]` or absent, satisfy `eq ""` and do not satisfy `not_eq ""`.
  So "is it empty / is the array non-empty" can be checked directly, without a normalising Set
  Parameters or Code node:
  ```json
  { "param": "{{items[0].id}}", "const": "", "fun": "eq", "cast": "string" }
  ```
- **`const` accepts a template**, so two task values can be compared without Code:
  ```json
  { "param": "{{found[0].ref}}", "const": "{{current_ref}}", "fun": "eq", "cast": "string" }
  ```
- With `cast: "number"`, `more` / `less` on a missing key route to the default path (no error).

## Error Handling

- Condition nodes are central to error handling in Corezoid processes:
  - They evaluate system parameters like `__conveyor_api_return_type_error__` to detect errors
  - They route tasks to appropriate error handling paths based on error types
  - They distinguish between hardware and software errors for different handling strategies
- Common error handling patterns include:
  - Checking for specific error types (e.g., `"__conveyor_code_return_type_error__": "hardware"`)
  - Routing hardware errors to retry paths with Delay nodes
  - Routing software errors to dedicated error nodes
  - Implementing validation checks before main process logic
- If no conditions match and no default path is specified, the task may become stuck
- Type mismatches in comparisons can lead to unexpected routing

## Using Semaphores in Condition Nodes

Condition nodes support both time and count semaphores to implement timeouts and concurrency
control:

### Time Semaphores

Time semaphores can be used to implement timeouts for condition evaluation. If the condition
evaluation doesn't complete within the specified time, the task is routed to a timeout node:

```json
"semaphors": [
  {
    "type": "time",
    "value": 10,
    "dimension": "sec",
    "to_node_id": "condition_timeout_node_id"
  }
]
```

This provides a mechanism for handling complex condition evaluations that might take longer than
expected.

### Count Semaphores

Count semaphores can be used to implement concurrency control for condition evaluations. If the
number of concurrent condition evaluations reaches the threshold, new tasks are routed to an
escalation node:

```json
"semaphors": [
  {
    "type": "count",
    "value": 100,
    "esc_node_id": "condition_limit_node_id"
  }
]
```

This can be used to prevent system overload when evaluating complex conditions.

## Best Practices

- **Always provide a default path** to prevent tasks from becoming stuck
  - Every condition node **must** include a default "go" logic as the last entry in the "logics"
    array
  - The default path handles cases where none of the specific conditions match
  - Omitting the default path will result in a 'Condition node has no link' error
- Use the "cast" property to ensure proper type comparison
- Order conditions from most specific to most general
- Implement parameter validation before complex condition logic
- For complex conditions, consider using a Code node instead
- Use descriptive node titles that indicate what is being evaluated
- Position error handling nodes to the right of the main process flow
- Use condition nodes after operation nodes to check for specific error types
- Implement comprehensive error handling with specific conditions for different error types
- For validation checks, route failed validations to error nodes with descriptive titles
- Use the "go_if_const" type for conditional paths and "go" type for default paths

## Basic Configuration Example

This example shows a simple Condition Node that checks if the input parameter `a` is equal to the
number `1`.

```json
{
  "id": "check_param_a", // Unique node ID (example uses "61d55237513aa04bc9697cb1")
  "obj_type": 0, // Object type for Logic node (Note: Standalone Condition nodes might use obj_type 3)
  "condition": {
    "logics": [
      {
        "type": "go_if_const", // Route if condition is met
        "to_node_id": "error_node_id", // Node to go to if a == 1 (example uses "64d3b1f2513aa04e113022a6")
        "conditions": [
          {
            "param": "a", // Parameter to check in the task data
            "const": "1", // Constant value to compare against
            "fun": "eq", // Comparison function (equal to)
            "cast": "number" // Treat the parameter 'a' as a number for comparison
          }
        ]
      },
      {
        "type": "go", // Default path if the condition above is false
        "to_node_id": "success_node_id" // Node to go to if a != 1 (example uses "61d55218513aa04bc969791a")
      }
    ],
    "semaphors": [] // Optional semaphores for implementing timeouts or concurrency control
  },
  "title": "Check if a equals 1", // Descriptive title (example node had empty title)
  "description": "Routes to error node if input parameter 'a' is 1, otherwise proceeds to success node.", // Optional description
  "x": 392, // X coordinate on canvas
  "y": 204, // Y coordinate on canvas
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}", // UI settings
  "options": null // No specific options set
}
```

**Explanation:**

- The first logic block uses `go_if_const` to check if the parameter `a` (cast to a number) is equal
  (`eq`) to `1`. If true, the task goes to `error_node_id`.
- The second logic block uses `go` to define the default path. If the first condition is false
  (i.e., `a` is not equal to `1`), the task goes to `success_node_id`.
- This demonstrates a fundamental branching pattern based on a task parameter's value.

## Implementation Example

Here's a complete example of a Condition Node:

```json
{
  "id": "condition_node",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "go_if_const",
        "conditions": [
          {
            "param": "status",
            "const": "success",
            "fun": "eq",
            "cast": "string"
          }
        ],
        "to_node_id": "success_node"
      },
      {
        "type": "go_if_const",
        "conditions": [
          {
            "param": "__conveyor_api_return_type_error__",
            "const": "hardware",
            "fun": "eq",
            "cast": "string"
          }
        ],
        "to_node_id": "retry_node"
      },
      {
        "type": "go",
        "to_node_id": "default_node"
      }
    ],
    "semaphors": []
  },
  "title": "Check Status",
  "description": "Route task based on status and error type",
  "x": 944,
  "y": 200,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

### Error Handling Pattern

Condition nodes are central to error handling in Corezoid processes. Here's a common pattern for
handling different error types:

```
API Call Node
    |
    v
Condition Node
    |
    ├─── [hardware error] ──→ Delay Node ──→ Retry API Call
    |
    ├─── [software error] ──→ Error Handling Node
    |
    └─── [success] ──→ Continue Process Flow
```

## Node Patterns

### Basic Condition Pattern

```
                    ┌─── [condition A] ──→ Path A
                    │
Start Node → Condition Node ─┼─── [condition B] ──→ Path B
                    │
                    └─── [default] ──→ Default Path
```

### Validation Pattern

```
                    ┌─── [invalid input] ──→ Error Node
                    │
Start Node → Condition Node ─┤
                    │
                    └─── [valid input] ──→ Process Logic
```

### API Error Handling Pattern

```
API Call Node → Condition Node ─┬─── [hardware error] ──→ Delay Node ──→ Retry API Call
                    │
                    ├─── [software error] ──→ Error Handling Node
                    │
                    └─── [success] ──→ Continue Process Flow
```

## Default Configuration with Escalation Nodes

When creating a Condition node in the Corezoid interface, the system automatically generates the
following default configuration:

```json
{
  "id": "condition_node_id",
  "obj_type": 3,
  "condition": {
    "logics": [
      {
        "type": "go_if_const",
        "to_node_id": null,
        "conditions": []
      },
      {
        "type": "go",
        "to_node_id": null
      }
    ],
    "semaphors": []
  },
  "title": "Condition",
  "description": "",
  "modeForm": "expand",
  "active": true
}
```

Unlike API Call and other operation nodes, Condition nodes do not have automatic escalation nodes
created by default, as they are typically part of the escalation pattern themselves. The default
configuration includes:

1. A primary condition path (`go_if_const`) where you define your conditions
2. A default path (`go`) for when no conditions match
3. Empty semaphors array for optional time-based or resource-based conditions

When used in an API Call escalation pattern, the Condition node is configured with specific
error-checking conditions:

```json
{
  "logics": [
    {
      "type": "go_if_const",
      "to_node_id": "delay_node_id",
      "conditions": [
        {
          "fun": "eq",
          "const": "hardware",
          "param": "__conveyor_api_return_type_error__",
          "cast": "string"
        }
      ]
    },
    {
      "type": "go_if_const",
      "to_node_id": "error_node_id",
      "conditions": [
        {
          "fun": "eq",
          "const": "software",
          "param": "__conveyor_api_return_type_error__",
          "cast": "string"
        }
      ]
    },
    {
      "type": "go",
      "to_node_id": "error_node_id"
    }
  ]
}
```

## Node Naming Guidelines

When creating Condition nodes in your processes:

1. **Node Titles** should:

   - Clearly indicate what is being evaluated (e.g., "Check Payment Status" rather than just
     "Condition")
   - Reflect the decision point in the context of your process
   - Be concise but descriptive enough to understand at a glance

2. **Node Descriptions** should:
   - Explain what conditions are being evaluated
   - Mention the possible outcomes and routing paths
   - Document any specific error handling considerations
   - Include information about the parameters being checked

Example of good naming:

- Title: "Validate Customer Age"
- Description: "Checks if customer is over 18 years old. Routes to adult verification if true,
  underage handling if false."

Example of poor naming:

- Title: "If"
- Description: "Checks condition"

Meaningful titles and descriptions make processes more maintainable, easier to troubleshoot, and
more accessible to other team members.
