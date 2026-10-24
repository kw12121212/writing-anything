/**
 * Host half of the dsh-writing-viewer plugin (dsh 0.2.x, cordis 4).
 *
 * Shape follows the published third-party plugins (@liustack/modsearch,
 * dshmarket): an ESM module exporting `name` and `apply(ctx, config)`, with
 * every service dependency acquired through scoped `ctx.inject` so the plugin
 * stays inert on profiles that never mount those services. The only service
 * seams used are:
 *
 * - `webServer` (web profile): registers two read-only GET routes, plus the
 *   review routes (session start/status/end and the same-origin proxy of the
 *   wa-cli reading view, see reviewroutes.ts) and the read-only copy route
 *   behind「复制图文」(wa-cli export --format paste-html --print, see
 *   copyroutes.ts).
 *   `/dsh-writing-viewer/workspace` returns the located project directory,
 *   whether `.writing-anything/` exists under it, and every candidate probed
 *   on the way. `/dsh-writing-viewer/snapshot` aggregates the whole workspace
 *   (config, works, unit trees, artifact full texts, decisions, sources,
 *   packs, validate diagnostics) read-only for the viewer client. An optional
 *   `?root=<absolute project path>` query parameter makes that path the only
 *   candidate for the one request (the panel's path switch).
 * - `workspaceRegistry` (DSH workspace registry): synchronous `list()` read
 *   of registered workspace paths, which are the GUI's own notion of project
 *   directories. The per-connection `workspaceController` remote is not
 *   injectable from a static plugin row — verified on 0.2.1-rc.2.
 *
 * The host performs no writes anywhere: the single filesystem operation in
 * this plugin is a `stat` on `<candidate>/.writing-anything`. The writing
 * workspace is only ever read here; the one write path is the review session,
 * where the wa-cli reading-view child writes what the author submits.
 */
import { lstat, readdir, readFile, stat } from 'node:fs/promises';
import { homedir, tmpdir } from 'node:os';
import { delimiter as pathDelimiter, isAbsolute, join, resolve } from 'node:path';
import { spawn } from 'node:child_process';
import {
  dedupeCandidates,
  locateWorkspace,
  normalizePathInput,
  parsePanelRoot,
  WORKSPACE_DIR,
  type CandidateProbe,
  type RootCandidate,
  type RootSource,
} from './root';
import { registryPaths, type WorkspaceRegistryLike } from './registry';
import {
  buildSnapshot,
  type SnapshotRouteBody,
} from './snapshot';
import { guardRequest, type GuardOptions } from './routeguard';
import type { CliRunner } from './wacli';
import type { DirEntry, SnapshotFs } from './artifacts';
import { parseSetupConfig, type SkillsServiceLike } from './setup';
import { realSetupFs } from './setupfs';
import { createSetupState, registerSetupRoutes, startupSetup, type SetupDeps, type SetupState } from './setuproutes';
import { createReviewSessions, type ReviewChild, type ReviewSessions } from './reviewsession';
import { registerReviewRoutes, type ReviewRootResolver } from './reviewroutes';
import type { ProxyFetch } from './reviewproxy';
import { registerCopyRoute } from './copyroutes';

// Re-exported so the live evidence harness can drive the exact provider the
// host registers against the real DSH skill registry (docs/evidence).
export { registerSetupSkills } from './setup';

export const name = 'dsh-writing-viewer';

/** No hard service requirements: both seams are acquired via scoped inject. */
export const inject: string[] = [];

/** Milliseconds before a wa-cli invocation counts as unavailable. */
const CLI_TIMEOUT_MS = 15_000;

/** Milliseconds a proxied reading-view request may wait on the upstream. */
const REVIEW_UPSTREAM_TIMEOUT_MS = 30_000;

/** Upper bound on captured wa-cli stdout; real envelopes are far smaller. */
const CLI_STDOUT_CAP = 8 * 1024 * 1024;

/** The copy route's wa-cli limits. wa-cli inlines up to 32 MiB of image
 * bytes (export.PasteEmbedBudget); as base64 that is about 42.7 MiB, and the
 * --print --json envelope carries the payload twice (export.html and
 * export.segments). JSON escaping grows it further: html/template writes "+"
 * as "&#43;", which encoding/json turns into "\u0026#43;" (10 bytes for 1,
 * one in 64 base64 characters, about +15 %). Worst case ≈ 2 × 42.7 × 1.15 ≈
 * 98 MiB plus markup, so the cap is 128 MiB — larger than the snapshot's, still
 * bounded. Re-encoding large images takes about a second each, hence the
 * longer timeout. */
