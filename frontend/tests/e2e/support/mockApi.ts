import type { Page, Route } from "@playwright/test";

// Local worktree fixture — owner "local", repo "myproject", worktree ID 7.
// The worktree ID is used as the PR number in the local-pulls URL pattern
// /pulls/local/<repo_name>/<id> and in worktree API calls /worktrees/<id>/...
const LOCAL_WORKTREE_ID = 7;
const LOCAL_REPO_NAME = "myproject";
const LOCAL_DOC_CONTENT =
  "# Hello\n\nsome text here\n\n" +
  // A bullet whose bold phrase wraps across a source line — regression
  // fixture for inline markup that straddles a soft-wrapped boundary.
  "- **This leading phrase is bold and it\n  wraps to a second source line** then plain.\n\n" +
  "[details section](links.md#details)\n";
const LOCAL_DIAGRAM_CONTENT = "# Diagram\n\n```mermaid\ngraph TD;\n  A-->B;\n```\n";
const LOCAL_LINKS_CONTENT =
  "# Top\n\n[jump to details](#details) or [see the readme](README.md)\n\n" +
  "Filler paragraph for scroll distance.\n\n".repeat(40) +
  "## Details\n\nThe details section.\n";

const localWorktreePull = {
  ID: LOCAL_WORKTREE_ID,
  RepoID: 99,
  GitHubID: 0,
  Number: LOCAL_WORKTREE_ID,
  URL: "",
  Title: "Local worktree: myproject",
  Author: "",
  State: "open",
  IsDraft: false,
  Body: "",
  HeadBranch: "main",
  BaseBranch: "main",
  Additions: 0,
  Deletions: 0,
  CommentCount: 0,
  ReviewDecision: "",
  CIStatus: "",
  CIChecksJSON: "[]",
  CreatedAt: "2026-06-01T00:00:00Z",
  UpdatedAt: "2026-06-01T00:00:00Z",
  LastActivityAt: "2026-06-01T00:00:00Z",
  MergedAt: null,
  ClosedAt: null,
  KanbanStatus: "new",
  Starred: false,
  repo_owner: "local",
  repo_name: LOCAL_REPO_NAME,
  platform_host: "",
  worktree_links: [],
};

const localWorktrees = {
  worktrees: [
    {
      id: LOCAL_WORKTREE_ID,
      repo_owner: "local",
      repo_name: LOCAL_REPO_NAME,
      path: "/home/dev/myproject",
      branch: "main",
      head_sha: "abc123",
      is_detached: false,
      is_locked: false,
      is_prunable: false,
      discovered_at: "2026-06-01T00:00:00Z",
      last_seen_at: "2026-06-01T00:00:00Z",
    },
  ],
};

