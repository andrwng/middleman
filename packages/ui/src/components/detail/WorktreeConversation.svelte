<script lang="ts">
  // Placeholder for the Activity tab when a worktree is selected.
  //
  // Per the design memo, the Activity surface for a draft (local
  // worktree) is the Claude conversation — turns of review feedback
  // submissions, free-text prompts, and Claude's replies + tool
  // calls. That machinery isn't wired yet; this component is the
  // empty-state placeholder that will be replaced by the real
  // conversation renderer once the session runner lands.
</script>

<div class="wt-conv">
  <div class="wt-conv__card">
    <h2 class="wt-conv__title">No interactive session yet</h2>
    <p class="wt-conv__copy">
      For local worktrees, this tab becomes the back-and-forth with
      Claude: review feedback you submit on the Review tab feeds in
      here, and Claude's replies (plus the file edits it makes) render
      as the conversation grows. Until the session runner lands, this
      tab stays empty.
    </p>
    <p class="wt-conv__hint">
      Use the Review tab to look at what's changed in this worktree.
    </p>
  </div>
</div>

<style>
  .wt-conv {
    display: flex;
    flex: 1;
    align-items: flex-start;
    justify-content: center;
    padding: 40px 24px;
    overflow-y: auto;
  }

  .wt-conv__card {
    max-width: 520px;
    padding: 20px 24px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-md);
    background: var(--bg-surface);
  }

  .wt-conv__title {
    margin: 0 0 8px;
    font-size: 14px;
    font-weight: 600;
    color: var(--text-primary);
  }

  .wt-conv__copy {
    margin: 0 0 10px;
    font-size: 13px;
    line-height: 1.5;
    color: var(--text-secondary);
  }

  .wt-conv__hint {
    margin: 0;
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
  }
</style>
