<script lang="ts">
  import { getStores } from "../../context.js";
  import { renderMarkdown } from "../../utils/markdown.js";
  import { timeAgo } from "../../utils/time.js";
  import { extractFileRefs } from "../../stores/fileResolver.svelte.js";

  interface Props {
    owner: string;
    name: string;
    number: number;
    // Render the body regardless of the persisted `collapsed` flag.
    // Used by the consolidated top-sections "peek" flow so a previously
    // collapsed brief still reveals its body when the user clicks the
    // pip. Defaults to false so the stacked card honors localStorage.
    forceExpanded?: boolean;
  }

  const { owner, name, number, forceExpanded = false }: Props = $props();

  const { brief: briefStore, diff: diffStore, fileResolver } = getStores();

  const brief = $derived(briefStore.current());
  const commits = $derived(diffStore.getCommits());
  const headSha = $derived(
    commits && commits.length > 0 ? commits[0]!.sha : "",
  );

  // Persisted collapse state — matches the pr-cover-collapsed /
  // pr-commit-msg-collapsed precedent so reloads keep the user's
  // chosen layout. The polarity is `collapsed` (inverse of the old
  // in-memory `expanded`); a derived alias keeps the existing template
  // markup working without a sweeping rename.
  let collapsed = $state(
    typeof localStorage !== "undefined" &&
      localStorage.getItem("pr-brief-collapsed") === "true",
  );
  function toggleCollapsed(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-brief-collapsed", String(collapsed));
    } catch {
      /* ignore */
    }
  }
  // Backwards-compatible alias for the existing template — `expanded`
  // is the inverse of `collapsed`, with the peek-flow `forceExpanded`
  // override folded in so a single derived covers every template branch.
  const expanded = $derived(!collapsed || forceExpanded);

  // Auto-expand when a new brief finishes so the reviewer can see the
  // result without clicking twice. We update the persisted collapsed
  // state so the auto-open survives reload as well. The first effect
  // run is treated as a seed — it records the initial status without
  // expanding so a page that loads with an already-done brief honors
  // the user's persisted collapsed preference.
  let lastSeenStatus = $state<string | null>(null);
  let effectSeeded = $state(false);
  $effect(() => {
    const s = brief?.status ?? null;
    if (!effectSeeded) {
      lastSeenStatus = s;
      effectSeeded = true;
      return;
    }
    if (s === "done" && lastSeenStatus !== "done" && collapsed) {
      collapsed = false;
      try {
        localStorage.setItem("pr-brief-collapsed", "false");
      } catch {
        /* ignore */
      }
    }
    lastSeenStatus = s;
  });

  const stale = $derived(briefStore.isStale(headSha));
  const inFlight = $derived(briefStore.isInFlight());

  // Kick off resolution of any bare filenames Claude mentioned so
  // they can be deep-linked once the server confirms the unique
  // path at brief.head_sha. The resolver caches per (owner, name,
  // sha) so this is a one-shot cost when the brief loads.
  $effect(() => {
    if (!brief?.content || !brief.head_sha) return;
    const refs = extractFileRefs(brief.content);
    if (refs.length === 0) return;
    void fileResolver.resolve(owner, name, brief.head_sha, refs);
  });

  // resolveBareFile reads from a $state-backed map; this $derived
  // tracks it, so renderMarkdown re-runs when resolutions arrive.
  const resolverVersion = $derived(
    brief?.head_sha ? fileResolver.getVersion(brief.head_sha) : 0,
  );

  const repoCtx = $derived({
    owner,
    name,
    sha: brief?.head_sha ?? "",
    resolveBareFile: (basename: string) =>
      brief?.head_sha
        ? fileResolver.lookup(brief.head_sha, basename)
        : undefined,
    cacheBust: String(resolverVersion),
  });

  async function generate(depth: "quick" | "deep"): Promise<void> {
    await briefStore.generate(depth);
  }

  async function regenerate(): Promise<void> {
    await briefStore.generate(brief?.depth === "deep" ? "deep" : "quick");
  }

  async function remove(): Promise<void> {
    await briefStore.remove();
  }

  // Parse the Markdown into known sections. The generator prompt
  // demands exact "## Subsystem / Layers / Intent / Before / After /
  // Mechanics / Commits / Risk surface / Open questions" headings;
  // anything outside those falls into "other" and renders raw at
  // the bottom. "observations" is kept as a back-compat bucket for
  // briefs generated against the old prompt.
  interface Sections {
    subsystem: string;
    layers: string;
    intent: string;
    before: string;
    after: string;
    mechanics: string;
    commits: string;
    risk: string;
    questions: string;
    observations: string; // legacy heading, pre-2026-04-29 briefs
    other: string;
  }

  function emptySections(): Sections {
    return {
      subsystem: "",
      layers: "",
      intent: "",
      before: "",
      after: "",
      mechanics: "",
      commits: "",
      risk: "",
      questions: "",
      observations: "",
      other: "",
    };
  }

  const sections = $derived.by<Sections>(() => {
    if (!brief || !brief.content) return emptySections();
    return splitSections(brief.content);
  });

  function splitSections(md: string): Sections {
    const out = emptySections();
    // Match "## Heading" at line start, capture heading + body until next "## " or EOF.
    const re = /(^|\n)##\s+([^\n]+)\n([\s\S]*?)(?=\n##\s+|$)/g;
    let m: RegExpExecArray | null;
    let matched = false;
    while ((m = re.exec(md)) !== null) {
      matched = true;
      const heading = m[2]!.trim().toLowerCase();
      const body = m[3]!.trim();
      switch (heading) {
        case "subsystem": out.subsystem = body; break;
        case "layers":
        case "stack":
        case "call path": out.layers = body; break;
        case "intent": out.intent = body; break;
        case "before": out.before = body; break;
        case "after": out.after = body; break;
        case "mechanics": out.mechanics = body; break;
        case "commits": out.commits = body; break;
        case "risk surface":
        case "risks": out.risk = body; break;
        case "open questions":
        case "questions": out.questions = body; break;
        case "observations": out.observations = body; break;
        default: out.other += (out.other ? "\n\n" : "") + `## ${m[2]!.trim()}\n\n${body}`;
      }
    }
    if (!matched) {
      // Fall back to dumping everything into Intent when Claude
      // ignored the structure.
      out.intent = md.trim();
    }
    return out;
  }
