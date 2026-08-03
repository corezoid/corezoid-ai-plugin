#!/usr/bin/env python3
"""Check that the plugin version agrees everywhere it is written down.

Five places, not four: the MCP server also carries the version, and reports it
to every client in `initialize.serverInfo.version`. That constant silently
drifted several releases behind the manifests before anything checked it.

Run from the repository root:

    python3 scripts/check-version-sync.py

Exits non-zero and prints every disagreement. The same invariant is enforced in
CI by TestFallbackServerVersionMatchesManifest, which runs as part of the
MCP server's `go test` job.
"""

import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent

MANIFESTS = [
    ("plugins/corezoid/.claude-plugin/plugin.json", lambda d: d["version"]),
    ("plugins/corezoid/.codex-plugin/plugin.json", lambda d: d["version"]),
    (".claude-plugin/marketplace.json", lambda d: d["plugins"][0]["version"]),
    (".agents/plugins/marketplace.json", lambda d: d["plugins"][0]["version"]),
]

MCP_VERSION_GO = "plugins/corezoid/mcp-server/mcp_version.go"


def main() -> int:
    versions = {}
    errors = []

    for rel, getter in MANIFESTS:
        path = ROOT / rel
        try:
            with open(path) as f:
                versions[rel] = getter(json.load(f))
        except (OSError, ValueError, KeyError, IndexError) as exc:
            errors.append(f"{rel}: could not read version ({exc})")

    match = None
    try:
        source = (ROOT / MCP_VERSION_GO).read_text()
        match = re.search(r'fallbackServerVersion\s*=\s*"([^"]+)"', source)
    except OSError as exc:
        errors.append(f"{MCP_VERSION_GO}: could not read ({exc})")
    if match:
        versions[f"{MCP_VERSION_GO} (fallbackServerVersion)"] = match.group(1)
    elif not errors:
        errors.append(f"{MCP_VERSION_GO}: fallbackServerVersion constant not found")

    for where, version in versions.items():
        print(f"  {version:<12} {where}")

    if len(set(versions.values())) > 1:
        errors.append("version mismatch — all five must agree (see RELEASE_CHECKLIST.md)")

    if errors:
        for e in errors:
            print("ERROR:", e, file=sys.stderr)
        return 1

    print(f"OK: version {next(iter(versions.values()))} is in sync across {len(versions)} files")
    return 0


if __name__ == "__main__":
    sys.exit(main())
