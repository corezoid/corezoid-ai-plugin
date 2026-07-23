# Guide: Converting Algorithms to Effective Corezoid Processes

This guide provides a systematic approach to converting algorithms into efficient, clear, and
maintainable Corezoid processes. By following these steps, you'll create processes that adhere to
best practices and optimize performance.

## Step 1: Analyze the Algorithm

Before creating a Corezoid process, thoroughly analyze the algorithm:

1. **Identify Inputs and Outputs**

   - List all required input parameters with their types
   - Define expected output parameters
   - Document validation requirements for inputs

2. **Break Down the Algorithm**

   - Identify distinct logical steps
   - Recognize decision points and branches
   - Identify potential error conditions
   - Note any loops or repetitive operations

3. **Identify External Dependencies**
   - List all API calls to external services
   - Document database queries or operations
   - Note any file operations or integrations

## Step 2: Design the Process Structure

Create a high-level design before implementation:

1. **Create a Process Flowchart**

   - Start with a simple diagram showing the main flow
   - Include all major decision points
   - Mark error handling paths
   - Identify potential asynchronous operations

2. **Optimize the Flow**

   - Apply the "Filter Early" principle for conditions
   - Group related operations to minimize node count
   - Position validation checks at the beginning
   - Design retry mechanisms for external calls

3. **Plan Error Handling Strategy**

   - Identify potential failure points
   - Design dedicated error paths for different error types
   - Plan retry mechanisms for transient failures

4. **Design Modular Process Architecture**
   - Instead of creating large monolithic processes, break down functionality into smaller, focused
     processes
   - Move related functionalities to separate processes and call them as functions from the main
     process
   - Organize processes in folders with a clear structure and naming convention
   - This approach enables:
     - Independent testing of each process component
     - Reusability across different workflows
     - Easier maintenance and updates
     - Better collaboration among team members
   - Example folder structure:
     ```
     Payment Processing (folder)
     ├── Main Payment Flow (process)
     ├── Validation (folder)
     │   ├── Customer Validation (process)
     │   └── Payment Method Validation (process)
     ├── Processing (folder)
     │   ├── Credit Card Processing (process)
     │   └── Bank Transfer Processing (process)
     └── Notification (folder)
         ├── Email Notification (process)
         └── SMS Notification (process)
     ```

## Step 3: Define Process Parameters

Configure the process parameters before adding nodes:

1. **Input Parameters**

   - Define all required input parameters with appropriate types
   - Add validation rules using regex where applicable
   - Include clear descriptions for each parameter

2. **Output Parameters**
   - Define all output parameters that will be returned
   - Ensure types match the expected return values
   - Include descriptive documentation

Example:

```json
"params": [
  {
    "name": "customer_id",
    "type": "string",
    "descr": "Unique customer identifier",
    "flags": ["required", "input"],
    "regex": "^[A-Z0-9]{8,12}$",
    "regex_error_text": "Customer ID must be 8-12 uppercase alphanumeric characters"
  },
  {
    "name": "transaction_result",
    "type": "object",
    "descr": "Transaction processing result",
    "flags": ["output"],
    "regex": "",
    "regex_error_text": ""
  }
]
```

## Step 4: Implement the Process Skeleton

Start with the basic structure:

1. **Add Start Node**

   - Configure with a descriptive title
   - Position at the top of the process

2. **Add Validation Nodes**

   - Add Condition nodes to validate input parameters
   - Route invalid inputs to dedicated error handling
   - Example validation condition:
     ```json
     {
       "conditions": [
         {
           "cast": "string",
           "const": "",
           "fun": "eq",
           "param": "customer_id"
         }
       ],
       "to_node_id": "error_node_id",
       "type": "go_if_const"
     }
     ```

3. **Add Final Nodes**

   - Create separate Final nodes for success and error paths
   - Position success path Final node at the bottom
   - Position error Final nodes to the right

