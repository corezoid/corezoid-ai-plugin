# Corezoid AI Assistant Documentation

This documentation provides detailed information about Corezoid processes, nodes, tasks, and
examples.

## Table of Contents

### Processes

- [Process Overview](process/README.md) - Definition, operational mechanics, and key features of
  Corezoid processes
- [Process Lifecycle and Object Moves](process/process-lifecycle-and-move.md) - Pause/resume
  semantics and safe process/folder reparenting

### Nodes

- [Nodes Overview](nodes/README.md) - General information about nodes in Corezoid
- [Start Node](nodes/start-node.md) - Receives tasks into a Process
- [End Node](nodes/end-node.md) - Terminates the processing flow and collects tasks
- [Condition Node](nodes/condition-node.md) - Evaluates parameter-based conditions to branch tasks
- [Delay Node](nodes/delay-node.md) - Holds tasks for a specified time period
- [Set Parameters Node](nodes/set-parameters-node.md) - Creates or updates key-value pairs in the
  task
  - [Set Parameters Built-in Functions](nodes/set-parameters-built-in-functions.md) - Documentation
    of built-in functions like $.math(), $.random(), $.date()
- [Code Node](nodes/code-node.md) - Runs custom user code to transform or compute data
  - [Code Node Libraries and Usage](nodes/code-node-libraries.md) - Documentation of available
    libraries and usage patterns
- [Git Call Node](nodes/git-call-node.md) - Pulls and executes code from a Git repository
- [API Call Node](nodes/api-call-node.md) - Sends HTTP requests to external endpoints
- [Database Call Node](nodes/database-call-node.md) - Executes SQL statements on an external
  database
- [Queue Node](nodes/queue-node.md) - Creates a queue of tasks for asynchronous handling
- [Get from Queue Node](nodes/get-from-queue-node.md) - Retrieves tasks from a Queue node
- [Copy Task Node](nodes/copy-task-node.md) - Duplicates tasks to another Process
- [Call a Process Node](nodes/call-process-node.md) - Invokes another Process with the current task
- [Reply to Process Node](nodes/reply-to-process-node.md) - Sends a response to a Call a Process
  node
- [Set State Node](nodes/set-state-node.md) - Updates or assigns a state for the current task
- [Waiting for Callback Node](nodes/waiting-for-callback-node.md) - Halts the task flow until an
  external HTTP request
- [Modify Task Node](nodes/modify-task-node.md) - Updates an existing task in another Process
- [Sum Node](nodes/sum-node.md) - Aggregates numeric values from incoming tasks

### Tasks

- [Tasks Overview](tasks/README.md) - Definition, structure, and examples of tasks in Corezoid

### Examples

- [Task Examples](tasks/task-examples.md) - JSON examples of tasks with different parameters
