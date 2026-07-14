# Reply to Process Node

## Purpose

- Sends a response to a **Call a Process** node in the original Process, finalizing a synchronous
  interaction.
- Enables request-response patterns between processes.
- Returns computed or processed data back to the calling process.

## Parameters

### Required

1. **Response Data** (Object)
   - Key-value pairs to be returned to the calling process.
   - Example: `"res_data": {"res": "{{res}}"}`
   - Validation: Must be a valid JSON object with properly formatted values.
   - **Every value must be a string** — either a `"{{variable}}"` template or a plain
     string literal. Literal non-string values (`[]`, `{}`, `0`, `true`, `null`) pass
     JSON-schema validation but hang the server-side commit when the process is pushed
     through the API (`push-process` fails with the opaque "no response from server").
     To return an empty array / zero / etc., set it in an upstream Code node
     (`data.links = []`) and reference it as `"{{links}}"` with the real type declared
     in `res_data_type`. `lint-process` flags this pattern.
2. **Response Data Type** (Object)
   - Specifies the data types of the returned values.
   - Example: `"res_data_type": {"res": "string"}`
   - Validation: Must match the structure of the Response Data object.

### Optional

1. **Mode** (String)
   - Format of the response data (e.g., "key_value").
   - Validation: Must be one of the allowed values ("key_value", "raw").
2. **Throw Exception** (Boolean)
   - Whether to throw an exception with an error message.
   - Default: `false`
   - Validation: Must be a boolean value.

## Interaction

- Once invoked, the calling Process's node resumes and updates the local task with returned fields.
- The response data is merged into the original task in the calling process.
- When `throw_exception` is set to `true`, the calling process will route to its error handling
  path.

## Error Handling

- If the caller did not request a reply, this node's data is ignored.
- Mismatch or rename confusion can result in incomplete data for the caller.
- When throwing exceptions, you must include an exception message in the configuration.
- The calling process can detect exceptions using the `__conveyor_rpc_reply_return_type_tag__`
  parameter.
- **Important**: When a Reply to Process node throws an exception (with `throw_exception: true`),
  the task will be routed to the error node specified in the `err_node_id` parameter of the Call
  Process node in the calling process.
- Error handling nodes in the calling process should check for expected error conditions and handle
  them appropriately.

### Common Error Scenarios

1. **Missing Exception Message**

   - When `throw_exception` is `true` but no error message is provided
   - Error tag: `api_rpc_reply_missing_error_message`
   - Recommended handling: Always include an error_message field in the response data

2. **Type Mismatch**

   - When the data type specified in `res_data_type` doesn't match the actual data
   - Error tag: `api_rpc_reply_type_mismatch`
   - Recommended handling: Ensure data types match the values being returned

3. **Invalid Variable Reference**

   - When a variable referenced in `res_data` doesn't exist in the task
   - Error tag: `api_rpc_reply_invalid_reference`
   - Recommended handling: Verify all referenced variables exist before the Reply node

4. **Malformed JSON**
   - When the response data contains invalid JSON syntax
   - Error tag: `api_rpc_reply_malformed_json`
   - Recommended handling: Validate JSON structure before configuring the node

## Validation Rules

The Reply to Process node undergoes validation during process commit with these checks:

1. If `throw_exception` is `true`, an error message must be included in the response data
2. The structure of `res_data_type` must match the structure of `res_data`
3. All variable references must use the correct syntax: `{{variable_name}}`
4. The `mode` parameter must be one of the allowed values if specified

## Using Semaphores in Reply to Process Nodes

Reply to Process nodes support both time and count semaphores to implement timeouts and concurrency
control:

### Time Semaphores

Time semaphores can be used to implement timeouts for reply operations. If the reply operation
doesn't complete within the specified time, the task is routed to a timeout node:

```json
"semaphors": [
  {
    "type": "time",
    "value": 30,
    "dimension": "sec",
    "to_node_id": "reply_timeout_node_id"
  }
]
```

The `dimension` parameter can have the following values:

- `"sec"` - seconds
- `"min"` - minutes
- `"hour"` - hours
- `"day"` - days

This provides a mechanism for handling reply operations that might take longer than expected,
especially when returning large data structures.

