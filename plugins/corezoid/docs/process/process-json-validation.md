# Process JSON Validation

This document outlines the validation requirements for Corezoid process JSON files.

## Extra and ExtraType Parameter Requirements

When configuring nodes with parameters, you must ensure that:

1. All parameters defined in `extra` have corresponding entries in `extra_type`
2. All entries in `extra_type` have corresponding parameters in `extra`
3. The data types in `extra_type` match the actual data types of the values in `extra`

### Common Extra/ExtraType Validation Errors

If you encounter the following error when uploading a process:

```
Wrong params in extra or extraType
```

This indicates that there is a mismatch between the `extra` and `extra_type` objects in one or more
nodes. Check:

1. That all keys in `extra` exist in `extra_type` and vice versa
2. That the data types specified in `extra_type` match the actual data types in `extra`
3. That all required parameters for the node type are present in both `extra` and `extra_type`


## Object Type Requirements

When creating or modifying process JSON files, the `obj_type` field must use the correct numeric
value:

1. **Process Objects**: Use `"obj_type": 1`
2. **Folder Objects**: Use `"obj_type": 0`
3. **Dashboard Objects**: Use `"obj_type": 3`
4. **Configuration Objects**: Use `"obj_type": 4`
5. **Project Objects**: Use `"obj_type": 5`
6. **Stage Objects**: Use `"obj_type": 6`
7. **Version Objects**: Use `"obj_type": 7`

For nodes within a process, use these numeric values:

1. **Start Node**: Use `"obj_type": 1`
2. **End Node**: Use `"obj_type": 2`
3. **Other Nodes**: Use `"obj_type": 3` for condition nodes, `"obj_type": 0` for code nodes, etc.

Using string values (like "conv", "folder") instead of numeric values will cause validation errors
when uploading processes.


## Node Type-Specific Requirements

### Call Process Nodes

When creating Call Process nodes (nodes that call another process), you must use the following
structure:

```json
{
  "type": "api_rpc",
  "conv_id": "process_id_to_call",
  "wait_for_reply": true,
  "extra": {
    "param1": "value1",
    "param2": 2
  },
  "extra_type": {
    "param1": "string",
    "param2": "number"
  },
  "err_node_id": "error_node_id"
}
```

Common validation errors for Call Process nodes:

- Using `"type": "call_process"` instead of `"type": "api_rpc"`
- Using `data` and `data_type` instead of `extra` and `extra_type`
- Missing the required `err_node_id` parameter
- Type mismatch between the called process's input parameter types and the types specified in
  `extra_type`
- Ensure numeric parameters use "number" type in `extra_type` when the target process expects
  numbers

#### Error Handling in Call Process Nodes

When a called process throws an exception via its Reply to Process node (with
`throw_exception: true`), the task will be routed to the error node specified in the `err_node_id`
parameter of the Call Process node. The error handling node should:

1. Check for expected error conditions using `go_if_const` logic
2. Route expected errors to appropriate handling nodes
3. Handle unexpected errors separately

Example of an error handling node for Call Process:

```json
{
  "id": "error_node_id",
  "obj_type": 3,
  "condition": {
    "logics": [
      {
        "type": "go_if_const",
        "to_node_id": "expected_error_handler_node_id",
        "conditions": [
          {
            "fun": "eq",
            "const": "Expected error message",
            "param": "error_message",
            "cast": "string"
          }
        ]
      },
      {
        "type": "set_param",
        "extra": {
          "error_details": "Unexpected error: {{error_message}}"
        },
        "extra_type": {
          "error_details": "string"
        },
        "err_node_id": "critical_error_node_id"
      },
      {
        "type": "go",
        "to_node_id": "unexpected_error_handler_node_id"
      }
    ]
  }
}
```

### Set Parameters Nodes

All Set Parameters nodes must include an `err_node_id` parameter pointing to an error handling node.
Additionally, when working with object values, they must be stringified:

```json
{
  "type": "set_param",
  "extra": {
    "param1": "value1",
    "object_param": "{\"key1\":\"value1\",\"key2\":\"value2\"}"
  },
  "extra_type": {
    "param1": "string",
    "object_param": "object"
  },
  "err_node_id": "error_node_id"
}
```

Important notes for Set Parameters nodes:

1. All numeric values must be formatted as strings in the `extra` object with corresponding "string"
   type in `extra_type`
