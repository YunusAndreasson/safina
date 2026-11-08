// The recordings' player, loaded only when a recording is about to play.
//
// A cast is real safina, recorded from the terminal during the shoot
// (scripts/castrec.mjs, cut by castcut.mjs) in asciinema's format. It plays
// in xterm.js, the renderer the stills are made with (scripts/snap.mjs), at
// the stills' own geometry: 10×22px cells in a 22/20px margin, scaled to the
// image on screen and cut to the same cell box where the still is a crop. So
// the first and last frames sit exactly on their stills, and box lines and
// braille are drawn the same way (customGlyphs, WebGL).

import { Terminal } from "@xterm/xterm";
import { WebglAddon } from "@xterm/addon-webgl";
import "@xterm/xterm/css/xterm.css";
import "../styles/term.css";

/** A crop of the screen in terminal cells: [col, row, cols, rows]. */
export type Box = [number, number, number, number];
type Event = [number, string, string];
export interface Cast {
  cols: number;
  rows: number;
  events: Event[];
  duration: number;
}

const CELL_W = 10;
const CELL_H = 22;
const FULL = { left: 22, top: 20, right: 22, bottom: 22 }; // snap.mjs's margin
const CROP = 8; // crops.sh pads a crop with 16 output pixels at 2x
const FAMILY = '"Safina Term", monospace';
const THEME = {
  background: "#1a1b26",
  foreground: "#c0caf5",
  cursor: "#1a1b26",
  cursorAccent: "#1a1b26",
  black: "#15161e",
  red: "#f7768e",
  green: "#9ece6a",
  yellow: "#e0af68",
  blue: "#7aa2f7",
  magenta: "#bb9af7",
  cyan: "#7dcfff",
  white: "#a9b1d6",
  brightBlack: "#414868",
  brightRed: "#f7768e",
  brightGreen: "#9ece6a",
  brightYellow: "#e0af68",
  brightBlue: "#7aa2f7",
  brightMagenta: "#bb9af7",
  brightCyan: "#7dcfff",
  brightWhite: "#c0caf5",
};

const casts = new Map<string, Promise<Cast>>();
/** Fetch and parse a cast once; later calls share the result. */
export function loadCast(url: string): Promise<Cast> {
  let cast = casts.get(url);
  if (!cast) {
    cast = fetch(url)
      .then((r) => {
        if (!r.ok) throw new Error(`${url}: HTTP ${String(r.status)}`);
        return r.text();
      })
      .then((text) => {
        const [head, ...lines] = text.trim().split("\n");
        const header = JSON.parse(head ?? "{}") as { width: number; height: number };
        const events = lines.map((l) => JSON.parse(l) as Event);
        return { cols: header.width, rows: header.height, events, duration: events.at(-1)?.[0] ?? 0 };
      });
    casts.set(url, cast);
  }
  return cast;
}

let fonts: Promise<unknown> | undefined;
/** The terminal measures its cells from the font, so it must be loaded first. */
export function loadFonts(): Promise<unknown> {
  fonts ??= Promise.all(["17px", "bold 17px", "italic 17px"].map((f) => document.fonts.load(`${f} ${FAMILY}`)));
  return fonts;
}

const nextFrame = () =>
  new Promise<void>((r) => {
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        r();
      });
    });
  });

/** One terminal over one still at a time; reusable for casts of its size. */
export class Stage {
  readonly root: HTMLDivElement;
  private readonly win: HTMLDivElement;
  private readonly clip: HTMLDivElement;
  private readonly host: HTMLDivElement;
  private readonly term: Terminal;
  private raf = 0;
  private run = 0;
  readonly cols: number;
  readonly rows: number;

  private constructor(cols: number, rows: number) {
    this.cols = cols;
    this.rows = rows;
    this.root = Object.assign(document.createElement("div"), { className: "cast" });
    this.root.setAttribute("aria-hidden", "true");
    this.win = Object.assign(document.createElement("div"), { className: "cast__window" });
    this.clip = Object.assign(document.createElement("div"), { className: "cast__clip" });
    this.host = Object.assign(document.createElement("div"), { className: "cast__term" });
    this.clip.append(this.host);
    this.win.append(this.clip);
    this.root.append(this.win);
    this.term = new Terminal({
      cols,
      rows,
      fontFamily: FAMILY,
      fontSize: 17,
      lineHeight: 1,
      letterSpacing: 0,
      customGlyphs: true,
      rescaleOverlappingGlyphs: true,
      drawBoldTextInBrightColors: false,
      minimumContrastRatio: 1,
      cursorBlink: false,
      cursorInactiveStyle: "none",
      disableStdin: true,
      scrollback: 0,
      theme: THEME,
    });
  }

