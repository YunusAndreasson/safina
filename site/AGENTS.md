# Directing safina.run

For agents changing the site. The root `AGENTS.md` (the tagline) and this
folder's `README.md` (commands, voice, layout, palette) still bind; this file
adds the direction they serve and how to check work against it. If this file
and the README disagree, ask the user which one is right before changing
either.

## The brief

1. **Hold its own against the best developer-tool sites of 2026.** Someone
   arriving from Linear, Ghostty or Zed should find nothing here that looks
   cheaper or loads slower, and nothing vaguer to read.
2. **Feel like a film directed by Christopher Nolan.** His discipline, not his
   imagery: real things in front of the camera, one story told under pressure,
   structure doing the work, plain words against an enormous frame.

The influence stays behind the camera. The page never names Nolan, a film, a
character or a line, and never borrows a poster, a title design or a score
cue. A visitor should feel it and not be able to point at it.

## The direction, as rules

1. **Practical effects.** Every product image is real safina on the demo
   cluster (README §Screenshots). Never a mockup, a CSS-drawn terminal, a
   typing animation or a generated UI. Motion is real safina, recorded: every
   still has a recording of its moment, cut from one recording of the whole
   shoot and played in the renderer the stills are made with, so a scene
   opens and ends exactly on its still (README §Screenshots and recordings). The photographs (`src/assets/img/`) are the only
   generated images; they carry weather, never a claim.
2. **Cold open.** The first frame is already the night: storm, tagline, the
   product rising from below. Nothing comes before it, now or at launch: no
   announcement bar, badge, "Introducing", cookie banner, popup or modal.
3. **Enormous frame, plain words.** Scale lives in the photographs, the
   aspect ratio and the product window. The words stay an engineer explaining
   a tool (README §Voice). A line that would work as trailer voice-over is
   wrong here. This rule keeps the cinema out of the copy.
4. **One night, one incident.** The tour and the features follow a single
   incident (see Continuity). Keep it like a script supervisor: one cluster,
   the same names, counts that agree, clocks that move forward in page order.
5. **Setup and payoff.** Every section sets up something a later one pays
   off, or pays off something earlier: the red 0/2 in the first frame pays
   off in the diff, the Logbook's "2/2 ready" in the storm passing, the storm
   at first light. A section that does neither is cut, not polished.
6. **Aspect ratio tells the story.** The tall frame belongs to the opening
   alone and closes to 2.39:1 as the product rises. The tour window is the one
   break-out. Photographs bleed; nothing else does.
7. **Silence before the reveal.** Darkness and space come before the moments
   that matter: the hold before the window rises, the black floor under it.
   Don't fill a pause with ornament.
8. **Motivated light.** Every glow has a source: the terminal's blue, light
   from above as through water, the one amber mast lamp. A photograph's crop
   keeps its subject (the ship, the lamp, the dawn line) clear of fades and
   text at every width; a band that shows only cloud has lost its shot. The
   lamp is the only warm light on the page, so the UI never uses a warm
   colour. Wherever the product window appears, it's the brightest thing in
   the frame.
9. **A strict grade.** Tokens only (`src/styles/global.css`). Cyan `--beacon`
   for what acts or marks where you are, red `--danger` for writes and nothing
   else. No third accent, gradient text, glowing type or new font.
10. **Cut on action.** Motion needs a cause, a scroll or a keypress. A
    recording plays on its key or a click, never by itself. The one
    exception is the deep dive's reel (the user's call, Nov 2026, so a first
    look shows the window is alive): once its keys are on screen it plays
    its stops in order, each recording to its still and a beat on it, and
    stops for good at the visitor's first key or click. It pauses on hover,
    focus, off-screen or in a hidden tab, has a Pause button (WCAG 2.2.2),
    and never runs with reduced motion. The
    opening is the one orchestrated moment; everything else is a cut (160 to
    240 ms, `--ease-out`) or tied to scroll. No other autoplay, no idle
    loops, parallax for its own sake or text that types itself. Reduced motion, and browsers
    without scroll timelines, get the storyboard: every beat as a still that
    reads on its own.
