/**
 * Opt-in setup core for the「内置技能与 CLI」capability.
 *
 * Everything here is gated by the plugin's `enableSetup` config (default
 * off) and is only ever triggered by an explicit panel action — never by a
 * snapshot request, never at host start beyond a workspace-free skill
 * registration. The module is pure over injected adapters so unit tests can
 * drive downloads, installs, materialization and registration without the
 * network or a real wa-cli:
 *
 * - CLI acquisition mirrors install.sh's release contract exactly: asset
 *   `wa-cli-<os>-<arch>` plus the `SHA256SUMS` record from the same release
 *   directory, verified before anything is written, installed atomically
 *   (temp file + rename, chmod 755) into the configured directory.
 * - Skills are materialized through `wa-cli skills install --target <tmpdir>`
 *   with the temp dir as the spawn cwd — the directory holds no
 *   `.writing-anything`, so the spawn writes no audit line anywhere
 *   (verified against the real binary). Parsed SKILL.md bodies are
 *   registered into the DSH skill registry through a provider whose rank
 *   (650) sits ABOVE every filesystem source (project 100/200, user
 *   400/500, bundled 600) in the ascending rank contest — any same-name
 *   external skill wins and the built-in serves as the fallback when none
 *   exists. No user or project skill directory is ever written.
 * - CLI precedence is external-first with a builtin fallback: the DSH host
 *   builds child process environments from the host `process.env` (minus
 *   sensitive keys and `DSH_*`), so idempotently APPENDING the managed
 *   binary's directory to the end of PATH keeps the existing order — an
 *   external wa-cli keeps resolving first, and the builtin is reachable
 *   only when no external binary is. The append happens only while setup
 *   is enabled.
 */
import { createHash } from 'node:crypto';

/** Parsed plugin config for the setup capability. */
export interface SetupOptions {
  enabled: boolean;
  /** Release tag to install; `latest` (default) never re-downloads an existing binary. */
  waVersion: string;
  /** Absolute directory the wa-cli binary is installed into (~ expanded). */
  cliInstallDir: string;
  /** Release base URL; overridable for mirrors and tests. */
  waBaseUrl: string;
}

/** Default release base, the project's own GitHub Releases. */
export const DEFAULT_RELEASE_BASE = 'https://github.com/kw12121212/writing-anything/releases';

/** Default binary install directory, matching install.sh's user-level default. */
export const DEFAULT_INSTALL_DIR = '~/.local/bin';

/** Name of the installed binary. */
export const CLI_BINARY_NAME = 'wa-cli';

/** The setup skill provider's fixed rank: ABOVE every DSH filesystem source
 * (project .dsh = 100, project .agents = 200, user = 400/500, bundled = 600)
 * in the ascending rank contest — any same-name external skill wins, and the
 * built-in registration serves as the fallback when none exists. */
export const SETUP_SKILL_RANK = 650;

/** Read the setup config tolerantly; anything absent or malformed keeps the
 * capability disabled or on its default — a broken config never turns writes on. */
export function parseSetupConfig(config: Record<string, unknown> | undefined, home: string | null): SetupOptions {
  const enabled = (config?.enableSetup === true);
  const waVersion = normalizeNonEmpty(config?.waVersion) ?? 'latest';
  const rawDir = normalizeNonEmpty(config?.cliInstallDir) ?? DEFAULT_INSTALL_DIR;
  const cliInstallDir = expandHome(rawDir, home);
  const waBaseUrl = (normalizeNonEmpty(config?.waBaseUrl) ?? DEFAULT_RELEASE_BASE).replace(/\/+$/, '');
  return { enabled, waVersion, cliInstallDir, waBaseUrl };
}

function normalizeNonEmpty(value: unknown): string | null {
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : null;
}

/** Expand a leading `~` (and `~user` is rejected — only the owner's home). */
export function expandHome(dir: string, home: string | null): string {
  if (dir === '~') return home ?? dir;
  if (dir.startsWith('~/')) return home !== null ? home + dir.slice(1) : dir;
  return dir;
}

/** Release asset name for a platform/arch pair, or null when unsupported.
 * Mirrors install.sh: linux/darwin × amd64/arm64. */
export function releaseAssetName(platform: string, arch: string): string | null {
  const os = platform === 'darwin' ? 'darwin' : platform === 'linux' ? 'linux' : null;
  const cpu = arch === 'x64' ? 'amd64' : arch === 'arm64' ? 'arm64' : null;
  return os === null || cpu === null ? null : `wa-cli-${os}-${cpu}`;
}