4. **Add Reply to Process Nodes**
   - Add Reply nodes before each Final node
   - Configure success Reply node with standard format:
     ```json
     {
       "type": "api_rpc_reply",
       "mode": "key_value",
       "res_data": {
         "result": "success",
         "data": {
           "transaction_result": "{{transaction_result}}"
         }
       },
       "res_data_type": {
         "result": "string",
         "data": "object"
       }
     }
     ```
   - Configure error Reply nodes with appropriate error messages

## Step 5: Implement Core Logic

Implement the algorithm's core logic:

1. **Data Transformation**

   - Use Set Parameters nodes for simple transformations
   - Use Code nodes for complex logic
   - Example Set Parameters configuration:
     ```json
     {
       "type": "set_param",
       "extra": {
         "full_name": "{{first_name}} {{last_name}}",
         "transaction_amount": "$.math({{base_amount}} * (1 + {{tax_rate}}))"
       },
       "extra_type": {
         "full_name": "string",
         "transaction_amount": "number"
       }
     }
     ```

2. **Decision Points**

   - Implement Condition nodes for each decision point
   - Use clear condition logic with appropriate data types
   - Example condition for numeric comparison:
     ```json
     {
       "conditions": [
         {
           "cast": "number",
           "const": "1000",
           "fun": "gt",
           "param": "amount"
         }
       ],
       "to_node_id": "high_amount_path",
       "type": "go_if_const"
     }
     ```

3. **External Interactions**
   - Implement API Call nodes for external service calls
   - Add Database Call nodes for database operations
   - Example API Call configuration:
     ```json
     {
       "type": "api",
       "method": "POST",
       "max_threads": 5,
       "extra_headers": {},
       "url": "https://api.example.com/validate",
       "extra": {
         "customer_id": "{{customer_id}}",
         "amount": "{{amount}}"
       },
       "extra_type": {
         "customer_id": "string",
         "amount": "number"
       },
       "extra_headers": {
         "content-type": "application/json; charset=utf-8"
       }
     }
     ```

## Step 6: Implement Error Handling

Add comprehensive error handling:

1. **API Call Error Handling**

   - Add Condition nodes after API calls to check for errors
   - Route hardware errors to retry mechanisms
   - Route software errors to appropriate error handling
   - Example condition for API error type:
     ```json
     {
       "conditions": [
         {
           "cast": "string",
           "const": "hardware",
           "fun": "eq",
           "param": "__conveyor_api_return_type_error__"
         }
       ],
       "to_node_id": "retry_node_id",
       "type": "go_if_const"
     }
     ```

2. **Retry Mechanisms**

   - Implement Delay nodes for retry logic
   - Use exponential backoff for progressive delays
   - Add counter parameters to limit retry attempts
   - Example retry counter implementation:
     ```json
     {
       "type": "set_param",
       "extra": {
         "retry_count": "$.math({{retry_count}} + 1)"
       },
       "extra_type": {
         "retry_count": "number"
       }
     }
     ```

3. **Validation Error Handling**
   - Create dedicated error paths for different validation failures
   - Include descriptive error messages in Reply nodes
   - Example validation error reply:
     ```json
     {
       "type": "api_rpc_reply",
       "mode": "key_value",
       "res_data": {
         "result": "error",
         "error_code": "INVALID_CUSTOMER_ID",
         "error_message": "Customer ID format is invalid"
       },
       "res_data_type": {
         "result": "string",
         "error_code": "string",
         "error_message": "string"
       },
       "throw_exception": true
     }
     ```

## Step 7: Optimize for Performance

Refine the process for optimal performance:

1. **Minimize Node Count**

   - Combine related operations where possible
   - Use built-in functions instead of Code nodes for simple operations
   - Example of using built-in functions:
     ```json
     {
       "type": "set_param",
       "extra": {
         "random_id": "$.random(100000, 999999)",
         "current_date": "$.date('YYYY-MM-DD')",
         "hash_value": "$.md5_hex('{{input_string}}')"
       },
       "extra_type": {
         "random_id": "number",
         "current_date": "string",
         "hash_value": "string"
       }
     }
     ```

