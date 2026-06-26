package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ---- classifyError ---------------------------------------------------------

func TestClassifyError(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"authentication failed", "auth_error"},
		{"invalid token supplied", "auth_error"},
		{"401 Unauthorized", "auth_error"},
		{"403 Forbidden", "auth_error"},
		{"validation failed: bad shape", "validation_error"},
		{"node is invalid", "validation_error"},
		{"lint reported issues", "validation_error"},
		{"resource not found", "not_found"},
		{"got 404 from server", "not_found"},
		{"api returned error", "api_error"},
		{"http request failed", "api_error"},
		{"fetch error", "api_error"},
		{"something else entirely", "unknown"},
		{"", "unknown"},
	}
	for _, c := range cases {
		got := classifyError(c.in)
		if got != c.want {
			t.Errorf("classifyError(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClassifyError_CaseInsensitive(t *testing.T) {
	if got := classifyError("AUTHENTICATION FAILED"); got != "auth_error" {
		t.Errorf("expected auth_error for upper-case input, got %q", got)
	}
}

// ---- hostnameOnly ----------------------------------------------------------

func TestHostnameOnly(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://api.corezoid.com/v2", "api.corezoid.com"},
		{"http://example.com:8080/path?x=1", "example.com"},
		{"api.corezoid.com", "api.corezoid.com"},
		{"api.corezoid.com/path", "api.corezoid.com"},
		{"", ""},
	}
	for _, c := range cases {
		got := hostnameOnly(c.in)
		if got != c.want {
			t.Errorf("hostnameOnly(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHostnameOnly_InvalidURL(t *testing.T) {
	// Pass a URL that fails url.Parse — strings with control characters.
	got := hostnameOnly("https://exa mple.com")
	if got != "" {
		t.Errorf("expected empty string for malformed URL, got %q", got)
	}
}

// ---- generateUUIDv4 --------------------------------------------------------

var reUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestGenerateUUIDv4_Format(t *testing.T) {
	id := generateUUIDv4()
	if len(id) != 36 {
		t.Fatalf("expected 36-char UUID, got %d: %q", len(id), id)
	}
	if !reUUIDv4.MatchString(id) {
		t.Errorf("UUID %q does not match v4 pattern", id)
	}
}

func TestGenerateUUIDv4_Unique(t *testing.T) {
	ids := make(map[string]struct{}, 50)
	for i := 0; i < 50; i++ {
		id := generateUUIDv4()
		if _, dup := ids[id]; dup {
			t.Fatalf("duplicate UUID generated: %s", id)
		}
		ids[id] = struct{}{}
	}
}

// ---- loadOrCreateInstallationID --------------------------------------------

// Replace HOME with a temp dir so the test does not touch the real
// ~/.corezoid/installation_id file.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	t.Cleanup(func() { _ = os.Setenv("HOME", orig) })
	return dir
}

func TestLoadOrCreateInstallationID_CreatesFile(t *testing.T) {
	home := withTempHome(t)

	id := loadOrCreateInstallationID()
	if !reUUIDv4.MatchString(id) {
		t.Fatalf("generated ID %q is not a valid UUID v4", id)
	}

	// File should have been persisted.
	path := filepath.Join(home, ".corezoid", "installation_id")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected installation_id file to be created: %v", err)
	}
	if strings.TrimSpace(string(data)) != id {
		t.Errorf("file content %q does not match returned id %q", strings.TrimSpace(string(data)), id)
	}
}

func TestLoadOrCreateInstallationID_ReadsExisting(t *testing.T) {
	home := withTempHome(t)

	dir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	existing := "11111111-2222-4333-8444-555555555555"
	if err := os.WriteFile(filepath.Join(dir, "installation_id"), []byte(existing+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	id := loadOrCreateInstallationID()
	if id != existing {
		t.Errorf("expected to read existing id %q, got %q", existing, id)
	}
}

func TestLoadOrCreateInstallationID_RegeneratesOnInvalidLength(t *testing.T) {
	home := withTempHome(t)

	dir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Length != 36 — should be discarded and a new ID generated.
	if err := os.WriteFile(filepath.Join(dir, "installation_id"), []byte("too-short\n"), 0600); err != nil {
		t.Fatal(err)
	}

	id := loadOrCreateInstallationID()
	if id == "too-short" {
		t.Error("expected invalid id to be discarded")
	}
	if !reUUIDv4.MatchString(id) {
		t.Errorf("expected fresh UUID v4, got %q", id)
	}
}

// ---- emitAnalyticsEvent ----------------------------------------------------

func TestEmitAnalyticsEvent_DisabledIsNoOp(t *testing.T) {
	// When analytics are disabled, emitAnalyticsEvent must not panic even if
	// analyticsCh is nil.
	prev := analyticsEnabled.Load()
	analyticsEnabled.Store(false)
	t.Cleanup(func() { analyticsEnabled.Store(prev) })

	emitAnalyticsEvent(AnalyticsEvent{Tool: "test"})
}

func TestEmitAnalyticsEvent_FullChannelDoesNotBlock(t *testing.T) {
	prevEnabled := analyticsEnabled.Load()
	prevCh := analyticsCh
	t.Cleanup(func() {
		analyticsEnabled.Store(prevEnabled)
		analyticsCh = prevCh
	})

	analyticsCh = make(chan AnalyticsEvent, 1)
	analyticsEnabled.Store(true)
	analyticsCh <- AnalyticsEvent{Tool: "filler"} // fill the buffer

	// Second emit should drop silently rather than block. If the function
	// blocks, the goroutine never closes `done` and the timeout below fires.
	done := make(chan struct{})
	go func() {
		emitAnalyticsEvent(AnalyticsEvent{Tool: "dropped"})
		close(done)
	}()
	select {
	case <-done:
		// pass
	case <-time.After(time.Second):
		t.Fatal("emitAnalyticsEvent blocked on a full channel")
	}
}
