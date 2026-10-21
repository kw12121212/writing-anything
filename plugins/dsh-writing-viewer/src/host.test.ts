import { mkdtemp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { isAbsolute, join, resolve } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import {
  collectCandidates,
  apply,
  fullSnapshot,
  registerRoute,
  registerSnapshotRoute,
  rootQueryOf,
  workspaceExistsAt,
  workspaceSnapshot,
  type WorkspaceRouteBody,
} from './host';
import type { WorkspaceRegistryLike } from './registry';
import { parsePanelRoot, PANEL_ROOT_MAX_LENGTH } from './root';
import type { SnapshotRouteBody } from './snapshot';

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
  await Promise.all(tempRoots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
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
    await route.handler({ method: 'GET', headers: { host: '127.0.0.1:13080' } }, res);
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
    // Pin a binary that cannot exist so the assertion below does not depend
    // on whether the developer machine has wa-cli on PATH.
    apply(ctx, { waCliPath: '/nonexistent/wa-cli-for-tests' });
    expect(routes.map((route) => route.path)).toEqual([
      '/dsh-writing-viewer/workspace',
      '/dsh-writing-viewer/snapshot',
    ]);
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
    await routes[0]!.handler({ method: 'GET', headers: { host: '127.0.0.1:13080' } }, res);
    const body = JSON.parse(written[0]!.body!) as WorkspaceRouteBody;
    expect(body.root).toBe(project);
    expect(body.source).toBe('workspace-registry');

    // The snapshot route serves a readable body without wa-cli installed:
    // an empty workspace degrades per area but never fails the response.
    written.length = 0;
    await routes[1]!.handler({ method: 'GET', headers: { host: '127.0.0.1:13080' } }, res);
    const snapshotBody = JSON.parse(written[0]!.body!) as {
      ok: boolean;
      workspaceExists: boolean;
      degraded: string[];
      snapshot: { works: unknown[] } | null;
    };
    expect(snapshotBody.ok).toBe(true);
    expect(snapshotBody.workspaceExists).toBe(true);
    expect(snapshotBody.snapshot?.works).toEqual([]);
    expect(snapshotBody.degraded.length).toBe(3);
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
    expect(routes).toHaveLength(2);
    // A ctx without inject (older host shapes) must not throw.
    expect(() => apply({}, {})).not.toThrow();
  });
});

/** A wa-cli path that cannot exist, so tests never depend on the machine's PATH. */
const NO_CLI = { waCliPath: '/nonexistent/wa-cli-for-tests' };

describe('parsePanelRoot', () => {
  const pathApi = { isAbsolute, resolve };

  it('treats a missing or blank parameter as absent', () => {
    expect(parsePanelRoot(null, pathApi)).toEqual({ kind: 'absent' });
    expect(parsePanelRoot(undefined, pathApi)).toEqual({ kind: 'absent' });
    expect(parsePanelRoot('   ', pathApi)).toEqual({ kind: 'absent' });
  });

  it('accepts an absolute path and normalizes it', () => {
    expect(parsePanelRoot(' /tmp/a/../b/ ', pathApi)).toEqual({ kind: 'ok', path: '/tmp/b' });
  });

  it('rejects relative, NUL-carrying and oversized paths with a readable reason', () => {
    const relative = parsePanelRoot('projects/x', pathApi);
    expect(relative.kind).toBe('invalid');
    expect(relative.kind === 'invalid' && relative.reason).toContain('绝对路径');
    expect(parsePanelRoot('-rf', pathApi).kind).toBe('invalid');
    // UNC and device paths (and their POSIX look-alikes) are refused everywhere.
    for (const unc of ['\\\\server\\share', '//server/share', '\\\\?\\C:\\x', '\\/mixed']) {
      const result = parsePanelRoot(unc, pathApi);
      expect(result.kind, unc).toBe('invalid');
    }
    expect(parsePanelRoot('/tmp/a\u0000b', pathApi).kind).toBe('invalid');
    expect(parsePanelRoot('/' + 'a'.repeat(PANEL_ROOT_MAX_LENGTH), pathApi).kind).toBe('invalid');
  });
});

