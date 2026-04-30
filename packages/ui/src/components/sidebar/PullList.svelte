<script lang="ts">
  import { getStores, getNavigate, getSidebar, getActions, getHostState } from "../../context.js";
  import { groupByWorkflow } from "../../stores/workflow.svelte.js";
  import PullItem from "./PullItem.svelte";

  const { pulls, sync, grouping, collapsedRepos, settings, authorGroups, viewer, diff: diffStore } = getStores();

  // "Awaiting my review" — viewer is on the PR's requested-reviewer
  // list. These sort to the top of the PR list (and within each
  // group for the grouped views) so the reviewer sees their queue
  // first.
  function awaitsMyReview(
    pr: { requested_reviewers?: string[] | null; reviewer_logins?: string[] | null },
  ): boolean {
    const login = viewer.getLogin();
    if (!login) return false;
    const needle = login.toLowerCase();
    for (const r of pr.requested_reviewers ?? []) {
      if (!r || r.startsWith("team:")) continue;
      if (r.toLowerCase() === needle) return true;
    }
    for (const r of pr.reviewer_logins ?? []) {
      if (r && r.toLowerCase() === needle) return true;
    }
    return false;
  }

  // Per-row review state, computed exactly the way PullItem renders
  // its chip — drafts override server-computed state because they
  // represent unsaved work the reviewer must finish.
  function rowReviewState(
    pr: {
      State?: string;
      review_state?: string | null;
      repo_owner?: string | null;
      repo_name?: string | null;
      Number: number;
    },
  ): "in-review" | "responded" | "unreviewed" | "reviewed" {
    if (pr.State !== "open") return "unreviewed";
    if (diffStore.hasDraftForPR(pr.repo_owner ?? "", pr.repo_name ?? "", pr.Number)) {
      return "in-review";
    }
    const s = pr.review_state ?? "unreviewed";
    if (s === "reviewed" || s === "responded") return s;
    return "unreviewed";
  }

  // Sort priority. Lower number = higher priority (closer to the top).
  // Top-to-bottom order:
  //   0 reviewed              — ball in author's court; you're done
  //   1 in-review             — unsaved drafts you're working on
  //   2 responded             — author moved while you were waiting
  //   3 unreviewed-on-queue   — you're on the reviewer list
  //   4 unreviewed            — default open PRs
  function rowPriority<
    T extends {
      State?: string;
      review_state?: string | null;
      requested_reviewers?: string[] | null;
      reviewer_logins?: string[] | null;
      repo_owner?: string | null;
      repo_name?: string | null;
      Number: number;
    },
  >(pr: T): number {
    const s = rowReviewState(pr);
    if (s === "reviewed") return 0;
    if (s === "in-review") return 1;
    if (s === "responded") return 2;
    return awaitsMyReview(pr) ? 3 : 4;
  }

  // Stable sort by review priority; existing relative order
  // preserved within a priority bucket (the source list is already
  // sorted by last_activity_at DESC server-side).
  function sortReviewFirst<
    T extends {
      State?: string;
      review_state?: string | null;
      requested_reviewers?: string[] | null;
      reviewer_logins?: string[] | null;
      repo_owner?: string | null;
      repo_name?: string | null;
      Number: number;
    },
  >(list: T[]): T[] {
    return list
      .map((item, i) => ({ item, i, prio: rowPriority(item) }))
      .sort((a, b) => a.prio - b.prio || a.i - b.i)
      .map((x) => x.item);
  }
  const navigate = getNavigate();
  const actions = getActions();
  const hostState = getHostState();
  const { isEmbedded, isSidebarToggleEnabled, toggleSidebar } = getSidebar();

  const importAction = $derived(
    (actions.pull ?? []).find(
      (a) => a.id === "import-worktree",
    ),
  );
  const activeWorktreeKey = $derived(
    hostState.getActiveWorktreeKey?.(),
  );
  const groupingMode = $derived(
    grouping.getGroupingMode(),
  );
  const workflowGroups = $derived(
    groupByWorkflow(pulls.getPulls(), activeWorktreeKey),
  );

  interface Props {
    getDetailTab?: () => string;
  }
  const { getDetailTab: _getDetailTab = () => "conversation" }: Props = $props();

  let searchInput = $state(pulls.getSearchQuery() ?? "");
  let debounceHandle: ReturnType<typeof setTimeout> | null = null;
  let refreshHandle: ReturnType<typeof setInterval> | null = null;

  $effect(() => {
    // Initial load + 15s background refresh. Intentionally does not
    // read any reactive sync state — earlier versions called
    // sync.getSyncState() here, which made $effect re-run on every
    // /sync/status poll (~every 2s while sync is running), which
    // in turn fired another loadPulls() per tick. Use the
    // event-based subscribeSyncComplete instead, and debounce it
    // so a flapping sync (e.g. rapid fail/retry) can't still
    // swamp /pulls.
    // Refresh sources, lowest → highest priority:
    //   - 60s safety-net poll (was 15s; with SSE + sync-complete
    //     wired up, the aggressive cadence was redundant and made
    //     the console look like the app was hammering the server).
    //   - sync.subscribeSyncComplete fires within seconds of a
    //     server sync finishing; debounced 2s so a flapping sync
    //     doesn't fire per retry.
    void pulls.loadPulls();

    refreshHandle = setInterval(() => {
      void pulls.loadPulls();
    }, 60_000);

    let syncDebounce: ReturnType<typeof setTimeout> | null = null;
    const unsub = sync.subscribeSyncComplete(() => {
      if (syncDebounce !== null) clearTimeout(syncDebounce);
      syncDebounce = setTimeout(() => {
        syncDebounce = null;
        void pulls.loadPulls();
      }, 2_000);
    });

    return () => {
      if (refreshHandle !== null) clearInterval(refreshHandle);
      if (syncDebounce !== null) clearTimeout(syncDebounce);
      unsub();
    };
  });

  function onSearchInput(e: Event): void {
    const value = (e.target as HTMLInputElement).value;
    searchInput = value;

    if (debounceHandle !== null) clearTimeout(debounceHandle);
    debounceHandle = setTimeout(() => {
      pulls.setSearchQuery(value.trim() === "" ? undefined : value.trim());
      void pulls.loadPulls();
    }, 300);
  }

  function handleSelect(owner: string, name: string, number: number): void {
    pulls.selectPR(owner, name, number);
    if (_getDetailTab() === "files") {
      navigate(`/pulls/${owner}/${name}/${number}/files`);
    } else {
      navigate(`/pulls/${owner}/${name}/${number}`);
    }
  }

  function isSelected(owner: string, name: string, number: number): boolean {
    const sel = pulls.getSelectedPR();
    return sel !== null && sel.owner === owner && sel.name === name && sel.number === number;
  }

  const selectedVisiblePR = $derived.by(() => {
    const sel = pulls.getSelectedPR();
    if (sel === null) return null;
    const pr = pulls.getPulls().find(
      (p) => (p.repo_owner ?? "") === sel.owner
        && (p.repo_name ?? "") === sel.name
        && p.Number === sel.number,
    );
    if (!pr) return null;
    // In byRepo mode, a user-collapsed repo group hides the PR row — treat
    // as not visible so the fallback file list renders instead.
    if (
      groupingMode === "byRepo"
      && collapsedRepos.isCollapsed("pulls", `${sel.owner}/${sel.name}`)
    ) {
      return null;
    }
    return pr;
  });

  const isDiffFocus = $derived(
    _getDetailTab() === "files" && selectedVisiblePR !== null,
  );

  const isSelectedActiveWorktree = $derived.by(() => {
    const key = activeWorktreeKey;
    const pr = selectedVisiblePR;
    if (!key || !pr || !pr.worktree_links) return false;
    return pr.worktree_links.some((l) => l.worktree_key === key);
  });

  let authorPopoverOpen = $state(false);
  const authorFilterActive = $derived(pulls.getFilterAuthors().length > 0);

  // Review-state tally for the visible PR list. Only counts open PRs;
  // closed/merged ones don't have an actionable review state.
  // Computed against the unsorted list so the totals don't shuffle
  // when sortReviewFirst reorders rows.
  const reviewTally = $derived.by(() => {
    let inReview = 0, responded = 0, reviewed = 0, unreviewed = 0;
    for (const pr of pulls.getPulls()) {
      if (pr.State !== "open") continue;
      const s = rowReviewState(pr);
      if (s === "in-review") inReview++;
      else if (s === "responded") responded++;
      else if (s === "reviewed") reviewed++;
      else unreviewed++;
    }
    return {
      inReview, responded, reviewed, unreviewed,
      total: inReview + responded + reviewed + unreviewed,
    };
  });

  const groupList = $derived(authorGroups.list());
  const activeGroupId = $derived(authorGroups.getActiveId());

  // --- Author group save-as form state ---
  let showSaveForm = $state(false);
  let saveName = $state("");
  let savingGroup = $state(false);
  let groupError = $state<string | null>(null);

  function toggleAuthorPopover(): void {
    authorPopoverOpen = !authorPopoverOpen;
    if (!authorPopoverOpen) resetSaveForm();
  }

  function resetSaveForm(): void {
    showSaveForm = false;
    saveName = "";
    groupError = null;
  }

  function applyGroup(id: number): void {
    const g = groupList.find((x) => x.id === id);
    if (!g) return;
    pulls.setFilterAuthors([...g.members]);
    authorGroups.setActive(id);
    void pulls.loadPulls();
    authorPopoverOpen = false;
  }

  function clearActiveGroup(): void {
    pulls.setFilterAuthors([]);
    authorGroups.setActive(null);
    authorPopoverOpen = false;
  }

  async function saveCurrentAsGroup(): Promise<void> {
    const members = pulls.getFilterAuthors();
    if (members.length === 0) {
      groupError = "Pick some authors first";
      return;
    }
    const name = saveName.trim();
    if (name === "") {
      groupError = "Group name is required";
      return;
    }
    savingGroup = true;
    groupError = null;
    try {
      // If a group with this name already exists, update it —
      // lets the user refresh a group's membership without
      // having to delete + recreate.
      const existing = groupList.find(
        (g) => g.name.toLowerCase() === name.toLowerCase(),
      );
      const g = existing
        ? await authorGroups.update(existing.id, name, members)
        : await authorGroups.create(name, members);
      if (!g) {
        groupError = authorGroups.getError() ?? "Failed to save group";
        return;
      }
      authorGroups.setActive(g.id);
      resetSaveForm();
    } finally {
      savingGroup = false;
    }
  }

  async function deleteGroup(id: number): Promise<void> {
    const g = groupList.find((x) => x.id === id);
    if (!g) return;
    if (!confirm(`Delete the "${g.name}" group?`)) return;
    await authorGroups.remove(id);
    if (activeGroupId === id) {
      pulls.setFilterAuthors([]);
    }
  }

  function handleAuthorPopoverMousedown(e: MouseEvent): void {
    // Close if click is outside the popover
    const target = e.target as HTMLElement;
    if (!target.closest(".author-filter-wrap")) {
      authorPopoverOpen = false;
      resetSaveForm();
    }
  }

  $effect(() => {
    if (!authorPopoverOpen) return;
    document.addEventListener("mousedown", handleAuthorPopoverMousedown);
    return () => document.removeEventListener("mousedown", handleAuthorPopoverMousedown);
  });
