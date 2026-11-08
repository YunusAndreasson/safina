#!/usr/bin/env node
// `pnpm ship`: puts dist/ live on safina.run, the Cloudflare Worker named in
// wrangler.jsonc, checks that the domain serves exactly this build, and
// rolls back to the previous version if it doesn't. CI runs it on every push to
// main (.github/workflows/deploy-site.yml), after `pnpm verify` has built
// dist/. Locally it uses wrangler's login; in CI, CLOUDFLARE_API_TOKEN and
// CLOUDFLARE_ACCOUNT_ID.
//
// `pnpm ship --dry-run`: everything except the upload. wrangler validates the
// upload, then serves dist/ through Cloudflare's own asset server
// (`wrangler dev`) and the same checks run against it. Needs no login.
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DIST = path.join(ROOT, "dist");
const WRANGLER = path.join(ROOT, "node_modules/.bin/wrangler");
// Constants, never arguments: the one Worker this site may replace, and the
// one domain it serves.
const WORKER = "safina-site";
const DOMAIN = "safina.run";
const LIVE = `https://${DOMAIN}`;
// How wrangler lists the custom domain among a deploy's targets.
const LIVE_TARGET = /^(https:\/\/)?safina\.run(\/?\*?| \(custom domain\))$/;

const args = process.argv.slice(2);
const dryRun = args.includes("--dry-run");
const unknown = args.filter((a) => a !== "--dry-run");

const log = (step, detail) => console.log(`[${step}] ${detail}`);
class ShipError extends Error {}
const fail = (message) => {
  throw new ShipError(message);
};
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const wrangler = (argv, opts = {}) =>
  spawnSync(WRANGLER, argv, { cwd: ROOT, encoding: "utf8", stdio: ["ignore", "inherit", "inherit"], ...opts });

/** wrangler.jsonc as wrangler reads it, comments and all, checked before anything uses it. */
function guard() {
  const file = path.join(ROOT, "wrangler.jsonc");
  const { config, error } = ts.parseConfigFileTextToJson(file, fs.readFileSync(file, "utf8"));
  if (error || !config) fail("wrangler.jsonc doesn't parse.");
  if (config.name !== WORKER) fail(`wrangler.jsonc names "${config.name}". Only ${WORKER} may ship from here.`);
  if (config.main) fail("wrangler.jsonc sets `main`. The site is static files only; a Worker script needs a decision first.");
  // A deploy without the route would detach the live domain; any other route would claim one.
  const routes = config.routes ?? [];
  const only = routes.length === 1 && routes[0]?.pattern === DOMAIN && routes[0]?.custom_domain === true && Object.keys(routes[0]).length === 2;
  if (config.route || !only) fail(`wrangler.jsonc must route exactly ${DOMAIN}, as a custom domain, and nothing else.`);
  // The live domain is the only copy, so nothing else can be indexed or drift.
  if (config.workers_dev !== false || config.preview_urls !== false) fail("wrangler.jsonc must keep workers.dev and preview URLs off.");
  if (config.assets?.directory !== "./dist") fail(`wrangler.jsonc serves "${config.assets?.directory}", not ./dist.`);
  for (const name of ["index.html", "404.html", "_headers"]) {
    if (!fs.existsSync(path.join(DIST, name))) fail(`dist/${name} is missing. Run \`pnpm verify\` first, which builds dist/.`);
  }
  log("guard", `wrangler.jsonc names ${WORKER}, static files only, on ${DOMAIN} alone; dist/ is built`);
}

/** One file of each kind the checks below need, from this build. */
function samples() {
  const astro = fs.readdirSync(path.join(DIST, "_astro"));
  const js = astro.find((f) => f.endsWith(".js"));
  const cast = astro.find((f) => f.endsWith(".cast"));
  if (!js || !cast) fail("dist/_astro holds no script or no recording to check.");
  return { js: `/_astro/${js}`, cast: `/_astro/${cast}` };
}

const read = (route) => fs.readFileSync(path.join(DIST, route));

