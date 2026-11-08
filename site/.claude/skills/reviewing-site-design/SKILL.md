---
name: reviewing-site-design
description: Reviews a visual or layout change to the safina website frame by frame against the direction in site/AGENTS.md - grid, type, palette and light, motion, images, responsive behaviour, accessibility and page weight - using real headless screenshots in Chromium and in Gecko (Zen, the user's browser) from scripts/frames.mjs. Use after changing anything under site/src that renders, before committing a site change, or when asked to review, critique, QA or polish the site's design.
argument-hint: "[what changed, or a section name]"
---

# Reviewing site design

Judge the frames, not the code. A visitor never sees the CSS; they see the
beat they've scrolled to, in their own browser. `site/AGENTS.md` is the
standard, and README.md §Layout and §Palette hold the details it points to.

## 1. Write down the intent

Read the change (`git diff -- src`, plus anything untracked under `src/`) and
write one line: what this should do to the visitor, and at which beat (the
acts in AGENTS.md). Review against that line, not against your own taste.

## 2. Shoot

```sh
pnpm build
pnpm preview                                 # in the background; serves :4321
node scripts/frames.mjs --label after        # Chromium, six viewports
node scripts/frames.mjs --label after --engine gecko --only desktop,phone
node scripts/frames.mjs --label after-still --reduced --only desktop,phone
```

- Shot `--label before` before the edit? Compare the same frame names side by
  side. If not, review the after frames alone. Never stash, reset or check out
  over the working tree to make a before.
- The user browses in Zen (Gecko). A Chromium pass alone isn't evidence; the
  Gecko frames are required. For a bug the user reports seeing, also follow
  their browser rule (a probe page in their own Zen), because the headless
  profile has none of their extensions.
- Headless Gecko doesn't render `backdrop-filter`, so the scrolled nav looks
  see-through in Gecko frames, with text running behind the wordmark. That's
  the headless renderer, not the site. Judge blur in Chromium frames or in
  the user's own Zen.
- Sections taller than the screen get continuation frames (`safety-2`,
  `tour-2`). Look at those too; the key list and the tour's controls live
  there.
- Look at `<viewport>-storyboard.png` for desktop and phone in both engines,
  then open full-size frames wherever something looks off, and every frame the
  change touches.

## 3. Review

Go through each group. A finding names the frame where you saw it.

**Grid.** Measure it; don't eyeball it:

```sh
node scripts/grid.mjs http://127.0.0.1:4321/                  # six viewports, Chromium
node scripts/grid.mjs http://127.0.0.1:4321/ --engine gecko
node scripts/grid.mjs http://127.0.0.1:4321/ --widths 700,960,1180,1300,1512
node scripts/grid.mjs http://127.0.0.1:4321/ --only desktop --lines   # every edge in use
```

It reports near misses (an edge within 24px of a line but not on it), titles
and text that don't start level, and two lines a few pixels apart. The rules
it encodes:

1. The lines are the grid's, not numbers: the title line (the column's left
   edge), the text line (`--split`, side by side only), the column's right
   edge, and, stacked, the measure's right edge. Secondary grids divide a
   column evenly (halves, thirds) and stay inside it.
2. Measure layout edges, not ink: a text run starts where its first line box
   starts, a drawn box at its border.
3. Optical corrections are declared, never accidental: a small negative
   inline-start margin (titles 0.05em, the headline's T bearing).
4. Hang only light marks. A solid shape (the wordmark's sail, a button, a
   rule) is an edge and stands on the line; hung, it starts a second edge.
5. Inside a drawn box (a tray, a code block, a button, a keycap) contents
   align to the box, and the box answers to the grid. Boxes of one kind share
   their inset (both code blocks start text 22px in).
6. Stacked, everything that reads (text, lists, tables, the key list) holds
   to the measure; screenshots and photographs take the column.
7. Side by side, a title and its column's first line start level at cap
   height.
8. Tiled windows land at any width: sweep widths, not only the six
   viewports.

Spacing between sections steps by `--s1` to `--s5`, with no new magic
numbers. Only photographs and the tour window bleed past the column.

**Type.** Three voices only (titles, reading, acting). Mona Sans width and
weight follow the tokens, with no italics and no new face. No paragraph ends
on a single word in any frame, no heading breaks badly, and no text is
clipped. Text-art uses only characters the mono subset carries
(ASCII and Latin-1); anything else falls back to another face (README
§Text-art).

**Palette and light.** Colours come from tokens. This finds raw ones:

```sh
grep -rnoE '#[0-9a-fA-F]{3,8}\b|rgba?\([0-9]' src --include=*.astro --include=*.css --include=*.ts | grep -v src/styles/global.css
```

Known and fine as of Nov 2026: `Base.astro` theme-color (a meta tag can't read
a token), the black shadow and mask in `Frame.astro`, `#fff` for the selected
tour key, and `#8288` (the word-joiner entity in `index.astro`). Anything new
needs a reason. Cyan marks only what acts or where you are; red marks only
writes. Every glow has a source. The amber lamp is the only warm light. Where
the product window appears, nothing on screen is brighter. Film grain sits on
photographs only.

**Motion.** Every movement has a cause (a scroll or a key). Cuts run 160 to
240 ms on `--ease-out`. Scroll-driven work sits behind
`@supports (animation-timeline: …)` and `prefers-reduced-motion:
no-preference`. In the `--reduced` frames, every beat reads as a still. Gecko
and Chromium land on the same beat at the same scroll offset.

**Images.** Screenshots are shown as rendered, with no filter, tint or overlay
at rest. A recording opens and ends exactly on its stills: press its key
in both engines and compare the last frame with the still (no sliver of a
neighbouring row at a crop's edge). Phone crops (`-m`) keep terminal text
readable, and a panned crop opens on its payoff (`panStart`): check the phone
frames show the changed host in the diff and "2/2 ready" in the Logbook. Photographs stay
graded below the product, and each crop keeps its subject (the ship, the
lamp, the dawn line) clear of fades and text at every viewport: on phones
and portrait tablets the first screen shows the ship and its lamp above the
headline. Every photograph's ship sails toward screen left. Every image has alt text that states what it shows.

**Responsive.** `metrics.json` shows `overflowX` 0 at every viewport. Tap
targets are 44 px or more on phones. Check the landscape phone (844×390) and
the portrait tablet (820×1180) explicitly; they take their own art direction
in `Opening.astro`.

**Accessibility.** Text contrast is at least 4.5:1, or 3:1 for large text.
Compute it for any new colour pair and quote the ratio. Focus rings show, the
tour works from the keyboard, headings descend in order, and Arabic text
carries `lang="ar"`.

**Weight.** Compare `metrics.json` before and after: first-load KB, request
count, JS KB, the LCP element, CLS. Any growth needs a reason the user agreed
to (AGENTS.md, the 2026 bar).

## 4. Report

Rank the findings:

- **Blocker**: breaks the tagline, accessibility, layout or a hard rule in
  AGENTS.md.
- **Major**: works but goes against the direction.
- **Minor**: polish.

Each finding gives the viewport and engine, the frame path, what's wrong, the
rule it breaks (an AGENTS.md rule number or a README section), and the fix.
End with one line on what works and should stay.

Don't change code during a review unless the user asked for fixes. If they
did, fix Blockers and Majors first, re-shoot, and report again with what
changed.
