#!/usr/bin/env node
// Record what safina writes to its terminal as an asciicast v2 file
// (asciinema's format), with real timing. capture.sh pipes the tmux pane's
// raw output here (`tmux pipe-pane -O`), so the recording is exactly the
// bytes safina wrote, nothing redrawn or retimed. A whole shoot is one
// recording; castcut.mjs cuts the beats out of it.
//
//   node castrec.mjs <out.cast> <cols> <rows> [marks file]   (stream on stdin)
//
// Lines appended to the marks file become events at the moment they arrive:
// "size <cols>x<rows>" an asciicast "r" (resize) event, written by
// capture.sh just before it resizes the pane, so a cut knows the grid each
// byte was meant for; anything else an "m" (marker) event, one per key press.

import { createWriteStream, existsSync, readFileSync, statSync } from "node:fs";

const [out, cols, rows, marks] = process.argv.slice(2);
if (!out || !cols || !rows) {
  console.error("usage: castrec.mjs <out.cast> <cols> <rows> [marks file]");
  process.exit(2);
}
const file = createWriteStream(out);
file.write(JSON.stringify({ version: 2, width: Number(cols), height: Number(rows), timestamp: Date.now() / 1000, env: { TERM: "xterm-256color" } }) + "\n");
const start = process.hrtime.bigint();
const now = () => Math.round((Number(process.hrtime.bigint() - start) / 1e9) * 1e6) / 1e6;
const event = (kind, data) => file.write(JSON.stringify([now(), kind, data]) + "\n");

let markAt = marks && existsSync(marks) ? statSync(marks).size : 0;
const readMarks = () => {
  if (!marks || !existsSync(marks)) return;
  const n = statSync(marks).size;
  if (n <= markAt) return;
  const added = readFileSync(marks).subarray(markAt, n).toString("utf8");
  markAt = n;
  for (const line of added.split("\n").map((l) => l.trim()).filter(Boolean)) {
    const size = /^size (\d+x\d+)$/.exec(line);
    event(size ? "r" : "m", size ? size[1] : line);
  }
};
const markPoll = setInterval(readMarks, 20);

// A chunk can end inside a multi-byte character; the streaming decoder holds
// the partial bytes until the rest arrive.
const utf8 = new TextDecoder("utf-8", { fatal: false });
process.stdin.on("data", (chunk) => {
  readMarks(); // a resize announced just now lands before the bytes for it
  const text = utf8.decode(chunk, { stream: true });
  if (text) event("o", text);
});
process.stdin.on("end", () => {
  clearInterval(markPoll);
  readMarks();
  const rest = utf8.decode();
  if (rest) event("o", rest);
  file.end();
});
