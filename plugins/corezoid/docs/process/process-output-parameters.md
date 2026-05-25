# Process Output Parameters

This document describes how to define and use output parameters in Corezoid processes, particularly
in conjunction with Reply to Process nodes.

## Overview

Output parameters define the interface of data returned from a process when it's called by another
process or through an API. They provide a clear contract for what data consumers can expect to
receive from the process.

## Defining Output Parameters

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
  },
  {
    "name": "data",
    "type": "object",
    "descr": "Response payload",
    "flags": ["output"],
    "regex": "",
    "regex_error_text": ""
  }
]
```

### Parameter Properties

Each output parameter has the following properties:

1. **name** (String, required)

   - Unique identifier for the parameter
   - Used to reference the parameter in Reply to Process nodes

2. **type** (String, required)

   - Data type of the parameter
   - Supported types: `string`, `number`, `boolean`, `object`, `array`

3. **descr** (String, optional)

   - Human-readable description of the parameter
   - Used for documentation purposes

4. **flags** (Array, required for output parameters)

   - Must include `"output"` to mark as an output parameter
   - Can include other flags like `"required"` if needed

5. **regex** (String, optional)

   - Regular expression pattern for validation
   - Only applicable for string parameters

6. **regex_error_text** (String, optional)
   - Custom error message for regex validation failures

## Using Output Parameters with Reply to Process Nodes

Output parameters are typically returned through Reply to Process nodes. The Reply to Process node
should include all defined output parameters in its response data:

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": {
    "result": "success",
    "data": {
      "a": "{{a}}",
      "b": {{b}},
      "c": {{c}},
      "d": {{d}},
      "e": {{e}}
    }
  },
  "res_data_type": {
    "result": "string",
    "data": "object"
  }
}
```

### Best Practices

1. **Consistent Naming**

   - Use the same parameter names in the Reply to Process node as defined in the output parameters
   - This ensures proper mapping of returned data

2. **Type Consistency**

   - Ensure the data types in the Reply to Process node match the defined output parameter types
   - For example, if `b` is defined as a number, don't wrap it in quotes in the Reply node

3. **Complete Response**

   - Include all defined output parameters in the Reply to Process node
   - Missing parameters may cause issues for consumers expecting complete data

4. **Structured Response**

   - For complex responses, use a nested structure with a standard format:
     ```json
     {
       "result": "success",
       "data": {
         // All output parameters here
       }
     }
     ```

5. **Documentation**
   - Document the purpose and expected values of each output parameter
   - Include examples of valid response formats

## Example Process with Output Parameters

Here's an example of a process with various output parameter types:

```json
{
  "obj_type": 1,
  "obj_id": 1643034,
  "parent_id": 0,
  "title": "Process with Output Parameters",
  "description": "",
  "status": "active",
  "params": [
    {
      "name": "a",
      "type": "string",
      "descr": "String output parameter",
      "flags": ["output"],
      "regex": "",
      "regex_error_text": ""
    },
    {
      "name": "b",
      "type": "number",
      "descr": "Numeric output parameter",
      "flags": ["output"],
      "regex": "",
      "regex_error_text": ""
    },
    {
      "name": "c",
      "type": "boolean",
      "descr": "Boolean output parameter",
      "flags": ["output"],
      "regex": "",
      "regex_error_text": ""
    },
    {
      "name": "d",
      "type": "array",
      "descr": "Array output parameter",
      "flags": ["output"],
      "regex": "",
      "regex_error_text": ""
    },
    {
      "name": "e",
      "type": "object",
      "descr": "Object output parameter",
      "flags": ["output"],
      "regex": "",
      "regex_error_text": ""
    }
  ],
  "scheme": {
    "nodes": [
      // Process nodes here
    ]
  }
}
```

### Corresponding Reply to Process Node

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": {
    "result": "success",
    "data": {
      "a": "string value",
      "b": 123,
      "c": true,
      "d": [1, 2, 3],
      "e": { "key": "value" }
    }
  },
  "res_data_type": {
    "result": "string",
    "data": "object"
  }
}
```

## Related Documentation

- [Reply to Process Node](../nodes/reply-to-process-node.md) - Documentation for the node that
  returns data from a process
- [Process with Input Parameters](process-with-parameters.md) - Documentation for defining input
  parameters
