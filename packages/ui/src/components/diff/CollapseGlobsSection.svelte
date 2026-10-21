<script lang="ts" module>
  // Client-side mirror of the server's PUT validation, so bad input
  // gets an inline message before a round trip. The server stays the
  // authority: its errors are shown the same way.
  export const MAX_COLLAPSE_GLOBS = 200;
  export const MAX_COLLAPSE_GLOB_LEN = 512;

  export function validateNewGlob(existing: string[], raw: string): string | null {
    const glob = raw.trim();
    if (!glob) return "Enter a glob.";
    if (glob.length > MAX_COLLAPSE_GLOB_LEN) {
      return `Globs can be at most ${MAX_COLLAPSE_GLOB_LEN} characters.`;
    }
    if (existing.includes(glob)) return `${glob} is already in the list.`;
    if (existing.length >= MAX_COLLAPSE_GLOBS) {
      return `At most ${MAX_COLLAPSE_GLOBS} globs per repo.`;
    }
    return null;
  }
</script>

<script lang="ts">
  import { getStores } from "../../context.js";
  import { compileGlob, matchesAnyGlob } from "../../utils/diffCollapse.js";

  // Repo-wide collapse globs: files matching any of them start collapsed
  // (badge "pattern") in every PR and worktree of this repo.
  const { diff } = getStores();

  const globs = $derived(diff.getCollapseGlobs());

  // How many files in the current diff each glob matches (new path or,
  // for renames/copies/deletes, old path).
  const counts = $derived.by(() => {
    const files = diff.getFileList()?.files ?? [];
    return globs.map((g) => {
      const re = [compileGlob(g)];
      let n = 0;
      for (const f of files) if (matchesAnyGlob(re, f.path, f.old_path)) n++;
      return n;
    });
  });

  // null = follow the default (open iff there are globs) until the
  // reader toggles it.
  let openChoice = $state<boolean | null>(null);
  const open = $derived(openChoice ?? globs.length > 0);

  let draft = $state("");
  let error = $state<string | null>(null);
  let saving = $state(false);

  async function save(next: string[]): Promise<boolean> {
    saving = true;
    error = null;
    try {
      await diff.saveCollapseGlobs(next);
      return true;
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
      return false;
    } finally {
      saving = false;
    }
  }

  async function add(): Promise<void> {
    if (saving) return;
    const invalid = validateNewGlob(globs, draft);
    if (invalid) {
      error = invalid;
      return;
    }
    if (await save([...globs, draft.trim()])) draft = "";
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key !== "Enter") return;
    e.preventDefault();
    void add();
  }

  function remove(glob: string): void {
    if (saving) return;
    void save(globs.filter((g) => g !== glob));
  }
</script>

<div class="globs">
  <button
    type="button"
    class="globs__toggle"
    aria-expanded={open}
    onclick={() => { openChoice = !open; }}
  >
    <span class="globs__chevron" class:globs__chevron--open={open}>&#8250;</span>
    <span class="globs__label">Collapse globs</span>
    <span class="globs__total">{globs.length}</span>
  </button>

  {#if open}
    <div class="globs__body">
      {#if globs.length > 0}
        <ul class="globs__list">
          {#each globs as g, i (g)}
            <li class="globs__item">
              <span class="globs__glob" title={g}>{g}</span>
              <span class="globs__count">{counts[i]} {counts[i] === 1 ? "file" : "files"}</span>
              <button
                type="button"
                class="globs__remove"
                aria-label="Remove collapse glob {g}"
                title="Remove"
                disabled={saving}
                onclick={() => remove(g)}
              >
                <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6">
                  <path d="M2 2L8 8M8 2L2 8" stroke-linecap="round" />
                </svg>
              </button>
            </li>
          {/each}
        </ul>
      {/if}
      <input
        type="text"
        class="globs__input"
        class:globs__input--invalid={error !== null}
        placeholder="Add glob, e.g. **/*.pb.go"
        aria-label="Add collapse glob"
        aria-invalid={error !== null}
        aria-describedby={error !== null ? "collapse-globs-error" : undefined}
        bind:value={draft}
        oninput={() => { error = null; }}
        onkeydown={onKeydown}
      />
      {#if error !== null}
        <p class="globs__error" id="collapse-globs-error" role="alert">{error}</p>
      {/if}
    </div>
  {/if}
</div>

<style>
  .globs {
    border-bottom: 1px solid var(--border-muted);
    padding: 2px 0;
  }

  .globs__toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 4px 10px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: var(--text-primary);
  }

  .globs__toggle:hover {
    background: var(--bg-surface-hover);
  }

  .globs__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 12px;
    height: 12px;
    font-size: 12px;
    color: var(--text-muted);
    transition: transform 0.15s;
  }

  .globs__chevron--open {
    transform: rotate(90deg);
  }

  .globs__label {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
  }

  .globs__total {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    background: var(--diff-bg);
    border: 1px solid var(--diff-border);
    border-radius: 999px;
    padding: 1px 6px;
  }

  .globs__body {
    padding: 2px 10px 6px 24px;
  }

  .globs__list {
    list-style: none;
    margin: 0 0 4px;
    padding: 0;
    max-height: 30vh;
    overflow-y: auto;
  }

  .globs__item {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 1px 0;
  }

  .globs__glob {
    flex: 1;
    min-width: 0;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .globs__count {
    flex-shrink: 0;
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
  }

  .globs__remove {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border: none;
    border-radius: var(--radius-sm);
    background: none;
    color: var(--text-muted);
    cursor: pointer;
  }

  .globs__remove:hover:not(:disabled),
  .globs__remove:focus-visible {
    background: var(--bg-surface-hover);
    color: var(--accent-red);
  }

  .globs__remove:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .globs__input {
    width: 100%;
    font-size: 11px;
    font-family: var(--font-mono);
    padding: 3px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-primary);
  }

  .globs__input:focus {
    border-color: var(--accent-blue);
    outline: none;
  }

  .globs__input--invalid,
  .globs__input--invalid:focus {
    border-color: var(--accent-red);
  }

  .globs__error {
    margin: 3px 0 0;
    font-size: 11px;
    color: var(--accent-red);
  }
</style>
