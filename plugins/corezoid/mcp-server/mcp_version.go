package main

import "strings"

// fallbackServerVersion is the version reported when the binary was built
// without -ldflags "-X main.Version=..." — most importantly run.sh's
// compile-from-source path and a bare `go run .`.
//
// It MUST equal the version in the four plugin manifests. That used to be a
// hand-maintained constant nobody remembered to touch, which is how the server
// spent several releases announcing itself as 2.3.5 while the plugin shipped
// 2.11.0.
//
// What now makes a recurrence a build failure rather than a silent lie is
// TestFallbackServerVersionMatchesManifest in this package, which runs as part
// of the mcp-server job in .github/workflows/ci.yml. Note that the separate
// "Version sync across manifests" step in that workflow only compares the four
// manifests to each other — it does NOT read this constant, so the Go test is
// the only thing standing between a release and another silent drift.
// scripts/check-version-sync.py compares all five in one shot and is the
// pre-tag check (see RELEASE_CHECKLIST.md); wiring it into the workflow in
// place of that inline step would be the tidier arrangement, but requires a
// token with the `workflows` scope.
const fallbackServerVersion = "2.11.0"

// serverVersion resolves the version reported in initialize.serverInfo, in
// analytics events, and in feedback reports.
//
// Release binaries get the real version injected into main.Version by the
// release workflow's -ldflags; run.sh injects the manifest version when it
// falls back to compiling from source. Anything else (tests, `go run .` by
// hand, an inspector session) leaves the "dev" placeholder, and reporting the
// manifest version there is more useful than reporting "dev" — the running
// code IS that plugin release's code.
//
// The leading "v" is stripped so the wire value matches the manifests'
// semver ("2.11.0"), regardless of whether the tag-shaped "v2.11.0" was
// injected.
func serverVersion() string {
	v := strings.TrimSpace(Version)
	if v == "" || v == "dev" {
		return fallbackServerVersion
	}
	return strings.TrimPrefix(v, "v")
}