11. **Trust the audience.** Text never explains the screenshot under it. The
    tour hands the visitor the camera (their own keys); keep it the
    centrepiece.
12. **Bookends.** The page opens on the ship in the storm and closes on the
    same ship at first light, then the footer's promise. Change one end and
    the other changes with it. Between them sits one interlude, the storm
    passing, cut in right after the Logbook shows the fix landed. The
    weather only moves forward, and the camera pulls back as it calms: close
    in the storm, medium as it passes, wide at first light. Three beats are a
    sequence; more would make a mood board, so the space between sections
    stays black.
13. **Fixed running time.** Measured by `scripts/frames.mjs` with every
    section laid out (Nov 2026, with the recordings' keys, the Metrics
    stop, the hero without buttons, the 160-column shoot, and Speed in
    place of the feature table with the reference lists across both
    lines, the user's call: 9.9 screens at 1440×900 with Install at 8.8;
    13.3 on a 390×844 phone; 9.9 and 13.6 in Gecko). Figures before Nov 2026 under-counted: off-screen features were
    measured at their 900px placeholder. A new section costs a cut or a merge
    somewhere else.
14. **Don't copy the mix.** Nolan lets the score bury the dialogue. Here words
    always win: body text at 4.5:1 or better, no grade that swallows a word,
    every screenshot readable on a phone (the `-m` crops).

## Structure

| Act | Sections, in order | Job |
| --- | --- | --- |
| I. Night falls | Opening, Deep dive (the tour) | The promise (tagline), then the incident seen: the red 0/2, `n` to it, the crash caught live, the back-off, the hot pods and their CPU |
| II. The watch | Chat, Barbarossa mode, Logbook | The diagnosis, the held breath (the red diff), what's still there in the morning |
| III. First light | Interlude, Speed, Also on board, Fair questions, Get safina, footer | The storm passing as the fix lands, the proof of the hero's speed claim, the cross-examination with limits admitted (k9s's wins in "Why not k9s?"), then calm water and the promise kept |

## Continuity

The cast lives in `scripts/demo-manifests.yaml`; the shoot order in README
§Screenshots and recordings. Cluster `eu-north-1`, 25 workloads, one session
from 10:59 to 11:17 UTC on 8 Nov 2026 in a 160×37 terminal, recorded whole; every still and
every recording on the page comes from it. The incident: `ledger-api` is 0/2 because its
`DATABASE_URL` names `ledger-db-primary`, a host that doesn't resolve. The
first frame rests on `ledger-db`, healthy two rows below, the name the diff
ends on. `n` jumps to `ledger-api`, Enter goes into its pods, and `l` catches
one crashing live (the shoot waits for its container to start): the failed
lookup and the panic, four seconds in. Its events show the back-off, Hot
pods puts `settlement-worker` on top and `m` shows its CPU over fifteen
minutes, chat reads those logs (attached from the Logbook) and says the
name is wrong and how to change it, the Barbarossa diff changes it to
`ledger-db`, Rollout watch follows the edit to "✓ edit complete" and 2/2,
and the Logbook closes on that edit completed at 2/2 ready, eight seconds
after it was requested. The fixture really looks the host up, so the fix really lands.
Around it: `search-indexer` 0/1 (Pending) and `settlement-worker` on top of
Hot pods. Alt text states what its image shows, counts and ages included,
so it changes with every re-shoot. The times on screen (log lines, the
Logbook, event ages) move forward in page order because the shoot does. On phones, `panStart` opens the
diff and Logbook crops at their payoff; re-measure it when a crop changes.
The photographs keep their own continuity: one ship, sailing toward screen
left in every shot, its one amber mast lamp lit in all of them. Attach the
existing shots as references when generating a new one
(`scripts/image-prompts.md`).

Known breaks, found by `screening-the-site`. Fix each on the next re-shoot or
once the user decides, then delete its line:

- **The first frame still reads against the lede.** Two blind readers saw
  the cursor on healthy `ledger-db` under "It jumps to what's broken"; the
  setup for the diff is invisible on a first look. Starting the tour on `n`
  (Next failing) would open on the jump itself. Structure, so the user's call.
