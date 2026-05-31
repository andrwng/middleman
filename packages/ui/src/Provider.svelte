<script lang="ts">
  import { setContext, onDestroy } from "svelte";
  import {
    API_CLIENT_KEY, ACTIONS_KEY, NAVIGATE_KEY, EVENT_KEY,
    PREPARE_ROUTE_KEY, STORES_KEY, UI_CONFIG_KEY, SIDEBAR_KEY,
    HOST_STATE_KEY,
    ROBOREV_CLIENT_KEY,
  } from "./context.js";
  import { createRoborevClient } from "./api/roborev/client.js";
  import {
    createDaemonStore,
  } from "./stores/roborev/daemon.svelte.js";
  import {
    createJobsStore,
  } from "./stores/roborev/jobs.svelte.js";
  import {
    createReviewStore,
  } from "./stores/roborev/review.svelte.js";
  import {
    createLogStore,
  } from "./stores/roborev/log.svelte.js";
  import type {
    MiddlemanClient, ActionRegistry, NavigateCallback,
    EventCallback, PrepareRouteCallback, HostStateAccessors,
    StoreInstances, UIConfig, SidebarAccessors,
  } from "./types.js";
  import type {
    PullsStoreOptions,
  } from "./stores/pulls.svelte.js";
  import type {
    IssuesStoreOptions,
  } from "./stores/issues.svelte.js";
  import type {
    DetailStoreOptions,
  } from "./stores/detail.svelte.js";
  import type {
    ActivityStoreOptions,
  } from "./stores/activity.svelte.js";
  import type {
    DiffStoreOptions,
  } from "./stores/diff.svelte.js";
  import {
    createPullsStore,
  } from "./stores/pulls.svelte.js";
  import {
    createIssuesStore,
  } from "./stores/issues.svelte.js";
  import {
    createDetailStore,
  } from "./stores/detail.svelte.js";
  import {
    createActivityStore,
  } from "./stores/activity.svelte.js";
  import {
    createSyncStore,
  } from "./stores/sync.svelte.js";
  import {
    createDiffStore,
  } from "./stores/diff.svelte.js";
  import {
    createGroupingStore,
  } from "./stores/grouping.svelte.js";
  import {
    createCollapsedReposStore,
  } from "./stores/collapsedRepos.svelte.js";
  import {
    createSettingsStore,
  } from "./stores/settings.svelte.js";
  import {
    createEventsStore,
  } from "./stores/events.svelte.js";
  import {
    createAIStore,
  } from "./stores/ai.svelte.js";
  import {
    createBriefStore,
  } from "./stores/brief.svelte.js";
  import {
    createFileResolverStore,
  } from "./stores/fileResolver.svelte.js";
  import {
    createCommitAnalysisStore,
  } from "./stores/commitAnalysis.svelte.js";
  import {
    createAuthorGroupsStore,
  } from "./stores/authorGroups.svelte.js";
  import { createViewerStore } from "./stores/viewer.svelte.js";
  import { createAISessionsStore } from "./stores/aiSessions.svelte.js";
  import { createWorktreesStore } from "./stores/worktrees.svelte.js";
  import { createWorktreeSessionStore } from "./stores/worktreeSession.svelte.js";
  import { createReviewThreadsStore } from "./stores/reviewThreads.svelte.js";

  interface Props {
    client: MiddlemanClient;
    actions?: ActionRegistry;
    onNavigate?: NavigateCallback;
    onEvent?: EventCallback;
    prepareRoute?: PrepareRouteCallback;
    hostState?: HostStateAccessors;
    config?: UIConfig;
    sidebar?: SidebarAccessors;
    getPage?: () => string;
    roborevBaseUrl?: string;
    onError?: (msg: string) => void;
    stores?: StoreInstances | undefined;
    children?: import("svelte").Snippet;
  }

  let {
    client,
    actions = {},
    onNavigate = () => {},
    onEvent = () => {},
    prepareRoute = undefined,
    hostState = {},
    config = {},
    sidebar = {
      isEmbedded: () => false,
      isSidebarToggleEnabled: () => true,
      toggleSidebar: () => {},
    },
    getPage = () => "",
    roborevBaseUrl = undefined,
    onError = undefined,
    stores = $bindable(),
    children,
  }: Props = $props();

  // All initialization is in this function so its
  // parameters are plain values, not reactive proxies.
  // This avoids state_referenced_locally warnings.
  function init(
    cl: MiddlemanClient,
    hs: HostStateAccessors,
    cfg: UIConfig,
    act: ActionRegistry,
    nav: NavigateCallback,
    evt: EventCallback,
    prep: PrepareRouteCallback | undefined,
    sb: SidebarAccessors,
    gp: () => string,
    roborevBase: string | undefined,
    errorCb: ((msg: string) => void) | undefined,
  ): StoreInstances {
    const grouping = createGroupingStore();
    const collapsedRepos = createCollapsedReposStore();
    const settingsStore = createSettingsStore();

    // Created early so the pulls store can read the viewer's login
    // for its "my reviews" filter without a circular dependency.
    const viewerStore = createViewerStore({ client: cl });
    void viewerStore.load();

    const pullsOpts: PullsStoreOptions = {
      client: cl,
      getViewerLogin: () => viewerStore.getLogin() ?? "",
    };
    if (hs.getGlobalRepo) {
      pullsOpts.getGlobalRepo = hs.getGlobalRepo;
    }
    pullsOpts.getGroupByRepo =
      hs.getGroupByRepo ?? grouping.getGroupByRepo;
    if (hs.getView) {
      pullsOpts.getView = hs.getView;
    }
    const pullsStore = createPullsStore(pullsOpts);

    const syncStore = createSyncStore({ client: cl });

    const detailOpts: DetailStoreOptions = {
      client: cl,
      getPage: gp,
      pulls: {
        loadPulls: (p?: unknown) => pullsStore.loadPulls(
          p as Parameters<typeof pullsStore.loadPulls>[0],
        ),
        optimisticKanbanUpdate:
          pullsStore.optimisticKanbanUpdate,
        getPullKanbanStatus:
          pullsStore.getPullKanbanStatus,
      },
      sync: syncStore,
    };
    const detailStore = createDetailStore(detailOpts);

    const issuesOpts: IssuesStoreOptions = {
      client: cl,
      getPage: gp,
      sync: {
        refreshSyncStatus:
          syncStore.refreshSyncStatus,
      },
    };
    if (hs.getGlobalRepo) {
      issuesOpts.getGlobalRepo = hs.getGlobalRepo;
    }
    issuesOpts.getGroupByRepo =
      hs.getGroupByRepo ?? grouping.getGroupByRepo;
    const issuesStore = createIssuesStore(issuesOpts);

    const activityOpts: ActivityStoreOptions = {
      client: cl,
    };
    if (hs.getGlobalRepo) {
      activityOpts.getGlobalRepo = hs.getGlobalRepo;
    }
    if (cfg.basePath != null) {
      const bp = cfg.basePath;
      activityOpts.getBasePath = () => bp;
    }
    const activityStore =
      createActivityStore(activityOpts);

    const diffOpts: DiffStoreOptions = { client: cl };
    if (cfg.basePath != null) {
      const bp = cfg.basePath;
      diffOpts.getBasePath = () => bp;
    }
    const diffStore = createDiffStore(diffOpts);

    const worktreesStore = createWorktreesStore({ client: cl });

    const reviewThreadsStore = createReviewThreadsStore({ client: cl });

    const eventsStore = createEventsStore({
      ...(cfg.basePath != null && {
        getBasePath: () => cfg.basePath as string,
      }),
      onDataChanged: () => {
        void pullsStore.loadPulls();
        void issuesStore.loadIssues();
        void activityStore.loadActivity();
        void worktreesStore.loadWorktrees();
        void reviewThreadsStore.refresh();
      },
      onSyncStatus: (status) => {
        syncStore.setSyncStatus(status);
      },
    });

    const aiStore = createAIStore({
      ...(cfg.basePath != null && { getBasePath: () => cfg.basePath as string }),
    });
    const briefStore = createBriefStore({
      ...(cfg.basePath != null && { getBasePath: () => cfg.basePath as string }),
    });
    const fileResolverStore = createFileResolverStore({
      ...(cfg.basePath != null && { getBasePath: () => cfg.basePath as string }),
    });
    const commitAnalysisStore = createCommitAnalysisStore({
      ...(cfg.basePath != null && { getBasePath: () => cfg.basePath as string }),
    });

    const authorGroupsStore = createAuthorGroupsStore({ client: cl });
    void authorGroupsStore.load();

    const aiSessionsStore = createAISessionsStore({
      client: cl,
      onThreadDeleted: (threadID) => {
        aiStore.markThreadDeletedExternally(threadID);
      },
    });
    aiSessionsStore.startPolling();

    const si: StoreInstances = {
      pulls: pullsStore,
      issues: issuesStore,
      detail: detailStore,
      activity: activityStore,
      sync: syncStore,
      diff: diffStore,
      grouping,
      collapsedRepos,
      settings: settingsStore,
      events: eventsStore,
      ai: aiStore,
      brief: briefStore,
      authorGroups: authorGroupsStore,
      viewer: viewerStore,
      aiSessions: aiSessionsStore,
      fileResolver: fileResolverStore,
      commitAnalysis: commitAnalysisStore,
      worktrees: worktreesStore,
      worktreeSession: createWorktreeSessionStore({ client: cl }),
      reviewThreads: reviewThreadsStore,
    };

    if (roborevBase) {
      const bp = (cfg.basePath ?? "/").replace(/\/$/, "");
      const roborevClient = createRoborevClient(
        bp + roborevBase,
      );

      const jobsOpts: Parameters<typeof createJobsStore>[0] = {
        client: roborevClient,
        navigate: nav,
      };
      if (errorCb) jobsOpts.onError = errorCb;
      const jobsStore = createJobsStore(jobsOpts);
      si.roborevJobs = jobsStore;

      const reviewOpts: Parameters<typeof createReviewStore>[0] = {
        client: roborevClient,
      };
      if (errorCb) reviewOpts.onError = errorCb;
      const reviewStore = createReviewStore(reviewOpts);
      si.roborevReview = reviewStore;

      const logStore = createLogStore({
        client: roborevClient,
        baseUrl: bp + roborevBase,
      });
      si.roborevLog = logStore;

      const daemon = createDaemonStore({
        client: roborevClient,
        healthBaseUrl: bp + "/api/v1",
        onRecover: () => {
          void jobsStore.loadJobs();
          const selectedId =
            reviewStore.getSelectedJobId();
          if (selectedId !== undefined) {
            void reviewStore.loadReview(selectedId);
          }
        },
      });
      si.roborevDaemon = daemon;

      setContext(ROBOREV_CLIENT_KEY, roborevClient);
      daemon.startPolling();
    }

    setContext(API_CLIENT_KEY, cl);
    setContext(ACTIONS_KEY, act);
    setContext(NAVIGATE_KEY, nav);
    setContext(EVENT_KEY, evt);
    setContext(PREPARE_ROUTE_KEY, prep ?? null);
    setContext(STORES_KEY, si);
    setContext(UI_CONFIG_KEY, cfg);
    setContext(SIDEBAR_KEY, sb);
    setContext(HOST_STATE_KEY, hs);

    return si;
  }

  // svelte-ignore state_referenced_locally
  stores = init(
    client, hostState, config, actions,
    onNavigate, onEvent, prepareRoute,
    sidebar, getPage, roborevBaseUrl, onError,
  );

  // Phase 2b (5A): while an agent turn is running, re-read the review's
  // threads on the same cadence as the session poll so statuses flip
  // (discussed/applied) and agent replies appear live. The $derived
  // boolean only changes when the running state flips, so the interval
  // is created/torn down once per turn rather than every poll tick.
  const sessionRunning = $derived(
    stores?.worktreeSession?.hasRunningTurn() ?? false,
  );
  $effect(() => {
    if (!sessionRunning) return;
    const rt = stores?.reviewThreads;
    if (!rt) return;
    const id = setInterval(() => {
      void rt.refresh();
    }, 1500);
    return () => clearInterval(id);
  });

  onDestroy(() => {
    stores?.roborevDaemon?.stopPolling();
  });
</script>

{#if children}
  {@render children()}
{/if}
