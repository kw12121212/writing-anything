/**
 * Setup route and action pipeline for the「内置技能与 CLI」capability.
 *
 * One origin-guarded route is registered only while `enableSetup` is on:
 * `GET /dsh-writing-viewer/setup` answers the panel's status section — CLI
 * readiness (path, version), the skills registered from the fallback
 * provider, what a later host shell resolves `wa-cli` to (external first,
 * builtin fallback), and the external binary currently winning (when one
 * exists). `POST` on the same path runs the explicit enable action: ensure
 * the builtin CLI (download + verify + atomic install when missing or
 * version-pinned differently), materialize the builtin skills through a
 * workspace-free temp directory, register them as the high-rank fallback
 * provider, and append the builtin binary's directory to the END of the
 * host PATH so existing order keeps winning. One action runs at a time; a
 * POST during an action just reports the busy status. Any other method is
 * a 405.
 *
 * The same pipeline (minus any download) also runs once at host start when
 * the builtin binary already exists, so a restart re-registers the skills
 * without the author clicking again — still no network access at startup.
 */

import {
  CLI_BINARY_NAME,
  SetupError,
  downloadRelease,
  externalSkillRoots,
  findExternalSkill,
  installBinary,
  materializeSkills,
  parseVersionOutput,
  appendPathValue,
  provenanceMarker,
  parseProvenanceMarker,
  provenanceMarkerPath,
  registerSetupSkills,
  releaseAssetName,
  resolveAllOnPath,
  sha256HexOf,
  type FetchLike,
  type ParsedSkill,
  type SetupFs,
  type SetupOptions,
  type SetupRunner,
  type SkillsServiceLike,
} from './setup';
import { guardRequest, type GuardOptions } from './routeguard';
import { isAbsolute, resolve as resolvePath } from 'node:path';
import { parsePanelRoot } from './root';

/** Mutable setup state shared by the routes, the startup path and the panel actions. */
export interface SetupState {
  busy: boolean;
  lastError: string | null;
  skillNames: string[];
  registered: boolean;
  disposeProvider: (() => void) | null;
}

export function createSetupState(): SetupState {
  return { busy: false, lastError: null, skillNames: [], registered: false, disposeProvider: null };
}

/** Everything the pipeline needs, injectable for tests. */
export interface SetupDeps {
  options: SetupOptions;
  /** Plugin `waCliPath` override; when set it is used as-is and never downloaded. */
  waCliPath: string | null;
  fs: SetupFs;
  fetchLike: FetchLike;
  /** Spawn factory for one binary (argument list only, never a shell). */
  spawn(binary: string): SetupRunner;
  /** The host skills service when injected, null until then. */
  skillsService(): SkillsServiceLike | null;
  /** Environment PATH holder; the real one is process.env. */
  env: { PATH?: string };
  pathSeparator: string;
  tempBase: string;
  platform: string;
  arch: string;
  /** ISO timestamp for provenance markers; injected for tests. */
  now(): string;
  /** User-level external skill directories, honoring the same environment
   * overrides as the host's skill filesystem source (DSH_HOME / DSH_AGENTS_HOME). */
  skillHomes: { dsh: string; agents: string };
}

/** The status route body. Fields are additive-only from here on. */
export interface SetupStatusBody {
  ok: true;
  enabled: true;
  busy: boolean;
  cli: {
    ready: boolean;
    path: string | null;
    version: string | null;
    resolved: string | null;
    external: string | null;
    /** True only when the plugin's own provenance marker is present next to
     * the binary AND its recorded sha256 matches the bytes on disk. */
    pluginInstalled: boolean;
  };
  skills: {
    registered: boolean;
    names: string[];
    /** Per-name source report from the standard-directory presence probe;
     * null before registration. */
    report: SkillSourceReport[] | null;
  };
  lastError: string | null;
}

