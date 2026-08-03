package main

// Annotation presets for the tool registry. Every tool in tools_*.go picks
// exactly one of these — hand-rolled per-tool literals drift, and the five
// shapes below cover the whole surface: read vs write, local vs remote.
//
// TestEveryToolHasAnnotations enforces that each registered tool uses one of
// these presets, so a new tool cannot ship with the zero value (which would
// wrongly advertise a non-idempotent, closed-world write).
var (
	// annReadOnlyRemote — reads Corezoid state through the API and changes
	// nothing anywhere.
	annReadOnlyRemote = mcpToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}

	// annReadOnlyLocal — operates purely on local files and changes nothing
	// (lint, reading a mirrored context file).
	annReadOnlyLocal = mcpToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   false,
	}

	// annCreateRemote — additively creates new state in an external system
	// (a process, a group, a task, a feedback report). Not idempotent:
	// calling twice creates two objects.
	annCreateRemote = mcpToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  false,
		OpenWorldHint:   true,
	}

	// annDestructiveRemote — overwrites or removes existing state that
	// reaches an external system: deletes, in-place modifications, deploys,
	// and pulls that overwrite a local file from the server.
	annDestructiveRemote = mcpToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: true,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}

	// annDestructiveLocal — overwrites local state only (rewriting a
	// process's coordinates, clearing stored credentials).
	annDestructiveLocal = mcpToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: true,
		IdempotentHint:  true,
		OpenWorldHint:   false,
	}
)