/** The served site against dist/. Returns problems (a failed deploy) and notes (worth knowing, not worth a rollback). */
async function check(origin, { remote }) {
  const problems = [];
  const notes = [];
  const { js, cast } = samples();
  const get = async (route) => {
    const res = await fetch(origin + route, { redirect: "manual", headers: { "accept-encoding": "gzip, br" } });
    return { res, body: Buffer.from(await res.arrayBuffer()) };
  };
  const expect = (ok, what) => {
    if (!ok) problems.push(what);
  };
  const robots = (res) => (res.headers.get("x-robots-tag") ?? "").includes("noindex");

  const home = await get("/");
  expect(home.res.status === 200, `/ answers ${home.res.status}`);
  expect(sha(home.body) === sha(read("index.html")), "/ is not this build's index.html");
  expect(!robots(home.res), "/ carries X-Robots-Tag: noindex, which keeps the live site out of search");

  const missing = await get("/no-such-page");
  expect(missing.res.status === 404, `/no-such-page answers ${missing.res.status}, not 404`);
  expect(sha(missing.body) === sha(read("404.html")), "/no-such-page is not this build's 404.html");

  const headers = await get("/_headers");
  expect(headers.res.status === 404, `/_headers is served (${headers.res.status})`);

  const script = await get(js);
  expect(script.res.status === 200 && sha(script.body) === sha(read(js)), `${js} is not this build's file`);
  expect((script.res.headers.get("cache-control") ?? "").includes("immutable"), `${js} isn't cached as immutable`);

  const recording = await get(cast);
  const type = recording.res.headers.get("content-type") ?? "";
  expect(recording.res.status === 200 && sha(recording.body) === sha(read(cast)), `${cast} is not this build's file`);
  expect(type.startsWith("text/plain"), `${cast} is served as "${type}", not text/plain`);
  if (remote && !recording.res.headers.get("content-encoding")) notes.push(`${cast} arrived uncompressed`);

  const card = await get("/og.jpg");
  expect(card.res.status === 200, `/og.jpg answers ${card.res.status}`);
  expect(!(card.res.headers.get("cache-control") ?? "").includes("immutable"), "/og.jpg, which keeps its name across builds, is cached as immutable");

  return { problems, notes };
}

/** A new version can take a few seconds to reach every location, so the checks retry until `wait` runs out. */
async function smoke(origin, { remote, wait }) {
  const deadline = Date.now() + wait;
  for (;;) {
    let result;
    try {
      result = await check(origin, { remote });
    } catch (error) {
      result = { problems: [`request failed: ${error.message}`], notes: [] };
    }
    if (result.problems.length === 0 || Date.now() >= deadline) return result;
    await sleep(3000);
  }
}

const freePort = () =>
  new Promise((resolve, reject) => {
    const server = net.createServer().once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });

async function dry() {
  const outdir = fs.mkdtempSync(path.join(os.tmpdir(), "safina-ship-"));
  try {
    if (wrangler(["deploy", "--dry-run", "--outdir", outdir]).status !== 0) fail("wrangler deploy --dry-run failed.");
  } finally {
    fs.rmSync(outdir, { recursive: true, force: true });
  }
  log("upload", "wrangler accepts the config and the files (dry run, nothing sent)");

  const port = await freePort();
  // The asset server matches _headers' host rules against this, so the local
  // run is checked as the live domain is served.
  const dev = spawn(WRANGLER, ["dev", "--ip", "127.0.0.1", "--port", String(port), "--local-upstream", DOMAIN, "--show-interactive-dev-session=false", "--log-level", "warn"], { cwd: ROOT, stdio: ["ignore", "inherit", "inherit"] });
  const origin = `http://127.0.0.1:${port}`;
  try {
    const deadline = Date.now() + 60_000;
    for (;;) {
      if (dev.exitCode !== null) fail(`wrangler dev exited with ${dev.exitCode}.`);
      try {
        await fetch(origin);
        break;
      } catch {
        if (Date.now() > deadline) fail("wrangler dev didn't answer within a minute.");
        await sleep(500);
      }
    }
    const { problems } = await smoke(origin, { remote: false, wait: 0 });
    if (problems.length > 0) fail(`The local asset server doesn't serve the build right:\n  ${problems.join("\n  ")}`);
    log("smoke", `${origin} (as ${DOMAIN}) serves this build: pages, 404, caching, recordings as text, indexable`);
  } finally {
    dev.kill("SIGTERM");
  }
}

