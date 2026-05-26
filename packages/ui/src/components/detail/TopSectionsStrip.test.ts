import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import TopSectionsStrip from "./TopSectionsStrip.svelte";

interface Props {
  onExpandAll: () => void;
  onPeek: (id: string) => void;
  peeked: string | null;
  pips: Array<{ id: string; label: string; muted: boolean }>;
}

beforeEach(() => { localStorage.clear(); });
afterEach(() => { cleanup(); });

describe("TopSectionsStrip", () => {
  it("renders one pip per section", () => {
    const props: Props = {
      onExpandAll: vi.fn(),
      onPeek: vi.fn(),
      peeked: null,
      pips: [
        { id: "cover", label: "cover", muted: false },
        { id: "msg", label: "message", muted: true },
        { id: "patchset", label: "patchset 2/3", muted: true },
        { id: "brief", label: "brief", muted: true },
      ],
    };
    render(TopSectionsStrip, { props });
    expect(screen.getByText("cover")).toBeTruthy();
    expect(screen.getByText("message")).toBeTruthy();
    expect(screen.getByText("patchset 2/3")).toBeTruthy();
    expect(screen.getByText("brief")).toBeTruthy();
  });

  it("clicking a pip calls onPeek with the id", async () => {
    const onPeek = vi.fn();
    render(TopSectionsStrip, {
      props: {
        onExpandAll: vi.fn(),
        onPeek,
        peeked: null,
        pips: [{ id: "cover", label: "cover", muted: false }],
      },
    });
    await fireEvent.click(screen.getByText("cover"));
    expect(onPeek).toHaveBeenCalledWith("cover");
  });

  it("clicking the leading chevron calls onExpandAll", async () => {
    const onExpandAll = vi.fn();
    render(TopSectionsStrip, {
      props: {
        onExpandAll,
        onPeek: vi.fn(),
        peeked: null,
        pips: [{ id: "cover", label: "cover", muted: false }],
      },
    });
    await fireEvent.click(screen.getByLabelText("Expand all sections"));
    expect(onExpandAll).toHaveBeenCalled();
  });

  it("marks the peeked pip with a peeked class", () => {
    const { container } = render(TopSectionsStrip, {
      props: {
        onExpandAll: vi.fn(),
        onPeek: vi.fn(),
        peeked: "cover",
        pips: [
          { id: "cover", label: "cover", muted: false },
          { id: "msg", label: "message", muted: true },
        ],
      },
    });
    expect(container.querySelector('[data-id="cover"].pip--peeked')).toBeTruthy();
    expect(container.querySelector('[data-id="msg"].pip--peeked')).toBeNull();
  });
});
