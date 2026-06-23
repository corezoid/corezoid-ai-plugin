# Process Development Guide

This guide provides a comprehensive approach to developing effective processes in Corezoid, from
initial planning to deployment and maintenance.

## Process Development Lifecycle

### 1. Planning and Design

Before creating a process, consider these key aspects:

- **Define the Process Purpose**: What business problem does this process solve?
- **Identify Inputs and Outputs**: What data will flow into and out of the process?
- **Map the Process Flow**: Sketch the logical flow of operations
- **Identify Integration Points**: What external systems will the process interact with?
- **Define Error Handling Strategy**: How will different error scenarios be managed?

#### Design Checklist

- [ ] Process purpose clearly defined
- [ ] Input parameters identified and documented
- [ ] Output parameters identified and documented
- [ ] Process flow mapped with decision points
- [ ] Integration requirements documented
- [ ] Error handling strategy defined
- [ ] Performance requirements identified

### 2. Implementation

When implementing a process, follow these best practices:

- **Start with a Skeleton**: Create a basic process with Start and End nodes
- **Build Incrementally**: Add and test nodes in logical groups
- **Validate Early**: Test each section before moving to the next
- **Document as You Go**: Add descriptive titles and descriptions to nodes

#### Implementation Checklist

- [ ] Basic process structure created with Start and End nodes
- [ ] Input validation implemented
- [ ] Main process logic implemented
- [ ] Integration points configured
- [ ] Error handling paths implemented
- [ ] Node positioning follows best practices
- [ ] Node titles and descriptions are clear and descriptive

### 3. Testing

Thorough testing is essential for reliable processes:

- **Unit Testing**: Test individual nodes and sections
- **Integration Testing**: Test interactions with external systems
- **End-to-End Testing**: Test the complete process flow
- **Error Testing**: Deliberately trigger error conditions to test handling

#### Testing Checklist

- [ ] All input validation scenarios tested
- [ ] All main process paths tested
- [ ] All error handling paths tested
- [ ] Integration with external systems verified
- [ ] Performance under expected load tested
- [ ] Edge cases and boundary conditions tested

### 4. Deployment

When deploying a process to production:

- **Version Control**: Maintain clear versioning of processes
- **Documentation**: Ensure all documentation is complete and up-to-date
- **Monitoring**: Set up monitoring for process performance and errors
- **Rollback Plan**: Have a plan for reverting to previous versions if needed

#### Deployment Checklist

- [ ] Process version documented
- [ ] Process documentation complete
- [ ] Monitoring configured
- [ ] Rollback plan defined
- [ ] Stakeholders informed of deployment

### 5. Maintenance

Ongoing maintenance ensures process reliability:

- **Regular Reviews**: Periodically review process performance and error rates
- **Updates**: Apply updates for changing business requirements
- **Optimization**: Identify and implement performance improvements
- **Documentation Updates**: Keep documentation current with changes

## Common Process Patterns

### Sequential Process Pattern

The simplest process pattern, where tasks flow linearly from one node to the next:

```
Start Node → Process Node 1 → Process Node 2 → ... → Process Node N → End Node
```

**Best for**: Simple, linear workflows with minimal decision points

### Conditional Branching Pattern

Processes that need to take different paths based on data values:

```
                    ┌─── [condition A] ──→ Path A ──→┐
                    │                               │
Start Node → Condition Node ─┼─── [condition B] ──→ Path B ──→┼─→ End Node
                    │                               │
                    └─── [default] ──→ Path C ──→┘
```

**Best for**: Workflows with distinct paths based on data values or states

### Request-Response Pattern

Processes that call external services and handle responses:

```
Start Node → API Call Node → Condition Node (Check Response) → Process Response → End Node
                                │
                                └─── [error] ──→ Error Handling → End Node
```

**Best for**: Integration with external systems and APIs

### Validation Pattern

Processes that validate inputs before processing:

```
                    ┌─── [invalid input] ──→ Error Response → End Node
                    │
Start Node → Validation Node ─┤
                    │
                    └─── [valid input] ──→ Process Logic → End Node
```

