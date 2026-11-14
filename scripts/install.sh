#!/bin/sh
# Install agentgate from the latest GitHub release.
#   AGENTGATE_VERSION=v0.1.0   pin a version
#   AGENTGATE_BIN=/some/dir    install directory (default ~/.local/bin)
#   AGENTGATE_LEAD=claude|codex|both   which sessions lead (asked when a terminal is attached)
#   AGENTGATE_BASE_URL=...     release mirror (default https://github.com/knaus94/agentgate/releases)
set -eu

REPO="knaus94/agentgate"
BIN="${AGENTGATE_BIN:-$HOME/.local/bin}"

fail() { echo "agentgate install: $*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || fail "only Linux is supported (the sandbox needs bubblewrap)"
case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac
for tool in curl tar sha256sum install mktemp awk; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done

RELEASES="${AGENTGATE_BASE_URL:-https://github.com/$REPO/releases}"
VERSION="${AGENTGATE_VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL --connect-timeout 10 --max-time 120 -o /dev/null -w '%{url_effective}' "$RELEASES/latest" | sed 's#.*/tag/##')
  [ -n "$VERSION" ] || fail "could not find the latest release"
fi
case "$VERSION" in
  '' | [!a-zA-Z0-9]* | *[!a-zA-Z0-9._-]*) fail "invalid release version: $VERSION" ;;
esac
case "${AGENTGATE_LEAD:-}" in
  '' | claude | codex | both) ;;
  *) fail "AGENTGATE_LEAD must be claude, codex or both" ;;
esac

ASSET="agentgate_linux_$ARCH.tar.gz"
BASE="$RELEASES/download/$VERSION"
TMP=$(mktemp -d)
STAGED=""
trap 'rm -rf "$TMP"; [ -z "$STAGED" ] || rm -f "$STAGED"' 0
trap 'exit 130' INT
trap 'exit 143' HUP TERM

echo "Downloading agentgate $VERSION ($ARCH)"
curl -fsSL --connect-timeout 10 --max-time 120 --retry 2 -o "$TMP/$ASSET" "$BASE/$ASSET" || fail "download failed: $BASE/$ASSET"
curl -fsSL --connect-timeout 10 --max-time 120 --retry 2 -o "$TMP/checksums.txt" "$BASE/checksums.txt" || fail "download failed: checksums.txt"
EXPECTED=$(awk -v asset="$ASSET" '$2 == asset || $2 == "*" asset { print $1 }' "$TMP/checksums.txt")
case "$EXPECTED" in
  '' | *[!a-fA-F0-9]*) fail "missing, duplicate or invalid release checksum" ;;
esac
[ "${#EXPECTED}" -eq 64 ] || fail "invalid SHA-256 digest"
ACTUAL=$(sha256sum "$TMP/$ASSET")
[ "${ACTUAL%% *}" = "$EXPECTED" ] || fail "checksum mismatch; nothing was installed"

tar -xOzf "$TMP/$ASSET" agentgate > "$TMP/agentgate" || fail "archive has no agentgate binary"
[ -s "$TMP/agentgate" ] || fail "archive contains an empty agentgate binary"
mkdir -p "$BIN"
STAGED=$(mktemp "$BIN/.agentgate.XXXXXX")
install -m 0755 "$TMP/agentgate" "$STAGED"
mv -f "$STAGED" "$BIN/agentgate"
STAGED=""
echo "Installed $BIN/agentgate"

case ":$PATH:" in
  *":$BIN:"*) ;;
  *) echo "Note: $BIN is not in PATH. Add it, for example: export PATH=\"$BIN:\$PATH\"" ;;
esac

# Which sessions lead the work: AGENTGATE_LEAD=claude|codex|both, or a question when a terminal is attached.
LEAD="${AGENTGATE_LEAD:-}"
if [ -z "$LEAD" ] && [ -r /dev/tty ] && [ -w /dev/tty ] && ( : < /dev/tty ) 2>/dev/null; then
  {
    echo
    echo "Which sessions lead the work?"
    echo "  1) Claude Code leads; Claude and Codex models work   (default)"
    echo "  2) Codex only: Codex leads, Codex models work"
    echo "  3) both: /dev in Claude Code and \$dev in Codex"
    printf "Choose 1, 2 or 3: "
  } > /dev/tty
  read -r answer < /dev/tty || answer=""
  case "$answer" in
    2) LEAD=codex ;;
    3) LEAD=both ;;
    *) LEAD=claude ;;
  esac
fi
if [ -n "$LEAD" ]; then
  "$BIN/agentgate" install --lead "$LEAD"
else
  "$BIN/agentgate" install
fi
echo
"$BIN/agentgate" doctor >/dev/null 2>&1 && echo "Environment looks good. Start a new session and type /dev help (Claude Code) or \$dev help (Codex)." \
  || echo "Run '$BIN/agentgate doctor' to see what is missing (bwrap, claude, codex)."
