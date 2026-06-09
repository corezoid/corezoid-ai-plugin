# Set Parameters Node Dynamic Values

## Overview

This document provides a comprehensive reference for all allowed dynamic values that can be used in
Set Parameters nodes in Corezoid processes. Dynamic values are referenced using the double curly
braces syntax `{{value}}` and allow for flexible, context-aware parameter setting.

Set Parameters nodes can use three types of values:

1. **Constants** - Fixed values that don't change
2. **Dynamic Values** - Values referenced using the `{{value}}` syntax
3. **Function Calls** - Built-in functions like `$.math()`, `$.random()`, etc.

## Basic Syntax

Dynamic values in Set Parameters nodes use the following syntax:

```
{{variable_name}}
```

This syntax allows you to reference:

- Task parameters
- System variables
- Node information
- Process information

## Constants

Set Parameters nodes can use constants (fixed values) for any parameter:

```json
{
  "type": "set_param",
  "extra": {
    "status": "processed",
    "priority": 1,
    "is_active": true,
    "config": { "timeout": 30, "retries": 3 },
    "tags": ["important", "customer"]
  },
  "extra_type": {
    "status": "string",
    "priority": "number",
    "is_active": "boolean",
    "config": "object",
    "tags": "array"
  }
}
```

Constants can be of different types:

- **Strings**: `"processed"`, `"completed"`, etc.
- **Numbers**: `1`, `42.5`, etc.
- **Booleans**: `true`, `false`
- **Objects**: `{"timeout": 30, "retries": 3}`
- **Arrays**: `["important", "customer"]`

## Task Parameters

Any parameter that exists in the current task data can be referenced directly:

```json
{
  "type": "set_param",
  "extra": {
    "new_parameter": "{{existing_parameter}}"
  },
  "extra_type": {
    "new_parameter": "string"
  }
}
```

### Nested Parameters

You can access nested properties using dot notation:

```
{{user.name}}                // Access 'name' property of the 'user' object
{{address.city}}             // Access 'city' property of the 'address' object
{{metadata.created_at}}      // Access 'created_at' property of the 'metadata' object
```

You can access deeply nested properties by chaining the dot notation:

```
{{user.address.street}}      // Access street property from address object inside user object
{{order.items[0].price}}     // Access price of first item in the items array of the order object
{{metadata.tags.primary}}    // Access primary tag from the tags object inside metadata
```

When working with complex objects, you can also use dynamic property names:

```
{{user[{{property_name}}]}}  // Access property of user object where property name is stored in another parameter
```

## Dynamic Keys

Set Parameters nodes also support dynamic keys, allowing you to dynamically determine parameter
names:

```json
{
  "type": "set_param",
  "extra": {
    "{{dynamic_key}}": "value",
    "{{user.preferred_field}}": "important_value"
  },
  "extra_type": {
    "{{dynamic_key}}": "string",
    "{{user.preferred_field}}": "string"
  }
}
```

**Rules for dynamic keys:**

1. For dynamic keys, the entire expression must be wrapped in `{{ }}` to make it dynamic
2. For simple keys, both `"my_special_key": "value"` and `"{{my_special_key}}": "value"` are
   equivalent