  /** A stage for casts of this size, or null where WebGL is missing: there
   *  the font would draw the box lines, and the stills stay instead. */
  static async create(cols: number, rows: number, parent: HTMLElement): Promise<Stage | null> {
    await loadFonts();
    const stage = new Stage(cols, rows);
    parent.append(stage.root);
    stage.term.open(stage.host);
    try {
      const gl = new WebglAddon();
      gl.onContextLoss(() => {
        gl.dispose();
      });
      stage.term.loadAddon(gl);
    } catch {
      stage.dispose();
      return null;
    }
    return stage;
  }

  /** Lay the stage exactly over a still: the whole screen, or a crop box. */
  place(img: HTMLImageElement, box: Box | null): void {
    const parent = img.parentElement?.closest<HTMLElement>(".frame__scroller") ?? img.parentElement;
    if (parent && this.root.parentElement !== parent) parent.append(this.root);
    const [col, row, cols, rows] = box ?? [0, 0, this.cols, this.rows];
    const pad = box ? { left: CROP, top: CROP, right: CROP, bottom: CROP } : FULL;
    const w = cols * CELL_W + pad.left + pad.right;
    const h = rows * CELL_H + pad.top + pad.bottom;
    const scale = img.offsetWidth / w;
    Object.assign(this.root.style, {
      left: `${String(img.offsetLeft)}px`,
      top: `${String(img.offsetTop)}px`,
      width: `${String(img.offsetWidth)}px`,
      height: `${String(img.offsetHeight)}px`,
    });
    Object.assign(this.win.style, { width: `${String(w)}px`, height: `${String(h)}px`, transform: `scale(${String(scale)})` });
    // The renderer snaps cells to device pixels; stretch the grid back to
    // the stills' 10×22 so every cell lands on its twin in the image.
    const screen = this.host.querySelector<HTMLElement>(".xterm-screen");
    const cw = screen ? screen.offsetWidth / this.cols : CELL_W;
    const ch = screen ? screen.offsetHeight / this.rows : CELL_H;
    // Only the still's own cells show; its margin is plain background, as
    // crops.sh pads a crop, so no sliver of a neighbouring row leaks in.
    Object.assign(this.clip.style, {
      left: `${String(pad.left)}px`,
      top: `${String(pad.top)}px`,
      width: `${String(cols * CELL_W)}px`,
      height: `${String(rows * CELL_H)}px`,
    });
    Object.assign(this.host.style, {
      left: `${String(-col * CELL_W)}px`,
      top: `${String(-row * CELL_H)}px`,
      transform: cw && ch ? `scale(${String(CELL_W / cw)}, ${String(CELL_H / ch)})` : "",
    });
  }

  /** Draw the cast as it stands at `at` seconds (its first frame by default)
   *  without showing it; resolves once the frame is rendered. */
  async cue(cast: Cast, at = 0): Promise<void> {
    this.stop();
    this.term.reset();
    let data = "";
    for (const [t, kind, d] of cast.events) {
      if (t > at) break;
      if (kind === "o") data += d;
    }
    await new Promise<void>((r) => {
      this.term.write(data, r);
    });
    await nextFrame();
  }

  /** Play from `from` seconds with the recording's own timing. Resolves
   *  true at the end, false if stopped first. */
  play(cast: Cast, from = 0): Promise<boolean> {
    this.stop();
    const run = ++this.run;
    let i = cast.events.findIndex(([t]) => t > from);
    if (i < 0) return Promise.resolve(true);
    const start = performance.now() - from * 1000;
    return new Promise((resolve) => {
      const tick = () => {
        if (run !== this.run) {
          resolve(false);
          return;
        }
        const now = (performance.now() - start) / 1000;
        let data = "";
        while (i < cast.events.length && (cast.events[i]?.[0] ?? Infinity) <= now) {
          const [, kind, d] = cast.events[i] ?? [];
          if (kind === "o" && d) data += d;
          i++;
        }
        if (data) this.term.write(data);
        if (i >= cast.events.length) {
          this.term.write("", () => {
            resolve(run === this.run);
          });
          return;
        }
        this.raf = requestAnimationFrame(tick);
      };
      this.raf = requestAnimationFrame(tick);
    });
  }

  stop(): void {
    this.run++;
    cancelAnimationFrame(this.raf);
  }

  show(): void {
    this.root.classList.add("is-on");
  }

  hide(): void {
    this.root.classList.remove("is-on");
  }

  dispose(): void {
    this.stop();
    this.term.dispose();
    this.root.remove();
  }
}
