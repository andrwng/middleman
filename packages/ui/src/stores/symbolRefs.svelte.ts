import type { MiddlemanClient } from "../types.js";
import type { components } from "../api/generated/schema.js";

// Use the exact generated names Task 4 reported.
export type SymbolHit = components["schemas"]["SymbolHit"];

export type SymbolRefsStatus = "idle" | "loading" | "ready" | "error";

// The longest selection worth searching, matching the server's
// symbolRefsMaxQueryBytes.
const MAX_QUERY_LENGTH = 128;

// isSymbolQuery gates the "Refs" affordance: a searchable symbol is a
// single run of non-whitespace within the server's length bound. A
// multi-word or multi-line selection can never match a line-based
// word-boundary grep, so the button stays hidden for those.
export function isSymbolQuery(text: string): boolean {
  const t = text.trim();
  return t.length > 0 && t.length <= MAX_QUERY_LENGTH && !/\s/.test(t);
}

export interface SymbolRefsStoreOptions {
  client: MiddlemanClient;
}

// Symbol references for the symbol currently selected in a diff. search()
// greps the PR (via the server) for other occurrences; close() (e.g.
// dismissing the gutter) returns the store to idle. A monotonic ticket
// guards against out-of-order responses: a slow earlier search must never
// overwrite a newer one's results, and close() bumps the ticket too so
// anything still in flight is discarded on arrival.
export function createSymbolRefsStore(opts: SymbolRefsStoreOptions) {
  const client = opts.client;
  let query = $state("");
  let hits = $state<SymbolHit[]>([]);
  let inPrTotal = $state(0);
  let outsidePrTotal = $state(0);
  let truncated = $state(false);
  let status = $state<SymbolRefsStatus>("idle");
  let errorMsg = $state<string | null>(null);

  let seq = 0;

  function getQuery(): string {
    return query;
  }
  function getHits(): SymbolHit[] {
    return hits;
  }
  function getInPrTotal(): number {
    return inPrTotal;
  }
  function getOutsidePrTotal(): number {
    return outsidePrTotal;
  }
  function isTruncated(): boolean {
    return truncated;
  }
  function getStatus(): SymbolRefsStatus {
    return status;
  }
  function getError(): string | null {
    return errorMsg;
  }
  function isActive(): boolean {
    return status !== "idle";
  }

  async function search(
    owner: string,
    name: string,
    number: number,
    sha: string,
    rawQuery: string,
  ): Promise<void> {
    const q = rawQuery.trim();
    if (!isSymbolQuery(q)) return;
    const ticket = ++seq;
    query = q;
    status = "loading";
    errorMsg = null;
    hits = [];
    inPrTotal = 0;
    outsidePrTotal = 0;
    truncated = false;

    const { data, error: err } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/symbol-refs",
      { params: { path: { owner, name, number }, query: { q, sha } } },
    );
    if (ticket !== seq) return;
    if (err || !data) {
      status = "error";
      errorMsg = "Symbol search failed";
      return;
    }
    hits = data.hits ?? [];
    inPrTotal = data.in_pr_total ?? 0;
    outsidePrTotal = data.outside_pr_total ?? 0;
    truncated = data.truncated ?? false;
    status = "ready";
  }

  function close(): void {
    seq++;
    query = "";
    hits = [];
    inPrTotal = 0;
    outsidePrTotal = 0;
    truncated = false;
    status = "idle";
    errorMsg = null;
  }

  return {
    getQuery, getHits, getInPrTotal, getOutsidePrTotal, isTruncated,
    getStatus, getError, isActive, search, close,
  };
}

export type SymbolRefsStore = ReturnType<typeof createSymbolRefsStore>;
