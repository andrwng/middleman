import type { DiffResult, FilesResult, CommitInfo } from "../api/types.js";

export type DiffScope =
  | { kind: "head" }
  | { kind: "commit"; sha: string }
  | { kind: "range"; fromSha: string; toSha: string }
  | { kind: "unreviewed" };

export interface DiffStoreOptions {
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

function loadCollapsedFiles(): Record<string, string[]> {
  try {
    const raw = safeGetItem("diff-collapsed-files");
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (
      typeof parsed !== "object" ||
      parsed === null ||
      Array.isArray(parsed)
    )
      return {};
    const result: Record<string, string[]> = {};
    for (const [key, value] of Object.entries(
      parsed as Record<string, unknown>,
    )) {
      if (
        Array.isArray(value) &&
        value.every((v) => typeof v === "string")
      ) {
        result[key] = value as string[];
      }
    }
    return result;
  } catch {
    return {};
  }
}

function saveCollapsedFiles(
  cf: Record<string, string[]>,
): void {
  safeSetItem("diff-collapsed-files", JSON.stringify(cf));
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

// Per-PR draft review state. Kept in localStorage so reloads don't
// lose pending comments. commit_id on each comment is critical —
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
}

export interface DraftReview {
  body: string;
  event: ReviewEvent;
  comments: DraftComment[];
}

function emptyDraft(): DraftReview {
  return { body: "", event: "COMMENT", comments: [] };
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

function saveDraftReviews(d: Record<string, DraftReview>): void {
  safeSetItem("diff-draft-reviews", JSON.stringify(d));
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

export function createDiffStore(opts?: DiffStoreOptions) {
  const getBasePath = opts?.getBasePath ?? (() => "/");

  let diff = $state<DiffResult | null>(null);
  let loading = $state(false);
  let storeError = $state<string | null>(null);
  let abortController: AbortController | null = null;

  let fileList = $state<FilesResult | null>(null);
  let fileListLoading = $state(false);
  let fileListAbortController: AbortController | null = null;

  let tabWidth = $state(loadTabWidth());
  let hideWhitespace = $state(
    safeGetItem("diff-hide-whitespace") === "true",
  );
  let layout = $state<DiffLayout>(loadLayout());
  let collapsedFiles = $state<Record<string, string[]>>(
    loadCollapsedFiles(),
  );
  let activeFile = $state<string | null>(null);
  let scrollTarget = $state<string | null>(null);
  let scrolling = $state(false);
  let commits = $state<CommitInfo[] | null>(null);
  let commitsLoading = $state(false);
  let commitsError = $state<string | null>(null);
  let scope = $state<DiffScope>({ kind: "head" });

  let currentOwner = $state("");
  let currentName = $state("");
  let currentNumber = $state(0);
  let reviewedCommits = $state<Record<string, string[]>>(loadReviewedCommits());
  let reviewedFiles = $state<Record<string, string[]>>(loadReviewedFiles());
  let draftReviews = $state<Record<string, DraftReview>>(loadDraftReviews());

  function getCurrentPR(): { owner: string; name: string; number: number } | null {
    if (!currentOwner) return null;
    return { owner: currentOwner, name: currentName, number: currentNumber };
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
    // Prefer diff.files once available — it respects hideWhitespace
    // and is authoritative. The lightweight /files response is a fast
    // preview used only until the full diff arrives.
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

  function isFileCollapsed(
    owner: string,
    name: string,
    number: number,
    filePath: string,
  ): boolean {
    const key = `${owner}/${name}#${number}`;
    return (collapsedFiles[key] ?? []).includes(filePath);
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

  function getScrollTarget(): string | null {
    return scrollTarget;
  }

  function consumeScrollTarget(): void {
    scrollTarget = null;
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
      const basePath = getBasePath();
      const syncURL =
        `${basePath}api/v1/repos/` +
        `${encodeURIComponent(currentOwner)}/` +
        `${encodeURIComponent(currentName)}/` +
        `pulls/${currentNumber}/sync`;
      const res = await fetch(syncURL, { method: "POST" });
      if (!res.ok) {
        refreshError = `Sync failed: ${res.status} ${res.statusText}`;
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
    abortController?.abort();
    // Abort any in-flight /files request so a late response from a
    // prior loadDiff() cannot repopulate fileList after we clear it.
    fileListAbortController?.abort();
    fileListAbortController = null;
    fileListLoading = false;
    const ac = new AbortController();
    abortController = ac;
    fileList = null;

    loading = true;
    storeError = null;
    try {
      const basePath = getBasePath();
      const reloadParams = scopeToDiffParams(scope);
      if (hideWhitespace) reloadParams.set("whitespace", "hide");
      const reloadQs = reloadParams.toString();
      const url =
        `${basePath}api/v1/repos/` +
        `${encodeURIComponent(currentOwner)}/` +
        `${encodeURIComponent(currentName)}/` +
        `pulls/${currentNumber}/diff` +
        (reloadQs ? `?${reloadQs}` : "");
      const data = await fetchJSON(url, ac.signal);
      if (abortController !== ac) return;
      diff = data as DiffResult;
      setActiveIfNeeded((data as DiffResult).files);
    } catch (err) {
      if (ac.signal.aborted) return;
      if (abortController !== ac) return;
      storeError =
        err instanceof Error ? err.message : String(err);
      diff = null;
    } finally {
      if (!ac.signal.aborted && abortController === ac) {
        loading = false;
      }
    }
  }

  function toggleFileCollapsed(
    owner: string,
    name: string,
    number: number,
    filePath: string,
  ): void {
    const key = `${owner}/${name}#${number}`;
    const current = collapsedFiles[key] ?? [];
    if (current.includes(filePath)) {
      collapsedFiles = {
        ...collapsedFiles,
        [key]: current.filter((f) => f !== filePath),
      };
    } else {
      collapsedFiles = {
        ...collapsedFiles,
        [key]: [...current, filePath],
      };
    }
    saveCollapsedFiles(collapsedFiles);
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
    currentOwner = owner;
    currentName = name;
    currentNumber = number;
    if (prChanged) {
      scope = { kind: "head" };
      commits = null;
      commitsLoading = false;
      commitsError = null;
    }

    abortController?.abort();
    fileListAbortController?.abort();
    const diffAc = new AbortController();
    const filesAc = new AbortController();
    abortController = diffAc;
    fileListAbortController = filesAc;

    diff = null;
    fileList = null;
    loading = true;
    fileListLoading = true;
    storeError = null;

    const basePath = getBasePath();
    const prefix =
      `${basePath}api/v1/repos/` +
      `${encodeURIComponent(owner)}/` +
      `${encodeURIComponent(name)}/` +
      `pulls/${number}`;

    const filesPromise = (async () => {
      try {
        const data = await fetchJSON(
          `${prefix}/files`,
          filesAc.signal,
        );
        if (fileListAbortController !== filesAc) return;
        fileList = data as FilesResult;
        setActiveIfNeeded((data as FilesResult).files);
      } catch {
        if (filesAc.signal.aborted) return;
        if (fileListAbortController !== filesAc) return;
        fileList = null;
      } finally {
        if (
          !filesAc.signal.aborted &&
          fileListAbortController === filesAc
        ) {
          fileListLoading = false;
        }
      }
    })();

    const diffPromise = (async () => {
      try {
        const params = scopeToDiffParams(scope);
        if (hideWhitespace) params.set("whitespace", "hide");
        const qs = params.toString();
        const url = `${prefix}/diff${qs ? `?${qs}` : ""}`;
        const data = await fetchJSON(url, diffAc.signal);
        if (abortController !== diffAc) return;
        diff = data as DiffResult;
        setActiveIfNeeded((data as DiffResult).files);
      } catch (_err) {
        if (diffAc.signal.aborted) return;
        if (abortController !== diffAc) return;
        storeError =
          _err instanceof Error ? _err.message : String(_err);
        diff = null;
        fileList = null;
        // Invalidate and abort /files so a late response cannot repopulate.
        fileListAbortController = null;
        filesAc.abort();
        fileListLoading = false;
      } finally {
        if (
          !diffAc.signal.aborted &&
          abortController === diffAc
        ) {
          loading = false;
        }
      }
    })();

    await Promise.allSettled([filesPromise, diffPromise]);
  }

  function clearDiff(): void {
    abortController?.abort();
    abortController = null;
    fileListAbortController?.abort();
    fileListAbortController = null;
    diff = null;
    fileList = null;
    storeError = null;
    loading = false;
    fileListLoading = false;
    activeFile = null;
    scrollTarget = null;
    scrolling = false;
    commits = null;
    commitsLoading = false;
    commitsError = null;
    scope = { kind: "head" };
    currentOwner = "";
    currentName = "";
    currentNumber = 0;
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

  function getScope(): DiffScope {
    return scope;
  }

  function getCommits(): CommitInfo[] | null {
    return commits;
  }

  function isCommitsLoading(): boolean {
    return commitsLoading;
  }

  function getCommitsError(): string | null {
    return commitsError;
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

  function setDraftBody(body: string): void {
    if (!currentOwner) return;
    const key = draftKey();
    const existing = draftReviews[key] ?? emptyDraft();
    draftReviews = { ...draftReviews, [key]: { ...existing, body } };
    saveDraftReviews(draftReviews);
  }

  function setDraftEvent(event: ReviewEvent): void {
    if (!currentOwner) return;
    const key = draftKey();
    const existing = draftReviews[key] ?? emptyDraft();
    draftReviews = { ...draftReviews, [key]: { ...existing, event } };
    saveDraftReviews(draftReviews);
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
    saveDraftReviews(draftReviews);
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
    saveDraftReviews(draftReviews);
  }

  function getDraftCommentsForPath(path: string): DraftComment[] {
    return getDraft().comments.filter((c) => c.path === path);
  }

  function clearDraft(): void {
    const key = draftKey();
    if (!draftReviews[key]) return;
    const next = { ...draftReviews };
    delete next[key];
    draftReviews = next;
    saveDraftReviews(draftReviews);
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

  function selectCommit(sha: string): void {
    scope = { kind: "commit", sha };
    if (currentOwner && currentName && currentNumber) {
      void loadDiff(currentOwner, currentName, currentNumber);
    }
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

  function resetToHead(): void {
    scope = { kind: "head" };
    if (currentOwner && currentName && currentNumber) {
      void loadDiff(currentOwner, currentName, currentNumber);
    }
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
    } else {
      // unreviewed → step into the oldest unreviewed commit.
      const r = getUnreviewedRange();
      if (r) selectCommit(r.fromSha);
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
    } else {
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
    setTabWidth,
    setHideWhitespace,
    setLayout,
    isFileCollapsed,
    toggleFileCollapsed,
    loadDiff,
    refresh,
    isRefreshing,
    getRefreshError,
    clearDiff,
    getScope,
    getCommits,
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
    setDraftBody,
    setDraftEvent,
    addDraftComment,
    removeDraftComment,
    getDraftCommentsForPath,
    clearDraft,
    loadCommits,
    selectCommit,
    selectRange,
    resetToHead,
    selectUnreviewed,
    hasUnreviewed,
    jumpToNextUnreviewed,
    stepPrev,
    stepNext,
  };
}

export type DiffStore = ReturnType<typeof createDiffStore>;
