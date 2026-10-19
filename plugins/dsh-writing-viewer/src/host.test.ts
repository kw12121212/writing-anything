import { mkdtemp, mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import {
  collectCandidates,
  apply,
  registerRoute,
  workspaceExistsAt,
  workspaceSnapshot,
  type WorkspaceRouteBody,
} from './host';
import type { WorkspaceRegistryLike } from './registry';

const tempRoots: string[] = [];

async function tempProject(withWorkspace: boolean): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), 'wa-viewer-'));
  tempRoots.push(root);
  if (withWorkspace) {
    await mkdir(join(root, '.writing-anything'));
  }
  return root;
}

afterEach(async () => {
  await Promise.all(tempRoots.splice(0).map(() => Promise.resolve()));
});

function registryWith(paths: string[]): WorkspaceRegistryLike {
  return {
    list: () => paths.map((path, i) => ({ id: 'w' + i, path, title: 'W' + i })),
  };
}

describe('collectCandidates', () => {
  it('pins exclusively to a configured workspaceRoot', () => {
    const candidates = collectCandidates({ workspaceRoot: '/configured' }, ['/r1', '/r2'], '/home/me');
    expect(candidates).toEqual([{ source: 'config', path: '/configured' }]);
  });

  it('orders registry paths, then process cwd, without config', () => {
    const candidates = collectCandidates(undefined, ['/r1', '/r2'], '/home/me');
    expect(candidates).toEqual([
      { source: 'workspace-registry', path: '/r1' },
      { source: 'workspace-registry', path: '/r2' },
      { source: 'process-cwd', path: '/home/me' },
    ]);
  });

  it('keeps the registry source when cwd names the same path', () => {
    const candidates = collectCandidates(undefined, ['/same'], '/same');
    expect(candidates).toEqual([{ source: 'workspace-registry', path: '/same' }]);
  });

  it('skips unconfigured and blank values without phantom candidates', () => {
    expect(collectCandidates(undefined, ['  ', '/r'], '/cwd')).toEqual([
      { source: 'workspace-registry', path: '/r' },
      { source: 'process-cwd', path: '/cwd' },
    ]);
  });
});

describe('workspaceExistsAt', () => {
  it('finds a real .writing-anything directory and nothing else', async () => {
    const withWorkspace = await tempProject(true);
    const withoutWorkspace = await tempProject(false);
    expect(await workspaceExistsAt(withWorkspace)).toBe(true);
    expect(await workspaceExistsAt(withoutWorkspace)).toBe(false);
    expect(await workspaceExistsAt(join(withoutWorkspace, 'missing'))).toBe(false);
  });

  it('does not accept a plain file named .writing-anything', async () => {
    const root = await tempProject(false);
    await writeFile(join(root, '.writing-anything'), 'not a directory');
    expect(await workspaceExistsAt(root)).toBe(false);
  });
});

describe('workspaceSnapshot', () => {
  it('locates the registry project that has a workspace, with the probe trail', async () => {
    const hit = await tempProject(true);
    const miss = await tempProject(false);
    const body = await workspaceSnapshot(registryWith([miss, hit]), undefined, '/nowhere');
    expect(body.root).toBe(hit);
    expect(body.source).toBe('workspace-registry');
    expect(body.workspaceExists).toBe(true);
    expect(body.workspaceDir).toBe(join(hit, '.writing-anything'));
    expect(body.emptyState).toBeNull();
    expect(body.candidates).toEqual([
      { source: 'workspace-registry', path: miss, workspaceExists: false },
      { source: 'workspace-registry', path: hit, workspaceExists: true },
    ]);
  });

  it('lets an explicit config override win over the registry', async () => {
    const configured = await tempProject(true);
    const registry = await tempProject(true);
    const body = await workspaceSnapshot(registryWith([registry]), { workspaceRoot: configured }, '/nowhere');
    expect(body.root).toBe(configured);
    expect(body.source).toBe('config');
    expect(body.candidates).toEqual([{ source: 'config', path: configured, workspaceExists: true }]);
  });

  it('serves the empty state when the pinned root has no workspace, even though the registry has one', async () => {
    const pinned = await tempProject(false);
    const registryHit = await tempProject(true);
    const body = await workspaceSnapshot(registryWith([registryHit]), { workspaceRoot: pinned }, '/nowhere');
    expect(body.root).toBeNull();
    expect(body.workspaceExists).toBe(false);
    expect(body.emptyState).toEqual({ command: 'wa-cli init' });
    expect(body.candidates).toEqual([{ source: 'config', path: pinned, workspaceExists: false }]);
  });

  it('serves the empty state with the wa-cli init hint when nothing matches', async () => {
    const miss = await tempProject(false);
    const body = await workspaceSnapshot(registryWith([miss]), undefined, miss);
    expect(body.root).toBeNull();
    expect(body.workspaceExists).toBe(false);
    expect(body.emptyState).toEqual({ command: 'wa-cli init' });
  });

  it('falls back to the process cwd when the registry is unavailable', async () => {
    const cwd = await tempProject(true);
    const body = await workspaceSnapshot(null, undefined, cwd);
    expect(body.root).toBe(cwd);
    expect(body.source).toBe('process-cwd');
  });
});

