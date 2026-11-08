#!/usr/bin/env bash
# Phone crops of each screenshot: the region that matters, at a size where
# the text stays readable on a 360-430px screen. Boxes are in terminal cells
# (col row cols rows), converted with the geometry snap.mjs renders at:
# 10x22 CSS px cells, 22px/20px padding, device scale 2.
set -euo pipefail
cd "$(dirname "$0")/../src/assets/shots"

CW=20 CH=44 PX=44 PY=40 # cell size and padding in output pixels

BG="#1a1b26" # Tokyo Night background, the terminal's own

# Cut exactly on cell edges, then pad with the terminal background, so no
# sliver of a neighbouring row or column shows at the edge of a crop.
cut() { # src out col row cols rows
	magick "$1" -crop "$(($5 * CW))x$(($6 * CH))+$((PX + $3 * CW))+$((PY + $4 * CH))" +repage \
		-bordercolor "$BG" -border 16 "$2"
	magick identify -format "%f %wx%h\n" "$2"
}

crop() { # name col row cols rows
	[[ -f "$1.png" ]] || return 0
	cut "$1.png" "$1-m.png" "$2" "$3" "$4" "$5"
}

# Feature strips: the terminal's full width, so they share the tour's text
# scale, cut down to the rows that matter. Used from tablet width up.
cropf() { # like crop, but writes <name>-f.png and leaves <name>-m.png alone
	cut "$1.png" "$1-f.png" "$2" "$3" "$4" "$5"
}
# The boxes live in src/data/crops.json, which the player reads too.
while read -r name kind col row cols rows; do
	if [[ "$kind" == m ]]; then crop "$name" "$col" "$row" "$cols" "$rows"; else cropf "$name" "$col" "$row" "$cols" "$rows"; fi
done < <(node -e '
	const c = require(process.argv[1]);
	for (const [name, kinds] of Object.entries(c)) if (!name.startsWith("/")) for (const [k, b] of Object.entries(kinds)) console.log(name, k, ...b);
' "$PWD/../../data/crops.json")
