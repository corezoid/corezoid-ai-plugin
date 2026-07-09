# Error Handling Strategies in Corezoid

Effective error handling is crucial for building robust and reliable processes in Corezoid. This
document outlines comprehensive strategies for handling different types of errors across various
node types.

## Error Types in Corezoid

Corezoid distinguishes between two primary types of errors:

### 1. Hardware Errors

Hardware errors are infrastructure or network-related issues that are typically transient and can be
resolved by retrying the operation:

- Network connectivity issues
- DNS resolution failures
- Timeout errors
- Server overload conditions
- Database connection issues

Hardware errors are identified by the system parameter `__conveyor_*_return_type_error__` with a
value of `"hardware"`.

### 2. Software Errors

Software errors are logical or configuration issues that typically require intervention to resolve:

- Invalid input parameters
- Authentication failures
- Authorization issues
- Business logic errors
- Data validation failures
- Malformed responses

Software errors are identified by the system parameter `__conveyor_*_return_type_error__` with a
value of `"software"`.

## Error Parameters

When an error occurs, Corezoid generates specific system parameters that can be evaluated by
Condition nodes:

### Common Error Parameters

| Parameter                           | Description                       | Example Values                                    |
| ----------------------------------- | --------------------------------- | ------------------------------------------------- |
| `__conveyor_*_return_type_error__`  | Type of error (hardware/software) | `"hardware"`, `"software"`                        |
| `__conveyor_*_return_type_tag__`    | Specific error tag                | `"api_connection_error"`, `"api_bad_answer"`      |
| `__conveyor_*_return_description__` | Detailed error description        | `"Connection refused"`, `"Invalid JSON response"` |
| `__conveyor_*_return_code__`        | Error code (for API calls)        | `404`, `500`                                      |

> Note: The `*` in parameter names is replaced with the specific node type, such as `api`, `code`,
> `db`, etc.

## Error Handling Patterns

### 1. Basic Error Routing

The simplest pattern routes `err_node_id` directly to a Final Error node when no action
is needed on the error path:

```
Operation Node ──→ Success Path
      │
      └─── [err_node_id] ──→ Final Error Node (obj_type:2)
```

Implementation:

```json
{
  "type": "api_rpc",
  "conv_id": 12345,
  "err_node_id": "<final_error_node_id>"
}
```

> **Rule:** Wire `err_node_id` directly to a Final Error node (`obj_type: 2`) when the
> error path needs no logic. Only interpose an Escalation node (`obj_type: 3`) when the
> error path requires an action such as replying to the caller or conditional retry routing.
> An escalation node that only contains a bare `go` with no action logic is an anti-pattern
> and is flagged by `lint-process` as a **passthrough escalation**.

### 2. Error Type Differentiation

A more sophisticated approach distinguishes between hardware and software errors:

```
                                 ┌─── [hardware error] ──→ Retry Logic
                                 │
Operation Node → Condition Node ─┤
                                 │
                                 └─── [software error] ──→ Error Handling
```

Implementation:

```json
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
  "to_node_id": "retry_node_id"
}
```

### 3. Retry Pattern for Transient Errors

For hardware errors, implementing a retry mechanism with exponential backoff is recommended:

```
Operation Node ──→ Success Path
      │
      └─── [error] ──→ Condition Node ─┬─── [retry count < max] ──→ Delay Node ──┐
                                      │                           │
                                      │                           ↓
                                      │                      Increment Retry Count
                                      │                           │
                                      │                           └───────────────┘
                                      │
                                      └─── [retry count >= max] ──→ Failure Node
```

Implementation:

```json
{
  "type": "set_param",
  "extra": {
    "retry_count": "{{$.math(retry_count+1)}}"
  },
  "extra_type": {
    "retry_count": "number"
  }
}
```

### 4. Specific Error Code Handling

For API calls, handling specific HTTP status codes:

```
API Call Node → Condition Node ─┬─── [status 401/403] ──→ Authentication Error
                               │
                               ├─── [status 404] ──→ Not Found Error
                               │
                               ├─── [status 5xx] ──→ Server Error
                               │
                               └─── [success] ──→ Continue Process Flow
```

