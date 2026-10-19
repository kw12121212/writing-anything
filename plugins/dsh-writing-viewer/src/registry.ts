/**
 * Read project directories out of the DSH workspace registry.
 *
 * Verified against dsh 0.2.1-rc.2: the registry (`ctx.workspaceRegistry`,
 * owned by @deepseek-ai/dsh-workspace) is a root-scoped service whose
 * `list()` returns the workspace entities in durable registry order, each
 * carrying a canonical `path`. The higher-level `workspaceController`
 * (`follow()` baseline) rides the per-connection remotes API composition and
 * is not injectable from a static plugin row, so the scaffold reads the
 * registry directly. Row extraction stays duck-typed: a benign field change
 * in an rc host degrades to "no registry candidates" here, which the
 * resolution order falls through, instead of breaking the panel.
 */

/** The fields the viewer needs from a registry workspace. */
export interface WorkspaceSummary {
  id: string;
  path: string;
  title: string;
}

/** The subset of the host registry service the reader touches. */
export interface WorkspaceRegistryLike {
  list(): Array<{ id?: unknown; path: unknown; title?: unknown } | null>;
}

/** Extract workspace summaries from a registry, tolerantly. */
export function registryPaths(registry: WorkspaceRegistryLike | null | undefined): WorkspaceSummary[] {
  if (registry === null || registry === undefined || typeof registry.list !== 'function') return [];
  let rows: unknown;
  try {
    rows = registry.list();
  } catch {
    return [];
  }
  if (!Array.isArray(rows)) return [];
  const summaries: WorkspaceSummary[] = [];
  for (const row of rows) {
    if (row === null || typeof row !== 'object') continue;
    const entry = row as { id?: unknown; path?: unknown; title?: unknown };
    if (typeof entry.path !== 'string' || entry.path.trim() === '') continue;
    const path = entry.path;
    const id = typeof entry.id === 'string' && entry.id.trim() !== '' ? entry.id : path;
    const title = typeof entry.title === 'string' && entry.title.trim() !== '' ? entry.title : path;
    summaries.push({ id, path, title });
  }
  return summaries;
}
