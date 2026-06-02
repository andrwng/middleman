<script lang="ts">
  import { tick } from "svelte";
  import { getClient } from "../../context.js";
  import { getStores } from "../../context.js";
  import type { ReviewEvent, DraftComment } from "../../stores/diff.svelte.js";

  interface Props {
    owner: string;
    name: string;
    number: number;
    onclose: () => void;
  }

  const { owner, name, number, onclose }: Props = $props();

  const { diff: diffStore, pulls: pullsStore, worktreeSession } = getStores();
  const client = getClient();

  // For local worktrees the submit verb sends review feedback to
  // the interactive Claude session instead of POSTing to GitHub.
  const isLocal = $derived(owner === "local");

  // Draft the user is about to publish. Read once on open to snapshot;
  // we still read live for comment deletion/adds but the body/event
  // come from draft state continuously.
  const draft = $derived(diffStore.getDraft());

  let submitting = $state(false);
  let errorMsg = $state<string | null>(null);

  // Split an error string into text segments and http(s) URL segments
  // so the template can render URLs as clickable anchors without
  // {@html} (keeps it XSS-safe).
  interface ErrorSegment {
    kind: "text" | "link";
    value: string;
  }
  const errorSegments = $derived.by<ErrorSegment[]>(() => {
    if (!errorMsg) return [];
    const out: ErrorSegment[] = [];
    const re = /https?:\/\/[^\s)]+/g;
    let last = 0;
    for (const m of errorMsg.matchAll(re)) {
      const idx = m.index ?? 0;
      if (idx > last) out.push({ kind: "text", value: errorMsg.slice(last, idx) });
      out.push({ kind: "link", value: m[0] });
      last = idx + m[0].length;
    }
    if (last < errorMsg.length) out.push({ kind: "text", value: errorMsg.slice(last) });
    return out;
  });

  function onBodyInput(e: Event): void {
    diffStore.setDraftBody((e.target as HTMLTextAreaElement).value);
  }

  function onEventChange(e: ReviewEvent): void {
    diffStore.setDraftEvent(e);
  }

  // Publish. Each inline comment carries the commit SHA it was
  // drafted against so the backend can POST each one individually —
  // reviewers drafting commit-by-commit don't have to worry about
  // everything getting anchored to HEAD at publish time. The
  // review-level commit_id is still sent as a fallback used when a
  // comment has no commit_sha of its own.
  // Compose review feedback as a markdown turn the Claude session
  // can read. Each draft comment becomes a quoted block tagged with
  // file:line so Claude has the same anchors the reviewer was
  // looking at.
  function compileReviewFeedback(
    body: string,
    comments: typeof draft.comments,
  ): string {
    const parts: string[] = [];
    if (body.trim()) parts.push(body.trim());
    if (comments.length > 0) {
      parts.push("Inline review comments:");
      for (const c of comments) {
        const range = c.startLine != null && c.startLine !== c.line
          ? `${c.startLine}–${c.line}`
          : `${c.line}`;
        parts.push(
          `- **${c.path}**:${range} (${c.side === "LEFT" ? "before" : "after"})\n` +
          `  ${c.body.split("\n").join("\n  ")}`,
        );
      }
    }
    return parts.join("\n\n");
  }

  async function onSubmit(): Promise<void> {
    if (submitting) return;
    submitting = true;
    errorMsg = null;

    // Local worktrees route through the session runner instead of
    // GitHub's review-submit endpoint. The Claude session is the
    // "downstream" of the review submission — it's what acts on the
    // feedback.
    if (isLocal) {
      try {
        const content = compileReviewFeedback(draft.body, draft.comments);
        if (!content.trim()) {
          errorMsg = "Add at least one inline comment or a summary";
          return;
        }
        await worktreeSession.submitTurn(owner, name, number, {
          type: "review_feedback",
          content,
        });
        diffStore.clearDraft();
        onclose();
      } catch (err) {
        errorMsg = err instanceof Error ? err.message : String(err);
      } finally {
        submitting = false;
      }
      return;
    }

    const commits = diffStore.getCommits();
    const headSha = commits && commits.length > 0 ? commits[0]!.sha : "";

    const commentsBody = draft.comments.map((c) => {
      // Replies inherit path/line/side/commit_id from the parent
      // thread — send only the body and in_reply_to so the backend
      // can route through GitHub's replies endpoint.
      if (c.inReplyTo != null && c.inReplyTo > 0) {
        return { body: c.body, in_reply_to: c.inReplyTo };
      }
      return {
        path: c.path,
        line: c.line,
        side: c.side,
        ...(c.startLine != null ? { start_line: c.startLine } : {}),
        body: c.body,
        ...(c.commitSha ? { commit_id: c.commitSha } : {}),
      };
    });

    try {
      const { data, error } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review",
        {
          params: { path: { owner, name, number } },
          body: {
            event: draft.event,
            body: draft.body || "",
            ...(headSha ? { commit_id: headSha } : {}),
            comments: commentsBody,
          },
        },
      );
      if (error || !data) {
        const detail =
          error && typeof error === "object" && "detail" in error
            ? String((error as { detail: unknown }).detail)
            : "Unknown error";
        errorMsg = detail;
        return;
      }
      diffStore.clearDraft();
      // Submit handler writes the review event to the DB
      // synchronously before responding, so a refetch right now
      // picks up the new "reviewed" state without waiting on the
      // 60s safety-net poll or a sync-complete event. Fire-and-
      // forget — close the panel immediately so the action feels
      // instant; the next render flips the chip when the refetch
      // lands.
      void pullsStore.loadPulls();
      onclose();
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      submitting = false;
    }
  }

  // Mirrors the submit button's disabled gate so the Cmd/Ctrl+Enter
  // shortcut doesn't bypass it. Kept as a derived boolean to keep
  // both call sites in sync if the gate ever evolves.
  const canSubmit = $derived(
    !submitting &&
      !(draft.event === "REQUEST_CHANGES" &&
        !draft.body.trim() &&
        draft.comments.length === 0),
  );

  // Cmd/Ctrl+Enter in the summary textarea submits the review,
  // matching the inline-comment composer (DiffComposer) and the
  // AI Ask composer. Plain Enter still inserts a newline so users
  // can write multi-line summaries.
  function onSummaryKeydown(e: KeyboardEvent): void {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      if (canSubmit) void onSubmit();
    }
  }

  // Clicking a draft in the preview list jumps the diff to the
  // comment's anchor. Same shape as QuestionsSection.scrollToThread:
  // switch scope to the commit the draft was anchored on (so line
  // numbers resolve), expand the file if it's collapsed, then
  // scroll to the line. Panel is left open by design — the
  // reviewer often wants to step through several drafts in a row.
  async function jumpToDraft(c: DraftComment): Promise<void> {
    const scope = diffStore.getScope();
    const alreadyHere = scope.kind === "commit" && scope.sha === c.commitSha;
    if (c.commitSha && !alreadyHere) {
      await diffStore.selectCommit(c.commitSha);
      await tick();
    }
    if (diffStore.isFileCollapsed(owner, name, number, c.path)) {
      diffStore.toggleFileCollapsed(owner, name, number, c.path);
      await tick();
    }
    const selector =
      `.diff-file[data-file-path="${CSS.escape(c.path)}"] ` +
      `.line-wrap[data-anchor-line="${c.line}"]` +
      `[data-anchor-side="${c.side}"]`;
    const el = document.querySelector<HTMLElement>(selector);
    if (el) {
      el.scrollIntoView({ block: "center", behavior: "smooth" });
      el.classList.add("line-wrap--flash");
      window.setTimeout(() => el.classList.remove("line-wrap--flash"), 1500);
      return;
    }
    const fileEl = document.querySelector<HTMLElement>(
      `.diff-file[data-file-path="${CSS.escape(c.path)}"]`,
    );
    if (fileEl) {
      fileEl.scrollIntoView({ block: "start", behavior: "smooth" });
    }
  }

  // Group pending comments by file for a compact preview list.
  const commentsByPath = $derived.by(() => {
    const map = new Map<string, typeof draft.comments>();
    for (const c of draft.comments) {
      const arr = map.get(c.path) ?? [];
      arr.push(c);
      map.set(c.path, arr);
    }
    return Array.from(map.entries()).map(([path, comments]) => ({
      path,
      comments,
    }));
  });
