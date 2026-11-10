# safina.run

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
pnpm ship:dry   # after verify: the deploy without the upload (see Deploy)
```

Use the Node versions declared in `package.json`; `packageManager` pins pnpm.
CI installs from the frozen lockfile and runs `pnpm verify` and `pnpm ship:dry`. TypeScript extends
Astro's strictest preset. Browser scripts belong in `src/scripts/` and are
referenced with `<script src="…">` so ESLint can analyze their complete types.
The root `make verify-all` also runs these checks.

Dev mode encodes AVIF on demand, so the first load of each screenshot is
slow. `build` pre-encodes everything and caches it in `node_modules/.astro`.

## Deploy

The site is https://safina.run: static files on a Cloudflare Worker,
`safina-site` (`wrangler.jsonc`: no Worker code, Cloudflare's asset server
answers every request), with the domain attached as its one custom domain,
the way openarabic.io is served. Every push to `main` that touches `site/` deploys it
(`.github/workflows/deploy-site.yml`), and the Actions tab can run that
workflow by hand. The job runs `pnpm verify`, then `pnpm ship`
(`scripts/ship.mjs`):

1. **Guard**: `wrangler.jsonc` names `safina-site`, has no `main`, routes
   safina.run alone as a custom domain with workers.dev and preview URLs
   off, and `dist/` is built. Any other Worker name is refused, so a copied
   config can't overwrite another Worker on the account, and a missing
   route is refused, so a deploy can't detach the domain.
2. **Upload** with `wrangler deploy`, the commit in the version's message.
3. **Smoke** on https://safina.run, retried for 3 minutes while the
   version spreads (15 on the Worker's first deploy, while the domain gets
   its DNS record and certificate): `/` and the 404 page are this build's
   bytes, `_headers` isn't served, hashed `/_astro/*` files are cached as
   immutable and nothing else is, the recordings come as `text/plain` (so
   the edge compresses them, about 500 KB to 38 KB), and nothing says
   `noindex`.
4. **Roll back** to the version that was live before if the smoke fails.

`pnpm ship:dry` runs the same guard and checks with no upload: wrangler
validates the files, then serves `dist/` through Cloudflare's asset server
on this machine, as safina.run, and the checks run against it. The
`verify` workflow runs it on every pull request. Headers live in
`public/_headers`; change a rule and its check together.

**The domain.** safina.run was bought through Cloudflare Registrar on
2026-11-08, so its zone lives in the same Cloudflare account; `site` in
`astro.config.mjs` gives canonical and social URLs. Moving to another domain
changes `wrangler.jsonc`, `site` and `DOMAIN` in `ship.mjs` in one commit.

**Setup, once.** Two repository secrets under Settings, Secrets and
variables, Actions: `CLOUDFLARE_ACCOUNT_ID`, and `CLOUDFLARE_API_TOKEN`, a
token from the dashboard (My Profile, API Tokens) made from the "Edit
Cloudflare Workers" template, limited to this account and the safina.run
zone. Locally,
`pnpm ship` uses `wrangler login` instead; CI is the normal way to deploy.
Without both secrets, as in a fork, the deploy job is skipped with a
notice rather than failed.

## Releases

A `v*` tag pushed to GitHub builds the Linux binaries (x86-64 and ARM64, on
Ubuntu 22.04, needing glibc 2.34 or newer) and the macOS ones (Apple silicon
and Intel, for macOS 13 or newer, ad-hoc signed and not notarized) and
publishes them with a `checksums.txt` (`.github/workflows/release.yml`; run
it by hand to build without publishing). `public/install.sh`, served at
https://safina.run/install.sh, installs the latest one: it refuses other
systems, musl, old glibc and macOS before 13 with the from-source line, verifies the archive's
SHA-256 and writes only `~/.local/bin/safina`. The archive names are shared
by the workflow, the script and `src/data/release.ts`; change them together.

`src/data/release.ts` drives the install section: `available` (true since
v0.1.0) leads with the one-line install and the archives, the visitor's
platform first; bump `version` with each release. Tag first, then deploy the
site, so the page never offers a release that doesn't exist yet.

## Screenshots and recordings

Every screen on the site is real safina, driven against a throwaway minikube
profile and rendered offscreen. Nothing is drawn on your display. Each still
has a recording of its moment: the bytes safina wrote to its terminal, in
asciinema's format (asciicast v2), cut from one recording of the whole shoot.

```sh
scripts/shoot.sh setup     # reset the incident, build safina, open Workloads on ledger-db, record
                           # ...then let it sit ~15 minutes, so the trend lines have history
scripts/shoot.sh tour      # Drill in, Next failing, Logs (a crash caught live), Events, Hot pods, Metrics
scripts/shoot.sh chat      # the Logbook, the crash's logs entry, c and a question (one live answer)
scripts/shoot.sh fix       # at 140x26: E, the diff, y, Rollout watch to 2/2 ready
scripts/shoot.sh logbook   # at 140x32: the Logbook
scripts/shoot.sh wrap      # cut the casts, the phone crops and the player's font
```

Underneath, `capture.sh` runs safina in a private tmux server (`keys`,
`text`, `shot`, `size`, `mark`, `rec start|stop`); `snap.mjs` renders a still
with xterm.js; `castrec.mjs` timestamps the pane's raw output into the
shoot's cast, with a marker for every key; `castcut.mjs` cuts a scene between
two markers, replaying everything before it into a headless terminal so the
scene opens on the exact screen; `crops.sh` cuts the phone crops from the
boxes in `src/data/crops.json`; `termfont.sh` cuts the player's font down to
the characters the casts use.

Shoot every scene in one session and in page order, so the times only move
forward and the ages agree. safina runs with `TZ=UTC`, the zone the Logbook
prints, in a 160×37 terminal, the narrowest where every workload's name is
whole (at 140 columns the list truncated twenty of them), in the shape
where the tour window and its keys share one 1440×900 screen (the diff is
shot at 160×30, where the edit modal fits its
diff). The chat runs through a `claude` with no tools of its own
(`--tools ""`), so the model sees only what safina sends it.
`scripts/edit-fix.sh` is the stand-in `$EDITOR`; the fixture looks the host
up for real, so ledger-api comes up 2/2 after `y`.

The player (`src/scripts/cast.ts`) plays a cast in xterm.js, the renderer
the stills are made with, laid over its still at the still's own geometry
and cut to the same crop on phones, so a scene opens and ends exactly on its
stills. It loads only as a recording nears the screen (about 114 KB of
script, 60 KB of font, a few KB per cast, all gzipped) and plays on a key or
a click. The deep dive alone also plays itself, stop by stop, once its keys
are on screen, until the visitor's first key or click (or its Pause button);
it pauses on hover, focus, off-screen or in a hidden tab, and never runs
with reduced motion (`src/scripts/tour.ts`). Without WebGL, with JavaScript off, or with
reduced motion, the stills stay (a recording that ends somewhere new cuts
straight to its last frame). Serve `.cast` files compressed: they are
plain text.

After a re-shoot, update each frame's alt text (counts, ages and times as
the new still shows them) and, if a phone crop moved, its `panStart` (the
crop column where the payoff begins).

The session keeps its own Logbook in `.demo/history`, emptied on every
start, so demo activity never reaches `~/.safina/history`. `shoot.sh setup`
re-runs `demo-cluster.sh`, which puts the broken `DATABASE_URL` back.

## Review

`AGENTS.md` holds the site's direction and how to check work against it;
`.claude/skills/` holds the copy, design-review and screening skills that use
these tools:

```sh
pnpm build && pnpm preview                                  # serve the build first
node scripts/frames.mjs --label after                       # frames, storyboards, metrics (Chromium)
node scripts/frames.mjs --label after --engine gecko        # the same beats in Zen or Firefox
node scripts/copy-tells.mjs                                 # the built copy, checked for tells
node scripts/grid.mjs http://127.0.0.1:4321/                # every edge against the grid
```

Frames land in `.frames/` (ignored by git).

## The mark

The mark is a sail, `src/assets/sail.svg`, traced from #1 of the refinement
sheet: three cubic curves, every edge within 0.7px of the drawing. The CSS
masks it out of the wordmark's cyan (Vite inlines it, so it costs no
request); `public/favicon.svg` and `scripts/og.html` carry copies of the
path, so change all three together and re-render `public/og.jpg`.

- **Wordmark**: sized against the top bars of Linear, Railway, Supabase,
  Tailscale, Fly.io and Headlamp (Nov 2026), whose symbols stand 1.31 to
  1.67 times their word's tallest letter. At 1.05em the sail is 1.41 times
  the f, so it reads as a symbol rather than one more letter, and against
  the light letters it carries about the word's own ink (0.95). It's
  centred on the word as theirs are, its tips 0.15em under the baseline so
  the arc of its foot, the waterline, rides on it, and the gap is 0.38 of
  its height, their median. Sized to match the old bar's ink instead
  (0.77em, 1.03 times the f), it read as a letter. #1 won over #6 on
  balance: its base slopes 1.7° to #6's 6.4°, so it sits level where #6
  tips toward the word.
- **Social card**: stacked over the name at the same ink ratio (0.92em), so
  the Arabic name keeps its place clear of the ship.
- **Favicon**: centred on its centre of mass, not its box (the sail's weight
  sits low and right).

The app's top bar still opens with the half block `▌`
(`internal/tui/components/topbar.go`); a terminal can't draw the curve.

## Text-art

The site keeps the terminal's habits where they're true to the product: the
screens move only as safina really drew them, the Logbook's
retention is shown as the real `history:` config, and an ASCII ship sails
under the verse at the top of the page source and again on the 404, with you
waving from its wake. Draw only with characters the mono subset carries
(ASCII and Latin-1, so `·` and `×` but no box drawing, block elements or em
dash), or they fall back to another face. Text-art is `aria-hidden` and never
carries a claim.

## Images

`scripts/image-prompts.md` has the Codex prompts for the photographs: the
storm (`storm-landscape`, and `storm-portrait` for portrait screens), the
storm passing (`storm-passing`, the interlude after the Logbook) and first
light (`first-light`, under Get safina). Each crop is set by where its
subject sits:

- `storm-portrait` has the ship and lamp in its top quarter. It's anchored
  to the top and pushed right, and the window peeks less on portrait screens
  (`--peek`: 30svh on phones, 36svh on portrait tablets), so the ship clears
  the headline and the red `0/2` stays in the peek.
- `storm-passing` is centred, with nothing over it: a held shot between
  acts, faded into the black on both sides.
- `first-light`'s subject sits low (the ship, its lamp, the dawn line), so
  it's cropped bottom-anchored, further right on phones, and the Get safina
  block rises only to the ship's waterline.

Check every crop at every viewport after a new take: a band that shows only
cloud has lost its shot.
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

The tagline is **"The Kubernetes TUI for teams that ship fast."** It is
the product's identity, not a stock phrase: safina is Arabic for ship, so
"teams that ship" reads both ways, the team that ships software and the
crew of a ship. "TUI" replaced "console" in Nov 2026 (the user's call): the
site speaks to engineers who know the genre, "TUI" can't be read as a web
console, and "TUI for teams" alliterates. "Fast" joined it later in Nov
2026 (the user's call again): speed is the claim safina leads with, and the
lede and Compare's measured rows give it numbers. "Terminal" stays in the meta
description ("A terminal UI for Kubernetes") and on the social card. It stands in the hero headline, the page title, the meta
description, the social card (`scripts/og.html`, then `scripts/og.sh`) and
its alt text (`Base.astro`). Never reword it, swap it for another line, or
cut it as marketing; every other line on the page can change, this one
stays.

Sound matters in the prose. Prefer the line that rhymes, alliterates or
scans ("Lowercase reads. Uppercase writes.") when it costs nothing in
meaning; never bend a fact to get the rhyme.

Lead with the benefit, in plain English: what the on-call engineer gets,
not how safina does it. Every feature has a name, short and easy to say,
and the name is what the app itself shows (the `:` palette says Logbook,
not History). Names: Barbarossa mode (the Shift keys that write),
Rollout watch (following a write until the pods are ready), No autopilot
(no cluster is ever picked for you), Logbook (the saved activity), and the
Also on board list (Shell anywhere, Forward list, Quick switch, Jump search,
Copy over SSH, Beacon). The AI feature is Chat, the name the app shows, and
the copy says "the model" and "an agent", never a vendor: other models may
follow. Name Claude only where the reader needs a fact about today, such as
the `claude` CLI that chat runs through and where it keeps conversations. A feature section puts its name over a headline
that says what it does for you (`<Feature name heading>`).

The nav follows Jakob's law: the words every product site uses, in page
order, so it makes sense before the page does. Deep dive (the tour, named
for the deep under the ship), Features (Chat to Also on board), Compare and
FAQ, then the Get safina button, which stays put from the first frame. Each
lights up in cyan while you're in its part (`aria-current`,
`src/scripts/nav.ts`), and each part shows the same word: the Deep dive
label under the tour window, the feature names, and the eyebrows over
Compare's and FAQ's titles. `copy-tells.mjs` fails the build's copy check
when a nav word has no match on the page. The hero holds no buttons: the
bar has both. On wide screens it ends on the install line instead, the
same command block as Get safina, as one block centred in the sky over the
window; phones and portrait screens leave it to the bar's button, so the
ship above the copy stays clear.

The name is Arabic, and the copy carries a few Arabic and Islamic touches,
kept subtle enough that a reader who has never met them loses nothing.
Three terms, each once, each glossed in the same sentence, never in a
heading and never at the start of a sentence: Barbarossa mode (after the
red-bearded Ottoman admiral; in the app its keys and confirmations are red,
and the site's write keys match), the amanah (the trust of the cluster, which stays with
you when Claude advises) and the isnad (the Logbook's chain: which cluster,
which session, when). Everywhere else the imagery lives in single English
words, never a metaphor carried through a sentence: pods weighed by CPU,
the night you export and the morning the Logbook is read in. The
photographs carry the storm and the calm water; the copy names them once,
in the footer. The tour is plain English. Hint, never explain: no stories,
no verse references in the copy.

Write it the way an engineer explains a tool to a colleague, not the way a
landing page does. Benchmarked against k9s, Aptakube, Ghostty, Atuin,
lazygit, Fly.io and incident.io, the lines that read as written by a person
share a few habits, and the ones that read as generated share others:

- Contractions: you're, it's, doesn't. "You have selected" reads stiff.
- Say the literal thing. "Every key that changes the cluster is a capital
  letter" beats any phrase about safety or confidence.
- Let the screen carry what it shows. Every feature has a real screenshot
  under its text, so the text says only what the picture can't: what
  Claude receives, the five-minute limit, what isn't recorded. It never
  narrates the screen below it. Keep the reason a feature exists, though:
  in a blind read, "That works without cluster access, which helps when
  the cluster was the problem" and "Then Rollout watch takes over" made
  the page clearer, and cutting them made it worse. Cut description, not
  reasons. Two paragraphs and a quiet note is the most a feature takes.
- Numbers and names over adjectives: an hour of events, five minutes of
  Rollout watch, `e` for events, ledger-api.
- Say what safina doesn't do ("It isn't an audit log of the cluster").
  Admitted limits are what make the other claims believable.
- One dry line per section at most, and it has to be true: "Usually this
  is where Kubernetes explains itself."
- Avoid the generated tells: verbs in threes ("find, see, fix"), mirrored
  negations ("Nothing to paste, nothing to explain twice"), a colon that
  sets up a reveal, an aphorism to close a paragraph ("The right command
  on the wrong cluster is how a lot of bad nights start"), a punch
  fragment after a sentence ("No scanning for red."), and stock phrases
  such as "built for", "without leaving the keyboard", "seamless",
  "supercharged". A real list of three different facts is fine; three
  synonyms for one idea is not.
- No gracious concessions and no slogans standing in for facts. "k9s
  does more and has years of hard use behind it. If it works for you,
  keep it." flatters to look fair, and "aimed at the bad night" is a
  slogan where a fact belongs. Answer with what safina actually does
  (capital-letter writes, rollouts watched until ready), and stop.

Fair questions (`Questions.astro`) answers what an engineer asks before
pointing a new tool at production, limits included ("Safina is
smaller"). Every answer there is a claim about the code: retention days,
what reaches the model, where files live. Recheck them when that code changes.
The nav calls it FAQ, the eyebrow over its title; each question is an h3
in the title voice, so a scan down the list reads the questions alone.

Safina and k9s (`Compare.astro`) is one table in two groups, no words in
the cells: nobody reads a comparison, they scan it. Measured comes first,
with figures from `cmd/safina-versus` (`make versus`): medians, the side
that's ahead beyond the noise in the brightest ink, ties and k9s's wins
(memory, idle API requests) kept in, and the run named in the note (versions,
cluster, machine). Re-run it on a release and change every row with the
note; never round a tie into a win. Features follows, checks and dashes. One sentence under
its title, the dated note below the table. Every k9s cell is checked against k9s's own README, k9scli.io
and its latest release, dated in the section's note (v0.51.0, June 2026);
recheck it when k9s ships, and keep k9s's wins in the table (read-only
mode, plugins, Windows). It replaces the "Why not just use k9s?" question;
the nav calls it Compare, the eyebrow over its title.

Mark a name safina gives its own behaviour with `<b>` and an Arabic term
with `<i>` (in a tour caption, `_word_`). Both render upright in the
brightest ink; `<b>` also takes the UI weight.
Quieter marks: the Logbook's export running "from the *bismillah* before
your first look to the *alhamdulillah* when the pods are ready" (said on
starting something and on its end, and the verse at the top of the page
source begins with the same bismillah; the sentence reads as start to
finish to anyone who doesn't know the words), "in sha Allah" on a promise
not yet kept (none on the page since v0.3.0 shipped the macOS binaries it
was on), the Hijri year in the footer, the 404 ("Overboard", and the belly of a whale), and the words Nuh
spoke boarding the ark at the top of the page source (`Base.astro`).

## Layout

One grid. Every section hangs off two verticals: the title line (the
column's left edge) and the text line, where a 5:6 split (`--split`, the
`.split` class) sets body text at about 56 characters. Titles sit on the
first, their text on the second, and anything that must line up with a
section's text (the "Write" keys, the install steps, the footer's story)
starts on the second too. The title column may carry a section's quiet
second voice under its title (the "Read" keys, the install facts, the
footer's name and licence, Compare's one sentence), so a closing weighs
the same on both sides. Only photographs and the tour window bleed past
the column.

Titles rank by size, so the page shows where the story ends and the
reference begins: the hero (`--t-mark`), a story beat (`--t-h2`: Chat,
Barbarossa mode, Logbook, Get safina), a reference section (`--t-h2-ref`,
`.h2-ref`: Also on board, Compare, FAQ) and an item inside one (`--t-h3`:
a question). A reference section's title column is sticky
(`.split__title`) side by side, so its title travels with a long list
instead of leaving an empty column behind it. Every key that plays
something sits in one tray (`.tray`): the tour's six and each feature's
recording key.

Side by side, a title and its text start level at cap height: `.split`
trims each column's first line with `text-box: trim-start cap alphabetic`.
Titles hang 0.05em into the margin so their stems meet the body text's.

`node scripts/grid.mjs` measures every edge on the page against these lines
at every viewport, in Chromium or Gecko, and reports near misses; the rules
it encodes are in `.claude/skills/reviewing-site-design`. Stacked, lists and
tables hold to the measure like running text, and a solid shape (the
wordmark's sail) stands on the line; only light marks hang.

Space steps by the golden ratio, `--s1` to `--s5` (15, 24, 39, 63, 102 at
desktop): s2 inside a group, s3 from a section's text to its figure, s4 to
a follow-on such as the key list, s5 between sections. Use these rather
than new numbers.

## Palette

Dark ocean, defined once in `src/styles/global.css`: the page is the abyss
(`--abyss`), and every fade, letterbox bar and shade dissolves into it. The
one accent is `--beacon`, the cyan of the app's own title bar
(`--tn-cyan`, from `internal/tui/theme/palette.go`), kept for what acts or
marks where you are: the wordmark, the feature names set like it, the
primary button, focus and the selected tour key. Its one counterpart is
`--danger`, the app's red (`--tn-red`), for the write keys only: in the app
those keys and every write confirmation are red (Barbarossa mode), so the
site's key list matches. The red beard is a pun, not a history claim: never
say the red itself is historical. Tints are relative colors of these tokens, never
raw channels.
