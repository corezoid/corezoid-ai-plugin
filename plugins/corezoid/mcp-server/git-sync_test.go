package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestDeriveGitURL(t *testing.T) {
	tests := []struct {
		accountURL string
		want       string
		wantErr    bool
	}{
		// Table from Gitea-URL-Formula.md
		{
			accountURL: "https://admin.dev.corezoid.com",
			want:       "https://git-dev.dev.corezoid.com/corezoid-dev",
		},
		{
			accountURL: "https://admin-pre.corezoid.com",
			want:       "https://git-pre.pre.corezoid.com/corezoid-pre",
		},
		{
			accountURL: "https://admin.corezoid.com",
			want:       "https://git-prod.prod.corezoid.com/corezoid-prod",
		},
		{
			accountURL: "https://corezoid.leobank.az",
			want:       "https://git-prod.prod.leobank.az/corezoid-prod",
		},
		{
			accountURL: "https://corezoid-lq.leobank.az",
			want:       "https://git-lq.lq.leobank.az/corezoid-lq",
		},
		{
			accountURL: "https://corezoid.staging.liobank.vn",
			want:       "https://git-staging.staging.liobank.vn/corezoid-staging",
		},
		{
			accountURL: "https://corezoid.tezbank.uz",
			want:       "https://git-prod.prod.tezbank.uz/corezoid-prod",
		},
		{
			accountURL: "https://corezoid-ach.tezbank.uz",
			want:       "https://git-ach.ach.tezbank.uz/corezoid-ach",
		},
		// No scheme — should still work
		{
			accountURL: "admin.dev.corezoid.com",
			want:       "https://git-dev.dev.corezoid.com/corezoid-dev",
		},
		// Unknown first label — should error
		{
			accountURL: "https://account.corezoid.com",
			wantErr:    true,
		},
		// Empty input — should error
		{
			accountURL: "",
			wantErr:    true,
		},
		// Only 1 label — should error
		{
			accountURL: "https://localhost",
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.accountURL, func(t *testing.T) {
			got, err := deriveGitURL(tc.accountURL)
			if tc.wantErr {
				if err == nil {
					t.Errorf("deriveGitURL(%q) = %q, want error", tc.accountURL, got)
				}
				return
			}
			if err != nil {
				t.Errorf("deriveGitURL(%q) unexpected error: %v", tc.accountURL, err)
				return
			}
			if got != tc.want {
				t.Errorf("deriveGitURL(%q)\n got  %q\n want %q", tc.accountURL, got, tc.want)
			}
		})
	}
}

// TestMergeExtSnapshot_LocalOnlyFileIsRestored pins that a file present only
// in the pre-reconnect local snapshot (not on the remote at all) is copied
// into extDir untouched.
func TestMergeExtSnapshot_LocalOnlyFileIsRestored(t *testing.T) {
	snapshotDir := t.TempDir()
	extDir := t.TempDir()

	writeFile(t, filepath.Join(snapshotDir, "docs", "new.md"), "local-only content")

	conflicts, err := mergeExtSnapshot(snapshotDir, extDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts, got %v", conflicts)
	}
	got := readFile(t, filepath.Join(extDir, "docs", "new.md"))
	if got != "local-only content" {
		t.Errorf("expected local-only file to be restored, got %q", got)
	}
}

// TestMergeExtSnapshot_IdenticalFileIsLeftAlone pins that a file unchanged on
// both sides is a no-op — not rewritten, not reported as a conflict.
func TestMergeExtSnapshot_IdenticalFileIsLeftAlone(t *testing.T) {
	snapshotDir := t.TempDir()
	extDir := t.TempDir()

	writeFile(t, filepath.Join(snapshotDir, "docs", "same.md"), "same content")
	writeFile(t, filepath.Join(extDir, "docs", "same.md"), "same content")

	conflicts, err := mergeExtSnapshot(snapshotDir, extDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts, got %v", conflicts)
	}
	got := readFile(t, filepath.Join(extDir, "docs", "same.md"))
	if got != "same content" {
		t.Errorf("expected identical file untouched, got %q", got)
	}
}