describe('registerRoute', () => {
  it('registers a read-only GET route and serves the snapshot as JSON', async () => {
    const routes: { name: string; kind: string; path: string; handler: (req: unknown, res: unknown) => Promise<void> }[] = [];
    const server = { register: (route: (typeof routes)[number]) => routes.push(route) };
    const snapshotRoot = await tempProject(true);
    registerRoute({ webServer: server } as unknown as Parameters<typeof registerRoute>[0], async () => ({
      ok: true,
      root: snapshotRoot,
      source: 'config',
      workspaceDir: join(snapshotRoot, '.writing-anything'),
      workspaceExists: true,
      candidates: [],
      emptyState: null,
    }));
    expect(routes).toHaveLength(1);
    const route = routes[0]!;
    expect(route.name).toBe('dsh-writing-viewer-workspace');
    expect(route.kind).toBe('exact');
    expect(route.path).toBe('/dsh-writing-viewer/workspace');

    const written: { status: number; headers?: Record<string, string>; body?: string }[] = [];
    const res = {
      writeHead: (status: number, headers?: Record<string, string>) => {
        written.push({ status, headers });
        return res;
      },
      end: (body?: string) => {
        written[written.length - 1]!.body = body;
      },
    };
    await route.handler({ method: 'GET' }, res);
    const served = JSON.parse(written[0]!.body!) as WorkspaceRouteBody;
    expect(served.root).toBe(snapshotRoot);
    expect(written[0]!.headers?.['content-type']).toBe('application/json; charset=utf-8');

    written.length = 0;
    await route.handler({ method: 'POST' }, res);
    expect(written[0]!.status).toBe(405);
  });

  it('keeps the plugin read-only: no workspace file changes across a snapshot', async () => {
    const project = await tempProject(true);
    const marker = join(project, '.writing-anything', 'sentinel');
    await writeFile(marker, 'keep');
    const entriesBefore = await readdir(join(project, '.writing-anything'));
    await workspaceSnapshot(registryWith([project]), undefined, '/nowhere');
    const entriesAfter = await readdir(join(project, '.writing-anything'));
    expect(entriesAfter).toEqual(entriesBefore);
    expect(await readFile(marker, 'utf8')).toBe('keep');
  });
});

describe('apply wiring', () => {
  it('resolves both service-scope shapes and serves snapshots through the registered route', async () => {
    const project = await tempProject(true);
    const routes: { path: string; handler: (req: unknown, res: unknown) => Promise<void> }[] = [];
    const scopes: Record<string, unknown> = {
      // workspaceRegistry arrives as a property on the scoped context.
      workspaceRegistry: { workspaceRegistry: registryWith([project]) },
      // webServer arrives as the scoped context itself.
      webServer: { register: (route: (typeof routes)[number]) => routes.push(route) },
    };
    const ctx = {
      inject: (names: string[], closure: (scope: unknown) => void) => {
        for (const name of names) {
          if (name in scopes) closure(scopes[name]!);
        }
      },
    };
    apply(ctx, {});
    expect(routes).toHaveLength(1);
    expect(routes[0]!.path).toBe('/dsh-writing-viewer/workspace');
    const written: { status: number; body?: string }[] = [];
    const res = {
      writeHead: (status: number) => {
        written.push({ status });
        return res;
      },
      end: (body?: string) => {
        written[written.length - 1]!.body = body;
      },
    };
    await routes[0]!.handler({ method: 'GET' }, res);
    const body = JSON.parse(written[0]!.body!) as WorkspaceRouteBody;
    expect(body.root).toBe(project);
    expect(body.source).toBe('workspace-registry');
  });

  it('ignores a registry service without list and stays quiet without inject at all', async () => {
    const routes: unknown[] = [];
    const scopes: Record<string, unknown> = {
      workspaceRegistry: { workspaceRegistry: { notList: true } },
      webServer: { register: (route: unknown) => routes.push(route) },
    };
    const seen: string[][] = [];
    const ctx = {
      inject: (names: string[], closure: (scope: unknown) => void) => {
        seen.push(names);
        for (const name of names) {
          if (name in scopes) closure(scopes[name]!);
        }
      },
    };
    apply(ctx, {});
    expect(seen).toEqual([['workspaceRegistry'], ['webServer']]);
    expect(routes).toHaveLength(1);
    // A ctx without inject (older host shapes) must not throw.
    expect(() => apply({}, {})).not.toThrow();
  });
});
