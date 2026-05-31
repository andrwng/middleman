import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const createThreads = vi.fn(async () => true);
const clearDraft = vi.fn();
const draft = {
  comments: [{ id: 1, path: "a.go", side: "RIGHT", line: 12, commitSha: "abc", body: "x", inReplyTo: null }],
  event: "COMMENT",
  body: "",
};

vi.mock("../../context.js", () => ({
  getStores: () => ({
    diff: { getDraft: () => draft, clearDraft, getCommits: () => [] },
    pulls: { loadPulls: vi.fn() },
    reviewThreads: { createThreads, getError: () => null },
  }),
  getClient: () => ({ POST: vi.fn() }),
}));

import ReviewPanel from "./ReviewPanel.svelte";

afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("ReviewPanel mode picker (local)", () => {
  it("defaults to persist-only and submits without a mode", async () => {
    const { getByText } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    await fireEvent.click(getByText("Create review threads"));
    expect(createThreads).toHaveBeenCalledWith(expect.any(Array), undefined);
  });

  it("submits the selected mode and reflects it in the button", async () => {
    const { getByText, getByRole } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    await fireEvent.click(getByRole("radio", { name: /discuss first/i }));
    await fireEvent.click(getByText("Create & discuss"));
    expect(createThreads).toHaveBeenCalledWith(expect.any(Array), "discuss-first");
  });
});