**Best for**: Processes with complex input validation requirements

### Retry Pattern

Processes that implement retry logic for transient failures:

```
Start Node → Operation Node ──→ Success Node → End Node
                │
                └─── [error] ──→ Condition Node ─┬─── [retry count < max] ──→ Delay Node ──┐
                                               │                           │
                                               │                           ↓
                                               │                      Increment Retry Count
                                               │                           │
                                               │                           └───────────────┘
                                               │
                                               └─── [retry count >= max] ──→ Failure Node → End Node
```

**Best for**: Operations with potential transient failures (network calls, external APIs)

## Decision Guide: Choosing the Right Node Type

### For Data Transformation

- **Simple Key-Value Updates**: Use [Set Parameters Node](../nodes/set-parameters-node.md)
- **Complex Transformations**: Use [Code Node](../nodes/code-node.md)
- **SQL-Based Transformations**: Use [Database Call Node](../nodes/database-call-node.md)

### For Integration

- **HTTP/REST APIs**: Use [API Call Node](../nodes/api-call-node.md)
- **Database Operations**: Use [Database Call Node](../nodes/database-call-node.md)
- **Other Corezoid Processes**: Use [Call a Process Node](../nodes/call-process-node.md)

### For Flow Control

- **Conditional Branching**: Use [Condition Node](../nodes/condition-node.md)
- **Time-Based Operations**: Use [Delay Node](../nodes/delay-node.md)
- **Process Termination**: Use [End Node](../nodes/end-node.md)

### For Task Management

- **Creating New Tasks**: Use [Copy Task Node](../nodes/copy-task-node.md)
- **Updating Existing Tasks**: Use [Modify Task Node](../nodes/modify-task-node.md)
- **Asynchronous Processing**: Use [Queue Node](../nodes/queue-node.md) and
  [Get from Queue Node](../nodes/get-from-queue-node.md)

## Troubleshooting Guide

### Common Issues and Solutions

#### Process Validation Errors

| Error                      | Possible Causes                                       | Solutions                                            |
| -------------------------- | ----------------------------------------------------- | ---------------------------------------------------- |
| Invalid node ID format     | Node IDs not in 24-character hexadecimal format       | Ensure all node IDs follow the required format       |
| Missing required parameter | Required parameter not provided in node configuration | Check node documentation for required parameters     |
| Invalid connection         | Connection to non-existent node                       | Verify all node connections reference valid node IDs |
| Duplicate node ID          | Multiple nodes with the same ID                       | Ensure each node has a unique ID                     |

#### Runtime Errors

| Error                | Possible Causes                     | Solutions                                                |
| -------------------- | ----------------------------------- | -------------------------------------------------------- |
| API connection error | Network issues, invalid endpoint    | Check network connectivity, verify endpoint URL          |
| API timeout          | Slow response from external service | Increase timeout settings, implement retry logic         |
| Code execution error | Syntax errors, runtime exceptions   | Debug code in Code node, check for proper error handling |
| Parameter not found  | Referencing non-existent parameter  | Verify parameter exists before reference, add validation |

#### Performance Issues

| Issue                  | Possible Causes                                         | Solutions                                                            |
| ---------------------- | ------------------------------------------------------- | -------------------------------------------------------------------- |
| Slow process execution | Inefficient node configuration, external service delays | Optimize node configuration, implement caching                       |
| High error rate        | Inadequate validation, external service issues          | Improve validation, implement retry logic, monitor external services |
| Process bottlenecks    | Sequential operations that could be parallel            | Identify bottlenecks, implement parallel processing where possible   |

## Process Validation and Node Positioning

### Process JSON Validation

Before deploying a process, validate its JSON structure to ensure it meets all requirements:

1. **Schema Validation**

   - Use the provided validation script: `npm run validate:schema`
   - Ensure all required fields are present
   - Verify node IDs follow the correct format (24-character hexadecimal)
   - Check that all node connections reference valid node IDs