3. When using dynamic keys with arrays, the index cannot exceed 100 (e.g., `{{my_array[{{70}}]}}`
   works but `{{my_array[101]}}` doesn't)
4. When using arrays in values, there are no index restrictions
5. Complex expressions like `"key1": "{{my_object.its_key[{{index}}].title}}"` are supported

### Array Elements

Array elements can be accessed using bracket notation:

```
{{items[0]}}                 // Access first element of items array
{{users[2].name}}            // Access name property of the third element in users array
```

You can also use dynamic indices in array access:

```
{{items[{{index}}]}}         // Access element at position specified by the 'index' parameter
{{array[$.math({{i}}+1)]}}   // Access element at position calculated using math function
```

**Note:** There are limitations when trying to dynamically set array indices. For example, using
`"my_array[{{my_array.length}}]"` to append elements may cause errors. Instead, use a Code Node or
the full array manipulation approach:

```json
// To append an element to an array:
"test_results.tests": "{{JSON.stringify(test_results.tests.concat([{\"name\":\"Test\",\"status\":\"passed\"}]))}}"

// To update a specific element in an array:
"my_array": "{{JSON.stringify(my_array.map((item, idx) => idx === {{target_index}} ? {...item, updated: true} : item))}}"
```

## Environment Variables

Environment variables store stage-scoped constants (URLs, tokens, API keys) that must
not be hardcoded. They are resolved at runtime before the node executes.

**Syntax:** `{{env_var[@variable-short-name]}}`

```json
{
  "type": "set_param",
  "extra": {
    "baseUrl": "{{env_var[@payment-api-url]}}",
    "token":   "{{env_var[@payment-api-token]}}"
  },
  "extra_type": {
    "baseUrl": "string",
    "token":   "string"
  },
  "err_node_id": "<error_node_id>"
}
```

Environment variable references can be used anywhere a dynamic value is accepted:
in `extra` values, in condition `arg`/`val` fields, in API Call URLs and headers.

To manage environment variables (create, list, modify, delete), see
`${CLAUDE_PLUGIN_ROOT}/skills/corezoid-variable-manager/SKILL.md`.

## System Variables

The following system variables are available in Set Parameters nodes:

| Variable    | Description       |
| ----------- | ----------------- |
| `{{__now}}` | Current timestamp |

## Node Information

You can access information about nodes in the current process or other processes:

### Node Task Count

Returns the number of tasks currently in a specific node:

```
{{node[NODE_ID].count}}
```

Examples:

```
// Node ID = 561a272782ba961374d44178
{{node[561a272782ba961374d44178].count}}

// Returns amount of tasks in the node specified by the parameter {{node_id}}
{{node[{{node_id}}].count}}

// Returns amount of tasks in the node specified by the parameter {{node_id}} from {{conv_id}} process
{{conv[{{conv_id}}].node[{{node_id}}].count}}
```

### Node Sum Value

Returns the sum value for a specific parameter across all tasks in a node:

```
{{node[NODE_ID].SumID}}
```

Examples:

```
// Node ID = 561a272782ba961374d44178
{{node[561a272782ba961374d44178].SumID}}

// Returns amount by SumID parameter from node {{node_id}}
{{node[{{node_id}}].SumID}}

// Returns amount by SumID parameter from {{node_id}} from {{conv_id}} process
{{conv[{{conv_id}}].node[{{node_id}}].SumID}}
```

## Process Information

You can access information about other processes and their tasks:

### Process Task Reference

Access task data from another process:

```
{{conv[PROCESS_ID].ref[TASK_REF_ID]}}
```

Examples:

```
// Access a specific task in another process
{{conv[1023399].ref[12345]}}

// Access a specific parameter in a task from another process
{{conv[1023399].ref[12345].amount_owed}}

// Using dynamic process and task references
{{conv[{{my_process_id}}].ref[{{my_task_ref}}].amount_owed}}

// Using special process references
{{conv[@user-states].ref[{{my_task_ref}}]}}
```

## Built-in Functions

Set Parameters nodes support several built-in functions that can be combined with dynamic values.
For detailed documentation of all built-in functions, see
[Set Parameters Built-in Functions](set-parameters-built-in-functions.md).

### Mathematical Operations

```
$.math({{value}}*2)               // Multiplies the 'value' parameter by 2
$.math({{price}}*{{quantity}})    // Multiplies 'price' by 'quantity'
$.math({{value}}/100)             // Divides 'value' by 100
```

### Random Number Generation

```
$.random({{min}}, {{max}})       // Uses parameter values for the range
$.random(1, {{max_value}})       // Combines literal and parameter values
```

### Cryptographic Functions

```
$.base64_encode({{username}})    // Encodes the 'username' parameter
$.md5_hex({{password}})          // Hashes the 'password' parameter
```

### Array Functions

```
$.map(fun(x) -> x * 2 end, {{numbers}})            // Multiplies each element by 2
$.filter(fun(x) -> x > 10 end, {{numbers}})        // Returns elements greater than 10
```

## Error Handling

When using dynamic values, be aware of the following potential issues:

1. **Missing Parameters**: If a referenced parameter doesn't exist, it may result in an empty string
   or error
2. **Type Conversion Issues**: Ensure the parameter type in `extra_type` matches the expected type
   after substitution
3. **Invalid Syntax**: Ensure all curly braces are properly closed and the syntax is correct

## Best Practices

1. **Direct Parameter Access**: Use `{{result}}` instead of `{{data.result}}` to access parameters
2. **Avoid Dynamic Array Indices**: Instead of
   `"test_results.tests[{{test_results.tests.length}}]"`, use
   `"test_results.tests": "{{JSON.stringify(test_results.tests.concat([{...}]))}}"`
3. **Type Safety**: Always specify the correct data type in the `extra_type` section
4. **Error Handling**: Always include an `err_node_id` parameter to handle potential failures
5. **Validation**: Consider validating parameters before setting them
6. **Proper Object Stringification**: Always stringify object values with properly escaped quotes
   - For all objects (static and dynamic): `"object_param": "{\"key\":\"value\",\"dynamic_key\":\"{{value}}\"}"` 
   - Dynamic values follow the same stringification rules as static values
7. **Consistent Naming**: Use consistent naming conventions for parameters

## Examples

### Basic Parameter Reference

```json
{
  "type": "set_param",
  "extra": {
    "b": "{{a}}"
  },
  "extra_type": {
    "b": "string"
  },
  "err_node_id": "error_node_id"
}
```

### Using System Variables

```json
{
  "type": "set_param",
  "extra": {
    "timestamp": "{{__now}}",
    "processed_flag": true
  },
  "extra_type": {
    "timestamp": "string",
    "processed_flag": "boolean"
  },
  "err_node_id": "error_node_id"
}
```

### Accessing Node Information

```json
{
  "type": "set_param",
  "extra": {
    "waiting_tasks": "{{node[561a272782ba961374d44178].count}}",
    "total_amount": "{{node[561a272782ba961374d44178].amount}}"
  },
  "extra_type": {
    "waiting_tasks": "number",
    "total_amount": "number"
  },
  "err_node_id": "error_node_id"
}
```

### Using Built-in Functions with Dynamic Values

```json
{
  "type": "set_param",
  "extra": {
    "calculated_value": "$.math({{base_value}}*1.2)",
    "hashed_password": "$.sha256_hex({{password}})",
    "doubled_numbers": "$.map(fun(x) -> x * 2 end, {{numbers}})",
    "filtered_users": "$.filter(fun(user) -> user.age >= 18 end, {{users}})"
  },
  "extra_type": {
    "calculated_value": "number",
    "hashed_password": "string",
    "doubled_numbers": "array",
    "filtered_users": "array"
  },
  "err_node_id": "error_node_id"
}
```

### Object Stringification Examples

When working with object values in Set Parameters nodes, proper stringification is essential:

```json
{
  "type": "set_param",
  "extra": {
    // Static object with escaped quotes
    "config": "{\"timeout\":30,\"retries\":3,\"url\":\"https://api.example.com\"}",
    
    // Dynamic object with escaped quotes (same rules as static objects)
    "user_data": "{\"id\":{{user_id}},\"name\":\"{{user_name}}\",\"active\":true}",
    
    // Combination of static and dynamic values
    "request_body": "{\"request_id\":\"{{request_id}}\",\"payload\":{\"items\":[{{items}}]}}"
  },
  "extra_type": {
    "config": "object",
    "user_data": "object",
    "request_body": "object"
  },
  "err_node_id": "error_node_id"
}
```

Always make sure that:
1. All quotes within string values are escaped with a backslash (`\"`)
2. Nested objects maintain proper JSON structure
3. The corresponding type in `extra_type` is set to "object"
