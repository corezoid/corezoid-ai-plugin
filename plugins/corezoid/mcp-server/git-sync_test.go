package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- GitSyncConfig.IsConfigured ---

func TestGitSyncConfig_IsConfigured_AllFields(t *testing.T) {
	cfg := GitSyncConfig{
		LoginID:   "52210",
		Secret:    "secret123",
		CompanyID: "ace69ce6-ee38-4297-b0a2-39ce4ba8eb9a",
		BaseURL:   "https://git-dev.dev.corezoid.com",
	}
	if !cfg.IsConfigured() {
		t.Error("expected IsConfigured=true when all fields set")
	}
}

func TestGitSyncConfig_IsConfigured_MissingLogin(t *testing.T) {
	cfg := GitSyncConfig{
		Secret:    "secret123",
		CompanyID: "ace69ce6",
		BaseURL:   "https://git-dev.dev.corezoid.com",
	}
	if cfg.IsConfigured() {
		t.Error("expected IsConfigured=false when LoginID missing")
	}
}

func TestGitSyncConfig_IsConfigured_MissingSecret(t *testing.T) {
	cfg := GitSyncConfig{
		LoginID:   "52210",
		CompanyID: "ace69ce6",
		BaseURL:   "https://git-dev.dev.corezoid.com",
	}
	if cfg.IsConfigured() {
		t.Error("expected IsConfigured=false when Secret missing")
	}
}

func TestGitSyncConfig_IsConfigured_Empty(t *testing.T) {
	cfg := GitSyncConfig{}
	if cfg.IsConfigured() {
		t.Error("expected IsConfigured=false for empty config")
	}
}

// --- GitSyncConfig.RepoURL ---

func TestGitSyncConfig_RepoURL_UUID(t *testing.T) {
	cfg := GitSyncConfig{
		LoginID:   "52210",
		Secret:    "plainSecret",
		CompanyID: "ace69ce6-ee38-4297-b0a2-39ce4ba8eb9a",
		BaseURL:   "https://git-dev.dev.corezoid.com",
	}
	got := cfg.RepoURL()
	want := "https://52210:plainSecret@git-dev.dev.corezoid.com/corezoid-dev/c-ace69ce6-ee38-4297-b0a2-39ce4ba8eb9a.git"
	if got != want {
		t.Errorf("RepoURL UUID:\n got  %s\n want %s", got, want)
	}
}

func TestGitSyncConfig_RepoURL_OldFormat(t *testing.T) {
	cfg := GitSyncConfig{
		LoginID:   "52210",
		Secret:    "sec",
		CompanyID: "i188280",
		BaseURL:   "https://git-dev.dev.corezoid.com",
	}
	got := cfg.RepoURL()
	if !strings.Contains(got, "c-i188280") {
		t.Errorf("expected c-i188280 in URL, got %s", got)
	}
}

func TestGitSyncConfig_RepoURL_SpecialCharsInSecret(t *testing.T) {
	cfg := GitSyncConfig{
		LoginID:   "52210",
		Secret:    "p@ss:w/ord#1",
		CompanyID: "company-id",
		BaseURL:   "https://git-dev.dev.corezoid.com",
	}
	got := cfg.RepoURL()
	// Special chars must be percent-encoded so they don't break URL parsing.
	if strings.Contains(got, "p@ss") {
		t.Errorf("raw @ in secret not encoded in URL: %s", got)
	}
	// url.UserPassword encodes @ as %40 in the password field.
	if !strings.Contains(got, "%40") {
		t.Errorf("expected %%40 for @ in secret, got %s", got)
	}
}

func TestGitSyncConfig_RepoURL_InvalidBaseURL(t *testing.T) {
	cfg := GitSyncConfig{
		LoginID:   "52210",
		Secret:    "sec",
		CompanyID: "company",
		BaseURL:   "not-a-url",
	}
	got := cfg.RepoURL()
	if got != "" {
		t.Errorf("expected empty RepoURL for invalid BaseURL, got %s", got)
	}
}

// --- buildSparsePaths ---

func TestBuildSparsePaths_NoStagePath(t *testing.T) {
	cfg := GitSyncConfig{}
	paths := buildSparsePaths(cfg)
	if len(paths) != 1 || paths[0] != "_ext" {
		t.Errorf("expected [_ext], got %v", paths)
	}
}