2. **Common Validation Issues**
   - Missing required fields in node definitions
   - Invalid node ID format
   - Connections to non-existent nodes
   - Duplicate node IDs
   - Missing Start node (every process must have exactly one Start node)

### Node ID Lifecycle: Server Assignment & Stability on Push

Node IDs you write locally are **temporary** for any node the server does not yet recognize. On
`push-process`, Corezoid assigns its own canonical 24-character hex ID to every **new** node and
rewrites all references to it. This is expected, not an error.

What is preserved vs. reassigned:

| Node | On push |
| ---- | ------- |
| **New** node (locally invented ID, even a valid `^[0-9a-f]{24}$` string) | ID is **reassigned** to a server-generated value; format/plausibility of your ID does not matter |
| **Existing** node (already carries a server-assigned ID) | ID is **preserved** — stable across all subsequent pushes |

Each node also carries a server-managed `uuid`, which is likewise assigned on first push and stable
thereafter.

Practical workflow:

1. For new nodes, supply any unique, format-valid placeholder ID (`^[0-9a-f]{24}$`) so the JSON
   validates and intra-push references resolve. **Within a single push**, references between your
   new nodes (`go`, `to_node_id`, `err_node_id`) are remapped to the canonical IDs automatically —
   you do **not** need to know the final IDs in advance.
2. `push-process` writes the canonical IDs back into your local process file and rewires references.
3. Run **`pull-process`** afterward to fully resync server-managed fields (canonical `id`, `uuid`,
   and any normalized values) before you make further edits.
4. In a **later** editing session, never reference an ID you invented in a previous push — it no
   longer exists. Use the canonical ID from the latest pull. (Node `title` is **not** a safe
   substitute: titles are not unique and may be empty.)

> Common failure: editing a process across sessions using stale, locally-invented IDs. The
> connection silently points to a non-existent node. Always `pull` first and work from canonical IDs.

### Automatic Node Positioning

To ensure clean, readable process diagrams with minimal edge intersections, use the provided node
positioning script:

1. **Using the Node Positioning Script**

   - Run: `npm run reposition-nodes -- path/to/process.json`
   - The script will analyze the process flow and adjust node positions
   - A new file with `.repositioned.json` suffix will be created

2. **What the Script Does**

   - Identifies the Start node and main process flow
   - Positions nodes in a grid-based layout
   - Applies proper spacing between nodes
   - Adjusts X-coordinates for Start/End nodes to account for center pivot points
   - Minimizes edge crossings by optimizing node positions

3. **When to Use the Script**

   - After creating a new process
   - After adding or removing nodes
   - Before committing a process to production
   - When refactoring an existing process

4. **Manual Adjustments After Repositioning**
   - Review the repositioned process for any special cases
   - Make minor adjustments as needed for complex flows
   - Ensure error paths are clearly separated from the main flow

## Best Practices Summary

- **Start with Validation**: Validate inputs at the beginning of the process
- **Implement Comprehensive Error Handling**: Every operation should have error handling
- **Use Descriptive Node Titles**: Clear titles make processes easier to understand
- **Follow Node Positioning Guidelines**: Consistent layout improves readability
- **Use Node Positioning Script**: Automatically adjust node positions using the provided script
- **Validate Process Structure**: Ensure process JSON is valid before deployment
- **Document Your Process**: Include purpose, inputs, outputs, and error handling
- **Test Thoroughly**: Test all paths, including error paths
- **Monitor Performance**: Track execution times and error rates
- **Maintain Version Control**: Keep track of process versions and changes

## Related Documentation

- [Process Overview](README.md) - Definition, operational mechanics, and key features of Corezoid
  processes
- [Node Positioning and Optimization](node-positioning-best-practices.md) - Detailed
  optimization techniques
- [Node Positioning Best Practices](node-positioning-best-practices.md) - Guidelines for visual
  process layout
- [Error Handling Strategies](error-handling.md) - Comprehensive error handling approaches
