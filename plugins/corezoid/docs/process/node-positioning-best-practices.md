# Node Positioning Best Practices

## Overview

This document outlines best practices for positioning and arranging nodes in Corezoid processes.
Following these guidelines ensures that processes are visually clear, easy to understand, and
maintainable.

## Node Dimensions

Corezoid nodes have specific dimensions that should be considered when positioning them:

1. **Start and End Nodes**

   - Shape: Circle
   - Dimensions: 56px × 56px
   - Radius: 28px
   - Pivot Point: Center of the node

2. **Standard Nodes (without escalation or error links)**

   - Width: 200px
   - Minimum Height: 150px
   - Actual height varies based on node content
   - Pivot Point: Top-left corner

3. **Nodes with a Timer (Semaphor / Delay)**

   - Width: 200px
   - Height: approximately **2× the standard height** of the same node type
   - A timer semaphor adds a visible timer block below the node body, roughly doubling the rendered height
   - Account for this when calculating vertical spacing to the next node — increase the Y gap accordingly
   - Pivot Point: Top-left corner

4. **Nodes with Escalation or Error Links**

   - Width: 200px
   - Minimum Height: 125px
   - Pivot Point: Top-left corner

5. **Condition Nodes**
   - With single rule:
     - Width: 200px
     - Minimum Height: 110px
   - With AND operator:
     - Width: 200px
     - Minimum Height: 140px
   - With 2 OR rules:
     - Width: 200px
     - Minimum Height: 160px
   - Pivot Point: Top-left corner

## Pivot Points and Their Impact on Positioning

Understanding the pivot point location for different node types is crucial for proper node
alignment:

1. **Pivot Point Definition**

   - The pivot point is the reference point used for node positioning
   - The X,Y coordinates of a node refer to its pivot point position
   - Different node types have different pivot point locations

2. **Start/End Nodes (Circular Nodes)**

   - Pivot point is at the center of the circle
   - When positioning Start/End nodes in line with other nodes, this center-based pivot must be
     considered

3. **All Other Node Types**

   - Pivot point is at the top-left corner
   - When aligning these nodes with Start/End nodes, proper offsets must be applied

4. **Alignment Adjustment for Start/End Nodes**
   - To align a Start/End node with other nodes vertically, add 150px to the X-coordinate of the
     Start/End node
   - Example: If regular nodes are at X=500, place the Start/End node at X=600 to achieve visual
     alignment

## Layout Guidelines

### Standard Patterns

1. **Pattern Consistency**
   - Nodes that implement standard patterns (like API Call with error handling) should maintain
     standard relative positions
   - Preserve the established spatial relationships between related nodes
   - This ensures visual consistency across different processes
   - Example: Error nodes should always be positioned to the right of their corresponding main nodes

### Vertical Flow for Happy Path

1. **Top-to-Bottom Flow**

   - The main process flow (happy path) should flow vertically from top to bottom
   - Start node should be at the top of the process
   - End node(s) should be at the bottom of the process
   - Maintain consistent vertical spacing between nodes (recommended: 150px)

2. **Node Alignment**
   - Align nodes in the happy path along a central vertical axis
   - This creates a clear visual indication of the primary process flow

### Horizontal Flow for Exceptions