/** One row of the skill source report. */
export interface SkillSourceReport {
  name: string;
  winner: 'builtin' | 'external';
  /** Source label of the winner (provider name for builtin). */
  source: string;
  /** SKILL.md path of the winning external copy, when it has one. */
  path: string | null;
  /** The builtin registration exists for this name (fallback ready). */
  fallbackReady: boolean;
}

/** The builtin binary path the setup manages (or the configured override). */
export function builtinCliPath(deps: SetupDeps): string {
  return deps.waCliPath ?? `${deps.options.cliInstallDir}/${CLI_BINARY_NAME}`;
}

async function queryVersion(deps: SetupDeps, binary: string): Promise<string | null> {
  const result = await deps.spawn(binary).run(['version', '--json'], { cwd: deps.tempBase });
  return result === null ? null : parseVersionOutput(result.stdout);
}

/** Whether the binary at `path` was installed by this plugin: the sidecar
 * marker must exist and its recorded sha256 must match the on-disk bytes —
 * a marker left behind by a manually replaced binary does not count. */
export async function isPluginInstalled(fs: SetupFs, path: string): Promise<boolean> {
  const text = await fs.readFile(provenanceMarkerPath(path));
  if (text === null) return false;
  const marker = parseProvenanceMarker(text);
  if (marker === null) return false;
  const bytes = await fs.readFileBytes(path);
  if (bytes === null) return false;
  return marker.sha256 === sha256HexOf(bytes);
}

/** Build the per-name source report by probing the standard external skill
 * directories in the filesystem source's own precedence order — under
 * external-first semantics an existing external copy wins over the builtin
 * registration, so presence decides. The preset-layer registry scope the
 * sessions see is not reachable from a static host plugin (the enabled
 * dsh-skill-filesystem lives in the agent preset layer on web profiles), so
 * this is a directory-presence comparison; custom skill dirs and other
 * providers are out of its scope by design. Null before registration. */
function skillSourceReport(
  deps: SetupDeps,
  state: SetupState,
  projectRoot: string | null,
): { name: string; resolve: Promise<{ source: string; path: string } | null> }[] | null {
  if (!state.registered) return null;
  const roots = externalSkillRoots(projectRoot, deps.skillHomes);
  return state.skillNames.map((name) => ({ name, resolve: findExternalSkill(deps.fs, roots, name) }));
}

/** Materialize the report rows (probing is async). */
async function resolveSkillSourceReport(deps: SetupDeps, state: SetupState, projectRoot: string | null): Promise<SkillSourceReport[] | null> {
  const pending = skillSourceReport(deps, state, projectRoot);
  if (pending === null) return null;
  const rows: SkillSourceReport[] = [];
  for (const item of pending) {
    const external = await item.resolve;
    rows.push(
      external === null
        ? { name: item.name, winner: 'builtin', source: 'dsh-writing-viewer', path: null, fallbackReady: true }
        : { name: item.name, winner: 'external', source: external.source, path: external.path, fallbackReady: true },
    );
  }
  return rows;
}

/** Assemble the status body from live probes; never throws. */
export async function buildSetupStatus(deps: SetupDeps, state: SetupState, projectRoot: string | null = null): Promise<SetupStatusBody> {
  const path = builtinCliPath(deps);
  const info = await deps.fs.stat(path);
  const ready = info !== null && info.isFile;
  let version: string | null = null;
  let pluginInstalled = false;
  if (ready && !state.busy) {
    version = await queryVersion(deps, path);
    pluginInstalled = await isPluginInstalled(deps.fs, path);
  }
  const hits = await resolveAllOnPath(deps.fs, deps.env.PATH, CLI_BINARY_NAME, deps.pathSeparator);
  const resolved = hits[0] ?? null;
  const external = hits.find((hit) => hit !== path) ?? null;
  return {
    ok: true,
    enabled: true,
    busy: state.busy,
    cli: { ready, path: ready ? path : null, version, resolved, external, pluginInstalled },
    skills: { registered: state.registered, names: [...state.skillNames], report: await resolveSkillSourceReport(deps, state, projectRoot) },
    lastError: state.lastError,
  };
}