// TestMergeExtSnapshot_DivergedFileIsConflict pins the fix for a bug where
// reconnecting to Gitea after an offline session blindly overwrote _ext/
// with the local snapshot, silently discarding any _ext/ changes made on the
// remote side (by another developer or the mirror bot) while offline. The
// remote version must survive in place, and the local version must be saved
// alongside — not silently dropped — with the conflict reported to the caller.
func TestMergeExtSnapshot_DivergedFileIsConflict(t *testing.T) {
	snapshotDir := t.TempDir()
	extDir := t.TempDir()

	writeFile(t, filepath.Join(snapshotDir, "docs", "decisions.md"), "local offline edit")
	writeFile(t, filepath.Join(extDir, "docs", "decisions.md"), "remote edit made while offline")

	conflicts, err := mergeExtSnapshot(snapshotDir, extDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0] != "docs/decisions.md" {
		t.Fatalf("expected exactly one conflict for docs/decisions.md, got %v", conflicts)
	}

	// Remote version must survive in place — this is the crux of the bug fix.
	got := readFile(t, filepath.Join(extDir, "docs", "decisions.md"))
	if got != "remote edit made while offline" {
		t.Errorf("expected remote version to be kept in place, got %q", got)
	}

	// Local version must be preserved alongside, not silently discarded.
	localBackup := readFile(t, filepath.Join(extDir, "docs", "decisions.md.local-conflict"))
	if localBackup != "local offline edit" {
		t.Errorf("expected local version saved as .local-conflict, got %q", localBackup)
	}
}

// TestMergeExtSnapshot_MixedTree exercises all three cases together across a
// small tree to guard against the walk logic mishandling one when others are
// present.
func TestMergeExtSnapshot_MixedTree(t *testing.T) {
	snapshotDir := t.TempDir()
	extDir := t.TempDir()

	writeFile(t, filepath.Join(snapshotDir, "context.md"), "local-only")
	writeFile(t, filepath.Join(snapshotDir, "invariants.md"), "unchanged")
	writeFile(t, filepath.Join(extDir, "invariants.md"), "unchanged")
	writeFile(t, filepath.Join(snapshotDir, "issues.md"), "local version")
	writeFile(t, filepath.Join(extDir, "issues.md"), "remote version")

	conflicts, err := mergeExtSnapshot(snapshotDir, extDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sort.Strings(conflicts)
	if len(conflicts) != 1 || conflicts[0] != "issues.md" {
		t.Fatalf("expected exactly one conflict (issues.md), got %v", conflicts)
	}
	if got := readFile(t, filepath.Join(extDir, "context.md")); got != "local-only" {
		t.Errorf("expected local-only file restored, got %q", got)
	}
	if got := readFile(t, filepath.Join(extDir, "invariants.md")); got != "unchanged" {
		t.Errorf("expected unchanged file untouched, got %q", got)
	}
	if got := readFile(t, filepath.Join(extDir, "issues.md")); got != "remote version" {
		t.Errorf("expected remote version kept for conflicting file, got %q", got)
	}
	if got := readFile(t, filepath.Join(extDir, "issues.md.local-conflict")); got != "local version" {
		t.Errorf("expected local version saved alongside, got %q", got)
	}
}

// TestStripEnvKeys pins the fix for a bug where runGitAuthed appended its own
// GIT_CONFIG_* entries on top of os.Environ() without removing any
// pre-existing entries of the same name. Go's exec.Cmd (and git itself)
// resolves duplicate env keys to the first match, so a pre-existing
// GIT_CONFIG_COUNT from the invoking shell/IDE would silently shadow ours and
// git would never see the injected Basic-auth header.
func TestStripEnvKeys(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=some.other.key",
		"GIT_CONFIG_VALUE_0=whatever",
		"HOME=/home/user",
	}
	got := stripEnvKeys(env, "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0")

	want := []string{"PATH=/usr/bin", "HOME=/home/user"}
	if len(got) != len(want) {
		t.Fatalf("stripEnvKeys() = %v, want %v", got, want)
	}
	for i, kv := range want {
		if got[i] != kv {
			t.Errorf("stripEnvKeys()[%d] = %q, want %q", i, got[i], kv)
		}
	}
}

