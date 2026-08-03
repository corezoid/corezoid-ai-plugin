package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowedVersionDecls lists the package-level version-ish identifiers that may
// legitimately be bound to a string literal in this package.
//
// Everything else is drift waiting to happen: a hand-maintained server version
// has to be bumped in lock-step with six manifest files, and history shows it
// isn't — the constant this guard replaced sat five minor releases behind the
// shipped plugin version. The server version must come from main.Version,
// which the release workflow injects with -ldflags; read it via serverVersion().
var allowedVersionDecls = map[string]string{
	// Injected at build time via -ldflags. Must stay a var — ldflags cannot
	// write into a const.
	"Version": "build-time injected by the release workflow",
	// The MCP wire-format version we negotiate. Legitimately hardcoded: it
	// tracks the MCP specification, not the plugin release cadence.
	"mcpProtocolVersion": "MCP wire protocol version, independent of the plugin version",
}

// TestNoHardcodedVersionDeclarations fails when a new package-level constant or
// variable whose name mentions "version" is bound to a string literal. See
// allowedVersionDecls for the rationale and the escape hatch.
func TestNoHardcodedVersionDeclarations(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++

		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range vs.Names {
					if !strings.Contains(strings.ToLower(ident.Name), "version") {
						continue
					}
					if i >= len(vs.Values) || !isStringLiteral(vs.Values[i]) {
						continue // declaration without a literal value — not a hardcoded version
					}
					if _, ok := allowedVersionDecls[ident.Name]; ok {
						continue
					}
					t.Errorf("%s: %s %s is a hardcoded version string. "+
						"The server version must derive from main.Version via serverVersion(); "+
						"if this declaration is genuinely unrelated to the plugin release "+
						"version, add it to allowedVersionDecls with a reason.",
						fset.Position(ident.Pos()), strings.ToLower(gen.Tok.String()), ident.Name)
				}
			}
		}
	}

	if scanned == 0 {
		t.Fatal("scanned no Go source files — the guard would pass vacuously")
	}
}

// isStringLiteral reports whether e is a plain string literal, possibly
// parenthesized.
func isStringLiteral(e ast.Expr) bool {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// TestServerInfoVersionMatchesManifest is the release-drift gate: it builds the
// server the way the release workflow does — injecting the tag-shaped version
// via -ldflags — and asserts the version reported over the wire in `initialize`
// equals the version the plugin manifests ship.
//
// This lives in the Go suite rather than as a bespoke workflow step so it runs
// in CI (via the mcp-server job's `go test -race ./...`) and locally, on the
// same footing. The repo's manifest-sync job only diffs the manifests against
// each other and cannot see inside the Go source; this closes that gap.
func TestServerInfoVersionMatchesManifest(t *testing.T) {
	manifest := filepath.Join("..", "..", "..", ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read %s: %v", manifest, err)
	}
	var m struct {
		Plugins []struct {
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse %s: %v", manifest, err)
	}
	if len(m.Plugins) == 0 || m.Plugins[0].Version == "" {
		t.Fatalf("%s: no plugins[0].version to compare against", manifest)
	}
	want := m.Plugins[0].Version

	// The release workflow injects github.ref_name, which carries the "v".
	bin := buildTestBinaryWithVersion(t, "v"+want)
	if got := initializeServerVersion(t, bin); got != want {
		t.Errorf("MCP serverInfo.version = %q but the manifests ship %q.\n"+
			"serverInfo.version must derive from the ldflags-injected main.Version "+
			"(see serverVersion() in mcp_server.go) — do not hardcode it.", got, want)
	}
}

// TestServerVersionStripsTagPrefix pins the normalization that makes
// serverInfo.version comparable to the manifest version: the release workflow
// injects the git tag ("v2.11.0") but the manifests carry "2.11.0".
func TestServerVersionStripsTagPrefix(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	cases := []struct{ injected, want string }{
		{"v2.11.0", "2.11.0"},
		{"2.11.0", "2.11.0"},
		{"dev", "dev"},
		{"v1.0.0-rc.1", "1.0.0-rc.1"},
	}
	for _, c := range cases {
		Version = c.injected
		if got := serverVersion(); got != c.want {
			t.Errorf("serverVersion() with Version=%q = %q, want %q", c.injected, got, c.want)
		}
	}
}