export const COPY_STDOUT_CAP = 128 * 1024 * 1024;
export const COPY_TIMEOUT_MS = 120_000;

/**
 * Read-only filesystem adapter for the snapshot: listing and reading only.
 * Symlinks are never followed — a symlinked directory entry is not a real
 * subdirectory for the walker, and a symlinked artifact file reads as absent,
 * matching wa-cli's artifact.not-found treatment of linked artifacts.
 */
export function realSnapshotFs(): SnapshotFs {
  return {
    async readDir(path: string): Promise<DirEntry[] | null> {
      try {
        const info = await lstat(path);
        if (!info.isDirectory() || info.isSymbolicLink()) return null;
        const entries = await readdir(path, { withFileTypes: true });
        return entries.map((entry) => ({
          name: entry.name,
          isDirectory: entry.isDirectory(),
          isSymbolicLink: entry.isSymbolicLink(),
        }));
      } catch {
        return null;
      }
    },
    async readFile(path: string): Promise<string | null> {
      try {
        const info = await lstat(path);
        if (!info.isFile() || info.isSymbolicLink()) return null;
        return await readFile(path, 'utf8');
      } catch {
        return null;
      }
    },
  };
}

/**
 * Spawn adapter for the wa-cli bridge: argument list only, never a shell,
 * stdout captured, stderr ignored, one timeout. A missing binary (ENOENT),
 * a timeout, or any spawn failure yields null, which the snapshot records as
 * a degradation instead of failing the whole response. The optional cwd is
 * how the setup path keeps `wa-cli skills install` inside a workspace-free
 * temporary directory, so no audit line is ever appended to a workspace.
 */
export function spawnCliRunner(binary: string, limits: { timeoutMs?: number; stdoutCap?: number } = {}): CliRunner {
  const timeoutMs = limits.timeoutMs ?? CLI_TIMEOUT_MS;
  const stdoutCap = limits.stdoutCap ?? CLI_STDOUT_CAP;
  return {
    run(args: readonly string[], options?: { cwd?: string }): Promise<{ stdout: string } | null> {
      return new Promise((resolve) => {
        let child: ReturnType<typeof spawn>;
        try {
          child = spawn(binary, args, { cwd: options?.cwd, stdio: ['ignore', 'pipe', 'ignore'] });
        } catch {
          resolve(null);
          return;
        }
        // Raw bytes are collected and decoded once at the end, so a multi-byte
        // character split across two chunks is never garbled.
        const chunks: Buffer[] = [];
        let size = 0;
        let settled = false;
        const finish = (value: { stdout: string } | null): void => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          resolve(value);
        };
        const timer = setTimeout(() => {
          child.kill();
          finish(null);
        }, timeoutMs);
        child.stdout?.on('data', (chunk: Buffer) => {
          // Bounded accumulation: an output stream larger than any legitimate
          // envelope stops growing and the oversized result parses as a
          // degradation instead of exhausting memory.
          if (size >= stdoutCap) return;
          const kept = chunk.length > stdoutCap - size ? chunk.subarray(0, stdoutCap - size) : chunk;
          chunks.push(kept);
          size += kept.length;
        });
        child.on('error', () => finish(null));
        child.on('close', (code: number | null) => {
          // Exit code is not failure: status/validate legitimately exit 1 on
          // an invalid workspace while still printing a full JSON envelope.
          if (code === null) finish(null);
          else finish({ stdout: Buffer.concat(chunks, size).toString('utf8') });
        });
      });
    },
  };
}

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

/** Read-only probe: does `<root>/.writing-anything` exist as a directory?
 *
 * Like wa-cli's own workspace detection (os.Stat semantics), a symlinked
 * .writing-anything itself is followed here; everything beneath it is walked
 * with lstat and never follows symlinks. */
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

/** Tolerantly read the plugin's `allowedHosts` config into bare authorities. */
export function collectAllowedHosts(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const out: string[] = [];
  for (const entry of value) {
    if (typeof entry === 'string' && entry.trim() !== '') out.push(entry.trim());
  }
  return out;
}

/** Tolerantly read the listener's bind address off the injected webServer scope. */
export function bindHostOf(scope: unknown): string | null {
  const wrapper = (scope as { webServer?: unknown } | undefined) ?? undefined;
  const source = wrapper !== null && typeof wrapper === 'object' && 'webServer' in wrapper
    ? wrapper.webServer
    : scope;
  const host = (source as { host?: unknown } | undefined)?.host;
  return typeof host === 'string' && host.trim() !== '' ? host : null;
}

