<script lang="ts">
  import {
    getNavigate, getSidebar, getStores,
  } from "../context.js";
  import CollapsibleResizableSidebar from "../components/shared/CollapsibleResizableSidebar.svelte";
  import PullList from "../components/sidebar/PullList.svelte";
  import PullDetail
    from "../components/detail/PullDetail.svelte";
  import WorktreeConversation
    from "../components/detail/WorktreeConversation.svelte";
  import ReviewSurface from "../components/detail/ReviewSurface.svelte";
  import { isLocalSource } from "../utils/sources.js";
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
  onCollapse={toggleSidebar}
  mainEmpty={selectedPR === null}
>
  {#snippet sidebar()}
    <PullList getDetailTab={() => detailTab} />
  {/snippet}

  {#if selectedPR !== null}
    {@const isLocalPR = isLocalSource(selectedPR.owner)}
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
        <ReviewSurface
          owner={selectedPR.owner}
          name={selectedPR.name}
          number={selectedPR.number}
          pr={selectedPRDetail?.merge_request ?? null}
        />
      {/key}
    {:else if isLocalPR}
      <WorktreeConversation
        owner={selectedPR.owner}
        name={selectedPR.name}
        number={selectedPR.number}
      />
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