const pulls = [
  {
    ID: 1,
    RepoID: 1,
    GitHubID: 101,
    Number: 42,
    URL: "https://github.com/acme/widgets/pull/42",
    Title: "Add browser regression coverage",
    Author: "marius",
    State: "open",
    IsDraft: false,
    Body: "Adds Playwright smoke tests for workspace panel.",
    HeadBranch: "feature/playwright",
    BaseBranch: "main",
    Additions: 120,
    Deletions: 12,
    CommentCount: 3,
    ReviewDecision: "APPROVED",
    CIStatus: "success",
    CIChecksJSON: "[]",
    CreatedAt: "2026-03-29T14:00:00Z",
    UpdatedAt: "2026-03-30T14:00:00Z",
    LastActivityAt: "2026-03-30T14:00:00Z",
    MergedAt: null,
    ClosedAt: null,
    KanbanStatus: "reviewing",
    Starred: false,
    repo_owner: "acme",
    repo_name: "widgets",
    platform_host: "github.com",
    worktree_links: [],
  },
  {
    ID: 2,
    RepoID: 2,
    GitHubID: 201,
    Number: 42,
    URL: "https://example.com/acme/widgets/pull/42",
    Title: "Mirror host stub PR",
    Author: "marius",
    State: "open",
    IsDraft: false,
    Body: "",
    HeadBranch: "mirror/stub",
    BaseBranch: "main",
    Additions: 1,
    Deletions: 0,
    CommentCount: 0,
    ReviewDecision: "",
    CIStatus: "success",
    CIChecksJSON: "[]",
    CreatedAt: "2026-03-29T14:00:00Z",
    UpdatedAt: "2026-03-30T14:00:00Z",
    LastActivityAt: "2026-03-30T14:00:00Z",
    MergedAt: null,
    ClosedAt: null,
    KanbanStatus: "new",
    Starred: false,
    repo_owner: "acme",
    repo_name: "widgets",
    platform_host: "example.com",
    worktree_links: [],
  },
  {
    ID: 3,
    RepoID: 1,
    GitHubID: 301,
    Number: 55,
    URL: "https://github.com/acme/widgets/pull/55",
    Title: "Refactor theme system",
    Author: "luisa",
    State: "open",
    IsDraft: false,
    Body: "Consolidates theme tokens.",
    HeadBranch: "refactor/theme",
    BaseBranch: "main",
    Additions: 80,
    Deletions: 40,
    CommentCount: 0,
    ReviewDecision: "",
    CIStatus: "pending",
    CIChecksJSON: "[]",
    CreatedAt: "2026-03-29T14:00:00Z",
    UpdatedAt: "2026-03-30T14:00:00Z",
    LastActivityAt: "2026-03-30T14:00:00Z",
    MergedAt: null,
    ClosedAt: null,
    KanbanStatus: "new",
    Starred: false,
    repo_owner: "acme",
    repo_name: "widgets",
    platform_host: "github.com",
    worktree_links: [
      {
        worktree_key: "projects/theme-rework",
        worktree_branch: "refactor/theme",
      },
    ],
  },
];

const issues = [
  {
    ID: 2,
    RepoID: 1,
    GitHubID: 202,
    Number: 7,
    URL: "https://github.com/acme/widgets/issues/7",
    Title: "Theme toggle does not stick",
    Author: "marius",
    State: "open",
    Body: "",
    CommentCount: 1,
    LabelsJSON: "[]",
    CreatedAt: "2026-03-28T14:00:00Z",
    UpdatedAt: "2026-03-30T14:00:00Z",
    LastActivityAt: "2026-03-30T14:00:00Z",
    ClosedAt: null,
    Starred: false,
    repo_owner: "acme",
    repo_name: "widgets",
  },
];

const repos = [
  {
    ID: 1,
    Owner: "acme",
    Name: "widgets",
    AllowSquashMerge: true,
    AllowMergeCommit: true,
    AllowRebaseMerge: true,
    LastSyncStartedAt: "2026-03-30T14:00:00Z",
    LastSyncCompletedAt: "2026-03-30T14:00:30Z",
    LastSyncError: "",
    CreatedAt: "2026-03-01T00:00:00Z",
  },
];

const syncStatus = {
  running: false,
  last_run_at: "2026-03-30T14:00:30Z",
  last_error: "",
};

function makeRateLimits() {
  const now = Date.now();
  return {
    hosts: {
      "github.com": {
        requests_hour: 188,
        rate_remaining: 4812,
        rate_limit: 5000,
        rate_reset_at: new Date(now + 42 * 60_000).toISOString(),
        hour_start: new Date(now - 18 * 60_000).toISOString(),
        sync_throttle_factor: 1,
        sync_paused: false,
        reserve_buffer: 200,
        known: true,
        budget_limit: 500,
        budget_spent: 42,
        budget_remaining: 458,
        gql_remaining: 4950,
        gql_limit: 5000,
        gql_reset_at: new Date(now + 38 * 60_000).toISOString(),
        gql_known: true,
      },
    },
  };
}

