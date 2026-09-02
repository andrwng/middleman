<script lang="ts">
  import { tick } from "svelte";
  import { getStores } from "../../context.js";
  import type { AIThread, AIQuestion } from "../../stores/ai.svelte.js";
  import SectionResizeHandle from "./SectionResizeHandle.svelte";
  import { getSectionHeight } from "./sectionHeights.svelte.js";

  const { ai: aiStore, diff: diffStore } = getStores();
  let bodyEl: HTMLDivElement | undefined = $state();

  // Reader-chosen height for this section's body. null leaves the
  // stylesheet's default cap in charge -- see sectionHeights for why
  // the cap is a max-height rather than a height.
  const bodyMax = $derived.by(() => {
    const h = getSectionHeight("questions");
    return h === null ? null : `${h}px`;
  });

  const data = $derived(aiStore.all());
  const threads = $derived(data.threads);
  const hasInFlight = $derived(aiStore.hasInFlightQuestions());

  // Collapsed by default so the sidebar stays quiet on PRs without
  // any threads. Auto-expands the first time a thread appears OR
  // whenever a question goes in-flight so the reviewer can watch
  // progress without opening the panel.
  let expanded = $state(false);
  let userCollapsed = $state(false);

  $effect(() => {
    if (hasInFlight && !userCollapsed) {
      expanded = true;
    }
  });

  function toggle(): void {
    expanded = !expanded;
    userCollapsed = !expanded;
  }

  function latestQuestion(threadID: number): AIQuestion | undefined {
    const qs = aiStore.getQuestionsForThread(threadID);
    return qs[qs.length - 1];
  }

  function threadStatus(thread: AIThread): "running" | "queued" | "failed" | "done" | "closed" {
    if (thread.status === "closed") return "closed";
    const qs = aiStore.getQuestionsForThread(thread.id);
    const inflight = qs.find((q) => q.status === "running" || q.status === "queued");
    if (inflight) return inflight.status as "running" | "queued";
    const anyFailed = qs.some((q) => q.status === "failed");
    if (anyFailed) return "failed";
    return "done";
  }

  function anchorLabel(thread: AIThread): string {
    const side = thread.anchor_side === "LEFT" ? "−" : "+";
    if (
      thread.hunk_start_line != null &&
      thread.hunk_end_line != null &&
      thread.hunk_start_line !== thread.hunk_end_line
    ) {
      return `${side}${thread.hunk_start_line}–${thread.hunk_end_line}`;
    }
    return `${side}${thread.anchor_line}`;
  }

  async function scrollToThread(thread: AIThread): Promise<void> {
    // Switch diff scope to the commit the question was anchored on
    // (if not already there) so line numbers resolve. Subtle bit:
    // when the question was asked in HEAD scope, thread.commit_sha
    // is the PR head's SHA — but switching to *commit* scope at
    // that SHA shows head^..head, not base..head, and the anchor's
    // line numbers won't match. So if the SHA matches the current
    // head, route back to HEAD scope; otherwise use commit scope.
    if (thread.commit_sha) {
      const scope = diffStore.getScope();
      const commits = diffStore.getCommits();
      const isCurrentHead = commits && commits.length > 0 && commits[0]!.sha === thread.commit_sha;
      const alreadyHeadHere = isCurrentHead && scope.kind === "head";
      const alreadyCommitHere = scope.kind === "commit" && scope.sha === thread.commit_sha;
      if (!alreadyHeadHere && !alreadyCommitHere) {
        if (isCurrentHead) {
          await diffStore.resetToHead();
        } else {
          await diffStore.selectCommit(thread.commit_sha);
        }
        // selectCommit/resetToHead awaits the diff fetch but DOM
        // hasn't necessarily committed yet — let the new files
        // render before querying.
        await tick();
      }
    }

    // If the file is collapsed, the .line-wrap nodes aren't in the
    // DOM and the selector would silently find nothing. Expand it
    // first, then await a render tick so the lines mount before we
    // try to locate the anchor.
    const pr = diffStore.getCurrentPR();
    if (
      pr &&
      diffStore.isFileCollapsed(pr.owner, pr.name, pr.number, thread.path)
    ) {
      diffStore.toggleFileCollapsed(pr.owner, pr.name, pr.number, thread.path);
      await tick();
    }

    const selector =
      `.diff-file[data-file-path="${CSS.escape(thread.path)}"] ` +
      `.line-wrap[data-anchor-line="${thread.anchor_line}"]` +
      `[data-anchor-side="${thread.anchor_side}"]`;
    const el = document.querySelector<HTMLElement>(selector);
    if (el) {
      el.scrollIntoView({ block: "center", behavior: "smooth" });
      // Brief highlight so the user's eye lands on the right line —
      // when several threads sit close together a smooth scroll
      // alone makes it ambiguous which one you jumped to.
      el.classList.add("line-wrap--flash");
      window.setTimeout(() => el.classList.remove("line-wrap--flash"), 1500);
      return;
    }

    // Fall back to scrolling the file header into view — the
    // anchored line might live inside an unexpanded context region
    // (CollapsedRegion) or the diff scope might have changed since
    // the thread was created (line numbers no longer resolve).
    const fileEl = document.querySelector<HTMLElement>(
      `.diff-file[data-file-path="${CSS.escape(thread.path)}"]`,
    );
    if (fileEl) {
      fileEl.scrollIntoView({ block: "start", behavior: "smooth" });
    }
  }

  async function cancel(thread: AIThread, q: AIQuestion): Promise<void> {
    const ok = await aiStore.deleteQuestion(thread.id, q.id);
    if (!ok) {
      alert(aiStore.getError() ?? "Cancel failed");
    }
  }

  async function closeThread(thread: AIThread): Promise<void> {
    const ok = await aiStore.deleteThread(thread.id);
    if (!ok) {
      alert(aiStore.getError() ?? "Close failed");
    }
  }

  function truncate(text: string, n: number): string {
    if (text.length <= n) return text;
    return text.slice(0, n).trimEnd() + "…";
  }
