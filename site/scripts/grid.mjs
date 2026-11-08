#!/usr/bin/env node
// Grid audit: every edge on the page, measured against the grid's own lines.
//
// The page hangs off a few verticals (README §Layout): the title line (the
// column's left edge), the text line (the 5:6 split, side by side only),
// the column's right edge, and, stacked, the right edge of the measure.
// Titles hang 0.05em into the margin on purpose. Anything that starts or
// ends within a few pixels of one of those lines without sitting on it is a
// near miss: the eye reads it as a mistake, never as a choice.
//
// Edges are layout edges, not ink: a text run starts where its first line
// box starts (a Range's first rect), a box at its border edge. Positions are
// read with reduced motion, so scroll-driven work has settled.
//
//   node scripts/grid.mjs [url] [--engine chromium|gecko] [--only desktop,phone]
//
// Exit 1 if any near miss is found.

import { launch } from "./cdp.mjs";
import { launchGecko } from "./bidi.mjs";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const url = args.find((a) => /^https?:/.test(a)) ?? "http://127.0.0.1:4321/";
const engine = flag("--engine") ?? "chromium";
const VIEWPORTS = { wide: [1920, 1080], desktop: [1440, 900], stacked: [1024, 768], tablet: [820, 1180], phone: [390, 844], landscape: [844, 390] };
// --widths 600,960,1280 sweeps arbitrary widths (tiled windows land anywhere).
for (const w of flag("--widths")?.split(",") ?? []) VIEWPORTS[`w${w}`] = [Number(w), 900];
const only = flag("--only")?.split(",") ?? (flag("--widths") ? Object.keys(VIEWPORTS).filter((k) => k.startsWith("w")) : Object.keys(VIEWPORTS));

// How far from a line still counts as "meant to be on it", and how close
// counts as on it. Sub-pixel rounding sits well inside ON.
const NEAR = 24;
const ON = 0.75;