// --- Review-threads mock state (stateful across GET/POST within a test) ---
//
// A module-scoped in-memory store so a thread created via POST is returned
// by a subsequent GET, mirroring the real server. Reset at the top of
// mockApi() so each test starts clean regardless of module/worker reuse
// across the spec file. Field names are snake_case, matching
// ReviewThreadDraft / ReviewThreadResponse in the generated API schema.

interface ReviewThreadDraftCommentRecord {
  author: "user" | "agent";
  body: string;
}

interface ReviewThreadDraftRecord {
  path: string;
  side: "LEFT" | "RIGHT";
  line: number;
  start_line?: number;
  commit_sha: string;
  body: string;
  comments?: ReviewThreadDraftCommentRecord[];
}

// One create-review-threads POST body, recorded verbatim so tests can
// assert `mode` (persist-only vs act-immediately) and per-doc scope (which
// paths were actually sent) without re-deriving it from the DOM.
export interface CreateReviewThreadsRequestRecord {
  mode?: "discuss-first" | "act-immediately" | "persist-only";
  threads: ReviewThreadDraftRecord[];
}

interface ReviewThreadCommentRecord {
  id: number;
  author: "user" | "agent";
  body: string;
  created_at: string;
  sent_to_agent: boolean;
}

export interface ReviewThreadRecord {
  id: number;
  path: string;
  side: "LEFT" | "RIGHT";
  line: number;
  start_line?: number;
  commit_sha: string;
  status: "open" | "discussed" | "applied" | "resolved";
  hidden: boolean;
  writes_allowed: boolean;
  created_at: string;
  updated_at: string;
  comments: ReviewThreadCommentRecord[];
}

let createdReviewThreads: ReviewThreadRecord[] = [];
let reviewThreadsRequests: CreateReviewThreadsRequestRecord[] = [];
let nextReviewThreadId = 1;
let nextReviewCommentId = 1;

// Read back every create-threads POST body sent this test.
export function getReviewThreadsRequests(): CreateReviewThreadsRequestRecord[] {
  return reviewThreadsRequests;
}

// Read back the thread list the mock is currently serving (i.e. what a
// subsequent GET would return) — used to assert a thread was persisted.
export function getCreatedReviewThreads(): ReviewThreadRecord[] {
  return createdReviewThreads;
}

