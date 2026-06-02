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

describe("ReviewPanel agent checkbox (local)", () => {
  it("defaults the agent checkbox ticked and submits act-immediately", async () => {
    const { getByText, getByRole } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    expect((getByRole("checkbox") as HTMLInputElement).checked).toBe(true);
    await fireEvent.click(getByText("Create & apply"));
    expect(createThreads).toHaveBeenCalledWith(expect.any(Array), "act-immediately");
  });

  it("submits persist-only when the agent checkbox is unticked", async () => {
    const { getByText, getByRole } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    await fireEvent.click(getByRole("checkbox"));
    await fireEvent.click(getByText("Create review threads"));
    expect(createThreads).toHaveBeenCalledWith(expect.any(Array), undefined);
  });

  it("does not offer a discuss-first option in the UI", () => {
    const { queryByText, queryByRole } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    expect(queryByRole("radio", { name: /discuss/i })).toBeNull();
    expect(queryByText(/discuss/i)).toBeNull();
  });
});
