import { runs } from "../lib/typeset";

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

  // Fetch a screen before it is needed (hover, focus), so the swap is instant.
  const warm = (t: HTMLElement) => {
    const img = imgOf(t);
    if (img && img.loading !== "eager") img.loading = "eager";
    return img;
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
    if (tab.getAttribute("aria-selected") === "true") return;
    const mine = ++token;
    // Decode first so the cross-fade never shows a half-loaded frame.
    await warm(tab)?.decode().catch(() => {});
    if (mine !== token) return;

    const swap = () => {
      for (const t of tabs) {
        const on = t === tab;
        t.setAttribute("aria-selected", String(on));
        t.tabIndex = on ? 0 : -1;
        const panel = panelOf(t);
        if (panel) panel.hidden = !on;
      }
      caption.replaceChildren(
        ...runs(tab.dataset.caption ?? "").map((r) =>
          r.term ? Object.assign(document.createElement("i"), { textContent: r.text }) : r.text,
        ),
      );
    };
    if ("startViewTransition" in document && !reduce.matches) {
      // A newer keypress may supersede this transition; that is expected.
      const vt = document.startViewTransition(swap);
      vt.ready.catch(() => {});
      vt.finished.catch(() => {});
    } else swap();
  };

  for (const t of tabs) {
    t.addEventListener("click", () => { select(t).catch(console.error); });
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
    select(tab, { flash: true }).catch(console.error);
  });
}

const root = document.querySelector<HTMLElement>("[data-tour]");
if (root) setupTour(root);
