---
name: screening-the-site
description: Screens the whole safina website as one film - story and act structure, continuity between screenshots, running time, a blind first-impression test, and a benchmark against leading developer-tool sites of 2026. Use before a launch or release, after adding, removing or reordering sections, after re-shooting screenshots, or when asked whether the site is competitive, convincing, or "feels like a film".
argument-hint: "[optional focus, e.g. continuity or benchmark]"
---

# Screening the site

A screening watches the whole cut at once, the way a stranger will. Per-change
review (`reviewing-site-design`) checks frames; this checks the story those
frames tell, and whether the site holds up next to the best of its field.
`site/AGENTS.md` is the standard: the direction, the acts, the continuity
notes and the 2026 bar.

Copy this checklist and work through it:

```
- [ ] 1 Shoot the cut
- [ ] 2 Story map
- [ ] 3 Continuity table
- [ ] 4 Running time
- [ ] 5 Blind reads (first screen, whole cut)
- [ ] 6 Benchmark
- [ ] 7 Report
```

## 1. Shoot the cut

```sh
pnpm build && pnpm preview       # preview in the background
node scripts/frames.mjs --label screening
node scripts/frames.mjs --label screening --engine gecko --only desktop,phone
```

View the desktop and phone storyboards for both engines.

## 2. Story map

For each section in page order, write down its act, what it sets up and what
it pays off. Flag:

- a section with neither a setup nor a payoff
- a setup that's never paid off, or a payoff with nothing set up
- two sections doing the same job
- a beat in the wrong act (a limit admitted before the claim it limits)

## 3. Continuity

Read every image in `src/assets/shots/` at full size, then the `-m` crops.
Build a table:

| shot | cluster | clock | in focus | state (ready counts) | implies |
| --- | --- | --- | --- | --- | --- |

Check it against AGENTS.md §Continuity and `scripts/demo-manifests.yaml`:

- one cluster
- names that exist in the manifests
- counts that agree between frames showing the same moment
- times on screen (log lines, the Logbook, event ages) that read night and
  move forward in page order
- the incident the tour sets up resolved on the page
- alt text matching its image, counts and names included

Update the "Known breaks" list in AGENTS.md: add what you find and delete what
has been fixed.

## 4. Running time

From `.frames/screening/metrics.json`, take screens per viewport and screens
to `#install`, and compare them with AGENTS.md rule 13. A longer cut needs a
reason; update the numbers there if the user accepts the change.

## 5. Blind reads

Your eye is anchored by now. Use fresh general-purpose agents that see only
images, with no brief, no AGENTS.md and no hint of what you expect. Launch
both in one message so they run in parallel.

**First screen.** Give the paths of `desktop/00-open.png` and
`phone/00-open.png`:

> These are the first screens of a website, on desktop and on a phone. In
> two sentences each, say what this product is, who it's for, and what you'd
> click next. Then say what, if anything, made you trust or distrust it.

The intended answer: a Kubernetes tool that runs in the terminal, for
whoever's on call, with next steps Get safina or the tour. Any miss is a
Blocker.

**Whole cut.** Give the paths of the desktop and phone storyboards:

> These sheets are a website in scroll order, one frame per stop. Where did
> your attention drop? What didn't you believe? What would you cut, and what
> did you want that wasn't there? Quote the frame names.

Triage what the readers say before it becomes a finding:

- **Tool artifacts** aren't findings: the see-through nav in headless Gecko
  frames (no `backdrop-filter` there), and anything that appears in one
  engine only until you've checked the other.
- **Choices the README records** (the Arabic touches, Barbarossa, the nav
  hidden on phones, the tour window's break-out) go to the user as reactions,
  not defects.
- **Everything else** is a finding. Where two readers, or a reader and the
  continuity table, point at the same frame, rank it first.

## 6. Benchmark

Pick two direct and three craft references from AGENTS.md §The 2026 bar,
choosing the ones closest to what changed. Shoot each:

```sh
node scripts/frames.mjs https://k9scli.io --first --only desktop,phone
```

Score safina and each reference from 1 to 5 on:

| | What it is, in five seconds | Real product on the first screen | Path to install | Proof that checks out | Craft (type, grid, light) | Motion with a reason | First-load KB |
| --- | --- | --- | --- | --- | --- | --- | --- |

Back every score with a frame or a number. Take principles from them, never
their copy, layout signatures or assets. Their first-load KB is a lower bound:
files from other origins without `Timing-Allow-Origin` report 0 bytes.

## 7. Report

1. A verdict in one paragraph.
2. The three moves with the most impact for their effort, each tied to its
   evidence: a frame path, a metric, or a quote from a blind reader.
3. The continuity table and the benchmark table.
4. AGENTS.md updates made (known breaks, numbers).

Moves that change structure, names or the photographs need the user's yes
before anyone acts on them.
