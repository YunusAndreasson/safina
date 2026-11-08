---
name: writing-site-copy
description: Writes, edits and reviews the visible text of the safina website (site/src - headings, feature paragraphs, tour captions, Also on board, Fair questions, alt text, meta description, social card) so it reads as written by a skilled technical writer who has run Kubernetes on call. Use when adding or changing any words on the site, naming a feature, writing alt text, or when asked whether copy sounds AI-written, generic, salesy or thin.
argument-hint: "[section or file to write or review]"
---

# Writing site copy

The bar: an engineer who has been paged at 3am reads a line and assumes a
colleague wrote it, someone who knows the cluster and the code. Two things get
you there: knowing more than the sentence says, and the craft below.

Binding before you start: the tagline rule (root `AGENTS.md`), README.md
§Voice (read it in full), and the direction in `site/AGENTS.md`, rule 3 above
all: the frame is cinematic, the words stay plain.

## 1. Know it before you write it

Precise copy comes from knowing more than the sentence says. Before drafting:

- **Find the code behind every claim** (`internal/tui`, `internal/history`,
  `internal/k8s`, `internal/broker`) and note each fact with `file:line`:
  limits, numbers, the names the app shows, what it refuses to do.
- **Look at the behaviour**: the screens in `src/assets/shots`, or drive
  safina yourself (`scripts/capture.sh`, README §Screenshots).
- **Know the world the line lands in**: [oncall-facts.md](oncall-facts.md).
  Readers who run Kubernetes trust a line that uses the field's terms exactly
  and knows where its soft edges are.
- **Write a facts sheet in the scratchpad first**: each claim, its source,
  and why it matters to someone on call. A sentence that can't point at a
  line on that sheet gets cut.

The facts sheet is where depth shows. "It never writes to your kubeconfig"
is a fact; knowing that `kubectl config use-context` rewrites the file every
other terminal reads is what tells you that fact is worth a sentence.

## 2. Draft

Craft habits, on top of §Voice:

- Answer the reader's question in the first clause. Usually it's "what
  happens when I press this" or "what can go wrong".
- Put subject and verb up front. The subject is safina, Claude, Kubernetes or
  you, never "users" or "teams".
- Use the specific case, not the category: `ledger-api`, `U`, five minutes,
  an hour of events.
- Vary sentence length on purpose. Read the paragraph aloud: where you'd
  breathe mid-sentence, split it; where sentences march at one length, change
  one.
- Earn each adjective. Most lines need none; numbers and names do the work.
- Keep the reason a feature exists; cut any description of its screenshot.
- Put the limit beside the claim. It's what makes the claim believable.
- Top and tail: delete the paragraph's first and last sentences. If it reads
  better, leave them deleted. Throat-clearing and summaries live there.
- Prefer sound (rhyme, alliteration, a line that scans) when it costs no
  meaning.

House typography (every component follows it):

- Curly quotes and apostrophes (’ “ ”). No em dashes and no spaced en
  dashes; commas and full stops.
- `&nbsp;` binds the last two or three words of a paragraph so it can't end
  on one word, and binds a number to its unit.
- `&#8288;` (word joiner) after a hyphen that must not break
  (`red-&#8288;bearded`).
- `<b>` for a name safina gives its own behaviour, `<i>` for an Arabic term
  (each once, glossed in the same sentence, never in a heading, never opening
  a sentence), `<kbd class="cap">` for a key, `<code>` for commands and paths.
  In tour captions, `_word_` marks a term.
- Headings in sentence case. Feature names are the names the app shows.
- Keep a word and the inline element after it on one source line. Astro drops
  the whitespace at a line break before an element, so `Your` at the end of
  one line and `<code>claude</code>` at the start of the next renders as
  "Yourclaude". The source looks right; `copy-tells.mjs` flags it.

## 3. Check

Copy this list into your notes and tick it off:

```
- [ ] Every claim maps to a facts-sheet line with its source
- [ ] pnpm build && node scripts/copy-tells.mjs: HARD clean, every FLAG fixed
      or given a reason, every CHECK confirmed
- [ ] Read aloud
- [ ] Blind read done and each point answered (below)
- [ ] node scripts/frames.mjs --only desktop,phone: no one-word last line,
      no bad break in the changed text
- [ ] README §Voice updated if a rule changed; Fair questions rechecked if
      the code behind them changed
```

`copy-tells.mjs` finds candidates; it doesn't judge. It skips code blocks
(`<pre>`), so config and commands are checked by reading them against the
code instead. A FLAG is usually right
(a stock phrase, a straight quote). A CHECK is often fine: three real facts in
a row are a list, not a tell.

### Blind read

You can't judge your own draft cold. Get a reader who has never seen the
brief:

1. `node scripts/copy-tells.mjs --text` prints the page's copy in page order.
   Take the sections you changed plus one you didn't touch, as a control.
2. Spawn a fresh general-purpose agent with only that text and this prompt.
   Give it no rules, no brief and no hint which section changed:

   > You're a senior technical editor who has also been on call for
   > production Kubernetes. Below is copy from a product website for a
   > terminal tool, section by section. Quote, don't rewrite: (1) every
   > sentence you'd cut, and why; (2) every sentence that reads as written by
   > a language model or a marketing team, naming the habit; (3) every claim
   > you'd want proof of before believing it; (4) anything you still don't
   > understand. If a section is good, say so in one line.

3. Triage every point before you touch the page:
   - **A claim it doubts**: check the code. If the reader is right, fix the
     claim or admit the limit beside it. Readers who know Kubernetes find the
     best fixes here; one found that the `claude` CLI keeps its own
     transcripts under `~/.claude`, which Fair questions now says.
   - **A choice README §Voice records** (the Arabic terms, Barbarossa, the
     one dry line, the tagline): keep it, and pass the objection to the user
     in your report. A blind reader doesn't know the brief, so it will object
     to the brief.
   - **Anything else it would cut or calls generated**: fix it, unless the
     facts sheet says the line carries a reason.

   If you need another read, spawn a new agent: the first one is anchored
   now.

## Examples

[examples.md](examples.md) has before-and-after pairs, most from this site's
own revisions, each with the reason. Read it before a first draft.
