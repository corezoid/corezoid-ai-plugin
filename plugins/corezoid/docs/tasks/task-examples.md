# Task Examples

This document provides JSON examples of tasks with different parameters in Corezoid.

## Task Metadata

For documentation on task metadata fields (such as root.prev_node_id, root.node_id, etc.) and how to
use them in processes, see [Task Metadata](task-metadata.md).

## Set Parameters Built-in Functions

For documentation on built-in functions available in Set Parameters nodes (such as
$.math(),
$.random(), $.date(), etc.), see
[Set Parameters Built-in Functions](../nodes/set-parameters-built-in-functions.md).

## Basic Task Structure

```json
{
  "task_id": "TASK_67890",
  "ref": "REF_98765",
  "status": "processed",
  "user_id": "USER_54321",
  "create_time": 1617283200,
  "change_time": 1617283300,
  "node_id": "NODE_33333",
  "node_prev_id": "NODEPREV_44444",
  "data": {
    "customer_id": "12345",
    "amount": 100.5,
    "currency": "USD",
    "description": "Monthly subscription"
  }
}
```

## Task with Empty Data

```json
{
  "task_id": "TASK_12345",
  "ref": "REF_54321",
  "status": "new",
  "user_id": "USER_98765",
  "create_time": 1617283400,
  "change_time": 1617283400,
  "node_id": "NODE_11111",
  "node_prev_id": "",
  "data": {}
}
```

## Task with Nested Data Structure

```json
{
  "task_id": "TASK_24680",
  "ref": "REF_13579",
  "status": "processing",
  "user_id": "USER_86420",
  "create_time": 1617283500,
  "change_time": 1617283600,
  "node_id": "NODE_22222",
  "node_prev_id": "NODEPREV_33333",
  "data": {
    "customer": {
      "id": "CUS_12345",
      "name": "John Doe",
      "email": "john.doe@example.com",
      "address": {
        "street": "123 Main St",
        "city": "Anytown",
        "state": "CA",
        "zip": "12345"
      }
    },
    "order": {
      "id": "ORD_67890",
      "items": [
        {
          "product_id": "PROD_11111",
          "name": "Product 1",
          "quantity": 2,
          "price": 29.99
        },
        {
          "product_id": "PROD_22222",
          "name": "Product 2",
          "quantity": 1,
          "price": 49.99
        }
      ],
      "total": 109.97,
      "tax": 8.8,
      "shipping": 5.0,
      "grand_total": 123.77
    },
    "payment": {
      "method": "credit_card",
      "status": "approved",
      "transaction_id": "TXN_54321"
    }
  }
}
```

## Task with Array Data

```json
{
  "task_id": "TASK_13579",
  "ref": "REF_24680",
  "status": "processed",
  "user_id": "USER_97531",
  "create_time": 1617283700,
  "change_time": 1617283800,
  "node_id": "NODE_44444",
  "node_prev_id": "NODEPREV_55555",
  "data": {
    "batch_id": "BATCH_12345",
    "items": [
      {
        "id": "ITEM_11111",
        "status": "active",
        "value": 100
      },
      {
        "id": "ITEM_22222",
        "status": "inactive",
        "value": 200
      },
      {
        "id": "ITEM_33333",
        "status": "active",
        "value": 300
      }
    ],
    "total_items": 3,
    "total_value": 600
  }
}
```

## Task with Error Status

```json
{
  "task_id": "TASK_97531",
  "ref": "REF_86420",
  "status": "error",
  "user_id": "USER_13579",
  "create_time": 1617283900,
  "change_time": 1617284000,
  "node_id": "NODE_66666",
  "node_prev_id": "NODEPREV_77777",
  "data": {
    "customer_id": "12345",
    "amount": 100.5,
    "currency": "USD",
    "description": "Monthly subscription",
    "error": {
      "code": "PAYMENT_FAILED",
      "message": "Payment authorization failed",
      "details": "Insufficient funds"
    }
  }
}
```

## Task with Null Values in Nested Structure

```json
{
  "task_id": "TASK_86420",
  "ref": "REF_97531",
  "status": "processed",
  "user_id": "USER_24680",
  "create_time": 1617284100,
  "change_time": 1617284200,
  "node_id": "NODE_88888",
  "node_prev_id": "NODEPREV_99999",
  "data": {
    "customer_id": "12345",
    "email": "customer@example.com",
    "phone": "",
    "order_details": {
      "shipping_address": {
        "street": "123 Main St",
        "city": "Anytown",
        "state": "CA",
        "zip": "12345",
        "apartment": null
      },
      "billing_address": null
    },
    "preferences": {
      "notifications": {
        "email": true,
        "sms": false,
        "push": null
      }
    }
  }
}
```

## Task Creation Request Example

```json
{
  "ops": [
    {
      "action": "user",
      "company_id": "12345",
      "conv_id": 67890,
      "data": {
        "customer_id": "CUS_12345",
        "amount": 100.5,
        "currency": "USD",
        "description": "Monthly subscription"
      },
      "obj": "task",
      "ref": "REF_98765",
      "type": "create"
    }
  ]
}
```
