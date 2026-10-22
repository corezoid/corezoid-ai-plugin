package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDescribeStageDocs covers the git-pull-context result line that tells the
// model which _ext/docs/ files exist, so a new stage is not probed file by
// file with read-context-file (every probe failed with "file not found").
func TestDescribeStageDocs(t *testing.T) {
	const stagePath = "projects/1_P/stages/2_dev"

	t.Run("no docs directory", func(t *testing.T) {
		got := describeStageDocs(t.TempDir(), stagePath)
		if !strings.Contains(got, "none yet") || !strings.Contains(got, stagePath+"/_ext/docs/") {
			t.Errorf("describeStageDocs() = %q, want first-use message naming the docs dir", got)
		}
	})

	t.Run("lists only existing markdown files", func(t *testing.T) {
		contextDir := t.TempDir()
		docsDir := filepath.Join(contextDir, filepath.FromSlash(stagePath), "_ext", "docs")
		if err := os.MkdirAll(filepath.Join(docsDir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"decisions.md", "context.md", "notes.txt"} {
			if err := os.WriteFile(filepath.Join(docsDir, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		got := describeStageDocs(contextDir, stagePath)
		if !strings.Contains(got, "context.md, decisions.md") {
			t.Errorf("describeStageDocs() = %q, want context.md, decisions.md", got)
		}
		for _, absent := range []string{"invariants.md", "dependencies.md", "issues.md", "notes.txt", "sub", "none yet"} {
			if strings.Contains(got, absent) {
				t.Errorf("describeStageDocs() = %q, must not mention %q", got, absent)
			}
		}
	})
}
