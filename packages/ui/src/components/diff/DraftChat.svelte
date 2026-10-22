<script lang="ts">
  import { getStores } from "../../context.js";
  import { renderMarkdown } from "../../utils/markdown.js";
  import type { ServerDraftMessage } from "../../stores/reviewDraftsApi.js";
  import SessionToolTimeline from "../detail/SessionToolTimeline.svelte";

  interface Props {
    draftId: string;
    onclose?: () => void;
  }

  const { draftId, onclose }: Props = $props();
  const { draftChat, diff: diffStore } = getStores();

  // #123/@user links only make sense for GitHub repos.
  const repoCtx = $derived.by(() => {
    const pr = diffStore.getCurrentPR();
    return pr && pr.owner !== "local" ? { owner: pr.owner, name: pr.name } : undefined;
  });

  let text = $state("");
  let activityOpen = $state<Record<number, boolean>>({});

  const messages = $derived(draftChat.getMessages(draftId));
  const error = $derived(draftChat.getError(draftId));

  // The store owns whether this chat is open (cards may be re-rendered
  // under it); closing goes through the store too.
  function close(): void {
    draftChat.close(draftId);
    onclose?.();
  }

  async function submit(): Promise<void> {
    const body = text.trim();
    if (!body) return;
    if (await draftChat.send(draftId, body)) text = "";
  }

  // The reviewer message a failed reply was answering, so "Start fresh
  // chat" can ask it again in a new session.
  function questionBefore(index: number): string {
    for (let i = index - 1; i >= 0; i--) {
      const m = messages[i]!;
      if (m.author === "user") return m.body;
    }
    return "";
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      close();
      return;
    }
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      void submit();
    }
  }

  function pendingLabel(m: ServerDraftMessage): string {
    return m.status === "queued" ? "Waiting for a free slot…" : "Thinking…";
  }
</script>

<div class="draft-chat">
  {#each messages as m, i (m.id)}
    {#if m.kind === "revision"}
      <div class="draft-chat__revision" class:draft-chat__revision--pending={!m.applied}>
        <div class="draft-chat__revision-head">
          <span class="draft-chat__badge">Claude</span>
          <span>{m.applied ? "Revised the comment" : "Suggests this wording"}</span>
          {#if m.applied}
            <button type="button" class="draft-chat__btn" onclick={() => void draftChat.revert(draftId, m.id)}>Revert</button>
          {:else}
            <button type="button" class="draft-chat__btn draft-chat__btn--primary" onclick={() => void draftChat.apply(draftId, m.id)}>Apply</button>
          {/if}
        </div>
        <div class="draft-chat__diff">
          <p class="draft-chat__old">{m.prev_body}</p>
          <p class="draft-chat__new">{m.body}</p>
        </div>
      </div>
    {:else if m.author === "user"}
      <div class="draft-chat__msg draft-chat__msg--user">
        <span class="draft-chat__who">You</span>
        <p class="draft-chat__text">{m.body}</p>
      </div>
    {:else}
      <div class="draft-chat__msg">
        <span class="draft-chat__badge">Claude</span>
        {#if m.status === "queued" || m.status === "running"}
          <span class="draft-chat__pending">{pendingLabel(m)}</span>
          <button type="button" class="draft-chat__btn" onclick={() => void draftChat.cancel(draftId, m.id)}>Cancel</button>
        {:else if m.status === "failed"}
          <p class="draft-chat__error" role="alert">{m.error}</p>
          {#if questionBefore(i)}
            <button
              type="button"
              class="draft-chat__btn"
              onclick={() => void draftChat.send(draftId, questionBefore(i), true)}
            >Start fresh chat</button>
          {/if}
        {:else if m.status === "cancelled"}
          <span class="draft-chat__pending">Cancelled</span>
        {:else}
          <div class="draft-chat__text markdown-body">{@html renderMarkdown(m.body, repoCtx)}</div>
        {/if}
        {#if m.raw_json}
          <button
            type="button"
            class="draft-chat__link"
            onclick={() => (activityOpen = { ...activityOpen, [m.id]: !activityOpen[m.id] })}
          >{activityOpen[m.id] ? "Hide activity" : "Activity"}</button>
          {#if activityOpen[m.id]}
            <SessionToolTimeline rawJSON={m.raw_json} />
          {/if}
        {/if}
      </div>
    {/if}
  {/each}

  {#if error}
    <p class="draft-chat__error" role="alert">{error}</p>
  {/if}

  <textarea
    class="draft-chat__input"
    rows="2"
    placeholder="Ask about this comment, or ask Claude to change it"
    bind:value={text}
    onkeydown={onKeydown}
    disabled={draftChat.isSending(draftId)}
  ></textarea>
  <div class="draft-chat__actions">
    <span class="draft-chat__hint">Ctrl-Enter to send · Esc to close</span>
    <button
      type="button"
      class="draft-chat__btn draft-chat__btn--primary"
      disabled={draftChat.isSending(draftId) || text.trim() === ""}
      onclick={() => void submit()}
    >Send</button>
  </div>
</div>

<style>
  .draft-chat {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--border-muted);
    max-height: 420px;
    overflow-y: auto;
    font-size: 12px;
  }

  .draft-chat__msg {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 6px;
  }

  .draft-chat__msg--user {
    justify-content: flex-end;
  }

  .draft-chat__who {
    font-weight: 600;
    color: var(--text-muted);
  }

  .draft-chat__badge {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-claude);
    color: #fff;
  }

  .draft-chat__text {
    flex-basis: 100%;
    margin: 0;
    color: var(--text-primary);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .draft-chat__msg--user .draft-chat__text {
    text-align: right;
  }

  .draft-chat__pending {
    color: var(--accent-claude);
  }

  .draft-chat__error {
    flex-basis: 100%;
    margin: 0;
    color: var(--accent-red);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .draft-chat__revision {
    padding: 6px 8px;
    border: 1px solid var(--accent-claude);
    border-radius: var(--radius-sm);
  }

  .draft-chat__revision--pending {
    border-style: dashed;
  }

  .draft-chat__revision-head {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .draft-chat__revision-head .draft-chat__btn {
    margin-left: auto;
  }

  .draft-chat__diff {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-top: 4px;
  }

  .draft-chat__old,
  .draft-chat__new {
    margin: 0;
    padding: 4px 6px;
    border-radius: var(--radius-sm);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .draft-chat__old {
    background: color-mix(in srgb, var(--accent-red) 8%, var(--bg-inset));
    color: var(--text-muted);
    text-decoration: line-through;
  }

  .draft-chat__new {
    background: color-mix(in srgb, var(--accent-green) 8%, var(--bg-inset));
    color: var(--text-primary);
  }

  .draft-chat__input {
    width: 100%;
    box-sizing: border-box;
    font: inherit;
    font-size: 12px;
    padding: 6px 8px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-primary);
    resize: vertical;
  }

  .draft-chat__actions {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .draft-chat__hint {
    margin-right: auto;
    font-size: 11px;
    color: var(--text-muted);
  }

  .draft-chat__btn {
    font-size: 11px;
    padding: 2px 8px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }

  .draft-chat__btn--primary {
    border-color: var(--accent-claude);
    background: var(--accent-claude);
    color: #fff;
  }

  .draft-chat__btn:disabled {
    opacity: 0.6;
    cursor: default;
  }

  .draft-chat__link {
    padding: 0;
    border: none;
    background: none;
    color: var(--accent-blue);
    font-size: 11px;
    cursor: pointer;
  }
</style>