/** Upper bound on one downloaded release asset (the binary is a few MB). */
export const ASSET_MAX_BYTES = 64 * 1024 * 1024;
/** Upper bound on the downloaded SHA256SUMS file. */
export const SUMS_MAX_BYTES = 64 * 1024;
/** Download timeout. */
export const DOWNLOAD_TIMEOUT_MS = 120_000;

/** Minimal fetch response shape the downloader needs. */
export interface FetchResponseLike {
  ok: boolean;
  status: number;
  /** Node fetch streams; tests can omit it and use arrayBuffer. */
  body?: unknown;
  arrayBuffer(): Promise<ArrayBuffer>;
}

/** Injectable fetch; the default is the host runtime's global fetch. */
export type FetchLike = (url: string, init?: { signal?: AbortSignal }) => Promise<FetchResponseLike>;

/** One setup failure with a human-readable, panel-ready message. */
export class SetupError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SetupError';
  }
}

function sha256Hex(bytes: Uint8Array): string {
  return createHash('sha256').update(bytes).digest('hex');
}

/** Expected SHA-256 for `asset` from a SHA256SUMS body, or null when absent. */
export function checksumFor(sumsText: string, asset: string): string | null {
  for (const rawLine of sumsText.split('\n')) {
    const line = rawLine.replace(/\r$/, '');
    const fields = line.trim().split(/\s+/);
    if (fields.length !== 2) continue;
    const hash: string | undefined = fields[0];
    const file: string | undefined = fields[1];
    if (hash === undefined || file === undefined) continue;
    if (file === asset || file === '*' + asset) return hash.toLowerCase();
  }
  return null;
}

/** Read one response with a hard byte cap; a larger body aborts the stream
 * instead of buffering an unbounded download. */
async function readBounded(response: FetchResponseLike, cap: number, what: string): Promise<Uint8Array> {
  const body = response.body as {
    getReader?: () => {
      read(): Promise<{ done: boolean; value?: Uint8Array }>;
      cancel?(): Promise<unknown>;
    };
  } | undefined;
  if (typeof body?.getReader === 'function') {
    const reader = body.getReader();
    const parts: Uint8Array[] = [];
    let total = 0;
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      const piece = chunk.value;
      if (piece !== undefined) {
        total += piece.byteLength;
        if (total > cap) {
          await reader.cancel?.().catch(() => undefined);
          throw new SetupError(`${what}超过大小上限（${cap} 字节），已中止下载`);
        }
        parts.push(piece);
      }
    }
    const out = new Uint8Array(total);
    let at = 0;
    for (const piece of parts) {
      out.set(piece, at);
      at += piece.byteLength;
    }
    return out;
  }
  const buffer = await response.arrayBuffer();
  if (buffer.byteLength > cap) throw new SetupError(`${what}超过大小上限（${cap} 字节），已中止下载`);
  return new Uint8Array(buffer);
}

/** Injectable filesystem adapter for setup writes and probes. */
export interface SetupFs {
  mkdir(dir: string): Promise<void>;
  writeFile(path: string, bytes: Uint8Array): Promise<void>;
  chmod(path: string, mode: number): Promise<void>;
  rename(from: string, to: string): Promise<void>;
  stat(path: string): Promise<{ isFile: boolean; isDirectory: boolean } | null>;
  readFile(path: string): Promise<string | null>;
  readdir(dir: string): Promise<string[] | null>;
  mkdtemp(prefix: string): Promise<string>;
  rm(dir: string, recursive: boolean): Promise<void>;
}

/** Download one release URL into bounded memory. */
async function download(fetchLike: FetchLike, url: string, cap: number, what: string): Promise<Uint8Array> {
  let response: FetchResponseLike;
  try {
    response = await fetchLike(url, { signal: AbortSignal.timeout(DOWNLOAD_TIMEOUT_MS) });
  } catch (error) {
    throw new SetupError(`${what}下载失败：${String((error as Error)?.message ?? error)}（${url}）`);
  }
  if (!response.ok) throw new SetupError(`${what}下载失败：HTTP ${response.status}（${url}）`);
  return await readBounded(response, cap, what);
}

