#!/usr/bin/env node
// Cut one beat out of a shoot recording (castrec.mjs) into a small cast for
// the site. The bytes safina wrote are kept as they were; only the start is
// new: the recording is replayed into a headless terminal up to the cut, and
// that exact screen becomes the beat's first frame. Long idle gaps are
// shortened, and the markers named with --pause stay in as pause points.
//
//   node castcut.mjs <shoot.cast> <out.cast> --from <at> --to <at>
//        [--pause <label>]... [--idle 1.2] [--lead 0.4] [--tail 0.8]
//
// <at> is seconds into the shoot, or a marker: "m:<text>" is a marker
// containing <text>, with an optional "+1.5" or "-0.2" offset. For --from it
// is the last such marker, so a re-take in the same recording wins; for --to
// and --pause, the first one after --from. A beat must not span a resize.

import { readFileSync, writeFileSync } from "node:fs";
import xtermHeadless from "@xterm/headless";
import serializePkg from "@xterm/addon-serialize";

const { Terminal } = xtermHeadless;
const { SerializeAddon } = serializePkg;

const args = process.argv.slice(2);
const [src, out] = args;
const opt = (name, fallback) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : fallback;
};
const all = (name) => args.flatMap((a, i) => (a === `--${name}` ? [args[i + 1]] : []));
if (!src || !out || !opt("from") || !opt("to")) {
  console.error("usage: castcut.mjs <shoot.cast> <out.cast> --from <at> --to <at> [--pause <label>]... [--idle s] [--lead s] [--tail s]");
  process.exit(2);
}

const lines = readFileSync(src, "utf8").split("\n").filter(Boolean);
const header = JSON.parse(lines[0]);
const events = lines.slice(1).map((l) => JSON.parse(l));

const at = (spec, after = 0, last = false) => {
  const m = /^m:(.*?)([+-]\d+(?:\.\d+)?)?$/.exec(spec);
  if (!m) return Number(spec);
  const marks = events.filter(([t, k, d]) => k === "m" && t >= after && d.includes(m[1]));
  const hit = last ? marks.at(-1) : marks[0];
  if (!hit) throw new Error(`no marker containing "${m[1]}" after ${after}s`);
  return hit[0] + Number(m[2] ?? 0);
};
const from = at(opt("from"), 0, true);
const to = at(opt("to"), from);
const idle = Number(opt("idle", "1.2"));
const lead = Number(opt("lead", "0.4"));
const tail = Number(opt("tail", "0.8"));
if (!(to > from)) throw new Error(`--to (${to}) must come after --from (${from})`);

// Replay everything before the cut into a headless terminal of the same size.
let [cols, rows] = [header.width, header.height];
const term = new Terminal({ cols, rows, allowProposedApi: true, scrollback: 0 });
const serializer = new SerializeAddon();
term.loadAddon(serializer);
const write = (d) => new Promise((r) => term.write(d, r));
for (const [t, kind, data] of events) {
  if (t >= from) break;
  if (kind === "r") {
    [cols, rows] = data.split("x").map(Number);
    term.resize(cols, rows);
  } else if (kind === "o") await write(data);
}
const resized = events.find(([t, k]) => k === "r" && t >= from && t <= to);
if (resized) throw new Error(`the beat spans a resize at ${resized[0]}s; cut after it`);

// The first frame: the screen as it stood at the cut, cursor hidden (safina
// shows it only where you type, and says so itself when it does).
const frame = "\x1b[?25l" + serializer.serialize({ excludeAltBuffer: false, excludeModes: false });
const pauses = all("pause");
const beat = [[0, "o", frame]];
let clock = lead;
let last = from;
for (const [t, kind, data] of events) {
  if (t < from || t > to) continue;
  const gap = t - last;
  clock += Math.min(gap, idle);
  last = t;
  if (kind === "o") beat.push([clock, "o", data]);
  else if (kind === "m" && pauses.some((p) => data.includes(p))) beat.push([clock, "m", data]);
}
// Hold the last frame a moment before the cast ends.
beat.push([clock + tail, "o", ""]);

const round = (n) => Math.round(n * 1000) / 1000;
const body = beat.map(([t, k, d]) => JSON.stringify([round(t), k, d])).join("\n");
writeFileSync(out, JSON.stringify({ version: 2, width: cols, height: rows, timestamp: header.timestamp + Math.floor(from) }) + "\n" + body + "\n");
const bytes = Buffer.byteLength(body);
console.log(`${out}: ${cols}x${rows}, ${round(clock + tail)}s (from ${round(from)}s to ${round(to)}s of the shoot), ${beat.length} events, ${(bytes / 1024).toFixed(1)} KB`);
term.dispose();