describe('rootQueryOf', () => {
  it('reads the root query parameter from the request url', () => {
    expect(rootQueryOf('/dsh-writing-viewer/snapshot?root=%2Ftmp%2Fmy%20project')).toBe('/tmp/my project');
    expect(rootQueryOf('/dsh-writing-viewer/snapshot')).toBeNull();
    expect(rootQueryOf(undefined)).toBeNull();
  });
});

describe('fullSnapshot with a panel root', () => {
  it('views the requested project ahead of the config pin and the registry', async () => {
    const other = await tempProject(true);
    const pinned = await tempProject(true);
    const body = await fullSnapshot(registryWith([pinned]), { workspaceRoot: pinned, ...NO_CLI }, '/nowhere', other + '/');
    expect(body.root).toBe(other);
    expect(body.source).toBe('panel-input');
    expect(body.workspaceExists).toBe(true);
    expect(body.candidates).toEqual([{ source: 'panel-input', path: other, workspaceExists: true }]);
    expect('rootError' in body).toBe(false);
  });

  it('answers the empty state with a reason for a relative path, without falling back', async () => {
    const project = await tempProject(true);
    const body = await fullSnapshot(registryWith([project]), NO_CLI, project, 'relative/path');
    expect(body.ok).toBe(true);
    expect(body.root).toBeNull();
    expect(body.workspaceExists).toBe(false);
    expect(body.candidates).toEqual([]);
    expect(body.emptyState).toEqual({ command: 'wa-cli init' });
    expect(body.rootError).toContain('绝对路径');
  });

  it('answers the empty state with a reason when the path has no workspace, without falling back', async () => {
    const empty = await tempProject(false);
    const project = await tempProject(true);
    const body = await fullSnapshot(registryWith([project]), NO_CLI, project, empty);
    expect(body.root).toBeNull();
    expect(body.snapshot).toBeNull();
    expect(body.candidates).toEqual([{ source: 'panel-input', path: empty, workspaceExists: false }]);
    expect(body.rootError).toContain('.writing-anything/');
    expect(await readdir(empty)).toEqual([]);
  });

  it('keeps the default resolution and body shape without the parameter', async () => {
    const project = await tempProject(true);
    const body = await fullSnapshot(registryWith([project]), NO_CLI, '/nowhere');
    expect(body.root).toBe(project);
    expect(body.source).toBe('workspace-registry');
    expect('rootError' in body).toBe(false);
  });
});

describe('snapshot route query wiring', () => {
  it('hands the root query parameter to the builder', async () => {
    const routes: { path: string; handler: (req: unknown, res: unknown) => Promise<void> }[] = [];
    const seen: (string | null)[] = [];
    registerSnapshotRoute(
      { webServer: { register: (route: (typeof routes)[number]) => routes.push(route) } } as Parameters<typeof registerSnapshotRoute>[0],
      async (query) => {
        seen.push(query.root);
        return { ok: true } as SnapshotRouteBody;
      },
    );
    const res = { writeHead: () => res, end: () => {} };
    const loopback = { host: 'localhost:13080' };
    await routes[0]!.handler({ method: 'GET', url: '/dsh-writing-viewer/snapshot?root=%2Fsrv%2Fbook', headers: loopback }, res);
    await routes[0]!.handler({ method: 'GET', url: '/dsh-writing-viewer/snapshot', headers: loopback }, res);
    await routes[0]!.handler({ method: 'GET', headers: loopback }, res);
    expect(seen).toEqual(['/srv/book', null, null]);
  });
});