async function fulfillJson(route: Route, body: unknown, status = 200): Promise<void> {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

export async function mockApi(page: Page): Promise<void> {
  // Reset the review-threads mock state so each test starts clean,
  // regardless of module/worker reuse across the spec file.
  createdReviewThreads = [];
  reviewThreadsRequests = [];
  nextReviewThreadId = 1;
  nextReviewCommentId = 1;

  // Deep-clone so mutations (e.g. PATCH) don't leak between tests.
  // Append the local-worktree pull so it appears in the sidebar list and
  // the singlePrMatch handler can find it by owner/name/number.
  const localPulls: (typeof pulls[number] | typeof localWorktreePull)[] = [
    ...JSON.parse(JSON.stringify(pulls)),
    JSON.parse(JSON.stringify(localWorktreePull)),
  ];

  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const { pathname } = url;
    const method = route.request().method();

    if (method === "GET" && pathname === "/api/v1/pulls") {
      await fulfillJson(route, localPulls);
      return;
    }

    const singlePrMatch = pathname.match(
      /^\/api\/v1\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)$/,
    );
    if (method === "GET" && singlePrMatch) {
      const prOwner = singlePrMatch[1];
      const prName = singlePrMatch[2];
      const prNumber = parseInt(singlePrMatch[3]!, 10);
      const pr = localPulls.find(
        (p) =>
          p.repo_owner === prOwner &&
          p.repo_name === prName &&
          p.Number === prNumber,
      );
      if (pr) {
        await fulfillJson(route, {
          merge_request: pr,
          repo_owner: pr.repo_owner,
          repo_name: pr.repo_name,
          detail_loaded: true,
          detail_fetched_at: "2026-03-30T14:00:00Z",
          worktree_links: pr.worktree_links,
        });
      } else {
        await fulfillJson(
          route,
          { error: "Not found" },
          404,
        );
      }
      return;
    }

    if (method === "GET" && pathname === "/api/v1/issues") {
      await fulfillJson(route, issues);
      return;
    }

    if (method === "GET" && pathname === "/api/v1/repos") {
      await fulfillJson(route, repos);
      return;
    }

    if (method === "GET" && pathname === "/api/v1/sync/status") {
      await fulfillJson(route, syncStatus);
      return;
    }

    if (method === "GET" && pathname === "/api/v1/rate-limits") {
      await fulfillJson(route, makeRateLimits());
      return;
    }

    if (method === "POST" && pathname === "/api/v1/sync") {
      await fulfillJson(route, undefined, 202);
      return;
    }

    const patchPrMatch = pathname.match(
      /^\/api\/v1\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)$/,
    );
    if (method === "PATCH" && patchPrMatch) {
      const prOwner = patchPrMatch[1];
      const prName = patchPrMatch[2];
      const prNumber = parseInt(patchPrMatch[3]!, 10);
      const pr = localPulls.find(
        (p) =>
          p.repo_owner === prOwner &&
          p.repo_name === prName &&
          p.Number === prNumber,
      );
      if (!pr) {
        await fulfillJson(route, { title: "Not found" }, 404);
        return;
      }
      const reqBody = JSON.parse(
        (await route.request().postData()) ?? "{}",
      );
      if (reqBody.title !== undefined) pr.Title = reqBody.title;
      if (reqBody.body !== undefined) pr.Body = reqBody.body;
      await fulfillJson(route, {
        merge_request: pr,
        repo_owner: pr.repo_owner,
        repo_name: pr.repo_name,
        detail_loaded: true,
        detail_fetched_at: "2026-03-30T14:00:00Z",
        worktree_links: pr.worktree_links,
      });
      return;
    }

    // --- Local worktree endpoints ---

    if (method === "GET" && pathname === "/api/v1/worktrees") {
      await fulfillJson(route, localWorktrees);
      return;
    }

    if (method === "GET" && pathname === "/api/v1/worktrees/running-turns") {
      await fulfillJson(route, { worktree_ids: [] });
      return;
    }

    const markdownFilesMatch = pathname.match(
      /^\/api\/v1\/worktrees\/(\d+)\/markdown-files$/,
    );
    if (method === "GET" && markdownFilesMatch) {
      await fulfillJson(route, { files: ["README.md", "diagram.md", "links.md"] });
      return;
    }

    // Blob endpoint — serves live working-tree content for the doc review.
    // Pattern: /api/v1/repos/<owner>/<name>/pulls/<number>/blob?path=...&sha=WORKING-TREE
    const blobMatch = pathname.match(
      /^\/api\/v1\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)\/blob$/,
    );
    if (method === "GET" && blobMatch) {
      const blobOwner = blobMatch[1];
      const blobPath = url.searchParams.get("path") ?? "";
      if (blobOwner === "local" && blobPath === "README.md") {
        await fulfillJson(route, { content: LOCAL_DOC_CONTENT, truncated: false });
      } else if (blobOwner === "local" && blobPath === "diagram.md") {
        await fulfillJson(route, { content: LOCAL_DIAGRAM_CONTENT, truncated: false });
      } else if (blobOwner === "local" && blobPath === "links.md") {
        await fulfillJson(route, { content: LOCAL_LINKS_CONTENT, truncated: false });
      } else {
        await fulfillJson(route, { error: "Not found" }, 404);
      }
      return;
    }

    // Review threads for local worktree — stateful in-memory store: GET
    // lists whatever has been created so far; POST appends threads built
    // from the request's drafts, preserving path/side/line/start_line/
    // commit_sha so each thread anchors to the same doc block the draft
    // was made on.
    const reviewThreadsMatch = pathname.match(
      /^\/api\/v1\/repos\/local\/[^/]+\/pulls\/\d+\/review-threads$/,
    );
    if (method === "GET" && reviewThreadsMatch) {
      await fulfillJson(route, { threads: createdReviewThreads });
      return;
    }
    if (method === "POST" && reviewThreadsMatch) {
      const body = JSON.parse(
        (await route.request().postData()) ?? "{}",
      ) as CreateReviewThreadsRequestRecord;
      reviewThreadsRequests.push(body);
      const now = new Date().toISOString();
      for (const draft of body.threads ?? []) {
        const comments: ReviewThreadCommentRecord[] = [
          {
            id: nextReviewCommentId++,
            author: "user",
            body: draft.body,
            created_at: now,
            sent_to_agent: false,
          },
          ...(draft.comments ?? []).map((c) => ({
            id: nextReviewCommentId++,
            author: c.author,
            body: c.body,
            created_at: now,
            sent_to_agent: false,
          })),
        ];
        createdReviewThreads.push({
          id: nextReviewThreadId++,
          path: draft.path,
          side: draft.side,
          line: draft.line,
          ...(draft.start_line != null ? { start_line: draft.start_line } : {}),
          commit_sha: draft.commit_sha,
          status: "open",
          hidden: false,
          writes_allowed: true,
          created_at: now,
          updated_at: now,
          comments,
        });
      }
      await fulfillJson(route, { threads: createdReviewThreads });
      return;
    }

    // AI threads for local worktree (empty list).
    const aiThreadsMatch = pathname.match(
      /^\/api\/v1\/repos\/local\/[^/]+\/pulls\/\d+\/ai-threads$/,
    );
    if (method === "GET" && aiThreadsMatch) {
      await fulfillJson(route, { threads: [] });
      return;
    }

    // AI sessions endpoint (used by aiStore.start).
    if (method === "GET" && pathname === "/api/v1/ai/sessions") {
      await fulfillJson(route, { sessions: [] });
      return;
    }

    // Worktree session — minimal idle stub so an act-immediately review
    // submit doesn't 404 if anything ever reads session state. Not
    // exercised by the doc-pane submit flow today (only
    // WorktreeConversation's mount calls loadSession, and the doc-review
    // tests never visit the Activity tab that renders it) — mocked
    // defensively so this path is covered regardless.
    const sessionMatch = pathname.match(
      /^\/api\/v1\/repos\/[^/]+\/[^/]+\/pulls\/\d+\/session$/,
    );
    if (method === "GET" && sessionMatch) {
      await fulfillJson(route, {
        session: { id: 0, status: "idle", started_at: "", last_activity_at: "" },
        turns: [],
      });
      return;
    }

    // Patchsets, notes, commits — used by ReviewSurface / DiffView for
    // GitHub-sourced PRs; return empty stubs so no proxy errors surface.
    const patchsetsMatch = pathname.match(
      /^\/api\/v1\/repos\/[^/]+\/[^/]+\/pulls\/\d+\/patchsets$/,
    );
    if (method === "GET" && patchsetsMatch) {
      await fulfillJson(route, { patchsets: [] });
      return;
    }
    const notesMatch = pathname.match(
      /^\/api\/v1\/repos\/[^/]+\/[^/]+\/pulls\/\d+\/notes$/,
    );
    if (method === "GET" && notesMatch) {
      await fulfillJson(route, { notes: [] });
      return;
    }
    const commitsMatch = pathname.match(
      /^\/api\/v1\/repos\/[^/]+\/[^/]+\/pulls\/\d+\/commits$/,
    );
    if (method === "GET" && commitsMatch) {
      await fulfillJson(route, { commits: [] });
      return;
    }

    await fulfillJson(route, { error: `Unhandled ${method} ${pathname}` }, 404);
  });
}
