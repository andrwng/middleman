<script lang="ts">
  import {
    getNavigate, getSidebar, getStores,
  } from "../context.js";
  import CollapsibleResizableSidebar from "../components/shared/CollapsibleResizableSidebar.svelte";
  import PullList from "../components/sidebar/PullList.svelte";
  import PullDetail
    from "../components/detail/PullDetail.svelte";
  import DiffView from "../components/diff/DiffView.svelte";
  import DiffSidebar from "../components/diff/DiffSidebar.svelte";
  import ReviewCoverBanner from "../components/detail/ReviewCoverBanner.svelte";
  import ReviewBriefCard from "../components/detail/ReviewBriefCard.svelte";
  import PRNotesPanel from "../components/detail/PRNotesPanel.svelte";
  import PatchsetPicker from "../components/diff/PatchsetPicker.svelte";
  import CommitMessageBanner from "../components/diff/CommitMessageBanner.svelte";
  import StackSidebar
    from "../components/detail/StackSidebar.svelte";

  const { isSidebarToggleEnabled, toggleSidebar } = getSidebar();
  const { detail: detailStore } = getStores();
  const navigate = getNavigate();

  interface Props {
    selectedPR?: {
      owner: string;
      name: string;
      number: number;
    } | null;
    detailTab?: "conversation" | "files";
    isSidebarCollapsed?: boolean;
    hideSidebar?: boolean;
    sidebarWidth?: number;
    onSidebarResize?: (width: number) => void;
  }

  let {
    selectedPR = null,
    detailTab = "files",
    isSidebarCollapsed = false,
    hideSidebar = false,
    sidebarWidth = 340,
    onSidebarResize,
  }: Props = $props();

  // Kick off detail loading whenever the selected PR changes AND the
  // Review branch is showing — PullDetail (which normally loads detail)
  // isn't rendered on the Review branch, so without this the cover
  // banner shows stale detail from the previously loaded PR.
  $effect(() => {
    if (!selectedPR) return;
    if (detailTab !== "files") return;
    const { owner, name, number } = selectedPR;
    void detailStore.loadDetail(owner, name, number);
    detailStore.startDetailPolling(owner, name, number);
    return () => detailStore.stopDetailPolling();
  });

  // Guard the banner against rendering a previous PR's detail during
  // the load gap.
  const selectedPRDetail = $derived.by(() => {
    const d = detailStore.getDetail();
    if (!d || !selectedPR) return null;
    const mr = d.merge_request;
    if (
      d.repo_owner !== selectedPR.owner ||
      d.repo_name !== selectedPR.name ||
      mr.Number !== selectedPR.number
    ) {
      return null;
    }
    return d;
  });
</script>

<CollapsibleResizableSidebar
  isCollapsed={isSidebarCollapsed}
  {hideSidebar}
  {sidebarWidth}
  {onSidebarResize}
  showCollapsedStrip={isSidebarToggleEnabled()}
  onExpand={toggleSidebar}
  mainEmpty={selectedPR === null}
>
  {#snippet sidebar()}
    <PullList getDetailTab={() => detailTab} />
  {/snippet}

  {#if selectedPR !== null}
    <div class="detail-tabs">
      <button
        class="detail-tab"
        class:detail-tab--active={detailTab === "files"}
        onclick={() => navigate(
          `/pulls/${selectedPR.owner}/${selectedPR.name}/${selectedPR.number}/files`,
        )}
      >
        Review
      </button>
      <button
        class="detail-tab"
        class:detail-tab--active={detailTab === "conversation"}
        onclick={() => navigate(
          `/pulls/${selectedPR.owner}/${selectedPR.name}/${selectedPR.number}`,
        )}
      >
        Activity
      </button>
    </div>
    {#if detailTab === "files"}
      {#key `${selectedPR.owner}/${selectedPR.name}/${selectedPR.number}`}
        <div class="review-layout">
          <aside class="review-sidebar">
            <DiffSidebar />
          </aside>
          <div class="review-main">
            {#if selectedPRDetail}
              <ReviewCoverBanner
                pr={selectedPRDetail.merge_request}
                owner={selectedPR.owner}
                name={selectedPR.name}
              />
            {/if}
            <CommitMessageBanner
              owner={selectedPR.owner}
              name={selectedPR.name}
              number={selectedPR.number}
            />
            <PatchsetPicker />
            <ReviewBriefCard
              owner={selectedPR.owner}
              name={selectedPR.name}
              number={selectedPR.number}
            />
            <PRNotesPanel />
            <DiffView
              owner={selectedPR.owner}
              name={selectedPR.name}
              number={selectedPR.number}
            />
          </div>
        </div>
      {/key}
    {:else}
      <PullDetail
        owner={selectedPR.owner}
        name={selectedPR.name}
        number={selectedPR.number}
        hideTabs={true}
      />
    {/if}
  {:else}
    <div class="placeholder-content">
      <p class="placeholder-text">Select a PR</p>
      <p class="placeholder-hint">
        j/k to navigate &middot; 1/2 to switch views
      </p>
    </div>
  {/if}

  {#snippet trailing()}
    {#if selectedPR !== null}
      <StackSidebar
        owner={selectedPR.owner}
        name={selectedPR.name}
        number={selectedPR.number}
      />
    {/if}
  {/snippet}
</CollapsibleResizableSidebar>

<style>
  .detail-tabs {
    display: flex;
    gap: 0;
    border-bottom: 1px solid var(--border-default);
    background: var(--bg-surface);
    flex-shrink: 0;
  }

  .review-layout {
    display: flex;
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  .review-sidebar {
    width: 280px;
    flex-shrink: 0;
    border-right: 1px solid var(--border-default);
    background: var(--bg-surface);
    overflow-y: auto;
    display: flex;
    flex-direction: column;
  }

  .review-main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  @media (max-width: 720px) {
    .review-layout {
      flex-direction: column;
    }
    .review-sidebar {
      width: 100%;
      max-height: 35vh;
      border-right: none;
      border-bottom: 1px solid var(--border-default);
    }
    .review-main {
      flex: 1;
      min-height: 0;
    }
  }

  .placeholder-content {
    text-align: center;
  }

  .placeholder-text {
    color: var(--text-muted);
    font-size: 13px;
  }

  .placeholder-hint {
    color: var(--text-muted);
    font-size: 11px;
    margin-top: 8px;
    opacity: 0.7;
  }

  .detail-tab {
    font-size: 12px;
    font-weight: 500;
    padding: 8px 16px;
    color: var(--text-secondary);
    border-bottom: 2px solid transparent;
    transition: color 0.1s, border-color 0.1s;
  }

  .detail-tab:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .detail-tab--active {
    color: var(--text-primary);
    border-bottom-color: var(--accent-blue);
  }
</style>