describe('route origin guard', () => {
  /** Register both routes with explicit guard options and capture responses. */
  async function guardedRoutes(guardOptions: Parameters<typeof registerRoute>[2]) {
    const routes: { path: string; handler: (req: unknown, res: unknown) => Promise<void> }[] = [];
    const scope = { webServer: { register: (route: (typeof routes)[number]) => routes.push(route) } };
    registerRoute(scope as Parameters<typeof registerRoute>[0], async () => ({ ok: true }) as WorkspaceRouteBody, guardOptions);
    registerSnapshotRoute(
      scope as Parameters<typeof registerSnapshotRoute>[0],
      async () => ({ ok: true } as SnapshotRouteBody),
      guardOptions,
    );
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
    return { routes, written, res };
  }

  it('refuses an untrusted Host on both routes with 403, no-store, and no data', async () => {
    const { routes, written, res } = await guardedRoutes({ allowedHosts: [], bindHost: null });
    for (const route of routes) {
      written.length = 0;
      await route.handler({ method: 'GET', headers: { host: 'attacker.example' } }, res);
      expect(written[0]!.status).toBe(403);
      expect(written[0]!.headers?.['cache-control']).toBe('no-store');
      const body = JSON.parse(written[0]!.body!) as { ok: boolean; error: string };
      expect(body.ok).toBe(false);
      expect(typeof body.error).toBe('string');
    }
  });

  it('refuses missing Host headers and cross-site fetch markers, still serving loopback', async () => {
    const { routes, written, res } = await guardedRoutes({ allowedHosts: [], bindHost: null });
    const route = routes[0]!;
    written.length = 0;
    await route.handler({ method: 'GET', headers: {} }, res);
    expect(written[0]!.status).toBe(403);
    written.length = 0;
    await route.handler({ method: 'GET', headers: { host: '127.0.0.1:13080', 'sec-fetch-site': 'cross-site' } }, res);
    expect(written[0]!.status).toBe(403);
    written.length = 0;
    await route.handler({ method: 'GET', headers: { host: '127.0.0.1:13080' } }, res);
    expect(written[0]!.status).toBe(200);
  });

  it('refuses an Origin that names a different authority than the Host', async () => {
    const { routes, written, res } = await guardedRoutes({ allowedHosts: [], bindHost: null });
    written.length = 0;
    await routes[0]!.handler({ method: 'GET', headers: { host: '127.0.0.1:13080', origin: 'http://evil.example' } }, res);
    expect(written[0]!.status).toBe(403);
    written.length = 0;
    await routes[0]!.handler({ method: 'GET', headers: { host: '127.0.0.1:13080', origin: 'http://127.0.0.1:13080' } }, res);
    expect(written[0]!.status).toBe(200);
  });

  it('accepts an explicitly allowed remote authority and the bind-address literal', async () => {
    const { routes, written, res } = await guardedRoutes({
      allowedHosts: ['gamepc.example.net:13080', 'proxy.internal'],
      bindHost: '192.168.1.10',
    });
    const route = routes[0]!;
    written.length = 0;
    await route.handler({ method: 'GET', headers: { host: 'gamepc.example.net:13080' } }, res);
    expect(written[0]!.status).toBe(200);
    written.length = 0;
    // Port-less entries match the hostname on any port.
    await route.handler({ method: 'GET', headers: { host: 'proxy.internal:9999' } }, res);
    expect(written[0]!.status).toBe(200);
    written.length = 0;
    // The bind address literal is accepted regardless of port.
    await route.handler({ method: 'GET', headers: { host: '192.168.1.10:13080' } }, res);
    expect(written[0]!.status).toBe(200);
    written.length = 0;
    // An allowed host on a different port when the entry pinned one is not.
    await route.handler({ method: 'GET', headers: { host: 'gamepc.example.net:9999' } }, res);
    expect(written[0]!.status).toBe(403);
  });

  it('collectAllowedHosts and bindHostOf tolerate arbitrary config and scope shapes', async () => {
    const { collectAllowedHosts, bindHostOf } = await import('./host');
    expect(collectAllowedHosts(undefined)).toEqual([]);
    expect(collectAllowedHosts(['a.example', ' ', 3, 'b.example:8080'])).toEqual(['a.example', 'b.example:8080']);
    expect(bindHostOf({ webServer: { host: '127.0.0.1' } })).toBe('127.0.0.1');
    expect(bindHostOf({ host: '0.0.0.0' })).toBe('0.0.0.0');
    expect(bindHostOf({ webServer: {} })).toBeNull();
    expect(bindHostOf({})).toBeNull();
  });
});