1. **Error Paths**

   - Position error handling nodes to the right of the main flow
   - Connect error nodes with horizontal lines from the main flow
   - Maintain consistent horizontal spacing (recommended: 200px from main flow)
   - **Dedicated error cluster per error-prone node:** each failing node gets its own collapsed
     **Reply to Process** node (`x + ~250`, same `y`) leading to its own descriptively-named **Error**
     node (`x + ~500`, same `y`). Keep the cluster pinned tight to the node it protects — at the same
     `y` with a small horizontal offset — so it reads as attached, not drifting off with a large gap.
     See [Dedicated Error Cluster Pattern](error-handling.md#dedicated-error-cluster-pattern-standard).

2. **Escalation Paths**

   - Position escalation handling nodes to the right of the main flow
   - For multiple escalation types, arrange them vertically on the right side

3. **Branching Paths**
   - For condition-based branching, position alternative paths to the right or left
   - When using multiple branches, consider positioning:
     - Primary/most common path: continue vertically
     - Secondary paths: branch to the right
     - Tertiary paths: branch to the left (if needed)

## Spacing and Overlap Prevention

1. **Vertical Spacing**

   - Minimum vertical spacing between connected nodes: 200px
   - For complex processes with many nodes, increase spacing to 300px
   - For sequential nodes in the main flow, use consistent spacing (recommended: 250px)
   - When nodes have multiple connections, increase vertical spacing to 350-400px
   - For Reply-Final node pairs, use at least 200px spacing between them

2. **Horizontal Spacing**

   - Minimum horizontal spacing between parallel flows: 300px
   - For processes with multiple columns, use at least 300px between columns
   - Error nodes should be positioned at least 250px to the right/left of the main flow
   - When nodes have multiple connections to different columns, increase horizontal spacing to 400px

3. **Preventing Overlap**
   - Nodes should never overlap each other
   - Connection lines should not cross through nodes
   - When connection lines must cross, ensure they do so at clear angles
   - Position nodes to minimize the number of edge crossings
   - For complex processes, increase vertical and horizontal spacing to reduce edge intersections

## Coordinates System

Corezoid uses an X,Y coordinate system for node positioning:

1. **X-Coordinate**

   - Determines horizontal position (left to right)
   - Increases as you move right on the canvas
   - Main flow typically uses consistent X values (e.g., X=500)
   - Remember that Start/End nodes need an X-offset of +100px for visual alignment with other nodes

2. **Y-Coordinate**
   - Determines vertical position (top to bottom)
   - Increases as you move down on the canvas
   - Sequential nodes typically increment Y by 200-250px
   - The Y-coordinate is not affected by node type (all pivot points are at the same vertical
     position)

## Example Coordinates

For a simple linear process with error handling (note the X-offset for Start/End nodes):

```
Start Node:         X=600, Y=100    # X=600 (not 500) to align with the nodes below
Validation Node:    X=500, Y=300
Error Node:         X=800, Y=300
Processing Node:    X=500, Y=500
Reply Node:         X=500, Y=700
End Node:           X=600, Y=850    # X=600 (not 500) to align with the nodes above
```

For a process with condition branching:

```
Start Node:         X=600, Y=100    # X=600 (not 500) to align with the nodes below
Condition Node:     X=500, Y=300
True Path Node:     X=500, Y=500
False Path Node:    X=800, Y=500
Join Node:          X=500, Y=700
End Node:           X=600, Y=850    # X=600 (not 500) to align with the nodes above
```

## Edge Connections and Routing

Corezoid uses Bezier curves for edge connections between nodes. Understanding how these connections
are rendered helps create clearer process diagrams:

1. **Edge Connection Types**

   - Edges are rendered as smooth Bezier curves
   - Connection points are determined by the port positions (top, bottom, left, right)
   - Control points for curves are calculated based on the distance between nodes

2. **Minimizing Edge Crossings**

   - Position nodes to minimize the number of edge crossings
   - Prefer vertical connections for the main flow (top-to-bottom)
   - Use horizontal connections for branches and error paths
   - Increase spacing between parallel flows to allow smoother curves

3. **Edge Routing Best Practices**

   - For sequential nodes, maintain consistent vertical alignment to create straight edges
   - For branching paths, position branch nodes at the same vertical level
   - When edges must cross, ensure they do so at clear angles (ideally 90 degrees)
   - For complex processes with many connections, increase both vertical and horizontal spacing

4. **Port Selection**
   - Connections from the bottom port of one node to the top port of another create the cleanest
     vertical flows
   - For error paths, use right/left ports to create horizontal connections
   - Avoid connecting opposite ports (e.g., left to right) when nodes are close to each other

## Complex Process Layout

For complex processes with multiple branches and error paths:

1. **Use Grid-Based Positioning**

   - Plan node positions on a grid with consistent spacing
   - Main flow: central column
   - Primary branches: adjacent columns
   - Error handling: rightmost columns

2. **Group Related Nodes**

   - Position related nodes in proximity to each other
   - Use consistent spacing within groups

3. **Visual Separation**
   - Use increased spacing to separate distinct process sections
   - Consider adding Comment nodes to label different sections

## Symmetry Principles

Applying symmetry to process layouts creates visually balanced and aesthetically pleasing diagrams:

1. **Balanced Error Handling**

   - Position error nodes symmetrically on both sides of the main flow when possible
   - For multiple error types, maintain consistent vertical alignment within each side
   - Example: Validation errors on left, runtime errors on right

2. **Vertical Alignment**

   - Align nodes with similar functions at the same vertical level
   - For parallel operations, position nodes at the same Y-coordinate
   - Example: All error nodes for a specific validation step should share the same Y-coordinate

3. **Horizontal Mirroring**

   - When branching occurs, mirror the structure on both sides of the main flow
   - Use equal distances from the center for nodes with equivalent importance
   - Example: If condition branches to left and right, use equal horizontal spacing

4. **Consistent Spacing Ratios**

   - Maintain consistent ratios between horizontal and vertical spacing
   - Recommended ratio: 1:1 or 1.5:1 (horizontal:vertical)
   - This creates a visually balanced grid pattern

5. **Center Alignment for Start/End Nodes**
   - Always center the Start node directly above the first process node
   - Center End nodes below their preceding nodes
   - This creates a clear visual entry and exit point for the process


```

## Related Documentation

- [Converting Algorithms to Effective Processes](algorithm-to-process-guide.md)
- [Execution Algorithm](execution-algorithm.md) - How processes are executed