</script>

<div class="overlay" onclick={onclose} role="presentation"></div>
<div class="panel" role="dialog" aria-label="Finish review">
  <header class="panel__header">
    <h3 class="panel__title">Finish review</h3>
    <button type="button" class="panel__close" onclick={onclose} title="Close">
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6">
        <path d="M3 3L13 13M13 3L3 13" stroke-linecap="round" />
      </svg>
    </button>
  </header>

  <textarea
    class="panel__body-input"
    rows="3"
    placeholder="Leave a review summary (optional). Cmd/Ctrl+Enter to submit."
    value={draft.body}
    oninput={onBodyInput}
    onkeydown={onSummaryKeydown}
  ></textarea>

  <fieldset class="panel__events">
    <legend class="visually-hidden">Review type</legend>
    <label class="panel__event">
      <input
        type="radio"
        name="review-event"
        value="COMMENT"
        checked={draft.event === "COMMENT"}
        onchange={() => onEventChange("COMMENT")}
      />
      <span>Comment</span>
      <small>Submit without approval</small>
    </label>
    <label class="panel__event">
      <input
        type="radio"
        name="review-event"
        value="APPROVE"
        checked={draft.event === "APPROVE"}
        onchange={() => onEventChange("APPROVE")}
      />
      <span>Approve</span>
      <small>Submit and approve</small>
    </label>
    <label class="panel__event">
      <input
        type="radio"
        name="review-event"
        value="REQUEST_CHANGES"
        checked={draft.event === "REQUEST_CHANGES"}
        onchange={() => onEventChange("REQUEST_CHANGES")}
      />
      <span>Request changes</span>
      <small>Submit and request changes</small>
    </label>
  </fieldset>

  {#if draft.comments.length > 0}
    <div class="panel__preview">
      <div class="panel__preview-title">{draft.comments.length} inline comment{draft.comments.length === 1 ? "" : "s"}</div>
      {#each commentsByPath as group (group.path)}
        <div class="preview-file">
          <div class="preview-file__path">{group.path}</div>
          {#each group.comments as c (c.id)}
            <button
              type="button"
              class="preview-comment preview-comment--clickable"
              onclick={() => void jumpToDraft(c)}
              title="Jump to this comment's anchor"
            >
              <span class="preview-comment__anchor">
                {c.side === "LEFT" ? "−" : "+"}{c.startLine != null && c.startLine !== c.line
                  ? `${c.startLine}–${c.line}`
                  : c.line}
              </span>
              <span class="preview-comment__body">{c.body}</span>
            </button>
          {/each}
        </div>
      {/each}
    </div>
  {:else}
    <p class="panel__empty">No inline comments yet. Click the + button beside any line to start one.</p>
  {/if}

  {#if errorMsg}
    <div class="panel__error">
      {#each errorSegments as seg, i (i)}
        {#if seg.kind === "link"}
          <a href={seg.value} target="_blank" rel="noopener noreferrer" class="panel__error-link">
            {seg.value}
          </a>
        {:else}
          {seg.value}
        {/if}
      {/each}
    </div>
  {/if}

  <div class="panel__actions">
    <button
      type="button"
      class="panel__btn panel__btn--primary"
      disabled={!canSubmit}
      onclick={() => void onSubmit()}
    >
      {#if isLocal}
        {submitting ? "Sending…" : "Send to Claude"}
      {:else}
        {submitting ? "Publishing…" : "Publish review"}
      {/if}
    </button>
    <button type="button" class="panel__btn" disabled={submitting} onclick={onclose}>
      Cancel
    </button>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.35);
    z-index: 50;
  }

  .panel {
    position: fixed;
    top: 48px;
    right: 16px;
    width: 420px;
    max-height: calc(100vh - 80px);
    overflow-y: auto;
    background: var(--bg-surface);
    border: 1px solid var(--border-default);
    border-radius: var(--radius-md);
    box-shadow: 0 10px 40px rgba(0, 0, 0, 0.25);
    padding: 14px 16px;
    z-index: 51;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .panel__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .panel__title {
    font-size: 14px;
    font-weight: 600;
    color: var(--text-primary);
    margin: 0;
  }

  .panel__close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    border-radius: var(--radius-sm);
    border: none;
    background: none;
    color: var(--text-muted);
    cursor: pointer;
  }

  .panel__close:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .panel__body-input {
    width: 100%;
    font-family: var(--font-sans);
    font-size: 13px;
    padding: 8px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-primary);
    resize: vertical;
  }

  .panel__body-input:focus {
    outline: none;
    border-color: var(--accent-blue);
  }

  .panel__events {
    display: flex;
    flex-direction: column;
    gap: 4px;
    border: none;
    padding: 0;
    margin: 0;
  }

  .panel__event {
    display: grid;
    grid-template-columns: auto auto 1fr;
    align-items: baseline;
    gap: 6px;
    font-size: 13px;
    padding: 4px;
    border-radius: var(--radius-sm);
    cursor: pointer;
  }

  .panel__event:hover {
    background: var(--bg-surface-hover);
  }

  .panel__event small {
    font-size: 11px;
    color: var(--text-muted);
  }

  .panel__preview {
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    padding: 8px 10px;
    max-height: 200px;
    overflow-y: auto;
  }

  .panel__preview-title {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
    margin-bottom: 6px;
    font-weight: 600;
  }

  .preview-file {
    margin-bottom: 8px;
  }

  .preview-file:last-child {
    margin-bottom: 0;
  }

  .preview-file__path {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    margin-bottom: 2px;
  }

  .preview-comment {
    display: grid;
    grid-template-columns: 48px 1fr;
    gap: 6px;
    font-size: 12px;
    padding: 2px 0;
  }

  /* The preview-comment is now a button so it can be clicked to
     jump to the comment's anchor. Strip the native button chrome
     and add a hover affordance so it reads as interactive without
     looking like a chunky button. */
  .preview-comment--clickable {
    width: 100%;
    text-align: left;
    background: transparent;
    border: 0;
    border-radius: var(--radius-sm);
    padding: 2px 4px;
    margin: 0;
    cursor: pointer;
    font: inherit;
    color: inherit;
  }
  .preview-comment--clickable:hover {
    background: var(--bg-surface-hover, color-mix(in srgb, var(--text-primary) 6%, transparent));
  }
  .preview-comment--clickable:focus-visible {
    outline: 2px solid var(--accent-blue);
    outline-offset: -2px;
  }

  .preview-comment__anchor {
    font-family: var(--font-mono);
    color: var(--text-muted);
  }

  .preview-comment__body {
    color: var(--text-primary);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .panel__empty {
    margin: 0;
    font-size: 12px;
    color: var(--text-muted);
    padding: 8px;
    text-align: center;
    font-style: italic;
  }

  .panel__error-link {
    color: var(--accent-red);
    text-decoration: underline;
    word-break: break-all;
  }

  .panel__error-link:hover {
    filter: brightness(1.15);
  }

  .panel__error {
    padding: 8px 10px;
    font-size: 12px;
    color: var(--accent-red);
    background: color-mix(in srgb, var(--accent-red) 8%, var(--bg-inset));
    border: 1px solid var(--accent-red);
    border-radius: var(--radius-sm);
  }

  .panel__actions {
    display: flex;
    gap: 6px;
    justify-content: flex-end;
  }

  .panel__btn {
    font-size: 12px;
    padding: 6px 14px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-primary);
    cursor: pointer;
  }

  .panel__btn:hover:not(:disabled) {
    background: var(--bg-surface-hover);
  }

  .panel__btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .panel__btn--primary {
    background: var(--accent-blue);
    color: #fff;
    border-color: var(--accent-blue);
  }

  .panel__btn--primary:hover:not(:disabled) {
    filter: brightness(1.1);
  }

  .visually-hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
</style>
