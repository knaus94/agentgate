#!/bin/sh
# Install the binary shipped in a release archive; no network or root access is needed.
set -eu

usage() {
  cat <<'HELP'
Usage: sh scripts/install-local.sh [--prefix <directory>] [--lead claude|codex|both] [--no-integrations]

Copies the binary for this Linux architecture to <directory>/agentgate.
Default directory: ~/.local/bin. Default lead integration: both.
--no-integrations copies the binary only; install integrations later with agentgate setup install.
--help performs no writes. This installer does not download dependencies.
HELP
}

for argument in "$@"; do
  case "$argument" in -h|--help) usage; exit 0 ;; esac
done

prefix=${HOME:?HOME is required}/.local/bin
lead=both
integrations=yes
while [ "$#" -gt 0 ]; do
  case "$1" in
    --prefix|--lead)
      option=$1
      shift
      [ "$#" -gt 0 ] && [ -n "$1" ] || { echo "$option requires a value" >&2; exit 2; }
      case "$option" in --prefix) prefix=$1 ;; --lead) lead=$1 ;; esac
      ;;
    --no-integrations) integrations=no ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done
case "$lead" in claude|codex|both) ;; *) echo 'Lead must be claude, codex or both' >&2; exit 2 ;; esac
[ "$(uname -s)" = Linux ] || { echo 'AgentGate requires Linux (native Linux or WSL2).' >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) architecture=amd64 ;;
  aarch64|arm64) architecture=arm64 ;;
  *) echo 'Supported architectures: x86-64 and arm64.' >&2; exit 1 ;;
esac
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
source_binary=$root/bin/linux-$architecture/agentgate
if [ ! -f "$source_binary" ]; then source_binary=$root/dist/linux-$architecture/agentgate; fi
[ -f "$source_binary" ] || { echo 'No release binary found. Build one with make binaries first.' >&2; exit 1; }
if [ -f "$root/SHA256SUMS" ]; then
  expected=$(awk -v name="bin/linux-$architecture/agentgate" '$2 == name {print $1}' "$root/SHA256SUMS")
  [ -n "$expected" ] || { echo 'Binary checksum is missing from SHA256SUMS.' >&2; exit 1; }
  actual=$(sha256sum "$source_binary")
  actual=${actual%% *}
  [ "$actual" = "$expected" ] || { echo 'Binary checksum mismatch. Extract the archive again.' >&2; exit 1; }
fi
mkdir -p "$prefix"
temporary=$(mktemp "$prefix/.agentgate-install.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM
cat "$source_binary" > "$temporary"
chmod 755 "$temporary"
mv -f "$temporary" "$prefix/agentgate"
printf 'Installed %s\n' "$prefix/agentgate"
"$prefix/agentgate" --version
if [ "$integrations" = yes ]; then "$prefix/agentgate" setup install --lead "$lead"; fi
printf '\nNext: %s setup doctor\n' "$prefix/agentgate"
case ":$PATH:" in *":$prefix:"*) ;; *) printf 'Add %s to PATH before opening a new lead session.\n' "$prefix" ;; esac
