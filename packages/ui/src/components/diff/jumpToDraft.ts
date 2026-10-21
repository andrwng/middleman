import { tick } from "svelte";
import type { DiffScope, DraftComment } from "../../stores/diff.svelte.js";

// The bits of diff state the jump needs. Structural so both the real
// store and component-test stubs satisfy it.
export interface DraftJumpStore {
  getScope: () => DiffScope;
  getCommits: () => { sha: string }[] | null;
  resetToHead: () => Promise<void>;
  selectCommit: (sha: string) => Promise<void>;
  getCurrentPR: () => { owner: string; name: string; number: number } | null;
  isFileCollapsed: (owner: string, name: string, number: number, path: string) => boolean;
  toggleFileCollapsed: (owner: string, name: string, number: number, path: string) => void;
}

// jumpToDraft brings a draft's anchor into view: switch scope to the
// commit the draft was anchored on (so line numbers resolve), expand
// the file if it's collapsed, then scroll to the line. Same shape as
// QuestionsSection.scrollToThread.
//
// If the draft's commit is the current PR head, switch to head scope
// rather than commit scope: commit scope at the head SHA would show
// head^..head, with different line numbers than the base..head diff
// the draft was anchored against.
export async function jumpToDraft(c: DraftComment, store: DraftJumpStore): Promise<void> {
  if (c.commitSha) {
    const scope = store.getScope();
    const commits = store.getCommits();
    const isCurrentHead = !!commits && commits.length > 0 && commits[0]!.sha === c.commitSha;
    const alreadyHeadHere = isCurrentHead && scope.kind === "head";
    const alreadyCommitHere = scope.kind === "commit" && scope.sha === c.commitSha;
    if (!alreadyHeadHere && !alreadyCommitHere) {
      if (isCurrentHead) {
        await store.resetToHead();
      } else {
        await store.selectCommit(c.commitSha);
      }
      // The switch awaits the diff fetch, but the new files may not
      // have rendered yet -- let them mount before querying.
      await tick();
    }
  }
  const pr = store.getCurrentPR();
  if (pr && store.isFileCollapsed(pr.owner, pr.name, pr.number, c.path)) {
    store.toggleFileCollapsed(pr.owner, pr.name, pr.number, c.path);
    await tick();
  }
  const path = CSS.escape(c.path);
  const el = document.querySelector<HTMLElement>(
    `.diff-file[data-file-path="${path}"] ` +
      `.line-wrap[data-anchor-line="${c.line}"]` +
      `[data-anchor-side="${c.side}"]`,
  );
  if (el) {
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    el.classList.add("line-wrap--flash");
    window.setTimeout(() => el.classList.remove("line-wrap--flash"), 1500);
    return;
  }
  const fileEl = document.querySelector<HTMLElement>(`.diff-file[data-file-path="${path}"]`);
  if (fileEl) {
    fileEl.scrollIntoView({ block: "start", behavior: "smooth" });
  }
}
