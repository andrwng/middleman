import type {
  KanbanStatus,
  PullDetail,
} from "../api/types.js";
import type { MiddlemanClient } from "../types.js";

export interface PublishedReviewComment {
  id: number;
  author: string;
  body: string;
  createdAt: string;
  path: string;
  line: number;
  startLine: number | null;
  side: "LEFT" | "RIGHT";
  commitId: string;
  htmlUrl: string;
  inReplyTo: number;
  // True when this comment belongs to a thread whose root id is in
  // the active hidden set. The card renders dimmed instead of being
  // dropped from the map when `showHiddenThreads` is true.
  isHidden: boolean;
}

export interface DetailStoreOptions {
  client: MiddlemanClient;
  getPage?: () => string;
  pulls?: {
    loadPulls: (params?: unknown) => Promise<void>;
    optimisticKanbanUpdate?: (
      owner: string,
      name: string,
      number: number,
      status: KanbanStatus,
    ) => void;
    getPullKanbanStatus?: (
      owner: string,
      name: string,
      number: number,
    ) => KanbanStatus | undefined;
  };
  sync?: {
    subscribeSyncComplete: (
      cb: () => void,
    ) => () => void;
    refreshSyncStatus?: () => Promise<void>;
  };
}

function apiErrorMessage(
  error: { detail?: string; title?: string },
  fallback: string,
): string {
  return error.detail ?? error.title ?? fallback;
}

