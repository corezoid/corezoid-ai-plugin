#!/bin/sh
# Start MCP server: use cached prebuilt binary from GitHub Releases, fall back to go run .
# Set COREZOID_MCP_DEV=1 to skip cache and always compile from source.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ -n "$COREZOID_MCP_DEV" ]; then
  cd "$SCRIPT_DIR" && exec go run . "$@"
fi

# Prefer a locally built binary (gitignored) — lets developers test source changes instantly.
if [ -x "$SCRIPT_DIR/convctl" ]; then
  exec "$SCRIPT_DIR/convctl" "$@"
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
EXT=""
# Git Bash / MSYS2 / Cygwin report e.g. "MINGW64_NT-10.0-22631". Without this
# mapping Windows always fell through to `go run .`, whose cold compile
# regularly overran the MCP client's 30-second startup timeout.
case "$OS" in
  mingw*|msys*|cygwin*) OS="windows"; EXT=".exe" ;;
esac
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
esac

VERSION=$(grep '"version"' "$SCRIPT_DIR/../.claude-plugin/plugin.json" 2>/dev/null \
  | sed 's/.*"version": *"\([^"]*\)".*/\1/' | head -1)

# download <url> <dest> <max-seconds> — bounded so a stalled network can't hold
# the MCP client past its 30-second startup timeout; on failure we fall back to
# building from source. Both downloads together get at most 20 s, leaving the
# rest of the budget for the binary to start and answer initialize.
download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 5 --max-time "$3" "$1" -o "$2" 2>/dev/null
  elif command -v wget >/dev/null 2>&1; then
    # wget's --timeout bounds each stall, not the whole transfer; timeout(1)
    # caps the total where it exists.
    if command -v timeout >/dev/null 2>&1; then
      timeout "$3" wget -q --timeout=5 --tries=1 "$1" -O "$2" 2>/dev/null
    else
      wget -q --timeout=5 --tries=1 "$1" -O "$2" 2>/dev/null
    fi
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

if [ -n "$VERSION" ] && { [ "$OS" = "darwin" ] || [ "$OS" = "linux" ] || [ "$OS" = "windows" ]; } && \
   { [ "$ARCH" = "amd64" ] || [ "$ARCH" = "arm64" ]; }; then

  ASSET="convctl-${OS}-${ARCH}${EXT}"
  CACHE_DIR="$HOME/.cache/corezoid-mcp/$VERSION"
  CACHE_BIN="$CACHE_DIR/$ASSET"
  BASE_URL="https://github.com/corezoid/corezoid-ai-plugin/releases/download/v${VERSION}"

  if [ ! -x "$CACHE_BIN" ]; then
    mkdir -p "$CACHE_DIR"
    TMP_BIN="${CACHE_BIN}.tmp"
    TMP_SUMS="${CACHE_DIR}/checksums.txt.tmp"

    # checksums.txt first: it is tiny, so a dead or stalled network costs its
    # 5 s cap rather than the binary's 15 s before we fall back.
    if download "${BASE_URL}/checksums.txt" "$TMP_SUMS" 5 && \
       download "${BASE_URL}/${ASSET}" "$TMP_BIN" 15; then

      EXPECTED=$(grep "${ASSET}\$" "$TMP_SUMS" | awk '{print $1}')
      ACTUAL=$(sha256 "$TMP_BIN")

      if [ -n "$EXPECTED" ] && [ -n "$ACTUAL" ] && [ "$ACTUAL" = "$EXPECTED" ]; then
        mv "$TMP_BIN" "$CACHE_BIN" && chmod +x "$CACHE_BIN"
        mv "$TMP_SUMS" "${CACHE_DIR}/checksums.txt"
      else
        rm -f "$TMP_BIN" "$TMP_SUMS"
        echo "corezoid-mcp: warning: checksum mismatch for convctl v${VERSION} (${OS}-${ARCH}) — ignoring download" >&2
      fi
    else
      rm -f "$TMP_BIN" "$TMP_SUMS" 2>/dev/null
      echo "corezoid-mcp: warning: no prebuilt binary found for v${VERSION} (${OS}-${ARCH})" >&2
      echo "  Check that a GitHub release exists: https://github.com/corezoid/corezoid-ai-plugin/releases/tag/v${VERSION}" >&2
    fi
  fi

  if [ -x "$CACHE_BIN" ]; then
    exec "$CACHE_BIN" "$@"
  fi
fi

# Fallback: compile from source (requires Go)
if ! command -v go >/dev/null 2>&1; then
  echo "corezoid-mcp: error: no prebuilt binary for v${VERSION} on ${OS}-${ARCH} and 'go' was not found." >&2
  echo "  → Update the plugin to the latest release: https://github.com/corezoid/corezoid-ai-plugin/releases/latest" >&2
  echo "  → Or install Go to build from source: https://go.dev/dl/" >&2
  exit 1
fi
cd "$SCRIPT_DIR" && exec go run . "$@"
