// A minimal Chrome DevTools Protocol client over Node's built-in WebSocket,
// shared by frames.mjs and copy-tells.mjs. Headless Chromium with a
// throwaway profile: nothing is drawn on the display, nothing is installed.
//
//   const browser = await launch();
//   const page = await browser.page();
//   await page.send("Page.navigate", { url });
//   ...
//   await browser.close();

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

function findBrowser() {
  if (process.env.CHROME) return process.env.CHROME;
  for (const bin of ["chromium", "google-chrome-stable", "google-chrome", "chromium-browser"]) {
    try {
      return execFileSync("sh", ["-c", `command -v ${bin}`], { encoding: "utf8" }).trim();
    } catch {
      // try the next name
    }
  }
  throw new Error("no Chromium found (set CHROME=/path/to/chromium)");
}

export async function launch({ args = [] } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "safina-cdp-"));
  const chrome = spawn(
    findBrowser(),
    [
      "--headless=new",
      "--remote-debugging-port=0",
      `--user-data-dir=${join(dir, "profile")}`,
      "--no-first-run",
      "--no-default-browser-check",
      "--hide-scrollbars",
      "--allow-file-access-from-files",
      "--use-angle=swiftshader",
      "--enable-unsafe-swiftshader",
      ...args,
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  const wsUrl = await new Promise((resolve, reject) => {
    let buf = "";
    chrome.stderr.on("data", (d) => {
      buf += d;
      const m = /DevTools listening on (ws:\/\/\S+)/.exec(buf);
      if (m) resolve(m[1]);
    });
    chrome.on("exit", () => reject(new Error(`chromium exited early:\n${buf}`)));
  });

  const ws = new WebSocket(wsUrl);
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));
  let id = 0;
  const pending = new Map();
  const listeners = new Map(); // sessionId -> Map(method -> Set(fn))
  ws.addEventListener("message", (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) reject(new Error(`${msg.error.message} (${msg.error.code})`));
      else resolve(msg.result);
      return;
    }
    const fns = listeners.get(msg.sessionId)?.get(msg.method);
    if (fns) for (const fn of fns) fn(msg.params);
  });
  const send = (method, params = {}, sessionId) =>
    new Promise((resolve, reject) => {
      const msgId = ++id;
      pending.set(msgId, { resolve, reject });
      ws.send(JSON.stringify({ id: msgId, method, params, ...(sessionId && { sessionId }) }));
    });

  return {
    async page() {
      const { targetId } = await send("Target.createTarget", { url: "about:blank" });
      const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
      listeners.set(sessionId, new Map());
      const on = (method, fn) => {
        const m = listeners.get(sessionId);
        if (!m.has(method)) m.set(method, new Set());
        m.get(method).add(fn);
      };
      const once = (method, timeoutMs = 30000) =>
        new Promise((resolve, reject) => {
          const timer = setTimeout(() => reject(new Error(`timed out waiting for ${method}`)), timeoutMs);
          const fn = (p) => {
            clearTimeout(timer);
            listeners.get(sessionId).get(method).delete(fn);
            resolve(p);
          };
          on(method, fn);
        });
      const p = {
        send: (method, params) => send(method, params, sessionId),
        on,
        once,
        /** Evaluate an expression (awaited, returned by value). */
        async eval(expression) {
          const r = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }, sessionId);
          if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? r.exceptionDetails.text);
          return r.result.value;
        },
        async goto(url) {
          const loaded = once("Page.loadEventFired", 60000);
          const nav = await send("Page.navigate", { url }, sessionId);
          if (nav.errorText) throw new Error(`could not load ${url}: ${nav.errorText}`);
          await loaded;
        },
        close: () => send("Target.closeTarget", { targetId }),
      };
      await p.send("Page.enable");
      await p.send("Runtime.enable");
      return p;
    },
    async close() {
      ws.close();
      chrome.kill();
      await new Promise((r) => setTimeout(r, 100));
      rmSync(dir, { recursive: true, force: true });
    },
  };
}
