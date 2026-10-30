# Set Parameters Node Built-in Functions

This document describes the built-in functions available for use in Set Parameters nodes in Corezoid
processes.

## Overview

Set Parameters nodes support several built-in functions that can be used to perform operations
directly within parameter values. These functions provide capabilities for mathematical operations,
random number generation, date/time manipulation, and cryptographic operations.

## Function Syntax

All built-in functions use the `$.functionName(arguments)` syntax pattern. Functions can be used
directly in parameter values or combined with parameter references using the `{{parameter}}` syntax.

## Available Functions

### Mathematical Operations

#### `$.math(expression)`

Performs mathematical calculations on numeric values.

**Syntax:**

```
$.math(expression)
```

**Arguments:**

- `expression`: A mathematical expression that can include numbers, operators (+, -, \*, /), and
  parameter references.

**Examples:**

```
$.math(100+50)                    // Returns: 150
$.math({{value}}*2)               // Multiplies the 'value' parameter by 2
$.math({{price}}*{{quantity}})    // Multiplies 'price' by 'quantity'
$.math({{value}}/100)             // Divides 'value' by 100
$.math($.math({{value}}+1)-1)     // Nested math functions
```

**Notes:**

- The result is always returned as a string representation of the number
- Decimal results are returned with a decimal point (e.g., "50.0")
- Can be used in array indices: `{{array[$.math({{index}}+1)]}}`
- **One operator per expression.** Only two operands are evaluated: `$.math({{a}}+{{b}}+{{c}})`
  returns `a+b` and `$.math({{a}}+{{b}}*2)` returns `a+b` — silently, without an error. Compute
  one operation per key and keep intermediate results in separate keys
- Supported operators: `+ - * /`. There is no `%`, `^`, `div`, `rem`, `floor`, `round`, `abs` or
  `trunc` — such a key goes to the error branch
- `+ - *` are exact for large integers (19+ digits); `/` returns a float
  (`$.math(4111111111111111/10)` → `411111111111111.125`)
- `lint-process` may report a key as unused (`UNUSED SET_PARAM`) when it is referenced only
  inside `$.math(...)` — a false positive

### Random Number Generation

#### `$.random()`

Generates a random floating-point number between 0 and 1.

**Syntax:**

```
$.random()
```

**Example:**

```
$.random()  // Returns a random number between 0 and 1
```

#### `$.random(max)`

Generates a random integer between 1 and the specified maximum value.

**Syntax:**

```
$.random(max)
```

**Arguments:**

- `max`: The maximum value (inclusive) for the random number.

**Example:**

```
$.random(10)  // Returns a random integer between 1 and 10
```

#### `$.random(min, max)`

Generates a random integer between the specified minimum and maximum values.

**Syntax:**

```
$.random(min, max)
```

**Arguments:**

- `min`: The minimum value (inclusive) for the random number.
- `max`: The maximum value (inclusive) for the random number.

**Examples:**

```
$.random(50, 100)                // Returns a random integer between 50 and 100
$.random({{min}}, {{max}})       // Uses parameter values for the range
$.random(1, {{max_value}})       // Combines literal and parameter values
```

### Date and Time Functions

#### `$.date(format)`

Formats the current date and time according to the specified format string.

**Syntax:**

```
$.date(format)
```

**Arguments:**

- `format`: A string containing date format specifiers.

**Format Specifiers:**

- `%y` - Year (4 digits)
- `%m` - Month (2 digits)
- `%d` - Day (2 digits)
- `%h` - Hour (2 digits, 24-hour format)
- `%i` - Minute (2 digits)
- `%s` - Second (2 digits)

**Examples:**

```
$.date(%y-%m-%d)                 // Returns: "2025-04-07" (current date)
$.date(%y-%m-%d %h:%i:%s)        // Returns: "2025-04-07 18:15:30" (current date and time)
$.date(%y-%m-%d+1-%h:%i:%s)      // Adds 1 day to the current date
```

**Notes:**

- The format string can include any characters; only the format specifiers are replaced
- To add or subtract from date components, append +N or -N to the format specifier

#### `$.unixtime(format)`

Converts a formatted date string to a Unix timestamp (seconds since January 1, 1970).

**Syntax:**

```
$.unixtime(format)
```

**Arguments:**

- `format`: A string containing date format specifiers (same as `$.date()`).

**Example:**

```
$.unixtime(%y-%m-%d %h:%i:%s)    // Returns the Unix timestamp for the specified date
```

