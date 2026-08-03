package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRootForTest resolves the repository root from this source file:
// mcp-server/ → plugins/corezoid/ → plugins/ → root.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine source file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// TestFallbackServerVersionMatchesManifest is the test half of the anti-drift
// guard described on fallbackServerVersion. If a release bumps the manifests
// and forgets the constant, this fails instead of the server quietly
// announcing a version from several releases ago.
func TestFallbackServerVersionMatchesManifest(t *testing.T) {
	root := repoRootForTest(t)

	manifests := []struct {
		path string
		get  func(map[string]interface{}) interface{}
	}{
		{filepath.Join(root, "plugins", "corezoid", ".claude-plugin", "plugin.json"),
			func(d map[string]interface{}) interface{} { return d["version"] }},
		{filepath.Join(root, "plugins", "corezoid", ".codex-plugin", "plugin.json"),
			func(d map[string]interface{}) interface{} { return d["version"] }},
		{filepath.Join(root, ".claude-plugin", "marketplace.json"), firstPluginVersion},
		{filepath.Join(root, ".agents", "plugins", "marketplace.json"), firstPluginVersion},
	}

	for _, m := range manifests {
		raw, err := os.ReadFile(m.path)
		if err != nil {
			t.Fatalf("could not read %s: %v", m.path, err)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("could not parse %s: %v", m.path, err)
		}
		got, _ := m.get(doc).(string)
		if got != fallbackServerVersion {
			t.Errorf("%s declares version %q but fallbackServerVersion is %q — bump mcp_version.go together with the manifests",
				filepath.Base(m.path), got, fallbackServerVersion)
		}
	}
}

func firstPluginVersion(d map[string]interface{}) interface{} {
	plugins, ok := d["plugins"].([]interface{})
	if !ok || len(plugins) == 0 {
		return nil
	}
	first, ok := plugins[0].(map[string]interface{})
	if !ok {
		return nil
	}
	return first["version"]
}

// TestServerVersionResolution covers the ldflags/fallback precedence.
func TestServerVersionResolution(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })

	cases := []struct {
		name     string
		injected string
		want     string
	}{
		{"unset build falls back to the manifest version", "dev", fallbackServerVersion},
		{"empty build falls back to the manifest version", "", fallbackServerVersion},
		{"whitespace-only falls back", "   ", fallbackServerVersion},
		{"ldflags value wins", "3.0.1", "3.0.1"},
		{"tag-shaped ldflags value loses its v", "v3.0.1", "3.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			Version = tc.injected
			if got := serverVersion(); got != tc.want {
				t.Errorf("serverVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInitializeResultReportsResolvedVersion pins the end-to-end wiring: the
// initialize payload both transports return must carry serverVersion(), not a
// separately maintained literal.
func TestInitializeResultReportsResolvedVersion(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })
	Version = "v9.8.7"

	result := buildInitializeResult()
	info, ok := result["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("initialize result has no serverInfo map: %#v", result)
	}
	if info["version"] != "9.8.7" {
		t.Errorf("serverInfo.version = %v, want 9.8.7", info["version"])
	}
	if info["name"] != "convctl-mcp" {
		t.Errorf("serverInfo.name = %v, want convctl-mcp", info["name"])
	}
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Errorf("protocolVersion = %v, want %v", result["protocolVersion"], mcpProtocolVersion)
	}
}