2. Object values must be stringified (JSON.stringify) in the `extra` object with "object" type in
   `extra_type`. For example:
   - Correct: `"object_param": "{\"key\":\"value\"}"`
   - Incorrect: `"object_param": {"key":"value"}`
3. All keys in `extra` must have matching keys in `extra_type` with the correct data type
4. The `err_node_id` parameter is required for all Set Parameters nodes

### Common Rules for Parameter Stringification

The following rules apply to all nodes that use `extra`/`extra_type` or `res_data`/`res_data_type` parameters:

1. All values must be valid strings in the parameter object (`extra` or `res_data`)
2. Type casting will happen automatically according to the type defined in the type object (`extra_type` or `res_data_type`)
3. Object values must be properly stringified with escaped quotes:
   - Correct: `"object_param": "{\"key\":\"value\"}"`
   - Incorrect: `"object_param": {"key":"value"}`
4. Dynamic objects follow the same rules as static objects:
   - Correct: `"dynamic_object": "{\"id\":{{user_id}},\"name\":\"{{user_name}}\"}"` 
   - Incorrect: `"dynamic_object": "{{JSON.stringify({\"id\": {{user_id}}, \"name\": \"{{user_name}}\"})}}"`
5. The corresponding type in the type object should be set to "object" for objects and "array" for arrays
6. All keys in the parameter object must have matching keys in the type object with the correct data type

These rules apply to:
- Set Parameters nodes (`extra`/`extra_type`)
- Reply to Process nodes (`res_data`/`res_data_type`)
- Call Process nodes (`extra`/`extra_type`)
- API Call nodes (`data`/`data_type`)
- And any other nodes that use these parameter structures

## Other Validation Requirements

In addition to UUID and object type validation, process JSON files must also meet these
requirements:

1. All required fields must be present (obj_type, obj_id, parent_id, title, status, etc.)
2. Node IDs must be unique within the process
3. Node references (to_node_id, err_node_id, etc.) must point to existing nodes
4. All JSON must be properly formatted and valid
5. All nodes must use the correct type names as defined in the schema
6. All required parameters for each node type must be present

Below is a complete list of validation rules for Corezoid process JSON files.

## Process Schema Structure

The Corezoid process JSON schema defines that a process must always be an object with the following structure:

```json
{
  "obj_type": 1,
  "obj_id": "123456",
  "parent_id": "0",
  "title": "Process Title",
  "status": "active",
  "params": [
    {
      "name": "param1",
      "type": "string",
      "flags": ["input"]
    }
  ],
  "scheme": {
    "nodes": [
      {
        "id": "67f4c3f482ba966c7fc7e5d6",
        "obj_type": 1,
        "condition": {
          "logics": [],
          "semaphors": []
        },
        "title": "Start",
        "x": 100,
        "y": 100
      }
    ]
  }
}
```

The schema enforces the following requirements:
- Process must be an object (not an array)
- Required fields: obj_type, obj_id, parent_id, title, status, params, scheme
- The scheme must contain at least one node
- Each node must have id, obj_type, condition, title, x, y properties
- Node IDs must be 24-character hexadecimal strings
- Node IDs must be unique within the process
- UUIDs must follow the UUID-v4 format
- Node obj_type values must be within the allowed list [0,1,2,3,4]
- Active Call Process Stub Mode uses node `obj_type: 4`. It is valid JSON, but
  it bypasses the real called process and returns temporary mock replies.
  `push-process` shows it as a warning on a resolved mutable non-production-like
  stage, and blocks immutable, production-like, or unknown stages unless
  `allow_active_stub_mode=true` is passed.
- Parent_id should be set to 0 for root level processes/folders unless specified otherwise
- Use single backslash for escaping quotes in JSON strings (\" instead of \\")



### Code Execution Node (logic_api_code)

When creating Code Execution nodes (nodes that run JavaScript or Erlang code), you must use the following structure:

```json
{
  "type": "api_code",
  "lang": "js",
  "src": "function myFunction(data) {\n  // Your code here\n  return data;\n}",
  "err_node_id": "error_node_id"
}
```

Common validation errors for Code Execution nodes:
- Missing the required `err_node_id` parameter
- Using an unsupported language (only "js" and "erl" are supported)
- Syntax errors in the source code

