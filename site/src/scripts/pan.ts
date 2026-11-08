// On phones, a screenshot too wide to shrink scrolls sideways (Frame.astro).
// Where the payoff sits to the right (the new host in the diff, "2/2 ready"
// in the Logbook), open the scroll there, a column or so before it; the
// reader can still scroll back to the left edge. Without JavaScript the
// frame simply opens at the left.

const phone = matchMedia("(width <= 640px)");
const COLUMN = 8.5; // CSS px per terminal column on phones (Frame.astro)
const BORDER = 8; // the crop's padding, in CSS px

function openAtPayoff(): void {
  if (!phone.matches) return;
  for (const scroller of document.querySelectorAll<HTMLElement>(".frame__scroller[data-pan-start]")) {
    const column = Number(scroller.dataset.panStart);
    if (Number.isFinite(column)) scroller.scrollLeft = BORDER + (column - 1) * COLUMN;
  }
}

openAtPayoff();
phone.addEventListener("change", openAtPayoff);
