# safina.dev

The marketing site. Astro 7, static output, no UI framework, dark only (a dark ocean palette, see below).

Modern browsers only: evergreen Chrome, Edge and Safari from mid-2025 on, with
Firefox ESR 140 as the floor (`vite.build.target` in `astro.config.mjs`). Write
the platform as it is today, without fallbacks: native nesting, relative colors
(`rgb(from var(--spray) r g b / 14%)`) for every tint, range media queries
(`width <= 640px`) everywhere including `<source media>` and `sizes`, and
individual `translate`/`scale` properties. Only scroll-driven animations and
view transitions stay behind `@supports` or a feature check. Check changes in
Firefox as well as Chromium.

```sh
pnpm install --frozen-lockfile
pnpm dev        # http://127.0.0.1:4321 (Astro 7 daemonizes: `pnpm astro dev stop|status|logs`)
pnpm verify     # strict typed ESLint + astro check + production build
pnpm lint       # TypeScript, Astro, accessibility, and JS tooling checks
pnpm build      # astro check + astro build into dist/
pnpm preview
```

Use the Node versions declared in `package.json`; `packageManager` pins pnpm.
CI installs from the frozen lockfile and runs `pnpm verify`. TypeScript extends
Astro's strictest preset. Browser scripts belong in `src/scripts/` and are
referenced with `<script src="…">` so ESLint can analyze their complete types.
The root `make verify-all` also runs these checks.

Dev mode encodes AVIF on demand, so the first load of each screenshot is
slow. `build` pre-encodes everything and caches it in `node_modules/.astro`.

## Launch switch

`src/data/release.ts`: set `available: true` once binaries are published on
GitHub Releases. The install section then offers downloads (the visitor's
platform first) instead of the build-from-source line.

## Screenshots

Every screen on the site is real safina, driven against a throwaway minikube
profile and rendered offscreen. Nothing is drawn on your display.

```sh
scripts/demo-cluster.sh            # minikube profile `safina-demo`, own kubeconfig in .demo/
scripts/capture.sh start           # safina in a private tmux server (REBUILD=1 to rebuild it)
scripts/capture.sh keys j Enter    # drive it
scripts/capture.sh text            # read the screen as text
scripts/capture.sh shot workloads  # -> src/assets/shots/workloads.png (2x, xterm.js + headless Chromium)
scripts/capture.sh stop            # also restores ~/.safina/config.yaml
scripts/crops.sh                   # phone crops (<name>-m.png), boxes in terminal cells
scripts/demo-cluster.sh stop       # or `delete`
```

Let safina sit on the Workloads view for ~15 minutes before shooting so the
trend lines have history. `scripts/edit-fix.sh` is the stand-in `$EDITOR`
used for the diff screenshot.

## Images

`scripts/image-prompts.md` has the Codex prompts for the three photographs.
`scripts/og.sh` renders `public/og.jpg` from `scripts/og.html`.

Fonts (OFL, licences beside them in `src/assets/fonts/`) are subset with
`pyftsubset`. Mona Sans is the sans, keeping both its weight (200–900) and
width (75–125) axes, because the headline, wordmark and buttons are set
expanded. JetBrains Mono is the mono, the face the screenshots are rendered
in. To rebuild the sans:

```sh
pyftsubset node_modules/@fontsource-variable/mona-sans/files/mona-sans-latin-wdth-normal.woff2 \
  --unicodes="U+20-7E,U+A0,U+A9,U+D7,U+2013-2014,U+2018-2019,U+201C-201D,U+2026,U+2191,U+2193,U+2212" \
  --flavor=woff2 --output-file=src/assets/fonts/mona-sans-var.woff2
```

Reem Kufi draws the Arabic name in the footer and carries only its letters.
To set another Arabic word, add its letters to `--text`:

```sh
pyftsubset node_modules/@fontsource-variable/reem-kufi/files/reem-kufi-arabic-wght-normal.woff2 \
  --text='سفينة' --layout-features='*' --flavor=woff2 \
  --output-file=src/assets/fonts/reem-kufi-safina.woff2
```

## Voice

The name is Arabic, and the copy carries a few Arabic and Islamic touches.
Each one names something safina really does, with a word or two of gloss so
a reader who has never met the term still follows: Barbarossa mode (after
the Ottoman admiral) for the Shift keys that write, shura (counsel) for
asking Claude, the amanah (the trust of the cluster) for what stays with
you. Hint, never explain: no stories, no verse references in the copy.
Quieter marks: "in sha Allah" on the release promise, the Hijri year in the
footer, the 404 ("Overboard", and the belly of a whale), and the words Nuh
spoke boarding the ark at the top of the page source (`Base.astro`).

## Layout

One grid. Every section hangs off two verticals: the title line (the
column's left edge) and the text line, where a 5:6 split (`--split`, the
`.split` class) sets body text at about 56 characters. Titles sit on the
first, their text on the second, and anything that must line up with a
section's text (the "Write" keys, the install steps, the footer's story)
starts on the second too. Only photographs and the tour window bleed past
the column.

Side by side, a title and its text start level at cap height: `.split`
trims each column's first line with `text-box: trim-start cap alphabetic`.
Titles hang 0.05em into the margin so their stems meet the body text's.

Space steps by the golden ratio, `--s1` to `--s5` (15, 24, 39, 63, 102 at
desktop): s2 inside a group, s3 from a section's text to its figure, s4 to
a follow-on such as the key list, s5 between sections. Use these rather
than new numbers.

## Palette

Dark ocean, defined once in `src/styles/global.css`: the page is the abyss
(`--abyss`), and every fade, letterbox bar and shade dissolves into it. The
one accent is `--beacon`, the cyan of the app's own title bar
(`--tn-cyan`, from `internal/tui/theme/palette.go`), kept for what acts or
marks where you are: the wordmark, the primary button, focus, the selected
tour key and the write keys. Tints are relative colors of these tokens, never
raw channels.
