/**
 * Opt-in setup core for the「内置技能与 CLI」capability.
 *
 * Everything here is gated by the plugin's `enableSetup` userConfig (default
 * off) and is only ever triggered by an explicit panel action — never by a
 * snapshot request, never at server start. The module is pure over injected
 * adapters so unit tests can drive downloads and installs without the
 * network or a real wa-cli:
 *
 * - CLI acquisition mirrors install.sh / install.ps1's release contract
 *   exactly: asset `wa-cli-<os>-<arch>` plus the `SHA256SUMS` record from the
 *   same release directory, verified before anything is written, installed
 *   atomically (temp file + rename, chmod 755) into the configured directory.
 * - Skills are NOT installed by this capability: the 13 built-in wa-*
 *   skills ship statically inside the plugin (staged at build time from
 *   internal/skills/skills). The setup area only reports, per skill, whether
 *   an external same-name skill exists in a standard skill directory, so the
 *   author can see what ZCode may resolve instead of the bundled copy.
 * - No PATH mutation: a ZCode plugin process cannot write the host process
 *   environment back. The server resolves wa-cli as waCliPath (userConfig)
 *   first, then PATH order, then the managed install directory; the setup
 *   area reports the resolution honestly.
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
  /** Explicit wa-cli executable override (userConfig waCliPath); null when unset. */
  waCliPath: string | null;
}

/** Default release base, the project's own GitHub Releases. */
export const DEFAULT_RELEASE_BASE = 'https://github.com/kw12121212/writing-anything/releases';

/** Default binary install directory, matching install.sh's user-level default. */
export const DEFAULT_INSTALL_DIR = '~/.local/bin';

/** Name of the installed binary. */
export const CLI_BINARY_NAME = 'wa-cli';

/** The built-in skill roster the plugin bundles (build-time staged, in
 * internal/skills/skills order). Used only for the source table. */
export const BUNDLED_SKILLS = [
  'wa-assemble',
  'wa-auto',
  'wa-brief',
  'wa-continuity',
  'wa-draft',
  'wa-illustrate',
  'wa-init',
  'wa-outline',
  'wa-plan',
  'wa-publish',
  'wa-review',
  'wa-revise',
  'wa-scene',
] as const;

/** Read the setup config tolerantly; anything absent or malformed keeps the
 * capability disabled or on its default — a broken config never turns writes on. */
