/**
 * The illustration image route through the host's dispatch semantics: the
 * real review routes mounted on a node:http server that dispatches like the
 * DSH webServer (exact first, then longest prefix, handlers get the native
 * req/res), forwarding with the real fetch to a loopback stand-in for the
 * wa-cli reading view service that answers GET /illustration with fixed
 * PNG bytes (dsh-plugin-structure/host-route-dispatch-harness).
 */
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import type { AddressInfo } from 'node:net';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { REVIEW_PREFIX, type ProxyFetch } from './reviewproxy';
import { registerReviewRoutes } from './reviewroutes';
import type { ReviewSessions } from './reviewsession';

const TOKEN = 'session-token-feedc0ffee';
const PNG = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01, 0xfe, 0xff, 0x80, 0x7f]);

function listen(server: Server): Promise<number> {
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve((server.address() as AddressInfo).port)));
}

describe('illustration route through the host dispatch', () => {
  const upstreamRequests: { method: string; url: string }[] = [];
  let upstream: Server;
  let front: Server;
  let base = '';
  let running = true;

  beforeAll(async () => {
    // Stand-in for wa-cli preview --serve: token-checked, image bytes or 404.
    upstream = createServer((req, res) => {
      upstreamRequests.push({ method: req.method ?? '', url: req.url ?? '' });
      const url = new URL(req.url ?? '/', 'http://x');
      if (url.pathname !== '/illustration' || url.searchParams.get('token') !== TOKEN) {
        res.writeHead(403).end();
        return;
      }
      if (url.searchParams.get('unit') === 'my-post' && url.searchParams.get('id') === 'fig-1') {
        res.writeHead(200, { 'content-type': 'image/png', 'cache-control': 'no-store', 'x-content-type-options': 'nosniff' }).end(req.method === 'HEAD' ? undefined : PNG);
        return;
      }
      res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8', 'cache-control': 'no-store' }).end('配图不存在');
    });
    const upstreamPort = await listen(upstream);
    const sessions: ReviewSessions = {
      start: async () => ({ ok: false, error: 'unused', diagnostics: [] }),
      status: () => (running ? { unit: 'my-post', root: '/r', startedAt: 't0' } : null),
      upstream: () => (running ? { port: upstreamPort, token: TOKEN } : null),
      end: () => {
        running = false;
      },
      dispose: () => {},
    };
    const routes: { kind: string; path: string; handler: (req: IncomingMessage, res: ServerResponse) => Promise<void> }[] = [];
    registerReviewRoutes({ register: (route) => routes.push(route as (typeof routes)[number]) }, {
      sessions,
      resolveRoot: async () => ({ ok: true, root: '/r' }),
      fetchLike: ((url, init) => fetch(url, init as RequestInit)) as ProxyFetch,
      guardOptions: { allowedHosts: [], bindHost: '127.0.0.1' },
    });
    front = createServer((req, res) => {
      const pathname = new URL(req.url ?? '/', 'http://x').pathname;
      const route =
        routes.find((r) => r.kind === 'exact' && r.path === pathname) ??
        routes.filter((r) => r.kind === 'prefix' && (pathname === r.path || pathname.startsWith(r.path + '/'))).sort((a, b) => b.path.length - a.path.length)[0];
      if (route === undefined) {
        res.writeHead(404).end();
        return;
      }
      void route.handler(req, res);
    });
    base = `http://127.0.0.1:${await listen(front)}`;
  });

  afterAll(async () => {
    await new Promise((resolve) => front.close(resolve));
    await new Promise((resolve) => upstream.close(resolve));
  });

  it('returns the exact image bytes, drops browser parameters and never exposes the token (proxy-illustration)', async () => {
    upstreamRequests.length = 0;
    const response = await fetch(`${base}${REVIEW_PREFIX}/illustration?unit=my-post&id=fig-1&token=forged&extra=1`);
    expect(response.status).toBe(200);
    expect(response.headers.get('content-type')).toBe('image/png');
    expect(response.headers.get('x-content-type-options')).toBe('nosniff');
    expect(Buffer.from(await response.arrayBuffer()).equals(PNG)).toBe(true);
    expect(upstreamRequests).toEqual([{ method: 'GET', url: `/illustration?unit=my-post&id=fig-1&token=${TOKEN}` }]);
    for (const [, value] of response.headers) {
      expect(value).not.toContain(TOKEN);
      expect(value).not.toContain('127.0.0.1');
    }
  });

  it('answers HEAD and passes a 404 through unchanged (proxy-illustration-not-found)', async () => {
    const head = await fetch(`${base}${REVIEW_PREFIX}/illustration?unit=my-post&id=fig-1`, { method: 'HEAD' });
    expect(head.status).toBe(200);
    expect(head.headers.get('content-type')).toBe('image/png');
    const ghost = await fetch(`${base}${REVIEW_PREFIX}/illustration?unit=my-post&id=ghost`);
    expect(ghost.status).toBe(404);
    expect(await ghost.text()).toBe('配图不存在');
  });

  it('refuses a foreign Host before reaching the upstream', async () => {
    upstreamRequests.length = 0;
    const { request } = await import('node:http');
    const status = await new Promise<number>((resolve, reject) => {
      const url = new URL(`${base}${REVIEW_PREFIX}/illustration?unit=my-post&id=fig-1`);
      const req = request({ host: url.hostname, port: url.port, path: url.pathname + url.search, headers: { host: 'attacker.example' } }, (res) => {
        res.resume();
        resolve(res.statusCode ?? 0);
      });
      req.on('error', reject);
      req.end();
    });
    expect(status).toBe(403);
    expect(upstreamRequests).toEqual([]);
  });

  it('answers the no-session notice without any upstream request (proxy-illustration-no-session)', async () => {
    upstreamRequests.length = 0;
    running = false;
    try {
      const response = await fetch(`${base}${REVIEW_PREFIX}/illustration?unit=my-post&id=fig-1`);
      expect(response.status).toBe(409);
      expect(await response.text()).toContain('没有进行中的审阅');
      expect(upstreamRequests).toEqual([]);
    } finally {
      running = true;
    }
  });
});