const AUDIT = `(() => {
  // Offscreen sections skip layout (content-visibility: auto); lay them out
  // so every edge is real. This doesn't move anything that's on screen.
  if (!document.getElementById("grid-audit")) {
    const st = document.createElement("style");
    st.id = "grid-audit";
    st.textContent = "*{content-visibility:visible!important}";
    document.head.append(st);
  }
  const ref = document.querySelector(".nav__inner, .wrap");
  const wrap = ref.getBoundingClientRect();
  const L1 = wrap.left, R = wrap.right;
  const split = document.querySelector("main .split");
  let L2 = null;
  if (split) {
    const cols = getComputedStyle(split).gridTemplateColumns.split(" ").filter((c) => c.endsWith("px")).map(parseFloat);
    if (cols.length > 1) L2 = split.getBoundingClientRect().left + cols[0] + parseFloat(getComputedStyle(split).columnGap);
  }
  const rem = parseFloat(getComputedStyle(document.documentElement).fontSize);
  const measure = 39 * rem;
  const lines = [{ name: "title line", x: L1, side: "left" }, { name: "column edge", x: R, side: "right" }];
  if (L2 !== null) lines.push({ name: "text line", x: L2, side: "left" });
  else lines.push({ name: "measure edge", x: L1 + measure, side: "right" });
  const where = (el) => {
    const s = el.closest("section[id], header, footer, main > div[id]");
    return s ? (s.id || s.tagName.toLowerCase()) : "page";
  };
  const name = (el) => el.tagName.toLowerCase() + (el.classList.length ? "." + [...el.classList].filter((c) => !c.startsWith("astro-")).join(".") : "");
  const visible = (el) => {
    const r = el.getBoundingClientRect();
    if (r.width < 2 || r.height < 2) return false;
    const cs = getComputedStyle(el);
    return cs.visibility !== "hidden" && cs.display !== "none" && cs.opacity !== "0" && !el.closest("[hidden], .visually-hidden");
  };
  const edges = [];
  const vw = innerWidth;
  const skip = (el) => el.closest(".frame__scroller, picture, svg, .band, .stage__media, .opening__photo, .lost__art") || el.matches("img, picture, source, br, wbr");
  const drawsBox = (el) => {
    const cs = getComputedStyle(el);
    return cs.backgroundColor !== "rgba(0, 0, 0, 0)" || parseFloat(cs.borderLeftWidth) > 0 || parseFloat(cs.borderRightWidth) > 0 || cs.boxShadow !== "none" || cs.backgroundImage !== "none";
  };
  const bleeds = (r) => r.left <= 0.5 || r.right >= vw - 0.5;
  // Inside a drawn box (a tray, a code block, a button) things align to that
  // box, not to the page: only the box itself answers to the grid.
  const insideDrawn = (el) => {
    for (let a = el.parentElement; a && a !== document.body; a = a.parentElement) {
      if (a.matches("section, header, footer, main, .lost")) return false;
      if (drawsBox(a) && !bleeds(a.getBoundingClientRect())) return true;
      if (a.matches("button, .btn")) return true;
    }
    return false;
  };
  // Where the element's first visible line starts: the first text, its own
  // or inside an inline child (a <b> name opening a sentence), never inside a
  // block child (that one answers for itself).
  const firstText = (el) => {
    const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT, {
      acceptNode: (n) => {
        if (!n.textContent.trim()) return NodeFilter.FILTER_REJECT;
        for (let a = n.parentElement; a && a !== el; a = a.parentElement) {
          if (a.matches(".visually-hidden") || getComputedStyle(a).display !== "inline") return NodeFilter.FILTER_REJECT;
        }
        return NodeFilter.FILTER_ACCEPT;
      },
    });
    for (let n = walk.nextNode(); n; n = walk.nextNode()) {
      const range = document.createRange();
      range.selectNodeContents(n);
      const rects = [...range.getClientRects()].filter((q) => q.width > 0.5);
      if (rects.length) return rects.reduce((a, b) => (b.top < a.top - 1 || (Math.abs(b.top - a.top) <= 1 && b.left < a.left) ? b : a));
    }
    return null;
  };
  for (const el of document.querySelectorAll("header *, main *, footer *, .lost *")) {
    if (skip(el) || !visible(el) || insideDrawn(el)) continue;
    const cs = getComputedStyle(el);
    const block = !cs.display.startsWith("inline") || cs.display === "inline-block" || cs.display === "inline-flex";
    const r = el.getBoundingClientRect();
    // Optical hangs are declared with a small negative inline-start margin
    // (titles 0.05em, the headline its T's bearing): measure the line the
    // element stands on before its hang.
    const ml = parseFloat(cs.marginLeft);
    const hang = ml < 0 && ml >= -0.1 * parseFloat(cs.fontSize) ? ml : 0;
    // Boxes that draw something answer with their edges; full-bleed ones
    // (photographs' bands, section grounds) are meant to cross the column.
    // Keycaps sit in their line of text or their row; the text answers.
    if (el.matches("kbd")) continue;
    if ((drawsBox(el) || el.matches(".frame, table, pre, button, .btn")) && !bleeds(r)) {
      edges.push({ where: where(el), el: name(el), kind: "box", side: "left", x: r.left, hang: 0, text: "" });
      edges.push({ where: where(el), el: name(el), kind: "box", side: "right", x: r.right, hang: 0, text: "" });
    }
    // The wordmark is a lockup: where its mark hangs in the margin, the name
    // answers to the title line; where it doesn't, the mark does.
    if (el.matches(".wordmark")) {
      const hung = parseFloat(cs.marginLeft) < 0;
      const t = firstText(el);
      if (hung && t) edges.push({ where: where(el), el: name(el), kind: "text", side: "left", x: t.left, hang: 0, text: "safina" });
      if (!hung) edges.push({ where: where(el), el: name(el), kind: "box", side: "left", x: r.left, hang: 0, text: "" });
      continue;
    }
    // Text: where its first visible line starts.
    // Text inside a drawn box (a keycap, a code block) answers through the box.
    if (drawsBox(el) || el.matches("pre")) continue;
    const firstChild = [...el.childNodes].find((n) => (n.nodeType === 3 && n.textContent.trim()) || n.nodeType === 1);
    if (firstChild?.nodeType === 1 && drawsBox(firstChild) && getComputedStyle(firstChild).display.startsWith("inline")) {
      edges.push({ where: where(el), el: name(el), kind: "box", side: "left", x: firstChild.getBoundingClientRect().left, hang: 0, text: firstChild.textContent.trim().slice(0, 20) });
      continue;
    }
    const ownText = [...el.childNodes].some((n) => (n.nodeType === 3 && n.textContent.trim()) || (n.nodeType === 1 && getComputedStyle(n).display === "inline" && n.textContent.trim()));
    const t = block && ownText && !el.matches("button, .btn") ? firstText(el) : null;
    if (t) edges.push({ where: where(el), el: name(el), kind: "text", side: "left", x: t.left, hang, text: el.textContent.trim().replace(/\\s+/g, " ").slice(0, 40) });
  }
  // Side by side, a title and its text start level at cap height (README
  // §Layout): the first child of each column of a .split shares its top.
  const level = [];
  if (L2 !== null) {
    for (const s of document.querySelectorAll(".split")) {
      if (!visible(s)) continue;
      const kids = [...s.children].filter(visible);
      const a = kids.find((k) => k.getBoundingClientRect().left < L2 - 1);
      const b = kids.find((k) => Math.abs(k.getBoundingClientRect().left - L2) < 2);
      if (a && b) {
        const d = b.getBoundingClientRect().top - a.getBoundingClientRect().top;
        if (Math.abs(d) > ${ON}) level.push({ where: where(s), a: name(a), b: name(b), d: Math.round(d * 10) / 10 });
      }
    }
  }
  const misses = [];
  for (const e of edges) {
    let best = null;
    for (const l of lines) {
      if (l.side !== e.side) continue;
      const d = e.x - (l.x + (l.side === "left" ? e.hang : 0));
      if (!best || Math.abs(d) < Math.abs(best.d)) best = { line: l.name, d };
    }
    if (best && Math.abs(best.d) > ${ON} && Math.abs(best.d) <= ${NEAR}) misses.push({ ...e, line: best.line, d: Math.round(best.d * 10) / 10 });
  }
  // Two lines a hair apart that should be one: left edges used by two or
  // more elements each, within a few pixels of each other (secondary grids:
  // list columns, table columns, footer columns).
  const counts = new Map();
  for (const e of edges) {
    if (e.side !== "left") continue;
    const x = Math.round((e.x - e.hang) * 2) / 2;
    if (!counts.has(x)) counts.set(x, new Set());
    counts.get(x).add(e.where + " " + e.el);
  }
  const xs = [...counts.keys()].filter((x) => counts.get(x).size >= 2).sort((a, b) => a - b);
  const pairs = [];
  for (let i = 1; i < xs.length; i++) {
    const d = xs[i] - xs[i - 1];
    if (d > ${ON} && d <= 6) pairs.push({ a: xs[i - 1], b: xs[i], d, ea: [...counts.get(xs[i - 1])].slice(0, 3), eb: [...counts.get(xs[i])].slice(0, 3) });
  }
  // Every left edge in use, grouped: the page's real verticals.
  const used = [...counts.entries()].sort((a, b) => a[0] - b[0]).map(([x, s]) => ({ x, n: s.size, who: [...s].slice(0, 4) }));
  return JSON.stringify({ lines: lines.map((l) => [l.name, Math.round(l.x * 10) / 10]), misses, level, pairs, used });
})()`;

