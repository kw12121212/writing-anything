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
  installBinary,
  materializeSkills,
  parseVersionOutput,
  appendPathValue,
  registerSetupSkills,
  releaseAssetName,
  resolveAllOnPath,
  type FetchLike,
  type ParsedSkill,
  type SetupFs,
  type SetupOptions,
  type SetupRunner,
  type SkillsServiceLike,
} from './setup';
import { guardRequest, type GuardOptions } from './routeguard';

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
  };
  skills: { registered: boolean; names: string[] };
  lastError: string | null;
}

/** The builtin binary path the setup manages (or the configured override). */
export function builtinCliPath(deps: SetupDeps): string {
  return deps.waCliPath ?? `${deps.options.cliInstallDir}/${CLI_BINARY_NAME}`;
}

async function queryVersion(deps: SetupDeps, binary: string): Promise<string | null> {
  const result = await deps.spawn(binary).run(['version', '--json'], { cwd: deps.tempBase });
  return result === null ? null : parseVersionOutput(result.stdout);
}

/** Assemble the status body from live probes; never throws. */
export async function buildSetupStatus(deps: SetupDeps, state: SetupState): Promise<SetupStatusBody> {
  const path = builtinCliPath(deps);
  const info = await deps.fs.stat(path);
  const ready = info !== null && info.isFile;
  let version: string | null = null;
  if (ready && !state.busy) version = await queryVersion(deps, path);
  const hits = await resolveAllOnPath(deps.fs, deps.env.PATH, CLI_BINARY_NAME, deps.pathSeparator);
  const resolved = hits[0] ?? null;
  const external = hits.find((hit) => hit !== path) ?? null;
  return {
    ok: true,
    enabled: true,
    busy: state.busy,
    cli: { ready, path: ready ? path : null, version, resolved, external },
    skills: { registered: state.registered, names: [...state.skillNames] },
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
  return await installBinary(deps.fs, deps.options.cliInstallDir, bytes);
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
export async function runSetupAction(deps: SetupDeps, state: SetupState): Promise<SetupStatusBody> {
  if (state.busy) return await buildSetupStatus(deps, state);
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
  return await buildSetupStatus(deps, state);
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
      if (method === 'GET') {
        try {
          json(res, 200, await buildSetupStatus(deps, state));
        } catch (error) {
          json(res, 200, { ok: false, error: String(error) });
        }
        return;
      }
      if (method === 'POST') {
        try {
          json(res, 200, await runSetupAction(deps, state));
        } catch (error) {
          json(res, 200, { ok: false, error: String(error) });
        }
        return;
      }
      res.writeHead(405, { allow: 'GET, POST' }).end();
    },
  });
}