**Current time:** `$.unixtime()` without arguments returns the current Unix time in seconds — but
only when it is the **whole value** of a key typed `number` (also in a Reply node `res_data`).
Wrapped in a template or embedded in a string / object JSON (`{{$.unixtime()}}`,
`"{\"at\":{{$.unixtime()}}}"`) it resolves to `""`, and in an `object` key that makes the JSON
invalid (error branch). To put the time into an object, set it in one node
(`"checked_at": "$.unixtime()"`, `number`) and reference `{{checked_at}}` in the next node.

### Cryptographic Functions

#### `$.base64_encode(string)`

Encodes a string using Base64 encoding.

**Syntax:**

```
$.base64_encode(string)
```

**Arguments:**

- `string`: The string to encode.

**Example:**

```
$.base64_encode(hello)           // Returns: "aGVsbG8="
$.base64_encode({{username}})    // Encodes the 'username' parameter
```

#### `$.md5(string)`

Calculates the MD5 hash of a string and returns the raw binary result.

**Syntax:**

```
$.md5(string)
```

**Arguments:**

- `string`: The string to hash.

#### `$.md5_hex(string)`

Calculates the MD5 hash of a string and returns the hexadecimal representation.

**Syntax:**

```
$.md5_hex(string)
```

**Arguments:**

- `string`: The string to hash.

**Example:**

```
$.md5_hex(password123)           // Returns the MD5 hash in hexadecimal
$.md5_hex({{password}})          // Hashes the 'password' parameter
```

#### `$.sha1(string)` and `$.sha1_hex(string)`

Calculates the SHA-1 hash of a string, returning either the raw binary result or the hexadecimal
representation.

#### `$.sha224(string)` and `$.sha224_hex(string)`

Calculates the SHA-224 hash of a string, returning either the raw binary result or the hexadecimal
representation.

#### `$.sha256(string)` and `$.sha256_hex(string)`

Calculates the SHA-256 hash of a string, returning either the raw binary result or the hexadecimal
representation.

#### `$.sha384(string)` and `$.sha384_hex(string)`

Calculates the SHA-384 hash of a string, returning either the raw binary result or the hexadecimal
representation.

#### `$.sha512(string)` and `$.sha512_hex(string)`

Calculates the SHA-512 hash of a string, returning either the raw binary result or the hexadecimal
representation.

### Array Functions

#### `$.map(function, array)`

Applies a function to each element of an array and returns a new array with the results.

**Syntax:**

```
$.map(fun(item) -> expression end, {{array}})
```

**Arguments:**

- `function`: An Erlang function expression that will be applied to each element
- `array`: A parameter reference that contains an array

**Examples:**

```
// Basic arithmetic operations
$.map(fun(x) -> x * 2 end, {{numbers}})            // Multiplies each element by 2
$.map(fun(item) -> item + 10 end, {{values}})      // Adds 10 to each element
$.map(fun(Item) -> Item div 3 end, {{numbers}})    // Integer division of each element by 3
$.map(fun(Item) -> Item rem 3 end, {{numbers}})    // Remainder of division by 3 for each element
$.map(fun(obj) -> obj.status end, {{users}})       // Extracts the status field from each object

// Using type checks and conversions
$.map(fun(x) ->
  case is_binary(x) of
    true -> erlang:binary_to_integer(x);
    false -> x
  end
end, {{mixed_values}})                             // Converts binary strings to integers

// Using binary operations
$.map(fun(str) ->
  binary:replace(str, <<"old">>, <<"new">>, [global])
end, {{strings}})                                  // Replaces all occurrences of "old" with "new"

// Using proplists functions
$.map(fun(obj) ->
  proplists:get_value(<<"key">>, obj, <<"default">>)
end, {{objects}})                                  // Extracts a value from each proplist with default

// Using list operations
$.map(fun(list) ->
  case is_list(list) of
    true -> [hd(list)];
    false -> [list]
  end
end, {{nested_lists}})                             // Gets the first element of each list or wraps non-lists

// Bitwise operations
$.map(fun(Item) -> Item band 2 end, {{numbers}})   // Bitwise AND with 2
$.map(fun(Item) -> Item bxor 2 end, {{numbers}})   // Bitwise XOR with 2
$.map(fun(Item) -> Item bsl 2 end, {{numbers}})    // Bitwise shift left by 2 bits
$.map(fun(Item) -> Item bsr 2 end, {{numbers}})    // Bitwise shift right by 2 bits
$.map(fun(Item) -> Item bor 2 end, {{numbers}})    // Bitwise OR with 2

// Comparison operations
$.map(fun(Item) -> Item == 2 end, {{numbers}})     // Equality check (returns true/false)
$.map(fun(Item) -> Item < 2 end, {{numbers}})      // Less than check
$.map(fun(Item) -> Item > 2 end, {{numbers}})      // Greater than check
$.map(fun(Item) -> Item >= 2 end, {{numbers}})     // Greater than or equal check
$.map(fun(Item) -> Item =< 2 end, {{numbers}})     // Less than or equal check
$.map(fun(Item) -> Item /= 2 end, {{numbers}})     // Not equal check
$.map(fun(Item) -> Item =:= 2 end, {{numbers}})    // Exact equality check
$.map(fun(Item) -> Item =/= 2 end, {{numbers}})    // Exact inequality check

// Logical operations
$.map(fun(Item) -> Item xor true end, {{booleans}})      // Logical XOR with true
$.map(fun(Item) -> Item andalso true end, {{booleans}})  // Logical AND (short-circuit)
$.map(fun(Item) -> Item orelse true end, {{booleans}})   // Logical OR (short-circuit)
$.map(fun(Item) -> Item and true end, {{booleans}})      // Logical AND
$.map(fun(Item) -> Item or true end, {{booleans}})       // Logical OR
$.map(fun(Item) -> not Item end, {{booleans}})          // Logical NOT

// List concatenation
$.map(fun(Item) -> Item ++ [{<<"key">>, 1}] end, {{lists}})  // Appends a new key-value pair to each list
```

