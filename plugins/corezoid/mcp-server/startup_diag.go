package main

import (
	"fmt"
	"io"
	rdebug "runtime/debug" // the package already has a `debug` flag
)

// In MCP server mode the log goes to a file, and the client usually shows
// only "connection closed" when the process dies. Everything here writes the
// reason to stderr — the one channel MCP clients surface — so a failed start
// is diagnosable without digging for mcp.log.

// startupSelfCheck reports problems that leave the server running but
// degraded, so the user sees them before a tool call fails for an unrelated
// looking reason. It never exits.
func startupSelfCheck(stderr io.Writer, logPath string) {
	if _, err := LoadConfig(); err != nil {
		fmt.Fprintf(stderr, "[corezoid-mcp] WARNING: cannot load saved credentials: %v\n", err)
		fmt.Fprintln(stderr, "[corezoid-mcp] Fix or remove the file, then run the 'login' MCP tool.")
		logger.Warn("startup: cannot load config: %v", err)
	}
	logger.Debug("startup: self-check done, log file %s", logPath)
}

// runGuarded runs the server body and turns a panic or a returned error into
// a one-paragraph stderr diagnostic plus a non-zero exit code. A clean return
// yields 0.
func runGuarded(stderr io.Writer, logPath string, fn func() error) (code int) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("fatal: panic: %v\n%s", r, rdebug.Stack())
			fmt.Fprintf(stderr, "[corezoid-mcp] FATAL: server crashed: %v\n", r)
			fmt.Fprintf(stderr, "[corezoid-mcp] Version: %s\n", buildIdentity())
			fmt.Fprintf(stderr, "[corezoid-mcp] Stack trace written to %s\n", logPath)
			code = 1
		}
	}()
	if err := fn(); err != nil {
		logger.Error("fatal: %v", err)
		fmt.Fprintf(stderr, "[corezoid-mcp] FATAL: server stopped: %v\n", err)
		fmt.Fprintf(stderr, "[corezoid-mcp] Version: %s; log: %s\n", buildIdentity(), logPath)
		return 1
	}
	return 0
}