export function createDetailStore(
  opts: DetailStoreOptions,
) {
  const apiClient = opts.client;
  const getPage = opts.getPage ?? (() => "");
  const pullsDep = opts.pulls;
  const syncDep = opts.sync;

  // --- state ---

  let detail = $state<PullDetail | null>(null);
  let loading = $state(false);
  let syncing = $state(false);
  let storeError = $state<string | null>(null);
  let detailLoaded = $state(false);
  let syncGeneration = 0;

  let showHiddenThreads = $state(false);

  // platform_id → root platform_id, computed when needed. Mirrors
  // the server walk in db.ActiveHiddenReviewThreadRoots so hidden
  // replies surface the same way the server's predicate sees them.
  function buildReviewCommentRootMap(
    events: PullDetail["events"],
  ): Map<number, number> {
    const parent = new Map<number, number>();
    if (!events) return new Map();
    for (const e of events) {
      if (
        e.EventType !== "review_comment" ||
        e.PlatformID == null
      )
        continue;
      try {
        const meta = JSON.parse(
          e.MetadataJSON ?? "{}",
        ) as { in_reply_to?: number };
        const pid = e.PlatformID as number;
        if (
          meta.in_reply_to &&
          meta.in_reply_to !== pid
        ) {
          parent.set(pid, meta.in_reply_to);
        }
      } catch {
        /* ignore */
      }
    }
    const root = new Map<number, number>();
    for (const e of events) {
      if (
        e.EventType !== "review_comment" ||
        e.PlatformID == null
      )
        continue;
      let cur = e.PlatformID as number;
      for (let i = 0; i < 32; i++) {
        const p = parent.get(cur);
        if (p == null) break;
        cur = p;
      }
      root.set(e.PlatformID as number, cur);
    }
    return root;
  }

  // Memoize the root map by `detail` identity. Every mutation to
  // detail reassigns it via `detail = { ...detail, ... }`, so a
  // reference-equality check is sufficient for invalidation. This
  // keeps getReviewCommentsByFilePath / getReviewCommentRootForPlatformID
  // from rebuilding the parent walk on every call.
  let cachedRootMapDetail: PullDetail | null = null;
  let cachedRootMap: Map<number, number> = new Map();

  function getReviewCommentRootMap(): Map<number, number> {
    if (detail === null) return new Map();
    if (cachedRootMapDetail === detail) return cachedRootMap;
    cachedRootMap = buildReviewCommentRootMap(
      detail.events ?? [],
    );
    cachedRootMapDetail = detail;
    return cachedRootMap;
  }

  // Per-PR monotonic counters for kanban updates.
  const kanbanSeqByPR = new Map<string, number>();

  // --- polling ---

  let detailPollHandle: ReturnType<
    typeof setInterval
  > | null = null;
  let unsubSyncComplete: (() => void) | null = null;

  // --- reads ---

  function getDetail(): PullDetail | null {
    return detail;
  }

  function isDetailLoading(): boolean {
    return loading;
  }

  function isDetailSyncing(): boolean {
    return syncing;
  }

  function getDetailError(): string | null {
    return storeError;
  }

  function getDetailLoaded(): boolean {
    return detailLoaded;
  }

  // Returns the number of review_comment events attached to each commit
  // SHA in the current PR, parsed from the event's MetadataJSON. Used by
  // the commit list to badge commits that have existing review threads.
  function getCommitCommentCounts(): Map<string, number> {
    const out = new Map<string, number>();
    const events = detail?.events;
    if (!events) return out;
    for (const e of events) {
      if (e.EventType !== "review_comment") continue;
      const raw = e.MetadataJSON;
      if (!raw) continue;
      try {
        const meta = JSON.parse(raw) as { commit_id?: string };
        const sha = meta.commit_id;
        if (!sha) continue;
        out.set(sha, (out.get(sha) ?? 0) + 1);
      } catch {
        /* ignore */
      }
    }
    return out;
  }

  // Returns a map from file path to an array of published (synced)
  // review comments on that file. Each entry has anchor info the
  // diff view can use to render the comment inline.
  function getReviewCommentsByFilePath(): Map<string, PublishedReviewComment[]> {
    const out = new Map<string, PublishedReviewComment[]>();
    const events = detail?.events;
    if (!events) return out;
    const hidden = getHiddenRootSet();
    const roots = getReviewCommentRootMap();

    for (const e of events) {
      if (e.EventType !== "review_comment") continue;
      const raw = e.MetadataJSON;
      if (!raw) continue;
      try {
        const meta = JSON.parse(raw) as {
          path?: string;
          line?: number;
          start_line?: number;
          side?: string;
          commit_id?: string;
          html_url?: string;
          in_reply_to?: number;
        };
        const path = meta.path;
        if (!path) continue;
        const side = meta.side === "LEFT" ? "LEFT" : "RIGHT";
        // e.ID is our local DB row id; the PR review-comment API
        // expects the GitHub comment id, which we store as
        // PlatformID. Mixing these up made "reply to a comment"
        // send our autoincrement, which GitHub doesn't recognize.
        const ghID = (e.PlatformID ?? 0) as number;
        if (!ghID) continue;

        const isHidden = hidden.has(roots.get(ghID) ?? ghID);
        if (isHidden && !showHiddenThreads) continue;

        const list = out.get(path) ?? [];
        list.push({
          id: ghID,
          author: e.Author,
          body: e.Body,
          createdAt: e.CreatedAt,
          path,
          line: meta.line ?? 0,
          startLine: meta.start_line ?? null,
          side,
          commitId: meta.commit_id ?? "",
          htmlUrl: meta.html_url ?? "",
          inReplyTo: meta.in_reply_to ?? 0,
          isHidden,
        });
        out.set(path, list);
      } catch {
        /* ignore */
      }
    }
    // Sort each file's comments oldest-first (by created date) so
    // replies land below the parent when rendered in a thread.
    for (const [, list] of out) {
      list.sort((a, b) =>
        a.createdAt.localeCompare(b.createdAt),
      );
    }
    return out;
  }

  function isStaleRefreshing(): boolean {
    if (!detail || !syncing) return false;
    const fetchedAt = detail.detail_fetched_at;
    if (!fetchedAt) return false;
    const fetchedMs = new Date(fetchedAt).getTime();
    const updatedMs = new Date(
      detail.merge_request.UpdatedAt,
    ).getTime();
    const hourAgo = Date.now() - 3_600_000;
    return fetchedMs < hourAgo && updatedMs > fetchedMs;
  }

  // --- internal helpers ---

  function prKey(
    owner: string,
    name: string,
    number: number,
  ): string {
    return `${owner}/${name}/${number}`;
  }

  function isDetailShowing(
    owner: string,
    name: string,
    number: number,
  ): boolean {
    return (
      detail !== null &&
      detail.repo_owner === owner &&
      detail.repo_name === name &&
      detail.merge_request.Number === number
    );
  }

  async function refreshPullsIfActive(): Promise<void> {
    if (getPage() === "pulls" && pullsDep) {
      await pullsDep.loadPulls();
    }
  }

  async function refreshDetail(
    owner: string,
    name: string,
    number: number,
  ): Promise<void> {
    const gen = syncGeneration;
    try {
      const { data } = await apiClient.GET(
        "/repos/{owner}/{name}/pulls/{number}",
        { params: { path: { owner, name, number } } },
      );
      if (gen !== syncGeneration) return;
      if (data !== undefined) {
        detail = {
          ...data,
          events: data.events ?? [],
        } as PullDetail;
        detailLoaded = data.detail_loaded ?? detailLoaded;
      }
    } catch {
      // Silent refresh
    }
  }

  async function syncDetail(
    owner: string,
    name: string,
    number: number,
    gen: number,
  ): Promise<void> {
    syncing = true;
    try {
      const { data, error: requestError } =
        await apiClient.POST(
          "/repos/{owner}/{name}/pulls/{number}/sync",
          { params: { path: { owner, name, number } } },
        );
      if (gen !== syncGeneration) return;
      if (requestError) {
        throw new Error(
          apiErrorMessage(requestError, "sync failed"),
        );
      }
      if (data) {
        storeError = null;
        detail = {
          ...data,
          events: data.events ?? [],
        } as PullDetail;
        detailLoaded =
          data.detail_loaded ?? detailLoaded;
      }
    } catch {
      // Sync failure is non-fatal.
    } finally {
      if (gen === syncGeneration) syncing = false;
    }
    // Always refresh rate limits -- the API calls happened
    // regardless of whether user navigated away.
    void syncDep?.refreshSyncStatus?.();
    if (gen === syncGeneration) {
      await refreshPullsIfActive();
    }
  }

  // --- writes ---

  function clearDetail(): void {
    ++syncGeneration;
    detail = null;
    loading = false;
    syncing = false;
    storeError = null;
    detailLoaded = false;
  }

  async function loadDetail(
    owner: string,
    name: string,
    number: number,
  ): Promise<void> {
    const gen = ++syncGeneration;

    loading = true;
    syncing = false;
    storeError = null;
    detail = null;
    detailLoaded = false;
    try {
      const { data, error: requestError } =
        await apiClient.GET(
          "/repos/{owner}/{name}/pulls/{number}",
          { params: { path: { owner, name, number } } },
        );
      if (gen !== syncGeneration) return;
      if (requestError) {
        throw new Error(
          requestError.detail ??
            requestError.title ??
            "failed to load pull request",
        );
      }
      detail = data
        ? ({
            ...data,
            events: data.events ?? [],
          } as PullDetail)
        : null;
      detailLoaded = data?.detail_loaded ?? false;
    } catch (err) {
      if (gen !== syncGeneration) return;
      storeError =
        err instanceof Error ? err.message : String(err);
    } finally {
      if (gen === syncGeneration) loading = false;
    }

    if (gen === syncGeneration) {
      void syncDetail(owner, name, number, gen);
    }
  }

  async function refreshDetailOnly(
    owner: string,
    name: string,
    number: number,
  ): Promise<void> {
    await refreshDetail(owner, name, number);
  }

  async function updateKanbanState(
    owner: string,
    name: string,
    number: number,
    status: KanbanStatus,
  ): Promise<void> {
    const key = prKey(owner, name, number);
    const seq = (kanbanSeqByPR.get(key) ?? 0) + 1;
    kanbanSeqByPR.set(key, seq);

    const prevDetailStatus = isDetailShowing(
      owner,
      name,
      number,
    )
      ? (detail!.merge_request
          .KanbanStatus as KanbanStatus)
      : undefined;
    const prevPullsStatus =
      pullsDep?.getPullKanbanStatus?.(
        owner,
        name,
        number,
      );

    if (prevDetailStatus !== undefined) {
      detail = {
        ...detail!,
        merge_request: {
          ...detail!.merge_request,
          KanbanStatus: status,
        },
      };
    }
    pullsDep?.optimisticKanbanUpdate?.(
      owner,
      name,
      number,
      status,
    );

    try {
      const { error: requestError } =
        await apiClient.PUT(
          "/repos/{owner}/{name}/pulls/{number}/state",
          {
            params: { path: { owner, name, number } },
            body: { status },
          },
        );
      if (requestError) {
        throw new Error(
          requestError.detail ??
            requestError.title ??
            "failed to update kanban state",
        );
      }
    } catch (err) {
      if (seq === kanbanSeqByPR.get(key)) {
        storeError =
          err instanceof Error
            ? err.message
            : String(err);
        if (
          prevDetailStatus !== undefined &&
          isDetailShowing(owner, name, number)
        ) {
          detail = {
            ...detail!,
            merge_request: {
              ...detail!.merge_request,
              KanbanStatus: prevDetailStatus,
            },
          };
        }
        if (prevPullsStatus !== undefined) {
          pullsDep?.optimisticKanbanUpdate?.(
            owner,
            name,
            number,
            prevPullsStatus,
          );
        }
        const reloads: Promise<void>[] = [];
        if (pullsDep) reloads.push(pullsDep.loadPulls());
        if (isDetailShowing(owner, name, number)) {
          reloads.push(
            loadDetail(owner, name, number),
          );
        }
        await Promise.all(reloads);
        if (seq === kanbanSeqByPR.get(key)) {
          kanbanSeqByPR.set(key, seq - 1);
        }
      }
      return;
    }

    if (seq === kanbanSeqByPR.get(key)) {
      const refreshes: Promise<void>[] = [
        refreshPullsIfActive(),
      ];
      if (isDetailShowing(owner, name, number)) {
        refreshes.push(
          loadDetail(owner, name, number),
        );
      }
      await Promise.all(refreshes);
    }
  }

  async function updatePRContent(
    owner: string,
    name: string,
    number: number,
    fields: { title?: string; body?: string },
  ): Promise<void> {
    if (!detail || !isDetailShowing(owner, name, number))
      return;

    const prevTitle = detail.merge_request.Title;
    const prevBody = detail.merge_request.Body;

    // Optimistic update.
    detail = {
      ...detail,
      merge_request: {
        ...detail.merge_request,
        ...(fields.title !== undefined && {
          Title: fields.title,
        }),
        ...(fields.body !== undefined && {
          Body: fields.body,
        }),
      },
    };

    try {
      const { data, error: requestError } =
        await apiClient.PATCH(
          "/repos/{owner}/{name}/pulls/{number}",
          {
            params: { path: { owner, name, number } },
            body: fields,
          },
        );
      if (requestError) {
        throw new Error(
          apiErrorMessage(
            requestError,
            "failed to update PR",
          ),
        );
      }
      // Apply server-canonical response.
      if (data && isDetailShowing(owner, name, number)) {
        detail = data as PullDetail;
      }
    } catch (err) {
      storeError =
        err instanceof Error ? err.message : String(err);
      // Revert optimistic update.
      if (
        isDetailShowing(owner, name, number) &&
        detail
      ) {
        detail = {
          ...detail,
          merge_request: {
            ...detail.merge_request,
            Title: prevTitle,
            Body: prevBody,
          },
        };
      }
      throw err;
    }
    // Refresh pulls list independently -- don't let a
    // refresh failure revert a successful edit.
    refreshPullsIfActive().catch(() => {});
  }

  function startDetailPolling(
    owner: string,
    name: string,
    number: number,
  ): void {
    stopDetailPolling();
    detailPollHandle = setInterval(() => {
      void refreshDetail(owner, name, number);
    }, 60_000);
    if (syncDep) {
      unsubSyncComplete =
        syncDep.subscribeSyncComplete(() => {
          void refreshDetail(owner, name, number);
        });
    }
  }

  function stopDetailPolling(): void {
    if (detailPollHandle !== null) {
      clearInterval(detailPollHandle);
      detailPollHandle = null;
    }
    if (unsubSyncComplete !== null) {
      unsubSyncComplete();
      unsubSyncComplete = null;
    }
  }

  async function toggleDetailPRStar(
    owner: string,
    name: string,
    number: number,
    currentlyStarred: boolean,
  ): Promise<void> {
    if (detail !== null) {
      detail = {
        ...detail,
        merge_request: {
          ...detail.merge_request,
          Starred: !currentlyStarred,
        },
      };
    }
    try {
      if (currentlyStarred) {
        const { error: requestError } =
          await apiClient.DELETE("/starred", {
            body: {
              item_type: "pr",
              owner,
              name,
              number,
            },
          });
        if (requestError) {
          throw new Error(
            requestError.detail ??
              requestError.title ??
              "failed to unstar pull request",
          );
        }
      } else {
        const { error: requestError } =
          await apiClient.PUT("/starred", {
            body: {
              item_type: "pr",
              owner,
              name,
              number,
            },
          });
        if (requestError) {
          throw new Error(
            requestError.detail ??
              requestError.title ??
              "failed to star pull request",
          );
        }
      }
    } catch (err) {
      storeError =
        err instanceof Error ? err.message : String(err);
      if (detail !== null) {
        detail = {
          ...detail,
          merge_request: {
            ...detail.merge_request,
            Starred: currentlyStarred,
          },
        };
      }
      return;
    }
    await refreshPullsIfActive();
  }

  async function submitComment(
    owner: string,
    name: string,
    number: number,
    body: string,
  ): Promise<void> {
    storeError = null;
    try {
      const { error: requestError } =
        await apiClient.POST(
          "/repos/{owner}/{name}/pulls/{number}/comments",
          {
            params: { path: { owner, name, number } },
            body: { body },
          },
        );
      if (requestError) {
        throw new Error(
          requestError.detail ??
            requestError.title ??
            "failed to post comment",
        );
      }
    } catch (err) {
      storeError =
        err instanceof Error ? err.message : String(err);
      return;
    }
    // Supersede any in-flight syncDetail so its stale response
    // cannot overwrite the detail we are about to fetch.
    const gen = ++syncGeneration;
    syncing = false;
    // Silent refresh: avoid flipping loading flag, which would
    // unmount the detail tree and reset scroll position.
    await refreshDetail(owner, name, number);
    // Pull authoritative state from GitHub so PR row metadata
    // (last_activity_at, comment_count) and the pulls list catch
    // up. Skip if the user navigated away mid-refresh.
    if (gen === syncGeneration) {
      void syncDetail(owner, name, number, gen);
    }
  }

  function getHiddenRootSet(): Set<number> {
    const ids = detail?.hidden_thread_root_ids ?? [];
    return new Set(ids);
  }

  function getHiddenThreadCount(): number {
    return detail?.hidden_thread_root_ids?.length ?? 0;
  }

  function isShowingHiddenThreads(): boolean {
    return showHiddenThreads;
  }

  function setShowHiddenThreads(next: boolean): void {
    showHiddenThreads = next;
  }

  function getReviewCommentRootForPlatformID(
    platformID: number,
  ): number {
    const roots = getReviewCommentRootMap();
    return roots.get(platformID) ?? platformID;
  }

  async function hideReviewThread(
    rootPlatformID: number,
  ): Promise<void> {
    if (!detail) return;
    const ownerRepo = {
      owner: detail.repo_owner,
      name: detail.repo_name,
      number: detail.merge_request.Number,
    };
    const prev = detail.hidden_thread_root_ids ?? [];
    if (prev.includes(rootPlatformID)) return;
    detail = {
      ...detail,
      hidden_thread_root_ids: [...prev, rootPlatformID],
    } as PullDetail;
    const { error } = await apiClient.POST(
      "/repos/{owner}/{name}/pulls/{number}/hidden-threads",
      {
        params: { path: ownerRepo },
        body: { root_comment_id: rootPlatformID },
      },
    );
    if (error) {
      // Guard against PR navigation between optimistic write and
      // error response -- the original PR's state is no longer in
      // memory, so there is nothing to revert.
      if (
        !isDetailShowing(
          ownerRepo.owner,
          ownerRepo.name,
          ownerRepo.number,
        )
      ) {
        return;
      }
      // Roll back
      const reverted = (
        detail?.hidden_thread_root_ids ?? []
      ).filter((id) => id !== rootPlatformID);
      if (detail) {
        detail = {
          ...detail,
          hidden_thread_root_ids: reverted,
        } as PullDetail;
      }
      storeError = apiErrorMessage(
        error,
        "failed to hide review thread",
      );
    }
  }

  async function unhideReviewThread(
    rootPlatformID: number,
  ): Promise<void> {
    if (!detail) return;
    const ownerRepo = {
      owner: detail.repo_owner,
      name: detail.repo_name,
      number: detail.merge_request.Number,
    };
    const prev = detail.hidden_thread_root_ids ?? [];
    if (!prev.includes(rootPlatformID)) return;
    detail = {
      ...detail,
      hidden_thread_root_ids: prev.filter(
        (id) => id !== rootPlatformID,
      ),
    } as PullDetail;
    const { error } = await apiClient.DELETE(
      "/repos/{owner}/{name}/pulls/{number}/hidden-threads/{root_comment_id}",
      {
        params: {
          path: {
            ...ownerRepo,
            root_comment_id: rootPlatformID,
          },
        },
      },
    );
    if (error) {
      // Guard against PR navigation between optimistic write and
      // error response -- the original PR's state is no longer in
      // memory, so there is nothing to revert.
      if (
        !isDetailShowing(
          ownerRepo.owner,
          ownerRepo.name,
          ownerRepo.number,
        )
      ) {
        return;
      }
      if (detail) {
        detail = {
          ...detail,
          hidden_thread_root_ids: [
            ...(detail.hidden_thread_root_ids ?? []),
            rootPlatformID,
          ],
        } as PullDetail;
      }
      storeError = apiErrorMessage(
        error,
        "failed to unhide review thread",
      );
    }
  }

  return {
    getDetail,
    isDetailLoading,
    isDetailSyncing,
    getDetailError,
    getDetailLoaded,
    isStaleRefreshing,
    getCommitCommentCounts,
    getReviewCommentsByFilePath,
    getHiddenRootSet,
    getHiddenThreadCount,
    isShowingHiddenThreads,
    setShowHiddenThreads,
    getReviewCommentRootForPlatformID,
    hideReviewThread,
    unhideReviewThread,
    clearDetail,
    loadDetail,
    refreshDetailOnly,
    updateKanbanState,
    updatePRContent,
    startDetailPolling,
    stopDetailPolling,
    toggleDetailPRStar,
    submitComment,
  };
}

export type DetailStore = ReturnType<
  typeof createDetailStore
>;
