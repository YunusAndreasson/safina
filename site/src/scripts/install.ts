// Copy-to-clipboard with an in-place confirmation, and, once binaries are
// published, the visitor's own platform floated to the front.
const status = document.querySelector<HTMLElement>("[data-copy-status]");
for (const btn of document.querySelectorAll<HTMLButtonElement>("[data-copy]")) {
  let reset: ReturnType<typeof setTimeout> | undefined;
  const copy = async () => {
    const id = btn.dataset.copy;
    if (!id) return;
    const src = document.getElementById(id);
    const label = btn.querySelector<HTMLElement>("[data-copy-label]");
    if (!src || !label) return;
    const text = src.dataset.text ?? src.textContent.trim();
    clearTimeout(reset);
    try {
      await navigator.clipboard.writeText(text);
      label.textContent = "Copied";
      btn.dataset.state = "done";
      if (status) status.textContent = "Command copied to clipboard";
    } catch {
      label.textContent = "Select and copy";
    }
    // One timer per button: a second click restarts the confirmation
    // instead of having the first click's timer cut it short.
    reset = setTimeout(() => {
      label.textContent = "Copy";
      delete btn.dataset.state;
      if (status) status.textContent = "";
    }, 2200);
  };
  btn.addEventListener("click", () => { copy().catch(console.error); });
}

const list = document.querySelector("[data-downloads] ul");
if (list) {
  const ua = navigator.userAgent;
  const os = /Mac/i.test(ua) ? "macos" : /Linux|X11/i.test(ua) ? "linux" : null;
  const arm = /arm|aarch64/i.test(ua) || os === "macos";
  const mine = [...list.querySelectorAll<HTMLAnchorElement>("a")].find(
    (a) => a.dataset.os === os && (a.dataset.arch === "arm64") === arm,
  );
  if (mine?.parentElement) {
    mine.classList.replace("btn--quiet", "btn--beacon");
    list.prepend(mine.parentElement);
  }
}
