/**
 * Server half of the zcode-writing-viewer plugin: an MCP stdio server the
 * ZCode manifest launches with `node dist/server.js` (cwd is
 * ZCODE_PROJECT_DIR, the project ZCode opened).
 *
 * The whole tool and resource surface is registered through low-level
 * JSON-RPC request handlers so every tool list entry and every tool result
 * can carry the `_meta.ui` fields the ZCode panel contract reads (surface
 * opening, model/app visibility) without depending on SDK-specific
 * high-level registration shapes:
 *
 * - read-only viewer tools: workspace_status, workspace_snapshot (with the
 *   panel's path-switch `root` argument), open_workspace (its result's
 *   _meta.ui.surface opens the panel), show_in_viewer, get_view,
 *   report_view, release_view;
 * - review tools: review_start / review_status / review_end plus the thin
 *   forwarders review_page and review_submit (see ./reviewproxy) — one
 *   wa-cli preview child per server, token never leaves this process;
 * - opt-in setup tools behind the enableSetup userConfig switch:
 *   setup_status and setup_enable (CLI download, see ./setup);
 * - resources: ui://zcode-writing-viewer/panel.html (the panel document)
 *   and the ui://zcode-writing-viewer/review-illustration/<unit>/<id>
 *   template (registered illustration and cover images only).
 *
 * The server performs no workspace writes anywhere: the only filesystem
 * operations are stats, directory listings and reads (plus the opt-in setup
 * install directory). The writing workspace is only ever read here; the one
 * write path is the review session, where the wa-cli reading-view child
 * writes what the author submits.
 */
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFile, readdir, lstat, stat } from 'node:fs/promises';
import { homedir } from 'node:os';
import { delimiter as pathDelimiter, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
  ListResourcesRequestSchema,
  ReadResourceRequestSchema,
} from '@modelcontextprotocol/sdk/types.js';

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
import { buildSnapshot, type SnapshotRouteBody } from './snapshot';
import type { CliRunner } from './wacli';
import type { DirEntry, SnapshotFs } from './artifacts';
import {
  BUNDLED_SKILLS,
  CLI_BINARY_NAME,
  downloadRelease,
  externalSkillRoots,
  installBinary,
  parseSetupConfig,
  parseVersionOutput,
  provenanceMarkerPath,
  parseProvenanceMarker,
  releaseAssetName,
  resolveAllOnPath,
  resolveCliBinary,
  skillSourceTable,
  type SetupOptions,
} from './setup';
import { realSetupFs } from './setupfs';
import { createReviewSessions, validReviewUnit, type ReviewChild, type ReviewSessions } from './reviewsession';
import { proxyReview, reviewTargetOf, type ProxyFetch } from './reviewproxy';

export const PLUGIN_ID = 'zcode-writing-viewer';
export const PANEL_URI = `ui://${PLUGIN_ID}/panel.html`;
export const ILLUSTRATION_PREFIX = `ui://${PLUGIN_ID}/review-illustration/`;

/** Milliseconds before a wa-cli invocation counts as unavailable. */
const CLI_TIMEOUT_MS = 15_000;

/** Milliseconds a forwarded reading-view request may wait on the upstream. */
const REVIEW_UPSTREAM_TIMEOUT_MS = 30_000;

/** Upper bound on captured wa-cli stdout; real envelopes are far smaller. */
const CLI_STDOUT_CAP = 8 * 1024 * 1024;

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
 * a degradation instead of failing the whole response.
 */
