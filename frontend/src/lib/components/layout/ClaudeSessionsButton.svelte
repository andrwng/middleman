<script lang="ts">
  import { getStores } from "@middleman/ui";
  import { navigate } from "../../stores/router.svelte.ts";
  import { timeAgo } from "@middleman/ui/utils/time";

  const { aiSessions } = getStores();

  let open = $state(false);
  let busyId = $state<number | null>(null);

  const threads = $derived(aiSessions.getThreads());
  const briefs = $derived(aiSessions.getBriefs());
  const total = $derived(aiSessions.getTotalCount());
  const running = $derived(aiSessions.getRunningCount());
  const errorMsg = $derived(aiSessions.getError());

  function toggle(): void {
    open = !open;
    if (open) void aiSessions.load();
  }

  function handleOutsideClick(e: MouseEvent): void {
    if (!(e.target instanceof HTMLElement)) return;
    if (!e.target.closest(".claude-sessions-wrap")) open = false;
  }

  $effect(() => {
    if (!open) return;
    document.addEventListener("mousedown", handleOutsideClick);
    return () => document.removeEventListener("mousedown", handleOutsideClick);
  });

  function goToPR(
    owner: string,
    name: string,
    number: number,
    tab: "conversation" | "files" = "files",
  ): void {
    open = false;
    navigate(`/pulls/${owner}/${name}/${number}/${tab}`);
  }

  async function closeThread(t: {
    id: number;
    repoOwner: string;
    repoName: string;
    mrNumber: number;
  }): Promise<void> {
    if (busyId === t.id) return;
    busyId = t.id;
    try {
      await aiSessions.closeThread({
        id: t.id,
        repoOwner: t.repoOwner,
        repoName: t.repoName,
        mrNumber: t.mrNumber,
      } as never);
    } finally {
      busyId = null;
    }
  }

  async function cancelBrief(b: {
    id: number;
    repoOwner: string;
    repoName: string;
    mrNumber: number;
  }): Promise<void> {
    if (busyId === b.id) return;
    busyId = b.id;
    try {
      await aiSessions.cancelBrief({
        id: b.id,
        repoOwner: b.repoOwner,
        repoName: b.repoName,
        mrNumber: b.mrNumber,
      } as never);
    } finally {
      busyId = null;
    }
  }
</script>

