#!/usr/bin/env bash
# The recordings' font: JetBrainsMono Nerd Font (the face the stills are
# rendered in, snap.mjs), cut down to the characters the casts actually use.
# Box drawing, block elements and braille are left out: the player draws
# those itself (xterm.js customGlyphs), as the stills do, so lines join.
#
#   scripts/termfont.sh      after a re-shoot, so a new glyph is never missing
#
# Needs: the font installed (fc-list), pyftsubset (fonttools + brotli).
set -euo pipefail
cd "$(dirname "$0")/.."
out=src/assets/fonts
mkdir -p "$out"

unicodes="$(node -e '
	const fs = require("fs"), dir = "src/assets/casts";
	const seen = new Set();
	for (const f of fs.readdirSync(dir).filter((f) => f.endsWith(".cast"))) {
		for (const line of fs.readFileSync(`${dir}/${f}`, "utf8").split("\n").slice(1)) {
			if (!line) continue;
			const [, kind, data] = JSON.parse(line);
			if (kind !== "o") continue;
			// Text only: strip CSI, OSC and other escape sequences first.
			const text = data.replace(/\x1b\[[0-9;?<=>:!]*[ -\/]*[@-~]/g, "").replace(/\x1b\][^\x07\x1b]*(\x07|\x1b\\)/g, "").replace(/\x1b[@-_]/g, "");
			for (const ch of text) seen.add(ch.codePointAt(0));
		}
	}
	for (let c = 0x20; c < 0x7f; c++) seen.add(c); // ASCII, always
	const drawn = (c) => (c >= 0x2500 && c <= 0x259f) || (c >= 0x2800 && c <= 0x28ff) || (c >= 0xe0b0 && c <= 0xe0bf);
	console.log([...seen].filter((c) => c >= 0x20 && !drawn(c)).sort((a, b) => a - b).map((c) => "U+" + c.toString(16).toUpperCase().padStart(4, "0")).join(","));
')"
echo "glyphs: $(tr ',' '\n' <<<"$unicodes" | wc -l)"

for style in Regular Bold Italic BoldItalic; do
	src="$(fc-list : file family style | grep -i 'JetBrainsMonoNerdFont-'"$style"'\.ttf' | head -1 | cut -d: -f1)"
	[[ -n "$src" ]] || { echo "JetBrainsMono Nerd Font $style not installed" >&2; exit 1; }
	dest="$out/term-$(tr '[:upper:]' '[:lower:]' <<<"$style").woff2"
	pyftsubset "$src" --unicodes="$unicodes" --flavor=woff2 --layout-features='*' --no-hinting --desubroutinize --output-file="$dest"
	echo "$dest $(wc -c <"$dest") bytes"
done
