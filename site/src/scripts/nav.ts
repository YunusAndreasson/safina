// The nav marks the part of the page you're in (aria-current="location",
// Nav.astro): the last part whose top has passed 40% of the window. Above
// the deep dive nothing is marked, nor before the first scroll: a tall screen
// shows the deep dive's top above that line while you're still in the
// opening. Without JavaScript nothing is, and every link still goes where it
// says.

const nav = document.querySelector<HTMLElement>("[data-spy]");

function spy(root: HTMLElement): void {
  const marks = [...root.querySelectorAll<HTMLAnchorElement>("a[href^='#']:not(.wordmark)")].flatMap((link) => {
    const part = document.getElementById(link.hash.slice(1));
    return part ? [{ link, part }] : [];
  });
  // A part away from its link's anchor names it in data-nav ("#features"),
  // so Also on board, after Speed, still marks Features.
  for (const part of document.querySelectorAll<HTMLElement>("[data-nav]")) {
    const link = marks.find((m) => m.link.hash === part.dataset.nav)?.link;
    if (link) marks.push({ link, part });
  }
  marks.sort((a, b) => (a.part.compareDocumentPosition(b.part) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1));
  if (marks.length === 0) return;

  let current: HTMLAnchorElement | undefined;
  let queued = false;
  const update = () => {
    queued = false;
    const line = innerHeight * 0.4;
    let next: HTMLAnchorElement | undefined;
    if (scrollY > 0) for (const { link, part } of marks) if (part.getBoundingClientRect().top <= line) next = link;
    if (next === current) return;
    current?.removeAttribute("aria-current");
    next?.setAttribute("aria-current", "location");
    current = next;
  };
  const queue = () => {
    if (queued) return;
    queued = true;
    requestAnimationFrame(update);
  };
  addEventListener("scroll", queue, { passive: true });
  addEventListener("resize", queue);
  update();
}

if (nav) spy(nav);
