import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, within } from "@testing-library/svelte";

const setLayout = vi.fn();
const setTabWidth = vi.fn();
const setHideWhitespace = vi.fn();
const viewState = { hideWhitespace: false };

vi.mock("../../context.js", () => ({
  getStores: () => ({
    diff: {
      getLayout: () => "unified",
      getTabWidth: () => 4,
      getHideWhitespace: () => viewState.hideWhitespace,
      setLayout,
      setTabWidth,
      setHideWhitespace,
    },
  }),
}));

import DiffViewMenu from "./DiffViewMenu.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  viewState.hideWhitespace = false;
});

describe("DiffViewMenu", () => {
  it("keeps the options closed until View is clicked", async () => {
    const { getByRole, queryByRole } = render(DiffViewMenu);
    const view = getByRole("button", { name: "View" });
    expect(queryByRole("group", { name: "View options" })).toBeNull();
    expect(view.getAttribute("aria-expanded")).toBe("false");

    await fireEvent.click(view);

    const group = getByRole("group", { name: "View options" });
    expect(view.getAttribute("aria-expanded")).toBe("true");
    for (const name of ["Unified", "Split", "1", "2", "4", "8"]) {
      expect(within(group).getByRole("button", { name })).toBeTruthy();
    }
    expect(within(group).getAllByRole("switch")).toHaveLength(1);
  });

  it("closes on a second click of View", async () => {
    const { getByRole, queryByRole } = render(DiffViewMenu);
    const view = getByRole("button", { name: "View" });
    await fireEvent.click(view);
    await fireEvent.click(view);
    expect(queryByRole("group", { name: "View options" })).toBeNull();
    expect(view.getAttribute("aria-expanded")).toBe("false");
  });

  it("closes on Escape", async () => {
    const { getByRole, queryByRole } = render(DiffViewMenu);
    await fireEvent.click(getByRole("button", { name: "View" }));
    await fireEvent.keyDown(document, { key: "Escape" });
    expect(queryByRole("group", { name: "View options" })).toBeNull();
  });

  it("closes on a mousedown outside, but not inside", async () => {
    const { getByRole, queryByRole } = render(DiffViewMenu);
    const view = getByRole("button", { name: "View" });
    await fireEvent.click(view);
    await fireEvent.mouseDown(document.body);
    expect(queryByRole("group", { name: "View options" })).toBeNull();

    await fireEvent.click(view);
    await fireEvent.mouseDown(getByRole("button", { name: "Split" }));
    expect(getByRole("group", { name: "View options" })).toBeTruthy();
  });

  // Several settings can be changed in one visit, so a change must not
  // close the popover.
  it("applies each setting immediately and stays open", async () => {
    const { getByRole } = render(DiffViewMenu);
    await fireEvent.click(getByRole("button", { name: "View" }));
    await fireEvent.click(getByRole("button", { name: "Split" }));
    await fireEvent.click(getByRole("button", { name: "2" }));
    await fireEvent.click(getByRole("switch"));
    expect(setLayout).toHaveBeenCalledWith("split");
    expect(setTabWidth).toHaveBeenCalledWith(2);
    expect(setHideWhitespace).toHaveBeenCalledWith(true);
    expect(getByRole("group", { name: "View options" })).toBeTruthy();
  });

  // Hiding whitespace persists across sessions and silently drops changes
  // from the diff, so with the switch tucked into the popover the closed
  // button has to say it is on.
  it("marks View only while whitespace changes are hidden", () => {
    const shown = render(DiffViewMenu);
    let view = shown.getByRole("button", { name: "View" });
    expect(view.querySelector(".view-btn__marker")).toBeNull();
    expect(view.getAttribute("title")).toBe("View options");
    cleanup();

    viewState.hideWhitespace = true;
    const hidden = render(DiffViewMenu);
    view = hidden.getByRole("button", { name: "View" });
    expect(view.querySelector(".view-btn__marker")).not.toBeNull();
    expect(view.getAttribute("title")).toBe("View options (whitespace changes are hidden)");
  });
});
