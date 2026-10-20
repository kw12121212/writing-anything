/**
 * Pure workspace-location logic for the writing-viewer host half.
 *
 * Everything here is free of node:fs so unit tests can drive it with an
 * injected probe; the host entry supplies the real `stat`. Resolution order
 * is fixed and recorded in the panel output: an explicit config override
 * first, then DSH workspace registry paths in registry order, then the host
 * process working directory as the last fallback. The first candidate whose
 * `.writing-anything/` directory exists wins; when none does the caller gets
 * a missing result with every probe recorded, which the panel renders as the
 * empty-state guidance (run `wa-cli init`) instead of an error.
 */

/** Where a candidate project directory came from. `panel-input` is a path
 * the author typed (or picked) in the panel for one snapshot request. */
export type RootSource = 'config' | 'workspace-registry' | 'process-cwd' | 'panel-input';

/** One directory that might contain a writing-anything workspace. */
export interface RootCandidate {
  source: RootSource;
  path: string;
}

/** A candidate together with the read-only probe result for it. */
export interface CandidateProbe extends RootCandidate {
  workspaceExists: boolean;
}

/** A project directory whose `.writing-anything/` exists. */
export interface LocatedWorkspace {
  root: string;
  source: RootSource;
  workspaceDir: string;
  workspaceExists: true;
  candidates: CandidateProbe[];
}

/** No candidate had a `.writing-anything/` directory. */
export interface MissingWorkspace {
  root: null;
  workspaceExists: false;
  candidates: CandidateProbe[];
}

export type WorkspaceProbe = LocatedWorkspace | MissingWorkspace;

/** The workspace directory name inside a project directory. */
export const WORKSPACE_DIR = '.writing-anything';

/**
 * Normalize a caller-supplied root (the plugin config field): only non-blank
 * strings survive, trimmed; anything else means "not configured" so the
 * resolution falls through to the registry.
 */
export function normalizePathInput(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

/** Longest project path the panel may request; real paths are far shorter. */
export const PANEL_ROOT_MAX_LENGTH = 4096;

/** Outcome of reading the panel's `root` query parameter. */
export type PanelRoot =
  | { kind: 'absent' }
  | { kind: 'ok'; path: string }
  | { kind: 'invalid'; reason: string };

/**
 * Validate the project path the panel asked for. Only an absolute path is
 * accepted: a relative one would drift with the host's working directory,
 * and an absolute one can never begin with `-`, so it cannot be mistaken for
 * a flag when handed to wa-cli as `--dir <root>`. A leading pair of slashes
 * or backslashes is refused on every platform: on Windows it is a UNC or
 * device path, whose mere `stat` reaches out over the network, and no local
 * project needs it. The path API is injected
 * (node:path in the host) to keep this module free of node imports.
 */
export function parsePanelRoot(
  value: string | null | undefined,
  pathApi: { isAbsolute(path: string): boolean; resolve(path: string): string },
): PanelRoot {
  if (value === null || value === undefined) return { kind: 'absent' };
  const trimmed = value.trim();
  if (trimmed === '') return { kind: 'absent' };
  if (trimmed.includes('\u0000')) return { kind: 'invalid', reason: '项目路径含有非法字符（NUL）' };
  if (trimmed.length > PANEL_ROOT_MAX_LENGTH) return { kind: 'invalid', reason: `项目路径超过 ${PANEL_ROOT_MAX_LENGTH} 个字符` };
  if (/^[\\/]{2}/.test(trimmed)) return { kind: 'invalid', reason: `项目路径不能是网络或设备路径：${trimmed}` };
  if (!pathApi.isAbsolute(trimmed)) return { kind: 'invalid', reason: `项目路径必须是绝对路径：${trimmed}` };
  return { kind: 'ok', path: pathApi.resolve(trimmed) };
}

/** Workspace dir path for a project root, joined without node:path so the
 * client bundle can reuse the shape in display strings. */
export function workspaceDirOf(root: string): string {
  return root.replace(/\/+$/, '') + '/' + WORKSPACE_DIR;
}

/**
 * Drop blank paths and duplicate paths, keeping each path's first (highest
 * priority) source. The registry and the config override can name the same
 * directory; the winner must be whichever source ranked earlier, and the
 * panel's candidate list must not show a path twice.
 */
export function dedupeCandidates(candidates: readonly RootCandidate[]): RootCandidate[] {
  const seen = new Set<string>();
  const out: RootCandidate[] = [];
  for (const candidate of candidates) {
    if (typeof candidate.path !== 'string' || candidate.path.trim() === '') continue;
    if (seen.has(candidate.path)) continue;
    seen.add(candidate.path);
    out.push(candidate);
  }
  return out;
}

/**
 * Probe candidates in order and stop at the first `.writing-anything/`
 * directory. A probe that rejects counts as "no workspace there" — a
 * permission problem or a broken symlink is an absent workspace for viewing
 * purposes, never a panel failure.
 */
export async function locateWorkspace(
  candidates: readonly RootCandidate[],
  workspaceExists: (root: string) => Promise<boolean>,
): Promise<WorkspaceProbe> {
  const probes: CandidateProbe[] = [];
  for (const candidate of dedupeCandidates(candidates)) {
    let exists = false;
    try {
      exists = await workspaceExists(candidate.path);
    } catch {
      exists = false;
    }
    probes.push({ source: candidate.source, path: candidate.path, workspaceExists: exists });
    if (exists) {
      return {
        root: candidate.path,
        source: candidate.source,
        workspaceDir: workspaceDirOf(candidate.path),
        workspaceExists: true,
        candidates: probes,
      };
    }
  }
  return { root: null, workspaceExists: false, candidates: probes };
}