async function audit(page, name) {
  // Scroll through once so lazy content has its size, then back to the top.
  await page.eval(`(async () => { for (let y = 0; y < document.documentElement.scrollHeight; y += innerHeight) { scrollTo(0, y); await new Promise((r) => requestAnimationFrame(r)); } scrollTo(0, 0); })()`);
  await new Promise((r) => setTimeout(r, 400));
  const { lines, misses, level, pairs, used } = JSON.parse(await page.eval(AUDIT));
  if (args.includes("--lines")) {
    const named = new Map(lines.map(([n, x]) => [Math.round(x * 2) / 2, n]));
    console.log(`\n${engine} ${name}: every left edge in use`);
    for (const u of used) console.log(`  ${String(u.x).padStart(7)}  ${String(u.n).padStart(3)}  ${(named.get(u.x) ?? "").padEnd(11)} ${u.who.join(", ")}`);
  }
  console.log(`\n${engine} ${name}  ${lines.map(([n, x]) => `${n} ${x}`).join(" · ")}`);
  const seen = new Set();
  for (const m of misses) {
    const key = `${m.where}|${m.el}|${m.side}|${m.d}`;
    if (seen.has(key)) continue;
    seen.add(key);
    console.log(`  ${m.where.padEnd(10)} ${m.el.slice(0, 34).padEnd(34)} ${m.kind.padEnd(4)} ${m.side.padEnd(5)} ${String(m.d).padStart(6)}px off the ${m.line}${m.text ? `  “${m.text}”` : ""}`);
  }
  for (const l of level) console.log(`  ${l.where.padEnd(10)} ${l.b} starts ${l.d}px below ${l.a}: side by side they start level`);
  for (const p of pairs) console.log(`  two lines ${p.d}px apart at ${p.a} and ${p.b}: ${p.ea.join(", ")} | ${p.eb.join(", ")}`);
  if (!misses.length && !level.length && !pairs.length) console.log("  on the grid");
  return misses.length + level.length + pairs.length;
}

let total = 0;
if (engine === "gecko") {
  const g = await launchGecko({ reduced: true });
  for (const name of only) {
    const [width, height] = VIEWPORTS[name];
    const p = await g.page({ width, height });
    await p.goto(url);
    total += await audit(p, name);
    await p.close();
  }
  await g.close();
} else {
  const b = await launch();
  for (const name of only) {
    const [width, height] = VIEWPORTS[name];
    const p = await b.page();
    await p.send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: width < 900 && height > width });
    await p.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
    await p.goto(url);
    total += await audit(p, name);
    await p.close();
  }
  await b.close();
}
process.exit(total ? 1 : 0);