#### Error Handling in Code Execution Nodes

When a code execution throws an exception, the task will be routed to the error node specified in the `err_node_id` parameter. The error handling node should:

1. Check for expected error conditions using `go_if_const` logic
2. Route expected errors to appropriate handling nodes
3. Handle unexpected errors separately

### Copy Task Node (logic_api_copy)

When creating Copy Task nodes (nodes that duplicate tasks), you must use the following structure:

```json
{
  "type": "api_copy",
  "data": {
    "param1": "value1",
    "param2": 2
  },
  "data_type": {
    "param1": "string",
    "param2": "number"
  },
  "conv_id": "target_process_id",
  "err_node_id": "error_node_id"
}
```

Common validation errors for Copy Task nodes:
- Missing the required `data` and `data_type` parameters
- Type mismatch between values in `data` and types in `data_type`
- Missing the required `err_node_id` parameter

### RPC Call Node (logic_api_rpc)

When creating RPC Call nodes (Call Process nodes), you must use the following structure:

```json
{
  "type": "api_rpc",
  "conv_id": "process_id_to_call",
  "extra": {
    "param1": "value1",
    "param2": 2
  },
  "extra_type": {
    "param1": "string",
    "param2": "number"
  },
  "err_node_id": "error_node_id"
}
```

Common validation errors for RPC Call nodes:
- Missing the required `extra` and `extra_type` parameters
- Type mismatch between the called process's input parameter types and the types specified in `extra_type`
- Missing the required `err_node_id` parameter
- Missing or invalid `conv_id` parameter

### RPC Reply Node (logic_api_rpc_reply)

When creating RPC Reply nodes (Reply to Process nodes), you must use the following structure:

```json
{
  "type": "api_rpc_reply",
  "res_data": {
    "param1": "value1",
    "param2": 2
  },
  "res_data_type": {
    "param1": "string",
    "param2": "number"
  },
  "throw_exception": false
}
```

Common validation errors for RPC Reply nodes:
- Missing the required `res_data` and `res_data_type` parameters
- Type mismatch between values in `res_data` and types in `res_data_type`
- No matching output parameter in the process definition for the data being returned

### Get Task Node (logic_api_get_task)

When creating Get Task nodes (nodes that retrieve tasks from queues), you must use the following structure:

```json
{
  "type": "api_get_task",
  "conv_id": "source_process_id",
  "node_id": "source_node_id",
  "extra": {
    "param1": "value1"
  },
  "extra_type": {
    "param1": "string"
  },
  "err_node_id": "error_node_id"
}
```

Common validation errors for Get Task nodes:
- Missing the required `extra` and `extra_type` parameters
- Missing the required `err_node_id` parameter
- Missing or invalid `conv_id` or `node_id` parameters

### Database Call Node (logic_db_call)

When creating Database Call nodes (nodes that execute SQL queries), you must use the following structure:

```json
{
  "type": "db_call",
  "instance_id": "database_instance_id",
  "query": "SELECT * FROM table WHERE condition = {{parameter}}",
  "extra": {
    "param1": "value1"
  },
  "extra_type": {
    "param1": "string"
  },
  "err_node_id": "error_node_id"
}
```

Common validation errors for Database Call nodes:
- Missing the required `extra` and `extra_type` parameters
- Missing the required `err_node_id` parameter
- Missing or invalid `instance_id` parameter
- SQL syntax errors in the `query` parameter

### Queue Node (logic_api_queue)

When creating Queue nodes (nodes that add tasks to queues), you must use the following structure:

```json
{
  "type": "api_queue",
  "extra": {
    "param1": "value1"
  },
  "extra_type": {
    "param1": "string"
  }
}
```

Common validation errors for Queue nodes:
- Missing the required `extra` and `extra_type` parameters
- Type mismatch between values in `extra` and types in `extra_type`

### Set Parameters Node (logic_set_param)

When creating Set Parameters nodes (nodes that set task parameters), you must use the following structure:

```json
{
  "type": "set_param",
  "extra": {
    "param1": "value1",
    "param2": "2",
    "object_param": "{\"key1\":\"value1\",\"key2\":\"value2\"}"
  },
  "extra_type": {
    "param1": "string",
    "param2": "string",
    "object_param": "string"
  },
  "err_node_id": "error_node_id"
}
```

