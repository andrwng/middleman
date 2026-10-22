import type {
  DiffFile,
  DiffResult,
  FilesResult,
  CommitInfo,
} from "../api/types.js";
import type { MiddlemanClient } from "../types.js";
import { apiErrorMessage } from "../api/errors.js";
import { createReviewDraftsApi, DraftApiError, type PRRef, type ServerDraft } from "./reviewDraftsApi.js";
import {
  LAZY_DIFF_LINES,
  compileGlob,
  resolveCollapse,
  type CollapseReason,
  type GlobMatcher,
} from "../utils/diffCollapse.js";
import {
  HunkLoader,
  MissingHunksError,
  type HunkState,
} from "./hunkLoader.js";
import {
  loadCollapseOverrides,
  saveCollapseOverrides,
  type CollapseOverrides,
} from "./collapseOverrides.js";

const COLLAPSE_GLOBS_PATH =
  "/repos/{owner}/{name}/pulls/{number}/diff-collapse-globs" as const;
const DIFF_HUNKS_PATH =
  "/repos/{owner}/{name}/pulls/{number}/diff/hunks" as const;

// Summary returned by the scoped /files endpoint: the file list with
// per-file counts but no hunks, plus the resolved revisions the lazy
// hunk requests pin against.
interface DiffSummary extends FilesResult {
  whitespace_only_count: number;
  diff_from: string;
  diff_to: string;
}

// Thrown by the hunk fetch when the server reports that the scope now
// resolves to different revisions than the summary (HTTP 409).
class HunksConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "HunksConflictError";
  }
}

function sortFilesByPath<T extends { files?: DiffFile[] }>(result: T): T {
  if (result.files) {
    result.files = [...result.files].sort((a, b) =>
      a.path.localeCompare(b.path),
    );
  }
  return result;
}

export type DiffScope =
  | { kind: "head" }
  | { kind: "commit"; sha: string }
  | { kind: "range"; fromSha: string; toSha: string }
  | { kind: "unreviewed" }
  | { kind: "patchsets"; fromNumber: number; toNumber: number };

export interface Patchset {
  id: number;
  number: number;
  head_sha: string;
  base_sha: string;
  merge_base_sha: string;
  observed_at: string;
}

export interface DiffStoreOptions {
  client: MiddlemanClient;
  getBasePath?: () => string;
}

function safeGetItem(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function safeSetItem(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* ignore */
  }
}

const VALID_TAB_WIDTHS = [1, 2, 4, 8];

function loadTabWidth(): number {
  const raw = parseInt(
    safeGetItem("diff-tab-width") ?? "4",
    10,
  );
  return VALID_TAB_WIDTHS.includes(raw) ? raw : 4;
}

export type DiffLayout = "unified" | "split";

function loadLayout(): DiffLayout {
  const raw = safeGetItem("diff-layout");
  return raw === "split" ? "split" : "unified";
}

function loadReviewedCommits(): Record<string, string[]> {
  try {
    const raw = safeGetItem("diff-reviewed-commits");
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
    const result: Record<string, string[]> = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (Array.isArray(value) && value.every((v) => typeof v === "string")) {
        result[key] = value as string[];
      }
    }
    return result;
  } catch {
    return {};
  }
}

function saveReviewedCommits(rc: Record<string, string[]>): void {
  safeSetItem("diff-reviewed-commits", JSON.stringify(rc));
}

// Per-PR draft review state. Persisted server-side through
// reviewDraftsApi; the in-memory copy is optimistic so the draft API
// stays synchronous for callers. commit_id on each comment is critical —
// GitHub refuses to anchor review comments unless the referenced
// commit is part of the PR's history.
export type ReviewEvent = "APPROVE" | "COMMENT" | "REQUEST_CHANGES";

export interface DraftComment {
  id: string;          // local UUID, used for delete/update
  path: string;
  line: number;
  side: "LEFT" | "RIGHT";
  startLine?: number;  // for multi-line ranges
  commitSha: string;   // the commit scope the comment was written against
  body: string;
  createdAt: string;   // ISO timestamp, for ordering
  // When set, this draft is a reply to an upstream (already-published)
  // review comment. At submit time the path/line/side/commit_sha are
  // ignored in favor of the parent thread's anchor — we still keep
  // them here so the draft renders in the right spot in the diff UI.
  inReplyTo?: number;
  // GitHub's error from the last publish attempt, when this draft
  // blocked it. Cleared by the next body edit.
  submitError?: string;
  // Set when the server never accepted this draft. The draft is kept
  // locally and saved again before publish.
  syncError?: string;
  // "claude" for comments that started as a Claude suggestion.
  origin?: "user" | "claude";
  // Claude's reasoning when it proposed the comment (never published).
  rationale?: string;
  runId?: number;
}

export interface DraftReview {
  body: string;
  event: ReviewEvent;
  comments: DraftComment[];
  // Claude suggestions not yet added to drafts. Never published.
  suggestions: DraftComment[];
}

function emptyDraft(): DraftReview {
  return { body: "", event: "COMMENT", comments: [], suggestions: [] };
}

function loadDraftReviews(): Record<string, DraftReview> {
  try {
    const raw = safeGetItem("diff-draft-reviews");
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      return {};
    }
    const result: Record<string, DraftReview> = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (!value || typeof value !== "object") continue;
      const v = value as Partial<DraftReview>;
      if (!Array.isArray(v.comments)) continue;
      const event: ReviewEvent =
        v.event === "APPROVE" || v.event === "REQUEST_CHANGES" ? v.event : "COMMENT";
      result[key] = {
        body: typeof v.body === "string" ? v.body : "",
        event,
        suggestions: [],
        comments: v.comments.filter(
          (c): c is DraftComment =>
            !!c &&
            typeof (c as DraftComment).id === "string" &&
            typeof (c as DraftComment).path === "string" &&
            typeof (c as DraftComment).line === "number" &&
            typeof (c as DraftComment).body === "string" &&
            typeof (c as DraftComment).commitSha === "string",
        ),
      };
    }
    return result;
  } catch {
    return {};
  }
}

function fromServerDraft(d: ServerDraft): DraftComment {
  return {
    id: d.id,
    path: d.path,
    line: d.line,
    side: d.side,
    ...(d.start_line != null ? { startLine: d.start_line } : {}),
    commitSha: d.commit_sha,
    body: d.body,
    createdAt: d.created_at,
    ...(d.in_reply_to != null ? { inReplyTo: d.in_reply_to } : {}),
    ...(d.submit_error ? { submitError: d.submit_error } : {}),
    origin: d.origin,
    ...(d.rationale ? { rationale: d.rationale } : {}),
    ...(d.run_id != null ? { runId: d.run_id } : {}),
  };
}

function toDraftWrite(c: DraftComment) {
  return {
    id: c.id,
    path: c.path,
    side: c.side,
    line: c.line,
    ...(c.startLine != null ? { start_line: c.startLine } : {}),
    commit_sha: c.commitSha,
    ...(c.inReplyTo != null ? { in_reply_to: c.inReplyTo } : {}),
    body: c.body,
    created_at: c.createdAt,
  };
}

function draftKeyFor(pr: PRRef): string {
  return `${pr.owner}/${pr.name}#${pr.number}`;
}

function parseDraftKey(key: string): PRRef | null {
  const m = key.match(/^(.+)\/(.+)#(\d+)$/);
  return m ? { owner: m[1]!, name: m[2]!, number: Number(m[3]) } : null;
}

function loadReviewedFiles(): Record<string, string[]> {
  try {
    const raw = safeGetItem("diff-reviewed-files");
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
    const result: Record<string, string[]> = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (Array.isArray(value) && value.every((v) => typeof v === "string")) {
        result[key] = value as string[];
      }
    }
    return result;
  } catch {
    return {};
  }
}

function saveReviewedFiles(rf: Record<string, string[]>): void {
  safeSetItem("diff-reviewed-files", JSON.stringify(rf));
}

