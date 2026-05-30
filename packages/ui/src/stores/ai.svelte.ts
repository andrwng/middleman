import type { components } from "../api/generated/schema.js";

// Structural types from the generated OpenAPI schema.
export type AIThread = components["schemas"]["AiThreadResponse"];
export type AIQuestion = components["schemas"]["AiQuestionResponse"];

export interface AIStoreOptions {
  getBasePath?: () => string;
}

export function createAIStore(opts?: AIStoreOptions) {
  const getBasePath = opts?.getBasePath ?? (() => "/");

  // Active PR context. Polling and the store contents are keyed here.
  let owner = $state("");
  let name = $state("");
  let number = $state(0);

  let threads = $state<AIThread[]>([]);
  let questions = $state<AIQuestion[]>([]);
  let errorMsg = $state<string | null>(null);
  let pollHandle: ReturnType<typeof setInterval> | null = null;
  // Ids the user just deleted. If a refresh poll races ahead of
  // the server's list-filter (e.g. running against an older binary
  // that still returns closed threads), the card can bounce back
  // after a successful DELETE. Keep the id blacklisted for a short
  // window so the next refresh can't revive a dead row.
  const deletedThreadIds = new Set<number>();
  const deletedQuestionIds = new Set<number>();
  const TOMBSTONE_MS = 15_000;

  function prefix(): string {
    return `${getBasePath()}api/v1/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/pulls/${number}`;
  }

  // --- reads ---

  function getThreadsForFile(path: string): AIThread[] {
    return threads.filter((t) => t.path === path);
  }

  function getThreadsAtAnchor(path: string, line: number, side: "LEFT" | "RIGHT"): AIThread[] {
    return threads.filter(
      (t) => t.path === path && t.anchor_line === line && t.anchor_side === side,
    );
  }

  function getQuestionsForThread(threadID: number): AIQuestion[] {
    return questions
      .filter((q) => q.thread_id === threadID)
      .sort((a, b) => a.id - b.id);
  }

  function hasInFlightQuestions(): boolean {
    return questions.some((q) => q.status === "queued" || q.status === "running");
  }

  function all(): { threads: AIThread[]; questions: AIQuestion[] } {
    return { threads, questions };
  }

  // --- writes ---

  // markThreadDeletedExternally lets other stores (e.g. the global
  // aiSessions list, which closes threads from outside this PR's
  // diff view) notify us of a server-side deletion so we strip the
  // card immediately. Without this, the diff view shows a phantom
  // thread until the next periodic refresh — which only fires while
  // a question is in flight, so completed-and-idle threads stick
  // around indefinitely.
  function markThreadDeletedExternally(threadID: number): void {
    deletedThreadIds.add(threadID);
    setTimeout(() => deletedThreadIds.delete(threadID), TOMBSTONE_MS);
    threads = threads.filter((t) => t.id !== threadID);
    questions = questions.filter((q) => q.thread_id !== threadID);
  }

  async function refresh(): Promise<void> {
    if (!owner) return;
    try {
      const res = await fetch(`${prefix()}/ai-threads`);
      if (!res.ok) return;
      const data = (await res.json()) as {
        threads: AIThread[] | null;
        questions: AIQuestion[] | null;
      };
      const raw = data.threads ?? [];
      const rawQ = data.questions ?? [];
      // Filter out anything the user just deleted. Protects
      // against server versions that still surface closed rows.
      threads = raw.filter((t) => !deletedThreadIds.has(t.id));
      questions = rawQ.filter(
        (q) => !deletedQuestionIds.has(q.id) && !deletedThreadIds.has(q.thread_id),
      );
    } catch {
      /* swallow; next poll will retry */
    }
  }

  async function createThread(input: {
    path: string;
    anchor_side: "LEFT" | "RIGHT";
    anchor_line: number;
    hunk_start_line?: number;
    hunk_end_line?: number;
    hunk_text?: string;
    selection_text?: string;
    commit_sha: string;
    question: string;
    prompt_context?: string;
  }): Promise<
    | { ok: true; thread: AIThread; question: AIQuestion }
    | { ok: false; error: string }
  > {
    try {
      const res = await fetch(`${prefix()}/ai-threads`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(input),
      });
      if (!res.ok) {
        // Huma returns { detail: "..." } on structured errors; fall
        // back to raw status text for anything else.
        let msg = `${res.status} ${res.statusText}`;
        try {
          const data = (await res.json()) as { detail?: string };
          if (data?.detail) msg = data.detail;
        } catch {
          /* ignore */
        }
        return { ok: false, error: msg };
      }
      const data = (await res.json()) as { thread: AIThread; question: AIQuestion };
      threads = [...threads, data.thread];
      questions = [...questions, data.question];
      return { ok: true, thread: data.thread, question: data.question };
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }

  async function addFollowUp(
    threadID: number,
    question: string,
  ): Promise<AIQuestion | null> {
    try {
      const res = await fetch(`${prefix()}/ai-threads/${threadID}/questions`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ question }),
      });
      if (!res.ok) return null;
      const q = (await res.json()) as AIQuestion;
      questions = [...questions, q];
      return q;
    } catch {
      return null;
    }
  }

  async function deleteThread(threadID: number): Promise<boolean> {
    // Only strip locally when the server actually deleted the row.
    // Previously we always pruned in a `finally`, which masked
    // failures — the card disappeared but a refresh pulled it
    // back in from the server.
    try {
      const res = await fetch(`${prefix()}/ai-threads/${threadID}`, {
        method: "DELETE",
        // Server's CSRF guard rejects mutations without an
        // application/json content-type (even on zero-body DELETEs).
        headers: { "content-type": "application/json" },
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        errorMsg =
          (body as Record<string, string>).detail ??
          (body as Record<string, string>).title ??
          `Close thread failed: HTTP ${res.status}`;
        return false;
      }
      deletedThreadIds.add(threadID);
      setTimeout(() => deletedThreadIds.delete(threadID), TOMBSTONE_MS);
      threads = threads.filter((t) => t.id !== threadID);
      questions = questions.filter((q) => q.thread_id !== threadID);
      return true;
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
      return false;
    }
  }

  async function deleteQuestion(threadID: number, questionID: number): Promise<boolean> {
    try {
      const res = await fetch(
        `${prefix()}/ai-threads/${threadID}/questions/${questionID}`,
        {
          method: "DELETE",
          headers: { "content-type": "application/json" },
        },
      );
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        errorMsg =
          (body as Record<string, string>).detail ??
          (body as Record<string, string>).title ??
          `Cancel question failed: HTTP ${res.status}`;
        return false;
      }
      deletedQuestionIds.add(questionID);
      setTimeout(() => deletedQuestionIds.delete(questionID), TOMBSTONE_MS);
      questions = questions.filter((q) => q.id !== questionID);
      return true;
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
      return false;
    }
  }

  // --- lifecycle ---

  function start(o: string, n: string, num: number): void {
    const changed = o !== owner || n !== name || num !== number;
    owner = o;
    name = n;
    number = num;
    if (changed) {
      threads = [];
      questions = [];
    }
    void refresh();
    stopPolling();
    pollHandle = setInterval(() => {
      // Throttle polling when nothing is in-flight; still refresh at a
      // slower cadence to pick up foreign mutations (e.g. delete from
      // another tab), but don't hammer the server while the reviewer is
      // just reading.
      if (hasInFlightQuestions()) {
        void refresh();
      }
    }, 2000);
  }

  function stopPolling(): void {
    if (pollHandle !== null) {
      clearInterval(pollHandle);
      pollHandle = null;
    }
  }

  function stop(): void {
    stopPolling();
    owner = "";
    name = "";
    number = 0;
    threads = [];
    questions = [];
  }

  function getError(): string | null {
    return errorMsg;
  }
  function clearError(): void {
    errorMsg = null;
  }

  return {
    getThreadsForFile,
    getThreadsAtAnchor,
    getQuestionsForThread,
    hasInFlightQuestions,
    all,
    refresh,
    createThread,
    addFollowUp,
    deleteThread,
    deleteQuestion,
    markThreadDeletedExternally,
    getError,
    clearError,
    start,
    stop,
  };
}

export type AIStore = ReturnType<typeof createAIStore>;
