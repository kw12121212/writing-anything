/**
 * Server tool-surface tests: dispatch through buildTools with injected
 * adapters — no real wa-cli, no network, no filesystem writes.
 */
import { describe, expect, it } from 'vitest';
import { buildTools, configFromEnv, createViewerState, workspaceStatus, collectCandidates } from './server';
import { createReviewSessions, type ReviewChild, type ReviewSessions } from './reviewsession';
import type { CliRunner } from './wacli';

const FIXTURE_ROOT = new URL('../testdata/workspace', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1');

function fakeRunner(stdoutFor: (args: readonly string[]) => string | null): CliRunner {
  return {
    run: async (args) => {
      const out = stdoutFor(args);
      return out === null ? null : { stdout: out };
    },
  };
}

/** A CliRunner the snapshot tools never reach: these tests stub fullSnapshot by
 * calling the tools with a fixture cwd. */
const cannedRunner = fakeRunner((args) => {
  const read = (name: string): string => {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const fs = require('node:fs') as typeof import('node:fs');
    return fs.readFileSync(new URL(`../testdata/cli/${name}`, import.meta.url), 'utf8');
  };
  if (args[0] === 'status') return read('status.json');
  if (args[0] === 'validate') return read('validate.json');
  if (args[0] === 'next') return read('next.json');
  if (args[0] === 'timeline') return read(`timeline-${args[1]}.json`);
  return null;
});

function childOf(stdoutFirstLine: string): ReviewChild {
  const listeners: { data?: (chunk: string) => void; close?: (code: number | null) => void; error?: (error: unknown) => void } = {};
  let killed = false;
  return {
    stdout: { on: (_event: 'data', listener: (chunk: string) => void) => { listeners.data = listener; } },
    on(event: 'close' | 'error', listener: (value: never) => void) {
      if (event === 'close') listeners.close = listener as (code: number | null) => void;
      else listeners.error = listener as (error: unknown) => void;
    },
    kill() {
      killed = true;
      listeners.close?.(0);
    },
  } as unknown as ReviewChild & { emit(): void; killed(): boolean };
}

function sessionsWith(upstream: { port: number; token: string } | null): ReviewSessions {
  if (upstream === null) {
    return createReviewSessions({ binary: 'wa-cli', spawn: () => childOf(''), now: () => '2026-10-23T00:00:00Z' });
  }
  const url = `http://127.0.0.1:${upstream.port}/?token=${upstream.token}`;
  return createReviewSessions({ binary: 'wa-cli', spawn: () => childOf(JSON.stringify({ preview: { url } })), now: () => '2026-10-23T00:00:00Z' });
}

function depsOf(config: Record<string, unknown> = {}, cwd = FIXTURE_ROOT) {
  return { config, env: {}, processCwd: () => cwd, now: () => '2026-10-23T00:00:00Z', panelHtmlPath: null };
}

async function call(rows: ReturnType<typeof buildTools>, name: string, args: Record<string, unknown> = {}) {
  const row = rows.find((candidate) => candidate.name === name);
  if (row === undefined) throw new Error(`no tool ${name}`);
  return (await row.run(args)).result;
}

describe('tool surface', () => {
  it('lists viewer, review tools with model+app visibility and panel-only report tools', () => {
    const rows = buildTools(depsOf(), sessionsWith(null), createViewerState());
    const byName = new Map(rows.map((row) => [row.name, row] as const));
    for (const name of ['workspace_status', 'workspace_snapshot', 'open_workspace', 'show_in_viewer', 'get_view', 'review_start', 'review_status', 'review_end', 'review_page', 'review_submit']) {
      expect(byName.get(name), name).toBeDefined();
      expect(byName.get(name)!.visibility).toEqual(['model', 'app']);
    }
    expect(byName.get('report_view')!.visibility).toEqual(['app']);
    expect(byName.get('release_view')!.visibility).toEqual(['app']);
  });

  it('registers no setup tools while enableSetup is off', () => {
    const rows = buildTools(depsOf(), sessionsWith(null), createViewerState());
    expect(rows.some((row) => row.name.startsWith('setup_'))).toBe(false);
  });

  it('registers setup tools behind the switch', () => {
    const rows = buildTools(depsOf({ enableSetup: true }), sessionsWith(null), createViewerState());
    expect(rows.filter((row) => row.name.startsWith('setup_')).map((row) => row.name).sort()).toEqual(['setup_enable', 'setup_status']);
  });

  it('workspace_status locates the fixture and records the probe trail', async () => {
    const body = await workspaceStatus(undefined, FIXTURE_ROOT);
    expect(body.workspaceExists).toBe(true);
    expect(body.root).toBeTruthy();
    expect(body.source).toBe('server-cwd');
    expect(body.emptyState).toBeNull();
  });

  it('a configured workspaceRoot is an exclusive pin', () => {
    const candidates = collectCandidates({ workspaceRoot: '/pinned' }, '/cwd');
    expect(candidates).toEqual([{ source: 'config', path: '/pinned' }]);
  });

  it('workspace_snapshot aggregates the fixture through the tool', async () => {
    const rows = buildTools(depsOf(undefined, FIXTURE_ROOT), sessionsWith(null), createViewerState());
    // Snapshot uses the real spawn runner; wa-cli is likely unavailable in the
    // test env, which exercises the degradation path instead of failing.
    const result = (await call(rows, 'workspace_snapshot')) as { ok: boolean; root: string | null; snapshot: { works: { id: string }[] } | null; degraded: string[] };
    expect(result.ok).toBe(true);
    expect(result.root).toBeTruthy();
    expect(result.snapshot?.works.map((w) => w.id)).toEqual(['my-novel', 'my-post']);
  });

  it('workspace_snapshot refuses an invalid panel root without reading anything', async () => {
    const rows = buildTools(depsOf(undefined, FIXTURE_ROOT), sessionsWith(null), createViewerState());
    const result = (await call(rows, 'workspace_snapshot', { root: 'relative/path' })) as { rootError: string };
    expect(result.rootError).toContain('绝对路径');
  });

  it('open_workspace marks its result with the surface the panel reacts to', async () => {
    const viewer = createViewerState();
    const rows = buildTools(depsOf(), sessionsWith(null), viewer);
    const outcome = rows.find((row) => row.name === 'open_workspace')!;
    const answer = await outcome.run({ unit: 'my-post' });
    expect(answer.uiSurface).toBe('writing-workspace');
    expect((answer.result as { surface: string }).surface).toBe('writing-workspace');
    expect(viewer.target).toEqual({ unit: 'my-post' });
  });

  it('report_view records what get_view answers', async () => {
    const viewer = createViewerState();
    const rows = buildTools(depsOf(), sessionsWith(null), viewer);
    await call(rows, 'report_view', { viewId: 'v1', root: '/r', unit: 'my-post', view: 'overview' });
    const seen = (await call(rows, 'get_view')) as { hasPanel: boolean; unit: string | null; root: string | null };
    expect(seen.hasPanel).toBe(true);
    expect(seen.unit).toBe('my-post');
    expect(seen.root).toBe('/r');
    await call(rows, 'release_view', { viewId: 'v1' });
    const after = (await call(rows, 'get_view')) as { hasPanel: boolean };
    expect(after.hasPanel).toBe(false);
  });

  it('review_start rejects flag-shaped and control-character unit refs without spawning', async () => {
    const rows = buildTools(depsOf(undefined, FIXTURE_ROOT), sessionsWith(null), createViewerState());
    for (const unit of ['--output=x', 'a\nb', '']) {
      const result = (await call(rows, 'review_start', { unit })) as { ok: boolean; error: string };
      expect(result.ok).toBe(false);
    }
  });

  it('review_submit refuses unknown actions without touching the network', async () => {
    const rows = buildTools(depsOf(), sessionsWith(null), createViewerState());
    const result = (await call(rows, 'review_submit', { action: 'feedback2', body: {} })) as { ok: boolean; error: string };
    expect(result.ok).toBe(false);
    expect(result.error).toContain('未知');
  });

  it('review_page and review_submit answer the no-session refusal readably', async () => {
    const rows = buildTools(depsOf(), sessionsWith(null), createViewerState());
    const page = (await call(rows, 'review_page')) as { ok: boolean; body: string };
    expect(page.ok).toBe(false);
    expect(String(page.body)).toContain('没有进行中的审阅');
    const submit = (await call(rows, 'review_submit', { action: 'feedback', body: { unit: 'my-post' } })) as { ok: boolean; body: string };
    expect(submit.ok).toBe(false);
    expect(String(submit.body)).toContain('没有进行中的审阅');
  });
});

describe('configFromEnv', () => {
  it('maps ZCODE_WV_* variables tolerantly', () => {
    const config = configFromEnv({
      ZCODE_WV_WORKSPACE_ROOT: ' /proj ',
      ZCODE_WV_ENABLE_SETUP: 'true',
      ZCODE_WV_WA_VERSION: 'v0.3.0',
      ZCODE_WV_CLI_INSTALL_DIR: '',
    });
    expect(config).toEqual({ workspaceRoot: '/proj', enableSetup: true, waVersion: 'v0.3.0' });
  });

  it('keeps setup off unless explicitly true', () => {
    expect(configFromEnv({ ZCODE_WV_ENABLE_SETUP: 'false' })).toEqual({});
    expect(configFromEnv({})).toEqual({});
  });
});