2. **Optimize Data Flow**

   - Prune unnecessary data fields
   - Prepare data for API calls in advance
   - Process API responses efficiently

3. **Implement Asynchronous Processing**
   - Use Queue nodes for non-critical operations
   - Implement parallel processing where appropriate
   - Example asynchronous pattern:
     ```
     Start → Process Data → Queue Node → End
                                ↓
                          Get from Queue → Process Asynchronously → End
     ```

## Step 8: Test and Validate

Thoroughly test the process before deployment:

1. **Unit Testing**

   - Test each node individually
   - Verify correct behavior for valid inputs
   - Test error handling with invalid inputs

2. **Integration Testing**

   - Test the entire process flow
   - Verify interactions with external systems
   - Test retry mechanisms

3. **Performance Testing**
   - Test with expected load
   - Identify and resolve bottlenecks
   - Verify memory usage stays within limits

## Step 9: Document the Process

Create comprehensive documentation:

1. **Process Overview**

   - Document the purpose and functionality
   - List input and output parameters
   - Describe main process flow

2. **Node Documentation**

   - Document key nodes and their configurations
   - Explain decision logic and conditions
   - Document error handling mechanisms

3. **Integration Points**
   - Document external API dependencies
   - List database interactions
   - Note any other integration points

## Step 10: Deploy and Monitor

Prepare for production deployment:

1. **Deployment Strategy**

   - Plan for zero-downtime deployment
   - Consider versioning strategy
   - Implement feature flags if needed

2. **Monitoring Setup**

   - Implement logging at key points
   - Set up alerts for critical errors
   - Monitor performance metrics

3. **Maintenance Plan**
   - Plan for regular reviews and updates
   - Document known limitations
   - Prepare for future enhancements

## Real-World Example: Payment Processing Algorithm

Let's convert a payment processing algorithm to a Corezoid process:

### Original Algorithm (Pseudocode):

```
function processPayment(customerId, amount, currency):
    // Validate inputs
    if customerId is empty or invalid format:
        return error("Invalid customer ID")
    if amount <= 0:
        return error("Invalid amount")
    if currency not in supported currencies:
        return error("Unsupported currency")

    // Get customer data
    customer = getCustomerById(customerId)
    if customer is null:
        return error("Customer not found")

    // Check balance
    if customer.balance < amount:
        return error("Insufficient funds")

    // Process payment
    try:
        transaction = createTransaction(customerId, amount, currency)
        updateBalance(customerId, customer.balance - amount)
        sendNotification(customerId, "Payment processed", amount, currency)
        return success(transaction)
    catch (error):
        logError(error)
        return error("Payment processing failed")
```

### Corezoid Process Implementation:

1. **Process Parameters:**

   - Input: customerId (string, required), amount (number, required), currency (string, required)
   - Output: transaction (object), status (string), message (string)

2. **Process Structure:**

   - Start Node → Input Validation → Get Customer Data → Check Balance → Process Payment → Reply
     (Success) → End
   - Error paths branch to the right with dedicated Reply (Error) → End nodes

3. **Key Nodes:**

   - Condition Nodes for validation checks
   - API Call Node to get customer data
   - Condition Node to check balance
   - API Call Node to create transaction
   - API Call Node to update balance
   - API Call Node to send notification
   - Reply to Process Node for success response
   - Multiple Reply to Process Nodes for different error conditions

4. **Error Handling:**

   - Dedicated error paths for each validation check
   - Hardware error retry for API calls
   - Comprehensive error messages in Reply nodes

5. **Optimization:**
   - Early validation to filter invalid requests
   - Efficient data flow with minimal transformations
   - Retry mechanism for transient failures

## Conclusion

Converting algorithms to Corezoid processes requires careful planning, structured implementation,
and attention to best practices. By following this guide, you'll create processes that are
efficient, maintainable, and resilient.

## Related Documentation

- [Process Overview](README.md) - Definition, operational mechanics, and key features of Corezoid
  processes
- [Process Output Parameters](process-output-parameters.md) - Documentation for defining output
  parameters