</script>

{#if threads.length > 0}
  <div class="q-section">
    <div class="q-section__header">
      <button class="q-section__toggle" onclick={toggle}>
        <span class="q-section__chevron" class:q-section__chevron--open={expanded}>&#8250;</span>
        <span class="q-section__label">Questions</span>
        <span class="q-section__count">{threads.length}</span>
        {#if hasInFlight}
          <span class="q-section__live" title="Claude is currently running">
            <span class="q-section__live-dot"></span>
            live
          </span>
        {/if}
      </button>
    </div>

    {#if expanded}
      <div class="q-section__body" bind:this={bodyEl} style:max-height={bodyMax}>
        {#each threads as thread (thread.id)}
          {@const status = threadStatus(thread)}
          {@const latest = latestQuestion(thread.id)}
          <div class="q-item" class:q-item--closed={thread.status === "closed"}>
            <button
              type="button"
              class="q-item__main"
              onclick={() => scrollToThread(thread)}
              title="Scroll to this thread in the diff"
            >
              <span class="q-item__status q-item__status--{status}">
                <span class="q-item__status-dot"></span>
                {status}
              </span>
              <span class="q-item__location">
                {thread.path}
                <span class="q-item__anchor">{anchorLabel(thread)}</span>
              </span>
              {#if latest}
                <span class="q-item__preview">{truncate(latest.question, 80)}</span>
              {/if}
            </button>
            <div class="q-item__actions">
              {#if latest && (latest.status === "running" || latest.status === "queued")}
                <button
                  type="button"
                  class="q-item__action"
                  title="Cancel the in-flight question"
                  onclick={() => void cancel(thread, latest)}
                >
                  Cancel
                </button>
              {/if}
              {#if thread.status === "active"}
                <button
                  type="button"
                  class="q-item__action q-item__action--danger"
                  title="Close and remove this thread"
                  onclick={() => void closeThread(thread)}
                >
                  Close
                </button>
              {/if}
            </div>
          </div>
        {/each}
      </div>
      <SectionResizeHandle id="questions" body={bodyEl} label="Resize Questions" />
    {/if}
  </div>
{/if}

<style>
  .q-section {
    background: var(--bg-inset);
    border-bottom: 1px solid var(--diff-border);
  }

  .q-section__header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 10px 2px 0;
  }

  .q-section__toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    padding: 4px 6px 4px 10px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: var(--text-primary);
    border-radius: var(--radius-sm);
  }

  .q-section__toggle:hover {
    background: var(--bg-surface-hover);
  }

  .q-section__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    width: 12px;
    height: 12px;
    color: var(--text-muted);
    transition: transform 0.15s;
  }

  .q-section__chevron--open {
    transform: rotate(90deg);
  }

  .q-section__label {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
  }

  .q-section__count {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    background: var(--diff-bg);
    border: 1px solid var(--diff-border);
    border-radius: 999px;
    padding: 1px 6px;
  }

  .q-section__live {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-left: auto;
    font-size: 10px;
    color: var(--accent-amber);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .q-section__live-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent-amber);
    animation: q-pulse 1.2s ease-in-out infinite;
  }

  @keyframes q-pulse {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 1; }
  }

  .q-section__body {
    padding: 2px 0 4px;
    max-height: 40vh;
    overflow-y: auto;
  }

  .q-item {
    display: flex;
    align-items: stretch;
    padding: 4px 10px 4px 12px;
    gap: 4px;
  }

  .q-item:hover {
    background: var(--bg-surface-hover);
  }

  .q-item--closed {
    opacity: 0.55;
  }

  .q-item__main {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    padding: 0;
    color: inherit;
  }

  .q-item__status {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 999px;
    text-transform: uppercase;
    font-weight: 600;
    flex-shrink: 0;
  }

  .q-item__status-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
  }

  .q-item__status--queued,
  .q-item__status--running {
    color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 14%, transparent);
  }

  .q-item__status--running .q-item__status-dot {
    animation: q-pulse 1.2s ease-in-out infinite;
  }

  .q-item__status--done {
    color: var(--accent-green);
    background: color-mix(in srgb, var(--accent-green) 14%, transparent);
  }

  .q-item__status--failed {
    color: var(--accent-red);
    background: color-mix(in srgb, var(--accent-red) 14%, transparent);
  }

  .q-item__status--closed {
    color: var(--text-muted);
    background: color-mix(in srgb, var(--text-muted) 14%, transparent);
  }

  .q-item__location {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 0 1 auto;
    min-width: 0;
  }

  .q-item__anchor {
    color: var(--text-muted);
    margin-left: 3px;
  }

  .q-item__preview {
    font-size: 11px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1 1 auto;
    min-width: 0;
  }

  .q-item__actions {
    display: inline-flex;
    gap: 4px;
    flex-shrink: 0;
  }

  .q-item__action {
    font-size: 10px;
    padding: 1px 6px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--diff-bg);
    color: var(--text-muted);
    cursor: pointer;
  }

  .q-item__action:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .q-item__action--danger:hover {
    color: var(--accent-red);
    border-color: var(--accent-red);
  }
</style>
