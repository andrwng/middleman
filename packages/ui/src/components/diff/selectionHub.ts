// One shared document `selectionchange` listener for every DiffFile.
//
// A diff with hundreds of files used to register one listener per file,
// and every selection change re-ran each file's selection parsing. Here
// the listener is added lazily with the first subscriber and removed with
// the last, and a subscriber is notified only when the selection's anchor
// or focus is inside its element, or was the last time it was notified,
// so it can clear what it derived from the old selection.
//
// DiffFile's selection readers only ever look at the anchor and focus
// nodes, so a file the selection neither touches nor touched has nothing
// to recompute.

interface Subscriber {
  getEl: () => Element | null | undefined;
  onChange: () => void;
  wasInside: boolean;
}

const subscribers = new Set<Subscriber>();
let listening = false;

function selectionTouches(el: Element, sel: Selection | null): boolean {
  if (!sel || sel.rangeCount === 0) return false;
  return (sel.anchorNode != null && el.contains(sel.anchorNode))
    || (sel.focusNode != null && el.contains(sel.focusNode));
}

function onSelectionChange(): void {
  const sel = typeof window !== "undefined" ? window.getSelection() : null;
  for (const sub of [...subscribers]) {
    if (!subscribers.has(sub)) continue;
    const el = sub.getEl();
    const inside = el ? selectionTouches(el, sel) : false;
    if (!inside && !sub.wasInside) continue;
    sub.wasInside = inside;
    sub.onChange();
  }
}

// Calls `onChange` on selection changes that involve `getEl()`'s element
// (see above), and once right away if the current selection already
// does, so a subscriber that arrives late (a body that just mounted)
// starts from the live selection. Returns an idempotent unsubscribe.
export function subscribeSelection(
  getEl: () => Element | null | undefined,
  onChange: () => void,
): () => void {
  if (typeof document === "undefined") return () => {};
  const sub: Subscriber = { getEl, onChange, wasInside: false };
  subscribers.add(sub);
  if (!listening) {
    document.addEventListener("selectionchange", onSelectionChange);
    listening = true;
  }
  const el = getEl();
  if (el && selectionTouches(el, window.getSelection())) {
    sub.wasInside = true;
    onChange();
  }
  return () => {
    if (!subscribers.delete(sub)) return;
    if (subscribers.size === 0 && listening) {
      document.removeEventListener("selectionchange", onSelectionChange);
      listening = false;
    }
  };
}
