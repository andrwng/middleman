// Cross-component UI state for PR detail surfaces.
// Centralizes the "review nav is collapsed" boolean so both DiffSidebar
// (the inner content) and PullDetail (the wrapper width) react from the
// same $state source — no event dispatching, no $effect indirection.
//
// localStorage is the canonical persistence layer; this module mirrors
// it as a reactive $state at first read and on every write.

const KEY = "pr-review-nav-collapsed";

function loadInitial(): boolean {
  try {
    return localStorage.getItem(KEY) === "true";
  } catch {
    return false;
  }
}

let reviewNavCollapsed = $state<boolean>(loadInitial());

export function isReviewNavCollapsed(): boolean {
  return reviewNavCollapsed;
}

export function setReviewNavCollapsed(value: boolean): void {
  reviewNavCollapsed = value;
  try {
    localStorage.setItem(KEY, String(value));
  } catch {
    // storage blocked; in-memory state still updates
  }
}

export function toggleReviewNavCollapsed(): void {
  setReviewNavCollapsed(!reviewNavCollapsed);
}
