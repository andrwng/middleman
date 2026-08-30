import type { MiddlemanClient } from "../types.js";
import type { components } from "../api/generated/schema.js";

// Use the exact generated names Task 4 reported.
export type SymbolHit = components["schemas"]["SymbolHit"];

// "prompt" is the search box open with nothing searched yet: active (so
// the gutter mounts) but with no query, no hits and no request in
// flight. It sits between "idle" and "loading" in the lifecycle, and
// exists because isActive() is `status !== "idle"` -- a distinct
// non-idle status is all it takes to open a blank gutter, with no change
// to the mount condition in DiffView.
export type SymbolRefsStatus = "idle" | "prompt" | "loading" | "ready" | "error";

// The longest selection worth searching, matching the server's
// symbolRefsMaxQueryBytes: 128 bytes of UTF-8, not UTF-16 code units.
const MAX_QUERY_BYTES = 128;

// isSymbolQuery gates the "Refs" affordance: a searchable symbol is a
// single run of non-whitespace within the server's byte-length bound.
// A multi-word or multi-line selection can never match a line-based
// word-boundary grep, so the button stays hidden for those. The bound
// is measured in UTF-8 bytes (not `.length`, which counts UTF-16 code
// units) so a multi-byte symbol is judged the same way the server
// judges it, instead of being accepted here only to be rejected there.
export function isSymbolQuery(text: string): boolean {
  const t = text.trim();
  if (t.length === 0 || /\s/.test(t)) return false;
  return new TextEncoder().encode(t).length <= MAX_QUERY_BYTES;
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
  // The SHA the current (or most recent) search was run against. Lets a
  // caller notice when the diff scope's SHA has since moved out from
  // under an active search -- e.g. a background sync advancing the PR
  // head -- so the stale hits can be closed rather than left rendered
  // against line numbers that no longer match.
  let searchedSha = $state("");
  let hits = $state<SymbolHit[]>([]);
  let inPrTotal = $state(0);
  let outsidePrTotal = $state(0);
  let truncated = $state(false);
  // Which labeller produced hits[].kind/tag for the current result set:
  // "ctags" or "heuristic" (empty while idle/loading, matching the other
  // response-derived fields above).
  let classifier = $state("");
  let status = $state<SymbolRefsStatus>("idle");
  let errorMsg = $state<string | null>(null);

  // A monotonic "focus the search input" signal. The gutter watches it in
  // an $effect and focuses + selects its input on every change. A counter
  // rather than a boolean because the same request can legitimately arrive
  // twice in a row (pressing `s` while the box is already open should
  // refocus it), and a boolean would coalesce the second request away.
  let focusSeq = $state(0);

  let seq = 0;

  function getQuery(): string {
    return query;
  }
  function getSearchedSha(): string {
    return searchedSha;
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
  function getClassifier(): string {
    return classifier;
  }
  function getStatus(): SymbolRefsStatus {
    return status;
  }
  function getError(): string | null {
    return errorMsg;
  }
  function getFocusSeq(): number {
    return focusSeq;
  }
  function isActive(): boolean {
    return status !== "idle";
  }

  function detail(err: unknown, fallback: string): string {
    return (err as { detail?: string }).detail ?? fallback;
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
    searchedSha = sha;
    status = "loading";
    errorMsg = null;
    hits = [];
    inPrTotal = 0;
    outsidePrTotal = 0;
    truncated = false;
    classifier = "";

    const { data, error: err } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/symbol-refs",
      { params: { path: { owner, name, number }, query: { q, sha } } },
    );
    if (ticket !== seq) return;
    if (err || !data) {
      status = "error";
      errorMsg = err ? detail(err, "Symbol search failed") : "Symbol search failed";
      return;
    }
    hits = data.hits ?? [];
    inPrTotal = data.in_pr_total ?? 0;
    outsidePrTotal = data.outside_pr_total ?? 0;
    truncated = data.truncated ?? false;
    classifier = data.classifier ?? "heuristic";
    status = "ready";
  }

  // openBlank opens the search box. It moves an idle store to "prompt" so
  // the gutter mounts with an empty query, and always signals a focus
  // request.
  //
  // When the store is NOT idle it deliberately changes nothing but the
  // focus signal: reaching for the search box while results are on screen
  // must not discard them. The gutter seeds its input from getQuery() and
  // selects the text, so typing still replaces the query immediately.
  function openBlank(): void {
    if (status === "idle") {
      status = "prompt";
    }
    focusSeq++;
  }

  function close(): void {
    seq++;
    query = "";
    searchedSha = "";
    hits = [];
    inPrTotal = 0;
    outsidePrTotal = 0;
    truncated = false;
    classifier = "";
    status = "idle";
    errorMsg = null;
  }

  return {
    getQuery, getSearchedSha, getHits, getInPrTotal, getOutsidePrTotal, isTruncated,
    getClassifier, getStatus, getError, isActive, getFocusSeq, search, close, openBlank,
  };
}

export type SymbolRefsStore = ReturnType<typeof createSymbolRefsStore>;
