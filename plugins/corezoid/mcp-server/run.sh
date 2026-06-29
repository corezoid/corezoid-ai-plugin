#!/bin/sh
# Start MCP server: use cached prebuilt binary from GitHub Releases, fall back to go run .
# Set COREZOID_MCP_DEV=1 to skip cache and always compile from source.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Host-neutral plugin root. Resolves from whichever host-specific variable is
# present (Claude Code, Codex, AWS Kiro), with a fallback to the directory
# containing run.sh so the script works when invoked directly. CLAUDE_PLUGIN_ROOT
# is re-exported for downstream consumers that still read the legacy name.
PLUGIN_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-${KIRO_PLUGIN_ROOT:-$SCRIPT_DIR/..}}}"
export PLUGIN_ROOT
export CLAUDE_PLUGIN_ROOT="$PLUGIN_ROOT"

if [ -n "$COREZOID_MCP_DEV" ]; then
  cd "$SCRIPT_DIR" && exec go run . "$@"
fi

# Prefer a locally built binary (gitignored) — lets developers test source changes instantly.
if [ -x "$SCRIPT_DIR/convctl" ]; then
  exec "$SCRIPT_DIR/convctl" "$@"
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
esac

VERSION=$(grep '"version"' "$SCRIPT_DIR/../.claude-plugin/plugin.json" 2>/dev/null \
  | sed 's/.*"version": *"\([^"]*\)".*/\1/' | head -1)

# download <url> <dest>
download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2" 2>/dev/null
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$1" -O "$2" 2>/dev/null
  else
    return 1
  fi
}

# sha256 <file> — prints hex digest
sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

if [ -n "$VERSION" ] && { [ "$OS" = "darwin" ] || [ "$OS" = "linux" ]; } && \
   { [ "$ARCH" = "amd64" ] || [ "$ARCH" = "arm64" ]; }; then

  CACHE_DIR="$HOME/.cache/corezoid-mcp/$VERSION"
  CACHE_BIN="$CACHE_DIR/convctl-${OS}-${ARCH}"
  BASE_URL="https://github.com/corezoid/corezoid-ai-plugin/releases/download/v${VERSION}"

  if [ ! -x "$CACHE_BIN" ]; then
    mkdir -p "$CACHE_DIR"
    TMP_BIN="${CACHE_BIN}.tmp"
    TMP_SUMS="${CACHE_DIR}/checksums.txt.tmp"

    if download "${BASE_URL}/convctl-${OS}-${ARCH}" "$TMP_BIN" && \
       download "${BASE_URL}/checksums.txt" "$TMP_SUMS"; then

      EXPECTED=$(grep "convctl-${OS}-${ARCH}$" "$TMP_SUMS" | awk '{print $1}')
      ACTUAL=$(sha256 "$TMP_BIN")

      if [ -n "$EXPECTED" ] && [ -n "$ACTUAL" ] && [ "$ACTUAL" = "$EXPECTED" ]; then
        mv "$TMP_BIN" "$CACHE_BIN" && chmod +x "$CACHE_BIN"
        mv "$TMP_SUMS" "${CACHE_DIR}/checksums.txt"
      else
        rm -f "$TMP_BIN" "$TMP_SUMS"
      fi
    else
      rm -f "$TMP_BIN" "$TMP_SUMS" 2>/dev/null
    fi
  fi

  if [ -x "$CACHE_BIN" ]; then
    exec "$CACHE_BIN" "$@"
  fi
fi

# Fallback: compile from source (requires Go)
cd "$SCRIPT_DIR" && exec go run . "$@"