Common validation errors for Set Parameters nodes:
- Missing the required `extra` and `extra_type` parameters
- Type mismatch between values in `extra` and types in `extra_type`
- Missing the required `err_node_id` parameter
- Not stringifying object values in `extra`

### Sum Node (logic_api_sum)

When creating Sum nodes (nodes that perform numeric operations), you must use the following structure:

```json
{
  "type": "api_sum",
  "extra": {
    "result_param": "total",
    "operand1": "{{value1}}",
    "operand2": "{{value2}}",
    "operation": "+"
  },
  "err_node_id": "error_node_id"
}
```

Common validation errors for Sum nodes:
- Missing the required `err_node_id` parameter
- Invalid operation in the `operation` parameter (only +, -, *, / are supported)
- Non-numeric values in the operands

### Callback Node (logic_api_callback)

When creating Callback nodes (nodes that handle external callbacks), you must use the following structure:

```json
{
  "type": "api_callback",
  "is_sync": true,
  "obj_id_path": "callback_id"
}
```

Common validation errors for Callback nodes:
- Missing the required `is_sync` parameter
- Missing or invalid `obj_id_path` parameter

### Git Call Node (logic_git_call)

When creating Git Call nodes (nodes that execute code from Git repositories), you must use the following structure:

```json
{
  "type": "git_call",
  "repo": "https://github.com/example/repo.git",
  "path": "scripts/example.js",
  "commit": "main",
  "lang": "js",
  "err_node_id": "error_node_id"
}
```

Common validation errors for Git Call nodes:
- Missing the required `err_node_id` parameter
- Missing or invalid `repo` parameter
- Missing or invalid `path` parameter
- Using an unsupported language in the `lang` parameter

### Reply Node (logic_api_reply)

When creating Reply nodes (nodes that send responses to external systems), you must use the following structure:

```json
{
  "type": "api_reply",
  "extra": {
    "param1": "value1",
    "param2": 2
  },
  "extra_type": {
    "param1": "string",
    "param2": "number"
  },
  "err_node_id": "error_node_id"
}
```

Common validation errors for Reply nodes:
- Missing the required `extra` and `extra_type` parameters
- Type mismatch between values in `extra` and types in `extra_type`
- Missing the required `err_node_id` parameter

### Form Node (logic_api_form) - DEPRECATED

When creating Form nodes (deprecated nodes for form submission), you must use the following structure:

```json
{
  "type": "api_form",
  "extra": {
    "param1": "value1"
  },
  "extra_type": {
    "param1": "string"
  },
  "err_node_id": "error_node_id"
}
```

Note: This node type is deprecated and should not be used in new processes.

### Sender Node (logic_api_sender) - DEPRECATED

When creating Sender nodes (deprecated nodes for sending messages), you must use the following structure:

```json
{
  "type": "api_sender",
  "extra": {
    "param1": "value1"
  },
  "extra_type": {
    "param1": "string"
  },
  "err_node_id": "error_node_id"
}
```

Note: This node type is deprecated and should not be used in new processes.

## Task Size Validation

When creating tasks in Corezoid processes, be aware of size limitations enforced by the system:

1. **Maximum Task Size**: By default, tasks are limited to 128KB (128000 bytes)
2. **Size Calculation**: The system calculates the total size of all parameters in the task
3. **Error Handling**: If a task exceeds the size limit, it will be rejected with a
   `task_size_limit` error
4. **Truncation**: When errors occur, task data may be truncated to `TASK_OVERFLOW_DATAPART_SIZE`
   (128 bytes) for logging

This validation prevents performance issues and ensures efficient task processing.

### Task Size Validation Errors

If you encounter the following error:

```
{task_size_limit, DataSize, MAX_TASK_SIZE, TrimmedData}
```

This indicates your task exceeds the maximum allowed size. Optimize your task data by:

1. Removing unnecessary parameters
2. Minimizing data structure nesting
3. Using more compact data representations
4. Splitting large tasks into smaller ones when possible

## Related Documentation

- [Process README](README.md) - Overview of Corezoid processes
- [Node Positioning and Optimization](node-positioning-best-practices.md) - Optimization
  techniques
- [Execution Algorithm](execution-algorithm.md) - How processes are executed
- [Task Examples](../tasks/README.md) - Guidelines for creating tasks
