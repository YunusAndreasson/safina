// Recordings on the page: which still each one belongs to, when to fetch the
// player, and the keys that play them. Small; the player itself (cast.ts,
// with xterm.js) loads only as a recording comes near the screen.
//
// Motion has a cause here, as everywhere on the page: a recording plays when
// its key is pressed or its button clicked, never by itself. With reduced
// motion, a recording that ends somewhere new cuts straight to its last
// frame, and one that ends on its own still offers nothing to play.

import type { Box, Cast, Stage } from "./cast";

type Player = typeof import("./cast");
let player: Promise<Player> | undefined;
export const loadPlayer = (): Promise<Player> => (player ??= import("./cast"));

export const phone = matchMedia("(width <= 640px)");
export const reduce = matchMedia("(prefers-reduced-motion: reduce)");
const saveData = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData === true;

/** The cell box the visible still shows: the phone crop, the wide strip, or
 *  null for the whole screen. */
export function boxFor(frame: HTMLElement): Box | null {
  const box = phone.matches ? frame.dataset.boxM : frame.dataset.boxF;
  return box ? (box.split(",").map(Number) as Box) : null;
}

// The first sign of interest: scrolling into the page, a key, or a pointer
// or finger reaching for a key. Until then the player costs nothing; the
// tour sits under the opening, so "near the screen" alone is true on arrival.
const intent = new Promise<void>((resolve) => {
  const done = () => {
    removeEventListener("scroll", onScroll);
    removeEventListener("keydown", done);
    removeEventListener("pointerover", onPointer);
    removeEventListener("touchstart", onPointer);
    resolve();
  };
  const onScroll = () => {
    if (scrollY > innerHeight * 0.25) done();
  };
  const onPointer = (e: Event) => {
    if (e.target instanceof Element && e.target.closest('[role="tab"], .cast-play')) done();
  };
  addEventListener("scroll", onScroll, { passive: true });
  addEventListener("keydown", done);
  addEventListener("pointerover", onPointer, { passive: true });
  addEventListener("touchstart", onPointer, { passive: true });
});

/** Fetch the player and its recordings ahead of time, once, when they near
 *  the screen and the visitor has shown interest (not on a data saver);
 *  `then` can set up a stage after. */
export function warmWhenNear(el: Element, urls: () => string[], then?: () => Promise<unknown>): void {
  if (saveData) return;
  const io = new IntersectionObserver(
    (entries) => {
      if (!entries.some((e) => e.isIntersecting)) return;
      io.disconnect();
      intent
        .then(loadPlayer)
        .then((p) => Promise.all([p.loadFonts(), ...urls().map((u) => p.loadCast(u))]))
        .then(() => then?.())
        .catch(() => {});
    },
    { rootMargin: "100% 0px" },
  );
  io.observe(el);
}

/** True while at least a third of `el` is on screen. */
export function watchInView(el: Element): () => boolean {
  let on = false;
  new IntersectionObserver(([e]) => (on = (e?.intersectionRatio ?? 0) >= 0.35), { threshold: [0, 0.35, 0.6] }).observe(el);
  return () => on;
}

/** A key meant for the page, not for a field or a shortcut. */
export function pageKey(e: KeyboardEvent): boolean {
  if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented || e.repeat) return false;
  const t = e.target instanceof Element ? e.target : null;
  return !t?.closest("input, textarea, select, [contenteditable]");
}

// Feature frames: a recording, a key and a button under the frame.
function setupFeature(frame: HTMLElement): void {
  const url = frame.dataset.cast;
  const key = frame.dataset.castKey;
  const holds = frame.dataset.castEnd === "hold";
  const img = frame.querySelector<HTMLImageElement>("img");
  const tray = frame.closest(".shot")?.querySelector<HTMLElement>(".cast-play");
  const button = tray?.querySelector<HTMLButtonElement>("button");
  if (!url || !img || !tray || !button) return;
  if (reduce.matches && !holds) return;
  tray.hidden = false;

  let stage: Stage | null | undefined;
  let cast: Cast | undefined;
  let busy = false;
  const ready = async () => {
    const p = await loadPlayer();
    cast ??= await p.loadCast(url);
    stage ??= await p.Stage.create(cast.cols, cast.rows, img.parentElement ?? frame);
    return stage;
  };

  const go = async () => {
    if (busy) return;
    busy = true;
    button.classList.add("is-playing");
    try {
      const s = await ready();
      if (!s || !cast) {
        tray.hidden = true; // no WebGL: the still stays, nothing to offer
        return;
      }
      s.place(img, boxFor(frame));
      if (reduce.matches) {
        await s.cue(cast, Infinity); // a cut to where it ends
        s.show();
        return;
      }
      await s.cue(cast, 0);
      s.show();
      const done = await s.play(cast);
      if (done && !holds) s.hide(); // its last frame is the still underneath
    } catch (err) {
      console.error(err);
      tray.hidden = true;
    } finally {
      busy = false;
      button.classList.remove("is-playing");
    }
  };

  button.addEventListener("click", () => void go());
  const inView = watchInView(frame);
  if (key) {
    document.addEventListener("keydown", (e) => {
      if (e.key !== key || !pageKey(e) || !inView()) return;
      e.preventDefault();
      button.classList.remove("is-pressed");
      button.getBoundingClientRect(); // Flush layout so the pressed flash restarts.
      button.classList.add("is-pressed");
      void go();
    });
  }
  warmWhenNear(frame, () => [url], ready);
  // Keep the player on its still when the layout changes (rotation, resize).
  new ResizeObserver(() => {
    if (stage?.root.classList.contains("is-on")) stage.place(img, boxFor(frame));
  }).observe(img);
}

for (const frame of document.querySelectorAll<HTMLElement>(".frame[data-cast][data-cast-key]")) setupFeature(frame);
