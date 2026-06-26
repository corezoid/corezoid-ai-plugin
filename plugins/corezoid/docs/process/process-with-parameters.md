# Process with Input Parameters

This document provides an example of a Corezoid process with defined input parameters of various
types, validation rules, and flags.

## Process Overview

The example process demonstrates how to configure input parameters with different data types,
validation rules, and flags. This pattern is useful when you need to:

- Validate incoming data before processing
- Enforce required parameters
- Define expected data types for parameters
- Apply regex validation to string inputs
- Handle complex object parameters

## JSON Structure

```json
{
  "obj_type": 1,
  "obj_id": 1643031,
  "parent_id": 0,
  "title": "New Process",
  "description": "",
  "status": "active",
  "params": [
    {
      "name": "a",
      "type": "string",
      "descr": "aa",
      "flags": ["required", "input"],
      "regex": "",
      "regex_error_text": ""
    },
    {
      "name": "b",
      "type": "number",
      "descr": "bb",
      "flags": ["input"],
      "regex": "",
      "regex_error_text": ""
    },
    {
      "name": "c",
      "type": "boolean",
      "descr": "cc",
      "flags": ["auto-clear", "input"],
      "regex": "",
      "regex_error_text": ""
    },
    {
      "name": "d",
      "type": "string",
      "descr": "dd",
      "flags": ["input"],
      "regex": "/s",
      "regex_error_text": "error"
    },
    {
      "name": "e",
      "type": "object",
      "descr": "ee",
      "flags": ["input"],
      "regex": "",
      "regex_error_text": ""
    }
  ],
  "ref_mask": false,
  "conv_type": "process",
  "scheme": {
    "nodes": [
      {
        "id": "67f40e0682ba966c7fb151b9",
        "obj_type": 2,
        "condition": {
          "logics": [],
          "semaphors": []
        },
        "title": "Final",
        "description": "",
        "x": 748,
        "y": 400,
        "extra": "{\"modeForm\":\"collapse\",\"icon\":\"success\"}",
        "options": "{\"save_task\":true}"
      },
      {
        "id": "67f40e0682ba966c7fb151b7",
        "obj_type": 1,
        "condition": {
          "logics": [
            {
              "type": "go",
              "to_node_id": "67f40e0682ba966c7fb151b9"
            }
          ],
          "semaphors": []
        },
        "title": "Start",
        "description": "",
        "x": 748,
        "y": 100,
        "extra": "{\"modeForm\":\"collapse\",\"icon\":\"\"}",
        "options": null
      }
    ],
    "web_settings": [[], []]
  }
}
```

## Required `params` element shape (deploy validation)

`params` is a required top-level field, but it may be an **empty array** — use `"params": []` when
the process declares no inputs.

When you DO declare a parameter, the server validates each element strictly. **Every element must
contain all six keys** below, or the deploy fails with `Params are not valid`:

| Key | Type | Value |
| --- | --- | --- |
| `name` | string | a real parameter name |
| `type` | string | one of `string`, `number`, `boolean`, `object`, `array` |
| `descr` | string | may be `""` |
| `flags` | array | may be `[]`; otherwise a subset of `required`, `input`, `output`, `auto-clear` |
| `regex` | string | may be `""` |
| `regex_error_text` | string | may be `""` |

The keys must be **present** even when their value is empty — `descr: ""`, `flags: []`,
`regex: ""`, and `regex_error_text: ""` are each accepted; `name` and `type` carry the parameter's
actual definition. The JSON schema is laxer than the server here — it lists only `name`/`type`/`flags`
and marks nothing as required, so an incomplete element passes client-side validation and is
rejected only at deploy.

Minimal valid single-parameter declaration:

```json
"params": [
  { "name": "x", "type": "string", "descr": "", "flags": ["input"], "regex": "", "regex_error_text": "" }
]
```

Verified by deploy: omitting any one of `descr`, `flags`, `regex`, or `regex_error_text` — or
shrinking the element to `{ "name": "x", "type": "string" }` — is rejected with `Params are not
valid`; the full element (and `"params": []`) deploy cleanly.

### `params` is not required to receive data

A process does **not** need declared `params` to accept input. When one process calls another via an
`api_rpc` (Call a Process) or `api_copy` (Copy Task) node, the payload is passed through that node's
`extra`, and the callee reads those keys directly from its task data — no `params` declaration
required. Declare `params` only to enforce types / required-ness or to drive a UI; otherwise keep
`"params": []`.

## Input Parameters

The process defines five input parameters with different types, validation rules, and flags:

| Parameter | Type    | Description | Flags             | Validation                  |
| --------- | ------- | ----------- | ----------------- | --------------------------- |
| `a`       | string  | aa          | required, input   | None                        |
| `b`       | number  | bb          | input             | None                        |
| `c`       | boolean | cc          | auto-clear, input | None                        |
| `d`       | string  | dd          | input             | Regex: `/s`, Error: "error" |
| `e`       | object  | ee          | input             | None                        |

### Parameter Details

#### Parameter `a` (string)

- **Description**: aa
- **Flags**:
  - `required`: This parameter must be provided when calling the process
  - `input`: This parameter is an input parameter
- **Validation**: None

#### Parameter `b` (number)

- **Description**: bb
- **Flags**:
  - `input`: This parameter is an input parameter
- **Validation**: None

#### Parameter `c` (boolean)

- **Description**: cc
- **Flags**:
  - `auto-clear`: This parameter will be automatically cleared after processing
  - `input`: This parameter is an input parameter
- **Validation**: None

#### Parameter `d` (string)

- **Description**: dd
- **Flags**:
  - `input`: This parameter is an input parameter
- **Validation**:
  - Regex pattern: `/s`
  - Error message: "error"

#### Parameter `e` (object)

- **Description**: ee
- **Flags**:
  - `input`: This parameter is an input parameter
- **Validation**: None

## Process Flow

This example process has a simple flow:

1. **Start Node**: Entry point for the process
2. **Final Node**: Terminates the process flow

The process does not contain any processing logic between the Start and Final nodes, as it is
intended to demonstrate parameter configuration rather than process logic.

## Parameter Flags

The example demonstrates several parameter flags:

- **required**: The parameter must be provided when calling the process
- **input**: The parameter is an input parameter
- **auto-clear**: The parameter will be automatically cleared after processing

## Validation Rules

The example demonstrates regex validation for string parameters:

- Parameter `d` has a regex pattern `/s` with an error message "error"
- When the input for parameter `d` does not match the regex pattern, the error message will be
  displayed

## Usage Example

To call this process with valid parameters:

```json
{
  "a": "string value",
  "b": 123,
  "c": true,
  "d": "string with s",
  "e": {
    "property1": "value1",
    "property2": "value2"
  }
}
```

## Best Practices

When defining process parameters:

1. Include all six keys in every `params` element (`name`, `type`, `descr`, `flags`, `regex`, `regex_error_text`) — an incomplete element fails deploy with `Params are not valid` (see [Required `params` element shape](#required-params-element-shape-deploy-validation)); use `"params": []` when no inputs are declared
2. Use the `required` flag for parameters that must be provided
2. Provide clear descriptions for each parameter
3. Use appropriate data types for parameters
4. Add regex validation for string parameters that need to follow specific patterns
5. Use the `auto-clear` flag for parameters that should not persist after processing
6. Document complex object parameters with examples of expected structure
