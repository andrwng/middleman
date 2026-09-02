<script lang="ts">
  import {
    clampSectionHeight,
    clearSectionHeight,
    persistSectionHeight,
    setSectionHeight,
    type SectionId,
  } from "./sectionHeights.svelte.js";

  // The draggable boundary at the bottom of a sidebar section. Dragging it
  // sizes the section above; whatever sits below slides, and the file list at
  // the foot of the column absorbs the difference. See sectionHeights for why
  // the space comes from there.

  interface Props {
    id: SectionId;
    // The section body being sized. The parent binds it, so it is null on the
    // first render pass and populated well before a pointer can reach us.
    body: HTMLElement | null | undefined;
    label: string;
  }
  const { id, body, label }: Props = $props();

  let dragging = false;
  let startY = 0;
  let startHeight = 0;
  let columnHeight = 0;

  function onPointerDown(e: PointerEvent): void {
    if (!body) return;
    dragging = true;
    startY = e.clientY;
    startHeight = body.getBoundingClientRect().height;
    // The column bounds how tall the section may get. Measured once per drag:
    // re-reading it per move would cost a layout on every pixel.
    const column = body.closest(".review-sidebar");
    columnHeight = column
      ? column.getBoundingClientRect().height
      : window.innerHeight;
    (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
    e.preventDefault();
  }

  function onPointerMove(e: PointerEvent): void {
    if (!dragging) return;
    const desired = startHeight + (e.clientY - startY);
    setSectionHeight(id, clampSectionHeight(desired, columnHeight));
  }

  function onPointerUp(e: PointerEvent): void {
    if (!dragging) return;
    dragging = false;
    (e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId);
    // A drag fires a move per pixel; only the release is worth storing.
    persistSectionHeight(id);
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="section-resize"
  role="separator"
  aria-orientation="horizontal"
  aria-label={label}
  title={`${label} — drag to resize, double-click to reset`}
  data-section-resize={id}
  onpointerdown={onPointerDown}
  onpointermove={onPointerMove}
  onpointerup={onPointerUp}
  onpointercancel={onPointerUp}
  ondblclick={() => clearSectionHeight(id)}
></div>

<style>
  .section-resize {
    height: 6px;
    cursor: row-resize;
    background: transparent;
  }

  .section-resize:hover {
    background: var(--accent-blue);
    opacity: 0.4;
  }
</style>
