// A minimal WebDriver BiDi client for Gecko, so frames.mjs can shoot the site
// in the engine the user actually browses with (Zen, a Firefox fork). Headless,
// with a throwaway profile: no extensions and none of the user's settings, so
// this checks the engine, not their exact browser.
//
//   const gecko = await launchGecko({ reduced: false });
//   const page = await gecko.page({ width: 1440, height: 900 }, preloadSource);
//   await page.goto(url); await page.eval("1 + 1"); await page.shot();

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

function findGecko() {
  if (process.env.GECKO) return process.env.GECKO;
  for (const bin of ["zen-browser", "zen", "firefox", "firefox-developer-edition"]) {
    try {
      return execFileSync("sh", ["-c", `command -v ${bin}`], { encoding: "utf8" }).trim();
    } catch {
      // try the next name
    }
  }
  throw new Error("no Gecko browser found (set GECKO=/path/to/zen-browser or firefox)");
}

export async function launchGecko({ reduced = false } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "safina-gecko-"));
  // Fresh every run: no cache between viewports, motion preference as asked.
  writeFileSync(
    join(dir, "user.js"),
    [
      `user_pref("ui.prefersReducedMotion", ${reduced ? 1 : 0});`,
      `user_pref("browser.cache.disk.enable", false);`,
      `user_pref("browser.cache.memory.enable", false);`,
      `user_pref("browser.shell.checkDefaultBrowser", false);`,
      `user_pref("datareporting.policy.dataSubmissionEnabled", false);`,
    ].join("\n"),
  );
  const bin = findGecko();
  const proc = spawn(bin, ["--headless", "--no-remote", "--profile", dir, "--remote-debugging-port", "0"], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  const wsUrl = await new Promise((resolve, reject) => {
    let buf = "";
    const read = (d) => {
      buf += d;
      const m = /WebDriver BiDi listening on (ws:\/\/\S+)/.exec(buf);
      if (m) resolve(m[1]);
    };
    proc.stdout.on("data", read);
    proc.stderr.on("data", read);
    proc.on("exit", () => reject(new Error(`${bin} exited early:\n${buf}`)));
    setTimeout(() => reject(new Error(`${bin} never offered WebDriver BiDi:\n${buf}`)), 30000);
  });

  const ws = new WebSocket(`${wsUrl.replace(/\/$/, "")}/session`);
  await new Promise((resolve, reject) => {
    ws.addEventListener("open", resolve, { once: true });
    ws.addEventListener("error", reject, { once: true });
  });
  let id = 0;
  const pending = new Map();
  const listeners = new Set();
  ws.addEventListener("message", (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.type === "error") reject(new Error(`${msg.error}: ${msg.message}`));
      else resolve(msg.result);
    } else if (msg.type === "event") {
      for (const fn of listeners) fn(msg);
    }
  });
  const send = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const msgId = ++id;
      pending.set(msgId, { resolve, reject });
      ws.send(JSON.stringify({ id: msgId, method, params }));
    });

  await send("session.new", { capabilities: {} });
  await send("session.subscribe", { events: ["log.entryAdded"] });

  return {
    name: `${bin.split("/").pop()} (Gecko)`,
    async page({ width, height, dpr = 1 }, preload) {
      const { context } = await send("browsingContext.create", { type: "tab" });
      await send("browsingContext.setViewport", { context, viewport: { width, height }, devicePixelRatio: dpr });
      let script;
      if (preload) script = (await send("script.addPreloadScript", { functionDeclaration: `() => { ${preload} }`, contexts: [context] })).script;
      const errors = [];
      const onLog = (msg) => {
        if (msg.method === "log.entryAdded" && msg.params.level === "error" && msg.params.source?.context === context)
          errors.push(msg.params.text);
      };
      listeners.add(onLog);
      return {
        errors,
        async goto(url) {
          await send("browsingContext.navigate", { context, url, wait: "complete" });
        },
        async eval(expression) {
          const r = await send("script.evaluate", {
            expression: `(async () => JSON.stringify(await (${expression})))()`,
            target: { context },
            awaitPromise: true,
          });
          if (r.type === "exception") throw new Error(r.exceptionDetails?.text ?? "script exception");
          const v = r.result?.value;
          return v === undefined || v === "undefined" ? undefined : JSON.parse(v);
        },
        async shot({ full = false } = {}) {
          const { data } = await send("browsingContext.captureScreenshot", { context, origin: full ? "document" : "viewport" });
          return data;
        },
        async close() {
          listeners.delete(onLog);
          if (script) await send("script.removePreloadScript", { script }).catch(() => {});
          await send("browsingContext.close", { context });
        },
      };
    },
    async close() {
      await send("session.end").catch(() => {});
      ws.close();
      proc.kill();
      await new Promise((r) => setTimeout(r, 300));
      rmSync(dir, { recursive: true, force: true });
    },
  };
}
