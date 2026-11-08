#!/usr/bin/env node
// Dailies: shoot the site the way a visitor sees it, beat by beat, at every
// viewport that matters, and measure what the page costs. Headless, nothing
// drawn on the display.
//
//   node scripts/frames.mjs [url] [options]
//
//   url              default http://127.0.0.1:4321/ (`pnpm build && pnpm preview`
//                    for real numbers: dev mode encodes images on demand)
//   --engine NAME    chromium (default) or gecko. Gecko runs Zen or Firefox
//                    over WebDriver BiDi: the engine the user browses with, so
//                    every review shoots it too. Chromium adds touch emulation,
//                    CLS and throttling.
//   --label NAME     write to .frames/NAME/ (default: a timestamp). Shoot
//                    `--label before` before you edit and `--label after` once
//                    you're done, then compare frame by frame.
//   --only LIST      comma-separated viewport names (see VIEWPORTS)
//   --first          the first screen only (use this for other sites)
//   --reduced        prefers-reduced-motion: reduce (the storyboard cut)
//   --throttle       Chromium only: 4x CPU and a slow 4G network while measuring
//   --dpr N          device pixel ratio of the frames (default 1)
//
// Writes, per viewport: <viewport>/NN-beat.png, one frame per beat (the
// opening's scroll beats, then each section as a nav link lands on it), and
// <viewport>-storyboard.png, every frame on one sheet. Gecko's folders and
// sheets are prefixed `gecko-`. metrics.json holds LCP, CLS, bytes,
// requests, page length and console errors.
//
// Headless timings on this machine are not a phone's timings. Use them to
// compare two cuts of the same page, not as absolute numbers. On other sites,
// cross-origin files without Timing-Allow-Origin report 0 bytes, so their KB
// is a lower bound. Headless Gecko doesn't render backdrop-filter, so a
// translucent bar looks see-through there; judge blur in Chromium or in the
// user's own Zen.

import { mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { launch } from "./cdp.mjs";
import { launchGecko } from "./bidi.mjs";

const site = join(dirname(fileURLToPath(import.meta.url)), "..");

const VIEWPORTS = [
  { name: "wide", width: 1920, height: 1080 },
  { name: "desktop", width: 1440, height: 900 },
  { name: "stacked", width: 1024, height: 768 },
  { name: "tablet", width: 820, height: 1180, mobile: true },
  { name: "phone", width: 390, height: 844, mobile: true },
  { name: "landscape", width: 844, height: 390, mobile: true },
];

// The opening holds the frame for 75svh of scroll (Opening.astro): copy out
// by 22svh, letterbox closed by 46svh, the product window risen by 75svh.
const OPENING_BEATS = [
  ["open", 0],
  ["copy-out", 0.23],
  ["scope", 0.46],
  ["rise", 0.75],
];

const args = process.argv.slice(2);
const flag = (name) => args.includes(`--${name}`);
const opt = (name) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : undefined;
};
const valued = new Set(["--label", "--only", "--dpr", "--engine"]);
const url = args.find((a, i) => !a.startsWith("--") && !valued.has(args[i - 1])) ?? "http://127.0.0.1:4321/";
const engine = opt("engine") ?? "chromium";
if (!["chromium", "gecko"].includes(engine)) {
  console.error(`--engine is chromium or gecko, not ${engine}`);
  process.exit(2);
}
const first = flag("first");
const reduced = flag("reduced");
const throttle = flag("throttle") && engine === "chromium";
const dpr = Number(opt("dpr") ?? 1);
const only = opt("only")?.split(",");
const label =
  opt("label") ?? (first ? new URL(url).hostname : new Date().toISOString().slice(0, 16).replace(/[:T]/g, "-"));
const out = join(site, ".frames", label);
const prefix = engine === "gecko" ? "gecko-" : "";
const viewports = only ? VIEWPORTS.filter((v) => only.includes(v.name)) : VIEWPORTS;
if (viewports.length === 0) {
  console.error(`no viewport matches --only ${only.join(",")}; known: ${VIEWPORTS.map((v) => v.name).join(", ")}`);
  process.exit(2);
}