/** Make sure the builtin CLI exists (and matches a pinned version).
 * Returns the binary path; downloads only when it must. */
async function ensureCli(deps: SetupDeps): Promise<string> {
  if (deps.waCliPath !== null) {
    const info = await deps.fs.stat(deps.waCliPath);
    if (info === null || !info.isFile) {
      throw new SetupError(`配置的 waCliPath 不存在或不是文件：${deps.waCliPath}`);
    }
    return deps.waCliPath;
  }
  const dest = `${deps.options.cliInstallDir}/${CLI_BINARY_NAME}`;
  const existing = await deps.fs.stat(dest);
  if (existing !== null && existing.isFile) {
    if (deps.options.waVersion === 'latest') return dest;
    const version = await queryVersion(deps, dest);
    if (version === deps.options.waVersion) return dest;
    // A pinned version that differs re-downloads (explicit action only).
  } else if (existing !== null) {
    throw new SetupError(`目标路径已被非文件占用：${dest}`);
  }
  const asset = releaseAssetName(deps.platform, deps.arch);
  if (asset === null) {
    throw new SetupError(`当前平台没有对应的发布产物（Release Asset）：${deps.platform}/${deps.arch}（支持 linux、darwin × amd64、arm64）`);
  }
  const bytes = await downloadRelease(deps.fetchLike, {
    baseUrl: deps.options.waBaseUrl,
    version: deps.options.waVersion,
    asset,
  });
  const installed = await installBinary(deps.fs, deps.options.cliInstallDir, bytes);
  // Best-effort provenance marker: failing to mark must not fail a working
  // install — the status then honestly classifies it as not-plugin-installed.
  try {
    const marker = JSON.stringify(provenanceMarker(bytes, deps.now()));
    await deps.fs.writeFile(provenanceMarkerPath(installed), new TextEncoder().encode(marker));
  } catch (error) {
    console.error(`[dsh-writing-viewer] provenance marker not written: ${String((error as Error)?.message ?? error)}`);
  }
  return installed;
}

/** Materialize, register and keep names in state; disposes any previous provider. */
async function registerSkills(deps: SetupDeps, state: SetupState, binary: string): Promise<void> {
  const service = deps.skillsService();
  if (service === null) throw new SetupError('宿主没有可用的 skills 服务，无法注册内置技能');
  const skills: ParsedSkill[] = await materializeSkills(deps.spawn(binary), deps.fs, deps.tempBase);
  state.disposeProvider?.();
  state.disposeProvider = registerSetupSkills(service, skills, 'dsh-writing-viewer');
  state.skillNames = skills.map((skill) => skill.name);
  state.registered = true;
}

/** Append the builtin binary's directory to the END of the host PATH
 * (idempotent): existing order wins, so an external wa-cli keeps resolving
 * first and the builtin is the fallback. A bare binary name without a
 * directory separator yields no directory, so nothing is appended for it. */
function appendCliDir(deps: SetupDeps, binary: string): void {
  const at = binary.lastIndexOf('/');
  if (at <= 0) return;
  const dir = binary.slice(0, at);
  deps.env.PATH = appendPathValue(deps.env.PATH, dir, deps.pathSeparator);
}

/**
 * The full enable pipeline. Single-flight: while `state.busy` is true the
 * call returns the busy status without starting anything.
 */
export async function runSetupAction(deps: SetupDeps, state: SetupState, projectRoot: string | null = null): Promise<SetupStatusBody> {
  if (state.busy) return await buildSetupStatus(deps, state, projectRoot);
  state.busy = true;
  state.lastError = null;
  try {
    const binary = await ensureCli(deps);
    await registerSkills(deps, state, binary);
    appendCliDir(deps, binary);
  } catch (error) {
    state.lastError = error instanceof SetupError ? error.message : `启用失败：${String((error as Error)?.message ?? error)}`;
  } finally {
    state.busy = false;
  }
  return await buildSetupStatus(deps, state, projectRoot);
}