**Notes:**

- The function must be a valid Erlang function expression using the syntax
  `fun(param) -> expression end`
- The array parameter must be an actual array, or the result will be empty
- Only the following operations are allowed in the function body:
  - Arithmetic operators: +, -, \*, /, div, rem
  - Comparison operators: ==, /=, =<, <, >=, >, =:=, =/=
  - Logical operators: not, and, or, andalso, orelse
  - Bitwise operators: bnot, band, bor, bxor, bsl, bsr
  - List operators: ++, --
- The following external functions can be called:
  - proplists module functions
  - base64 module functions
  - binary:split/2,3 and binary:replace/3,4
  - eutils:get_value/2, eutils:from_json/1,2, eutils:to_json/1
  - erlang:binary_to_float/1, erlang:binary_to_integer/1
  - erlang:integer_to_binary/1,2
  - erlang:round/1
  - erlang type checks: is_integer/1, is_binary/1, is_list/1, is_float/1, is_boolean/1, is_number/1
  - erlang:hd/1, erlang:tl/1
- You can reference object properties using the dot notation in the function
- You can call other built-in functions like $.math() within the function

#### `$.filter(function, array)`

Filters an array based on a predicate function, returning a new array with elements that pass the
filter.

**Syntax:**

```
$.filter(fun(item) -> condition end, {{array}})
```

**Arguments:**

- `function`: An Erlang function that returns true or false for each element
- `array`: A parameter reference that contains an array

**Examples:**

```
// Basic comparison operations
$.filter(fun(x) -> x > 10 end, {{numbers}})                // Returns elements greater than 10
$.filter(fun(item) -> item.active =:= true end, {{users}}) // Returns only active users
$.filter(fun(str) -> str =/= "" end, {{strings}})          // Removes empty strings

// Using type checks
$.filter(fun(x) -> is_number(x) end, {{mixed_values}})     // Keeps only numeric values
$.filter(fun(x) -> is_binary(x) andalso byte_size(x) > 5 end, {{strings}})  // Keeps strings longer than 5 bytes
$.filter(fun(Item) ->
  Test = proplists:get_value(<<"test">>, Item),
  is_integer(Test)
end, {{objects}})                                          // Keeps only objects with integer 'test' property
$.filter(fun(Item) ->
  Test = proplists:get_value(<<"test">>, Item),
  is_float(Test)
end, {{objects}})                                          // Keeps only objects with float 'test' property

// Using binary operations
$.filter(fun(str) ->
  case binary:match(str, <<"search">>) of
    nomatch -> false;
    _ -> true
  end
end, {{texts}})                                            // Keeps only strings containing "search"

// Using proplists functions
$.filter(fun(obj) ->
  proplists:is_defined(<<"required_key">>, obj)
end, {{objects}})                                          // Keeps only objects with a specific key

// Using JSON conversion
$.filter(fun(json_str) ->
  Obj = eutils:from_json(json_str),
  eutils:get_value(<<"status">>, Obj) =:= <<"active">>
end, {{json_strings}})                                     // Filters JSON strings by a property value

// Advanced filtering with pattern matching
$.filter(fun(Item) ->
  case Item of
    [{<<"type">>, <<"user">>} | _] -> true;
    _ -> false
  end
end, {{records}})                                          // Keeps only records with type 'user'
```

