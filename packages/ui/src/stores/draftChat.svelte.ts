import { createReviewDraftsApi, type PRRef, type ServerDraftMessage } from "./reviewDraftsApi.js";

export interface DraftChatStoreOptions {
  getBasePath?: () => string;
  // Called when Claude revised a comment or the reviewer applied or
  // reverted a revision, so the caller can reload the draft's body.
  onRevision?: () => void;
}

const POLL_MS = 1500;

interface Chat {
  pr: PRRef;
  messages: ServerDraftMessage[];
  error: string | null;
  sending: boolean;
  // false after the reviewer closed the chat while Claude was still
  // replying: the store keeps watching so a revision still refreshes the
  // card, then forgets the chat.
  visible: boolean;
}

// createDraftChatStore holds the open conversations with Claude about
// individual drafts. It owns which chats are open, so a card that is
// re-rendered keeps its chat. Each chat polls only while a Claude reply
// is queued or running.
export function createDraftChatStore(opts?: DraftChatStoreOptions) {
  const api = createReviewDraftsApi(opts?.getBasePath ?? (() => "/"));
  let chats = $state<Record<string, Chat>>({});
  const timers = new Map<string, ReturnType<typeof setInterval>>();
  const seenRevisions = new Map<string, Set<number>>();

  function update(id: string, fn: (c: Chat) => Chat): void {
    const cur = chats[id];
    if (cur) chats = { ...chats, [id]: fn(cur) };
  }

  function busy(messages: ServerDraftMessage[]): boolean {
    return messages.some((m) => m.author === "claude" && (m.status === "queued" || m.status === "running"));
  }

  // absorb stores a fresh message list and reports revisions not seen
  // before. The first load only records what exists.
  function absorb(id: string, messages: ServerDraftMessage[], initial: boolean): void {
    if (!chats[id]) return; // closed and forgotten while the request was in flight
    const seen = seenRevisions.get(id) ?? new Set<number>();
    let fresh = false;
    for (const m of messages) {
      if (m.kind === "revision" && !seen.has(m.id)) {
        seen.add(m.id);
        if (!initial) fresh = true;
      }
    }
    seenRevisions.set(id, seen);
    update(id, (c) => ({ ...c, messages }));
    if (fresh) opts?.onRevision?.();
    if (busy(messages)) arm(id);
    else {
      disarm(id);
      if (!chats[id]?.visible) forget(id);
    }
  }

  async function refresh(id: string, initial = false): Promise<void> {
    const c = chats[id];
    if (!c) return;
    try {
      absorb(id, await api.listMessages(c.pr, id), initial);
    } catch (err) {
      update(id, (cur) => ({ ...cur, error: err instanceof Error ? err.message : String(err) }));
    }
  }

  function arm(id: string): void {
    if (timers.has(id)) return;
    timers.set(id, setInterval(() => void refresh(id), POLL_MS));
  }

  function disarm(id: string): void {
    const t = timers.get(id);
    if (t !== undefined) clearInterval(t);
    timers.delete(id);
  }

  function open(pr: PRRef, id: string): void {
    const existing = chats[id];
    if (existing) {
      update(id, (c) => ({ ...c, visible: true }));
    } else {
      chats = { ...chats, [id]: { pr, messages: [], error: null, sending: false, visible: true } };
    }
    void refresh(id, !existing);
  }

  function forget(id: string): void {
    disarm(id);
    seenRevisions.delete(id);
    const next = { ...chats };
    delete next[id];
    chats = next;
  }

  function close(id: string): void {
    const c = chats[id];
    if (!c) return;
    if (busy(c.messages)) update(id, (cur) => ({ ...cur, visible: false }));
    else forget(id);
  }

  async function send(id: string, body: string, fresh = false): Promise<boolean> {
    const c = chats[id];
    if (!c || c.sending || body.trim() === "") return false;
    update(id, (cur) => ({ ...cur, sending: true, error: null }));
    try {
      absorb(id, await api.sendMessage(c.pr, id, body.trim(), fresh), false);
      return true;
    } catch (err) {
      update(id, (cur) => ({ ...cur, error: err instanceof Error ? err.message : String(err) }));
      return false;
    } finally {
      update(id, (cur) => ({ ...cur, sending: false }));
    }
  }

  async function cancel(id: string, messageId: number): Promise<void> {
    const c = chats[id];
    if (!c) return;
    try {
      await api.cancelMessage(c.pr, id, messageId);
    } finally {
      await refresh(id);
    }
  }

  async function revision(id: string, messageId: number, apply: boolean): Promise<void> {
    const c = chats[id];
    if (!c) return;
    try {
      if (apply) await api.applyRevision(c.pr, id, messageId);
      else await api.revertRevision(c.pr, id, messageId);
      opts?.onRevision?.();
    } catch (err) {
      update(id, (cur) => ({ ...cur, error: err instanceof Error ? err.message : String(err) }));
    }
    await refresh(id);
  }

  return {
    open,
    close,
    isOpen: (id: string): boolean => chats[id]?.visible ?? false,
    getMessages: (id: string): ServerDraftMessage[] => chats[id]?.messages ?? [],
    isBusy: (id: string): boolean => busy(chats[id]?.messages ?? []),
    isSending: (id: string): boolean => chats[id]?.sending ?? false,
    getError: (id: string): string | null => chats[id]?.error ?? null,
    send,
    cancel,
    apply: (id: string, messageId: number) => revision(id, messageId, true),
    revert: (id: string, messageId: number) => revision(id, messageId, false),
  };
}

export type DraftChatStore = ReturnType<typeof createDraftChatStore>;