// Collected from the first paint on, before any script of the page runs.
// Where an engine lacks an entry type (Gecko has no layout-shift), the
// number stays null rather than a reassuring zero.
const OBSERVE = `
  window.__frames = { lcp: 0, lcpEl: "", cls: null };
  const name = (el) => el ? el.tagName.toLowerCase() + (el.id ? "#" + el.id : "") + (typeof el.className === "string" && el.className.trim() ? "." + el.className.trim().split(/\\s+/).join(".") : "") : "";
  if (PerformanceObserver.supportedEntryTypes?.includes("largest-contentful-paint")) {
    new PerformanceObserver((l) => { for (const e of l.getEntries()) { __frames.lcp = e.startTime; __frames.lcpEl = name(e.element); } })
      .observe({ type: "largest-contentful-paint", buffered: true });
  }
  // An unsupported entry type doesn't throw, so ask first: a 0 must mean 0.
  if (PerformanceObserver.supportedEntryTypes?.includes("layout-shift")) {
    __frames.cls = 0;
    new PerformanceObserver((l) => { for (const e of l.getEntries()) if (!e.hadRecentInput) __frames.cls += e.value; })
      .observe({ type: "layout-shift", buffered: true });
  }
`;

// Wait until every image in view has loaded and decoded, then two frames
// so scroll-driven animations have caught up with the scroll position.
const SETTLE = `(async () => {
  const inView = [...document.images].filter((i) => {
    const r = i.getBoundingClientRect();
    return r.width > 0 && r.bottom > 0 && r.top < innerHeight;
  });
  await Promise.all(inView.map((i) => i.complete ? null : new Promise((r) => {
    i.addEventListener("load", r, { once: true });
    i.addEventListener("error", r, { once: true });
    setTimeout(r, 10000);
  })));
  await Promise.all(inView.map((i) => i.decode().catch(() => {})));
  await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
  await new Promise((r) => setTimeout(r, 150));
})()`;

const BYTES = `(() => {
  const nav = performance.getEntriesByType("navigation")[0];
  const res = performance.getEntriesByType("resource");
  const kb = (n) => Math.round(n / 1024);
  const sum = (list) => list.reduce((n, e) => n + (e.transferSize || 0), 0);
  return {
    totalKB: kb((nav?.transferSize || 0) + sum(res)),
    htmlKB: kb(nav?.transferSize || 0),
    scriptKB: kb(sum(res.filter((e) => e.initiatorType === "script"))),
    imgKB: kb(sum(res.filter((e) => e.initiatorType === "img"))),
    fontKB: kb(sum(res.filter((e) => /\\.(woff2?|ttf|otf)(\\?|$)/.test(e.name)))),
    requests: res.length + 1,
  };
})()`;

// ─── engines: one small page interface over CDP and over BiDi ───────────────
async function chromium() {
  const browser = await launch();
  return {
    name: "Chromium",
    async page(vp, preload) {
      const p = await browser.page();
      const errors = [];
      p.on("Runtime.exceptionThrown", (e) => errors.push(e.exceptionDetails?.exception?.description ?? e.exceptionDetails?.text));
      p.on("Runtime.consoleAPICalled", (e) => {
        if (e.type === "error") errors.push(e.args.map((a) => a.value ?? a.description).join(" "));
      });
      await p.send("Log.enable");
      p.on("Log.entryAdded", (e) => {
        if (e.entry.level === "error") errors.push(`${e.entry.text}${e.entry.url ? ` (${e.entry.url})` : ""}`);
      });
      await p.send("Emulation.setDeviceMetricsOverride", {
        width: vp.width,
        height: vp.height,
        deviceScaleFactor: vp.dpr ?? 1,
        mobile: Boolean(vp.mobile),
      });
      if (vp.mobile) await p.send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 5 });
      await p.send("Emulation.setEmulatedMedia", {
        features: [{ name: "prefers-reduced-motion", value: reduced ? "reduce" : "no-preference" }],
      });
      await p.send("Network.enable");
      await p.send("Network.setCacheDisabled", { cacheDisabled: true });
      if (throttle && vp.measure) {
        await p.send("Emulation.setCPUThrottlingRate", { rate: 4 });
        await p.send("Network.emulateNetworkConditions", {
          offline: false,
          latency: 150,
          downloadThroughput: (1.6 * 1024 * 1024) / 8,
          uploadThroughput: (750 * 1024) / 8,
        });
      }
      if (preload) await p.send("Page.addScriptToEvaluateOnNewDocument", { source: preload });
      return {
        errors,
        goto: (u) => p.goto(u),
        eval: (e) => p.eval(e),
        unthrottle: () => p.send("Emulation.setCPUThrottlingRate", { rate: 1 }),
        async shot({ full = false } = {}) {
          if (full) {
            const h = await p.eval("document.documentElement.scrollHeight");
            await p.send("Emulation.setDeviceMetricsOverride", { width: vp.width, height: h, deviceScaleFactor: 1, mobile: false });
          }
          return (await p.send("Page.captureScreenshot", { format: "png" })).data;
        },
        close: () => p.close(),
      };
    },
    close: () => browser.close(),
  };
}

