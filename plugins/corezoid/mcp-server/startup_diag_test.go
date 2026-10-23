package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// quietLogger routes the package logger to io.Discard for the test, so the
// stderr buffers under test only contain what the diagnostics wrote.
func quietLogger(t *testing.T) {
	t.Helper()
	prev := logger.writer
	logger.writer = io.Discard
	t.Cleanup(func() { logger.writer = prev })
}

func TestRunGuarded_PanicReportsReasonOnStderr(t *testing.T) {
	quietLogger(t)
	var stderr bytes.Buffer
	code := runGuarded(&stderr, "/tmp/mcp.log", func() error {
		panic("config exploded")
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	out := stderr.String()
	for _, want := range []string{"FATAL", "config exploded", "/tmp/mcp.log"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr missing %q:\n%s", want, out)
		}
	}
}

func TestRunGuarded_ErrorReportsReasonOnStderr(t *testing.T) {
	quietLogger(t)
	var stderr bytes.Buffer
	code := runGuarded(&stderr, "/tmp/mcp.log", func() error {
		return errors.New("bind: address already in use")
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if out := stderr.String(); !strings.Contains(out, "FATAL") || !strings.Contains(out, "address already in use") {
		t.Errorf("stderr lacks the failure reason:\n%s", out)
	}
}

func TestRunGuarded_CleanExitIsSilent(t *testing.T) {
	quietLogger(t)
	var stderr bytes.Buffer
	if code := runGuarded(&stderr, "/tmp/mcp.log", func() error { return nil }); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("clean exit wrote to stderr: %q", stderr.String())
	}
}

// Before this fix the stdio loop ignored scanner.Err(), so an oversized or
// unreadable message ended the server with exit 0 and no output at all.
func TestServeMCP_ReadErrorIsReturned(t *testing.T) {
	quietLogger(t)
	oversized := strings.Repeat("x", 10*1024*1024+1) + "\n"
	err := serveMCP(strings.NewReader(oversized), io.Discard)
	if err == nil {
		t.Fatal("serveMCP returned nil for an unreadable stdin; the server would exit silently")
	}
	if !strings.Contains(err.Error(), "stdin") {
		t.Errorf("error does not name the failing stream: %v", err)
	}
}

func TestServeMCP_EOFIsCleanShutdown(t *testing.T) {
	quietLogger(t)
	var out bytes.Buffer
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	if err := serveMCP(strings.NewReader(in), &out); err != nil {
		t.Fatalf("serveMCP on EOF = %v, want nil", err)
	}
	if !strings.Contains(out.String(), `"id":1`) {
		t.Errorf("initialize was not answered: %q", out.String())
	}
}

func TestStartupSelfCheck_MalformedConfigWarnsOnStderr(t *testing.T) {
	quietLogger(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	startupSelfCheck(&stderr, "/tmp/mcp.log")
	out := stderr.String()
	if !strings.Contains(out, "WARNING") || !strings.Contains(out, "config.json") {
		t.Errorf("malformed config not reported on stderr:\n%s", out)
	}
}

func TestStartupSelfCheck_FreshInstallIsSilent(t *testing.T) {
	quietLogger(t)
	t.Setenv("HOME", t.TempDir())
	var stderr bytes.Buffer
	startupSelfCheck(&stderr, "/tmp/mcp.log")
	if stderr.Len() != 0 {
		t.Errorf("missing config should not warn: %q", stderr.String())
	}
}