<div class="claude-sessions-wrap">
  <button
    class="claude-btn"
    class:claude-btn--active={total > 0}
    class:claude-btn--running={running > 0}
    onclick={toggle}
    title={total === 0
      ? "No Claude sessions running"
      : `${total} Claude ${total === 1 ? "session" : "sessions"}${running > 0 ? ` (${running} active)` : ""}`}
  >
    <svg width="12" height="12" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
      <!-- Simple spark icon to hint at Claude without trademark infringement risk. -->
      <path d="M8 1.5l1.5 4.5L14 7.5l-4.5 1.5L8 13.5 6.5 9 2 7.5 6.5 6 8 1.5z" />
    </svg>
    <span class="claude-label">Claude</span>
    {#if total > 0}
      <span class="claude-badge" class:claude-badge--running={running > 0}>
        {total}
      </span>
    {/if}
  </button>

  {#if open}
    <div class="claude-popover" role="dialog" aria-label="Claude sessions">
      <div class="claude-popover__header">
        <span class="claude-popover__title">Claude sessions</span>
        {#if total > 0}
          <span class="claude-popover__sub">
            {#if briefs.length > 0}
              <span class="claude-popover__sub-chip claude-popover__sub-chip--brief">
                {briefs.length} {briefs.length === 1 ? "brief" : "briefs"}
              </span>
            {/if}
            {#if threads.length > 0}
              <span class="claude-popover__sub-chip claude-popover__sub-chip--ask">
                {threads.length} {threads.length === 1 ? "thread" : "threads"}
              </span>
            {/if}
            {#if running > 0}
              <span class="claude-popover__sub-running">· {running} running</span>
            {/if}
          </span>
        {/if}
      </div>

      {#if errorMsg}
        <div class="claude-popover__error">{errorMsg}</div>
      {/if}

      {#if total === 0}
        <div class="claude-popover__empty">
          No active Q&amp;A threads or brief runs. Anything you open
          stays alive until you close it from here.
        </div>
      {:else}
        {#if briefs.length > 0}
          <div class="claude-popover__section-head">
            Review briefs
            <span class="claude-popover__section-hint">auto-generated summaries</span>
          </div>
          {#each briefs as b (b.id)}
            <div class="claude-row claude-row--brief">
              <button
                class="claude-row__main"
                onclick={() => goToPR(b.repoOwner, b.repoName, b.mrNumber, "files")}
              >
                <div class="claude-row__title">
                  <span class="claude-row__type claude-row__type--brief">
                    <svg width="9" height="9" viewBox="0 0 12 12" fill="currentColor" aria-hidden="true">
                      <rect x="2" y="2" width="8" height="1.4" rx="0.4" />
                      <rect x="2" y="5.3" width="8" height="1.4" rx="0.4" />
                      <rect x="2" y="8.6" width="5" height="1.4" rx="0.4" />
                    </svg>
                    Brief
                  </span>
                  <span class="claude-row__repo">{b.repoOwner}/{b.repoName}#{b.mrNumber}</span>
                  <span class="claude-row__status claude-row__status--{b.status}">{b.status}</span>
                </div>
                <div class="claude-row__subtitle">
                  {b.mrTitle || ""}
                </div>
                <div class="claude-row__meta">
                  {b.depth} · started {timeAgo(b.startedAt || b.createdAt)}
                </div>
              </button>
              <button
                class="claude-row__action"
                title="Cancel brief"
                disabled={busyId === b.id}
                onclick={() => void cancelBrief(b)}
                aria-label="Cancel brief for {b.repoOwner}/{b.repoName}#{b.mrNumber}"
              >
                <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6">
                  <path d="M2 2L8 8M8 2L2 8" stroke-linecap="round" />
                </svg>
              </button>
            </div>
          {/each}
        {/if}

        {#if threads.length > 0}
          <div class="claude-popover__section-head">
            Q&amp;A threads
            <span class="claude-popover__section-hint">questions you asked</span>
          </div>
          {#each threads as t (t.id)}
            <div class="claude-row claude-row--ask">
              <button
                class="claude-row__main"
                onclick={() => goToPR(t.repoOwner, t.repoName, t.mrNumber, "files")}
              >
                <div class="claude-row__title">
                  <span class="claude-row__type claude-row__type--ask">
                    <svg width="9" height="9" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                      <path d="M4 4.5a2 2 0 1 1 2.4 1.95c-.4.1-.4.35-.4.55v.5" stroke-linecap="round" stroke-linejoin="round" />
                      <circle cx="6" cy="9.5" r="0.6" fill="currentColor" stroke="none" />
                    </svg>
                    Ask
                  </span>
                  <span class="claude-row__repo">{t.repoOwner}/{t.repoName}#{t.mrNumber}</span>
                  {#if t.openQuestionCount > 0}
                    <span class="claude-row__status claude-row__status--running">running</span>
                  {:else if t.latestQuestionStatus === "failed"}
                    <span class="claude-row__status claude-row__status--failed">failed</span>
                  {:else}
                    <span class="claude-row__status claude-row__status--idle">idle</span>
                  {/if}
                </div>
                <div class="claude-row__subtitle">
                  {t.mrTitle || ""}
                </div>
                <div class="claude-row__meta">
                  {t.path}:{t.anchorLine} · opened {timeAgo(t.createdAt)}
                </div>
              </button>
              <button
                class="claude-row__action"
                title="Close thread (terminates any in-flight question and removes the worktree)"
                disabled={busyId === t.id}
                onclick={() => void closeThread(t)}
                aria-label="Close thread for {t.repoOwner}/{t.repoName}#{t.mrNumber}"
              >
                <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6">
                  <path d="M2 2L8 8M8 2L2 8" stroke-linecap="round" />
                </svg>
              </button>
            </div>
          {/each}
        {/if}
      {/if}
    </div>
  {/if}
</div>

<style>
  .claude-sessions-wrap {
    position: relative;
    display: inline-flex;
  }

  .claude-btn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 4px 10px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-muted);
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
  }

  .claude-btn:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .claude-btn--active {
    color: var(--accent-claude);
    border-color: color-mix(in srgb, var(--accent-claude) 50%, var(--border-default));
  }

  .claude-btn--running {
    background: color-mix(in srgb, var(--accent-claude) 12%, var(--bg-surface));
  }

  .claude-label {
    font-weight: 600;
  }

  .claude-badge {
    font-family: var(--font-mono);
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--bg-inset);
    color: var(--text-secondary);
    border: 1px solid var(--border-muted);
  }

  .claude-badge--running {
    background: var(--accent-claude);
    color: #fff;
    border-color: var(--accent-claude);
  }

  .claude-popover {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    z-index: 50;
    width: min(420px, calc(100vw - 32px));
    max-height: 60vh;
    overflow-y: auto;
    background: var(--bg-surface);
    border: 1px solid var(--border-default);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-md);
    padding: 6px 0;
  }

  .claude-popover__header {
    padding: 6px 12px 4px;
    display: flex;
    flex-direction: column;
    gap: 2px;
    border-bottom: 1px solid var(--border-muted);
    margin-bottom: 4px;
  }

  .claude-popover__title {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
  }

  .claude-popover__sub {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
    font-size: 10px;
    color: var(--text-muted);
  }

  .claude-popover__sub-chip {
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 999px;
    font-weight: 600;
    font-size: 10px;
  }

  .claude-popover__sub-chip--brief {
    background: var(--accent-claude);
    color: #fff;
  }

  .claude-popover__sub-chip--ask {
    border: 1px solid var(--accent-claude);
    color: var(--accent-claude);
    background: transparent;
  }

  .claude-popover__sub-running {
    color: var(--text-muted);
  }

  .claude-popover__section-head {
    display: flex;
    align-items: baseline;
    gap: 6px;
    padding: 6px 12px 2px;
    font-size: 10px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    font-weight: 600;
  }

  .claude-popover__section-hint {
    font-size: 9px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: none;
    letter-spacing: 0;
  }

  .claude-popover__empty {
    padding: 8px 12px 12px;
    font-size: 12px;
    color: var(--text-muted);
    line-height: 1.5;
  }

  .claude-popover__error {
    padding: 6px 12px;
    font-size: 11px;
    color: var(--accent-red);
  }

  .claude-row {
    display: flex;
    align-items: stretch;
    gap: 4px;
    padding: 0 4px 0 8px;
    border-left: 3px solid transparent;
  }

  .claude-row--brief {
    border-left-color: var(--accent-claude);
  }

  .claude-row--ask {
    border-left-color: color-mix(in srgb, var(--accent-claude) 50%, transparent);
  }

  .claude-row:hover {
    background: var(--bg-surface-hover);
  }

  .claude-row__type {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 1px 6px;
    border-radius: 999px;
    flex-shrink: 0;
  }

  .claude-row__type--brief {
    background: var(--accent-claude);
    color: #fff;
  }

  .claude-row__type--ask {
    border: 1px solid var(--accent-claude);
    color: var(--accent-claude);
    background: transparent;
  }

  .claude-row__main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 6px 4px 6px 0;
    text-align: left;
    background: none;
    border: none;
    cursor: pointer;
    color: inherit;
  }

  .claude-row__title {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .claude-row__repo {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-primary);
  }

  .claude-row__status {
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 1px 5px;
    border-radius: 999px;
  }

  .claude-row__status--running,
  .claude-row__status--queued {
    background: var(--accent-claude);
    color: #fff;
  }

  .claude-row__status--idle {
    background: var(--bg-inset);
    color: var(--text-muted);
  }

  .claude-row__status--failed {
    background: color-mix(in srgb, var(--accent-red) 16%, var(--bg-inset));
    color: var(--accent-red);
  }

  .claude-row__status--done {
    background: color-mix(in srgb, var(--accent-green) 16%, var(--bg-inset));
    color: var(--accent-green);
  }

  .claude-row__subtitle {
    font-size: 12px;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .claude-row__meta {
    font-size: 10px;
    color: var(--text-muted);
    font-family: var(--font-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .claude-row__action {
    flex-shrink: 0;
    width: 26px;
    border: none;
    background: none;
    color: var(--text-muted);
    cursor: pointer;
    border-radius: var(--radius-sm);
  }

  .claude-row__action:hover:not(:disabled) {
    background: var(--bg-surface-hover);
    color: var(--accent-red);
  }

  .claude-row__action:disabled {
    opacity: 0.4;
    cursor: default;
  }
</style>