### Count Semaphores

Count semaphores can be used to implement concurrency control for reply operations. If the number of
concurrent replies reaches the threshold, new tasks are routed to an escalation node:

```json
"semaphors": [
  {
    "type": "count",
    "value": 50,
    "esc_node_id": "reply_limit_node_id"
  }
]
```

This can be used to prevent system overload when many processes are replying simultaneously.

## Best Practices

- Every Final node should be preceded by a Reply to Process node
- For error/exception paths, the Reply node should throw an exception with an appropriate result
  description
- For success paths, the Reply node should return result="success" with the payload
- Maintain consistent naming to ensure the caller correctly parses returned data
- Only return essential fields for efficiency
- Position the node between data processing nodes and the Final node
- Use descriptive node titles that indicate what data is being returned
- Combine all returned data into a single payload object rather than separate parameters
- Use double curly braces syntax for variable references: `{{variable_name}}`
- **Always properly stringify object values with escaped quotes**
  - Object values must be JSON strings with properly escaped quotes
  - Example: `"payload": "{\"id\":\"{{id}}\",\"data\":{\"key\":\"{{value}}\"}}"` 
  - This ensures proper JSON formatting and prevents validation errors

### Standard Response Format

The Reply to Process node supports two response format modes:

#### Mode: "key_value" (Object Format)

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": {
    "result": "success",
    "payload": "{{payload}}"
  },
  "res_data_type": {
    "result": "string",
    "payload": "object"
  },
  "throw_exception": false
}
```

For error responses with key_value mode:

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": {
    "result": "error",
    "error_message": "{{error_message}}"
  },
  "res_data_type": {
    "result": "string",
    "error_message": "string"
  },
  "throw_exception": true
}
```

#### Mode: "keys" (Array Format)

```json
{
  "type": "api_rpc_reply",
  "mode": "keys",
  "res_data": ["result", "operation", "num1", "num2", "calculated_result"],
  "res_data_type": ["string", "string", "string", "string", "number"],
  "throw_exception": false
}
```

For error responses with keys mode:

```json
{
  "type": "api_rpc_reply",
  "mode": "keys",
  "res_data": ["result", "error_message"],
  "res_data_type": ["string", "string"],
  "throw_exception": true
}
```

> **Important**: The Reply to Process node must use one of these two formats exactly:
>
> 1. `mode = "keys"` with array format for res_data and res_data_type
> 2. `mode = "key_value"` with object format for res_data and res_data_type
>
> Using incorrect format combinations will result in validation errors during process upload.

### Handling Complex Object Values

When returning complex objects in a Reply to Process node, you must properly stringify them with escaped quotes:

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": {
    "result": "success",
    "simple_value": "{{simple_value}}",
    "complex_object": "{\"id\":\"{{id}}\",\"metadata\":{\"created\":\"{{created_at}}\",\"type\":\"{{type}}\"}}"
  },
  "res_data_type": {
    "result": "string",
    "simple_value": "string",
    "complex_object": "object"
  },
  "throw_exception": false
}
```

**Note**: When setting object values, you must:
1. Properly escape all quotes inside the JSON string with backslashes
2. Use a single set of quotes around the entire string
3. Set the correct data type in `res_data_type` (usually "object" for objects and "array" for arrays)

### Real-World Example

Here's a real-world example of a Reply to Process node with properly stringified object values:

```json
{
  "id": "67ffa3bf82ba966c7f954281",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_rpc_reply",
        "mode": "key_value",
        "res_data": {
          "result": "success",
          "data": "{\"positive_integers\":\"{{positive_integers}}\",\"negative_integers\":\"{{negative_integers}}\",\"strings\":\"{{strings}}\"}"
        },
        "res_data_type": {
          "result": "string",
          "data": "object"
        },
        "throw_exception": false
      },
      {
        "type": "go",
        "to_node_id": "67ffa3bf513aa034c895b83c"
      }
    ],
    "semaphors": []
  },
  "title": "Reply with Result",
  "description": "Send result back to caller",
  "x": 500,
  "y": 500,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}

## Process Output Parameters

When using Reply to Process nodes, it's important to align the response data with the process's
defined output parameters. Output parameters define the interface of data returned from a process
when it's called by another process or through an API.