- **The fix ends with the Inspector still saying Unavailable.** In the
  Nov 2026 build, the Deployment's Inspector header stays "Unavailable"
  after the row and the Logbook reach 2/2 ready; the recording's last frame
  shows both. An app bug, not the site's: re-shoot once it's fixed.

## The 2026 bar

Benchmark against the live sites, not memory: they change. Direct: k9s
(k9scli.io), Headlamp, Freelens, Aptakube. Craft: Linear, Ghostty, Zed, Warp,
Raycast, incident.io, Fly.io. Take principles from them, never their copy,
layout signatures or assets.

| What the best of them do | Here |
| --- | --- |
| Say what it is, for whom, with the real product on the first screen at 1440×900 and 390×844 | Yes: tagline, lede, the window's peek (`--peek`). Keep it. |
| One obvious next step | Get safina, the bar's one filled button, in place from the first frame (the hero has none; on wide screens it ends on the install line itself; the nav's words follow Jakob's law, README §Voice). A one-line install on Linux (since v0.1.0) and macOS (since v0.3.0) (`public/install.sh`, README §Releases). Still to come: a package manager, which also changes the FAQ's "Why not k9s?" |
| Let you try the product | The Deep dive, the keyboard tour |
| Proof that checks out | Today Speed (from `make versus`, reproducible from the repo) and Fair questions, both checked against code. The public repo, release notes and version (v0.3.0, Nov 2026). Never invented quotes, logos, user counts, stars or benchmarks |
| Load fast | Readable with no JavaScript, CLS 0, first load about 431 KB on desktop and 203 KB on a phone (Nov 2026, Chromium), before the 10 KB (gzipped) beacon of Cloudflare Web Analytics, which is cookieless and stores no personal data (the user's call, Nov 2026, in `src/layouts/Base.astro`). The recordings' player (about 114 KB of script, 80 KB of font and 38 KB of casts, gzipped) loads only once the visitor scrolls in or reaches for a key; the deep dive's reel starts only once its keys are on screen. Growth needs a reason the user agreed to |
| Accessible | WCAG 2.2 AA: contrast, focus, keyboard, alt text that says what each screen shows |
| Look current | Dated notes (k9s "as of v0.51.0") get rechecked; a changelog once there are releases |

## How to work here

1. **Read first**: the component, the README sections it touches, and for any
   claim, the Go code behind it.
2. **Shoot before you touch**: `pnpm build`, serve it (`pnpm preview`, in the
   background), then `node scripts/frames.mjs --label before`. Never stash,
   reset or check out over the working tree to get a "before"; it usually
   holds the user's uncommitted work.
3. **Name the beat**: say which act the change serves and what it sets up or
   pays off. For more than one section, or anything that touches structure,
   names or the tagline's surroundings, show the plan and wait for the user.
4. **Make the smallest change that does the job.** No new sections, effects,
   dependencies, fonts or colours unless asked.
5. **Check it**:
   - `pnpm verify`
   - `node scripts/frames.mjs --label after`, then the same with
     `--engine gecko --only desktop,phone`. The user browses in Zen (Gecko),
     so a Chromium pass alone proves nothing. Look at the desktop and phone
     storyboards and every frame the change touches.
   - `node scripts/copy-tells.mjs` after any change to words
   - `node scripts/grid.mjs` after any change to layout, in both engines
6. **Review with the project skills** (`.claude/skills/`):
   `writing-site-copy` for any words, `reviewing-site-design` for any visual
   change, `screening-the-site` before a release, after adding, removing or
   reordering a section, or after re-shooting screens.
7. **Keep the docs true**: the README when a rule changes, this file when the
   direction, the numbers or the continuity change.
8. **Pushing is publishing**: a push to `main` that touches `site/` goes live
   on safina.run (README §Deploy), so push only when the user asks. Never
   run `wrangler deploy` or `wrangler rollback` yourself: `pnpm ship` is the
   one way, and it guards the Worker name and the domain.

IMPORTANT: never fabricate anything a visitor could take as fact (screens,
numbers, quotes, logos, stars), and never report a check you didn't run.
