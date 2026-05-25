# Code Node

## Purpose

- Runs custom user code to transform or compute data.
- Allows for complex logic, calculations, and data manipulation beyond what standard nodes provide.
- Executes in a boxed standalone v8 engine (version v8.1.97) with no web-interface support.

## Parameters

### Required

1. **src** (String)
   - JavaScript (preferred) or Erlang code to execute.
   - For JavaScript, directly manipulate the `data` object which contains the entire task body.
   - Example: `data.a = 1;` sets a parameter named "a" with value 1.
   - Validation: Must be valid JavaScript or Erlang syntax.
2. **extra** and **extra_type** (Objects)
   - Must include both parameters in the node configuration, even if empty.
   - The extra_type parameter is directly linked to the content of the extra parameter.
   - Validation: Both parameters must be present, even if empty objects.
3. **Error Node ID** (String)
   - Specifies which node to route to if code execution fails.
   - This parameter is required for all Code nodes to ensure proper error handling.
   - Each Code node should have its own dedicated error node.
   - Validation: Must reference a valid node ID in the process.

### Optional

1. **Timeout** (Number)
   - Maximum execution time in milliseconds.
   - Default: 1000ms (1 second)
   - Validation: Must be a positive integer.

## Available Libraries

Code nodes have access to several built-in libraries that can be imported using the `require()`
function:

- **Cryptographic Libraries**: SHA-1, MD5, HMAC, AES, etc.
- **Date and Time Libraries**: Date Utils, Moment.js, Moment Timezone
- **Other Libraries**: XRegExp, Rabbit

For detailed documentation of all available libraries and usage examples, see
[Code Node Libraries and Usage](code-node-libraries.md).

## Error Handling

- Corezoid distinguishes between two types of errors:
  - **Hardware errors**: Infrastructure or system-level failures (retried automatically)
  - **Software errors**: Code syntax or runtime errors (routed to error handling)
- Error routing is configured through a Condition node that checks
  `__conveyor_code_return_type_error__`
- Dedicated error nodes should be positioned to the right of the Code node

### Common Error Scenarios

1. **Syntax Errors** (Software)

   - Invalid JavaScript or Erlang syntax
   - Error tag: `api_code_syntax_error`
   - Error details available in: `__conveyor_code_return_description__`
   - Recommended handling: Fix code syntax in process design

2. **Runtime Errors** (Software)

   - Exceptions thrown during code execution
   - Error tag: `api_code_runtime_error`
   - Error details available in: `__conveyor_code_return_description__`
   - Recommended handling: Implement try/catch blocks in your code

3. **Timeout Errors** (Hardware)

   - Code execution exceeded the configured timeout
   - Error tag: `api_code_timeout`
   - Recommended handling: Optimize code or increase timeout value

4. **Memory Limit Errors** (Hardware)

   - Code exceeded memory allocation limits
   - Error tag: `api_code_memory_limit`
   - Recommended handling: Optimize memory usage or process data in smaller chunks

5. **Library Import Errors** (Software)
   - Attempting to import non-existent or unsupported libraries
   - Error tag: `api_code_import_error`
   - Recommended handling: Verify library names and availability

## Validation Rules

The Code node undergoes validation during process commit with these checks:

1. Code must be valid JavaScript or Erlang syntax
2. If `err_node_id` is specified, it must reference a valid node
3. `timeout` must be a positive integer if specified
4. Total code size must not exceed system limits

## Best Practices

- Include error handling within your code using try/catch blocks:
  ```javascript
  try {
    // Your code here
  } catch (e) {
    data.error = e.toString();
  }
  ```
- Always modify the `data` object directly rather than returning values
- For logging, use an artificial array key in the data object:
  ```javascript
  data._ = data._ || [];
  data._.push("Log message");
  ```
