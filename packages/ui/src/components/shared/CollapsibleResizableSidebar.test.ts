import { afterEach, describe, expect, it, vi } from "vitest";
import { createRawSnippet } from "svelte";
import {
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/svelte";
import CollapsibleResizableSidebar from "./CollapsibleResizableSidebar.svelte";

afterEach(() => cleanup());

function sidebarSnippet() {
  return createRawSnippet(() => ({
    render: () => `<div data-testid="sb">Sidebar content</div>`,
  }));
}

function childrenSnippet() {
  return createRawSnippet(() => ({
    render: () => `<div data-testid="main">main content</div>`,
  }));
}

describe("CollapsibleResizableSidebar — always-visible collapse chevron", () => {
  it("renders a collapse chevron when expanded and onCollapse is set", () => {
    render(CollapsibleResizableSidebar, {
      props: {
        isCollapsed: false,
        sidebarWidth: 320,
        showCollapsedStrip: true,
        onExpand: vi.fn(),
        onCollapse: vi.fn(),
        sidebar: sidebarSnippet(),
        children: childrenSnippet(),
      },
    });
    expect(screen.getByLabelText(/collapse sidebar/i)).toBeTruthy();
  });

  it("clicking the chevron calls onCollapse", async () => {
    const onCollapse = vi.fn();
    render(CollapsibleResizableSidebar, {
      props: {
        isCollapsed: false,
        sidebarWidth: 320,
        showCollapsedStrip: true,
        onExpand: vi.fn(),
        onCollapse,
        sidebar: sidebarSnippet(),
        children: childrenSnippet(),
      },
    });
    await fireEvent.click(screen.getByLabelText(/collapse sidebar/i));
    expect(onCollapse).toHaveBeenCalled();
  });

  it("does NOT render the chevron when onCollapse is undefined (back-compat)", () => {
    render(CollapsibleResizableSidebar, {
      props: {
        isCollapsed: false,
        sidebarWidth: 320,
        showCollapsedStrip: true,
        onExpand: vi.fn(),
        sidebar: sidebarSnippet(),
        children: childrenSnippet(),
      },
    });
    expect(screen.queryByLabelText(/collapse sidebar/i)).toBeNull();
  });

  it("does NOT render the chevron in sidebarOnly mode", () => {
    render(CollapsibleResizableSidebar, {
      props: {
        isCollapsed: false,
        sidebarWidth: 320,
        showCollapsedStrip: true,
        sidebarOnly: true,
        hasMain: false,
        onExpand: vi.fn(),
        onCollapse: vi.fn(),
        sidebar: sidebarSnippet(),
      },
    });
    expect(screen.queryByLabelText(/collapse sidebar/i)).toBeNull();
  });
});