/** Route request shape the handlers need: method plus headers for the guard. */
interface RouteRequest {
  method?: string;
  headers?: Record<string, unknown>;
}

/** Route response shape: writeHead chains to end like node:http. */
interface RouteResponse {
  writeHead: (status: number, headers?: Record<string, string>) => { end: (body?: string) => void };
  end: (body?: string) => void;
}

/** Reject one request the origin guard refused: 403 JSON, never cached. */
function refuseOrigin(res: RouteResponse, reason: string): void {
  res
    .writeHead(403, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' })
    .end(JSON.stringify({ ok: false, error: reason }));
}

/** Register the read-only route on an acquired webServer scope. */
export function registerRoute(
  scope: { webServer?: { register: (route: unknown) => unknown } & Record<string, unknown> } & Record<string, unknown>,
  snapshot: () => Promise<WorkspaceRouteBody>,
  guardOptions: GuardOptions = { allowedHosts: [], bindHost: null },
): void {
  const server = (scope.webServer ?? scope) as { register: (route: unknown) => unknown };
  server.register({
    name: 'dsh-writing-viewer-workspace',
    kind: 'exact',
    path: '/dsh-writing-viewer/workspace',
    handler: async (req: RouteRequest, res: RouteResponse) => {
      if ((req.method ?? 'GET') !== 'GET') {
        res.writeHead(405, { allow: 'GET' }).end();
        return;
      }
      const verdict = guardRequest(req.headers ?? {}, guardOptions);
      if (!verdict.ok) {
        refuseOrigin(res, verdict.reason);
        return;
      }
      const body = await snapshot();
      res.writeHead(200, { 'content-type': 'application/json; charset=utf-8' }).end(JSON.stringify(body));
    },
  });
}

/** Read the snapshot route's optional `root` query parameter. */
export function rootQueryOf(url: string | undefined): string | null {
  try {
    return new URL(url ?? '/', 'http://x').searchParams.get('root');
  } catch {
    return null;
  }
}

/** Register the read-only snapshot route on an acquired webServer scope. */
export function registerSnapshotRoute(
  scope: { webServer?: { register: (route: unknown) => unknown } & Record<string, unknown> } & Record<string, unknown>,
  build: (query: { root: string | null }) => Promise<SnapshotRouteBody>,
  guardOptions: GuardOptions = { allowedHosts: [], bindHost: null },
): void {
  const server = (scope.webServer ?? scope) as { register: (route: unknown) => unknown };
  server.register({
    name: 'dsh-writing-viewer-snapshot',
    kind: 'exact',
    path: '/dsh-writing-viewer/snapshot',
    handler: async (req: RouteRequest & { url?: string }, res: RouteResponse) => {
      if ((req.method ?? 'GET') !== 'GET') {
        res.writeHead(405, { allow: 'GET' }).end();
        return;
      }
      const verdict = guardRequest(req.headers ?? {}, guardOptions);
      if (!verdict.ok) {
        refuseOrigin(res, verdict.reason);
        return;
      }
      try {
        const body = await build({ root: rootQueryOf(req.url) });
        res.writeHead(200, { 'content-type': 'application/json; charset=utf-8' }).end(JSON.stringify(body));
      } catch (error) {
        // The snapshot is best-effort per area; an unexpected failure still
        // answers readable JSON instead of an unhandled route error. Status
        // stays 200 on purpose: the panel contract is "always parse this
        // body", and `ok: false` plus the error text is the readable failure
        // shape — the DSH 0.2.x webServer exposes no documented error channel
        // a 500 would serve better than this body.
        res
          .writeHead(200, { 'content-type': 'application/json; charset=utf-8' })
          .end(JSON.stringify({ ok: false, error: String(error) } satisfies Partial<SnapshotRouteBody> & { error: string }));
      }
    },
  });
}

/** Locate a workspace once, shared by both routes of one snapshot call. */
async function locateOnce(
  registry: WorkspaceRegistryLike | null,
  config: { workspaceRoot?: unknown } | undefined,
  processCwd: string,
): Promise<{ candidates: CandidateProbe[]; located: { root: string; source: RootSource } | null }> {
  const summaries = registryPaths(registry);
  const candidates = collectCandidates(config, summaries.map((s) => s.path), processCwd);
  const probe = await locateWorkspace(candidates, workspaceExistsAt);
  if (probe.root !== null) return { candidates: probe.candidates, located: { root: probe.root, source: probe.source } };
  return { candidates: probe.candidates, located: null };
}

/**
 * Build the full snapshot route body from the real adapters.
 *
 * `panelRoot` is the panel's path switch: a valid absolute path becomes the
 * only candidate for this request, ahead of even the config pin, because it
 * is the author's most recent explicit choice. An invalid path, or one with
 * no workspace under it, answers the empty state with a readable `rootError`
 * instead of falling back to the default location — a silent fallback would
 * show another project while the author believes the switch worked.
 */
export async function fullSnapshot(
  registry: WorkspaceRegistryLike | null,
  config: { workspaceRoot?: unknown; waCliPath?: unknown } | undefined,
  processCwd: string,
  panelRoot: string | null = null,
): Promise<SnapshotRouteBody> {
  const fs = realSnapshotFs();
  const runner = spawnCliRunner(normalizePathInput(config?.waCliPath) ?? 'wa-cli');
  const requested = parsePanelRoot(panelRoot, { isAbsolute, resolve });
  if (requested.kind === 'invalid') {
    return { ...(await buildSnapshot(fs, runner, [], null)), rootError: requested.reason };
  }
  if (requested.kind === 'ok') {
    const probe = await locateWorkspace([{ source: 'panel-input', path: requested.path }], workspaceExistsAt);
    if (probe.root === null) {
      return {
        ...(await buildSnapshot(fs, runner, probe.candidates, null)),
        rootError: `该目录下没有 ${WORKSPACE_DIR}/ 工作区：${requested.path}`,
      };
    }
    return await buildSnapshot(fs, runner, probe.candidates, { root: probe.root, source: probe.source });
  }
  const { candidates, located } = await locateOnce(registry, config, processCwd);
  return await buildSnapshot(fs, runner, candidates, located);
}

/** Spawn adapter for the review session child: argument list only, never a
 * shell; stdout piped (the session drains it), stdin and stderr ignored. */
export function spawnReviewChild(binary: string, args: readonly string[]): ReviewChild {
  return spawn(binary, args, { stdio: ['ignore', 'pipe', 'ignore'] }) as unknown as ReviewChild;
}

/**
 * Where a review session starts: the panel's explicit root (validated like
 * the snapshot's `root` query, never falling back), else the default
 * location. A directory without a workspace is a readable refusal.
 */
export function reviewRootResolver(
  registry: () => WorkspaceRegistryLike | null,
  config: { workspaceRoot?: unknown } | undefined,
  processCwd: () => string,
): ReviewRootResolver {
  return async (root) => {
    const requested = parsePanelRoot(root, { isAbsolute, resolve });
    if (requested.kind === 'invalid') return { ok: false, error: requested.reason };
    if (requested.kind === 'ok') {
      return (await workspaceExistsAt(requested.path))
        ? { ok: true, root: requested.path }
        : { ok: false, error: `该目录下没有 ${WORKSPACE_DIR}/ 工作区：${requested.path}` };
    }
    const { located } = await locateOnce(registry(), config, processCwd());
    return located === null ? { ok: false, error: `没有找到 ${WORKSPACE_DIR}/ 工作区` } : { ok: true, root: located.root };
  };
}

/** Resolve the injected `skills` service off whatever scope shape the host
 * hands over (wrapper object or the service itself), tolerantly. */
function resolveSkillsService(scope: unknown): SkillsServiceLike | null {
  const service = (scope as { skills?: SkillsServiceLike } | SkillsServiceLike | undefined) ?? undefined;
  const resolved =
    service !== null && typeof service === 'object' && 'skills' in (service as object)
      ? (service as { skills?: SkillsServiceLike }).skills ?? null
      : (service as SkillsServiceLike | undefined) ?? null;
  return resolved !== null && typeof resolved.registerProvider === 'function' ? resolved : null;
}

export function apply(ctx: {
  inject?: (names: string[], closure: (scope: unknown) => void) => unknown;
  effect?: (execute: () => () => void, label?: string) => unknown;
}, config: {
  workspaceRoot?: unknown;
  waCliPath?: unknown;
  allowedHosts?: unknown;
  enableSetup?: unknown;
  waVersion?: unknown;
  cliInstallDir?: unknown;
  waBaseUrl?: unknown;
} = {}): void {
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
  // The setup capability exists only behind its config switch: with
  // enableSetup off there is no route, no skills injection, no PATH change
  // and no network access — the plugin behaves exactly as before.
  const setupOptions = parseSetupConfig(config, homedir());
  let setupDeps: SetupDeps | null = null;
  let setupState: SetupState | null = null;
  if (setupOptions.enabled) {
    setupState = createSetupState();
    let skills: SkillsServiceLike | null = null;
    setupDeps = {
      options: setupOptions,
      waCliPath: normalizePathInput(config?.waCliPath),
      fs: realSetupFs(),
      fetchLike: (url, init) => fetch(url, init),
      spawn: spawnCliRunner,
      skillsService: () => skills,
      env: process.env,
      pathSeparator: pathDelimiter,
      tempBase: tmpdir(),
      platform: process.platform,
      arch: process.arch,
      now: () => new Date().toISOString(),
      skillHomes: {
        dsh: process.env.DSH_HOME ?? `${homedir()}/.dsh`,
        agents: process.env.DSH_AGENTS_HOME ?? `${homedir()}/.agents`,
      },
    };
    // Once the host skills service is available, register from an existing
    // builtin binary — startup never downloads; the panel action does.
    ctx.inject(['skills'], (scope: unknown) => {
      const resolved = resolveSkillsService(scope);
      if (resolved !== null) {
        skills = resolved;
        void startupSetup(setupDeps as SetupDeps, setupState as SetupState);
      }
    });
  }
  // One review session per host; the plugin going away (or the host process
  // exiting) never leaves a reading-view child or its port behind.
  const reviewSessions: ReviewSessions = createReviewSessions({
    binary: normalizePathInput(config?.waCliPath) ?? 'wa-cli',
    spawn: spawnReviewChild,
    now: () => new Date().toISOString(),
  });
  // Teardown rides a cordis effect disposer: DSH 0.2.x's cordis 4 emits no
  // dispose event, so a listener registered for one would never run. The
  // disposer runs when the plugin fiber unloads (uninstall, reload, host stop).
  const endReviewOnExit = (): void => reviewSessions.dispose();
  process.once('exit', endReviewOnExit);
  try {
    ctx.effect?.(() => () => {
      process.removeListener('exit', endReviewOnExit);
      reviewSessions.dispose();
    }, 'dsh-writing-viewer: review sessions');
  } catch (error) {
    console.error(`[dsh-writing-viewer] review teardown not registered: ${String(error)}`);
  }
  ctx.inject(['webServer'], (scope: unknown) => {
    const guardOptions: GuardOptions = {
      allowedHosts: collectAllowedHosts(config.allowedHosts),
      bindHost: bindHostOf(scope),
    };
    try {
      registerRoute(scope as Parameters<typeof registerRoute>[0], () => workspaceSnapshot(registry, config, process.cwd()), guardOptions);
    } catch (error) {
      console.error(`[dsh-writing-viewer] route skipped: ${String(error)}`);
    }
    try {
      registerSnapshotRoute(scope as Parameters<typeof registerSnapshotRoute>[0], (query) => fullSnapshot(registry, config, process.cwd(), query.root), guardOptions);
    } catch (error) {
      console.error(`[dsh-writing-viewer] snapshot route skipped: ${String(error)}`);
    }
    try {
      const server = ((scope as { webServer?: unknown }).webServer ?? scope) as { register(route: unknown): unknown };
      registerReviewRoutes(server, {
        sessions: reviewSessions,
        resolveRoot: reviewRootResolver(() => registry, config, () => process.cwd()),
        // Bounded upstream wait: a hung reading view never pins a proxied request.
        fetchLike: ((url, init) => fetch(url, { ...(init as RequestInit), signal: AbortSignal.timeout(REVIEW_UPSTREAM_TIMEOUT_MS) })) as ProxyFetch,
        guardOptions,
      });
    } catch (error) {
      console.error(`[dsh-writing-viewer] review routes skipped: ${String(error)}`);
    }
    try {
      const server = ((scope as { webServer?: unknown }).webServer ?? scope) as { register(route: unknown): unknown };
      registerCopyRoute(server, {
        runner: spawnCliRunner(normalizePathInput(config?.waCliPath) ?? 'wa-cli', { timeoutMs: COPY_TIMEOUT_MS, stdoutCap: COPY_STDOUT_CAP }),
        resolveRoot: reviewRootResolver(() => registry, config, () => process.cwd()),
        guardOptions,
      });
    } catch (error) {
      console.error(`[dsh-writing-viewer] copy route skipped: ${String(error)}`);
    }
    if (setupDeps !== null && setupState !== null) {
      try {
        registerSetupRoutes(scope as Parameters<typeof registerSetupRoutes>[0], setupDeps, setupState, guardOptions);
      } catch (error) {
        console.error(`[dsh-writing-viewer] setup route skipped: ${String(error)}`);
      }
    }
  });
}