Implementation:

```json
{
  "type": "go_if_const",
  "conditions": [
    {
      "param": "__conveyor_api_return_code__",
      "const": "404",
      "fun": "eq",
      "cast": "string"
    }
  ],
  "to_node_id": "not_found_error_node"
}
```

### 5. Validation Error Handling

For input validation errors, providing specific error messages:

```
                    ┌─── [missing required field] ──→ Missing Field Error
                    │
Validation Node ────┼─── [invalid format] ──→ Format Error
                    │
                    └─── [valid input] ──→ Continue Process Flow
```

Implementation:

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": {
    "result": "error",
    "error_code": "MISSING_FIELD",
    "error_message": "Required field 'customer_id' is missing"
  },
  "res_data_type": {
    "result": "string",
    "error_code": "string",
    "error_message": "string"
  },
  "throw_exception": true
}
```

## Node-Specific Error Handling

### API Call Node

- **Connection Errors**: Implement retry with exponential backoff
- **HTTP Status Errors**: Route based on status code
- **Timeout Errors**: Consider increasing timeout or implementing retry
- **Authentication Errors**: Handle 401/403 status codes appropriately

Example:

```json
{
  "type": "api",
  "method": "GET",
  "url": "https://api.example.com",
  "extra": {},
  "extra_type": {},
  "err_node_id": "api_error_node"
}
```

### Code Node

- **Syntax Errors**: Validate code before deployment
- **Runtime Exceptions**: Implement try/catch blocks
- **Memory Errors**: Optimize code for memory usage
- **Timeout Errors**: Break complex operations into smaller steps

Example:

```javascript
try {
  // Code logic here
} catch (e) {
  data.error = e.message;
  return data;
}
```

### Database Call Node

- **Connection Errors**: Implement retry mechanism
- **Query Errors**: Validate SQL queries before execution
- **Timeout Errors**: Optimize queries for performance
- **Data Integrity Errors**: Handle constraint violations appropriately

Example:

```json
{
  "type": "db",
  "db_url": "postgres://user:password@host:port/database",
  "sql": "SELECT * FROM users WHERE id = {{user_id}}",
  "err_node_id": "db_error_node"
}
```

### Call a Process Node

- **Process Not Found**: Validate process ID before calling
- **Access Denied**: Ensure proper permissions
- **Timeout Errors**: Set appropriate timeout values
- **Process Errors**: Handle errors returned by the called process

Example:

```json
{
  "type": "api_rpc",
  "conv_id": "{{process_id}}",
  "extra": {
    "param1": "value1"
  },
  "extra_type": {
    "param1": "string"
  },
  "err_node_id": "process_call_error_node"
}
```

## Best Practices for Error Handling

1. **Early Validation**: Validate inputs at the beginning of the process
2. **Dedicated Error Nodes**: Create specific error nodes for different error types
3. **Descriptive Error Messages**: Provide clear error messages that help diagnose issues
4. **Retry Mechanisms**: Implement retry logic for transient errors
5. **Logging**: Log error details for troubleshooting
6. **Graceful Degradation**: Design processes to continue with limited functionality when possible
7. **Consistent Error Response Format**: Standardize error response format across processes
8. **Error Monitoring**: Set up monitoring for error rates and patterns
9. **Documentation**: Document error handling strategy for each process
10. **Testing**: Test error paths as thoroughly as success paths

## Error Response Format

Standardize error responses using this format:

```json
{
  "result": "error",
  "error_code": "ERROR_CODE",
  "error_message": "Human-readable error message",
  "error_details": {
    "field": "field_with_error",
    "value": "invalid_value",
    "expected": "expected_format"
  }
}
```

## Troubleshooting Common Errors

| Error                     | Possible Causes                     | Solutions                                                |
| ------------------------- | ----------------------------------- | -------------------------------------------------------- |
| API connection error      | Network issues, invalid endpoint    | Check network connectivity, verify endpoint URL          |
| API timeout               | Slow response from external service | Increase timeout settings, implement retry logic         |
| Code execution error      | Syntax errors, runtime exceptions   | Debug code in Code node, check for proper error handling |
| Database connection error | Network issues, invalid credentials | Check network connectivity, verify credentials           |
| Process call error        | Invalid process ID, access denied   | Verify process ID, check permissions                     |
| Validation error          | Invalid input data                  | Improve input validation, provide clear error messages   |

## Dedicated Error Cluster Pattern (standard)

Every node that can return an error must have its **own** error cluster — never funnel several
failing nodes into a single shared Reply/Error node. Each failure point gets a distinct, descriptively
named Error node so the diagram reads clearly.

A cluster is two nodes pinned tight to the node they protect:

1. **Reply to Process** node (`api_rpc_reply`) that returns the error to the caller, kept
   **collapsed** (`"extra": "{\"modeForm\":\"collapse\",\"icon\":\"\"}"`). Placed at the **same `y`**
   as the failing node, just to its right (`x + ~250`) so the small collapsed node sits right next to it.
2. **Error** END node (`obj_type: 2`, **expanded** so its name stays visible:
   `"extra": "{\"modeForm\":\"expand\",\"icon\":\"error\"}"`) **named after the specific failure**
   (e.g. `Charge Payment Error`, not a generic `Error`/`Final`). Placed immediately to the right of
   the Reply node (`x + ~500`), same `y`.

Wiring: failing node `err_node_id` → its Reply node; the Reply node's `go` → its Error node.

For fire-and-forget processes that do not reply to a caller, omit the Reply node and wire
`err_node_id` directly to the dedicated named Error node.

This one-to-one mapping between each error-prone node and its named Error node — instead of one shared
terminal — improves error isolation, troubleshooting, and readability.

Example Code Node with its dedicated Reply + Error cluster (Reply collapsed and pinned to the node):

```json
{
  "id": "code_node_id",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_code",
        "code": "data.result = data.a + data.b;",
        "err_node_id": "code_reply_error_node_id",
        "extra": {},
        "extra_type": {}
      },
      {
        "type": "go",
        "to_node_id": "next_node_id"
      }
    ],
    "semaphors": []
  },
  "title": "Calculate Sum",
  "x": 200,
  "y": 300
}
```

Its dedicated **Reply** node (collapsed, pinned to the right of the failing node, same `y`):

```json
{
  "id": "code_reply_error_node_id",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_rpc_reply",
        "mode": "key_value",
        "res_data": {
          "status": "error",
          "description": "{{__conveyor_code_return_description__}}"
        },
        "res_data_type": {
          "status": "string",
          "description": "string"
        },
        "throw_exception": false
      },
      {
        "type": "go",
        "to_node_id": "code_error_node_id"
      }
    ],
    "semaphors": []
  },
  "title": "Reply: Calculate Sum Error",
  "x": 450,
  "y": 300,
  "extra": "{\"modeForm\":\"collapse\",\"icon\":\"\"}"
}
```

Its dedicated, descriptively-named **Error** node (expanded so the name is visible, immediately to the right, same `y`):

```json
{
  "id": "code_error_node_id",
  "obj_type": 2,
  "condition": {
    "logics": [],
    "semaphors": []
  },
  "title": "Calculate Sum Error",
  "x": 700,
  "y": 300,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"error\"}"
}
```

This dedicated error cluster pattern — one Reply + one named Error node per error-prone node — is the
standard practice for error handling in Corezoid processes.

## Related Documentation

- [Process Best Practices](node-positioning-best-practices.md) - Optimization techniques for processes
- [API Call Node](../nodes/api-call-node.md) - Documentation for API Call nodes
- [Code Node](../nodes/code-node.md) - Documentation for Code nodes
- [Database Call Node](../nodes/database-call-node.md) - Documentation for Database Call nodes
- [Call a Process Node](../nodes/call-process-node.md) - Documentation for Call a Process nodes
- [Execution Algorithm](execution-algorithm.md) - How processes are executed