export function parseSetupConfig(config: Record<string, unknown> | undefined, home: string | null): SetupOptions {
  const enabled = config?.enableSetup === true;
  const waVersion = normalizeNonEmpty(config?.waVersion) ?? 'latest';
  const rawDir = normalizeNonEmpty(config?.cliInstallDir) ?? DEFAULT_INSTALL_DIR;
  const cliInstallDir = expandHome(rawDir, home);
  const waBaseUrl = (normalizeNonEmpty(config?.waBaseUrl) ?? DEFAULT_RELEASE_BASE).replace(/\/+$/, '');
  const waCliPath = normalizeNonEmpty(config?.waCliPath);
  return { enabled, waVersion, cliInstallDir, waBaseUrl, waCliPath };
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
 * Mirrors install.sh / install.ps1: linux/darwin/windows × amd64/arm64. */
export function releaseAssetName(platform: string, arch: string): string | null {
  const os = platform === 'darwin' ? 'darwin' : platform === 'linux' ? 'linux' : platform === 'win32' ? 'windows' : null;
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

/** Injectable fetch; the default is the server runtime's global fetch. */
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

/** SHA-256 of arbitrary bytes, hex — used for download verification and for
 * provenance-marker comparison against the on-disk binary. */
export const sha256HexOf = sha256Hex;

/** Sidecar marker the plugin writes next to a binary IT installed, so
 * "built-in" can be distinguished from an author-installed binary at the
 * same path (install.sh and install.ps1 default to other directories, but
 * only the marker distinguishes who installed what). */
export const PROVENANCE_MARKER_NAME = 'wa-cli.zcode-writing-viewer.json';

/** Marker payload: who manages the binary, what bytes were installed, when. */
export interface ProvenanceMarker {
  managedBy: string;
  sha256: string;
  installedAt: string;
}

/** Marker path next to a binary path. */
export function provenanceMarkerPath(binaryPath: string): string {
  const normalized = binaryPath.replace(/\\/g, '/');
  const at = normalized.lastIndexOf('/');
  return at <= 0 ? PROVENANCE_MARKER_NAME : normalized.slice(0, at) + '/' + PROVENANCE_MARKER_NAME;
}

/** Build the marker for bytes the plugin is about to install. */
export function provenanceMarker(bytes: Uint8Array, installedAt: string): ProvenanceMarker {
  return { managedBy: 'zcode-writing-viewer', sha256: sha256Hex(bytes), installedAt };
}

/** Parse marker JSON; null for anything malformed or foreign. */
export function parseProvenanceMarker(text: string): ProvenanceMarker | null {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return null;
  }
  if (raw === null || typeof raw !== 'object') return null;
  const marker = raw as Record<string, unknown>;
  if (marker.managedBy !== 'zcode-writing-viewer') return null;
  if (typeof marker.sha256 !== 'string' || marker.sha256 === '') return null;
  if (typeof marker.installedAt !== 'string' || marker.installedAt === '') return null;
  return { managedBy: marker.managedBy, sha256: marker.sha256, installedAt: marker.installedAt };
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
  /** Raw bytes read (marker verification hashes the real binary). */
  readFileBytes(path: string): Promise<Uint8Array | null>;
  rm(path: string, recursive: boolean): Promise<void>;
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

/** Atomically install the binary bytes as `<dir>/wa-cli` (temp + rename, 0755)
 * and write the provenance marker next to it. */
export async function installBinary(fs: SetupFs, dir: string, bytes: Uint8Array, installedAt: string): Promise<string> {
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
  const marker = provenanceMarker(bytes, installedAt);
  try {
    await fs.writeFile(provenanceMarkerPath(dest), new TextEncoder().encode(JSON.stringify(marker, null, 2) + '\n'));
  } catch (error) {
    throw new SetupError(`写入溯源标记失败：${String((error as Error)?.message ?? error)}`);
  }
  return dest;
}

/** Standard external skill directories ZCode reads, project level first.
 * `projectRoot` null skips the project-level roots. This is an existence
 * comparison only — which copy ZCode actually loads is decided by the host's
 * own precedence, which the panel does not claim to predict. */
export function externalSkillRoots(
  projectRoot: string | null,
  homes: { agents: string; claude: string },
): { dir: string; source: string }[] {
  const roots: { dir: string; source: string }[] = [];
  if (projectRoot !== null) {
    roots.push({ dir: `${projectRoot}/.agents/skills`, source: 'project-agents' });
    roots.push({ dir: `${projectRoot}/.claude/skills`, source: 'project-claude' });
  }
  roots.push({ dir: `${homes.agents}/skills`, source: 'user-agents' });
  roots.push({ dir: `${homes.claude}/skills`, source: 'user-claude' });
  return roots;
}

/** One row of the skill source table. */
export interface SkillSourceRow {
  name: string;
  /** Where an external same-name SKILL.md was found, when it was. */
  external: { source: string; path: string } | null;
}

/** Build the skill source table: for every bundled skill, the first standard
 * directory holding `<name>/SKILL.md` (existence only). */
export async function skillSourceTable(
  fs: SetupFs,
  roots: readonly { dir: string; source: string }[],
  roster: readonly string[] = BUNDLED_SKILLS,
): Promise<SkillSourceRow[]> {
  const rows: SkillSourceRow[] = [];
  for (const name of roster) {
    let external: { source: string; path: string } | null = null;
    for (const root of roots) {
      const path = `${root.dir}/${name}/SKILL.md`;
      const info = await fs.stat(path);
      if (info !== null && info.isFile) {
        external = { source: root.source, path };
        break;
      }
    }
    rows.push({ name, external });
  }
  return rows;
}

/** Every PATH entry holding a regular file named `name`, in PATH order —
 * an honest report of what the server resolves and where the other binaries
 * sit. */
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

/** The binary the plugin itself runs: waCliPath override when set, else the
 * first PATH hit, else the managed install-dir binary when present. */
export async function resolveCliBinary(
  fs: SetupFs,
  options: SetupOptions,
  pathValue: string | undefined,
  separator: string,
): Promise<string | null> {
  if (options.waCliPath !== null) return options.waCliPath;
  const onPath = await resolveAllOnPath(fs, pathValue, CLI_BINARY_NAME, separator);
  if (onPath.length > 0) return onPath[0] ?? null;
  const managed = `${options.cliInstallDir}/${CLI_BINARY_NAME}`;
  const info = await fs.stat(managed);
  return info !== null && info.isFile ? managed : null;
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