export function createDiffStore(opts: DiffStoreOptions) {
  const apiClient = opts.client;
  const getBasePath = opts.getBasePath ?? (() => "/");

  let diff = $state<DiffResult | null>(null);
  let loading = $state(false);
  let storeError = $state<string | null>(null);
  let abortController: AbortController | null = null;

  // The scoped /files summary for the current load (file list + counts,
  // no hunks). In lazy mode it is the authoritative file list.
  let fileList = $state<DiffSummary | null>(null);
  let fileListLoading = $state(false);

  // --- lazy mode ---
  // Lazy mode: the summary totals more than LAZY_DIFF_LINES changed lines,
  // so `diff.files` starts as the summary (empty hunks) and HunkLoader
  // fills individual files in place as they are requested.
  let lazy = $state(false);
  // Set when lazily loaded hunks disagree with the summary (counts differ,
  // a file is missing, or the server answered 409). Cleared on reload.
  let worktreeChanged = $state(false);
  // HunkLoader is framework-free; bumping this on every state transition
  // makes getFileHunkState reactive.
  let hunkVersion = $state(0);
  let hunkLoader: HunkLoader | null = null;
  // path -> index in diff.files, and path -> raw summary entry, for the
  // current lazy load.
  let lazyIndex = new Map<string, number>();
  let summaryByPath = new Map<string, DiffFile>();

  let tabWidth = $state(loadTabWidth());
  let hideWhitespace = $state(
    safeGetItem("diff-hide-whitespace") === "true",
  );
  let layout = $state<DiffLayout>(loadLayout());
  let collapseOverrides = $state<CollapseOverrides>(loadCollapseOverrides());
  // Collapse globs per route key ("owner/name#number"), fetched once per
  // route. Globs are per repo server-side, but the client cannot tell
  // which routes share a repo (local worktrees are "local/<basename>",
  // and a PR and a worktree of the same repo use different owners), so
  // it caches per route and drops every entry on save.
  // Raw state so each list keeps its identity for the compiled cache.
  let globsByRepo = $state.raw<Record<string, string[]>>({});
  const globsInflight = new Map<string, Promise<void>>();
  const compiledGlobs = new WeakMap<string[], GlobMatcher[]>();
  // O(1) path -> file metadata for the collapse rules, rebuilt only when
  // the underlying file list changes identity.
  let metaSource: DiffFile[] | null = null;
  let metaByPath = new Map<string, DiffFile>();
  let activeFile = $state<string | null>(null);
  let scrollTarget = $state<string | null>(null);
  let scrolling = $state(false);
  // Patchsets are the distinct (head_sha, base_sha) pairs sync has
  // observed for the PR — Gerrit-style PSn chips. Loaded lazily on
  // first paint of the review surface; null = "not yet fetched".
  let patchsets = $state<Patchset[] | null>(null);
  let patchsetsLoading = $state(false);
  let patchsetsError = $state<string | null>(null);

  let commits = $state<CommitInfo[] | null>(null);
  let commitsLoading = $state(false);
  let commitsError = $state<string | null>(null);

  // Reviewer-local scratchpad per PR. `notesLoaded` distinguishes
  // "not yet fetched" from "empty on purpose" so the UI doesn't
  // flash an empty textarea before the GET returns.
  let notesContent = $state("");
  let notesUpdatedAt = $state<string | null>(null);
  let notesLoaded = $state(false);
  let notesLoading = $state(false);
  let notesSaving = $state(false);
  let notesError = $state<string | null>(null);
  let notesSaveTimer: ReturnType<typeof setTimeout> | null = null;
  // Generation counter so late responses from a previous PR don't
  // overwrite state after the user has switched away.
  let notesGen = 0;
  let scope = $state<DiffScope>({ kind: "head" });
  // The commit a shift-click spans from: the last one selected on its own.
  // Kept beside the scope rather than inside it because a span normalizes to
  // (older, newer) and so cannot say which end the reader started from.
  let rangeAnchorSha = $state<string | null>(null);

  let currentOwner = $state("");
  let currentName = $state("");
  let currentNumber = $state(0);
  let reviewedCommits = $state<Record<string, string[]>>(loadReviewedCommits());
  let reviewedFiles = $state<Record<string, string[]>>(loadReviewedFiles());
  let draftReviews = $state<Record<string, DraftReview>>({});
  let draftSyncError = $state<string | null>(null);
  // Per-PR publishable-draft counts for PRs whose drafts are not
  // loaded; fetched lazily by hasDraftForPR. summaryRequested is plain
  // (not $state) because hasDraftForPR runs inside $derived, where
  // writing state throws state_unsafe_mutation.
  let draftSummary = $state<Record<string, number>>({});
  let summaryRequested = false;
  const draftsApi = createReviewDraftsApi(getBasePath);
  // Bumped per drafts load so a late response for a PR the user has
  // left is dropped.
  let draftsGen = 0;
  let importPromise: Promise<void> | null = null;
  // Creates the server has not answered yet, by draft id. Removes and
  // edits wait on these so the server never ends up with a draft the
  // user already deleted.
  const pendingCreates = new Map<string, Promise<unknown>>();
  const inflight = new Set<Promise<unknown>>();
  // Draft mutations (create/edit/delete/meta) still in flight, and a
  // per-PR counter bumped by every local mutation. A list response
  // that started before a mutation is stale: it may predate a delete or
  // edit, so it is discarded and re-fetched once the writes land.
  const writes = new Set<Promise<unknown>>();
  const mutationSeq = new Map<string, number>();
  let metaTimer: ReturnType<typeof setTimeout> | null = null;
  let metaPendingFor: PRRef | null = null;

  function track<T>(p: Promise<T>): Promise<T> {
    inflight.add(p);
    void p.then(
      () => inflight.delete(p),
      () => inflight.delete(p),
    );
    return p;
  }

  function trackWrite<T>(p: Promise<T>): Promise<T> {
    writes.add(p);
    void p.then(
      () => writes.delete(p),
      () => writes.delete(p),
    );
    return track(p);
  }

  function bumpDrafts(key: string): void {
    mutationSeq.set(key, (mutationSeq.get(key) ?? 0) + 1);
  }

  async function whenDraftsSettled(): Promise<void> {
    while (inflight.size > 0) {
      await Promise.allSettled([...inflight]);
    }
  }

  async function whenWritesSettled(): Promise<void> {
    while (writes.size > 0) {
      await Promise.allSettled([...writes]);
    }
  }

  function errorText(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  function setSyncError(key: string, ids: string[], msg: string | undefined): void {
    const existing = draftReviews[key];
    if (!existing) return;
    const idSet = new Set(ids);
    draftReviews = {
      ...draftReviews,
      [key]: {
        ...existing,
        comments: existing.comments.map((c) => {
          if (!idSet.has(c.id)) return c;
          const { syncError: _old, ...rest } = c;
          return msg ? { ...rest, syncError: msg } : rest;
        }),
      },
    };
  }

  // retryUnsaved saves drafts the server never accepted. Returns null
  // when everything is saved, otherwise the error that blocked it.
  async function retryUnsaved(pr: PRRef): Promise<{ count: number; error: string } | null> {
    const key = draftKeyFor(pr);
    const unsaved = (draftReviews[key]?.comments ?? []).filter((c) => c.syncError);
    if (unsaved.length === 0) return null;
    const ids = unsaved.map((c) => c.id);
    try {
      await trackWrite(draftsApi.create(pr, unsaved.map(toDraftWrite)));
      setSyncError(key, ids, undefined);
      return null;
    } catch (err) {
      setSyncError(key, ids, errorText(err));
      return { count: unsaved.length, error: errorText(err) };
    }
  }

  function refreshSummary(): void {
    summaryRequested = true;
    track(
      draftsApi
        .summary()
        .then((prs) => {
          const next: Record<string, number> = {};
          for (const p of prs) next[`${p.owner}/${p.name}#${p.number}`] = p.count;
          draftSummary = next;
        })
        .catch(() => {}),
    );
  }

  // forgetSummaryFor drops a PR's cached count after its drafts were
  // published or cleared, and refetches if anything reads the summary.
  function forgetSummaryFor(key: string): void {
    if (key in draftSummary) {
      const next = { ...draftSummary };
      delete next[key];
      draftSummary = next;
    }
    if (summaryRequested) refreshSummary();
  }

  function currentPRRef(): PRRef | null {
    return currentOwner ? { owner: currentOwner, name: currentName, number: currentNumber } : null;
  }

  function syncFailed(err: unknown): void {
    draftSyncError = err instanceof Error ? err.message : String(err);
  }

  // importLegacyEntry moves one PR's localStorage drafts to the server.
  // Returns the part that must stay in localStorage for a later retry,
  // or null when nothing is left. A PR the server no longer knows (404)
  // is dropped; a batch rejected as invalid (400) is retried one draft
  // at a time so only the invalid drafts are dropped.
  async function importLegacyEntry(pr: PRRef, d: DraftReview): Promise<DraftReview | null> {
    try {
      if (d.comments.length > 0) await draftsApi.create(pr, d.comments.map(toDraftWrite));
    } catch (err) {
      if (err instanceof DraftApiError && err.status === 404) return null;
      if (!(err instanceof DraftApiError && err.status === 400)) return d;
      const kept: DraftComment[] = [];
      for (const c of d.comments) {
        try {
          await draftsApi.create(pr, [toDraftWrite(c)]);
        } catch (one) {
          if (!(one instanceof DraftApiError && (one.status === 400 || one.status === 404))) kept.push(c);
        }
      }
      if (kept.length > 0) return { ...d, comments: kept };
    }
    if (d.body || d.event !== "COMMENT") {
      try {
        await draftsApi.putMeta(pr, d.body, d.event);
      } catch (err) {
        if (!(err instanceof DraftApiError && (err.status === 400 || err.status === 404))) {
          return { ...d, comments: [] };
        }
      }
    }
    return null;
  }

  // One-time move of drafts written by older builds into the server.
  // Each PR is removed from localStorage as soon as it imports, so a
  // later retry never re-posts drafts the user has since deleted or
  // published. The server ignores ids it already has.
  function importLegacyDrafts(): Promise<void> {
    if (importPromise) return importPromise;
    importPromise = track((async () => {
      const legacy = loadDraftReviews();
      const keys = Object.keys(legacy);
      if (keys.length === 0) return;
      const remaining: Record<string, DraftReview> = {};
      for (const key of keys) {
        const pr = parseDraftKey(key);
        if (!pr) continue;
        const left = await importLegacyEntry(pr, legacy[key]!);
        if (left) remaining[key] = left;
      }
      try {
        if (Object.keys(remaining).length === 0) localStorage.removeItem("diff-draft-reviews");
        else localStorage.setItem("diff-draft-reviews", JSON.stringify(remaining));
      } catch {
        // storage unavailable; the import will just run again
      }
      if (Object.keys(remaining).length > 0) {
        draftSyncError = "Some drafts from this browser could not be imported; they will retry on reload";
      }
    })());
    return importPromise;
  }

  function loadDraftsFor(pr: PRRef): Promise<void> {
    const gen = ++draftsGen;
    const key = draftKeyFor(pr);
    return track((async () => {
      await importLegacyDrafts();
      try {
        const seqAtStart = mutationSeq.get(key) ?? 0;
        const res = await draftsApi.list(pr);
        if (gen !== draftsGen) return;
        if ((mutationSeq.get(key) ?? 0) !== seqAtStart) {
          // The snapshot may predate a local delete or edit. Fetch again
          // once those writes land, unless the user has moved on.
          await whenWritesSettled();
          if (gen === draftsGen) await loadDraftsFor(pr);
          return;
        }
        const server = res.drafts.filter((d) => d.state === "draft").map(fromServerDraft);
        const suggestions = res.drafts.filter((d) => d.state === "suggestion").map(fromServerDraft);
        const local = draftReviews[key]?.comments ?? [];
        const pendingLocal = local.filter(
          (c) => (pendingCreates.has(c.id) || c.syncError) && !server.some((sd) => sd.id === c.id),
        );
        // A summary/event edit not yet flushed is newer than what the
        // server has; keep it rather than clobbering the user's typing.
        const localMeta = metaPendingFor && draftKeyFor(metaPendingFor) === key ? draftReviews[key] : undefined;
        const event: ReviewEvent = localMeta
          ? localMeta.event
          : res.meta.event === "APPROVE" || res.meta.event === "REQUEST_CHANGES"
            ? res.meta.event
            : "COMMENT";
        draftReviews = {
          ...draftReviews,
          [key]: {
            body: localMeta ? localMeta.body : res.meta.body,
            event,
            comments: [...server, ...pendingLocal],
            suggestions,
          },
        };
      } catch (err) {
        if (gen === draftsGen) syncFailed(err);
      }
    })());
  }

  function reloadDrafts(): Promise<void> {
    const pr = currentPRRef();
    return pr ? loadDraftsFor(pr) : Promise.resolve();
  }

  function flushMeta(): void {
    if (metaTimer) {
      clearTimeout(metaTimer);
      metaTimer = null;
    }
    const pr = metaPendingFor;
    metaPendingFor = null;
    if (!pr) return;
    const d = draftReviews[draftKeyFor(pr)] ?? emptyDraft();
    trackWrite(draftsApi.putMeta(pr, d.body, d.event).catch(syncFailed));
  }

  function scheduleMeta(): void {
    const pr = currentPRRef();
    if (!pr) return;
    if (metaPendingFor && draftKeyFor(metaPendingFor) !== draftKeyFor(pr)) flushMeta();
    metaPendingFor = pr;
    if (metaTimer) clearTimeout(metaTimer);
    metaTimer = setTimeout(flushMeta, 400);
  }

  function removeOnServer(pr: PRRef, ids: string[]): void {
    if (ids.length === 0) return;
    bumpDrafts(draftKeyFor(pr));
    trackWrite(
      (async () => {
        await Promise.allSettled(ids.map((id) => pendingCreates.get(id) ?? Promise.resolve()));
        await draftsApi.remove(pr, ids);
      })().catch(syncFailed),
    );
  }

  function getCurrentPR(): { owner: string; name: string; number: number } | null {
    if (!currentOwner) return null;
    return { owner: currentOwner, name: currentName, number: currentNumber };
  }

  function setActivePR(owner: string, name: string, number: number): void {
    const changed = owner !== currentOwner || name !== currentName || number !== currentNumber;
    if (changed) flushMeta();
    currentOwner = owner;
    currentName = name;
    currentNumber = number;
    if (changed && owner) void loadDraftsFor({ owner, name, number });
  }

  // --- reads ---

  function getDiff(): DiffResult | null {
    return diff;
  }
  function isDiffLoading(): boolean {
    return loading;
  }
  function getDiffError(): string | null {
    return storeError;
  }
  function getFileList(): FilesResult | null {
    // In lazy mode the summary is authoritative (same scope and
    // whitespace params as the hunks) and, unlike diff.files, does not
    // change as hunks load in.
    if (lazy && fileList) {
      return { stale: fileList.stale, files: fileList.files ?? [] };
    }
    // Otherwise prefer diff.files once available — it is authoritative.
    // The /files summary is a fast preview used until the diff arrives.
    if (diff) return { stale: diff.stale, files: diff.files ?? [] };
    if (fileList) return { stale: fileList.stale, files: fileList.files ?? [] };
    return null;
  }
  function isFileListLoading(): boolean {
    // Show loading until we have *some* file data. When /files fails
    // but /diff is still in flight, keep showing loading state.
    return !diff && (fileListLoading || loading);
  }
  function getTabWidth(): number {
    return tabWidth;
  }
  function getHideWhitespace(): boolean {
    return hideWhitespace;
  }
  function getLayout(): DiffLayout {
    return layout;
  }
  function getActiveFile(): string | null {
    return activeFile;
  }
  function isScrolling(): boolean {
    return scrolling;
  }

  function prKey(owner: string, name: string, number: number): string {
    return `${owner}/${name}#${number}`;
  }

  function isCurrentPR(owner: string, name: string, number: number): boolean {
    return (
      owner === currentOwner && name === currentName && number === currentNumber
    );
  }

  function compiledGlobsFor(
    owner: string,
    name: string,
    number: number,
  ): GlobMatcher[] {
    const list = globsByRepo[prKey(owner, name, number)];
    if (!list || list.length === 0) return [];
    let compiled = compiledGlobs.get(list);
    if (!compiled) {
      compiled = list.map(compileGlob);
      compiledGlobs.set(list, compiled);
    }
    return compiled;
  }

  function fileMeta(path: string): DiffFile | undefined {
    const files = getFileList()?.files ?? null;
    if (files !== metaSource) {
      metaSource = files;
      metaByPath = new Map((files ?? []).map((f) => [f.path, f]));
    }
    return metaByPath.get(path);
  }

  function resolveFor(
    owner: string,
    name: string,
    number: number,
    path: string,
  ): { collapsed: boolean; reason: CollapseReason | null } {
    const override = collapseOverrides[prKey(owner, name, number)]?.[path];
    // Default rules need the file's metadata, which the store only has
    // for the PR it currently has loaded.
    const meta = isCurrentPR(owner, name, number) ? fileMeta(path) : undefined;
    if (!meta) {
      return override === true
        ? { collapsed: true, reason: "user" }
        : { collapsed: false, reason: null };
    }
    return resolveCollapse(meta, override, compiledGlobsFor(owner, name, number));
  }

  function isFileCollapsed(
    owner: string,
    name: string,
    number: number,
    filePath: string,
  ): boolean {
    return resolveFor(owner, name, number, filePath).collapsed;
  }

  function getCollapseReason(path: string): CollapseReason | null {
    if (!currentOwner) return null;
    return resolveFor(currentOwner, currentName, currentNumber, path).reason;
  }

  function writeOverrides(key: string, entries: Record<string, boolean>): void {
    collapseOverrides = {
      ...collapseOverrides,
      [key]: { ...collapseOverrides[key], ...entries },
    };
    saveCollapseOverrides(collapseOverrides);
  }

  function setFilesCollapsed(paths: string[], collapsed: boolean): void {
    if (!currentOwner || paths.length === 0) return;
    const entries: Record<string, boolean> = {};
    for (const p of paths) entries[p] = collapsed;
    writeOverrides(prKey(currentOwner, currentName, currentNumber), entries);
  }

  // A jump to a file or line expands it if collapsed, recorded as an
  // explicit user choice.
  function expandForJump(path: string): void {
    if (!currentOwner) return;
    if (!isFileCollapsed(currentOwner, currentName, currentNumber, path)) return;
    writeOverrides(prKey(currentOwner, currentName, currentNumber), {
      [path]: false,
    });
  }

  // --- collapse globs ---

  function getCollapseGlobs(): string[] {
    if (!currentOwner) return [];
    return globsByRepo[prKey(currentOwner, currentName, currentNumber)] ?? [];
  }

  // Loads the route's globs once and caches them. Failures are not
  // cached (the next load retries) and never block the diff.
  function ensureGlobs(owner: string, name: string, number: number): Promise<void> {
    const rk = prKey(owner, name, number);
    if (rk in globsByRepo) return Promise.resolve();
    const inflight = globsInflight.get(rk);
    if (inflight) return inflight;
    const p = (async () => {
      try {
        const { data, error } = await apiClient.GET(COLLAPSE_GLOBS_PATH, {
          params: { path: { owner, name, number } },
        });
        if (error || !data) return;
        const globs = Array.isArray(data.globs) ? data.globs : [];
        globsByRepo = { ...globsByRepo, [rk]: globs };
      } catch {
        /* treat as no globs; retried on next load */
      } finally {
        globsInflight.delete(rk);
      }
    })();
    globsInflight.set(rk, p);
    return p;
  }

  async function saveCollapseGlobs(globs: string[]): Promise<void> {
    if (!currentOwner) return;
    const owner = currentOwner;
    const name = currentName;
    const number = currentNumber;
    const { data, error } = await apiClient.PUT(COLLAPSE_GLOBS_PATH, {
      params: { path: { owner, name, number } },
      body: { globs },
    });
    if (error || !data) {
      throw new Error(apiErrorMessage(error, "failed to save collapse globs"));
    }
    // Any other cached route may be the same repo; drop them all so the
    // next load of each refetches.
    globsByRepo = {
      [prKey(owner, name, number)]: Array.isArray(data.globs) ? data.globs : [],
    };
  }

  // --- lazy hunks ---

  function isLazy(): boolean {
    return lazy;
  }

  function isWorktreeChanged(): boolean {
    return worktreeChanged;
  }

  function getFileHunkState(path: string): HunkState {
    void hunkVersion;
    if (!lazy) return "loaded";
    // Between a reload starting and its summary arriving there is no
    // loader yet; nothing is renderable.
    if (!hunkLoader) return "loading";
    return hunkLoader.state(path);
  }

  function requestHunks(path: string): void {
    if (!lazy || !hunkLoader) return;
    const f = summaryByPath.get(path);
    if (!f) return;
    const before = hunkLoader.state(path);
    hunkLoader.request({
      path: f.path,
      old_path: f.old_path,
      lines: f.additions + f.deletions,
    });
    if (hunkLoader.state(path) !== before) hunkVersion++;
  }

  function resetLazyLoader(): void {
    hunkLoader?.reset();
    hunkLoader = null;
    lazyIndex = new Map();
    summaryByPath = new Map();
    worktreeChanged = false;
    hunkVersion++;
  }

  function enterLazyMode(
    owner: string,
    name: string,
    number: number,
    summary: DiffSummary,
    params: URLSearchParams,
  ): void {
    const files = summary.files.map((f) => ({ ...f, hunks: [] }));
    lazyIndex = new Map(files.map((f, i) => [f.path, i]));
    summaryByPath = new Map(summary.files.map((f) => [f.path, f]));
    diff = {
      stale: summary.stale,
      whitespace_only_count: summary.whitespace_only_count,
      files,
    };
    lazy = true;

    // Same scope as the summary, pinned to the revisions it resolved.
    const base: {
      commit?: string;
      from?: string;
      to?: string;
      whitespace?: string;
      expect_from: string;
      expect_to: string;
    } = { expect_from: summary.diff_from, expect_to: summary.diff_to };
    for (const k of ["commit", "from", "to", "whitespace"] as const) {
      const v = params.get(k);
      if (v !== null) base[k] = v;
    }

    const loader: HunkLoader = new HunkLoader({
      fetchBatch: async (batch, signal) => {
        const paths: string[] = [];
        const seen = new Set<string>();
        for (const f of batch) {
          for (const p of [f.path, f.old_path]) {
            if (p && !seen.has(p)) {
              seen.add(p);
              paths.push(p);
            }
          }
        }
        const { data, error, response } = await apiClient.POST(DIFF_HUNKS_PATH, {
          params: { path: { owner, name, number } },
          body: { ...base, paths },
          signal,
        });
        if (response?.status === 409) {
          throw new HunksConflictError(apiErrorMessage(error, "diff revisions changed"));
        }
        if (error || !data) {
          throw new Error(apiErrorMessage(error, "failed to load diff hunks"));
        }
        return (data.files ?? []) as DiffFile[];
      },
      onLoaded: (loaded) => {
        if (hunkLoader !== loader || !diff) return;
        for (const f of loaded) {
          const idx = lazyIndex.get(f.path);
          if (idx === undefined) continue;
          const sum = summaryByPath.get(f.path);
          if (
            sum &&
            (sum.additions !== f.additions || sum.deletions !== f.deletions)
          ) {
            worktreeChanged = true;
          }
          // Replace in place; keep the summary's sort order. Status and
          // old_path stay as the summary paired them: a batch of only some
          // paths can pair a copy or rename differently. Hunks, binary
          // flag, and counts come from the loaded file.
          diff.files[idx] = sum
            ? { ...f, status: sum.status, old_path: sum.old_path }
            : f;
        }
        hunkVersion++;
      },
      onError: (_paths, err) => {
        if (hunkLoader !== loader) return;
        if (err instanceof HunksConflictError || err instanceof MissingHunksError) {
          worktreeChanged = true;
        }
        hunkVersion++;
      },
    });
    hunkLoader = loader;
    hunkVersion++;
  }

  // --- writes ---

  function setActiveFile(path: string | null): void {
    activeFile = path;
  }

  function clearScrolling(): void {
    scrolling = false;
  }

  function requestScrollToFile(path: string): void {
    activeFile = path;
    scrollTarget = path;
    scrolling = true;
  }

  // Jump readiness (see components/diff/fileReadiness.ts). DiffView
  // registers its helper here so jumpers outside it (the sidebar
  // sections) reach the same viewport window through the store they
  // already hold. Without a registered helper (no DiffView mounted)
  // a jump proceeds exactly as before: scrollToDiffLine expands the
  // file itself and looks the line up in whatever is rendered.
  let fileReadiness: ((path: string) => Promise<boolean>) | null = null;

  // Returns an unregister that only clears its own registration, so a
  // DiffView torn down after its replacement mounted cannot unhook it.
  function registerFileReadiness(fn: (path: string) => Promise<boolean>): () => void {
    fileReadiness = fn;
    return () => {
      if (fileReadiness === fn) fileReadiness = null;
    };
  }

  function ensureFileReady(path: string): Promise<boolean> {
    return fileReadiness ? fileReadiness(path) : Promise.resolve(true);
  }

  function getScrollTarget(): string | null {
    return scrollTarget;
  }

  function consumeScrollTarget(): void {
    scrollTarget = null;
  }

  // A line the UI wants brought into view that is not currently
  // rendered because it sits in an unexpanded context gap. DiffFile
  // hands this to its collapsed regions; the one containing the line
  // expands itself, then reports back through its onrevealed callback
  // so DiffFile can finish the jump and consume the target.
  let revealTarget = $state<{ path: string; line: number } | null>(null);

  function requestRevealLine(path: string, line: number): void {
    revealTarget = { path, line };
  }

  function getRevealTarget(): { path: string; line: number } | null {
    return revealTarget;
  }

  function consumeRevealTarget(): void {
    revealTarget = null;
  }

  function setTabWidth(w: number): void {
    tabWidth = w;
    safeSetItem("diff-tab-width", String(w));
  }

  function setHideWhitespace(v: boolean): void {
    hideWhitespace = v;
    safeSetItem("diff-hide-whitespace", String(v));
    if (currentOwner && currentName && currentNumber) {
      void reloadDiffOnly();
    }
  }

  function setLayout(v: DiffLayout): void {
    layout = v;
    safeSetItem("diff-layout", v);
  }

  // refresh triggers a server-side PR sync (fetches latest from
  // GitHub) and then re-loads the diff, commits, and detail in the
  // store so the Review surface reflects new pushes. The sync path
  // is already exposed at POST /pulls/{n}/sync.
  let refreshing = $state(false);
  let refreshError = $state<string | null>(null);

  async function refresh(): Promise<void> {
    if (!currentOwner || refreshing) return;
    refreshing = true;
    refreshError = null;
    try {
      const { error } = await apiClient.POST(
        "/repos/{owner}/{name}/pulls/{number}/sync",
        {
          params: {
            path: {
              owner: currentOwner,
              name: currentName,
              number: currentNumber,
            },
          },
        },
      );
      if (error) {
        refreshError = apiErrorMessage(error, "sync failed");
        return;
      }
    } catch (err) {
      refreshError = err instanceof Error ? err.message : String(err);
      return;
    } finally {
      refreshing = false;
    }
    // Clear the commit cache so loadCommits re-fetches against the
    // new head SHAs that the sync just wrote to the DB.
    commits = null;
    commitsError = null;
    patchsets = null;
    patchsetsError = null;
    void loadCommits();
    void reloadDiffOnly();
  }

  function isRefreshing(): boolean {
    return refreshing;
  }

  function getRefreshError(): string | null {
    return refreshError;
  }

  async function reloadDiffOnly(): Promise<void> {
    await loadScoped(currentOwner, currentName, currentNumber, false);
  }

  function toggleFileCollapsed(
    owner: string,
    name: string,
    number: number,
    filePath: string,
  ): void {
    const next = !isFileCollapsed(owner, name, number, filePath);
    writeOverrides(prKey(owner, name, number), { [filePath]: next });
  }

  function fetchJSON(
    url: string,
    signal: AbortSignal,
  ): Promise<unknown> {
    return fetch(url, { signal }).then(async (r) => {
      if (!r.ok) {
        const body = await r.json().catch(() => ({}));
        throw new Error(
          (body as Record<string, string>).detail ??
            (body as Record<string, string>).title ??
            `HTTP ${r.status}`,
        );
      }
      return r.json();
    });
  }

  function setActiveIfNeeded(
    files: { path: string }[] | undefined,
  ): void {
    if (
      !files?.some((f) => f.path === activeFile)
    ) {
      activeFile = files?.[0]?.path ?? null;
    }
  }

  async function loadDiff(
    owner: string,
    name: string,
    number: number,
  ): Promise<void> {
    const prChanged =
      owner !== currentOwner ||
      name !== currentName ||
      number !== currentNumber;
    if (prChanged) flushMeta();
    currentOwner = owner;
    currentName = name;
    currentNumber = number;
    // Reload drafts even for the same PR: another tab, or a local
    // worktree switching branches, may have changed what the server
    // holds for it.
    void loadDraftsFor({ owner, name, number });
    if (prChanged) {
      scope = { kind: "head" };
      commits = null;
      commitsLoading = false;
      commitsError = null;
      patchsets = null;
      patchsetsLoading = false;
      patchsetsError = null;
      resetNotes();
    }
    await loadScoped(owner, name, number, true);
  }

  function normalizeSummary(data: unknown): DiffSummary {
    const raw = (data ?? {}) as Partial<DiffSummary>;
    return sortFilesByPath({
      stale: raw.stale ?? false,
      whitespace_only_count: raw.whitespace_only_count ?? 0,
      diff_from: raw.diff_from ?? "",
      diff_to: raw.diff_to ?? "",
      files: raw.files ?? [],
    });
  }

  // loadScoped runs the load flow for the current scope:
  //   1. the scoped /files summary (plus the repo's collapse globs, once),
  //   2. then either the full /diff (patchset scope, small diffs, or when
  //      the summary failed) or lazy mode, where hunks load per file.
  // `clear` drops the displayed diff immediately (PR or scope change);
  // otherwise (whitespace toggle, refresh) it stays until replaced.
  async function loadScoped(
    owner: string,
    name: string,
    number: number,
    clear: boolean,
  ): Promise<void> {
    abortController?.abort();
    const ac = new AbortController();
    abortController = ac;
    resetLazyLoader();
    if (clear) {
      diff = null;
      lazy = false;
    }
    fileList = null;
    loading = true;
    fileListLoading = true;
    storeError = null;

    const prefix =
      `${getBasePath()}api/v1/repos/` +
      `${encodeURIComponent(owner)}/` +
      `${encodeURIComponent(name)}/` +
      `pulls/${number}`;
    const s = scope;
    const params = scopeToDiffParams(s);
    if (hideWhitespace) params.set("whitespace", "hide");
    const qs = params.toString();
    // /files takes the same scope params as /diff, except patchsets:
    // patchset scope always loads the full /diff, and a head-scope
    // summary would briefly drive the sidebar with the wrong files.
    const summaryPromise: Promise<DiffSummary | null> =
      s.kind === "patchsets"
        ? Promise.resolve(null)
        : fetchJSON(
            `${prefix}/files${qs ? `?${qs}` : ""}`,
            ac.signal,
          ).then(
            (data) => normalizeSummary(data),
            () => null,
          );
    const [summary] = await Promise.all([
      summaryPromise,
      ensureGlobs(owner, name, number),
    ]);
    if (abortController !== ac) return;
    fileList = summary;
    fileListLoading = false;
    if (summary) setActiveIfNeeded(summary.files);

    const total = summary
      ? summary.files.reduce((n, f) => n + f.additions + f.deletions, 0)
      : 0;
    if (summary && total > LAZY_DIFF_LINES) {
      enterLazyMode(owner, name, number, summary, params);
      loading = false;
      return;
    }

    try {
      const data = await fetchJSON(
        `${prefix}/diff${qs ? `?${qs}` : ""}`,
        ac.signal,
      );
      if (abortController !== ac) return;
      lazy = false;
      diff = sortFilesByPath(data as DiffResult);
      setActiveIfNeeded(diff.files);
    } catch (err) {
      if (abortController !== ac) return;
      storeError = err instanceof Error ? err.message : String(err);
      lazy = false;
      diff = null;
      // The sidebar must not fall back to the summary after a failure.
      fileList = null;
    } finally {
      if (abortController === ac) loading = false;
    }
  }

  function clearDiff(): void {
    abortController?.abort();
    abortController = null;
    resetLazyLoader();
    lazy = false;
    diff = null;
    fileList = null;
    storeError = null;
    loading = false;
    fileListLoading = false;
    activeFile = null;
    scrollTarget = null;
    scrolling = false;
    // A reveal request from a symbol-refs jump (or any future caller)
    // must not survive into the next PR's DiffView mount: this store
    // is an app-wide singleton, so an unclaimed target would otherwise
    // sit armed until some later, unrelated CollapsedRegion happens to
    // cover the same line and spontaneously expands for it.
    revealTarget = null;
    commits = null;
    commitsLoading = false;
    commitsError = null;
    patchsets = null;
    patchsetsLoading = false;
    patchsetsError = null;
    resetNotes();
    flushMeta();
    scope = { kind: "head" };
    currentOwner = "";
    currentName = "";
    currentNumber = 0;
  }

  function resetNotes(): void {
    notesGen += 1;
    if (notesSaveTimer) {
      clearTimeout(notesSaveTimer);
      notesSaveTimer = null;
    }
    notesContent = "";
    notesUpdatedAt = null;
    notesLoaded = false;
    notesLoading = false;
    notesSaving = false;
    notesError = null;
  }

  async function loadCommits(): Promise<void> {
    if (commits || commitsLoading) return;
    if (!currentOwner || !currentName || !currentNumber) return;

    commitsLoading = true;
    commitsError = null;
    const owner = currentOwner;
    const name = currentName;
    const number = currentNumber;
    try {
      const basePath = getBasePath();
      const url =
        `${basePath}api/v1/repos/` +
        `${encodeURIComponent(owner)}/` +
        `${encodeURIComponent(name)}/` +
        `pulls/${number}/commits`;
      const response = await fetch(url);
      if (currentOwner !== owner || currentName !== name || currentNumber !== number) return;
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error(
          (body as Record<string, string>).detail ??
            (body as Record<string, string>).title ??
            `HTTP ${response.status}`,
        );
      }
      const data = (await response.json()) as { commits: CommitInfo[] };
      if (currentOwner !== owner || currentName !== name || currentNumber !== number) return;
      commits = data.commits;
    } catch (err) {
      if (currentOwner !== owner || currentName !== name || currentNumber !== number) return;
      commitsError = err instanceof Error ? err.message : String(err);
    } finally {
      if (currentOwner === owner && currentName === name && currentNumber === number) {
        commitsLoading = false;
      }
    }
  }

  async function loadPatchsets(): Promise<void> {
    if (patchsets !== null || patchsetsLoading) return;
    if (!currentOwner || !currentName || !currentNumber) return;

    patchsetsLoading = true;
    patchsetsError = null;
    const owner = currentOwner;
    const name = currentName;
    const number = currentNumber;
    try {
      const basePath = getBasePath();
      const url =
        `${basePath}api/v1/repos/` +
        `${encodeURIComponent(owner)}/` +
        `${encodeURIComponent(name)}/` +
        `pulls/${number}/patchsets`;
      const response = await fetch(url);
      if (currentOwner !== owner || currentName !== name || currentNumber !== number) return;
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error(
          (body as Record<string, string>).detail ??
            (body as Record<string, string>).title ??
            `HTTP ${response.status}`,
        );
      }
      const data = (await response.json()) as { patchsets: Patchset[] };
      if (currentOwner !== owner || currentName !== name || currentNumber !== number) return;
      patchsets = data.patchsets ?? [];
    } catch (err) {
      if (currentOwner !== owner || currentName !== name || currentNumber !== number) return;
      patchsetsError = err instanceof Error ? err.message : String(err);
    } finally {
      if (currentOwner === owner && currentName === name && currentNumber === number) {
        patchsetsLoading = false;
      }
    }
  }

  function getPatchsets(): Patchset[] | null {
    return patchsets;
  }

  function isPatchsetsLoading(): boolean {
    return patchsetsLoading;
  }

  function getPatchsetsError(): string | null {
    return patchsetsError;
  }

  function getScope(): DiffScope {
    return scope;
  }

  function getCommits(): CommitInfo[] | null {
    return commits;
  }

  // The SHA new comments and symbol-ref searches anchor against: the
  // single commit or range's newer end for those scopes, the target
  // patchset's head for a patchset-pair scope (the interdiff's new
  // side, NOT the PR head), otherwise the newest commit in the
  // currently loaded list (commits is newest-first), or "" if that
  // data hasn't loaded yet or the target patchset isn't in the loaded
  // list. Shared by DiffFile (per-file rendering) and DiffView (the
  // symbol-refs staleness watcher) so both read the exact same
  // derivation instead of each keeping their own copy of this
  // scope/commits/patchsets logic.
  function getCurrentCommitSha(): string {
    if (scope.kind === "commit") return scope.sha;
    if (scope.kind === "range") return scope.toSha;
    if (scope.kind === "patchsets") {
      // scope.toNumber is captured into a local before the closure:
      // scope is a mutable $state variable, so TS won't carry the
      // "patchsets" narrowing from this `if` into a callback that
      // could (as far as it knows) run after scope changes again.
      const toNumber = scope.toNumber;
      const toPatchset = patchsets?.find((p) => p.number === toNumber);
      return toPatchset ? toPatchset.head_sha : "";
    }
    if (commits && commits.length > 0) return commits[0]!.sha;
    return "";
  }

  function isCommitsLoading(): boolean {
    return commitsLoading;
  }

  function getCommitsError(): string | null {
    return commitsError;
  }

  // --- PR scratchpad notes ---

  const NOTES_DEBOUNCE_MS = 800;

  function notesPrefix(owner: string, name: string, number: number): string {
    return (
      `${getBasePath()}api/v1/repos/` +
      `${encodeURIComponent(owner)}/` +
      `${encodeURIComponent(name)}/` +
      `pulls/${number}/notes`
    );
  }

  // loadBlobRange fetches a slice of a file blob for context
  // expansion. Callers supply absolute 1-based [start, end] line
  // numbers in the NEW-side numbering of the diff scope; `sha`
  // must match what the caller used to render the hunks.
  async function loadBlobRange(
    path: string,
    sha: string,
    start: number,
    end: number,
  ): Promise<string[]> {
    if (!currentOwner || !currentName || !currentNumber) return [];
    const basePath = getBasePath();
    const url =
      `${basePath}api/v1/repos/` +
      `${encodeURIComponent(currentOwner)}/` +
      `${encodeURIComponent(currentName)}/` +
      `pulls/${currentNumber}/blob-range` +
      `?path=${encodeURIComponent(path)}` +
      `&sha=${encodeURIComponent(sha)}` +
      `&start=${start}&end=${end}`;
    const res = await fetch(url);
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(
        (body as Record<string, string>).detail ??
          (body as Record<string, string>).title ??
          `HTTP ${res.status}`,
      );
    }
    const data = (await res.json()) as { lines: string[] };
    return data.lines ?? [];
  }

  async function loadNotes(): Promise<void> {
    if (notesLoaded || notesLoading) return;
    if (!currentOwner || !currentName || !currentNumber) return;
    const gen = notesGen;
    notesLoading = true;
    notesError = null;
    try {
      const response = await fetch(
        notesPrefix(currentOwner, currentName, currentNumber),
      );
      if (gen !== notesGen) return;
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error(
          (body as Record<string, string>).detail ??
            (body as Record<string, string>).title ??
            `HTTP ${response.status}`,
        );
      }
      const data = (await response.json()) as { content: string; updated_at?: string };
      if (gen !== notesGen) return;
      notesContent = data.content ?? "";
      notesUpdatedAt = data.updated_at ?? null;
      notesLoaded = true;
    } catch (err) {
      if (gen !== notesGen) return;
      notesError = err instanceof Error ? err.message : String(err);
    } finally {
      if (gen === notesGen) notesLoading = false;
    }
  }

  // updateNotes is called from the UI on every keystroke. It updates
  // local state immediately and debounces the PUT so we don't write
  // to SQLite on every character.
  function updateNotes(next: string): void {
    notesContent = next;
    notesError = null;
    if (notesSaveTimer) clearTimeout(notesSaveTimer);
    const owner = currentOwner;
    const name = currentName;
    const number = currentNumber;
    const gen = notesGen;
    if (!owner || !number) return;
    notesSaveTimer = setTimeout(() => {
      void flushNotes(owner, name, number, gen);
    }, NOTES_DEBOUNCE_MS);
  }

  // flushNotes runs immediately (used on blur and before PR switch)
  // so the user doesn't lose the tail of what they just typed.
  async function flushNotes(
    owner = currentOwner,
    name = currentName,
    number = currentNumber,
    gen = notesGen,
  ): Promise<void> {
    if (!owner || !number) return;
    if (notesSaveTimer) {
      clearTimeout(notesSaveTimer);
      notesSaveTimer = null;
    }
    const content = notesContent;
    notesSaving = true;
    notesError = null;
    try {
      const response = await fetch(notesPrefix(owner, name, number), {
        method: "PUT",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ content }),
      });
      if (gen !== notesGen) return;
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error(
          (body as Record<string, string>).detail ??
            (body as Record<string, string>).title ??
            `HTTP ${response.status}`,
        );
      }
      const data = (await response.json()) as { content: string; updated_at?: string };
      if (gen !== notesGen) return;
      notesUpdatedAt = data.updated_at ?? null;
    } catch (err) {
      if (gen !== notesGen) return;
      notesError = err instanceof Error ? err.message : String(err);
    } finally {
      if (gen === notesGen) notesSaving = false;
    }
  }

  function getNotes(): string {
    return notesContent;
  }

  function getNotesUpdatedAt(): string | null {
    return notesUpdatedAt;
  }

  function isNotesLoaded(): boolean {
    return notesLoaded;
  }

  function isNotesSaving(): boolean {
    return notesSaving;
  }

  function getNotesError(): string | null {
    return notesError;
  }

  /** Returns the 1-based index and total for the active commit. */
  function getCommitIndex(): { current: number; total: number } | null {
    if (!commits || commits.length === 0) return null;
    const s = scope;
    if (s.kind !== "commit") return null;
    const idx = commits.findIndex((c) => c.sha === s.sha);
    if (idx === -1) return null;
    // commits are newest-first, so position is reversed for display
    return { current: commits.length - idx, total: commits.length };
  }

  /** Returns the CommitInfo for the active commit scope. */
  function getActiveCommit(): CommitInfo | null {
    const s = scope;
    if (!commits || s.kind !== "commit") return null;
    return commits.find((c) => c.sha === s.sha) ?? null;
  }

  function reviewedKey(): string {
    return `${currentOwner}/${currentName}#${currentNumber}`;
  }

  // scopeKey returns a stable string identifier for the current scope.
  // Used as part of the reviewed-files storage key so a file marked
  // viewed inside commit X isn't implicitly considered viewed when the
  // user switches to commit Y (which may have a very different version
  // of the same file).
  function scopeKey(s: DiffScope): string {
    switch (s.kind) {
      case "head": return "head";
      case "commit": return `commit:${s.sha}`;
      case "range": return `range:${s.fromSha}..${s.toSha}`;
      case "unreviewed": {
        const r = getUnreviewedRange();
        return r ? `unreviewed:${r.fromSha}..${r.toSha}` : "unreviewed:empty";
      }
      case "patchsets": return `ps:${s.fromNumber}..${s.toNumber}`;
    }
  }

  function reviewedFilesKey(): string {
    return `${reviewedKey()}::${scopeKey(scope)}`;
  }

  // Resolves the "unreviewed" scope into a concrete range, or null when
  // every commit has been reviewed (nothing to diff).
  function getUnreviewedRange(): { fromSha: string; toSha: string } | null {
    if (!commits || commits.length === 0) return null;
    const key = reviewedKey();
    const reviewed = new Set(reviewedCommits[key] ?? []);
    // commits are newest-first; the oldest unreviewed is the highest index.
    let oldestIdx = -1;
    for (let i = commits.length - 1; i >= 0; i--) {
      if (!reviewed.has(commits[i]!.sha)) {
        oldestIdx = i;
        break;
      }
    }
    if (oldestIdx === -1) return null;
    return { fromSha: commits[oldestIdx]!.sha, toSha: commits[0]!.sha };
  }

  function scopeToDiffParams(s: DiffScope): URLSearchParams {
    const p = new URLSearchParams();
    if (s.kind === "commit") p.set("commit", s.sha);
    if (s.kind === "range") {
      p.set("from", s.fromSha);
      p.set("to", s.toSha);
    }
    if (s.kind === "unreviewed") {
      const r = getUnreviewedRange();
      if (r) {
        p.set("from", r.fromSha);
        p.set("to", r.toSha);
      }
      // If r is null, caller falls through to HEAD scope (no params).
    }
    if (s.kind === "patchsets") {
      p.set("from_patchset", String(s.fromNumber));
      p.set("to_patchset", String(s.toNumber));
    }
    return p;
  }

  function markFileReviewed(path: string, viewed: boolean): void {
    if (!currentOwner) return;
    const key = reviewedFilesKey();
    const current = reviewedFiles[key] ?? [];
    const has = current.includes(path);
    if (viewed && has) return;
    if (!viewed && !has) return;
    const next = viewed
      ? [...current, path]
      : current.filter((p) => p !== path);
    reviewedFiles = { ...reviewedFiles, [key]: next };
    saveReviewedFiles(reviewedFiles);
  }

  function isFileReviewed(path: string): boolean {
    const key = reviewedFilesKey();
    return (reviewedFiles[key] ?? []).includes(path);
  }

  function getFileReviewProgress(): { reviewed: number; total: number } | null {
    const list = getFileList();
    if (!list || list.files.length === 0) return null;
    const reviewed = new Set(reviewedFiles[reviewedFilesKey()] ?? []);
    const count = list.files.filter((f) => reviewed.has(f.path)).length;
    return { reviewed: count, total: list.files.length };
  }

  function draftKey(): string {
    return reviewedKey();
  }

  function getDraft(): DraftReview {
    return draftReviews[draftKey()] ?? emptyDraft();
  }

  // hasDraftForPR lets sidebar components peek at any PR's drafts,
  // not just the currently-loaded one. Used by the per-row state
  // chip to override server-computed state with "in-review" when
  // the viewer has unsaved comments. Reads $state-backed
  // draftReviews so callers re-render when drafts change.
  function hasDraftForPR(
    owner: string,
    name: string,
    number: number,
  ): boolean {
    const key = `${owner}/${name}#${number}`;
    const d = draftReviews[key];
    if (d) return d.comments.length > 0;
    if (!summaryRequested) refreshSummary();
    return (draftSummary[key] ?? 0) > 0;
  }

  // publishDraft posts the current PR's server-side drafts to GitHub.
  // On failure it reloads drafts so the ones GitHub accepted disappear
  // and the one that failed shows its error.
  async function publishDraft(req: {
    event: ReviewEvent;
    body: string;
    commitId?: string;
  }): Promise<{ ok: true } | { ok: false; error: string }> {
    const pr = currentPRRef();
    if (!pr) return { ok: false, error: "No pull request selected" };
    flushMeta();
    await whenDraftsSettled();
    const unsaved = await retryUnsaved(pr);
    if (unsaved) {
      return {
        ok: false,
        error:
          `${unsaved.count} draft(s) could not be saved to the server (${unsaved.error}). ` +
          "They are kept here; try publishing again.",
      };
    }
    try {
      await draftsApi.publish(pr, {
        event: req.event,
        body: req.body,
        ...(req.commitId ? { commit_id: req.commitId } : {}),
      });
      const next = { ...draftReviews };
      delete next[draftKeyFor(pr)];
      draftReviews = next;
      forgetSummaryFor(draftKeyFor(pr));
      return { ok: true };
    } catch (err) {
      await reloadDrafts();
      return { ok: false, error: err instanceof DraftApiError ? err.message : String(err) };
    }
  }

  function setDraftBody(body: string): void {
    if (!currentOwner) return;
    const key = draftKey();
    const existing = draftReviews[key] ?? emptyDraft();
    draftReviews = { ...draftReviews, [key]: { ...existing, body } };
    bumpDrafts(key);
    scheduleMeta();
  }

  function setDraftEvent(event: ReviewEvent): void {
    if (!currentOwner) return;
    const key = draftKey();
    const existing = draftReviews[key] ?? emptyDraft();
    draftReviews = { ...draftReviews, [key]: { ...existing, event } };
    bumpDrafts(key);
    scheduleMeta();
  }

  function addDraftComment(
    input: Omit<DraftComment, "id" | "createdAt">,
  ): DraftComment {
    const comment: DraftComment = {
      ...input,
      id:
        typeof crypto !== "undefined" && "randomUUID" in crypto
          ? crypto.randomUUID()
          : `c-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      createdAt: new Date().toISOString(),
    };
    const key = draftKey();
    const existing = draftReviews[key] ?? emptyDraft();
    draftReviews = {
      ...draftReviews,
      [key]: { ...existing, comments: [...existing.comments, comment] },
    };
    const pr = currentPRRef();
    if (pr) {
      bumpDrafts(key);
      const p = trackWrite(draftsApi.create(pr, [toDraftWrite(comment)]));
      pendingCreates.set(comment.id, p);
      void p.then(
        () => pendingCreates.delete(comment.id),
        (err: unknown) => {
          pendingCreates.delete(comment.id);
          setSyncError(key, [comment.id], errorText(err));
          syncFailed(err);
        },
      );
    }
    return comment;
  }

  function removeDraftComment(id: string): void {
    const key = draftKey();
    const existing = draftReviews[key];
    if (!existing) return;
    const next = existing.comments.filter((c) => c.id !== id);
    if (next.length === existing.comments.length) return;
    draftReviews = {
      ...draftReviews,
      [key]: { ...existing, comments: next },
    };
    const pr = currentPRRef();
    if (pr) removeOnServer(pr, [id]);
  }

  // updateDraftCommentBody edits the body of an existing draft without
  // re-anchoring it. Empty bodies are treated as a deletion so users
  // can clear-and-save to discard (matching the composer's behavior).
  function updateDraftCommentBody(id: string, body: string): void {
    const trimmed = body.trim();
    if (trimmed === "") {
      removeDraftComment(id);
      return;
    }
    const key = draftKey();
    const existing = draftReviews[key];
    if (!existing) return;
    let changed = false;
    const nextComments = existing.comments.map((c) => {
      if (c.id !== id || c.body === trimmed) return c;
      changed = true;
      const { submitError: _cleared, ...rest } = c;
      return { ...rest, body: trimmed };
    });
    if (!changed) return;
    draftReviews = {
      ...draftReviews,
      [key]: { ...existing, comments: nextComments },
    };
    const pr = currentPRRef();
    if (pr) {
      bumpDrafts(key);
      trackWrite(
        (async () => {
          await (pendingCreates.get(id) ?? Promise.resolve()).catch(() => {});
          // An unsaved draft has no server row to PATCH; its latest
          // body goes out with the retry create before publish.
          if (draftReviews[key]?.comments.find((c) => c.id === id)?.syncError) return;
          await draftsApi.updateBody(pr, id, trimmed);
        })().catch(syncFailed),
      );
    }
  }

  // Transient signal so the sidebar drafts list can ask the inline
  // card to open its editor after the scroll-into-view lands. The
  // card clears this once it has entered edit mode.
  let editingDraftId = $state<string | null>(null);

  function requestEditDraft(id: string): void {
    editingDraftId = id;
  }

  function getEditRequest(): string | null {
    return editingDraftId;
  }

  function ackEditRequest(id: string): void {
    if (editingDraftId === id) editingDraftId = null;
  }

  // --- Claude suggestions ---
  //
  // Suggestions live beside drafts in the same per-PR bucket. Actions
  // are optimistic like drafts: the bucket changes immediately, and a
  // failed request puts the suggestion back and reports the error.

  // Last discard batch per PR, so Undo only ever restores into the PR
  // the suggestions came from.
  let lastDiscardedByKey = $state<Record<string, DraftComment[]>>({});
  // Discard requests in flight by suggestion id, so an undo can wait
  // for the matching discard to land before restoring.
  const pendingDiscards = new Map<string, Promise<unknown>>();

  function getSuggestions(): DraftComment[] {
    return getDraft().suggestions;
  }

  function getSuggestionsForPath(path: string): DraftComment[] {
    return getDraft().suggestions.filter((c) => c.path === path);
  }

  function getLastDiscarded(): DraftComment[] {
    return lastDiscardedByKey[draftKey()] ?? [];
  }

  function setLastDiscarded(key: string, list: DraftComment[]): void {
    lastDiscardedByKey = { ...lastDiscardedByKey, [key]: list };
  }

  // updateBucket applies fn to the current PR's bucket.
  function updateBucket(key: string, fn: (d: DraftReview) => DraftReview): void {
    draftReviews = { ...draftReviews, [key]: fn(draftReviews[key] ?? emptyDraft()) };
  }

  function acceptSuggestion(id: string): void {
    const pr = currentPRRef();
    if (!pr) return;
    const key = draftKeyFor(pr);
    const s = draftReviews[key]?.suggestions.find((c) => c.id === id);
    if (!s) return;
    const accepted: DraftComment = { ...s, origin: "claude" };
    updateBucket(key, (d) => ({
      ...d,
      suggestions: d.suggestions.filter((c) => c.id !== id),
      comments: [...d.comments, accepted],
    }));
    bumpDrafts(key);
    trackWrite(
      draftsApi.accept(pr, id).catch((err: unknown) => {
        updateBucket(key, (d) => ({
          ...d,
          comments: d.comments.filter((c) => c.id !== id),
          suggestions: d.suggestions.some((c) => c.id === id) ? d.suggestions : [...d.suggestions, s],
        }));
        syncFailed(err);
      }),
    );
  }

  function acceptAllSuggestions(): void {
    for (const s of [...getSuggestions()]) acceptSuggestion(s.id);
  }

  function discardSuggestions(ids: string[]): void {
    const pr = currentPRRef();
    if (!pr) return;
    const key = draftKeyFor(pr);
    const idSet = new Set(ids);
    const removed = (draftReviews[key]?.suggestions ?? []).filter((c) => idSet.has(c.id));
    if (removed.length === 0) return;
    updateBucket(key, (d) => ({ ...d, suggestions: d.suggestions.filter((c) => !idSet.has(c.id)) }));
    setLastDiscarded(key, removed);
    bumpDrafts(key);
    for (const s of removed) {
      const p = trackWrite(
        draftsApi.discard(pr, s.id).catch((err: unknown) => {
          updateBucket(key, (d) => ({
            ...d,
            suggestions: d.suggestions.some((c) => c.id === s.id) ? d.suggestions : [...d.suggestions, s],
          }));
          setLastDiscarded(key, (lastDiscardedByKey[key] ?? []).filter((c) => c.id !== s.id));
          syncFailed(err);
        }),
      );
      pendingDiscards.set(s.id, p);
      void p.finally(() => {
        if (pendingDiscards.get(s.id) === p) pendingDiscards.delete(s.id);
      });
    }
  }

  function discardSuggestion(id: string): void {
    discardSuggestions([id]);
  }

  function discardAllSuggestions(): void {
    discardSuggestions(getSuggestions().map((c) => c.id));
  }

  function undoDiscard(): void {
    const pr = currentPRRef();
    if (!pr) return;
    const key = draftKeyFor(pr);
    const restored = lastDiscardedByKey[key] ?? [];
    if (restored.length === 0) return;
    setLastDiscarded(key, []);
    updateBucket(key, (d) => {
      const have = new Set(d.suggestions.map((c) => c.id));
      return { ...d, suggestions: [...d.suggestions, ...restored.filter((c) => !have.has(c.id))] };
    });
    bumpDrafts(key);
    for (const s of restored) {
      trackWrite(
        (async () => {
          // The discard may still be in flight; restore only after it lands.
          await pendingDiscards.get(s.id);
          await draftsApi.restore(pr, s.id);
        })().catch((err: unknown) => {
          updateBucket(key, (d) => ({ ...d, suggestions: d.suggestions.filter((c) => c.id !== s.id) }));
          syncFailed(err);
        }),
      );
    }
  }

  function updateSuggestionBody(id: string, body: string): void {
    const trimmed = body.trim();
    if (trimmed === "") {
      discardSuggestion(id);
      return;
    }
    const pr = currentPRRef();
    if (!pr) return;
    const key = draftKeyFor(pr);
    const cur = draftReviews[key]?.suggestions.find((c) => c.id === id);
    if (!cur || cur.body === trimmed) return;
    updateBucket(key, (d) => ({
      ...d,
      suggestions: d.suggestions.map((c) => (c.id === id ? { ...c, body: trimmed } : c)),
    }));
    bumpDrafts(key);
    trackWrite(draftsApi.updateBody(pr, id, trimmed).catch(syncFailed));
  }

  function getDraftCommentsForPath(path: string): DraftComment[] {
    return getDraft().comments.filter((c) => c.path === path);
  }

  function clearDraft(): void {
    const key = draftKey();
    const existing = draftReviews[key];
    if (!existing) return;
    const ids = existing.comments.map((c) => c.id);
    const next = { ...draftReviews };
    delete next[key];
    draftReviews = next;
    forgetSummaryFor(key);
    const pr = currentPRRef();
    if (!pr) return;
    removeOnServer(pr, ids);
    if (metaTimer) clearTimeout(metaTimer);
    metaTimer = null;
    metaPendingFor = null;
    trackWrite(draftsApi.putMeta(pr, "", "").catch(syncFailed));
  }

  function clearDraftCommentsForPath(path: string): void {
    const key = draftKey();
    const existing = draftReviews[key];
    if (!existing) return;
    const next = existing.comments.filter((c) => c.path !== path);
    if (next.length === existing.comments.length) return;
    const removed = existing.comments.filter((c) => c.path === path).map((c) => c.id);
    draftReviews = { ...draftReviews, [key]: { ...existing, comments: next } };
    const pr = currentPRRef();
    if (pr) removeOnServer(pr, removed);
  }

  function markCommitReviewed(sha: string): void {
    if (!currentOwner) return;
    const key = reviewedKey();
    const current = reviewedCommits[key] ?? [];
    if (current.includes(sha)) return;
    reviewedCommits = { ...reviewedCommits, [key]: [...current, sha] };
    saveReviewedCommits(reviewedCommits);
  }

  function isCommitReviewed(sha: string): boolean {
    const key = reviewedKey();
    return (reviewedCommits[key] ?? []).includes(sha);
  }

  function getReviewProgress(): { reviewed: number; total: number } | null {
    if (!commits || commits.length === 0) return null;
    const key = reviewedKey();
    const reviewed = reviewedCommits[key] ?? [];
    const count = commits.filter((c) => reviewed.includes(c.sha)).length;
    return { reviewed: count, total: commits.length };
  }

  function selectCommit(sha: string): Promise<void> {
    scope = { kind: "commit", sha };
    rangeAnchorSha = sha;
    if (currentOwner && currentName && currentNumber) {
      return loadDiff(currentOwner, currentName, currentNumber);
    }
    return Promise.resolve();
  }

  // The commit a shift-click would span from, or null when there is none.
  // Derived from the scope rather than tracked through it: every other scope
  // (head, patchsets, unreviewed) and every reset answers null on its own, so
  // none of those code paths has to remember to clear the anchor.
  function getSpanAnchorSha(): string | null {
    if (scope.kind === "commit") return scope.sha;
    if (scope.kind === "range") {
      const anchor = rangeAnchorSha;
      // Only trust the anchor while it is still an end of the live span.
      if (anchor !== null && (anchor === scope.fromSha || anchor === scope.toSha)) {
        return anchor;
      }
    }
    return null;
  }

  // extendSpan re-spans from the anchor to `sha`, which grows the span when
  // `sha` lies outside it and narrows it when inside -- the behaviour a
  // shift-click has in any list. Landing on the anchor collapses to that
  // single commit.
  function extendSpan(sha: string): void {
    const anchor = getSpanAnchorSha();
    if (anchor === null || anchor === sha) {
      void selectCommit(sha);
      return;
    }
    selectRange(anchor, sha);
  }

  function selectRange(fromSha: string, toSha: string): void {
    if (!commits) return;
    const fromIdx = commits.findIndex((c) => c.sha === fromSha);
    const toIdx = commits.findIndex((c) => c.sha === toSha);
    if (fromIdx === -1 || toIdx === -1) return;
    const [older, newer] = fromIdx > toIdx ? [fromSha, toSha] : [toSha, fromSha];
    scope = { kind: "range", fromSha: older, toSha: newer };
    if (currentOwner && currentName && currentNumber) {
      void loadDiff(currentOwner, currentName, currentNumber);
    }
  }

  // selectPatchsets pins the diff to a Gerrit-style interdiff between
  // two patchsets. Server does the cherry-pick math and returns the
  // rebase-subtracted diff; UI reads interdiff_kind on the response to
  // banner-flag conflicted or unrelated fallbacks.
  function selectPatchsets(fromNumber: number, toNumber: number): void {
    if (fromNumber === toNumber) return;
    scope = { kind: "patchsets", fromNumber, toNumber };
    if (currentOwner && currentName && currentNumber) {
      void loadDiff(currentOwner, currentName, currentNumber);
    }
  }

  function getInterdiff(): { kind: string; reason: string } | null {
    if (!diff || !diff.interdiff_kind) return null;
    return { kind: diff.interdiff_kind, reason: diff.interdiff_reason ?? "" };
  }

  function resetToHead(): Promise<void> {
    scope = { kind: "head" };
    if (currentOwner && currentName && currentNumber) {
      return loadDiff(currentOwner, currentName, currentNumber);
    }
    return Promise.resolve();
  }

  // jumpToNextUnreviewed advances to the oldest unreviewed commit newer
  // than the current position, or resets to HEAD when none remain.
  // Since selecting a commit auto-marks it reviewed, this is effectively
  // the "I'm done with this one, show me the next thing I haven't seen"
  // keybinding (m).
  function jumpToNextUnreviewed(): void {
    if (!commits) {
      void loadCommits();
      return;
    }
    if (commits.length === 0) return;
    const key = reviewedKey();
    const reviewed = new Set(reviewedCommits[key] ?? []);
    const s = scope;

    // Determine the "newest already-reviewed" cutoff: anything equal or
    // older than this should be ignored. commits are newest-first.
    let cutoffIdx = commits.length; // nothing ignored initially.
    if (s.kind === "commit") {
      cutoffIdx = commits.findIndex((c) => c.sha === s.sha);
    } else if (s.kind === "range") {
      cutoffIdx = commits.findIndex((c) => c.sha === s.toSha);
    }

    // Search from the cutoff toward newer commits (lower indices) first
    // — if none, fall through to older commits.
    for (let i = cutoffIdx - 1; i >= 0; i--) {
      if (!reviewed.has(commits[i]!.sha)) {
        selectCommit(commits[i]!.sha);
        return;
      }
    }
    for (let i = commits.length - 1; i > cutoffIdx; i--) {
      if (!reviewed.has(commits[i]!.sha)) {
        selectCommit(commits[i]!.sha);
        return;
      }
    }
    resetToHead();
  }

  function selectUnreviewed(): void {
    scope = { kind: "unreviewed" };
    if (currentOwner && currentName && currentNumber) {
      void loadDiff(currentOwner, currentName, currentNumber);
    }
  }

  // True when at least one commit is unreviewed. Used to enable/disable
  // the "since last review" control.
  function hasUnreviewed(): boolean {
    return getUnreviewedRange() !== null;
  }

  function stepPrev(): void {
    if (!commits) {
      void loadCommits();
      return;
    }
    if (commits.length === 0) return;
    const s = scope;
    if (s.kind === "head") {
      selectCommit(commits[0]!.sha);
    } else if (s.kind === "commit") {
      const idx = commits.findIndex((c) => c.sha === s.sha);
      if (idx < commits.length - 1) selectCommit(commits[idx + 1]!.sha);
    } else if (s.kind === "range") {
      selectCommit(s.fromSha);
    } else if (s.kind === "unreviewed") {
      const r = getUnreviewedRange();
      if (r) selectCommit(r.fromSha);
    } else if (s.kind === "patchsets") {
      // Commit-level navigation is orthogonal to patchset comparison —
      // stepping drops back to HEAD so j/k doesn't silently change the
      // interdiff pair under the user.
      resetToHead();
    }
  }

  function stepNext(): void {
    if (!commits) {
      void loadCommits();
      return;
    }
    if (commits.length === 0) return;
    const s = scope;
    if (s.kind === "head") {
      return;
    } else if (s.kind === "commit") {
      const idx = commits.findIndex((c) => c.sha === s.sha);
      if (idx > 0) {
        selectCommit(commits[idx - 1]!.sha);
      } else {
        resetToHead();
      }
    } else if (s.kind === "range") {
      selectCommit(s.toSha);
    } else if (s.kind === "unreviewed") {
      resetToHead();
    } else if (s.kind === "patchsets") {
      resetToHead();
    }
  }

  return {
    getDiff,
    getCurrentPR,
    isDiffLoading,
    getDiffError,
    getFileList,
    isFileListLoading,
    getTabWidth,
    getHideWhitespace,
    getLayout,
    getActiveFile,
    setActiveFile,
    isScrolling,
    clearScrolling,
    requestScrollToFile,
    getScrollTarget,
    consumeScrollTarget,
    registerFileReadiness,
    ensureFileReady,
    requestRevealLine,
    getRevealTarget,
    consumeRevealTarget,
    setTabWidth,
    setHideWhitespace,
    setLayout,
    isFileCollapsed,
    toggleFileCollapsed,
    getCollapseReason,
    setFilesCollapsed,
    expandForJump,
    getCollapseGlobs,
    saveCollapseGlobs,
    isLazy,
    isWorktreeChanged,
    getFileHunkState,
    requestHunks,
    loadDiff,
    reloadDiff: reloadDiffOnly,
    setActivePR,
    refresh,
    isRefreshing,
    getRefreshError,
    clearDiff,
    getScope,
    getCommits,
    getCurrentCommitSha,
    isCommitsLoading,
    getCommitsError,
    getCommitIndex,
    getActiveCommit,
    markCommitReviewed,
    isCommitReviewed,
    getReviewProgress,
    markFileReviewed,
    isFileReviewed,
    getFileReviewProgress,
    getDraft,
    hasDraftForPR,
    setDraftBody,
    setDraftEvent,
    addDraftComment,
    removeDraftComment,
    updateDraftCommentBody,
    requestEditDraft,
    getEditRequest,
    ackEditRequest,
    getDraftCommentsForPath,
    clearDraft,
    clearDraftCommentsForPath,
    getDraftSyncError: (): string | null => draftSyncError,
    getSuggestions,
    getSuggestionsForPath,
    getLastDiscarded,
    acceptSuggestion,
    acceptAllSuggestions,
    discardSuggestion,
    discardAllSuggestions,
    undoDiscard,
    updateSuggestionBody,
    reloadDrafts,
    publishDraft,
    whenDraftsSettled,
    loadCommits,
    loadPatchsets,
    getPatchsets,
    isPatchsetsLoading,
    getPatchsetsError,
    loadBlobRange,
    loadNotes,
    updateNotes,
    flushNotes,
    getNotes,
    getNotesUpdatedAt,
    isNotesLoaded,
    isNotesSaving,
    getNotesError,
    selectCommit,
    selectRange,
    extendSpan,
    getSpanAnchorSha,
    selectPatchsets,
    getInterdiff,
    resetToHead,
    selectUnreviewed,
    hasUnreviewed,
    jumpToNextUnreviewed,
    stepPrev,
    stepNext,
  };
}

export type DiffStore = ReturnType<typeof createDiffStore>;
