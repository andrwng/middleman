import { afterEach, describe, expect, it, vi } from "vitest";
import { createAIStore, type AIThread } from "./ai.svelte.js";

function thread(over: Partial<AIThread>): AIThread {
  return {
    id: 1, path: "a.go", anchor_side: "RIGHT", anchor_line: 1,
    ...over,
  } as AIThread;
}

const threads: AIThread[] = [
  thread({ id: 1, path: "a.go", anchor_side: "RIGHT", anchor_line: 5 }),
  thread({ id: 2, path: "a.go", anchor_side: "LEFT", anchor_line: 5 }),
  thread({ id: 3, path: "a.go", anchor_side: "RIGHT", anchor_line: 5 }),
  thread({ id: 4, path: "b.go", anchor_side: "RIGHT", anchor_line: 5 }),
  thread({ id: 5, path: "b.go", anchor_side: "LEFT", anchor_line: 9 }),
  thread({ id: 6, path: "a|b.go", anchor_side: "RIGHT", anchor_line: 1 }),
];

function stubFetch(list: AIThread[]): void {
  vi.stubGlobal("fetch", vi.fn(async () => ({
    ok: true,
    json: async () => ({ threads: list, questions: [] }),
  })));
}

async function storeWith(list: AIThread[]) {
  stubFetch(list);
  const store = createAIStore();
  store.start("o", "n", 1);
  await vi.waitFor(() => expect(store.all().threads).toHaveLength(list.length));
  return store;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

// The linear filter getThreadsAtAnchor used to run per rendered line.
function reference(all: AIThread[], path: string, line: number, side: "LEFT" | "RIGHT"): AIThread[] {
  return all.filter((t) => t.path === path && t.anchor_line === line && t.anchor_side === side);
}

describe("AI store anchor lookup", () => {
  it("matches the linear filter for every anchor across files and sides", async () => {
    const store = await storeWith(threads);
    for (const path of ["a.go", "b.go", "a|b.go", "c.go"]) {
      for (let line = 0; line <= 10; line++) {
        for (const side of ["LEFT", "RIGHT"] as const) {
          expect(store.getThreadsAtAnchor(path, line, side).map((t) => t.id))
            .toEqual(reference(threads, path, line, side).map((t) => t.id));
        }
      }
    }
    store.stop();
  });

  it("returns the same array for repeated lookups instead of filtering again", async () => {
    const store = await storeWith(threads);
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT"))
      .toBe(store.getThreadsAtAnchor("a.go", 5, "RIGHT"));
    expect(store.getThreadsAtAnchor("z.go", 1, "LEFT"))
      .toBe(store.getThreadsAtAnchor("y.go", 2, "RIGHT"));
    store.stop();
  });

  it("rebuilds when the threads change", async () => {
    const store = await storeWith(threads);
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT").map((t) => t.id)).toEqual([1, 3]);
    store.markThreadDeletedExternally(1);
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT").map((t) => t.id)).toEqual([3]);
    store.stop();
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT")).toEqual([]);
  });
});
