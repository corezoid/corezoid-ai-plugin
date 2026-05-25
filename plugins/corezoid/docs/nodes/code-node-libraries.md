# Code Node Libraries and Usage

This document describes the available libraries, usage patterns, constraints, and best practices for
Code nodes in Corezoid processes.

## Overview

Code nodes in Corezoid run JavaScript (preferred) or Erlang code in a boxed standalone v8 engine
(version v8.1.97) with no web-interface support. They allow for complex logic, calculations, and
data manipulation beyond what standard nodes provide.

## Available Libraries

Code nodes have access to several built-in libraries that can be imported using the `require()`
function. These libraries provide functionality for cryptography, date manipulation, and more.

### Cryptographic Libraries

| Library     | Import Statement                 | Description                                              |
| ----------- | -------------------------------- | -------------------------------------------------------- |
| SHA-1       | `require("libs/sha1.js")`        | Implements the SHA-1 cryptographic hash function         |
| MD5         | `require("libs/md5.js")`         | Implements the MD5 message-digest algorithm              |
| HMAC-SHA1   | `require("libs/hmac-sha1.js")`   | Implements HMAC using SHA-1 for message authentication   |
| HMAC-MD5    | `require("libs/hmac-md5.js")`    | Implements HMAC using MD5 for message authentication     |
| HMAC-SHA256 | `require("libs/hmac-sha256.js")` | Implements HMAC using SHA-256 for message authentication |
| SHA-512     | `require("libs/sha512.js")`      | Implements the SHA-512 cryptographic hash function       |
| AES         | `require("libs/aes.js")`         | Implements the Advanced Encryption Standard              |
| Triple DES  | `require("libs/tripledes.js")`   | Implements the Triple Data Encryption Algorithm          |
| RC4         | `require("libs/rc4.js")`         | Implements the RC4 stream cipher                         |
| Base64      | `require("libs/base64.js")`      | Provides Base64 encoding and decoding functions          |

### Date and Time Libraries

| Library             | Import Statement                         | Description                                                                       |
| ------------------- | ---------------------------------------- | --------------------------------------------------------------------------------- |
| Date Utils          | `require("libs/dateutils.js")`           | Provides date formatting and parsing functions                                    |
| Moment.js           | `require("libs/moment.js")`              | Comprehensive library for parsing, validating, manipulating, and formatting dates |
| Moment Timezone     | `require("libs/moment-timezone.js")`     | Adds timezone support to Moment.js                                                |
| Moment with Locales | `require("libs/moment-with-locales.js")` | Adds internationalization support to Moment.js                                    |

### Other Libraries

| Library | Import Statement             | Description                                |
| ------- | ---------------------------- | ------------------------------------------ |
| XRegExp | `require("libs/xregexp.js")` | Extended JavaScript regular expressions    |
| Rabbit  | `require("libs/rabbit.js")`  | Implementation of the Rabbit stream cipher |

## Library Usage Examples

### Cryptographic Functions

#### SHA-1 Hash

```javascript

  var CryptoJS = require("libs/sha1.js");

  // Create a SHA-1 hash
  var hash = CryptoJS.SHA1("message to hash").toString();

  data.hash_result = hash;
  
```

#### MD5 Hash

```javascript

  var CryptoJS = require("libs/md5.js");

  // Create an MD5 hash
  var hash = CryptoJS.MD5(data.password).toString();

  data.password_hash = hash;
  
```

#### AES Encryption/Decryption

```javascript

  var CryptoJS = require("libs/aes.js");

  // Encrypt
  var encrypted = CryptoJS.AES.encrypt(data.plaintext, data.secret_key).toString();
  data.encrypted = encrypted;

  // Decrypt
  var decrypted = CryptoJS.AES.decrypt(encrypted, data.secret_key).toString(CryptoJS.enc.Utf8);
  data.decrypted = decrypted;

  
```

### Date Manipulation

#### Using Date Utils

```javascript

  require("libs/dateutils.js");

  // Convert date from one format to another
  var formattedDate = fn_convertDate(data.date, "yyyy-MM-dd", "dd/MM/yyyy");
  data.formatted_date = formattedDate;

  // Get date with offset (e.g., tomorrow)
  var tomorrow = fn_getDate(1, "yyyy-MM-dd");
  data.tomorrow = tomorrow;

  
```

#### Using Moment.js

```javascript

  var moment = require("libs/moment.js");

  // Parse and format a date
  var date = moment(data.timestamp);
  data.formatted_date = date.format("YYYY-MM-DD HH:mm:ss");

  // Add time
  data.next_week = moment().add(7, 'days').format("YYYY-MM-DD");

  // Calculate duration
  var start = moment(data.start_date);
  var end = moment(data.end_date);
  data.duration_days = end.diff(start, 'days');

  
```

#### Using Moment Timezone

```javascript

  var moment = require("libs/moment-timezone.js");

  // Convert timestamp to specific timezone
  var date = moment(data.timestamp).tz("Europe/Kiev");
  data.local_time = date.format("YYYY-MM-DD HH:mm:ss");

  // Get current time in different timezone
  data.ny_time = moment().tz("America/New_York").format("HH:mm:ss");

  
```