/** What's live now, to roll back to. null before the first deploy. */
function current() {
  const result = wrangler(["deployments", "status", "--name", WORKER, "--json"], { stdio: ["ignore", "pipe", "pipe"] });
  if (result.status !== 0) {
    if (/code: 10007/.test(result.stderr)) return null;
    fail(`wrangler deployments status failed:\n${result.stderr}`);
  }
  const version = JSON.parse(result.stdout).versions?.find((v) => v.percentage === 100)?.version_id;
  if (!version) fail("The live deployment isn't one version at 100%, so there's no single version to roll back to. Look in the dashboard first.");
  return version;
}

function commit() {
  const run = (argv) => spawnSync("git", argv, { cwd: ROOT, encoding: "utf8" }).stdout?.trim() ?? "";
  const head = run(["rev-parse", "--short=12", "HEAD"]);
  const dirty = run(["status", "--porcelain", "--", "."]) !== "";
  return { head, dirty, subject: run(["log", "-1", "--format=%s"]) };
}

async function live() {
  const previous = current();
  log("live", previous ? `${WORKER} serves version ${previous}` : `${WORKER} doesn't exist yet; this is its first deploy`);

  const { head, dirty, subject } = commit();
  const message = `${head}${dirty ? " plus uncommitted changes" : ""}: ${subject}`.slice(0, 100);
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "safina-ship-"));
  const output = path.join(dir, "out.ndjson");
  let deployed;
  try {
    const result = wrangler(["deploy", "--message", message], { env: { ...process.env, WRANGLER_OUTPUT_FILE_PATH: output } });
    if (result.status !== 0) fail("wrangler deploy failed. Nothing can be assumed live; the previous version should still be serving.");
    const lines = fs.existsSync(output) ? fs.readFileSync(output, "utf8").trim().split("\n") : [];
    deployed = lines.map((line) => JSON.parse(line)).findLast((line) => line.type === "deploy");
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  if (!deployed) fail("wrangler deploy reported no deployment.");
  if (deployed.worker_name !== WORKER) fail(`wrangler deployed "${deployed.worker_name}", not ${WORKER}.`);
  const targets = deployed.targets ?? [];
  log("upload", `version ${deployed.version_id}, ${message}; targets ${targets.join(", ") || "none"}`);

  // A just-attached domain needs its DNS record and certificate, which can
  // take minutes; after that a version spreads in seconds.
  const wait = previous ? 180_000 : 900_000;
  const onlyLive = targets.length > 0 && targets.every((t) => LIVE_TARGET.test(t));
  const result = onlyLive ? await smoke(LIVE, { remote: true, wait }) : { problems: [`the deploy's targets aren't ${DOMAIN} alone: ${targets.join(", ") || "none"}`], notes: [] };
  if (result.problems.length > 0) {
    const lines = [`The new version fails at the edge:\n  ${result.problems.join("\n  ")}`];
    if (!previous) {
      lines.push(`There's no earlier version to roll back to, so it stays up. Fix the cause and ship again, or take ${DOMAIN} off the Worker in the dashboard.`);
    } else if (wrangler(["rollback", previous, "--name", WORKER, "--yes", "--message", `ship: smoke failed on ${deployed.version_id}`]).status === 0) {
      lines.push(`Rolled ${WORKER} back to ${previous}.`);
    } else {
      lines.push(`THE ROLLBACK FAILED. ${WORKER} still serves the failed version. Run: wrangler rollback ${previous} --name ${WORKER}`);
    }
    fail(lines.join("\n"));
  }
  for (const note of result.notes) log("note", note);
  log("smoke", `${LIVE} serves this build: pages, 404, caching, recordings as text, indexable`);

  if (process.env.GITHUB_OUTPUT) fs.appendFileSync(process.env.GITHUB_OUTPUT, `url=${LIVE}\nversion=${deployed.version_id}\n`);
  if (process.env.GITHUB_STEP_SUMMARY) {
    fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, `### Site deployed\n\n${LIVE}\n\nVersion \`${deployed.version_id}\`, from ${message}.${previous ? ` Replaced \`${previous}\`.` : ""}\n`);
  }
}

try {
  if (unknown.length > 0) fail(`Unknown argument: ${unknown.join(" ")}. The only option is --dry-run; the Worker comes from wrangler.jsonc.`);
  guard();
  await (dryRun ? dry() : live());
} catch (error) {
  if (!(error instanceof ShipError)) throw error;
  console.error(`\n${error.message}`);
  process.exit(1);
}