Output parameters are defined in the process configuration using the `params` array with the
`output` flag:

```json
"params": [
  {
    "name": "result",
    "type": "string",
    "descr": "Operation result status",
    "flags": ["output"],
    "regex": "",
    "regex_error_text": ""
  }
]
```

The Reply to Process node should include all defined output parameters in its response data,
ensuring type consistency and complete responses.

For detailed documentation on defining and using output parameters, see
[Process Output Parameters](../process/process-output-parameters.md).

## Related Documentation

- [Call a Process Node](call-process-node.md) - Documentation for the node that initiates the
  request
- [Process Output Parameters](../process/process-output-parameters.md) - Documentation for defining
  output parameters
- [Best Practices for Building Fast and Effective Processes](../process/process-development-guide.md) -
  Optimization techniques for processes

## Configuration Example

This example demonstrates a Reply to Process Node configuration extracted from a real process. It
shows how to return a calculated result (`res`) back to the calling process.

```json
{
  "id": "reply_node_example", // Unique node ID (example uses "61d549ae82ba963bce68a55f")
  "obj_type": 0, // Object type for Logic node
  "condition": {
    "logics": [
      {
        "type": "api_rpc_reply", // Specifies this is a Reply to Process logic block
        "res_data": {
          // Data to be returned to the calling process
          "res": "{{res}}" // Returns the value of the 'res' parameter from the current task
        },
        "res_data_type": {
          // Specifies the data type of the returned parameter
          "res": "string" // Assuming 'res' was calculated as a string or number convertible to string
        },
        "mode": "key_value", // Response format mode
        "throw_exception": false // Do not throw an exception (indicates success)
      },
      {
        "type": "go", // Logic block for the path after sending the reply
        "to_node_id": "final_node_id" // ID of the next node, typically a Final node (example uses "61d5497c513aa04bc968792a")
      }
    ],
    "semaphors": [] // Optional semaphores for implementing timeouts or concurrency control
  },
  "title": "Return Result", // Descriptive title (example uses "return res;")
  "description": "Sends the calculated 'res' value back to the calling process.", // Optional description
  "x": 576, // X coordinate on canvas
  "y": 212, // Y coordinate on canvas

  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}", // UI settings
  "options": null // No specific options set
}
```

**Explanation:**

- **`type: "api_rpc_reply"`**: Identifies the node's function.
- **`mode: "key_value"`**: Specifies that the response data is structured as key-value pairs.
- **`res_data` / `res_data_type`**: Define the data being returned. Here, the value of the task
  parameter `res` is returned as a string.
- **`throw_exception: false`**: Indicates a successful response. If set to `true`, the calling
  process's error handling path would be triggered.
- The `go` logic block defines where the task proceeds _after_ the reply has been sent (usually to a
  Final node).

## Node Patterns

### Basic Reply Pattern

```
Start Node → Process Logic → Reply to Process Node → End Node
```

### Reply with Error Handling Pattern

```
                                 ┌─── [success case] ──→ Reply (success) ──→ End Node
                                 │
Start Node → Process Logic ──────┤
                                 │
                                 └─── [error case] ──→ Reply (exception) ──→ End Node
```

### Reply in API Call Pattern

```
Start Node → API Call Node → Reply to Process Node → End Node
```

## Node Naming Guidelines

When creating Reply to Process nodes in your processes:

1. **Node Titles** should:

   - Clearly indicate what data is being returned (e.g., "Return Validation Results" rather than
     just "Reply to Process")
   - Reflect the purpose of the response in the context of your process
   - Be concise but descriptive enough to understand at a glance

2. **Node Descriptions** should:
   - Explain what data is being returned and why
   - Mention any important transformations or formatting applied to the response
   - Document any specific error handling considerations
   - Include information about how the response will be used by the calling process

Example of good naming:

- Title: "Return Payment Processing Results"
- Description: "Returns payment status, transaction ID, and timestamp to the calling process. Throws
  exception with detailed error message if payment failed."

Example of poor naming:

- Title: "Reply"
- Description: "Sends data back"

Meaningful titles and descriptions make processes more maintainable, easier to troubleshoot, and
more accessible to other team members.