**Notes:**

- The function must return a boolean value (true to keep the element, false to filter it out)
- The array parameter must be an actual array, or the result will be empty
- Only the following operations are allowed in the function body:
  - Arithmetic operators: +, -, \*, /, div, rem
  - Comparison operators: ==, /=, =<, <, >=, >, =:=, =/=
  - Logical operators: not, and, or, andalso, orelse
  - Bitwise operators: bnot, band, bor, bxor, bsl, bsr
  - List operators: ++, --
- The following external functions can be called:
  - proplists module functions
  - base64 module functions
  - binary:split/2,3 and binary:replace/3,4
  - eutils:get_value/2, eutils:from_json/1,2, eutils:to_json/1
  - erlang:binary_to_float/1, erlang:binary_to_integer/1
  - erlang:integer_to_binary/1,2
  - erlang:round/1
  - erlang type checks: is_integer/1, is_binary/1, is_list/1, is_float/1, is_boolean/1, is_number/1
  - erlang:hd/1, erlang:tl/1
- You can reference object properties using the dot notation in the function
- You can call other built-in functions like $.random() within the function
- Verified pitfalls:
  - `lists:*` functions (`lists:member`, `lists:any`, …) are not allowed — the key goes to the
    error branch. Use `orelse` / `=:=` chains instead
  - JSON `null` arrives as the atom `null`, and in Erlang term order an atom is greater than any
    number: `null > 0` is **true**. Guard numeric checks:
    `(is_number(V) andalso V > 0)`
  - An empty string is `<<>>`; read object fields with
    `proplists:get_value(<<"key">>, Item, Default)`
  - To test "is the result non-empty", use a Condition on `{{result[0].id}}` `eq ""` (a missing
    element resolves to `""`, see the Condition node docs)

## Function Nesting

Built-in functions can be nested within each other for more complex operations:

```
$.math($.math({{value}}+1)-1)
$.unixtime($.date({{format}}))
$.sha1_hex($.base64_encode({{username}}))
$.map(fun(x) -> $.math(x * 2) end, {{numbers}})
$.filter(fun(x) -> x > $.random(100) end, {{numbers}})
```

## Usage in Set Parameters Node

To use these functions in a Set Parameters node:

1. Create a Set Parameters node in your process
2. Add a new parameter with the desired name
3. In the value field, use any of the built-in functions described above
4. Optionally, combine with parameter references using the `{{parameter}}` syntax

**Example JSON Configuration:**

```json
{
  "type": "set_param",
  "mode": "key_value",
  "extra": {
    "random_number": "$.random(1, 100)",
    "current_date": "$.date(%y-%m-%d)",
    "calculated_value": "$.math({{base_value}}*1.2)",
    "hashed_password": "$.sha256_hex({{password}})",
    "doubled_numbers": "$.map(fun(x) -> x * 2 end, {{numbers}})",
    "filtered_users": "$.filter(fun(user) -> user.age >= 18 end, {{users}})"
  },
  "extra_type": {
    "random_number": "number",
    "current_date": "string",
    "calculated_value": "number",
    "hashed_password": "string",
    "doubled_numbers": "array",
    "filtered_users": "array"
  }
}
```

## Examples

Below are examples of Set Parameters nodes from actual Corezoid processes using built-in functions:

### Using $.map() for arithmetic operations

```json
{
  "type": "set_param",
  "extra": {
    "test": "$.map(fun(Item) -> Item div 3 end, {{newtest}})",
    "test1": "$.map(fun(Item) -> Item rem 3 end, {{newtest}})",
    "test2": "$.map(fun(Item) -> Item +1 end, {{newtest}})"
  },
  "extra_type": {
    "test": "string",
    "test1": "string",
    "test2": "string"
  },
  "err_node_id": "error_node_id"
}
```

### Using $.map() for bitwise operations

