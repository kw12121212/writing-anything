/**
 * Host half of the dsh-writing-viewer plugin (dsh 0.2.x, cordis 4).
 *
 * Shape follows the published third-party plugins (@liustack/modsearch,
 * dshmarket): an ESM module exporting `name` and `apply(ctx, config)`, with
 * every service dependency acquired through scoped `ctx.inject` so the plugin
 * stays inert on profiles that never mount those services. The only service
 * seams used are:
 *
 * - `webServer` (web profile): register one read-only GET route
 *   `/dsh-writing-viewer/workspace` that returns the located project
 *   directory, whether `.writing-anything/` exists under it, and every
 *   candidate probed on the way.
 * - `workspaceRegistry` (DSH workspace registry): synchronous `list()` read
 *   of registered workspace paths, which are the GUI's own notion of project
 *   directories. The per-connection `workspaceController` remote is not
 *   injectable from a static plugin row — verified on 0.2.1-rc.2.
 *
 * The host performs no writes anywhere: the single filesystem operation in
 * this plugin is a `stat` on `<candidate>/.writing-anything`. The writing
 * workspace is only ever read; the author keeps editing through wa-cli.
 */
import { stat } from 'node:fs/promises';
import { join } from 'node:path';
import {
  dedupeCandidates,
  locateWorkspace,
  normalizePathInput,
  WORKSPACE_DIR,
  type CandidateProbe,
  type RootCandidate,
  type RootSource,
} from './root';
import { registryPaths, type WorkspaceRegistryLike } from './registry';

export const name = 'dsh-writing-viewer';

/** No hard service requirements: both seams are acquired via scoped inject. */
export const inject: string[] = [];

/** The JSON body the route serves. Fields are additive-only from here on. */
export interface WorkspaceRouteBody {
  ok: boolean;
  root: string | null;
  source: RootSource | null;
  workspaceDir: string | null;
  workspaceExists: boolean;
  candidates: CandidateProbe[];
  emptyState: { command: string } | null;
}

/** Read-only probe: does `<root>/.writing-anything` exist as a directory? */
export async function workspaceExistsAt(root: string): Promise<boolean> {
  try {
    const info = await stat(join(root, WORKSPACE_DIR));
    return info.isDirectory();
  } catch {
    return false;
  }
}

/**
 * Collect resolution candidates. An explicit `workspaceRoot` config is an
 * exclusive pin: when set, it is the only candidate, so a pinned directory
 * without a workspace shows the empty state instead of silently falling
 * through to some other project. Without it, the candidates are the DSH
 * workspace registry paths in registry order, then the host process working
 * directory.
 */
export function collectCandidates(
  config: { workspaceRoot?: unknown } | undefined,
  registryPaths: readonly string[],
  processCwd: string,
): RootCandidate[] {
  const configured = normalizePathInput(config?.workspaceRoot);
  if (configured !== null) return [{ source: 'config', path: configured }];
  const candidates: RootCandidate[] = [];
  for (const path of registryPaths) {
    const normalized = normalizePathInput(path);
    if (normalized !== null) candidates.push({ source: 'workspace-registry', path: normalized });
  }
  const cwd = normalizePathInput(processCwd);
  if (cwd !== null) candidates.push({ source: 'process-cwd', path: cwd });
  return dedupeCandidates(candidates);
}

/** Build the route body from a registry (or null) and the plugin config. */
export async function workspaceSnapshot(
  registry: WorkspaceRegistryLike | null,
  config: { workspaceRoot?: unknown } | undefined,
  processCwd: string,
): Promise<WorkspaceRouteBody> {
  const summaries = registryPaths(registry);
  const candidates = collectCandidates(config, summaries.map((s) => s.path), processCwd);
  const probe = await locateWorkspace(candidates, workspaceExistsAt);
  if (probe.root !== null) {
    return {
      ok: true,
      root: probe.root,
      source: probe.source,
      workspaceDir: probe.workspaceDir,
      workspaceExists: true,
      candidates: probe.candidates,
      emptyState: null,
    };
  }
  return {
    ok: true,
    root: null,
    source: null,
    workspaceDir: null,
    workspaceExists: false,
    candidates: probe.candidates,
    emptyState: { command: 'wa-cli init' },
  };
}

/** Register the read-only route on an acquired webServer scope. */
export function registerRoute(
  scope: { webServer?: { register: (route: unknown) => unknown } & Record<string, unknown> } & Record<string, unknown>,
  snapshot: () => Promise<WorkspaceRouteBody>,
): void {
  const server = (scope.webServer ?? scope) as { register: (route: unknown) => unknown };
  server.register({
    name: 'dsh-writing-viewer-workspace',
    kind: 'exact',
    path: '/dsh-writing-viewer/workspace',
    handler: async (req: { method?: string }, res: {
      writeHead: (status: number, headers?: Record<string, string>) => { end: (body?: string) => void };
      end: (body?: string) => void;
    }) => {
      if ((req.method ?? 'GET') !== 'GET') {
        res.writeHead(405, { allow: 'GET' }).end();
        return;
      }
      const body = await snapshot();
      res.writeHead(200, { 'content-type': 'application/json; charset=utf-8' }).end(JSON.stringify(body));
    },
  });
}

export function apply(ctx: {
  inject?: (names: string[], closure: (scope: unknown) => void) => unknown;
}, config: { workspaceRoot?: unknown } = {}): void {
  if (typeof ctx?.inject !== 'function') return;
  let registry: WorkspaceRegistryLike | null = null;
  ctx.inject(['workspaceRegistry'], (scope: unknown) => {
    const service = (scope as { workspaceRegistry?: WorkspaceRegistryLike } | WorkspaceRegistryLike | undefined) ?? undefined;
    const resolved =
      service !== null && typeof service === 'object' && 'workspaceRegistry' in (service as object)
        ? (service as { workspaceRegistry?: WorkspaceRegistryLike }).workspaceRegistry ?? null
        : (service as WorkspaceRegistryLike | undefined) ?? null;
    if (resolved !== null && typeof resolved.list === 'function') registry = resolved;
  });
  ctx.inject(['webServer'], (scope: unknown) => {
    try {
      registerRoute(scope as Parameters<typeof registerRoute>[0], () => workspaceSnapshot(registry, config, process.cwd()));
    } catch (error) {
      console.error(`[dsh-writing-viewer] route skipped: ${String(error)}`);
    }
  });
}