/** Download and verify the wa-cli release asset; nothing is written on any failure. */
export async function downloadRelease(
  fetchLike: FetchLike,
  options: { baseUrl: string; version: string; asset: string },
): Promise<Uint8Array> {
  const dir = options.version === 'latest' ? `${options.baseUrl}/latest/download` : `${options.baseUrl}/download/${options.version}`;
  const asset = await download(fetchLike, `${dir}/${options.asset}`, ASSET_MAX_BYTES, `发布产物 ${options.asset}`);
  const sums = await download(fetchLike, `${dir}/SHA256SUMS`, SUMS_MAX_BYTES, 'SHA256SUMS');
  const expected = checksumFor(new TextDecoder().decode(sums), options.asset);
  if (expected === null) throw new SetupError(`SHA256SUMS 中没有 ${options.asset} 的记录，拒绝安装`);
  const actual = sha256Hex(asset);
  if (actual !== expected) throw new SetupError(`校验失败：${options.asset} 的 SHA-256 为 ${actual}，SHA256SUMS 记录为 ${expected}`);
  return asset;
}

/** Atomically install the binary bytes as `<dir>/wa-cli` (temp + rename, 0755). */
export async function installBinary(fs: SetupFs, dir: string, bytes: Uint8Array): Promise<string> {
  const existing = await fs.stat(dir);
  if (existing !== null && !existing.isDirectory) throw new SetupError(`安装目录不是真实目录：${dir}`);
  if (existing === null) await fs.mkdir(dir);
  const dest = `${dir}/${CLI_BINARY_NAME}`;
  const current = await fs.stat(dest);
  if (current !== null && !current.isFile) throw new SetupError(`目标路径已被非文件占用：${dest}`);
  const tmp = `${dir}/.wa-cli-install-${Date.now()}-${Math.floor(Math.random() * 1e6)}.tmp`;
  try {
    await fs.writeFile(tmp, bytes);
    await fs.chmod(tmp, 0o755);
    await fs.rename(tmp, dest);
  } catch (error) {
    // Best-effort cleanup: a failed install must not leave a temp executable.
    await fs.rm(tmp, false).catch(() => undefined);
    throw new SetupError(`安装 wa-cli 失败：${String((error as Error)?.message ?? error)}`);
  }
  return dest;
}

/** A materialized built-in skill: frontmatter plus the instruction body. */
export interface ParsedSkill {
  name: string;
  description: string;
  /** SKILL.md body after the closing frontmatter fence, verbatim. */
  body: string;
}

/** Parse one SKILL.md (frontmatter with name + description, then the body);
 * returns null for anything that does not follow the built-in skill shape. */
export function parseSkillFile(content: string): ParsedSkill | null {
  if (!content.startsWith('---\n')) return null;
  const end = content.indexOf('\n---\n', 3);
  if (end === -1) return null;
  const head = content.slice(4, end);
  const body = content.slice(end + 5).replace(/^\n+/, '').replace(/\s+$/, '');
  let name: string | null = null;
  let description: string | null = null;
  for (const line of head.split('\n')) {
    const colon = line.indexOf(':');
    if (colon === -1) continue;
    const key = line.slice(0, colon).trim();
    const value = line.slice(colon + 1).trim();
    if (key === 'name' && name === null) name = value;
    if (key === 'description' && description === null) description = value;
  }
  if (name === null || name === '' || description === null || description === '' || body === '') return null;
  return { name, description, body };
}

/** Runner for the setup spawns; cwd must be controllable so the skills
 * materialization never runs inside a directory that holds a workspace. */
export interface SetupRunner {
  run(args: readonly string[], options: { cwd: string }): Promise<{ stdout: string } | null>;
}

/** Materialize every built-in skill into a fresh temp directory and parse the
 * SKILL.md files; the temp directory is removed afterwards — the registered
 * provider keeps the parsed bodies in memory. */
export async function materializeSkills(
  runner: SetupRunner,
  fs: SetupFs,
  tempBase: string,
): Promise<ParsedSkill[]> {
  const dir = await fs.mkdtemp(`${tempBase}/wa-skills-`);
  try {
    const result = await runner.run(['skills', 'install', '--target', dir, '--json'], { cwd: dir });
    if (result === null) throw new SetupError('wa-cli 不可用或执行失败，无法物化内置技能');
    let envelope: unknown;
    try {
      envelope = JSON.parse(result.stdout);
    } catch (error) {
      throw new SetupError(`wa-cli skills install 输出无法解析：${String(error)}`);
    }
    const valid = (envelope as { valid?: unknown })?.valid;
    if (valid !== true) {
      const first = Array.isArray((envelope as { diagnostics?: unknown[] })?.diagnostics)
        ? ((envelope as { diagnostics: { message?: unknown }[] }).diagnostics[0]?.message ?? '')
        : '';
      throw new SetupError(`wa-cli skills install 未成功${first === '' ? '' : `：${String(first)}`}`);
    }
    const entries = await fs.readdir(dir);
    if (entries === null) throw new SetupError('无法读取物化结果目录');
    const skills: ParsedSkill[] = [];
    for (const entry of entries.sort()) {
      const text = await fs.readFile(`${dir}/${entry}/SKILL.md`);
      if (text === null) continue;
      const parsed = parseSkillFile(text);
      if (parsed === null || parsed.name !== entry) continue;
      skills.push(parsed);
    }
    if (skills.length === 0) throw new SetupError('物化结果中没有可用的内置技能（SKILL.md 缺失或格式不符）');
    return skills;
  } finally {
    await fs.rm(dir, true).catch(() => undefined);
  }
}

