// Persistence for explicit per-file collapse choices in the diff view.
//
// Format (localStorage "diff-collapse-overrides"):
//   Record<prKey, Record<path, boolean>>   // true = collapsed, false = expanded
//
// The legacy "diff-collapsed-files" key stored Record<prKey, string[]> of
// collapsed paths only. It is migrated on load (each path becomes true)
// and removed.

export type CollapseOverrides = Record<string, Record<string, boolean>>;

export const OVERRIDES_KEY = "diff-collapse-overrides";
export const LEGACY_COLLAPSED_KEY = "diff-collapsed-files";

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function readJSON(key: string): unknown {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as unknown) : undefined;
  } catch {
    return undefined;
  }
}

function parseOverrides(parsed: unknown): CollapseOverrides {
  const result: CollapseOverrides = {};
  if (!isRecord(parsed)) return result;
  for (const [prKey, value] of Object.entries(parsed)) {
    if (!isRecord(value)) continue;
    const entry: Record<string, boolean> = {};
    for (const [path, v] of Object.entries(value)) {
      if (typeof v === "boolean") entry[path] = v;
    }
    result[prKey] = entry;
  }
  return result;
}

export function loadCollapseOverrides(): CollapseOverrides {
  const result = parseOverrides(readJSON(OVERRIDES_KEY));
  const legacy = readJSON(LEGACY_COLLAPSED_KEY);
  if (legacy === undefined) return result;
  if (isRecord(legacy)) {
    for (const [prKey, value] of Object.entries(legacy)) {
      if (!Array.isArray(value)) continue;
      const entry = { ...result[prKey] };
      for (const path of value) {
        // A choice already recorded in the new format wins.
        if (typeof path === "string" && !(path in entry)) entry[path] = true;
      }
      result[prKey] = entry;
    }
  }
  saveCollapseOverrides(result);
  try {
    localStorage.removeItem(LEGACY_COLLAPSED_KEY);
  } catch {
    /* ignore */
  }
  return result;
}

export function saveCollapseOverrides(o: CollapseOverrides): void {
  try {
    localStorage.setItem(OVERRIDES_KEY, JSON.stringify(o));
  } catch {
    /* ignore */
  }
}