- Use Code nodes for dynamic array operations that can't be handled by Set Parameters nodes:
  ```javascript
  // Add an item to an array
  data.test_results.tests.push({
    name: data.current_test.name,
    status: "passed",
    result: data.result
  });
  ```
- Keep code modular and focused on a single responsibility
- Test your code thoroughly before deploying
- Avoid modifying system parameters (those starting with "\_\_")
- Each Code node should have its own dedicated error escalation node
- Position error nodes to the right of the Code node
- Console methods like `console.log()` have no effect - use data object for logging
- Minimize library usage to only what's necessary
- Avoid deep recursion and complex loops that might exceed stack limits
- Limit data size to stay within system constraints (2MB total task size)

## When to Use Code Nodes Instead of Set Parameters

In some scenarios, Code nodes are preferable to Set Parameters nodes:

1. **Dynamic Array Operations**: When you need to manipulate arrays dynamically (push, pop, splice,
   etc.)
2. **Complex Data Transformations**: When data requires complex transformations beyond simple
   key-value setting
3. **Conditional Logic**: When you need to apply conditional logic to determine what data to set
4. **Multiple Dynamic Elements**: When you need to use multiple dynamic elements at root level,
   which is not supported by Set Parameters nodes

For example, to add an item to an array in a Set Parameters node, you might try:

```json
"test_results.tests[{{test_results.tests.length}}]": "{\"name\":\"Test\",\"status\":\"passed\"}"
```

However, this approach has limitations with dynamic indices. Instead, use a Code node:

```javascript
data.test_results.tests.push({
  name: "Test",
  status: "passed"
});
```

## Using Semaphores in Code Nodes

Code nodes support both time and count semaphores to implement timeouts and concurrency control:

### Time Semaphores

Time semaphores can be used to implement timeouts for code execution. If the code doesn't complete
within the specified time, the task is routed to a timeout node:

```json
"semaphors": [
  {
    "type": "time",
    "value": 60,
    "dimension": "sec",
    "to_node_id": "timeout_node_id"
  }
]
```

This provides an alternative to the built-in timeout parameter and allows for more flexible timeout
handling.

### Count Semaphores

Count semaphores can be used to implement concurrency control for code execution. If the number of
concurrent executions reaches the threshold, new tasks are routed to an escalation node:

```json
"semaphors": [
  {
    "type": "count",
    "value": 50,
    "esc_node_id": "concurrency_limit_node_id"
  }
]
```

This can be used to prevent resource exhaustion when executing complex code that requires
significant CPU or memory resources.

## Related Documentation

- [Code Node Libraries and Usage](code-node-libraries.md) - Detailed documentation of available
  libraries and usage patterns
- [Best Practices for Building Fast and Effective Processes](../process/process-development-guide.md) -
  Optimization techniques for processes
- [Set Parameters Node](set-parameters-node.md) - When to use Set Parameters vs. Code nodes

## Node Patterns

### Basic Code Node Pattern

```
Start Node → Code Node → Continue Process Flow
```

### Code with Error Handling Pattern

```
                    ┌─── [hardware error] ──→ Delay Node ──→ Retry Code
                    │
Start Node → Code Node ─┼─── [software error] ──→ Error End Node
                    │
                    └─── [success] ──→ Continue Process Flow
```

### Data Transformation Pattern

```
Start Node → API Call Node → Code Node (Transform Response) → Continue Process Flow
```

### Dynamic Array Operations Pattern

```
Start Node → Code Node (Array Manipulation) → Continue Process Flow
```

## Configuration Example

This example demonstrates a basic Code Node configuration extracted from a real process. It includes
the JavaScript source code, connection to an error handling node, and the success path connection.

