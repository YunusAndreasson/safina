#!/usr/bin/env bash
# Fetch the k9s release pinned in bench/versus/versions.json into bench/.build,
# verifying its checksum. safina-versus runs this binary, never one on PATH,
# so a comparison names exactly what it measured.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PINS="$ROOT/bench/versus/versions.json"
DEST="$ROOT/bench/.build"

case "$(uname -s)" in
    Linux) os=Linux ;;
    Darwin) os=Darwin ;;
    *) echo "unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "unsupported arch $(uname -m)" >&2; exit 1 ;;
esac

read -r version url sum < <(python3 - "$PINS" "${os}_${arch}" <<'PY'
import json, sys
pin = json.load(open(sys.argv[1]))["k9s"]
key = sys.argv[2]
print(pin["version"], pin["url"].replace("{os}_{arch}", key), pin["sha256"][key])
PY
)

if [[ -x "$DEST/k9s" ]] && "$DEST/k9s" version -s 2>/dev/null | grep -q "$version"; then
    echo "k9s $version already in $DEST"
    exit 0
fi

mkdir -p "$DEST"
tmp="$(mktemp -d "$DEST/k9s.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
echo "→ downloading k9s $version ($os/$arch)"
curl -fsSL -o "$tmp/k9s.tar.gz" "$url"
if command -v sha256sum >/dev/null; then
    echo "$sum  $tmp/k9s.tar.gz" | sha256sum -c --quiet
else
    echo "$sum  $tmp/k9s.tar.gz" | shasum -a 256 -c --quiet
fi
tar -xzf "$tmp/k9s.tar.gz" -C "$tmp" k9s
mv "$tmp/k9s" "$DEST/k9s"
"$DEST/k9s" version -s | head -1
