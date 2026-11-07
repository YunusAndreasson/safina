#!/usr/bin/env bash
# Render public/og.jpg (1200x630 social card) from scripts/og.html.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp="$(mktemp -d)"
chromium --headless=new --disable-gpu --hide-scrollbars --allow-file-access-from-files \
	--user-data-dir="$tmp/profile" --window-size=1200,630 --virtual-time-budget=4000 \
	--screenshot="$tmp/og.png" "file://$PWD/scripts/og.html" 2>/dev/null
magick "$tmp/og.png" -crop 1200x630+0+0 +repage -quality 86 -sampling-factor 4:4:4 public/og.jpg
rm -rf "$tmp"
magick identify -format "public/og.jpg %wx%h %b\n" public/og.jpg