```json
{
  "id": "code_node_example", // Unique node ID (example uses "62a9825e513aa00bd6544e63")
  "obj_type": 0, // Object type for Logic node
  "condition": {
    "logics": [
      {
        "type": "api_code", // Specifies this is a Code logic block
        "lang": "js", // Language used (JavaScript)
        "src": "data.a = 1;\\n", // The actual JavaScript code to execute. This example sets a parameter 'a' to 1 in the task data.
        "err_node_id": "error_condition_node" // ID of the node to go to on code execution error (example uses "62a9825e513aa00bd6544e67")
      },
      {
        "type": "go", // Logic block for successful execution path
        "to_node_id": "next_node_in_flow" // ID of the next node on success (example uses "62a9827a82ba966e74498b81")
      }
    ],
    "semaphors": [] // Optional semaphores for implementing timeouts or concurrency control
  },
  "title": "Set Parameter A", // Descriptive title (example node had an empty title)
  "description": "Sets the task parameter 'a' to the value 1.", // Optional description (example node had empty description)
  "x": 720, // X coordinate on canvas
  "y": 256, // Y coordinate on canvas
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}", // UI settings (expanded form, default icon)
  "options": null // No specific options set
}
```

**Explanation:**

- **`type: "api_code"`**: Identifies this logic block as a Code Node execution.
- **`lang: "js"`**: Specifies JavaScript as the execution language.
- **`src: "data.a = 1;\\n"`**: Contains the code. It modifies the task data by adding or updating
  the parameter `a` with the value `1`. Note the escaped newline `\n`.
- **`err_node_id`**: Crucial for error handling. If the code in `src` fails (syntax error, runtime
  error, timeout, etc.), the task is routed to the node with this ID (typically a Condition node
  starting the error escalation pattern).
- **`type: "go"`**: Defines the path for successful code execution, routing the task to the
  `to_node_id`.

This configuration demonstrates the fundamental structure for executing custom code within a
Corezoid process, including the essential error handling connection.

## Default Configuration with Escalation Nodes

When creating a Code node in the Corezoid interface, the system automatically generates the
following default configuration:

```json
{
  "id": "code_node_id",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_code",
        "lang": "js",
        "src": "",
        "err_node_id": "error_node_id"
      }
    ],
    "semaphors": []
  },
  "title": "Code",
  "description": "",
  "modeForm": "expand",
  "active": true
}
```

The default escalation pattern for Code nodes consists of:

1. **Condition Node** - Evaluates the type of error:

   - Checks `__conveyor_code_return_type_error__` for "hardware" or "software" errors
   - Routes tasks to appropriate handling paths

2. **Delay Node** - For hardware errors:

   - Implements a retry mechanism with configurable delay (default: 30 seconds)
   - Routes back to the original Code node after the delay

3. **Error End Node** - For software errors (syntax errors, runtime errors):
   - Marks the task as failed
   - Provides error details for debugging

The escalation pattern is automatically positioned to the right of the Code node:

```
                           ┌─── [hardware error] ──→ Delay Node ──→ Back to Code Node
                           │
Code Node ──→ Condition Node ─┤
                           │
                           └─── [software error] ──→ Error End Node
```

To create this pattern automatically:

1. Select the Code node
2. Click on the error message that says "Node must be connected to an error-handling node"
3. Click "Create escalation nodes" button in the node properties panel

## Node Naming Guidelines

When creating Code nodes in your processes:

1. **Node Titles** should:

   - Clearly indicate the specific operation being performed (e.g., "Calculate Total Price" rather
     than just "Code")
   - Reflect the purpose of the code in the context of your process
   - Be concise but descriptive enough to understand at a glance

2. **Node Descriptions** should:
   - Explain what data is being processed
   - Mention any important input and output parameters
   - Document any specific error handling considerations
   - Include information about the algorithm or logic being implemented

Example of good naming:

- Title: "Calculate Shipping Cost"
- Description: "Calculates shipping cost based on weight, dimensions, and destination. Returns
  shipping_cost parameter."

Example of poor naming:

- Title: "JS Code"
- Description: "Runs JavaScript"

Meaningful titles and descriptions make processes more maintainable, easier to troubleshoot, and
more accessible to other team members.
