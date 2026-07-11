#!/bin/sh
# Start MCP server: use cached prebuilt binary from GitHub Releases, fall back to go run .
# Set COREZOID_MCP_DEV=1 to skip cache and always compile from source.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ -n "$COREZOID_MCP_DEV" ]; then
  cd "$SCRIPT_DIR" && exec go run . "$@"
fi

# sha256 <file> — prints hex digest
sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# notice_binary_change <path> — warn when the binary differs from the last run:
# sessions attached to the previous process keep dead tool handles and must be
# restarted (a plain reconnect may not be enough).
notice_binary_change() {
  MARKER_DIR="$HOME/.cache/corezoid-mcp"
  mkdir -p "$MARKER_DIR" 2>/dev/null || return 0
  # One marker per binary path: alternating between a dev checkout and the
  # release cache must not cry "changed" on every start.
  PATH_KEY=$(printf '%s' "$1" | cksum | awk '{print $1}')
  MARKER="$MARKER_DIR/.last-binary.$PATH_KEY"
  NEW_HASH=$(sha256 "$1" 2>/dev/null)
  # sha256() ends in a pipe, so its exit code is awk's — check the output
  # instead: no hash tool (or an unreadable file) yields an empty string.
  [ -n "$NEW_HASH" ] || return 0
  OLD_HASH=$(cat "$MARKER" 2>/dev/null)
  if [ -n "$OLD_HASH" ] && [ "$OLD_HASH" != "$NEW_HASH" ]; then
    echo "[corezoid-mcp] binary changed since the last run (${OLD_HASH%${OLD_HASH#??????????}}… -> ${NEW_HASH%${NEW_HASH#??????????}}…) — live Claude Code sessions started before this must be RESTARTED (a plain reconnect may not be enough)." >&2
  fi
  printf '%s' "$NEW_HASH" > "$MARKER" 2>/dev/null || true
}

# Prefer a locally built binary (gitignored) — lets developers test source changes instantly.
if [ -x "$SCRIPT_DIR/convctl" ]; then
  notice_binary_change "$SCRIPT_DIR/convctl"
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
    notice_binary_change "$CACHE_BIN"
    exec "$CACHE_BIN" "$@"
  fi
fi

# Fallback: compile from source (requires Go)
cd "$SCRIPT_DIR" && exec go run . "$@"