export function spawnCliRunner(binary: string): CliRunner {
  return {
    run(args: readonly string[], options?: { cwd?: string }): Promise<{ stdout: string } | null> {
      return new Promise((done) => {
        let child: ReturnType<typeof spawn>;
        try {
          child = spawn(binary, args, { cwd: options?.cwd, stdio: ['ignore', 'pipe', 'ignore'] });
        } catch {
          done(null);
          return;
        }
        let stdout = '';
        let settled = false;
        const finish = (value: { stdout: string } | null): void => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          done(value);
        };
        const timer = setTimeout(() => {
          child.kill();
          finish(null);
        }, CLI_TIMEOUT_MS);
        child.stdout?.on('data', (chunk: Buffer) => {
          // Bounded accumulation: an output stream larger than any legitimate
          // envelope stops growing and the oversized result parses as a
          // degradation instead of exhausting memory.
          if (stdout.length > CLI_STDOUT_CAP) return;
          stdout += chunk.toString('utf8');
          if (stdout.length > CLI_STDOUT_CAP) stdout = stdout.slice(0, CLI_STDOUT_CAP);
        });
        child.on('error', () => finish(null));
        child.on('close', (code: number | null) => {
          // Exit code is not failure: status/validate legitimately exit 1 on
          // an invalid workspace while still printing a full JSON envelope.
          if (code === null) finish(null);
          else finish({ stdout });
        });
      });
    },
  };
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
 * Collect resolution candidates. An explicit `workspaceRoot` userConfig is an
 * exclusive pin: when set, it is the only candidate, so a pinned directory
 * without a workspace shows the empty state instead of silently falling
 * through to some other project. Without it, the candidates are the server's
 * startup working directory (the manifest pins it to ZCODE_PROJECT_DIR, the
 * project ZCode opened) and then the process working directory.
 */
export function collectCandidates(
  config: { workspaceRoot?: unknown } | undefined,
  processCwd: string,
): RootCandidate[] {
  const configured = normalizePathInput(config?.workspaceRoot);
  if (configured !== null) return [{ source: 'config', path: configured }];
  const candidates: RootCandidate[] = [];
  const cwd = normalizePathInput(processCwd);
  if (cwd !== null) candidates.push({ source: 'server-cwd', path: cwd });
  return dedupeCandidates(candidates);
}

/** The JSON body workspace_status serves. Fields are additive-only. */
export interface WorkspaceStatusBody {
  ok: boolean;
  root: string | null;
  source: RootSource | null;
  workspaceDir: string | null;
  workspaceExists: boolean;
  candidates: CandidateProbe[];
  emptyState: { command: string } | null;
}

