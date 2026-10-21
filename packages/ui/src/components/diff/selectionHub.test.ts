import { afterEach, describe, expect, it, vi } from "vitest";
import { subscribeSelection } from "./selectionHub.js";

function makeEl(text: string): HTMLElement {
  const el = document.createElement("div");
  el.textContent = text;
  document.body.appendChild(el);
  return el;
}

function select(el: Node | null): void {
  const sel = window.getSelection()!;
  sel.removeAllRanges();
  if (el) {
    const r = document.createRange();
    r.selectNodeContents(el);
    sel.addRange(r);
  }
  document.dispatchEvent(new Event("selectionchange"));
}

afterEach(() => {
  vi.restoreAllMocks();
  window.getSelection()?.removeAllRanges();
  document.body.innerHTML = "";
});

describe("subscribeSelection", () => {
  it("shares one document listener across subscribers, added lazily and removed with the last", () => {
    const add = vi.spyOn(document, "addEventListener");
    const remove = vi.spyOn(document, "removeEventListener");
    const count = (spy: typeof add) =>
      spy.mock.calls.filter(([type]) => type === "selectionchange").length;

    expect(count(add)).toBe(0);
    const a = subscribeSelection(() => makeEl("a"), () => {});
    const b = subscribeSelection(() => makeEl("b"), () => {});
    expect(count(add)).toBe(1);
    a();
    a(); // idempotent
    expect(count(remove)).toBe(0);
    b();
    expect(count(remove)).toBe(1);
    const c = subscribeSelection(() => makeEl("c"), () => {});
    expect(count(add)).toBe(2);
    c();
  });

  it("notifies only subscribers the selection is in, or was in before", () => {
    const a = makeEl("alpha");
    const b = makeEl("beta");
    const other = makeEl("other");
    const calls: string[] = [];
    const offA = subscribeSelection(() => a, () => calls.push("a"));
    const offB = subscribeSelection(() => b, () => calls.push("b"));

    select(a.firstChild);
    expect(calls).toEqual(["a"]);
    calls.length = 0;

    select(a.firstChild); // still inside a
    expect(calls).toEqual(["a"]);
    calls.length = 0;

    select(b.firstChild); // left a, entered b
    expect(calls.sort()).toEqual(["a", "b"]);
    calls.length = 0;

    select(other.firstChild); // left b; a was not involved
    expect(calls).toEqual(["b"]);
    calls.length = 0;

    select(other.firstChild); // nobody involved
    expect(calls).toEqual([]);

    offA();
    offB();
  });

  it("evaluates once on subscribe when the selection is already inside", () => {
    const a = makeEl("alpha");
    const b = makeEl("beta");
    select(a.firstChild);
    const fa = vi.fn();
    const fb = vi.fn();
    const offA = subscribeSelection(() => a, fa);
    const offB = subscribeSelection(() => b, fb);
    expect(fa).toHaveBeenCalledTimes(1);
    expect(fb).not.toHaveBeenCalled();
    select(b.firstChild); // a was inside, so it hears about leaving
    expect(fa).toHaveBeenCalledTimes(2);
    offA();
    offB();
  });

  it("stops notifying after unsubscribe", () => {
    const a = makeEl("alpha");
    const fn = vi.fn();
    const off = subscribeSelection(() => a, fn);
    off();
    select(a.firstChild);
    expect(fn).not.toHaveBeenCalled();
  });

  it("treats a missing element as outside", () => {
    const fn = vi.fn();
    const off = subscribeSelection(() => undefined, fn);
    select(makeEl("x").firstChild);
    expect(fn).not.toHaveBeenCalled();
    off();
  });
});