/**
 * Host-start registration when the builtin binary already exists: no
 * download, no version check, no network — just materialize, register and
 * append the fallback PATH entry, so the skills survive a host restart
 * without another click. A missing binary stays quiet (the panel's enable
 * action downloads it). Failures stay quiet too (the enable action reports
 * them properly).
 */
export async function startupSetup(deps: SetupDeps, state: SetupState): Promise<void> {
  try {
    const path = builtinCliPath(deps);
    const info = await deps.fs.stat(path);
    if (info === null || !info.isFile) return;
    await registerSkills(deps, state, path);
    appendCliDir(deps, path);
  } catch (error) {
    console.error(`[dsh-writing-viewer] startup setup skipped: ${String((error as Error)?.message ?? error)}`);
  }
}

/** Route request/response shapes shared with the existing routes. */
interface RouteRequest {
  method?: string;
  headers?: Record<string, unknown>;
}

interface RouteResponse {
  writeHead(status: number, headers?: Record<string, string>): { end(body?: string): void };
  end(body?: string): void;
}

/** The setup route's optional `root` query parameter (the panel's current
 * project directory), validated exactly like the snapshot route's panel root
 * (host-route-path-input MUST): only an absolute, non-network path survives;
 * anything else is treated as absent so the report falls back to the
 * user-level directories instead of probing unintended paths. */
function rootParamOf(url: string | undefined): string | null {
  let raw: string | null = null;
  try {
    raw = new URL(url ?? '/', 'http://x').searchParams.get('root');
  } catch {
    return null;
  }
  if (raw === null) return null;
  const parsed = parsePanelRoot(raw, { isAbsolute, resolve: resolvePath });
  return parsed.kind === 'ok' ? parsed.path : null;
}

export const SETUP_PATH = '/dsh-writing-viewer/setup';

/** Register the setup route on an acquired webServer scope: GET answers the
 * status, POST runs the explicit enable action, anything else is a 405. */
export function registerSetupRoutes(
  scope: { webServer?: { register(route: unknown): unknown } & Record<string, unknown> } & Record<string, unknown>,
  deps: SetupDeps,
  state: SetupState,
  guardOptions: GuardOptions,
): void {
  const server = (scope.webServer ?? scope) as { register(route: unknown): unknown };
  const json = (res: RouteResponse, status: number, body: unknown, extraHeaders?: Record<string, string>): void => {
    res
      .writeHead(status, { 'content-type': 'application/json; charset=utf-8', ...(extraHeaders ?? {}) })
      .end(JSON.stringify(body));
  };
  const refuseOrigin = (res: RouteResponse, reason: string): void => {
    json(res, 403, { ok: false, error: reason }, { 'cache-control': 'no-store' });
  };
  server.register({
    name: 'dsh-writing-viewer-setup',
    kind: 'exact',
    path: SETUP_PATH,
    handler: async (req: RouteRequest, res: RouteResponse) => {
      const verdict = guardRequest(req.headers ?? {}, guardOptions);
      if (!verdict.ok) {
        // A refused request must not trigger any download or registration.
        refuseOrigin(res, verdict.reason);
        return;
      }
      const method = req.method ?? 'GET';
      const root = rootParamOf((req as { url?: string }).url);
      if (method === 'GET') {
        try {
          json(res, 200, await buildSetupStatus(deps, state, root));
        } catch (error) {
          json(res, 200, { ok: false, error: String(error) });
        }
        return;
      }
      if (method === 'POST') {
        try {
          json(res, 200, await runSetupAction(deps, state, root));
        } catch (error) {
          json(res, 200, { ok: false, error: String(error) });
        }
        return;
      }
      res.writeHead(405, { allow: 'GET, POST' }).end();
    },
  });
}