func TestBuildSparsePaths_WithStagePath(t *testing.T) {
	cfg := GitSyncConfig{
		StagePath: "projects/188280_Smart_Form/stages/188281_develop",
	}
	paths := buildSparsePaths(cfg)

	want := map[string]bool{
		"_ext":                                                              true,
		"projects/188280_Smart_Form/_ext":                                  true,
		"projects/188280_Smart_Form/stages/188281_develop/_ext":            true,
	}
	if len(paths) != len(want) {
		t.Errorf("expected %d paths, got %d: %v", len(want), len(paths), paths)
	}
	for _, p := range paths {
		if !want[p] {
			t.Errorf("unexpected path %q in sparse paths", p)
		}
	}
}

// --- GitPull with unconfigured config ---

func TestGitPull_NotConfigured_ReturnsNil(t *testing.T) {
	cfg := GitSyncConfig{} // empty — not configured
	if err := GitPull(cfg); err != nil {
		t.Errorf("GitPull on unconfigured config should return nil, got: %v", err)
	}
}

// --- GitPush with unconfigured config ---

func TestGitPush_NotConfigured_ReturnsNil(t *testing.T) {
	cfg := GitSyncConfig{}
	if err := GitPush(cfg, ""); err != nil {
		t.Errorf("GitPush on unconfigured config should return nil, got: %v", err)
	}
}

// --- ReadContextFile / UpdateContextFile ---

func TestReadContextFile_NotFound(t *testing.T) {
	cfg := GitSyncConfig{WorkDir: t.TempDir()}
	content, found := ReadContextFile(cfg, "_ext/docs/context.md")
	if found {
		t.Error("expected found=false for missing file")
	}
	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
}

func TestUpdateContextFile_Replace(t *testing.T) {
	dir := t.TempDir()
	cfg := GitSyncConfig{WorkDir: dir}

	if err := UpdateContextFile(cfg, "_ext/docs/context.md", "hello world", "replace"); err != nil {
		t.Fatalf("UpdateContextFile replace: %v", err)
	}

	content, found := ReadContextFile(cfg, "_ext/docs/context.md")
	if !found {
		t.Fatal("expected file to exist after write")
	}
	if content != "hello world" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestUpdateContextFile_Append(t *testing.T) {
	dir := t.TempDir()
	cfg := GitSyncConfig{WorkDir: dir}

	if err := UpdateContextFile(cfg, "_ext/docs/issues.md", "line1\n", "replace"); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	if err := UpdateContextFile(cfg, "_ext/docs/issues.md", "line2\n", "append"); err != nil {
		t.Fatalf("append: %v", err)
	}

	content, _ := ReadContextFile(cfg, "_ext/docs/issues.md")
	if !strings.Contains(content, "line1") || !strings.Contains(content, "line2") {
		t.Errorf("expected both lines, got %q", content)
	}
}

func TestUpdateContextFile_CreatesSubdirs(t *testing.T) {
	dir := t.TempDir()
	cfg := GitSyncConfig{WorkDir: dir}

	if err := UpdateContextFile(cfg, "_ext/docs/decisions.md", "decision1", "replace"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(dir, ".git-context", "_ext", "docs", "decisions.md")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Errorf("expected file at %s, got error: %v", expectedPath, err)
	}
}

func TestUpdateContextFile_WithStagePath(t *testing.T) {
	dir := t.TempDir()
	cfg := GitSyncConfig{
		WorkDir:   dir,
		StagePath: "projects/188280_Test/stages/188281_develop",
	}

	if err := UpdateContextFile(cfg, "_ext/docs/context.md", "stage context", "replace"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(dir, ".git-context",
		"projects/188280_Test/stages/188281_develop",
		"_ext/docs/context.md")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Errorf("expected file at %s: %v", expectedPath, err)
	}
}

// --- gitContextDir ---

func TestGitContextDir_UsesWorkDir(t *testing.T) {
	cfg := GitSyncConfig{WorkDir: "/tmp/myproject"}
	got := cfg.gitContextDir()
	want := "/tmp/myproject/.git-context"
	if got != want {
		t.Errorf("gitContextDir: got %s, want %s", got, want)
	}
}

func TestGitContextDir_FallsBackToCwd(t *testing.T) {
	cfg := GitSyncConfig{} // no WorkDir
	got := cfg.gitContextDir()
	if !strings.HasSuffix(got, ".git-context") {
		t.Errorf("expected path ending in .git-context, got %s", got)
	}
}
