#!/usr/bin/env node
// Reads the site's copy as a visitor (and a screen reader) gets it, from the
// built page, and points at the habits that make text read as generated.
// It finds candidates; a person, or the writing-site-copy skill, judges them.
//
//   node scripts/copy-tells.mjs [--text] [page ...]
//
//   --text plain copy in page order, section by section, no analysis: the
//          input for a blind read
//
//   page   a built HTML file or a URL (default: dist/index.html dist/404.html;
//          run `pnpm build` first so it reads what you just wrote)
//
// Three levels:
//   HARD   identity breaks: the tagline missing from one of its places, or
//          a nav item whose part of the page doesn't show its word. Exit
//          status 1.
//   FLAG   a likely tell. Fix it, or be able to say why it stays.
//   CHECK  fine when the content earns it (three real facts, a short true
//          sentence). Confirm each one.

import { readFileSync, existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { launch } from "./cdp.mjs";

const site = join(dirname(fileURLToPath(import.meta.url)), "..");
const TAGLINE = "The Kubernetes TUI for teams that ship fast.";
const SHIP = /for teams that ship fast/i;

const textOnly = process.argv.includes("--text");
const pages = process.argv.slice(2).filter((a) => a !== "--text");
if (pages.length === 0) pages.push(join(site, "dist/index.html"), join(site, "dist/404.html"));

const EXTRACT = `(() => {
  const clean = (s) => (s ?? "").replace(/[\\u2060\\u00ad]/g, "").replace(/_([^_]+)_/g, "$1").replace(/\\s+/g, " ").trim();
  const where = (el) => el.closest?.("section[id]")?.id ?? (el.closest?.("footer") ? "footer" : el.closest?.("header") ? "nav" : el.closest?.(".opening") ? "opening" : el === document.head ? "head" : "page");
  // Text as read aloud: decorative (aria-hidden) parts dropped, and a space
  // between elements that sit side by side with no text between them.
  const textOf = (el) => {
    const c = el.cloneNode(true);
    // Code blocks (config, commands) aren't prose: a YAML "- item" isn't a dash.
    c.querySelectorAll('[aria-hidden="true"], pre').forEach((n) => n.replaceWith(" "));
    c.querySelectorAll("*").forEach((n) => { if (n.nextSibling?.nodeType === 1) n.after(" "); });
    return c.textContent;
  };
  const blocks = [];
  const seen = new Set();
  const add = (kind, el, text) => {
    text = clean(text);
    if (!text || seen.has(text)) return;
    seen.add(text);
    blocks.push({ kind, section: where(el), text });
  };
  add("title", document.head, document.title);
  const meta = (sel) => document.querySelector(sel)?.getAttribute("content");
  add("description", document.head, meta('meta[name="description"]'));
  add("og-alt", document.head, meta('meta[property="og:image:alt"]'));
  // One pass in document order, so captions and alt text sit in their sections.
  for (const el of document.body.querySelectorAll("h1, h2, h3, p, li, dt, dd, th, figcaption, button, a, img[alt]")) {
    if (el.closest("pre, script, style, template")) continue;
    if (el.matches("img")) { add("alt", el, el.alt); continue; }
    if (el.matches("a") && el.closest("p, li, dd, dt, h1, h2, h3, th")) continue;
    if (el.querySelector("p, li, dd, dt, h1, h2, h3")) continue;
    add(el.matches(".section-name") ? "name" : el.tagName.toLowerCase(), el, textOf(el));
    if (el.dataset.caption) add("caption", el, el.dataset.caption);
  }
  const terms = [...document.body.querySelectorAll("i")].map((i) => {
    const block = i.closest("p, li, dd, dt, h1, h2, h3") ?? i.parentElement;
    const full = block.textContent;
    const before = clean(full.slice(0, full.indexOf(i.textContent)));
    return {
      text: clean(i.textContent),
      section: where(i),
      heading: Boolean(i.closest("h1, h2, h3")),
      opensSentence: before === "" || /[.!?][”’")]?$/.test(before),
    };
  });
  // An inline element run into the word beside it ("Your<code>claude</code>"):
  // Astro drops whitespace at a source line break before an element, and the
  // source still looks right. Only the rendered text shows it.
  const glued = [];
  for (const el of document.body.querySelectorAll("code, kbd, b, i, a, strong, em, span")) {
    if (el.closest("pre, script, style") || el.matches(".visually-hidden") || !el.textContent.trim()) continue;
    const before = el.previousSibling, after = el.nextSibling;
    const left = before?.nodeType === 3 && /[\\p{L}\\d]$/u.test(before.textContent) && /^[\\p{L}\\d]/u.test(el.textContent);
    const right = after?.nodeType === 3 && /^[\\p{L}\\d]/u.test(after.textContent) && /[\\p{L}\\d]$/u.test(el.textContent);
    if (left || right) {
      const block = el.closest("p, li, dd, dt, h1, h2, h3, th, td") ?? el.parentElement;
      glued.push({ section: where(el), text: clean((left ? before.textContent.split(/\\s/).pop() : "") + el.textContent + (right ? after.textContent.split(/\\s/)[0] : "")), block: clean(block.textContent).slice(0, 120) });
    }
  }
  // Each nav item and the part of the page it lights up for: the part must
  // exist and show the item's word, as its eyebrow (.section-name), its
  // title, another link to it (the hero's button for the deep dive), or a
  // group of named sections (Features).
  const nav = [...document.querySelectorAll("header nav a[href^='#']:not(.wordmark)")].map((a) => {
    const label = clean(a.textContent);
    const id = a.hash.slice(1);
    const t = document.getElementById(id);
    const own = t?.matches("section") ? t.querySelector(".section-name") : null;
    const title = t?.querySelector("h2:not(.visually-hidden)");
    const echoed = [...document.querySelectorAll("main a")].some((o) => o.hash === "#" + id && clean(o.textContent).toLowerCase().includes(label.toLowerCase()));
    const group = Boolean(t && !t.matches("section") && t.querySelectorAll(".section-name").length > 1);
    return { label, id, found: Boolean(t), own: own ? clean(own.textContent) : null, title: title ? clean(title.textContent) : null, echoed, group };
  });
  const names = [...document.querySelectorAll(".section-name")].map((n) => clean(n.textContent));
  const h1 = clean(document.querySelector("h1")?.textContent);
  return { blocks, terms, glued, nav, names, h1, home: Boolean(document.querySelector(".opening")) };
})()`;

// ─── what to look for ────────────────────────────────────────────────────────
const PROSE = new Set(["p", "li", "dd", "caption", "description"]);
const HEADINGS = new Set(["h1", "h2", "h3", "name"]);

const STOCK = new RegExp(
  "\\b(" +
    [
      "seamless(ly)?", "effortless(ly)?", "supercharg\\w*", "leverag\\w*", "unlock\\w*", "empower\\w*", "elevat\\w*",
      "delv\\w*", "robust", "cutting[- ]edge", "next[- ]gen\\w*", "game[- ]chang\\w*", "revolution\\w*", "streamlin\\w*",
      "harness\\w*", "unleash\\w*", "world[- ]class", "best[- ]in[- ]class", "state[- ]of[- ]the[- ]art", "blazing(ly)?",
      "lightning[- ]fast", "powerful", "intuitive", "at your fingertips", "peace of mind", "built for", "designed (to|for)",
      "without leaving", "out of the box", "first[- ]class", "heavy lifting", "guesswork", "say goodbye", "look no further",
      "whether you['’]re", "in today['’]s", "the future of", "reimagin\\w*", "single pane of glass", "mission[- ]critical",
      // No "deep dive": it's the tour's name (Nav.astro), the user's on-brand
      // pick (the deep under the ship), not the boardroom phrase.
      "crafted", "holistic", "synerg\\w*", "tapestry", "testament", "journey", "realm", "dive (in|into)",
      "worry[- ]free", "hassle[- ]free", "all[- ]in[- ]one", "one[- ]stop", "magic(al)?", "just works", "best part",
      "here['’]s the thing", "level up", "actionable", "insights?", "visibility into", "boost\\w*", "ultimate",
      "unparalleled", "comprehensive", "game plan", "stay in the loop", "a breeze", "in seconds", "with ease",
      "focus on what matters", "the right tool", "at scale", "end[- ]to[- ]end", "frictionless", "delight\\w*",
      "superpower\\w*", "on steroids", "rest easy", "sleep (better|soundly)",
    ].join("|") +
    ")\\b",
  "i",
);
const INTENSIFIER = /\b(very|really|truly|incredibly|extremely|super|deeply|highly|absolutely|literally|simply|basically|essentially|totally)\b/i;
const HEDGE = /\b(helps? you|allows? you|enables? you|is designed to|aims to|strives to|can help|lets you easily|makes it easy)\b/i;
const UNCONTRACTED = /\b(do not|does not|did not|is not|are not|was not|it is|you are|cannot|will not|you will|that is|we are|you have selected)\b/i;
const OPENER = /^(so|and so|ultimately|in short|simply put|the result|the best part|here['’]s|imagine|ready to|meet|say hello|introducing|think of it as|in other words|put simply|the bottom line|at the end of the day)\b/i;
const MIRRORED = /\b(no|nothing|never|without|zero)\b[^.;:!?]{1,40}[,;]\s*(no|nothing|never|without|zero)\b/i;
const CONTRAST = /\b(isn['’]t|aren['’]t|not)\s+(just|only|merely)\b|\bmore than (just )?a\b|\bit['’]s not\b[^.]*,\s*it['’]s\b|\bnot because\b[^.]*,\s*but\b/i;
const DASH = /\s[—–-]\s|—/;
// Aphorism shapes, not every sentence with "every" in it: safina's facts are
// often universal ("every write asks"), and those are the point.
const GENERAL = /\b(always|everyone|no one|that['’]s (why|how|what)|is how|is what|is where|is when|the (real|only) \w+ is|all you need)\b/i;
const TRIAD = /\b([\p{L}’'-]+),\s+([\p{L}’'-]+),?\s+(and|or)\s+(?!(?:from|to|the|a|an|of|in|on|for|with|when|what|which|that|it|you)\b)([\p{L}’'-]+)\b/u;
const PROPER = /^(Kubernetes|Claude|Safina|Logbook|Barbarossa|Rollout|Ask|Shift|Enter|Esc|Ctrl|SSH|API|RBAC|CRDs?|JSON|YAML|Go|macOS|Linux|Windows|GitHub|MIT|Secret|Allah|Ottoman|Nuh|Hijri|Also|Fair|Get)$/;

const sentencesOf = (t) =>
  t
    .split(/(?<=[.!?][”’")]?)\s+(?=[\p{Lu}\d“‘"(])/u)
    .map((s) => s.trim())
    .filter(Boolean);
const wordsOf = (t) => t.split(/\s+/).filter((w) => /[\p{L}\d]/u.test(w));

/** Each rule returns [sentence, note] pairs for one block. */
const RULES = [
  // FLAG: likely tells.
  ["FLAG", "all", (b, ss) => ss.flatMap((s) => (STOCK.exec(s) ? [[s, `stock phrase “${STOCK.exec(s)[0]}”`]] : []))],
  ["FLAG", "all", (b, ss) => ss.flatMap((s) => (DASH.test(s) ? [[s, "dash as a clause break (house style: commas and full stops)"]] : []))],
  ["FLAG", "all", (b, ss) => ss.flatMap((s) => (/['"]/.test(s) ? [[s, "straight quote or apostrophe (use ’ “ ”)"]] : []))],
  ["FLAG", "all", (b, ss) => ss.flatMap((s) => (s.includes("!") ? [[s, "exclamation mark"]] : []))],
  ["FLAG", "all", (b, ss) => ss.flatMap((s) => (MIRRORED.test(s) ? [[s, "mirrored negation (“no X, no Y”)"]] : []))],
  ["FLAG", "all", (b, ss) => ss.flatMap((s) => (CONTRAST.test(s) ? [[s, "contrast frame (“not just X”, “it’s not X, it’s Y”)"]] : []))],
  ["FLAG", PROSE, (b, ss) => ss.flatMap((s) => (OPENER.test(s) ? [[s, `stock opener “${OPENER.exec(s)[0]}”`]] : []))],
  ["FLAG", PROSE, (b, ss) => ss.flatMap((s) => (HEDGE.test(s) ? [[s, `hedge “${HEDGE.exec(s)[0]}”: say what it does`]] : []))],
  ["FLAG", PROSE, (b, ss) => ss.flatMap((s) => (INTENSIFIER.test(s) ? [[s, `intensifier “${INTENSIFIER.exec(s)[0]}”`]] : []))],
  ["FLAG", PROSE, (b, ss) => ss.flatMap((s) => (UNCONTRACTED.test(s) ? [[s, `uncontracted “${UNCONTRACTED.exec(s)[0]}” (house style: contractions)`]] : []))],
  ["FLAG", PROSE, (b, ss) => ss.flatMap((s) => (wordsOf(s).length > 32 ? [[s, `long sentence (${wordsOf(s).length} words): one breath?`]] : []))],
  ["FLAG", HEADINGS, (b) => (b.text.endsWith("?") && b.section !== "questions" ? [[b.text, "question as a heading"]] : [])],
  [
    "FLAG",
    new Set(["h1", "h2", "h3"]),
    (b) => {
      const caps = wordsOf(b.text).slice(1).filter((w) => /^\p{Lu}\p{Ll}{3,}/u.test(w) && !PROPER.test(w.replace(/[^\p{L}]/gu, "")));
      return caps.length >= 2 ? [[b.text, "Title Case heading (house style: sentence case)"]] : [];
    },
  ],
  // CHECK: fine when earned.
  ["CHECK", PROSE, (b, ss) => ss.flatMap((s) => (TRIAD.test(s) ? [[s, `three in a row “${TRIAD.exec(s)[0]}”: three facts, or one idea three ways?`]] : []))],
  ["CHECK", PROSE, (b, ss) => ss.flatMap((s) => (/[\p{L}\d)]:\s+\S+(\s+\S+){0,3}[.!]?$/u.test(s) ? [[s, "colon sets up a short ending: a list, or a reveal?"]] : []))],
  [
    "CHECK",
    PROSE,
    (b, ss) =>
      ss.flatMap((s, i) =>
        i > 0 && wordsOf(s).length <= 4 && wordsOf(ss[i - 1]).length >= 12 ? [[s, "short sentence after a long one: a fact, or a punchline?"]] : [],
      ),
  ],
  [
    "CHECK",
    PROSE,
    (b, ss) => (ss.length >= 2 && GENERAL.test(ss.at(-1)) ? [[ss.at(-1), "paragraph ends on a general claim: a fact, or an aphorism?"]] : []),
  ],
  [
    "CHECK",
    PROSE,
    (b, ss) => {
      if (ss.length < 3) return [];
      const n = ss.map((s) => wordsOf(s).length);
      const mean = n.reduce((a, c) => a + c, 0) / n.length;
      const cv = Math.sqrt(n.reduce((a, c) => a + (c - mean) ** 2, 0) / n.length) / mean;
      return cv < 0.2 ? [[b.text, `even rhythm (${n.join(", ")} words): vary a sentence`]] : [];
    },
  ],
  ["CHECK", PROSE, (b, ss) => ss.flatMap((s) => (/\b(we|our|us)\b/i.test(s) ? [[s, "who is “we”? The site talks to you about safina"]] : []))],
  [
    "CHECK",
    PROSE,
    (b, ss) =>
      ss.flatMap((s) =>
        /\b(users|customers|developers|organi[sz]ations|companies|enterprises)\b/i.test(s) ? [[s, "talks about a group: talk to the reader"]] : [],
      ),
  ],
];

// ─── run ─────────────────────────────────────────────────────────────────────
const clip = (s, n = 150) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);
const browser = await launch();
let hardFailures = 0;

try {
  for (const target of pages) {
    const isUrl = /^https?:/.test(target);
    if (!isUrl && !existsSync(target)) {
      console.error(`${target}: not found (run \`pnpm build\` first, or pass a URL)`);
      hardFailures++;
      continue;
    }
    const page = await browser.page();
    await page.goto(isUrl ? target : pathToFileURL(resolve(target)).href);
    const data = await page.eval(EXTRACT);
    await page.close();

    const all = data.blocks;
    if (textOnly) {
      let section = "";
      for (const b of all) {
        if (b.section !== section) console.log(`\n## ${(section = b.section)}`);
        console.log(PROSE.has(b.kind) && b.kind !== "description" ? b.text : `[${b.kind}] ${b.text}`);
      }
      continue;
    }
    const prose = all.filter((b) => PROSE.has(b.kind));
    const sentences = prose.flatMap((b) => sentencesOf(b.text));
    const lengths = sentences.map((s) => wordsOf(s).length).sort((a, b) => a - b);
    const words = all.reduce((n, b) => n + wordsOf(b.text).length, 0);
    const mean = lengths.length ? (lengths.reduce((a, c) => a + c, 0) / lengths.length).toFixed(1) : 0;
    console.log(`\n${target}`);
    console.log(
      `${all.length} blocks, ${words} words. Prose: ${sentences.length} sentences, ` +
        `mean ${mean} words, median ${lengths[Math.floor(lengths.length / 2)] ?? 0}, shortest ${lengths[0] ?? 0}, longest ${lengths.at(-1) ?? 0}.`,
    );

    // HARD
    const hard = [];
    if (data.home) {
      if (!data.h1.includes(TAGLINE)) hard.push(`h1 lacks the tagline “${TAGLINE}” (it reads “${data.h1}”)`);
      for (const kind of ["title", "description", "og-alt"]) {
        const b = all.find((x) => x.kind === kind);
        if (!b || !SHIP.test(b.text)) hard.push(`${kind} lacks “for teams that ship fast”`);
      }
      const og = join(site, "scripts/og.html");
      if (existsSync(og) && !/teams that ship fast/i.test(readFileSync(og, "utf8"))) hard.push("scripts/og.html (the social card) lacks “teams that ship fast”");
      for (const n of data.nav) {
        if (!n.found) hard.push(`nav “${n.label}” points at #${n.id}, which isn't on the page`);
        else if (n.own !== null ? n.own !== n.label : !(n.title === n.label || n.echoed || n.group))
          hard.push(`nav “${n.label}” lights up for #${n.id}, which shows ${n.own !== null ? `“${n.own}”` : "no matching word"}`);
      }
    }
    console.log(`\nHARD  ${hard.length ? "" : "none (tagline in title, h1, description, card and its alt; every nav item's part shows its word)"}`);
    for (const h of hard) console.log(`  ✗ ${h}`);
    hardFailures += hard.length;

    // FLAG and CHECK
    const hits = { FLAG: [], CHECK: [] };
    for (const b of all) {
      const ss = sentencesOf(b.text);
      for (const [level, kinds, rule] of RULES) {
        if (kinds !== "all" && !kinds.has(b.kind)) continue;
        for (const [s, note] of rule(b, ss)) hits[level].push(`  [${b.section} ${b.kind}] “${clip(s)}”\n      → ${note}`);
      }
    }
    // Foreign terms (<i>): each once, never in a heading, never opening a sentence.
    const count = new Map();
    for (const t of data.terms) count.set(t.text.toLowerCase(), (count.get(t.text.toLowerCase()) ?? 0) + 1);
    for (const t of data.terms) {
      if (t.heading) hits.FLAG.push(`  [${t.section} term] “${t.text}” → in a heading (terms live in running text)`);
      if (t.opensSentence) hits.FLAG.push(`  [${t.section} term] “${t.text}” → opens a sentence (gloss it mid-sentence)`);
    }
    for (const [term, n] of count) if (n > 1) hits.FLAG.push(`  [page term] “${term}” → used ${n} times (each term once)`);
    for (const g of data.glued) hits.FLAG.push(`  [${g.section} markup] “${g.text}” in “${g.block}…”\n      → no space between a word and the element beside it (keep them on one source line)`);

    for (const level of ["FLAG", "CHECK"]) {
      console.log(`\n${level}  ${hits[level].length || "none"}`);
      for (const h of hits[level]) console.log(h);
    }
    if (data.terms.length) console.log(`\nTerms in <i>: ${data.terms.map((t) => `${t.text} (${t.section})`).join(", ")}`);
  }
} finally {
  await browser.close();
}
process.exit(hardFailures ? 1 : 0);
