# Notes for agents working on safina

## The tagline is the identity

**"The Kubernetes TUI for teams that ship fast."**

Safina (سفينة) is Arabic for ship, so "teams that ship" reads both ways:
the team that ships software and the crew of a ship. That double meaning is
the product's core identity. It is not a stock marketing phrase, and it is
not "AI slop" to be cleaned up.

- Never reword it, replace it with another line, or cut it. Only the user
  changes it (twice so far, both in Nov 2026: "console" became "TUI", then
  "ship" became "ship fast", so the line carries safina's speed, which the
  hero's lede and the measured rows in Compare back with numbers).
- It appears in five places on the site, and all five must agree: the hero
  headline (`site/src/components/Opening.astro`), the page title and meta
  description (`site/src/pages/index.astro`), the social card
  (`site/scripts/og.html`, rendered to `site/public/og.jpg` by
  `site/scripts/og.sh`) and the card's alt text (`site/src/layouts/Base.astro`).
  The app's welcome screen carries it too (`internal/tui/sections/welcome.go`).
- "TUI" because the audience knows the genre (k9s, lazygit) and can't mistake
  it for a web console, and "TUI for teams" alliterates; "terminal" stays in
  the meta description ("A terminal UI for Kubernetes") and the social card.
- Other copy may change around it. This line stays.
- Sound matters in the prose: prefer a line that rhymes, alliterates or scans,
  as long as it costs nothing in meaning.

## Writing site copy

The full voice guide is the "Voice" section of `site/README.md`. In short:

- Let the screenshots carry what they show. Text never describes the
  screen below it, but keeps the reason each feature exists. Cut
  description, not reasons.
- Write the way an engineer explains a tool to a colleague. No gracious
  concessions ("If it works for you, keep it"), no slogans standing in for
  facts ("aimed at the bad night"), no punch fragments ("No scanning for
  red."), no aphorisms closing a paragraph.