/** The provider candidate the DSH skill registry validates. `locator` is the
 * opaque handle the registry passes back to `get` — here the skill name. */
export interface SkillCandidate {
  name: string;
  description: string;
  source: string;
  provider: string;
  rank: number;
  locator: unknown;
  invocation: { modelInvocable: boolean; userInvocable: boolean };
}

/** The definition `get` resolves for one candidate. */
export interface SkillDefinition extends SkillCandidate {
  content: string;
}

/** The slice of the DSH `skills` service the setup uses. The provider
 * contract is ASYNCHRONOUS: the registry awaits `list`/`get` and, whenever a
 * lookup carries an AbortSignal (every real consumer passes one), calls
 * `.then` on the returned value directly — a synchronous array or object is
 * a TypeError and the registry silently skips the whole provider. */
export interface SkillsServiceLike {
  registerProvider(create: (control: { signal: AbortSignal; invalidate(): void }) => {
    name: string;
    list(options: { signal?: AbortSignal }): Promise<readonly SkillCandidate[]>;
    get(candidate: SkillCandidate, options: { signal?: AbortSignal }): Promise<SkillDefinition | undefined>;
  }): () => void;
}

/** Register the built-in skills as one provider ranked ABOVE every filesystem
 * source; any same-name external skill wins, and the built-in serves as the
 * fallback when none exists. Returns the disposer. */
export function registerSetupSkills(service: SkillsServiceLike, skills: readonly ParsedSkill[], providerName: string): () => void {
  const byName = new Map(skills.map((skill) => [skill.name, skill]));
  return service.registerProvider(() => ({
    name: providerName,
    async list(): Promise<readonly SkillCandidate[]> {
      return skills.map((skill) => ({
        name: skill.name,
        description: skill.description,
        source: providerName,
        provider: providerName,
        rank: SETUP_SKILL_RANK,
        locator: skill.name,
        invocation: { modelInvocable: true, userInvocable: true },
      }));
    },
    async get(candidate: SkillCandidate): Promise<SkillDefinition | undefined> {
      const skill = byName.get(candidate.name);
      if (skill === undefined) return undefined;
      return {
        name: skill.name,
        description: skill.description,
        source: providerName,
        provider: providerName,
        rank: SETUP_SKILL_RANK,
        locator: skill.name,
        invocation: { modelInvocable: true, userInvocable: true },
        content: skill.body,
      };
    },
  }));
}

/**
 * Idempotent PATH append: the builtin binary's directory goes to the END of
 * PATH (fallback position) when it is not on PATH at all, and PATH is left
 * untouched when the directory is already present anywhere — existing order
 * wins, so an external wa-cli keeps resolving first and the builtin serves
 * only when no external binary is reachable.
 */
export function appendPathValue(pathValue: string | undefined, dir: string, separator: string): string {
  const current = pathValue ?? '';
  const entries = current.split(separator);
  if (entries.includes(dir)) return current;
  return current === '' ? dir : current + separator + dir;
}

/** Every PATH entry holding a regular file named `name`, in PATH order —
 * an honest report of what later host children resolve and where the other
 * binaries (external ahead of the appended builtin, or the builtin itself
 * when no external exists) sit. */
export async function resolveAllOnPath(
  fs: SetupFs,
  pathValue: string | undefined,
  name: string,
  separator: string,
): Promise<string[]> {
  const entries = (pathValue ?? '').split(separator).filter((entry) => entry !== '');
  const hits: string[] = [];
  for (const entry of entries) {
    const info = await fs.stat(`${entry}/${name}`);
    if (info !== null && info.isFile) hits.push(`${entry}/${name}`);
  }
  return hits;
}

/** Parsed `wa-cli version --json` output. */
export function parseVersionOutput(stdout: string): string | null {
  try {
    const version = (JSON.parse(stdout) as { version?: unknown })?.version;
    return typeof version === 'string' && version !== '' ? version : null;
  } catch {
    return null;
  }
}
