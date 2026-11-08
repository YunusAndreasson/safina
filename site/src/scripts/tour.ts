import { runs } from "../lib/typeset";
import { boxFor, loadPlayer, warmWhenNear } from "./scenes";
import type { Stage } from "./cast";

function setupTour(root: HTMLElement) {
  const tabs = [...root.querySelectorAll<HTMLButtonElement>('[role="tab"]')];
  const caption = root.querySelector<HTMLElement>("[data-caption-out]");
  const screen = root.querySelector<HTMLElement>(".tour__screen");
  const tablist = root.querySelector<HTMLElement>('[role="tablist"]');
  if (!caption || !screen || !tablist) return;
  const reduce = matchMedia("(prefers-reduced-motion: reduce)");
  const panelOf = (t: HTMLElement) => {
    const id = t.getAttribute("aria-controls");
    return id ? document.getElementById(id) : null;
  };
  const imgOf = (t: HTMLElement) => panelOf(t)?.querySelector("img");
  const frameOf = (t: HTMLElement) => panelOf(t)?.querySelector<HTMLElement>(".frame") ?? null;
  const castOf = (t: HTMLElement) => frameOf(t)?.dataset.cast;

  // Fetch a screen before it is needed (hover, focus), so the swap is instant.
  const warm = (t: HTMLElement) => {
    const img = imgOf(t);
    if (img && img.loading !== "eager") img.loading = "eager";
    return img;
  };

  // The key and its caption answer at once; the screen changes with it, or
  // when its recording is ready to play.
  const mark = (tab: HTMLButtonElement) => {
    for (const t of tabs) {
      const on = t === tab;
      t.setAttribute("aria-selected", String(on));
      t.tabIndex = on ? 0 : -1;
    }
    caption.replaceChildren(
      ...runs(tab.dataset.caption ?? "").map((r) =>
        r.term ? Object.assign(document.createElement("i"), { textContent: r.text }) : r.text,
      ),
    );
  };
  const reveal = (tab: HTMLButtonElement) => {
    for (const t of tabs) {
      const panel = panelOf(t);
      if (panel) panel.hidden = t !== tab;
    }
  };
  const swap = (tab: HTMLButtonElement) => {
    mark(tab);
    reveal(tab);
  };

  // A key's recording plays over the screen and ends on its stop's still.
  // Its first frame is drawn before the panels change, and the change and
  // the reveal happen in one paint, so the cut lands on the keypress.
  // One terminal for every stop (all shot at one size), made ahead of time
  // as the tour nears the screen, so a key plays at once.
  let stage: Stage | null | undefined;
  let staging: Promise<Stage | null> | undefined;
  const firstCast = () => tabs.map(castOf).find((u): u is string => Boolean(u));
  const ensureStage = () =>
    (staging ??= (async () => {
      const url = firstCast();
      if (!url) return null;
      const p = await loadPlayer();
      const cast = await p.loadCast(url);
      stage = await p.Stage.create(cast.cols, cast.rows, screen);
      return stage;
    })());
  const playScene = async (tab: HTMLButtonElement, mine: number): Promise<boolean> => {
    const url = castOf(tab);
    const frame = frameOf(tab);
    const img = imgOf(tab);
    if (!url || !frame || !img) return false;
    mark(tab);
    const p = await loadPlayer();
    const [cast, s] = await Promise.all([p.loadCast(url), ensureStage()]);
    if (mine !== token) return true;
    if (!s || s.cols !== cast.cols || s.rows !== cast.rows) return false;
    await s.cue(cast, 0);
    if (mine !== token) return true;
    reveal(tab);
    s.place(img, boxFor(frame));
    s.show();
    const done = await s.play(cast);
    if (done && mine === token) s.hide();
    return true;
  };

  let token = 0;
  const select = async (tab: HTMLButtonElement, opts: { focus?: boolean; flash?: boolean } = {}) => {
    if (!panelOf(tab)) return;
    if (opts.focus) tab.focus();
    if (opts.flash) {
      tab.classList.remove("is-pressed");
      tab.getBoundingClientRect(); // Flush layout so the pressed animation restarts.
      tab.classList.add("is-pressed");
    }
    const selected = tab.getAttribute("aria-selected") === "true";
    const recorded = Boolean(castOf(tab)) && !reduce.matches;
    if (selected && !recorded) return;
    const mine = ++token;
    if (recorded) {
      try {
        if (await playScene(tab, mine)) return;
      } catch (err) {
        console.error(err);
      }
      if (mine !== token || selected) return;
    }
    stage?.stop();
    stage?.hide();
    // Decode first so the cross-fade never shows a half-loaded frame.
    await warm(tab)?.decode().catch(() => {});
    if (mine !== token) return;
    if ("startViewTransition" in document && !reduce.matches) {
      // A newer keypress may supersede this transition; that is expected.
      const vt = document.startViewTransition(() => {
        swap(tab);
      });
      vt.ready.catch(() => {});
      vt.finished.catch(() => {});
    } else swap(tab);
  };

  warmWhenNear(
    screen,
    () => tabs.map(castOf).filter((u): u is string => Boolean(u)),
    () => (reduce.matches ? Promise.resolve() : ensureStage()),
  );
  new ResizeObserver(() => {
    const tab = tabs.find((t) => t.getAttribute("aria-selected") === "true");
    const img = tab && imgOf(tab);
    const frame = tab && frameOf(tab);
    if (stage?.root.classList.contains("is-on") && img && frame) stage.place(img, boxFor(frame));
  }).observe(screen);

  // The reel: until the visitor takes over, the deep dive plays itself, key
  // by key, each recording through to its still and a beat on it, so a
  // first look shows it's alive. It runs only while the keys are on screen
  // (the visitor sees which key "pressed"), the tab is visible, and no
  // pointer or focus is inside; Pause stops it, and so does any key or
  // click, for good. Never with reduced motion. Screen readers hear captions
  // only once the visitor is driving.
  const toggle = root.querySelector<HTMLButtonElement>("[data-autoplay]");
  const controls = root.querySelector<HTMLElement>(".tour__controls");
  let paused = reduce.matches || !toggle || !firstCast();
  let keysInView = false;
  let hovering = false;
  let focusWithin = false;
  let reeling = false;
  const canReel = () => !paused && keysInView && !hovering && !focusWithin && document.visibilityState === "visible";
  const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
  const reel = async () => {
    if (reeling) return;
    reeling = true;
    try {
      await wait(1200);
      while (canReel()) {
        const at = tabs.findIndex((t) => t.getAttribute("aria-selected") === "true");
        const next = tabs[(at + 1) % tabs.length];
        if (!next) break;
        await select(next, { flash: true });
        await wait(2600);
      }
    } finally {
      reeling = false;
    }
  };
  const kick = () => {
    if (canReel()) reel().catch(console.error);
  };
  const setPaused = (p: boolean) => {
    paused = p;
    if (toggle) toggle.textContent = p ? "Play" : "Pause";
    caption.setAttribute("aria-live", p ? "polite" : "off");
    kick();
  };
  const takeOver = () => {
    if (!paused) setPaused(true);
  };
  if (toggle && !paused) {
    toggle.hidden = false;
    caption.setAttribute("aria-live", "off");
    toggle.addEventListener("click", () => {
      setPaused(!paused);
    });
    root.addEventListener("pointerenter", () => (hovering = true));
    root.addEventListener("pointerleave", () => {
      hovering = false;
      kick();
    });
    root.addEventListener("focusin", (e) => (focusWithin = e.target !== toggle));
    root.addEventListener("focusout", () => {
      focusWithin = false;
      kick();
    });
    document.addEventListener("visibilitychange", kick);
    if (controls) {
      new IntersectionObserver(
        ([entry]) => {
          keysInView = (entry?.intersectionRatio ?? 0) >= 0.5;
          kick();
        },
        { threshold: [0, 0.5, 1] },
      ).observe(controls);
    }
  }

  for (const t of tabs) {
    t.addEventListener("click", () => {
      takeOver();
      select(t).catch(console.error);
    });
    t.addEventListener("pointerenter", () => warm(t), { passive: true });
    t.addEventListener("focus", () => warm(t));
  }

  // Arrow keys move along the tablist (roving tabindex).
  tablist.addEventListener("keydown", (ev) => {
    const active = document.activeElement;
    const i = active instanceof HTMLButtonElement ? tabs.indexOf(active) : -1;
    if (i < 0) return;
    const next =
      ev.key === "ArrowRight" ? tabs[(i + 1) % tabs.length]
      : ev.key === "ArrowLeft" ? tabs[(i - 1 + tabs.length) % tabs.length]
      : ev.key === "Home" ? tabs[0]
      : ev.key === "End" ? tabs[tabs.length - 1]
      : null;
    if (next) {
      ev.preventDefault();
      takeOver();
      select(next, { focus: true }).catch(console.error);
    }
  });

  // safina's own keys work here too, but only while the tour is on screen,
  // never while typing or with a modifier held.
  let inView = false;
  new IntersectionObserver(([entry]) => (inView = (entry?.intersectionRatio ?? 0) >= 0.35), {
    threshold: [0, 0.35, 0.6],
  }).observe(screen);

  const byKey = new Map<string, HTMLButtonElement>();
  for (const tab of tabs) {
    if (tab.dataset.key) byKey.set(tab.dataset.key, tab);
  }
  document.addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented || e.repeat) return;
    const target = e.target instanceof Element ? e.target : null;
    if (target?.closest("input, textarea, select, [contenteditable]")) return;
    const tab = byKey.get(e.key);
    if (!tab) return;
    const focusInside = root.contains(document.activeElement);
    if (!inView && !focusInside) return;
    // Enter keeps its normal meaning on links and buttons outside the tour.
    if (e.key === "Enter" && document.activeElement !== document.body && !focusInside) return;
    if (e.key === "Enter" && focusInside) return; // native button activation handles it
    e.preventDefault();
    takeOver();
    select(tab, { flash: true }).catch(console.error);
  });
}

const root = document.querySelector<HTMLElement>("[data-tour]");
if (root) setupTour(root);