```json
{
  "type": "set_param",
  "extra": {
    "band": "$.map(fun(Item) -> Item band 2 end, {{newtest}})",
    "bxor": "$.map(fun(Item) -> Item bxor 2 end, {{newtest}})",
    "bsl": "$.map(fun(Item) -> Item bsl 2 end, {{newtest}})",
    "bsr": "$.map(fun(Item) -> Item bsr 2 end, {{newtest}})"
  },
  "extra_type": {
    "band": "string",
    "bxor": "string",
    "bsl": "string",
    "bsr": "string"
  },
  "err_node_id": "error_node_id"
}
```

### Using $.map() for comparison operations

```json
{
  "type": "set_param",
  "extra": {
    "==": "$.map(fun(Item) -> Item == 2 end, {{newtest}})",
    "<": "$.map(fun(Item) -> Item < 2 end, {{newtest}})",
    ">": "$.map(fun(Item) -> Item > 2 end, {{newtest}})",
    ">=": "$.map(fun(Item) -> Item >= 2 end, {{newtest}})",
    "<=": "$.map(fun(Item) -> Item =< 2 end, {{newtest}})",
    "/=": "$.map(fun(Item) -> Item /= 2 end, {{newtest}})",
    "=:=": "$.map(fun(Item) -> Item =:= 2 end, {{newtest}})",
    "=/=": "$.map(fun(Item) -> Item =/= 2 end, {{newtest}})"
  },
  "extra_type": {
    "==": "string",
    "<": "string",
    ">": "string",
    ">=": "string",
    "<=": "string",
    "/=": "string",
    "=:=": "string",
    "=/=": "string"
  },
  "err_node_id": "error_node_id"
}
```

### Using $.map() for logical operations

```json
{
  "type": "set_param",
  "extra": {
    "xor": "$.map(fun(Item) -> Item xor true end, {{oldtest}})",
    "andalso": "$.map(fun(Item) -> Item andalso true end, {{oldtest}})",
    "orelse": "$.map(fun(Item) -> Item orelse true end, {{oldtest}})"
  },
  "extra_type": {
    "xor": "string",
    "andalso": "string",
    "orelse": "string"
  },
  "err_node_id": "error_node_id"
}
```

### Using $.filter() with type checking

```json
{
  "type": "set_param",
  "extra": {
    "is_integer": "$.filter(fun(Item) -> Test = proplists:get_value(<<\"test\">>, Item), is_integer(Test) end, {{b}})",
    "is_float": "$.filter(fun(Item) -> Test = proplists:get_value(<<\"test\">>, Item), is_float(Test) end, {{b}})"
  },
  "extra_type": {
    "is_integer": "array",
    "is_float": "array"
  },
  "err_node_id": "error_node_id"
}
```

### Data initialization for the examples above

```json
{
  "type": "set_param",
  "extra": {
    "oldtest": "[true, false]",
    "oldtest1": "[{\"test\":1},{\"test1\":2}]",
    "newtest": "[1, 2,3]",
    "b": "[{\"test\":\"3FF\"},{\"test\":30},{\"test\":[{\"a\":1}]},{\"test\":30.3},{\"test\":true}]"
  },
  "extra_type": {
    "oldtest": "string",
    "oldtest1": "array",
    "newtest": "array",
    "b": "array"
  },
  "err_node_id": "error_node_id"
}
```

## Performance Considerations

Built-in functions in Set Parameters nodes execute significantly faster than equivalent code in Code
nodes. This performance advantage occurs because:

1. Built-in functions are executed directly by the worker process on the fly
2. Code nodes require sending the logic to the conveyor api-code component, adding network overhead
   and processing time

Even within Code nodes, there are performance differences based on the language used:

1. Erlang code executes faster because it's the native language of the Corezoid system
2. JavaScript code requires starting the Google V8 engine as an execution environment, adding
   overhead

For operations that can be accomplished with built-in functions, using Set Parameters nodes will
result in better performance and lower latency. When a Code node is necessary, consider using Erlang
for performance-critical operations.

## Best Practices

1. **Type Conversion**: Always specify the correct data type in the `extra_type` section to ensure
   proper type conversion
2. **Error Handling**: Add error handling nodes after Set Parameters nodes to catch potential errors
   in function evaluation
3. **Performance**: Use built-in functions in Set Parameters nodes instead of Code nodes when
   possible
4. **Security**: Use cryptographic functions for sensitive data, but remember that the results are
   stored in the task data
5. **Testing**: Test your functions with various inputs to ensure they handle edge cases correctly