/** Build the status body from the plugin config and the process cwd. */
export async function workspaceStatus(
  config: { workspaceRoot?: unknown } | undefined,
  processCwd: string,
): Promise<WorkspaceStatusBody> {
  const candidates = collectCandidates(config, processCwd);
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

/** Locate a workspace once, shared by the snapshot and review tools. */
async function locateOnce(
  config: { workspaceRoot?: unknown } | undefined,
  processCwd: string,
): Promise<{ candidates: CandidateProbe[]; located: { root: string; source: RootSource } | null }> {
  const candidates = collectCandidates(config, processCwd);
  const probe = await locateWorkspace(candidates, workspaceExistsAt);
  if (probe.root !== null) return { candidates: probe.candidates, located: { root: probe.root, source: probe.source } };
  return { candidates: probe.candidates, located: null };
}

/**
 * Build the full snapshot body from the real adapters.
 *
 * `panelRoot` is the panel's path switch: a valid absolute path becomes the
 * only candidate for this call, ahead of even the config pin, because it is
 * the author's most recent explicit choice. An invalid path, or one with no
 * workspace under it, answers the empty state with a readable `rootError`
 * instead of falling back to the default location — a silent fallback would
 * show another project while the author believes the switch worked.
 */
export async function fullSnapshot(
  runner: CliRunner,
  config: { workspaceRoot?: unknown } | undefined,
  processCwd: string,
  panelRoot: string | null = null,
): Promise<SnapshotRouteBody> {
  const fs = realSnapshotFs();
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
  const { candidates, located } = await locateOnce(config, processCwd);
  return await buildSnapshot(fs, runner, candidates, located);
}

/** Spawn adapter for the review session child: argument list only, never a
 * shell; stdout piped (the session drains it), stdin and stderr ignored. */
export function spawnReviewChild(binary: string, args: readonly string[]): ReviewChild {
  return spawn(binary, args, { stdio: ['ignore', 'pipe', 'ignore'] }) as unknown as ReviewChild;
}

/**
 * Where a review session starts: the panel's explicit root (validated like
 * the snapshot's `root` argument, never falling back), else the default
 * location. A directory without a workspace is a readable refusal.
 */
export function reviewRootResolver(
  config: { workspaceRoot?: unknown } | undefined,
  processCwd: () => string,
): (root: string | null) => Promise<{ ok: true; root: string } | { ok: false; error: string }> {
  return async (root) => {
    const requested = parsePanelRoot(root, { isAbsolute, resolve });
    if (requested.kind === 'invalid') return { ok: false, error: requested.reason };
    if (requested.kind === 'ok') {
      return (await workspaceExistsAt(requested.path))
        ? { ok: true, root: requested.path }
        : { ok: false, error: `该目录下没有 ${WORKSPACE_DIR}/ 工作区：${requested.path}` };
    }
    const { located } = await locateOnce(config, processCwd());
    return located === null ? { ok: false, error: `没有找到 ${WORKSPACE_DIR}/ 工作区` } : { ok: true, root: located.root };
  };
}

// ---------------------------------------------------------------------------
// Viewer state: what the panel shows, reported by the panel and read by the
// model (the documents plugin's get_view/report_view pattern).

interface ViewerReport {
  viewId: string;
  root: string | null;
  unit: string | null;
  /** Which list/detail view the panel shows. */
  view: string | null;
  selection: { unit: string; text: string } | null;
  at: string;
}

export interface ViewerState {
  /** The panel's latest report, if any panel reported yet. */
  current: ViewerReport | null;
  /** Target the model asked the panel to show (unit or named view). */
  target: { unit?: string; view?: string } | null;
}

export function createViewerState(): ViewerState {
  return { current: null, target: null };
}

// ---------------------------------------------------------------------------
// Setup state: single-flight enable action plus status assembly.

interface SetupStateBox {
  busy: boolean;
  lastError: string | null;
}

function cliProvenanceCheck(
  markerText: string | null,
  binaryBytes: Uint8Array | null,
): boolean {
  if (markerText === null || binaryBytes === null) return false;
  const marker = parseProvenanceMarker(markerText);
  if (marker === null) return false;
  return marker.sha256 === createHash('sha256').update(binaryBytes).digest('hex');
}

async function setupStatusBody(
  options: SetupOptions,
  state: SetupStateBox,
  deps: {
    fs: ReturnType<typeof realSetupFs>;
    pathValue: string | undefined;
    separator: string;
    platform: string;
    arch: string;
    projectRoot: string | null;
    homes: { agents: string; claude: string };
  },
): Promise<Record<string, unknown>> {
  const managed = `${options.cliInstallDir}/${CLI_BINARY_NAME}`;
  const managedStat = await deps.fs.stat(managed);
  const markerText = managedStat !== null && managedStat.isFile
    ? await deps.fs.readFile(provenanceMarkerPath(managed))
    : null;
  const managedBytes = markerText !== null ? await deps.fs.readFileBytes(managed) : null;
  const resolved = await resolveCliBinary(deps.fs, options, deps.pathValue, deps.separator);
  const version = resolved === null ? null : parseVersionOutput((await spawnCliRunner(resolved).run(['version']))?.stdout ?? '');
  const onPath = await resolveAllOnPath(deps.fs, deps.pathValue, CLI_BINARY_NAME, deps.separator);
  const external = onPath.find((hit) => hit !== managed) ?? null;
  const report = await skillSourceTable(deps.fs, externalSkillRoots(deps.projectRoot, deps.homes), [...BUNDLED_SKILLS]);
  return {
    ok: true,
    enabled: true,
    busy: state.busy,
    cli: {
      ready: managedStat !== null && managedStat.isFile,
      path: managedStat !== null && managedStat.isFile ? managed : null,
      version,
      resolved,
      external,
      pluginInstalled: cliProvenanceCheck(markerText, managedBytes),
    },
    skills: { bundled: true, names: [...BUNDLED_SKILLS], report },
    lastError: state.lastError,
  };
}

async function runSetupEnable(
  options: SetupOptions,
  state: SetupStateBox,
  deps: {
    fs: ReturnType<typeof realSetupFs>;
    fetchLike: (url: string, init?: { signal?: AbortSignal }) => Promise<Response>;
    now: () => string;
    platform: string;
    arch: string;
  },
): Promise<Record<string, unknown>> {
  if (state.busy) return { ok: true, enabled: true, busy: true, note: '一次启用动作正在进行' };
  const asset = releaseAssetName(deps.platform, deps.arch);
  if (asset === null) return { ok: false, error: `不支持的平台或架构：${deps.platform}/${deps.arch}` };
  state.busy = true;
  state.lastError = null;
  try {
    const dest = `${options.cliInstallDir}/${CLI_BINARY_NAME}`;
    const existing = await deps.fs.stat(dest);
    // `latest` never re-downloads an existing binary; a concrete version
    // re-downloads (the panel shows whatever version is current). A binary
    // this plugin did not install keeps no marker and reports honestly as
    // non-plugin — the marker is only ever written by installBinary.
    const needDownload = existing === null || !existing.isFile || options.waVersion !== 'latest';
    if (needDownload) {
      const bytes = await downloadRelease(deps.fetchLike, { baseUrl: options.waBaseUrl, version: options.waVersion, asset });
      await installBinary(deps.fs, options.cliInstallDir, bytes, deps.now());
    }
    return { ok: true, enabled: true, installed: dest };
  } catch (error) {
    state.lastError = error instanceof Error ? error.message : String(error);
    return { ok: false, error: state.lastError };
  } finally {
    state.busy = false;
  }
}

// ---------------------------------------------------------------------------
// Tool table: one row per tool, hand-rolled so list entries and results can
// carry _meta.ui without betting on SDK high-level registration extras.

type JsonRecord = Record<string, unknown>;

/** Tool results are arbitrary structured objects (interfaces without index
 * signatures flow through here). */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type ToolPayload = Record<string, any>;

interface ToolRow {
  name: string;
  description: string;
  inputSchema: JsonRecord;
  /** 'model' | 'app' — who may call it; panel-only bookkeeping is app. */
  visibility: ('model' | 'app')[];
  readOnly: boolean;
  run(args: JsonRecord): Promise<{ result: ToolPayload; uiSurface?: string; uiNote?: string }>;
}

/** Decode a forwarded upstream body to UTF-8 text: structuredContent must
 * stay JSON-serializable (a Uint8Array would serialize as an indexed
 * object), and every forwarded endpoint answers JSON or a readable refusal. */
function decodeAnswerBody(body: Uint8Array | string): string {
  return typeof body === 'string' ? body : new TextDecoder().decode(body);
}

function toolError(message: string): { result: JsonRecord } {
  return { result: { ok: false, error: message } };
}

/** Tool result content: a text block mirroring the structured payload. */
function toolContent(result: JsonRecord): JsonRecord[] {
  return [{ type: 'text', text: JSON.stringify(result) }];
}

export interface ServerDeps {
  config: Record<string, unknown>;
  env: NodeJS.ProcessEnv;
  processCwd: () => string;
  now: () => string;
  /** Panel document location; the built server reads it next to itself. */
  panelHtmlPath: string | null;
}

/** Read the plugin userConfig out of the ZCODE_WV_* environment. */
export function configFromEnv(env: NodeJS.ProcessEnv): Record<string, unknown> {
  const config: Record<string, unknown> = {};
  if (typeof env.ZCODE_WV_WORKSPACE_ROOT === 'string' && env.ZCODE_WV_WORKSPACE_ROOT.trim() !== '') {
    config.workspaceRoot = env.ZCODE_WV_WORKSPACE_ROOT.trim();
  }
  if (typeof env.ZCODE_WV_WA_CLI_PATH === 'string' && env.ZCODE_WV_WA_CLI_PATH.trim() !== '') {
    config.waCliPath = env.ZCODE_WV_WA_CLI_PATH.trim();
  }
  if (env.ZCODE_WV_ENABLE_SETUP === 'true' || env.ZCODE_WV_ENABLE_SETUP === '1') {
    config.enableSetup = true;
  }
  for (const [envName, key] of [
    ['ZCODE_WV_WA_VERSION', 'waVersion'],
    ['ZCODE_WV_CLI_INSTALL_DIR', 'cliInstallDir'],
    ['ZCODE_WV_WA_BASE_URL', 'waBaseUrl'],
  ] as const) {
    const value = env[envName];
    if (typeof value === 'string' && value.trim() !== '') config[key] = value.trim();
  }
  return config;
}

/** Build the tool table around shared state. */
export function buildTools(
  deps: ServerDeps,
  sessions: ReviewSessions,
  viewer: ViewerState,
): ToolRow[] {
  const runnerOf = (): CliRunner => spawnCliRunner(normalizePathInput(deps.config.waCliPath) ?? 'wa-cli');
  const reviewRoot = reviewRootResolver(deps.config, deps.processCwd);
  const fetchLike: ProxyFetch = (url, init) =>
    fetch(url, { ...(init as RequestInit), signal: AbortSignal.timeout(REVIEW_UPSTREAM_TIMEOUT_MS) }) as Promise<{
      status: number;
      headers: Iterable<[string, string]>;
      arrayBuffer(): Promise<ArrayBuffer>;
    }>;

  const rows: ToolRow[] = [
    {
      name: 'workspace_status',
      description: 'Locate the writing-anything project directory: the located root, whether .writing-anything/ exists under it, and every candidate probed (userConfig pin, then the project ZCode opened).',
      inputSchema: { type: 'object', properties: {}, additionalProperties: false },
      visibility: ['model', 'app'],
      readOnly: true,
      async run() {
        return { result: await workspaceStatus(deps.config, deps.processCwd()) };
      },
    },
    {
      name: 'workspace_snapshot',
      description: 'Read-only workspace snapshot: config, works and unit trees (stage, progress, blockers), every artifact as full text, sources, decisions, packs, validate diagnostics, pending author confirmations/reviews, per-area degradation reasons. Optional root (absolute project path) is the panel path switch for this one call.',
      inputSchema: {
        type: 'object',
        properties: { root: { type: 'string', description: 'Absolute project directory to view instead of the default location' } },
        additionalProperties: false,
      },
      visibility: ['model', 'app'],
      readOnly: true,
      async run(args) {
        const root = typeof args.root === 'string' ? args.root : null;
        return { result: await fullSnapshot(runnerOf(), deps.config, deps.processCwd(), root) };
      },
    },
    {
      name: 'open_workspace',
      description: 'Open the Writing Workspace panel in ZCode (optionally focusing a unit or a named view: sources, decisions, reviews, review).',
      inputSchema: {
        type: 'object',
        properties: {
          unit: { type: 'string', description: 'Unit reference to focus, e.g. my-post or my-novel/scene-1' },
          view: { type: 'string', description: 'Named view: sources | decisions | reviews | review' },
        },
        additionalProperties: false,
      },
      visibility: ['model', 'app'],
      readOnly: true,
      async run(args) {
        const unit = typeof args.unit === 'string' && args.unit.trim() !== '' ? args.unit.trim() : undefined;
        const view = typeof args.view === 'string' && args.view.trim() !== '' ? args.view.trim() : undefined;
        if (unit !== undefined && view !== undefined) {
          return toolError('unit 与 view 只能二选一');
        }
        viewer.target = unit !== undefined ? { unit } : view !== undefined ? { view } : {};
        return {
          result: { ok: true, opened: true, surface: 'writing-workspace', ...(unit !== undefined ? { unit } : {}), ...(view !== undefined ? { view } : {}) },
          uiSurface: 'writing-workspace',
          uiNote: unit !== undefined ? `聚焦单元 ${unit}` : view !== undefined ? `打开${view}视图` : '打开面板',
        };
      },
    },
    {
      name: 'show_in_viewer',
      description: 'Point the Writing Workspace panel at a unit or a named list view (sources, decisions, reviews) so the author sees exactly what is being discussed.',
      inputSchema: {
        type: 'object',
        properties: {
          unit: { type: 'string', description: 'Unit reference to focus' },
          view: { type: 'string', description: 'Named view: sources | decisions | reviews' },
        },
        additionalProperties: false,
      },
      visibility: ['model', 'app'],
      readOnly: true,
      async run(args) {
        const unit = typeof args.unit === 'string' && args.unit.trim() !== '' ? args.unit.trim() : undefined;
        const view = typeof args.view === 'string' && args.view.trim() !== '' ? args.view.trim() : undefined;
        if (unit === undefined && view === undefined) return toolError('需要 unit 或 view 之一');
        viewer.target = unit !== undefined ? { unit } : { view };
        return {
          result: { ok: true, surface: 'writing-workspace', ...(unit !== undefined ? { unit } : {}), ...(view !== undefined ? { view } : {}) },
          uiSurface: 'writing-workspace',
          uiNote: unit !== undefined ? `定位到单元 ${unit}` : `定位到${view}视图`,
        };
      },
    },
    {
      name: 'get_view',
      description: 'What the author is currently looking at in the Writing Workspace panel: the project directory, the selected unit, the visible view, and any text they selected.',
      inputSchema: { type: 'object', properties: {}, additionalProperties: false },
      visibility: ['model', 'app'],
      readOnly: true,
      async run() {
        const report = viewer.current;
        return {
          result: {
            ok: true,
            hasPanel: report !== null,
            ...(report === null
              ? {}
              : {
                  root: report.root,
                  unit: report.unit,
                  view: report.view,
                  selection: report.selection,
                  reportedAt: report.at,
                }),
            ...(viewer.target !== null ? { pendingTarget: viewer.target } : {}),
          },
        };
      },
    },
    {
      name: 'report_view',
      description: 'Panel-only: the panel reports the author\'s current location and selection so get_view can answer. Not for model use.',
      inputSchema: {
        type: 'object',
        properties: {
          viewId: { type: 'string' },
          root: { type: ['string', 'null'] },
          unit: { type: ['string', 'null'] },
          view: { type: ['string', 'null'] },
          selection: {
            type: ['object', 'null'],
            properties: { unit: { type: 'string' }, text: { type: 'string' } },
            additionalProperties: false,
          },
        },
        additionalProperties: false,
      },
      visibility: ['app'],
      readOnly: false,
      async run(args) {
        const viewId = typeof args.viewId === 'string' && args.viewId !== '' ? args.viewId : 'default';
        const selection =
          args.selection !== null && args.selection !== undefined && typeof args.selection === 'object'
            ? {
                unit: String((args.selection as JsonRecord).unit ?? ''),
                text: String((args.selection as JsonRecord).text ?? ''),
              }
            : null;
        viewer.current = {
          viewId,
          root: typeof args.root === 'string' ? args.root : null,
          unit: typeof args.unit === 'string' ? args.unit : null,
          view: typeof args.view === 'string' ? args.view : null,
          selection: selection !== null && selection.text !== '' ? selection : null,
          at: deps.now(),
        };
        return { result: { ok: true } };
      },
    },
    {
      name: 'release_view',
      description: 'Panel-only: release a viewer report when its page leaves.',
      inputSchema: {
        type: 'object',
        properties: { viewId: { type: 'string' } },
        required: ['viewId'],
        additionalProperties: false,
      },
      visibility: ['app'],
      readOnly: false,
      async run(args) {
        const viewId = typeof args.viewId === 'string' ? args.viewId : '';
        if (viewer.current?.viewId === viewId) viewer.current = null;
        return { result: { ok: true } };
      },
    },
    {
      name: 'review_start',
      description: 'Start the review session for one unit: spawns wa-cli preview <unit> --serve once (10s first-line deadline), returns the session summary (never the token or port). Replaces any running session.',
      inputSchema: {
        type: 'object',
        properties: {
          unit: { type: 'string', description: 'Unit reference, e.g. my-post' },
          root: { type: 'string', description: 'Optional absolute project directory (the panel path switch value)' },
        },
        required: ['unit'],
        additionalProperties: false,
      },
      visibility: ['model', 'app'],
      readOnly: false,
      async run(args) {
        const unit = validReviewUnit(args.unit);
        if (unit === null) return toolError('无效的单元引用');
        const rootInput = typeof args.root === 'string' ? args.root : null;
        const located = await reviewRoot(rootInput);
        if (!located.ok) return toolError(located.error);
        const started = await sessions.start({ unit, root: located.root });
        return started.ok
          ? { result: { ok: true, session: started.session } }
          : { result: { ok: false, error: started.error, diagnostics: started.diagnostics } };
      },
    },
    {
      name: 'review_status',
      description: 'The running review session summary (unit, root, startedAt), or null. Never calls wa-cli.',
      inputSchema: { type: 'object', properties: {}, additionalProperties: false },
      visibility: ['model', 'app'],
      readOnly: true,
      async run() {
        return { result: { ok: true, session: sessions.status() } };
      },
    },
    {
      name: 'review_end',
      description: 'End the running review session and stop its wa-cli preview child.',
      inputSchema: { type: 'object', properties: {}, additionalProperties: false },
      visibility: ['model', 'app'],
      readOnly: false,
      async run() {
        sessions.end();
        return { result: { ok: true, session: null } };
      },
    },
    {
      name: 'review_page',
      description: 'The reading view page data (page.json) of the running review session, forwarded from its wa-cli child.',
      inputSchema: { type: 'object', properties: {}, additionalProperties: false },
      visibility: ['model', 'app'],
      readOnly: true,
      async run() {
        const answer = await proxyReview(reviewTargetOf('data'), sessions.upstream(), null, fetchLike);
        return { result: { ok: answer.status === 200, status: answer.status, headers: answer.headers, body: decodeAnswerBody(answer.body) } };
      },
    },
    {
      name: 'review_submit',
      description: 'Forward one review submit to the reading view of the running session. action is feedback, confirm or author-review; body is the exact JSON payload wa-cli\'s reading page posts. 64 KiB limit.',
      inputSchema: {
        type: 'object',
        properties: {
          action: { type: 'string', description: 'feedback | confirm | author-review' },
          body: { type: 'object', description: 'The JSON payload of the submit endpoint' },
        },
        required: ['action', 'body'],
        additionalProperties: false,
      },
      visibility: ['model', 'app'],
      readOnly: false,
      async run(args) {
        const target = reviewTargetOf('submit', { action: args.action });
        if (target.kind === 'unknown') return toolError(`未知的提交动作：${String(args.action)}`);
        let serialized: string;
        try {
          serialized = JSON.stringify(args.body ?? null);
        } catch {
          return toolError('请求体无法序列化');
        }
        const bytes = new TextEncoder().encode(serialized);
        const answer = await proxyReview(target, sessions.upstream(), bytes, fetchLike);
        return { result: { ok: answer.status === 200, status: answer.status, headers: answer.headers, body: decodeAnswerBody(answer.body) } };
      },
    },
  ];

  // The setup capability exists only behind its userConfig switch: with
  // enableSetup off there are no setup tools, no download action and no
  // network access — the plugin behaves exactly as before.
  const setupOptions = parseSetupConfig(deps.config, homedir());
  if (setupOptions.enabled) {
    const state: SetupStateBox = { busy: false, lastError: null };
    const setupFs = realSetupFs();
    const statusDeps = () => ({
      fs: setupFs,
      pathValue: deps.env.PATH,
      separator: pathDelimiter,
      platform: process.platform,
      arch: process.arch,
      projectRoot: null,
      homes: {
        agents: deps.env.ZCODE_AGENTS_HOME ?? `${homedir()}/.agents`,
        claude: deps.env.ZCODE_CLAUDE_HOME ?? `${homedir()}/.claude`,
      },
    });
    rows.push(
      {
        name: 'setup_status',
        description: 'Setup section status (only while enableSetup is on): built-in CLI readiness, resolved wa-cli path, provenance classification, and the bundled-vs-external skill source table. Optional root is the panel\'s current project directory for the project-level skill roots.',
        inputSchema: {
          type: 'object',
          properties: { root: { type: 'string', description: 'Absolute project directory (panel location) for project-level skill roots' } },
          additionalProperties: false,
        },
        visibility: ['model', 'app'],
        readOnly: true,
        async run(args) {
          const base = statusDeps();
          const rootInput = typeof args.root === 'string' ? args.root : null;
          const requested = parsePanelRoot(rootInput, { isAbsolute, resolve });
          return {
            result: await setupStatusBody(setupOptions, state, {
              ...base,
              projectRoot: requested.kind === 'ok' ? requested.path : null,
            }),
          };
        },
      },
      {
        name: 'setup_enable',
        description: 'Run the one-shot enable: download the wa-cli release asset for this platform, verify it against SHA256SUMS, atomically install into the configured directory with a provenance marker. Single-flight; no skills or PATH are touched.',
        inputSchema: { type: 'object', properties: {}, additionalProperties: false },
        visibility: ['model', 'app'],
        readOnly: false,
        async run() {
          return {
            result: await runSetupEnable(setupOptions, state, {
              fs: setupFs,
              fetchLike: (url, init) => fetch(url, init),
              now: deps.now,
              platform: process.platform,
              arch: process.arch,
            }),
          };
        },
      },
    );
  }
  return rows;
}

// ---------------------------------------------------------------------------
// Server assembly.

export function createMcpServer(deps: ServerDeps, sessions: ReviewSessions): Server {
  const server = new Server(
    { name: PLUGIN_ID, version: '0.1.0' },
    { capabilities: { tools: {}, resources: {} } },
  );
  const viewer = createViewerState();
  const tools = buildTools(deps, sessions, viewer);

  server.setRequestHandler(ListToolsRequestSchema, () => ({
    tools: tools.map((row) => ({
      name: row.name,
      description: row.description,
      inputSchema: row.inputSchema,
      annotations: { readOnlyHint: row.readOnly },
      _meta: { ui: { visibility: row.visibility } },
    })),
  }));

  server.setRequestHandler(CallToolRequestSchema, async (request) => {
    const row = tools.find((candidate) => candidate.name === request.params.name);
    if (row === undefined) {
      return {
        content: [{ type: 'text', text: `未知工具：${request.params.name}` }],
        isError: true,
      };
    }
    const args = (request.params.arguments ?? {}) as JsonRecord;
    let outcome: { result: JsonRecord; uiSurface?: string; uiNote?: string };
    try {
      outcome = await row.run(args);
    } catch (error) {
      outcome = toolError(error instanceof Error ? error.message : String(error));
    }
    const meta: JsonRecord = {};
    if (outcome.uiSurface !== undefined) meta.ui = { surface: outcome.uiSurface, ...(outcome.uiNote !== undefined ? { note: outcome.uiNote } : {}) };
    return {
      content: toolContent(outcome.result),
      structuredContent: outcome.result,
      ...(Object.keys(meta).length > 0 ? { _meta: meta } : {}),
    };
  });

  server.setRequestHandler(ListResourcesRequestSchema, () => ({
    resources: [
      {
        uri: PANEL_URI,
        name: 'Writing Workspace panel',
        mimeType: 'text/html;profile=mcp-app',
      },
      {
        uri: `${ILLUSTRATION_PREFIX}<unit>/<id>`,
        name: 'Review illustration image',
        mimeType: 'application/octet-stream',
      },
    ],
  }));

  server.setRequestHandler(ReadResourceRequestSchema, async (request) => {
    const uri = request.params.uri;
    if (uri === PANEL_URI) {
      if (deps.panelHtmlPath === null) {
        throw new Error('panel document is unavailable');
      }
      const text = await readFile(deps.panelHtmlPath, 'utf8');
      return {
        contents: [{ uri, mimeType: 'text/html;profile=mcp-app', text }],
      };
    }
    if (uri.startsWith(ILLUSTRATION_PREFIX)) {
      const rest = uri.slice(ILLUSTRATION_PREFIX.length);
      const slash = rest.indexOf('/');
      if (slash <= 0 || slash === rest.length - 1) {
        throw new Error('invalid illustration uri');
      }
      const unit = decodeURIComponent(rest.slice(0, slash));
      const id = decodeURIComponent(rest.slice(slash + 1));
      const target = reviewTargetOf('image', { unit, id });
      const fetchLike: ProxyFetch = (url, init) =>
        fetch(url, { ...(init as RequestInit), signal: AbortSignal.timeout(REVIEW_UPSTREAM_TIMEOUT_MS) }) as Promise<{
          status: number;
          headers: Iterable<[string, string]>;
          arrayBuffer(): Promise<ArrayBuffer>;
        }>;
      const answer = await proxyReview(target, sessions.upstream(), null, fetchLike);
      if (answer.status !== 200 || typeof answer.body === 'string') {
        throw new Error(`illustration unavailable (status ${answer.status})`);
      }
      return {
        contents: [
          {
            uri,
            mimeType: answer.headers['content-type'] ?? 'application/octet-stream',
            blob: Buffer.from(answer.body).toString('base64'),
          },
        ],
      };
    }
    throw new Error(`unknown resource: ${uri}`);
  });

  return server;
}

/** The built server's entry: real adapters, stdio transport, exit cleanup. */
export async function runServer(): Promise<void> {
  const here = fileURLToPath(new URL('.', import.meta.url));
  const deps: ServerDeps = {
    config: configFromEnv(process.env),
    env: process.env,
    processCwd: () => process.cwd(),
    now: () => new Date().toISOString(),
    // dist/panel.html sits next to dist/server.js in the installed plugin.
    panelHtmlPath: `${here}panel.html`,
  };
  const sessions = createReviewSessions({
    binary: normalizePathInput(deps.config.waCliPath) ?? 'wa-cli',
    spawn: spawnReviewChild,
    now: deps.now,
  });
  // The server process exiting never leaves a reading-view child behind.
  process.once('exit', () => sessions.dispose());
  const server = createMcpServer(deps, sessions);
  const transport = new StdioServerTransport();
  await server.connect(transport);
}

if (process.argv[1] !== undefined && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  void runServer();
}
