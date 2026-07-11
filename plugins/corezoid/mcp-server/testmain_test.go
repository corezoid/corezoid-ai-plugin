package main

import (
	"os"
	"testing"
)

// TestMain redirects HOME (and the debug log) to a throwaway directory for the
// WHOLE package: several handlers write to ~/.corezoid (credentials, mcp.log),
// and a test that forgets fakeHome must corrupt a sandbox, not the developer's
// real login. Field-tested the hard way: a plain suite run deleted the real
// ~/.corezoid/credentials and appended megabytes to the real mcp.log.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "convctl-test-home-*")
	if err != nil {
		// Never limp on with the real HOME — a suite run against it once
		// deleted the developer's actual credentials file.
		println("testmain: cannot create sandbox HOME:", err.Error())
		os.Exit(1)
	}
	os.Setenv("HOME", tmp)
	os.Setenv("COREZOID_DEBUG_LOG", tmp+"/mcp.log")
	// Never inherit real tokens from the developer's environment.
	os.Unsetenv("ACCESS_TOKEN")
	os.Unsetenv("ACCESS_TOKEN_EXPIRES_AT")
	os.Unsetenv("REFRESH_TOKEN")
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}
