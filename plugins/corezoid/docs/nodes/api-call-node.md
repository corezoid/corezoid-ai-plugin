# API Call Node

## Purpose

- Sends HTTP requests to external endpoints.
- Integrates with third-party services and systems.
- Retrieves or submits data to external APIs.

## Parameters

### Required

1. **URL** (String)
   - The endpoint to call.
   - Must be a valid URL with protocol (http:// or https://).
   - Validation: URL format is checked during process validation.
2. **Method** (String)
   - HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD, etc.).
   - Validation: Must be one of the standard HTTP methods.
3. **Format** (String)
   - Request format type (default, corezoid, raw).
   - Default: Standard HTTP request format (represented by an empty string `""` or `"default"`).
   - Corezoid: Specific format for Corezoid-to-Corezoid communication.
   - Raw: Allows sending raw request body without parameter mapping.
4. **extra** and **extra_type** (Objects)
   - Must include both parameters in the node configuration, even if empty.
   - Used for additional request parameters.
   - The extra_type parameter is directly linked to the content of the extra parameter.
   - Each key in extra should have a corresponding type definition in extra_type.
   - Validation: Both parameters must be present, even if empty objects.
5. **Error Node ID** (String)
   - Specifies which node to route to if the API call fails.
   - This parameter is required for all API Call nodes to ensure proper error handling.
   - Validation: Must reference a valid node ID in the process.

### Optional

1. **Headers** (Object)
   - HTTP headers to include in the request.
   - Example: `{"Content-type": "application/json; charset=utf-8"}`
   - Common Content-Type options:
     - `application/json`: For JSON payloads
     - `application/x-www-form-urlencoded`: For form data
     - `application/xml`: For XML payloads
     - `text/xml`: For text-based XML
     - `application/soap+xml`: For SOAP requests
2. **Body** (Object/String)
   - Request payload for POST, PUT, etc.
   - For JSON payloads, use the appropriate content-type header.
3. **Max Threads** (Number)
   - Limits concurrent API calls.
   - Default: 5 (configured in environment settings)
   - Validation: Must be a positive integer.
4. **Response Mapping** (Object)
   - Maps API response fields to task parameters.
   - Example: `{"header": "{{header}}", "body": "{{body}}"}`
   - Customize response format by selecting the appropriate content type.
5. **Add system parameters to the request** (Boolean)
   - When enabled, adds Corezoid system parameters to the request.
   - Default: true (configured in environment settings)
   - Useful for tracking and debugging purposes.
6. **Sign the request with the secret key** (String)
   - Adds a signature to the request for authentication.
   - Requires enabling "Add system parameters to the request" option.
   - Can generate a random key or enter a custom key.
7. **RFC standard response** (Boolean)
   - When enabled, formats the response according to RFC standards.
8. **Include debug info** (Boolean)
   - When enabled, adds debugging information to the response.
   - Useful for troubleshooting API calls.
9. **Sign the request by certificate** (String)

   - Allows signing the request with a PEM certificate.
   - Enter the certificate content in the provided text area.