## Code Node Constraints

### Execution Environment

1. **JavaScript Version**: Code nodes use v8 engine version v8.1.97
2. **No DOM Access**: No access to browser-specific objects like `window`, `document`, etc.
3. **No Network Access**: Cannot make direct HTTP requests or open network connections
4. **Execution Timeout**: Code execution is limited to a configurable timeout (default: 1000ms)
5. **Memory Limitations**: Limited memory allocation to prevent resource abuse

### Data Handling

1. **Data Object**: All task data is accessible through the `data` object parameter
2. **Return Value**: Must return the modified `data` object
3. **Root Access**: Task metadata is accessible through `data.__root` object
4. **Size Limitations**: Total task data size is limited to 2MB

### Console and Logging

1. **No Console Output**: `console.log()` and other console methods have no effect
2. **Custom Logging**: For logging, use an artificial array key in the data object:
   ```javascript
   data._ = data._ || [];
   data._.push("Log message");
   ```

## Best Practices

### Code Structure

1. **Function Wrapper**: Always wrap your code in a function that accepts and returns the `data`
   object:

   ```javascript
   
     // Your code here
     
   ```

2. **Error Handling**: Use try/catch blocks to handle errors gracefully:

   ```javascript
   
     try {
       // Your code here
        catch (e) {
       data.error = e.toString();
       return data;
     }
   }
   ```

3. **Modular Code**: Keep code modular and focused on a single responsibility

### Performance Optimization

1. **Minimize Library Usage**: Only require libraries you actually need
2. **Avoid Deep Recursion**: Recursive functions can quickly exceed stack limits
3. **Limit Data Size**: Process only necessary data to stay within size limits
4. **Early Returns**: Use early returns for validation to avoid unnecessary processing:
   ```javascript
   
     if (!data.input) {
       data.error = "Missing input";
       
     // Process valid input
     return data;
   }
   ```

### Security Considerations

1. **Input Validation**: Always validate input data before processing
2. **Avoid eval()**: Never use `eval()` or similar functions that execute dynamic code
3. **Secure Cryptography**: Use strong cryptographic algorithms for sensitive data
4. **Data Sanitization**: Sanitize data that will be used in database queries or API calls

## Node Configuration

### Error Handling

Each Code node should have its own dedicated error escalation node:

1. Create a basic "error" END node positioned to the right of each Code node
2. Connect the Code node's error path (via the "err_node_id" parameter) to its dedicated error node
3. This one-to-one mapping between Code nodes and their error nodes improves error isolation and
   troubleshooting

### Timeout Configuration

Set an appropriate timeout value based on the complexity of your code:

1. Simple transformations: 1000ms (default)
2. Complex calculations or multiple library operations: 2000-5000ms
3. Very complex operations: Up to 10000ms (use with caution)

## Examples

### Basic Data Transformation

```javascript

  // Add a new field
  data.greeting = "Hello, " + (data.name || "World");

  // Transform existing data
  if (data.amount) {
    data.amount_with_tax = data.amount * 1.2;
  }

  
```

### Working with Arrays

```javascript

  // Initialize array if it doesn't exist
  data.items = data.items || [];

  // Add a new item
  data.items.push({
    id: data.items.length + 1,
    name: "New Item",
    timestamp: new Date().getTime()
  });

  // Calculate total
  data.total = data.items.reduce(function(sum, item) {
    return sum + (item.price || 0);
  }, 0);

  
```

### Dynamic Array Operations (When Set Parameters Nodes Can't Be Used)

```javascript

  // Initialize test results if they don't exist
  data.test_results = data.test_results || {
    total: 0,
    passed: 0,
    failed: 0,
    tests: []
  };

  // Add a test result to the array
  // This operation can't be done with Set Parameters nodes due to dynamic index limitations
  data.test_results.tests.push({
    name: data.current_test.name,
    status: data.current_test.passed ? 'passed' : 'failed',
    result: data.result
  });

  // Update counters
  data.test_results.total += 1;
  data.test_results.passed += data.current_test.passed ? 1 : 0;
  data.test_results.failed += data.current_test.passed ? 0 : 1;

  
```

### Complex Data Processing with Libraries

```javascript

  try {
    // Require necessary libraries
    var moment = require("libs/moment.js");
    var CryptoJS = require("libs/sha256.js");

    // Process dates
    var now = moment();
    data.current_date = now.format("YYYY-MM-DD");

    // Calculate expiration date (30 days from now)
    data.expiry_date = now.add(30, 'days').format("YYYY-MM-DD");

    // Generate a secure token
    var tokenInput = data.user_id + "|" + data.current_date + "|" + data.secret;
    data.token = CryptoJS.SHA256(tokenInput).toString();

    // Log processing steps
    data._ = data._ || [];
    data._.push("Generated token for user: " + data.user_id);

     catch (e) {
    // Handle errors
    data.error = e.toString();
    data.error_time = new Date().toISOString();
    return data;
  }
}
```

## Related Documentation

- [Code Node](code-node.md) - Basic information about Code nodes