</script>

<div class="brief" class:brief--stale={stale}>
  <div class="brief__header">
    {#if brief}
      <button
        type="button"
        class="brief__toggle"
        onclick={toggleCollapsed}
        title={expanded ? "Collapse brief" : "Expand brief"}
        aria-expanded={expanded}
      >
        <svg
          class="brief__chevron"
          class:brief__chevron--open={expanded}
          width="10" height="10" viewBox="0 0 10 10" fill="none"
          stroke="currentColor" stroke-width="1.6"
        >
          <polyline points="3,2 7,5 3,8" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        <span class="brief__badge">Claude's brief</span>
        <span class="brief__meta">
          <span class="brief__sha" title="Brief anchored to this PR head">
            @ {brief.head_sha.slice(0, 7)}
          </span>
          <span class="brief__depth">{brief.depth}</span>
          {#if brief.created_at}
            <span class="brief__time">{timeAgo(brief.created_at)}</span>
          {/if}
          {#if inFlight}
            <span class="brief__status brief__status--running">
              <span class="brief__status-dot"></span>
              {brief.status === "queued" ? "queued" : "thinking"}
            </span>
          {:else if brief.status === "failed"}
            <span class="brief__status brief__status--failed">failed</span>
          {:else if stale}
            <span class="brief__status brief__status--stale" title="PR head has moved since this brief was generated — regenerate for a fresh take">
              stale
            </span>
          {/if}
        </span>
      </button>
      <div class="brief__actions">
        <button
          type="button"
          class="brief__btn"
          disabled={inFlight}
          onclick={() => void regenerate()}
          title="Regenerate against the current head"
        >
          {inFlight ? "..." : "Regenerate"}
        </button>
        <button
          type="button"
          class="brief__btn brief__btn--subtle"
          onclick={() => void remove()}
          title="Delete this brief"
        >
          &times;
        </button>
      </div>
    {:else}
      <span class="brief__badge">Claude's brief</span>
      <div class="brief__actions">
        <button
          type="button"
          class="brief__btn brief__btn--primary"
          disabled={inFlight || briefStore.isLoading()}
          onclick={() => void generate("quick")}
          title="Generate a quick structural brief (diff + commit log only)"
        >
          ✨ Generate brief
        </button>
        <button
          type="button"
          class="brief__btn"
          disabled={inFlight || briefStore.isLoading()}
          onclick={() => void generate("deep")}
          title="Deep mode — Claude explores the repo for context (slower, more thorough)"
        >
          Deep
        </button>
      </div>
    {/if}
  </div>

  {#if briefStore.getError()}
    <div class="brief__error">{briefStore.getError()}</div>
  {/if}

  {#if brief && brief.status === "failed" && brief.error}
    <div class="brief__error">{brief.error}</div>
  {/if}

  {#if brief && expanded && brief.content}
    <div class="brief__body">
      {#if sections.subsystem}
        <section class="brief__section">
          <h4 class="brief__section-title">Subsystem</h4>
          <div class="brief__section-body markdown-body">
            {@html renderMarkdown(sections.subsystem, repoCtx)}
          </div>
        </section>
      {/if}
      {#if sections.layers}
        <section class="brief__section">
          <h4 class="brief__section-title">Layers</h4>
          <div class="brief__section-body markdown-body">
            {@html renderMarkdown(sections.layers, repoCtx)}
          </div>
        </section>
      {/if}
      {#if sections.intent}
        <section class="brief__section">
          <h4 class="brief__section-title">Intent</h4>
          <div class="brief__section-body markdown-body">
            {@html renderMarkdown(sections.intent, repoCtx)}
          </div>
        </section>
      {/if}
      {#if sections.before || sections.after}
        <section class="brief__section brief__section--split">
          {#if sections.before}
            <div class="brief__half">
              <h4 class="brief__section-title brief__section-title--before">Before</h4>
              <div class="brief__section-body markdown-body">
                {@html renderMarkdown(sections.before, repoCtx)}
              </div>
            </div>
          {/if}
          {#if sections.after}
            <div class="brief__half">
              <h4 class="brief__section-title brief__section-title--after">After</h4>
              <div class="brief__section-body markdown-body">
                {@html renderMarkdown(sections.after, repoCtx)}
              </div>
            </div>
          {/if}
        </section>
      {/if}
      {#if sections.mechanics}
        <section class="brief__section">
          <h4 class="brief__section-title">Mechanics</h4>
          <div class="brief__section-body markdown-body">
            {@html renderMarkdown(sections.mechanics, repoCtx)}
          </div>
        </section>
      {/if}
      {#if sections.commits}
        <section class="brief__section">
          <h4 class="brief__section-title">Commits</h4>
          <div class="brief__section-body markdown-body">
            {@html renderMarkdown(sections.commits, repoCtx)}
          </div>
        </section>
      {/if}
      {#if sections.risk}
        <section class="brief__section">
          <h4 class="brief__section-title">Risk surface</h4>
          <div class="brief__section-body markdown-body">
            {@html renderMarkdown(sections.risk, repoCtx)}
          </div>
        </section>
      {/if}
      {#if sections.questions}
        <section class="brief__section">
          <h4 class="brief__section-title">Open questions</h4>
          <div class="brief__section-body markdown-body">
            {@html renderMarkdown(sections.questions, repoCtx)}
          </div>
        </section>
      {/if}
      {#if sections.observations}
        <section class="brief__section">
          <h4 class="brief__section-title">Observations</h4>
          <div class="brief__section-body markdown-body">
            {@html renderMarkdown(sections.observations, repoCtx)}
          </div>
        </section>
      {/if}
      {#if sections.other}
        <section class="brief__section">
          <div class="brief__section-body markdown-body">
            {@html renderMarkdown(sections.other, repoCtx)}
          </div>
        </section>
      {/if}
      <div class="brief__footer">
        Generated by Claude — treat as a reading aid, not a verdict. Cites may be wrong.
      </div>
    </div>
  {/if}
</div>

<style>
  .brief {
    margin: 0;
    padding: 8px 16px;
    background: color-mix(in srgb, var(--accent-claude) 4%, var(--bg-surface));
    border-bottom: 1px solid var(--diff-border);
  }

  .brief--stale {
    background: color-mix(in srgb, var(--accent-amber) 4%, var(--bg-surface));
  }

  .brief__header {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  /* The toggle grows to fill the row so clicking anywhere on the
     header (except the explicit action buttons) expands/collapses.
     Reset all button chrome so it reads as a flat clickable row. */
  .brief__toggle {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
    padding: 2px 4px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: inherit;
    border-radius: var(--radius-sm);
  }

  .brief__toggle:hover {
    background: var(--bg-surface-hover);
  }

  .brief__chevron {
    flex-shrink: 0;
    color: var(--text-muted);
    transition: transform 0.15s;
  }

  .brief__chevron--open {
    transform: rotate(90deg);
  }

  .brief__badge {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 2px 8px;
    border-radius: 999px;
    background: var(--accent-claude);
    color: #fff;
  }

  .brief__meta {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    color: var(--text-muted);
    flex-wrap: wrap;
  }

  .brief__sha {
    font-family: var(--font-mono);
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 999px;
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
  }

  .brief__depth {
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 999px;
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .brief__status {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 999px;
    text-transform: uppercase;
    font-weight: 600;
  }

  .brief__status--running {
    color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 14%, transparent);
  }

  .brief__status--failed {
    color: var(--accent-red);
    background: color-mix(in srgb, var(--accent-red) 14%, transparent);
  }

  .brief__status--stale {
    color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 14%, transparent);
    cursor: help;
  }

  .brief__status-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
    animation: brief-pulse 1.2s ease-in-out infinite;
  }

  @keyframes brief-pulse {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 1; }
  }

  .brief__actions {
    margin-left: auto;
    display: inline-flex;
    gap: 4px;
    flex-shrink: 0;
  }

  .brief__btn {
    font-size: 11px;
    padding: 3px 10px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-secondary);
    cursor: pointer;
  }

  .brief__btn:hover:not(:disabled) {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .brief__btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .brief__btn--primary {
    border-color: var(--accent-claude);
    background: var(--accent-claude);
    color: #fff;
  }

  .brief__btn--primary:hover:not(:disabled) {
    filter: brightness(1.1);
  }

  .brief__btn--subtle {
    color: var(--text-muted);
  }

  .brief__error {
    margin-top: 6px;
    padding: 6px 10px;
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-red) 8%, var(--bg-inset));
    color: var(--accent-red);
    font-size: 12px;
    white-space: pre-wrap;
  }

  .brief__body {
    margin-top: 10px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    /* The review surface puts us inside a flex column with
       overflow:hidden and DiffView takes the remaining space with
       flex:1. Without a cap here, a long brief pushes past the
       visible area and becomes unreachable. 50vh keeps the diff
       in view and lets the brief scroll internally. */
    max-height: 50vh;
    overflow-y: auto;
  }

  .brief__section {
    border: 1px dashed color-mix(in srgb, var(--accent-claude) 40%, transparent);
    border-radius: var(--radius-sm);
    padding: 8px 12px;
    background: var(--bg-surface);
  }

  .brief__section--split {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
    border: none;
    padding: 0;
    background: none;
  }

  .brief__half {
    border: 1px dashed color-mix(in srgb, var(--accent-claude) 40%, transparent);
    border-radius: var(--radius-sm);
    padding: 8px 12px;
    background: var(--bg-surface);
  }

  @media (max-width: 720px) {
    .brief__section--split {
      grid-template-columns: 1fr;
    }
  }

  .brief__section-title {
    margin: 0 0 6px 0;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
  }

  .brief__section-title--before {
    color: var(--accent-amber);
  }

  .brief__section-title--after {
    color: var(--accent-green);
  }

  .brief__section-body {
    font-size: 13px;
    color: var(--text-primary);
    line-height: 1.5;
  }

  /* Cap prose readability at ~80ch on the prose elements only.
     Code blocks, tables, and ASCII diagrams keep full width because
     wrapping them is worse than letting them be wide — alignment
     matters for diagrams and scanning matters for logs/code. */
  .brief__section-body :global(p),
  .brief__section-body :global(ul),
  .brief__section-body :global(ol),
  .brief__section-body :global(blockquote),
  .brief__section-body :global(h1),
  .brief__section-body :global(h2),
  .brief__section-body :global(h3),
  .brief__section-body :global(h4),
  .brief__section-body :global(h5),
  .brief__section-body :global(h6) {
    max-width: 80ch;
  }

  /* <pre> still scrolls horizontally for the rare case where a
     single fenced line exceeds the container; full-width is the
     starting point. */
  .brief__section-body :global(pre) {
    overflow-x: auto;
  }

  /* Inline code (file:line citations) should keep words together
     instead of breaking mid-path. */
  .brief__section-body :global(code) {
    word-break: keep-all;
    overflow-wrap: normal;
  }

  .brief__footer {
    font-size: 11px;
    color: var(--text-muted);
    text-align: right;
    font-style: italic;
  }
</style>