// ─── shoot ───────────────────────────────────────────────────────────────────
const browser = engine === "gecko" ? await launchGecko({ reduced }) : await chromium();
const metricsFile = join(out, "metrics.json");
const metrics = existsSync(metricsFile) ? JSON.parse(readFileSync(metricsFile, "utf8")) : { url, label, viewports: {} };
metrics.viewports ??= {};
console.log(`${browser.name}, ${url}${reduced ? ", reduced motion" : ""}${throttle ? ", throttled" : ""}`);

try {
  for (const vp of viewports) {
    const key = prefix + vp.name;
    const dir = join(out, key);
    rmSync(dir, { recursive: true, force: true }); // no stale frames from an earlier take
    mkdirSync(dir, { recursive: true });
    const page = await browser.page({ ...vp, dpr, measure: true }, OBSERVE);

    try {
      await page.goto(url);
    } catch (e) {
      throw new Error(`${e.message}\nIs the site being served? \`pnpm build && pnpm preview\`, or pass a URL.`, { cause: e });
    }
    await page.eval("document.fonts.ready.then(() => new Promise((r) => setTimeout(r, 400)))");
    await page.eval(SETTLE);

    const initial = await page.eval(BYTES);
    const { lcp, lcpEl } = await page.eval("window.__frames");
    // Off-screen features hold a 900px placeholder (content-visibility: auto
    // in Feature.astro) until they first render, so measured as loaded the
    // length depends on which ones happened to be near the viewport. Lay
    // every section out for real, measure, then put the page back.
    const shape = await page.eval(`(async () => {
      const real = document.createElement("style");
      real.textContent = "*{content-visibility:visible!important}";
      document.head.append(real);
      await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      const out = {
        screens: Math.round(document.scrollingElement.scrollHeight / innerHeight * 10) / 10,
        overflowX: document.scrollingElement.scrollWidth - document.scrollingElement.clientWidth,
        toInstall: (() => { const el = document.getElementById("install"); return el ? Math.round((el.getBoundingClientRect().top + scrollY) / innerHeight * 10) / 10 : null; })(),
      };
      real.remove();
      return out;
    })()`);
    await page.unthrottle?.();

    const frames = [];
    const shoot = async (name) => {
      await page.eval(SETTLE);
      const file = join(dir, `${String(frames.length).padStart(2, "0")}-${name}.png`);
      writeFileSync(file, Buffer.from(await page.shot(), "base64"));
      frames.push({ name, file });
    };

    if (first) {
      await shoot("first");
    } else {
      const hasOpening = await page.eval("Boolean(document.querySelector('.opening'))");
      for (const [name, at] of hasOpening ? OPENING_BEATS : [["top", 0]]) {
        await page.eval(`scrollTo({ top: innerHeight * ${at}, behavior: "instant" })`);
        await shoot(name);
      }
      // Each section as a visitor arrives at it from a link (scroll-padding included).
      const ids = await page.eval(
        `[...document.querySelectorAll("main section[id], body > footer")].map((el) => el.id || (el.id = "frames-" + el.tagName.toLowerCase()))`,
      );
      for (const id of ids) {
        const el = `document.getElementById(${JSON.stringify(id)})`;
        await page.eval(`${el}.scrollIntoView({ block: "start", behavior: "instant" })`);
        const name = id.replace(/^frames-/, "");
        await shoot(name);
        // A section taller than the screen gets more frames, so nothing in
        // it (a key list, the tour's controls) goes unseen.
        for (let n = 2; n <= 5; n++) {
          if (!(await page.eval(`${el}.getBoundingClientRect().bottom > innerHeight + 48`))) break;
          await page.eval(`scrollBy({ top: Math.round(innerHeight * 0.8), behavior: "instant" })`);
          await shoot(`${name}-${n}`);
        }
      }
    }

    const after = await page.eval(BYTES);
    const { cls } = await page.eval("window.__frames");
    const errors = [...page.errors];
    await page.close();

    metrics.viewports[key] = {
      engine: browser.name,
      size: `${vp.width}x${vp.height}`,
      reduced,
      throttle,
      at: new Date().toISOString(),
      lcpMs: Math.round(lcp),
      lcpElement: lcpEl,
      cls: cls == null ? null : Math.round(cls * 1000) / 1000,
      initial,
      afterScrollKB: after.totalKB,
      ...shape,
      errors,
      frames: frames.map((f) => relative(out, f.file)),
    };

    // The storyboard: every frame of this viewport on one sheet.
    const thumb = vp.height > vp.width ? 200 : 440;
    const sheetFile = join(out, `${key}-storyboard.html`);
    writeFileSync(
      sheetFile,
      `<!doctype html><meta charset="utf-8"><style>
        body{margin:0;padding:20px;background:#000;color:#8494a8;font:13px system-ui;display:flex;flex-wrap:wrap;gap:16px}
        figure{margin:0;width:${thumb}px} img{width:100%;display:block;outline:1px solid #222} figcaption{padding-top:6px}
        h1{width:100%;margin:0 0 4px;font:600 15px system-ui;color:#eaf3fb}
      </style><h1>${label} · ${browser.name} · ${vp.name} ${vp.width}×${vp.height}${reduced ? " · reduced motion" : ""}</h1>
      ${frames.map((f) => `<figure><img src="${pathToFileURL(f.file).href}"><figcaption>${f.name}</figcaption></figure>`).join("")}`,
    );
    const sheet = await browser.page({ width: 1840, height: 900 });
    await sheet.goto(pathToFileURL(sheetFile).href);
    await sheet.eval(SETTLE);
    writeFileSync(join(out, `${key}-storyboard.png`), Buffer.from(await sheet.shot({ full: true }), "base64"));
    await sheet.close();

    const m = metrics.viewports[key];
    console.log(
      `${key.padEnd(15)} ${m.size.padEnd(9)}  LCP ${String(m.lcpMs).padStart(5)} ms  CLS ${m.cls == null ? "  n/a" : m.cls.toFixed(3)}  ` +
        `first load ${String(m.initial.totalKB).padStart(5)} KB (${m.initial.requests} req, JS ${m.initial.scriptKB} KB)  ` +
        `all ${String(m.afterScrollKB).padStart(5)} KB  ${m.screens} screens` +
        (m.toInstall != null ? `, install at ${m.toInstall}` : "") +
        (m.overflowX > 0 ? `  OVERFLOW-X ${m.overflowX}px` : "") +
        (m.errors.length ? `  ${m.errors.length} console error(s)` : ""),
    );
  }
} finally {
  await browser.close();
}

writeFileSync(metricsFile, JSON.stringify(metrics, null, 2));
console.log(`\nframes in ${relative(process.cwd(), out) || "."}/  (storyboards: ${prefix}<viewport>-storyboard.png, numbers: metrics.json)`);