</script>

<div class="pull-list">
  <div class="filter-bar">
    <span class="count-badge">{pulls.getPulls().length} PRs</span>
    <div class="state-toggle">
      {#each ["open", "closed", "all"] as s (s)}
        <button
          class="state-btn"
          class:state-btn--active={pulls.getFilterState() === s}
          onclick={() => { pulls.setFilterState(s); void pulls.loadPulls(); }}
        >{s === "open" ? "Open" : s === "closed" ? "Closed" : "All"}</button>
      {/each}
    </div>
    <div class="state-toggle" title="Filter by how recently the PR was updated">
      {#each [null, 30, 7] as d (d ?? "all")}
        <button
          class="state-btn"
          class:state-btn--active={pulls.getFilterRecencyDays() === d}
          onclick={() => pulls.setFilterRecencyDays(d)}
          title={d === null
            ? "Show all open PRs"
            : `Show only PRs updated in the last ${d} days`}
        >{d === null ? "Any" : `${d}d`}</button>
      {/each}
    </div>
    <div class="group-toggle">
      <button
        class="group-btn"
        class:group-btn--active={groupingMode === "byRepo"}
        onclick={() => grouping.setGroupingMode("byRepo")}
      >Repo</button>
      <button
        class="group-btn"
        class:group-btn--active={groupingMode === "byWorkflow"}
        onclick={() => grouping.setGroupingMode("byWorkflow")}
      >Status</button>
      <button
        class="group-btn"
        class:group-btn--active={groupingMode === "flat"}
        onclick={() => grouping.setGroupingMode("flat")}
      >All</button>
    </div>
    {#if isSidebarToggleEnabled()}
      <button class="sidebar-toggle" onclick={toggleSidebar} title="Collapse sidebar">
        <svg width="14" height="14" viewBox="0 0 16 16"
          fill="none" stroke="currentColor" stroke-width="1.5">
          <rect x="1" y="1" width="14" height="14" rx="2" />
          <line x1="6" y1="1" x2="6" y2="15" />
          <polyline points="10,6 8,8 10,10"
            stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
    {/if}
  </div>
  <div class="search-bar">
    <div class="search-input-wrap">
      <svg class="search-icon" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
        <circle cx="6.5" cy="6.5" r="4.5" stroke="currentColor" stroke-width="1.5" />
        <path d="M10 10L14 14" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
      </svg>
      <input
        class="search-input"
        type="search"
        placeholder="Search PRs..."
        value={searchInput}
        oninput={onSearchInput}
      />
    </div>
    <button
      class="star-filter-btn"
      class:star-filter-btn--active={pulls.getFilterStarred()}
      onclick={() => { pulls.setFilterStarred(!pulls.getFilterStarred()); void pulls.loadPulls(); }}
      title={pulls.getFilterStarred() ? "Show all" : "Show starred only"}
    >
      {#if pulls.getFilterStarred()}
        <svg width="12" height="12" viewBox="0 0 16 16" fill="currentColor">
          <path d="M8 .25a.75.75 0 01.673.418l1.882 3.815 4.21.612a.75.75 0 01.416 1.279l-3.046 2.97.719 4.192a.75.75 0 01-1.088.791L8 12.347l-3.766 1.98a.75.75 0 01-1.088-.79l.72-4.194L.818 6.374a.75.75 0 01.416-1.28l4.21-.611L7.327.668A.75.75 0 018 .25z"/>
        </svg>
      {:else}
        <svg width="12" height="12" viewBox="0 0 16 16" fill="currentColor">
          <path d="M8 .25a.75.75 0 01.673.418l1.882 3.815 4.21.612a.75.75 0 01.416 1.279l-3.046 2.97.719 4.192a.75.75 0 01-1.088.791L8 12.347l-3.766 1.98a.75.75 0 01-1.088-.79l.72-4.194L.818 6.374a.75.75 0 01.416-1.28l4.21-.611L7.327.668A.75.75 0 018 .25zm0 2.445L6.615 5.5a.75.75 0 01-.564.41l-3.097.45 2.24 2.184a.75.75 0 01.216.664l-.528 3.084 2.769-1.456a.75.75 0 01.698 0l2.77 1.456-.53-3.084a.75.75 0 01.216-.664l2.24-2.183-3.096-.45a.75.75 0 01-.564-.41L8 2.694z"/>
        </svg>
      {/if}
    </button>
    <div class="author-filter-wrap">
      <button
        class="author-filter-btn"
        class:author-filter-btn--active={authorFilterActive}
        onclick={toggleAuthorPopover}
        title={authorFilterActive
          ? `Filtering: ${pulls.getFilterAuthors().join(", ")}`
          : "Filter by author"}
      >
        <svg width="12" height="12" viewBox="0 0 16 16" fill="currentColor">
          <path d="M10.561 8.073a6.005 6.005 0 0 1 3.432 5.142.75.75 0 1 1-1.498.07 4.5 4.5 0 0 0-8.99 0 .75.75 0 0 1-1.498-.07 6.004 6.004 0 0 1 3.431-5.142 3.999 3.999 0 1 1 5.123 0zM10.5 5a2.5 2.5 0 1 0-5 0 2.5 2.5 0 0 0 5 0z" />
        </svg>
        {#if authorFilterActive}
          <span class="author-filter-badge">{pulls.getFilterAuthors().length}</span>
        {/if}
      </button>
      {#if authorPopoverOpen}
        <div class="author-popover">
          {#if authorFilterActive}
            {#if showSaveForm}
              <div class="author-popover__save-form">
                <input
                  class="author-popover__save-input"
                  placeholder="Group name"
                  bind:value={saveName}
                  onkeydown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      void saveCurrentAsGroup();
                    } else if (e.key === "Escape") {
                      e.preventDefault();
                      resetSaveForm();
                    }
                  }}
                />
                <button
                  class="author-popover__save-btn"
                  onclick={() => void saveCurrentAsGroup()}
                  disabled={savingGroup || saveName.trim() === ""}
                >Save</button>
                <button
                  class="author-popover__cancel-btn"
                  onclick={resetSaveForm}
                >Cancel</button>
              </div>
              {#if groupError}
                <div class="author-popover__error">{groupError}</div>
              {/if}
            {:else}
              <button
                class="author-popover__save-as"
                onclick={() => { showSaveForm = true; groupError = null; }}
              >Save selection as group</button>
            {/if}
            <div class="author-popover__divider"></div>
          {/if}

          {#if groupList.length > 0}
            <div class="author-popover__section-head">Groups</div>
            {#each groupList as g (g.id)}
              {@const active = activeGroupId === g.id}
              <div
                class="author-popover__group"
                class:author-popover__group--active={active}
              >
                <button
                  class="author-popover__group-apply"
                  onclick={() => applyGroup(g.id)}
                  title={`Filter to ${g.members.length} ${g.members.length === 1 ? "author" : "authors"}: ${g.members.join(", ")}`}
                >
                  <span class="author-popover__check">{active ? "✓" : ""}</span>
                  <span class="author-popover__name">{g.name}</span>
                  <span class="author-popover__group-count">{g.members.length}</span>
                </button>
                <button
                  class="author-popover__group-del"
                  title="Delete group"
                  onclick={() => void deleteGroup(g.id)}
                  aria-label={`Delete ${g.name} group`}
                >
                  <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6">
                    <path d="M2 2L8 8M8 2L2 8" stroke-linecap="round" />
                  </svg>
                </button>
              </div>
            {/each}
            <div class="author-popover__divider"></div>
          {/if}

          <div class="author-popover__section-head">Authors</div>
          {#each pulls.getAvailableAuthors() as author (author)}
            {@const checked = pulls.getFilterAuthors().includes(author)}
            <button
              class="author-popover__item"
              class:author-popover__item--active={checked}
              onclick={() => {
                pulls.toggleFilterAuthor(author);
                // Editing membership manually breaks the active
                // group identity; clear it so we don't mislabel
                // a custom filter as a saved group.
                if (activeGroupId !== null) authorGroups.setActive(null);
              }}
            >
              <span class="author-popover__check">{checked ? "\u2713" : ""}</span>
              <span class="author-popover__name">{author}</span>
            </button>
          {:else}
            <div class="author-popover__empty">No authors</div>
          {/each}
          {#if authorFilterActive}
            <div class="author-popover__divider"></div>
            <button
              class="author-popover__reset"
              onclick={clearActiveGroup}
            >Clear filter</button>
          {:else if groupList.length === 0}
            <div class="author-popover__divider"></div>
            <div class="author-popover__hint">Pick authors, then save as a group</div>
          {/if}
        </div>
      {/if}
    </div>
  </div>

  {#if pulls.getFilterState() !== "open"}
    <p class="state-note">Showing items closed after middleman began tracking them</p>
  {/if}
  {#if reviewTally.total > 0}
    <div class="review-tally" title="Review state breakdown across the visible list">
      {#if reviewTally.inReview > 0}
        <span class="review-tally__pill review-tally__pill--inreview">drafts {reviewTally.inReview}</span>
      {/if}
      {#if reviewTally.responded > 0}
        <span class="review-tally__pill review-tally__pill--responded">↻ {reviewTally.responded}</span>
      {/if}
      {#if reviewTally.unreviewed > 0}
        <span class="review-tally__pill review-tally__pill--unreviewed">unreviewed {reviewTally.unreviewed}</span>
      {/if}
      {#if reviewTally.reviewed > 0}
        <span class="review-tally__pill review-tally__pill--reviewed">✓ {reviewTally.reviewed}</span>
      {/if}
    </div>
  {/if}
  <div
    class="list-body"
    class:list-body--diff-focus={isDiffFocus}
    class:list-body--diff-focus-worktree={isDiffFocus && isSelectedActiveWorktree}
  >
    {#if settings.isSettingsLoaded() && !settings.hasConfiguredRepos()}
      <p class="state-message">No repositories configured.<br />
        {#if !isEmbedded()}<button class="settings-link" onclick={() => navigate("/settings")}>Add one in Settings</button>{/if}</p>
    {:else if pulls.isLoading() && pulls.getPulls().length === 0}
      <p class="state-message">Loading…</p>
    {:else if pulls.getError() !== null && pulls.getPulls().length === 0}
      <p class="state-message state-message--error">Error: {pulls.getError()}</p>
    {:else if pulls.getPulls().length === 0 && sync.getSyncState()?.running}
      <div class="state-message sync-message">
        <span class="sync-dot"></span>
        Syncing from GitHub…
      </div>
    {:else if pulls.getPulls().length === 0 && !sync.getSyncState()?.last_run_at}
      <p class="state-message">Waiting for first sync…</p>
    {:else if pulls.getPulls().length === 0}
      <p class="state-message">No pull requests found.</p>
    {:else}
      {#if groupingMode === "byRepo"}
        {#each [...pulls.pullsByRepo().entries()] as [repo, prs] (repo)}
          {@const userCollapsed = collapsedRepos.isCollapsed("pulls", repo)}
          {@const hasSelectedPR = isDiffFocus && prs.some(
            (p) => isSelected(p.repo_owner ?? "", p.repo_name ?? "", p.Number),
          )}
          {@const collapsed = userCollapsed && !hasSelectedPR}
          <div class="repo-group">
            <button
              type="button"
              class="repo-header"
              aria-expanded={!collapsed}
              onclick={() => collapsedRepos.toggle("pulls", repo)}
            >
              <svg
                class="repo-header__chevron"
                class:repo-header__chevron--collapsed={collapsed}
                width="10" height="10" viewBox="0 0 10 10"
                fill="none" stroke="currentColor" stroke-width="1.5"
              >
                <polyline points="2,3 5,7 8,3" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
              <span class="repo-header__name">{repo}</span>
              <span class="repo-header__count">{prs.length}</span>
            </button>
            {#if !collapsed}
              {#each sortReviewFirst(prs) as pr (pr.ID)}
                {@const prSelected = isSelected(pr.repo_owner ?? "", pr.repo_name ?? "", pr.Number)}
                <PullItem
                  {pr}
                  showRepo={false}
                  selected={prSelected}
                  {importAction}
                  onclick={() => handleSelect(pr.repo_owner ?? "", pr.repo_name ?? "", pr.Number)}
                />
              {/each}
            {/if}
          </div>
        {/each}
      {:else if groupingMode === "byWorkflow"}
        {#each workflowGroups as wg (wg.group)}
          <div class="repo-group">
            <h3 class="repo-header">{wg.label}</h3>
            {#each sortReviewFirst(wg.items) as pr (pr.ID)}
              {@const prSelected = isSelected(pr.repo_owner ?? "", pr.repo_name ?? "", pr.Number)}
              <PullItem
                {pr}
                showRepo={true}
                selected={prSelected}
                {importAction}
                onclick={() => handleSelect(pr.repo_owner ?? "", pr.repo_name ?? "", pr.Number)}
              />
            {/each}
          </div>
        {/each}
      {:else}
        {#each sortReviewFirst(pulls.getPulls()) as pr (pr.ID)}
          {@const prSelected = isSelected(pr.repo_owner ?? "", pr.repo_name ?? "", pr.Number)}
          <PullItem
            {pr}
            showRepo={true}
            selected={prSelected}
            {importAction}
            onclick={() => handleSelect(pr.repo_owner ?? "", pr.repo_name ?? "", pr.Number)}
          />
        {/each}
      {/if}
    {/if}
  </div>
  <div class="sidebar-footer">
    {#if !isEmbedded()}
      <button class="add-repo-link" onclick={() => navigate("/settings")}>
        + Add repository
      </button>
    {/if}
  </div>
</div>

<style>
  .pull-list {
    display: flex;
    flex-direction: column;
    height: 100%;
    width: 100%;
  }

  .filter-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--border-muted);
    flex-shrink: 0;
    background: var(--bg-surface);
  }

  .sidebar-toggle {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    margin-left: auto;
    flex-shrink: 0;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
    cursor: pointer;
    transition: color 0.1s, background 0.1s;
  }

  .sidebar-toggle:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .search-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--border-default);
    flex-shrink: 0;
    background: var(--bg-surface);
  }

  .search-input-wrap {
    position: relative;
    flex: 1;
    min-width: 0;
  }

  .search-icon {
    position: absolute;
    left: 8px;
    top: 50%;
    transform: translateY(-50%);
    width: 13px;
    height: 13px;
    color: var(--text-muted);
    pointer-events: none;
  }

  .search-input {
    width: 100%;
    font-size: 12px;
    padding: 5px 8px 5px 28px;
    background: var(--bg-inset);
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
  }

  .search-input:focus {
    border-color: var(--accent-blue);
    outline: none;
  }

  .star-filter-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
    cursor: pointer;
    flex-shrink: 0;
    transition: color 0.1s, background 0.1s;
  }

  .star-filter-btn:hover {
    color: var(--accent-amber);
    background: var(--bg-surface-hover);
  }

  .star-filter-btn--active {
    color: var(--accent-amber);
  }

  .author-filter-wrap {
    position: relative;
    flex-shrink: 0;
  }

  .author-filter-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 3px;
    width: 26px;
    height: 26px;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
    cursor: pointer;
    flex-shrink: 0;
    transition: color 0.1s, background 0.1s;
  }

  .author-filter-btn:hover {
    color: var(--accent-blue);
    background: var(--bg-surface-hover);
  }

  .author-filter-btn--active {
    color: var(--accent-blue);
    width: auto;
    padding: 0 6px;
  }

  .author-filter-badge {
    font-size: 9px;
    font-weight: 600;
    min-width: 14px;
    height: 14px;
    line-height: 14px;
    text-align: center;
    border-radius: 7px;
    background: var(--accent-blue);
    color: white;
  }

  .author-popover {
    position: absolute;
    top: 100%;
    right: 0;
    margin-top: 4px;
    min-width: 160px;
    max-height: 260px;
    overflow-y: auto;
    background: var(--bg-surface);
    border: 1px solid var(--border-default);
    border-radius: 6px;
    box-shadow: var(--shadow-md);
    z-index: 50;
    padding: 4px 0;
  }

  .author-popover__item {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 5px 10px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
    border: none;
    background: none;
    text-align: left;
  }

  .author-popover__item:hover {
    background: var(--bg-surface-hover);
  }

  .author-popover__item--active {
    color: var(--text-primary);
  }

  .author-popover__check {
    width: 14px;
    text-align: center;
    font-size: 11px;
    color: var(--accent-blue);
    flex-shrink: 0;
  }

  .author-popover__name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-mono);
    font-size: 11px;
  }

  .author-popover__empty {
    padding: 8px 10px;
    font-size: 11px;
    color: var(--text-muted);
  }

  .author-popover__reset {
    display: block;
    width: 100%;
    padding: 5px 10px;
    font-size: 11px;
    color: var(--text-muted);
    cursor: pointer;
    border: none;
    border-top: 1px solid var(--border-muted);
    background: none;
    text-align: left;
    margin-top: 2px;
  }

  .author-popover__reset:hover {
    color: var(--accent-blue);
    background: var(--bg-surface-hover);
  }

  .author-popover__section-head {
    padding: 6px 10px 2px;
    font-size: 10px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-weight: 600;
  }

  .author-popover__divider {
    height: 1px;
    background: var(--border-muted);
    margin: 4px 0;
  }

  .author-popover__group {
    display: flex;
    align-items: stretch;
  }

  .author-popover__group--active .author-popover__name {
    color: var(--accent-blue);
    font-weight: 600;
  }

  .author-popover__group-apply {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 5px 6px 5px 10px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: var(--text-primary);
    flex: 1;
    min-width: 0;
    font-size: 11px;
  }

  .author-popover__group-apply:hover {
    background: var(--bg-surface-hover);
  }

  .author-popover__group-count {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--bg-inset);
    margin-left: auto;
  }

  .author-popover__group-del {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    border: none;
    background: none;
    color: var(--text-muted);
    cursor: pointer;
  }

  .author-popover__group-del:hover {
    color: var(--accent-red);
    background: var(--bg-surface-hover);
  }

  .author-popover__hint {
    padding: 6px 10px;
    font-size: 11px;
    color: var(--text-muted);
    font-style: italic;
  }

  .author-popover__save-form {
    display: flex;
    gap: 4px;
    padding: 6px 8px;
  }

  .author-popover__save-input {
    flex: 1;
    min-width: 0;
    padding: 3px 6px;
    font-size: 11px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
  }

  .author-popover__save-input:focus {
    outline: none;
    border-color: var(--accent-blue);
  }

  .author-popover__save-btn,
  .author-popover__cancel-btn {
    padding: 3px 8px;
    font-size: 11px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }

  .author-popover__save-btn {
    background: var(--accent-blue);
    border-color: var(--accent-blue);
    color: #fff;
  }

  .author-popover__save-btn:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .author-popover__cancel-btn:hover {
    background: var(--bg-surface-hover);
  }

  .author-popover__save-as {
    display: block;
    width: 100%;
    padding: 5px 10px;
    font-size: 11px;
    color: var(--accent-blue);
    cursor: pointer;
    border: none;
    background: none;
    text-align: left;
  }

  .author-popover__save-as:hover {
    background: var(--bg-surface-hover);
  }

  .author-popover__error {
    padding: 4px 10px 6px;
    font-size: 11px;
    color: var(--accent-red);
  }

  .count-badge {
    font-size: 11px;
    font-weight: 600;
    color: var(--text-muted);
    background: var(--bg-inset);
    border: 1px solid var(--border-muted);
    border-radius: 10px;
    padding: 2px 7px;
    flex-shrink: 0;
  }

  .list-body {
    flex: 1;
    overflow-y: auto;
  }

  /* Diff focus: combine typographic mute on siblings + a continuous
     accent rail that extends from the selected card through the inline
     file list, binding them as one visual unit. */
  .list-body--diff-focus :global(.pull-item:not(.selected) .title) {
    color: var(--text-muted);
    font-weight: 400;
    transition: color 0.15s ease;
  }

  .list-body--diff-focus :global(.pull-item:not(.selected) .state-dot) {
    opacity: 0.45;
  }

  .list-body--diff-focus :global(.pull-item:not(.selected):hover .title) {
    color: var(--text-secondary);
  }

  .state-message {
    padding: 24px 16px;
    font-size: 13px;
    color: var(--text-muted);
    text-align: center;
  }

  .state-message--error {
    color: var(--accent-red);
  }

  .settings-link {
    color: var(--accent-blue);
    cursor: pointer;
    font-size: 13px;
    margin-top: 4px;
    display: inline-block;
  }

  .settings-link:hover {
    text-decoration: underline;
  }

  .sync-message {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
  }

  .sync-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent-green);
    animation: pulse 1.5s ease-in-out infinite;
  }

  @keyframes pulse {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 1; }
  }

  .repo-group {
    border-bottom: 1px solid var(--border-default);
  }

  .repo-header {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    padding: 6px 12px 4px;
    background: var(--bg-inset);
    border-bottom: 1px solid var(--border-muted);
    position: sticky;
    top: 0;
    z-index: 1;

    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    text-align: left;
    border-top: none;
    border-left: none;
    border-right: none;
    cursor: pointer;
    font-family: inherit;
  }

  .repo-header:hover {
    background: var(--bg-surface-hover);
  }

  .repo-header[aria-expanded="false"] {
    border-bottom: none;
  }

  .repo-header__chevron {
    color: var(--text-muted);
    transition: transform 120ms ease;
    flex-shrink: 0;
  }

  .repo-header__chevron--collapsed {
    transform: rotate(-90deg);
  }

  .repo-header__name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .repo-header__count {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .sidebar-footer {
    padding: 8px 12px;
    border-top: 1px solid var(--border-muted);
    flex-shrink: 0;
  }

  .add-repo-link {
    font-size: 12px;
    color: var(--text-muted);
    cursor: pointer;
    transition: color 0.1s;
    padding: 0;
  }

  .add-repo-link:hover {
    color: var(--accent-blue);
  }

  .state-toggle {
    display: flex;
    gap: 2px;
    background: var(--bg-inset);
    border-radius: 6px;
    padding: 2px;
  }
  .state-btn {
    font-size: 11px;
    padding: 2px 8px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
    white-space: nowrap;
  }
  .state-btn--active {
    background: var(--bg-surface);
    color: var(--text-primary);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
  }
  .state-note {
    font-size: 11px;
    color: var(--text-muted);
    padding: 4px 10px;
    margin: 0;
    border-bottom: 1px solid var(--border-muted);
  }

  .review-tally {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    padding: 4px 10px;
    border-bottom: 1px solid var(--border-muted);
    background: var(--bg-inset);
  }

  .review-tally__pill {
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    font-size: 10px;
    font-weight: 600;
    border-radius: 999px;
    line-height: 1.4;
  }

  /* Tally pill palette tracks the row chips:
       responded → solid amber (act now)
       in-review → outlined purple (your drafts)
       unreviewed → outlined neutral (default queue)
       reviewed → outlined green (done) */
  .review-tally__pill--responded {
    color: #fff;
    background: var(--accent-amber);
  }

  .review-tally__pill--inreview {
    color: var(--accent-purple);
    border: 1px solid var(--accent-purple);
    background: color-mix(in srgb, var(--accent-purple) 10%, transparent);
  }

  .review-tally__pill--unreviewed {
    color: var(--text-secondary);
    background: var(--bg-surface);
    border: 1px solid var(--border-muted);
  }

  .review-tally__pill--reviewed {
    color: var(--accent-green);
    border: 1px solid color-mix(in srgb, var(--accent-green) 60%, transparent);
    background: color-mix(in srgb, var(--accent-green) 12%, transparent);
  }
  .group-toggle {
    display: flex;
    gap: 2px;
    background: var(--bg-inset);
    border-radius: 6px;
    padding: 2px;
  }
  .group-btn {
    font-size: 11px;
    padding: 2px 8px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
    white-space: nowrap;
  }
  .group-btn--active {
    background: var(--bg-surface);
    color: var(--text-primary);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
  }
</style>
