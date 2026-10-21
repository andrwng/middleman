import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import CollapseGlobsSection from "./CollapseGlobsSection.svelte";

function makeFile(path: string, old_path = path) {
  return {
    path,
    old_path,
    status: "modified",
    is_binary: false,
    is_whitespace_only: false,
    additions: 1,
    deletions: 0,
    hunks: [],
  };
}

function diffStub(globs: string[] = []) {
  return {
    getCollapseGlobs: () => globs,
    saveCollapseGlobs: vi.fn(async (_globs: string[]) => {}),
    getFileList: () => ({
      stale: false,
      files: [
        makeFile("api/gen/a.pb.go"),
        makeFile("api/gen/b.pb.go"),
        makeFile("src/new.go", "vendor/old.go"),
        makeFile("README.md"),
      ],
    }),
  };
}

function renderSection(diff = diffStub()) {
  render(CollapseGlobsSection, {
    context: new Map<symbol, unknown>([[STORES_KEY, { diff }]]),
  });
  return { diff };
}

async function openSection(): Promise<void> {
  const toggle = screen.getByRole("button", { name: /collapse globs/i });
  if (toggle.getAttribute("aria-expanded") !== "true") await fireEvent.click(toggle);
}

async function addGlob(value: string): Promise<void> {
  const input = screen.getByLabelText("Add collapse glob");
  await fireEvent.input(input, { target: { value } });
  await fireEvent.keyDown(input, { key: "Enter" });
}

afterEach(() => {
  cleanup();
});

describe("CollapseGlobsSection", () => {
  it("is closed by default when there are no globs", () => {
    renderSection();
    const toggle = screen.getByRole("button", { name: /collapse globs/i });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByLabelText("Add collapse glob")).toBeNull();
  });

  it("is open by default and shows match counts when globs exist", () => {
    renderSection(diffStub(["**/*.pb.go", "vendor/**", "docs/**"]));
    const toggle = screen.getByRole("button", { name: /collapse globs/i });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    const count = (glob: string) =>
      screen.getByText(glob).closest("li")?.querySelector(".globs__count")?.textContent;
    expect(count("**/*.pb.go")).toBe("2 files");
    // Matches the rename's old path.
    expect(count("vendor/**")).toBe("1 file");
    expect(count("docs/**")).toBe("0 files");
  });

  it("Enter adds a trimmed glob and saves the full list", async () => {
    const { diff } = renderSection(diffStub(["vendor/**"]));
    await openSection();
    await addGlob("  **/*.pb.go  ");
    expect(diff.saveCollapseGlobs).toHaveBeenCalledWith(["vendor/**", "**/*.pb.go"]);
  });

  it.each([
    ["empty input", "   ", "Enter a glob."],
    ["duplicate", "vendor/**", "vendor/** is already in the list."],
    ["too long", "a".repeat(513), "Globs can be at most 512 characters."],
  ])("rejects %s inline without saving", async (_label, value, message) => {
    const { diff } = renderSection(diffStub(["vendor/**"]));
    await openSection();
    await addGlob(value);
    expect(screen.getByRole("alert").textContent).toContain(message);
    expect(diff.saveCollapseGlobs).not.toHaveBeenCalled();
  });

  it("rejects adding past the 200-glob limit", async () => {
    const globs = Array.from({ length: 200 }, (_, i) => `g${i}/**`);
    const { diff } = renderSection(diffStub(globs));
    await openSection();
    await addGlob("more/**");
    expect(screen.getByRole("alert").textContent).toContain("At most 200 globs");
    expect(diff.saveCollapseGlobs).not.toHaveBeenCalled();
  });

  it("shows a server error inline", async () => {
    const diff = diffStub();
    diff.saveCollapseGlobs.mockRejectedValueOnce(new Error("glob too long"));
    renderSection(diff);
    await openSection();
    await addGlob("x/**");
    expect((await screen.findByRole("alert")).textContent).toContain("glob too long");
  });

  it("remove saves the list without that glob", async () => {
    const { diff } = renderSection(diffStub(["vendor/**", "docs/**"]));
    await fireEvent.click(screen.getByRole("button", { name: "Remove collapse glob vendor/**" }));
    expect(diff.saveCollapseGlobs).toHaveBeenCalledWith(["docs/**"]);
  });
});
