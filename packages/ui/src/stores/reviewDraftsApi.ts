// Thin fetch wrapper over the review-drafts routes. Raw fetch (not the
// generated client) to match how diff.svelte.ts talks to the notes
// routes, so the same test doubles work. Every mutating call sends a
// JSON body, which also satisfies the server's CSRF content-type check.

export interface ServerDraft {
  id: string;
  path: string;
  side: "LEFT" | "RIGHT";
  line: number;
  start_line?: number;
  commit_sha: string;
  in_reply_to?: number;
  body: string;
  origin: "user" | "claude";
  state: "suggestion" | "draft" | "discarded";
  submit_error?: string;
  created_at: string;
  updated_at: string;
}

export interface ServerDraftsResponse {
  drafts: ServerDraft[];
  meta: { body: string; event: string };
}

export interface DraftWrite {
  id: string;
  path: string;
  side: "LEFT" | "RIGHT";
  line: number;
  start_line?: number;
  commit_sha: string;
  in_reply_to?: number;
  body: string;
  created_at?: string;
}

export interface PRRef {
  owner: string;
  name: string;
  number: number;
}

export interface DraftSummaryItem {
  owner: string;
  name: string;
  number: number;
  count: number;
}

export class DraftApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

export function createReviewDraftsApi(getBasePath: () => string) {
  function prPrefix(pr: PRRef): string {
    return (
      `${getBasePath()}api/v1/repos/` +
      `${encodeURIComponent(pr.owner)}/` +
      `${encodeURIComponent(pr.name)}/` +
      `pulls/${pr.number}`
    );
  }

  async function call<T>(url: string, method: string, body?: unknown): Promise<T> {
    const init: RequestInit = { method };
    if (body !== undefined) {
      init.headers = { "content-type": "application/json" };
      init.body = JSON.stringify(body);
    }
    const response = await fetch(url, init);
    if (!response.ok) {
      const data = (await response.json().catch(() => ({}))) as Record<string, string>;
      throw new DraftApiError(data.detail ?? data.title ?? `HTTP ${response.status}`, response.status);
    }
    return (await response.json().catch(() => ({}))) as T;
  }

  return {
    list(pr: PRRef): Promise<ServerDraftsResponse> {
      return call(`${prPrefix(pr)}/review-drafts`, "GET");
    },
    create(pr: PRRef, drafts: DraftWrite[]): Promise<ServerDraftsResponse> {
      return call(`${prPrefix(pr)}/review-drafts`, "POST", { drafts });
    },
    async updateBody(pr: PRRef, id: string, body: string): Promise<void> {
      await call(`${prPrefix(pr)}/review-drafts/${encodeURIComponent(id)}`, "PATCH", { body });
    },
    async remove(pr: PRRef, ids: string[]): Promise<void> {
      if (ids.length === 0) return;
      await call(`${prPrefix(pr)}/review-drafts/delete`, "POST", { ids });
    },
    async putMeta(pr: PRRef, body: string, event: string): Promise<void> {
      await call(`${prPrefix(pr)}/review-draft-meta`, "PUT", { body, event });
    },
    publish(
      pr: PRRef,
      req: { event: string; body: string; commit_id?: string },
    ): Promise<{ review_id: number; state: string; published: number }> {
      return call(`${prPrefix(pr)}/review-drafts/publish`, "POST", req);
    },
    async summary(): Promise<DraftSummaryItem[]> {
      const data = await call<{ prs?: DraftSummaryItem[] }>(
        `${getBasePath()}api/v1/review-drafts/summary`,
        "GET",
      );
      return data.prs ?? [];
    },
  };
}

export type ReviewDraftsApi = ReturnType<typeof createReviewDraftsApi>;
