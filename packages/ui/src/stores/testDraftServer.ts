// Shared in-memory fake of the review-drafts routes for store tests.
import { vi } from "vitest";
import type { MiddlemanClient } from "../types.js";

export function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

export interface Call { url: string; method: string; body: unknown }

function gate(): { wait: Promise<void>; release: () => void } {
  let release!: () => void;
  const wait = new Promise<void>((r) => { release = r; });
  return { wait, release };
}

// In-memory fake of the review-drafts routes. Gates let a test hold a
// create or a list request to exercise ordering.
export function installDraftServer() {
  const calls: Call[] = [];
  const drafts = new Map<string, Record<string, unknown>[]>();
  const metas = new Map<string, { body: string; event: string }>();
  let createGate: Promise<void> | null = null;
  const listGates = new Map<string, Promise<void>>();
  // Per-PR forced status for every drafts request (404 = PR gone, 500 = flaky).
  const keyStatus = new Map<string, number>();
  let createFailures = 0;
  let publishCalls = 0;
  const failingActions = new Set<string>();
  globalThis.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(init.body as string) : undefined;
    calls.push({ url, method, body });
    const json = (b: unknown, status = 200) =>
      new Response(JSON.stringify(b), { status, headers: { "Content-Type": "application/json" } });
    if (url.endsWith("/review-drafts/summary")) return json({ prs: [] });
    const m = url.match(/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)\/(review-drafts|review-draft-meta)(\/.*)?$/);
    if (!m) return json({}, 404);
    const key = `${decodeURIComponent(m[1]!)}/${decodeURIComponent(m[2]!)}#${m[3]}`;
    const list = drafts.get(key) ?? [];
    const forced = keyStatus.get(key);
    if (forced) return json({ detail: `forced ${forced}` }, forced);
    if (m[4] === "review-draft-meta") {
      metas.set(key, body as { body: string; event: string });
      return json(body);
    }
    if (!m[5] && method === "GET") {
      // Snapshot before the gate: a real server reads the rows when the
      // request arrives, not when the response finally reaches the tab.
      // Discarded rows are never listed, like the real route.
      const live = (drafts.get(key) ?? []).filter((d) => d.state !== "discarded").map((d) => ({ ...d }));
      const snapshot = { drafts: live, meta: metas.get(key) ?? { body: "", event: "" } };
      const g = listGates.get(key);
      if (g) await g;
      return json(snapshot);
    }
    const action = m[5]?.match(/^\/([^/]+)\/(accept|discard|restore)$/);
    if (action && method === "POST") {
      const id = decodeURIComponent(action[1]!);
      const verb = action[2]!;
      if (failingActions.has(verb)) return json({ detail: `${verb} failed` }, 500);
      const d = list.find((x) => x.id === id);
      if (!d) return json({ detail: "draft not found" }, 404);
      const state = d.state as string;
      if (verb === "accept" && state === "suggestion") d.state = "draft";
      else if (verb === "discard" && state !== "discarded") {
        d.discarded_from = state;
        d.state = "discarded";
      } else if (verb === "restore" && state === "discarded") d.state = (d.discarded_from as string) || "suggestion";
      else return json({ detail: "invalid transition" }, 409);
      return json(d);
    }
    if (m[5] === "/publish") {
      publishCalls++;
      const n = list.length;
      drafts.set(key, []);
      return json({ review_id: 1, state: "COMMENTED", published: n });
    }
    if (!m[5] && method === "POST") {
      if (createGate) await createGate;
      if (createFailures > 0) {
        createFailures--;
        return json({ detail: "database is locked" }, 500);
      }
      const incoming = (body as { drafts: Record<string, unknown>[] }).drafts.map(
        (d): Record<string, unknown> => ({ ...d, origin: "user", state: "draft" }),
      );
      const next = [...(drafts.get(key) ?? [])];
      for (const d of incoming) if (!next.some((x) => x.id === d.id)) next.push(d);
      drafts.set(key, next);
      return json({ drafts: next, meta: { body: "", event: "" } });
    }
    if (m[5] && m[5] !== "/delete" && method === "PATCH") {
      const id = decodeURIComponent(m[5].slice(1));
      const d = list.find((x) => x.id === id);
      if (!d) return json({ detail: "draft not found" }, 404);
      d.body = (body as { body: string }).body;
      return json(d);
    }
    if (m[5] === "/delete") {
      const ids = (body as { ids: string[] }).ids;
      drafts.set(key, list.filter((d) => !ids.includes(d.id as string)));
      return json({ deleted: ids.length });
    }
    return json({});
  }) as unknown as typeof fetch;
  return {
    calls,
    drafts,
    metas,
    keyStatus,
    failCreates(n: number): void { createFailures = n; },
    failAction(name: "accept" | "discard" | "restore"): void { failingActions.add(name); },
    publishCount: (): number => publishCalls,
    holdCreates(): () => void {
      const g = gate();
      createGate = g.wait;
      return () => { createGate = null; g.release(); };
    },
    holdList(key: string): () => void {
      const g = gate();
      listGates.set(key, g.wait);
      return () => { listGates.delete(key); g.release(); };
    },
  };
}