// TestStripEnvKeys_DoesNotMutateInput guards against the shared-backing-array
// footgun: stripEnvKeys must return a fresh slice, never overwrite entries of
// the slice the caller passed in (which for runGitAuthed is os.Environ()'s
// backing data).
func TestStripEnvKeys_DoesNotMutateInput(t *testing.T) {
	env := []string{"A=1", "GIT_CONFIG_COUNT=1", "B=2"}
	envCopy := append([]string(nil), env...)

	_ = stripEnvKeys(env, "GIT_CONFIG_COUNT")

	for i := range env {
		if env[i] != envCopy[i] {
			t.Errorf("stripEnvKeys mutated its input at index %d: got %q, want %q", i, env[i], envCopy[i])
		}
	}
}

// TestGitPushContext_RevertsNonExtChangesBeforePull pins the fix for a bug
// where `pull --rebase --autostash` ran before the non-_ext/ safety-unstage
// step. If .git-context/ had an uncommitted non-_ext/ modification (nothing
// in normal operation should cause this, but the guard is defensive),
// --autostash would scoop it up along with the intended _ext/ change; a
// stash-pop conflict after the rebase could then leave the repo half-rebased
// before the safety-unstage ever ran. gitPushContext now reverts non-_ext/
// working-tree changes to HEAD before the pull, so autostash only ever has
// to carry the _ext/ change through the rebase.
func TestGitPushContext_RevertsNonExtChangesBeforePull(t *testing.T) {
	ctx := context.Background()

	// Bare "origin" — a local path is enough to exercise pull/push without
	// any network dependency.
	bareDir := t.TempDir()
	if out, err := runGit(ctx, "", "init", "--bare", bareDir); err != nil {
		t.Fatalf("bare init failed: %s", out)
	}

	// Working clone that will become .git-context/.
	projectDir := t.TempDir()
	contextDir := filepath.Join(projectDir, ".git-context")
	if out, err := runGit(ctx, "", "clone", bareDir, contextDir); err != nil {
		t.Fatalf("clone failed: %s", out)
	}
	runGit(ctx, contextDir, "config", "user.email", "test@example.com") //nolint:errcheck
	runGit(ctx, contextDir, "config", "user.name", "Test")              //nolint:errcheck
	runGit(ctx, contextDir, "checkout", "-b", "main")                   //nolint:errcheck

	// Initial commit: a tracked non-_ext/ file (stands in for CLAUDE.md) plus
	// an _ext/ file, pushed to origin with upstream tracking set.
	writeFile(t, filepath.Join(contextDir, "CLAUDE.md"), "original content")
	writeFile(t, filepath.Join(contextDir, "_ext", "docs", "context.md"), "initial docs")
	runGit(ctx, contextDir, "add", "-A")               //nolint:errcheck
	runGit(ctx, contextDir, "commit", "-m", "initial") //nolint:errcheck
	if out, err := runGit(ctx, contextDir, "push", "-u", "origin", "main"); err != nil {
		t.Fatalf("initial push failed: %s", out)
	}

	// Simulate an accidental uncommitted non-_ext/ modification sitting in
	// the working tree — the scenario the fix guards against.
	writeFile(t, filepath.Join(contextDir, "CLAUDE.md"), "ACCIDENTAL LOCAL EDIT")

	// A legitimate _ext/ change to push.
	writeFile(t, filepath.Join(contextDir, "_ext", "docs", "context.md"), "updated docs")

	chdirWithCleanup(t, projectDir)

	msg, err := gitPushContext(ctx, "test commit")
	if err != nil {
		t.Fatalf("gitPushContext failed: %v (%s)", err, msg)
	}

	if got := readFile(t, filepath.Join(contextDir, "CLAUDE.md")); got != "original content" {
		t.Errorf("expected non-_ext/ working-tree edit to be reverted to HEAD before pull, got %q", got)
	}

	// Reference the branch explicitly rather than HEAD — the bare repo's HEAD
	// symref follows whatever init.defaultBranch the git installation defaults
	// to (e.g. "master" on some runners), which was never pushed here; only
	// "main" was.
	pushed, err := runGit(ctx, bareDir, "show", "main:_ext/docs/context.md")
	if err != nil {
		t.Fatalf("could not read pushed content from origin: %v (%s)", err, pushed)
	}
	if strings.TrimSpace(pushed) != "updated docs" {
		t.Errorf("expected _ext/ change to be pushed to origin, got %q", pushed)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
