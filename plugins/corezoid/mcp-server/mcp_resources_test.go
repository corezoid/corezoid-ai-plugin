package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupProcessesDir(t *testing.T) string {
	t.Helper()
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	return dir
}

// ---- listResources ---------------------------------------------------------

func TestListResources_Empty(t *testing.T) {
	setupProcessesDir(t)
	// No .processes dir — should return empty slice without error.
	resources, err := listResources()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(resources))
	}
}

func TestListResources_WithFiles(t *testing.T) {
	dir := setupProcessesDir(t)
	procDir := filepath.Join(dir, ".processes")
	os.MkdirAll(procDir, 0755) //nolint:errcheck
	os.WriteFile(filepath.Join(procDir, "123_hello.conv.json"), []byte(`{}`), 0644) //nolint:errcheck
	os.WriteFile(filepath.Join(procDir, "readme.txt"), []byte("skip me"), 0644)     //nolint:errcheck

	resources, err := listResources()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].Name != "123_hello.conv.json" {
		t.Errorf("unexpected resource name: %s", resources[0].Name)
	}
	if !strings.HasPrefix(resources[0].URI, resourceURIPrefix) {
		t.Errorf("URI %q missing expected prefix", resources[0].URI)
	}
	if resources[0].MimeType != "application/json" {
		t.Errorf("unexpected mime type: %s", resources[0].MimeType)
	}
}

func TestListResources_Nested(t *testing.T) {
	dir := setupProcessesDir(t)
	sub := filepath.Join(dir, ".processes", "subfolder")
	os.MkdirAll(sub, 0755) //nolint:errcheck
	os.WriteFile(filepath.Join(sub, "456_deep.conv.json"), []byte(`{}`), 0644) //nolint:errcheck

	resources, err := listResources()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
}

// ---- readResource ----------------------------------------------------------

func TestReadResource_OK(t *testing.T) {
	dir := setupProcessesDir(t)
	procDir := filepath.Join(dir, ".processes")
	os.MkdirAll(procDir, 0755)                                                                   //nolint:errcheck
	os.WriteFile(filepath.Join(procDir, "123_test.conv.json"), []byte(`{"ok":true}`), 0644) //nolint:errcheck

	uri := resourceURIPrefix + "123_test.conv.json"
	content, err := readResource(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content.URI != uri {
		t.Errorf("URI mismatch: %q vs %q", content.URI, uri)
	}
	if content.Text != `{"ok":true}` {
		t.Errorf("unexpected text: %q", content.Text)
	}
	if content.MimeType != "application/json" {
		t.Errorf("unexpected mime type: %s", content.MimeType)
	}
}

func TestReadResource_NotFound(t *testing.T) {
	setupProcessesDir(t)
	os.MkdirAll(".processes", 0755) //nolint:errcheck

	_, err := readResource(resourceURIPrefix + "missing.conv.json")
	if err == nil {
		t.Error("expected error for missing resource, got nil")
	}
}

func TestReadResource_UnsupportedScheme(t *testing.T) {
	setupProcessesDir(t)
	_, err := readResource("http://example.com/something")
	if err == nil {
		t.Error("expected error for unsupported URI scheme, got nil")
	}
}

func TestReadResource_PathTraversal(t *testing.T) {
	setupProcessesDir(t)
	_, err := readResource(resourceURIPrefix + "../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal URI, got nil")
	}
}
