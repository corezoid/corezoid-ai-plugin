# Simulator.Company User Flows

This document provides an overview of common user flows in the Simulator.Company platform, illustrating how different API endpoints are used together to accomplish specific tasks.

## User Flow Overview

The Simulator.Company platform provides a comprehensive API for managing business processes, graph structures, forms, and financial tracking:

1. **Form Management** - Creating custom form templates and actor instances
2. **Graph Management** - Creating and managing actors, links, and layers
3. **Financial Management** - Managing accounts, transactions, and transfers

## User Flow Documentation

### Form Management

- [Custom Car Form](./custom-car-form.md) - Creating a custom form, instantiating actors from it, and attaching financial accounts

### Graph Management

- [Graph Functionality](./graph-functionality.md) - Creating and managing actors, links, and layers in graph structures
- [Actor Graph Management](./actor-graph-management.md) - Managing actors on graphs: creating links, organizing on layers, searching and filtering

## Related Entity Documentation

For detailed information about the entities used in these user flows, please refer to:

- [Actors](../entities/actors.md) - Core entity representing nodes in a business process graph
- [Forms](../entities/forms.md) - Reusable data structure templates for actors
- [Links](../entities/links.md) - Connections between actors forming graph structures
- [Layers](../entities/layers.md) - Visual organization of actors and edges
- [Accounts](../entities/accounts.md) - Financial tracking for actors
- [Transactions](../entities/transactions.md) - Financial operations within accounts
- [Transfers](../entities/transfers.md) - Movement of funds between accounts
- [Attachments](../entities/attachments.md) - File storage system for actors

## Authentication

All API requests require a Bearer token:

```
Authorization: Simulator <your_token>
```

The token is set via the `ACCESS_TOKEN` environment variable and passed automatically by the MCP server.

## API Documentation

For the full API reference see [doc.simulator.company](https://doc.simulator.company).
