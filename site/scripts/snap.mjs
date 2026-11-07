#!/usr/bin/env node
// Render safina's current screen to a 2x PNG without touching the display.
//
// safina runs in a private tmux server (see capture.sh). We read the exact
// cell grid with `tmux capture-pane -e` (24-bit colour preserved), replay it
// into xterm.js in headless Chromium, and screenshot the terminal element over
// the DevTools protocol. Box drawing, blocks and braille come from xterm's
// built-in glyph renderer, so lines join perfectly at any size.
//
//   node scripts/snap.mjs <name>         -> src/assets/shots/<name>.png

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const site = join(here, "..");
const name = process.argv[2];
if (!name) {
  console.error("usage: snap.mjs <name>");
  process.exit(2);
}
const out = join(site, "src/assets/shots", `${name}.png`);

const tmux = (...a) => execFileSync("tmux", ["-L", "safina-capture", "-f", "/dev/null", ...a], { encoding: "utf8" });
const [cols, rows] = tmux("display", "-p", "-t", "demo", "#{pane_width} #{pane_height}").trim().split(" ").map(Number);
const ansi = tmux("capture-pane", "-e", "-p", "-t", "demo").replace(/\n$/, "").split("\n").join("\r\n");

const mod = (p) => pathToFileURL(join(site, "node_modules", p)).href;
const html = `<!doctype html><meta charset="utf-8">
<link rel="stylesheet" href="${mod("@xterm/xterm/css/xterm.css")}">
<style>
  html,body{margin:0;background:#1a1b26}
  #wrap{display:inline-block;padding:20px 22px 22px;background:#1a1b26}
  .xterm .xterm-viewport{overflow:hidden!important;background:#1a1b26!important}
</style>
<div id="wrap"><div id="t"></div></div>
<script src="${mod("@xterm/xterm/lib/xterm.js")}"></script>
<script src="${mod("@xterm/addon-webgl/lib/addon-webgl.js")}"></script>
<script>
window.render = async (data) => {
  const family = '"JetBrainsMono Nerd Font"';
  await Promise.all([document.fonts.load('17px ' + family), document.fonts.load('bold 17px ' + family), document.fonts.load('italic 17px ' + family)]);
  const term = new Terminal({
    cols: ${cols}, rows: ${rows}, fontFamily: family + ', monospace', fontSize: 17, lineHeight: 1.0,
    letterSpacing: 0, customGlyphs: true, rescaleOverlappingGlyphs: true, allowTransparency: false,
    drawBoldTextInBrightColors: false, minimumContrastRatio: 1, cursorBlink: false, cursorInactiveStyle: 'none',
    theme: {
      background: '#1a1b26', foreground: '#c0caf5', cursor: '#1a1b26', cursorAccent: '#1a1b26',
      black: '#15161e', red: '#f7768e', green: '#9ece6a', yellow: '#e0af68', blue: '#7aa2f7', magenta: '#bb9af7', cyan: '#7dcfff', white: '#a9b1d6',
      brightBlack: '#414868', brightRed: '#f7768e', brightGreen: '#9ece6a', brightYellow: '#e0af68', brightBlue: '#7aa2f7', brightMagenta: '#bb9af7', brightCyan: '#7dcfff', brightWhite: '#c0caf5',
    },
  });
  term.open(document.getElementById('t'));
  try { term.loadAddon(new WebglAddon.WebglAddon()); } catch (e) { console.warn('webgl unavailable', e); }
  await new Promise((r) => term.write('\\x1b[?25l' + data, r));
  await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
  const b = document.getElementById('wrap').getBoundingClientRect();
  return { x: b.x, y: b.y, width: b.width, height: b.height };
};
</script>`;

const dir = mkdtempSync(join(tmpdir(), "safina-snap-"));
const page = join(dir, "term.html");
writeFileSync(page, html);

// Minimal CDP client over Node's built-in WebSocket.
const chrome = spawn(
  "chromium",
  [
    "--headless=new",
    "--remote-debugging-port=0",
    `--user-data-dir=${join(dir, "profile")}`,
    "--allow-file-access-from-files",
    "--hide-scrollbars",
    "--force-device-scale-factor=2",
    "--window-size=1800,1200",
    "--use-angle=swiftshader",
    "--enable-unsafe-swiftshader",
    "about:blank",
  ],
  { stdio: ["ignore", "ignore", "pipe"] },
);
const wsUrl = await new Promise((resolve, reject) => {
  let buf = "";
  chrome.stderr.on("data", (d) => {
    buf += d;
    const m = buf.match(/DevTools listening on (ws:\/\/\S+)/);
    if (m) resolve(m[1]);
  });
  chrome.on("exit", () => reject(new Error("chromium exited early:\n" + buf)));
});

const browser = new WebSocket(wsUrl);
await new Promise((r) => browser.addEventListener("open", r, { once: true }));
let id = 0;
const pending = new Map();
browser.addEventListener("message", (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
  }
});
const send = (method, params = {}, sessionId) =>
  new Promise((resolve, reject) => {
    const msgId = ++id;
    pending.set(msgId, { resolve, reject });
    browser.send(JSON.stringify({ id: msgId, method, params, ...(sessionId && { sessionId }) }));
  });

try {
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  const s = (m, p) => send(m, p, sessionId);
  await s("Page.enable");
  await s("Page.navigate", { url: pathToFileURL(page).href });
  for (let i = 0; i < 100; i++) {
    const r = await s("Runtime.evaluate", { expression: "typeof window.render", returnByValue: true });
    if (r.result.value === "function") break;
    await new Promise((r) => setTimeout(r, 50));
  }
  const { result } = await s("Runtime.evaluate", {
    expression: `render(${JSON.stringify(ansi)})`,
    awaitPromise: true,
    returnByValue: true,
  });
  if (process.env.SNAP_DEBUG) { const d = await s("Runtime.evaluate", { expression: "JSON.stringify({dpr: devicePixelRatio, canv: [...document.querySelectorAll('canvas')].map(c => [c.width, c.height, c.style.width, c.style.height]), screen: document.querySelector('.xterm-screen')?.getBoundingClientRect(), rows: document.querySelectorAll('.xterm-rows > div').length})", returnByValue: true }); console.log(d.result.value, result.value); }
  const clip = { ...result.value, scale: 1 };
  const shot = await s("Page.captureScreenshot", { format: "png", clip, captureBeyondViewport: true });
  writeFileSync(out, Buffer.from(shot.data, "base64"));
  console.log(`${name}.png  ${Math.round(clip.width * 2)}x${Math.round(clip.height * 2)}  (${cols}x${rows} cells)`);
} finally {
  browser.close();
  chrome.kill();
}
