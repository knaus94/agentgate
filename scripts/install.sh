#!/bin/sh
# Install claude-dev from the latest GitHub release.
#   CLAUDE_DEV_VERSION=v0.1.0   pin a version
#   CLAUDE_DEV_BIN=/some/dir    install directory (default ~/.local/bin)
#   CLAUDE_DEV_BASE_URL=...     release mirror (default https://github.com/knaus94/claude-dev/releases)
set -eu

REPO="knaus94/claude-dev"
BIN="${CLAUDE_DEV_BIN:-$HOME/.local/bin}"

fail() { echo "claude-dev install: $*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || fail "only Linux is supported (the sandbox needs bubblewrap)"
case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac
for tool in curl tar sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done

RELEASES="${CLAUDE_DEV_BASE_URL:-https://github.com/$REPO/releases}"
VERSION="${CLAUDE_DEV_VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$RELEASES/latest" | sed 's#.*/tag/##')
  [ -n "$VERSION" ] || fail "could not find the latest release"
fi

ASSET="claude-dev_linux_$ARCH.tar.gz"
BASE="$RELEASES/download/$VERSION"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Downloading claude-dev $VERSION ($ARCH)"
curl -fsSL -o "$TMP/$ASSET" "$BASE/$ASSET" || fail "download failed: $BASE/$ASSET"
curl -fsSL -o "$TMP/checksums.txt" "$BASE/checksums.txt" || fail "download failed: checksums.txt"
(cd "$TMP" && grep " $ASSET\$" checksums.txt | sha256sum -c - >/dev/null) || fail "checksum mismatch; nothing was installed"

tar -xzf "$TMP/$ASSET" -C "$TMP" claude-dev
mkdir -p "$BIN"
install -m 0755 "$TMP/claude-dev" "$BIN/claude-dev"
echo "Installed $BIN/claude-dev"

case ":$PATH:" in
  *":$BIN:"*) ;;
  *) echo "Note: $BIN is not in PATH. Add it, for example: export PATH=\"$BIN:\$PATH\"" ;;
esac

"$BIN/claude-dev" install
echo
"$BIN/claude-dev" doctor >/dev/null 2>&1 && echo "Environment looks good. Open a new Claude Code session and type: /dev help" \
  || echo "Run '$BIN/claude-dev doctor' to see what is missing (bwrap, claude, codex)."
