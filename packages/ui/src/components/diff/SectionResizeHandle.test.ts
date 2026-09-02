import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/svelte";
import SectionResizeHandle from "./SectionResizeHandle.svelte";
import {
  SECTION_MIN_HEIGHT,
  SECTION_RESERVE_BELOW,
  clearSectionHeight,
  getSectionHeight,
} from "./sectionHeights.svelte.js";

afterEach(() => {
  cleanup();
  clearSectionHeight("commits");
  localStorage.clear();
  document.body.innerHTML = "";
});

// jsdom gives every element a zero-sized rect, so the heights the handle reads
// have to be stubbed. Keeping `as DOMRect` on the closing-paren line matters:
// on its own line, esbuild's ASI splits the expression.
function stubHeight(el: HTMLElement, height: number): void {
  el.getBoundingClientRect = () =>
    ({
      height,
      width: 0,
      top: 0,
      left: 0,
      right: 0,
      bottom: height,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    }) as DOMRect;
}

// Builds the section body the handle sizes. With `columnHeight` the body is
// nested in a stand-in for the sidebar column, which is what bounds a drag;
// without it the handle falls back to the viewport.
function makeBody(bodyHeight: number, columnHeight?: number): HTMLElement {
  const body = document.createElement("div");
  stubHeight(body, bodyHeight);
  if (columnHeight === undefined) {
    document.body.appendChild(body);
    return body;
  }
  const column = document.createElement("aside");
  column.className = "review-sidebar";
  stubHeight(column, columnHeight);
  column.appendChild(body);
  document.body.appendChild(column);
  return body;
}

// jsdom implements neither PointerEvent nor setPointerCapture. MouseEvent
// carries the only field the handle reads off the event, and the capture calls
// are optional-chained, so a plain bubbling MouseEvent drives the real code
// path end to end.
function firePointer(el: Element, type: string, clientY: number): void {
  el.dispatchEvent(new MouseEvent(type, { clientY, bubbles: true }));
}

function renderHandle(body: HTMLElement | null) {
  const { getByRole } = render(SectionResizeHandle, {
    props: { id: "commits" as const, body, label: "Resize commits" },
  });
  return getByRole("separator");
}

describe("SectionResizeHandle", () => {
  it("renders a horizontal separator carrying its label and a stable hook", () => {
    const handle = renderHandle(makeBody(200));
    expect(handle.getAttribute("aria-orientation")).toBe("horizontal");
    expect(handle.getAttribute("aria-label")).toBe("Resize commits");
    expect(handle.dataset.sectionResize).toBe("commits");
    expect(handle.getAttribute("title")).toContain("double-click to reset");
  });

  it("grows the section by the distance dragged down", () => {
    const handle = renderHandle(makeBody(200, 900));
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", 520);
    expect(getSectionHeight("commits")).toBe(320);
  });

  it("shrinks the section by the distance dragged up", () => {
    const handle = renderHandle(makeBody(200, 900));
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", 330);
    expect(getSectionHeight("commits")).toBe(130);
  });

  it("tracks a drag across successive moves rather than accumulating them", () => {
    const handle = renderHandle(makeBody(200, 900));
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", 450);
    firePointer(handle, "pointermove", 500);
    // Both moves measure from the press, so the second lands at +100, not +150.
    expect(getSectionHeight("commits")).toBe(300);
  });

  it("stops shrinking at the floor", () => {
    const handle = renderHandle(makeBody(200, 900));
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", -5000);
    expect(getSectionHeight("commits")).toBe(SECTION_MIN_HEIGHT);
  });

  it("caps growth against the measured column, not the viewport", () => {
    // A 400px column leaves 400 - 120 = 280, well under the jsdom viewport's
    // 768 - 120 = 648, so a runaway drag proves which one bounded it.
    const handle = renderHandle(makeBody(200, 400));
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", 5000);
    expect(getSectionHeight("commits")).toBe(400 - SECTION_RESERVE_BELOW);
  });

  it("persists on release, not on every move", () => {
    const handle = renderHandle(makeBody(200, 900));
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", 520);
    expect(localStorage.getItem("pr-section-height:commits")).toBeNull();
    firePointer(handle, "pointerup", 520);
    expect(localStorage.getItem("pr-section-height:commits")).toBe("320");
  });

  it("persists the last height when the pointer is cancelled mid-drag", () => {
    const handle = renderHandle(makeBody(200, 900));
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", 520);
    firePointer(handle, "pointercancel", 520);
    expect(localStorage.getItem("pr-section-height:commits")).toBe("320");
  });

  it("ignores moves that are not part of a drag", () => {
    const handle = renderHandle(makeBody(200, 900));
    firePointer(handle, "pointermove", 900);
    expect(getSectionHeight("commits")).toBeNull();
  });

  it("ignores moves after the drag has ended", () => {
    const handle = renderHandle(makeBody(200, 900));
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", 520);
    firePointer(handle, "pointerup", 520);
    firePointer(handle, "pointermove", 700);
    expect(getSectionHeight("commits")).toBe(320);
  });

  it("resets the section to its default height on double-click", () => {
    const handle = renderHandle(makeBody(200, 900));
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", 520);
    firePointer(handle, "pointerup", 520);
    handle.dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    expect(getSectionHeight("commits")).toBeNull();
    expect(localStorage.getItem("pr-section-height:commits")).toBeNull();
  });

  it("stays inert until the parent has bound a body element", () => {
    const handle = renderHandle(null);
    firePointer(handle, "pointerdown", 400);
    firePointer(handle, "pointermove", 520);
    firePointer(handle, "pointerup", 520);
    expect(getSectionHeight("commits")).toBeNull();
  });
});