10. **Customize Response** (Boolean)
    - Default: `false`.
    - When `false`, the parsed JSON response body is **spread into the task data root** — each
      top-level field of the body becomes a top-level task parameter (response headers are not
      captured, and the `response`/`response_type` mapping is ignored). When `true`, only the
      fields you declare in `response`/`response_type` are written, nested under the keys you
      choose. See [Reading the response body](#reading-the-response-body-customize_response-false-vs-true).
11. **Response Type** (Object)
    - Used only when `customize_response` is `true`.
    - Defines the expected data types for the fields mapped in the `response` parameter.
    - Example: `{"status_code": "number", "user_data": "object"}`
    - Validation: Ensures the received data types match the expected types during mapping.
12. **Content Type** (String)
    - Specifies the expected `Content-Type` of the _response_ body for parsing purposes, especially
      when the server might not return a correct `Content-Type` header or when you need to force a
      specific parsing method (e.g., `application/json`, `text/plain`).
    - This complements the `Content-Type` set in `extra_headers` which applies to the _request_.
13. **is_migrate** (Boolean)
    - Internal flag, likely related to system migrations or versioning. Its specific function is not
      typically relevant for user configuration. (Note: Further clarification might be needed if
      specific use cases arise).

- Corezoid distinguishes between two types of errors:
  - **Hardware errors**: Infrastructure or network failures (retried automatically)
  - **Software errors**: API response errors, bad requests, etc.
- Error routing is configured through a Condition node that checks:
  - `__conveyor_api_return_type_error__`: Type of error (hardware/software)
  - `__conveyor_api_return_type_tag__`: Specific error tag (e.g., api_bad_answer)
  - `__conveyor_api_return_description__`: Detailed error description

### Common Error Scenarios

1. **Connection Errors** (Hardware)

   - Network connectivity issues
   - DNS resolution failures
   - Timeout errors
   - Error tag: `api_connection_error`
   - Recommended handling: Implement retry with exponential backoff

2. **HTTP Status Errors** (Software)

   - 4xx errors (client errors): Bad request, unauthorized, forbidden, not found
   - 5xx errors (server errors): Internal server error, service unavailable
   - Error tag: `api_bad_answer`
   - Response code available in: `__conveyor_api_return_code__`
   - Recommended handling: Route to error handling based on status code

3. **Malformed Response** (Software)

   - Invalid JSON in response
   - Missing expected fields
   - Error tag: `api_bad_answer_format`
   - Recommended handling: Log error details and notify administrators

4. **Timeout Errors** (Hardware)

   - Request exceeded configured timeout
   - Error tag: `api_timeout`
   - Recommended handling: Increase timeout or implement retry mechanism

5. **Validation Errors** (Software)
   - Invalid URL format
   - Missing required parameters
   - Error tag: `api_validation_error`
   - Recommended handling: Fix configuration issues in process design

## Response Customization

The API Call node allows customization of how response data is processed:

1. **Response Format**

   - Default: Uses the Content-Type header from the response
   - Specific formats: application/json, application/x-www-form-urlencoded, application/xml,
     text/xml, application/soap+xml
   - Format selection affects how the response is parsed before mapping

2. **Response Mapping**
   - Maps response data to task parameters
   - With `customize_response: false` (default), no mapping is applied — the response body is
     spread into the task data root (see below); headers are discarded
   - With `customize_response: true`, only the fields declared in `response`/`response_type` are
     written, under the keys you name (`{{body}}` = full body, `{{header}}` = full headers)

## Reading the response body: `customize_response` false vs true

How the response body reaches the task depends entirely on `customize_response`. The two modes
behave very differently (verified against a live endpoint returning
`{"data":{...},"json":{...},"url":"..."}`):

| `customize_response` | `response`/`response_type` | Where the body lands |
| --- | --- | --- |
| `false` (default) | **ignored** | Body is **spread into the task root**: each top-level field becomes a task parameter. A body `{"data":{"id":42}}` is read as `{{data.id}}` (task path `data.data.id`). Response **headers are not captured**. |
| `true` | applied | Only the declared fields are written, **nested under your keys**. `response: {"body":"{{body}}"}` puts the whole body under `{{body}}`, so the same id is `{{body.data.id}}` (task path `data.body.data.id`). |

`rfc_format` does not change this placement.

### Prefer `customize_response: false` to capture a body

For most cases — especially reading an ID or object the API just created — `customize_response: false`
is the robust choice: the whole body is available at the task root with no mapping to get wrong.

### ⚠️ Silent failure with `customize_response: true`

Custom mapping is **sensitive to the real response shape**, and a mismatch fails quietly:

- **Mapped path absent.** `response: {"new_id":"{{body.data.id}}"}` against a body that has no
  `data.id` sets `new_id` to an **empty string** and routes the task to the **success** path — no
  error, no `err_node`. Downstream nodes then run with an empty value.
- **Type mismatch is coerced, not rejected.** `response_type` accepts only `string`, `object`, or
  `array`; declaring `array` for an object body wraps it (`[ {...} ]`) rather than erroring.
- A genuine `err_node` exit happens only when the response is not parseable JSON at all (e.g. an
  HTML 5xx page → tag `api_no_valid_json`, with `__conveyor_api_return_*` fields populated).

Therefore: use `customize_response: true` only after confirming the **actual** response shape, and
validate the mapped value downstream (e.g. a Condition node checking it is non-empty) instead of
assuming the mapping succeeded.

## Validation Rules

The API Call node undergoes validation during process commit with these checks:

1. URL must be a valid format with protocol
2. Method must be a standard HTTP method
3. Both `extra` and `extra_type` parameters must be present
4. If `err_node_id` is specified, it must reference a valid node
5. `max_threads` must be a positive integer if specified

## Best Practices

- Include proper error handling for all API calls
- After an API Call node, add a "Reply to process" node to return the data
- Set reasonable timeouts based on the expected response time
- Use parameter mapping to transform data before sending
- Validate required parameters before making the API call
- Include both "extra": {} and "extra_type": {} parameters in the configuration
- Position error handling nodes to the right of the API Call node
- Use descriptive node titles that indicate the API being called
- Implement retry logic for hardware errors using Delay nodes
- Use Condition nodes to route different error types appropriately

## Environment Settings

The API Call node has the following default environment settings:

```json
"api_call": {
    "send_sys": true,
    "default_max_thread": 5
}
```

These settings affect:

- `send_sys`: Default value for "Add system parameters to the request" option (true)
- `default_max_thread`: Default maximum number of concurrent API calls (5)

## Using Semaphores in API Call Nodes

API Call nodes support both time and count semaphores to implement timeouts and rate limiting:

### Time Semaphores

Time semaphores can be used to implement timeouts for API calls. If the API doesn't respond within
the specified time, the task is routed to a timeout node:

```json
"semaphors": [
  {
    "type": "time",
    "value": 30,
    "dimension": "sec",
    "to_node_id": "timeout_node_id"
  }
]
```

This provides an alternative to the built-in timeout mechanism and allows for more flexible timeout
handling.

### Count Semaphores

Count semaphores can be used to implement rate limiting for API calls. If the number of concurrent
calls reaches the threshold, new tasks are routed to an escalation node:

```json
"semaphors": [
  {
    "type": "count",
    "value": 100,
    "esc_node_id": "rate_limit_node_id"
  }
]
```

This can be used in addition to the `max_threads` parameter to implement more sophisticated rate
limiting strategies.

## Related Documentation

- [Best Practices for Building Fast and Effective Processes](../process/process-development-guide.md) -
  Optimization techniques for processes

## Implementation Example

### Configuration Example (GET Request)

This example demonstrates a simple GET request configuration based on a real process, including the
default error escalation pattern connection.

```json
{
  "id": "api_call_get_node", // Unique node ID
  "obj_type": 0, // Object type for Logic node
  "condition": {
    "logics": [
      {
        "type": "api", // Specifies this is an API Call logic block
        "err_node_id": "error_condition_node", // ID of the node to go to on error
        "format": "", // Request format type (empty string means "default")
        "method": "GET", // HTTP method
        "url": "https://blockchain.info/ticker", // Target API endpoint
        "extra": {}, // No extra query parameters for this GET request
        "extra_type": {}, // Corresponding types for extra parameters (empty)
        "max_threads": 5, // Maximum concurrent requests allowed for this node
        "debug_info": false, // Do not include extra debug info in the task
        "customize_response": false, // false → response body is spread into the task root; response/response_type below are IGNORED
        "response": {
          // Ignored while customize_response is false (only used when it is true)
          "header": "{{header}}",
          "body": "{{body}}"
        },
        "response_type": {
          // Ignored while customize_response is false (only used when it is true)
          "header": "object",
          "body": "object"
        },
        "extra_headers": {
          // Custom request headers
          "Content-type": "test" // Example header (Note: Content-Type for GET is unusual but possible)
        },
        "send_sys": true, // Include Corezoid system parameters in the request
        "cert_pem": "", // No client certificate used for signing
        "content_type": "application/json", // Expected response content type for parsing
        "rfc_format": true, // Use RFC standard response format
        "is_migrate": true // Internal flag
      },
      {
        "type": "go", // Logic block for successful execution path
        "to_node_id": "success_reply_node" // ID of the next node on success
      }
    ],
    "semaphors": [] // Optional semaphores for implementing timeouts or rate limiting
  },
  "title": "Get Blockchain Ticker", // Descriptive title
  "description": "Fetch current Bitcoin ticker info", // Optional description
  "x": 664, // X coordinate on canvas
  "y": 200, // Y coordinate on canvas
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}", // UI settings (expanded form, default icon)
  "options": null // No specific options set
}
```

This configuration performs a GET request to the specified URL. If the request fails, it routes the
task to `error_condition_node` (which would typically start the error escalation pattern). On
success, it proceeds to `success_reply_node`.

### API Call with Reply Pattern

After an API Call node, always add a "Reply to Process" node to return the data:

```
Start Node → API Call Node → Reply to Process Node → End Node
```

This ensures proper data flow and response handling.

### Default Escalation Pattern

The Corezoid system can automatically generate an escalation pattern for API Call nodes to handle
errors. This pattern consists of:

1. **Condition Node** - Evaluates the type of error:

   - Checks `__conveyor_api_return_type_error__` for "hardware" or "software" errors
   - Checks `__conveyor_api_return_type_tag__` for specific error tags (e.g., "api_bad_answer")
   - Routes tasks to appropriate handling paths

2. **Delay Node** - For hardware errors (network issues, timeouts):

   - Implements a retry mechanism with configurable delay (default: 30 seconds)
   - Routes back to the original API Call node after the delay

3. **Error End Node** - For software errors (bad responses, validation errors):
   - Marks the task as failed
   - Provides error details for debugging

The escalation pattern is automatically positioned to the right of the API Call node:

- Condition Node: Positioned diagonally to the right and down from the API Call node
- Delay Node: Positioned directly to the right of the API Call node
- Error End Node: Positioned diagonally to the right and down from the Condition Node

```
                           ┌─── [hardware error] ──→ Delay Node ──→ Back to API Call
                           │
API Call Node ──→ Condition Node ─┤
                           │
                           └─── [software error] ──→ Error End Node
```

To create this pattern automatically:

1. Select the API Call node
2. Click on the error message that says "Node must be connected to an error-handling node"
3. Click "Create escalation nodes" button in the node properties panel

## Node Naming Guidelines

When creating API Call nodes in your processes:

1. **Node Titles** should:

   - Clearly indicate the specific API being called (e.g., "Call Payment API" rather than just "API
     Call")
   - Reflect the purpose of the API call in the context of your process
   - Be concise but descriptive enough to understand at a glance

2. **Node Descriptions** should:
   - Explain what data is being sent and received
   - Mention any important parameters or headers
   - Document any specific error handling considerations
   - Include information about retry strategies if applicable

Example of good naming:

- Title: "Get Customer Profile"
- Description: "Retrieves customer data from CRM API using customer_id. Returns profile data
  including contact information and preferences."

Example of poor naming:

- Title: "API"
- Description: "Makes API call"

Meaningful titles and descriptions make processes more maintainable, easier to troubleshoot, and
more accessible to other team members.
