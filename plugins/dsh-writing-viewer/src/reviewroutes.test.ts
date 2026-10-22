import { describe, expect, it } from 'vitest';
import { parseStartBody, registerReviewRoutes, REVIEW_SESSION_END_PATH, REVIEW_SESSION_PATH, type ReviewRouteDeps } from './reviewroutes';
import { REVIEW_PREFIX, type ProxyFetch } from './reviewproxy';
import type { ReviewSessions, ReviewStartResult } from './reviewsession';

interface Route {
  name: string;
  kind: string;
  path: string;
  handler: (req: unknown, res: unknown) => Promise<void>;
}

/** Recording sessions double: start answers whatever the test queues. */
function fakeSessions(startResult: ReviewStartResult = { ok: true, session: { unit: 'my-post', root: '/r', startedAt: 't0' } }) {
  const calls: string[] = [];
  let running = false;
  const sessions: ReviewSessions = {
    async start(request) {
      calls.push(`start ${request.unit} ${request.root}`);
      running = startResult.ok;
      return startResult;
    },
    status: () => (running ? { unit: 'my-post', root: '/r', startedAt: 't0' } : null),
    upstream: () => {
      calls.push('upstream');
      return running ? { port: 5000, token: 'tok' } : null;
    },
    end: () => {
      calls.push('end');
      running = false;
    },
    dispose: () => calls.push('dispose'),
  };
  return { sessions, calls, setRunning: (value: boolean) => (running = value) };
}

function setup(overrides: Partial<ReviewRouteDeps> = {}, sessions = fakeSessions()) {
  const routes: Route[] = [];
  const upstreamCalls: string[] = [];
  const fetchLike: ProxyFetch = async (url) => {
    upstreamCalls.push(url);
    return { status: 200, headers: [['content-type', 'application/json; charset=utf-8']], arrayBuffer: async () => new TextEncoder().encode('{"units":[]}').buffer as ArrayBuffer };
  };
  registerReviewRoutes({ register: (route) => routes.push(route as Route) }, {
    sessions: sessions.sessions,
    resolveRoot: async (root) => (root === 'relative/path' ? { ok: false, error: '项目路径必须是绝对路径：relative/path' } : { ok: true, root: root ?? '/default' }),
    fetchLike,
    guardOptions: { allowedHosts: ['gamepc.tail1234.ts.net:13080'], bindHost: '127.0.0.1' },
    ...overrides,
  });
  const call = async (path: string, method: string, opts: { body?: string; headers?: Record<string, string>; url?: string } = {}) => {
    const route = routes.find((r) => (r.kind === 'exact' ? r.path === path : path === r.path || path.startsWith(r.path + '/')));
    if (route === undefined) throw new Error('no route for ' + path);
    const chunks = opts.body === undefined ? [] : [Buffer.from(opts.body, 'utf8')];
    const req = {
      method,
      url: opts.url ?? path,
      headers: { host: '127.0.0.1:13080', ...(opts.headers ?? {}) },
      async *[Symbol.asyncIterator]() {
        yield* chunks;
      },
    };
    let status = 0;
    let headers: Record<string, string> = {};
    let body = '';
    const res = {
      writeHead: (s: number, h?: Record<string, string>) => {
        status = s;
        headers = h ?? {};
        return { end: (b?: string | Uint8Array) => (body = b === undefined ? '' : typeof b === 'string' ? b : Buffer.from(b).toString('utf8')) };
      },
    };
    await route.handler(req, res);
    return { status, headers, body, json: () => JSON.parse(body) };
  };
  return { routes, call, upstreamCalls, sessions };
}

describe('review route registration', () => {
  it('registers the session routes as exact paths and the proxy as one prefix route', () => {
    const { routes } = setup();
    expect(routes.map((r) => [r.kind, r.path])).toEqual([
      ['exact', REVIEW_SESSION_PATH],
      ['exact', REVIEW_SESSION_END_PATH],
      ['prefix', REVIEW_PREFIX],
    ]);
  });
});

describe('review session routes', () => {
  it('starts a session from the panel body and reports it (start-ok)', async () => {
    const { call, sessions } = setup();
    const started = await call(REVIEW_SESSION_PATH, 'POST', { body: JSON.stringify({ unit: 'my-post', root: '/home/me/book' }) });
    expect(started.status).toBe(200);
    expect(started.json()).toEqual({ ok: true, session: { unit: 'my-post', root: '/r', startedAt: 't0' } });
    expect(sessions.calls).toContain('start my-post /home/me/book');
    const status = await call(REVIEW_SESSION_PATH, 'GET');
    expect(status.json()).toEqual({ ok: true, session: { unit: 'my-post', root: '/r', startedAt: 't0' } });
  });

  it('uses the default location when the body names no root', async () => {
    const { call, sessions } = setup();
    await call(REVIEW_SESSION_PATH, 'POST', { body: JSON.stringify({ unit: 'my-post' }) });
    expect(sessions.calls).toContain('start my-post /default');
  });

  it('refuses an invalid root with 400 and never starts wa-cli (start-bad-root)', async () => {
    const { call, sessions } = setup();
    const answer = await call(REVIEW_SESSION_PATH, 'POST', { body: JSON.stringify({ unit: 'my-post', root: 'relative/path' }) });
    expect(answer.status).toBe(400);
    expect(answer.json().error).toContain('绝对路径');
    expect(sessions.calls.some((c) => c.startsWith('start'))).toBe(false);
  });

  it('refuses malformed bodies and flag-shaped units with 400', async () => {
    const { call, sessions } = setup();
    for (const body of ['not json', '[]', '{"unit":"--output=x"}', '{"unit":"my-post","root":7}', '{}']) {
      expect((await call(REVIEW_SESSION_PATH, 'POST', { body })).status, body).toBe(400);
    }
    expect((await call(REVIEW_SESSION_PATH, 'POST', { body: 'x'.repeat(70 * 1024) })).status).toBe(400);
    expect(sessions.calls.some((c) => c.startsWith('start'))).toBe(false);
  });

  it('passes a wa-cli start failure through as readable ok:false (start-bad-unit)', async () => {
    const failing = fakeSessions({ ok: false, error: '找不到单元 nope', diagnostics: [{ code: 'unit.not-found', message: '找不到单元 nope' }] });
    const { call } = setup({}, failing);
    const answer = await call(REVIEW_SESSION_PATH, 'POST', { body: JSON.stringify({ unit: 'nope' }) });
    expect(answer.status).toBe(200);
    expect(answer.json()).toEqual({ ok: false, error: '找不到单元 nope', diagnostics: [{ code: 'unit.not-found', message: '找不到单元 nope' }] });
  });

  it('ends the session on POST end and refuses other methods (end-explicit)', async () => {
    const { call, sessions } = setup();
    await call(REVIEW_SESSION_PATH, 'POST', { body: JSON.stringify({ unit: 'my-post' }) });
    const ended = await call(REVIEW_SESSION_END_PATH, 'POST');
    expect(ended.json()).toEqual({ ok: true, session: null });
    expect(sessions.calls).toContain('end');
    expect((await call(REVIEW_SESSION_PATH, 'GET')).json().session).toBeNull();
    expect((await call(REVIEW_SESSION_END_PATH, 'GET')).status).toBe(405);
    expect((await call(REVIEW_SESSION_PATH, 'DELETE')).status).toBe(405);
  });
});

describe('review proxy route', () => {
  it('proxies page.json of the running session, 409s without one and 404s the HTML page', async () => {
    const { call, upstreamCalls, sessions } = setup();
    const none = await call(REVIEW_PREFIX + '/page.json', 'GET');
    expect(none.status).toBe(409);
    expect(none.json().diagnostics[0].code).toBe('review.no-session');
    sessions.setRunning(true);
    const page = await call(REVIEW_PREFIX + '/page.json', 'GET', { url: REVIEW_PREFIX + '/page.json?token=browser-forged' });
    expect(page.status).toBe(200);
    expect(page.body).toBe('{"units":[]}');
    expect(upstreamCalls).toEqual(['http://127.0.0.1:5000/page.json?token=tok']);
    const image = await call(REVIEW_PREFIX + '/illustration', 'GET', { url: REVIEW_PREFIX + '/illustration?unit=my-post&id=fig-1&token=browser-forged' });
    expect(image.status).toBe(200);
    expect(upstreamCalls[1]).toBe('http://127.0.0.1:5000/illustration?unit=my-post&id=fig-1&token=tok');
    upstreamCalls.splice(1, 1);
    expect((await call(REVIEW_PREFIX + '/', 'GET')).status).toBe(404);
    expect((await call(REVIEW_PREFIX + '/nope', 'GET')).status).toBe(404);
    expect(upstreamCalls).toHaveLength(1);
  });
});

describe('review routes origin guard', () => {
  it('refuses a foreign Host on every review route without starting or forwarding (review-routes-guarded)', async () => {
    const { call, sessions, upstreamCalls } = setup();
    sessions.setRunning(true);
    const evil = { host: 'attacker.example' };
    for (const [path, method] of [
      [REVIEW_SESSION_PATH, 'POST'],
      [REVIEW_SESSION_PATH, 'GET'],
      [REVIEW_SESSION_END_PATH, 'POST'],
      [REVIEW_PREFIX + '/page.json', 'GET'],
      [REVIEW_PREFIX + '/illustration', 'GET'],
      [REVIEW_PREFIX + '/confirm', 'POST'],
    ] as const) {
      const answer = await call(path, method, { headers: evil, body: '{"unit":"my-post"}' });
      expect(answer.status, `${method} ${path}`).toBe(403);
      expect(answer.headers['cache-control']).toBe('no-store');
    }
    expect(sessions.calls.filter((c) => c.startsWith('start') || c === 'end' || c === 'upstream')).toEqual([]);
    expect(upstreamCalls).toEqual([]);
  });

  it('refuses a cross-site marker and a mismatched Origin', async () => {
    const { call } = setup();
    expect((await call(REVIEW_SESSION_PATH, 'POST', { headers: { 'sec-fetch-site': 'cross-site' }, body: '{"unit":"my-post"}' })).status).toBe(403);
    expect((await call(REVIEW_PREFIX + '/feedback', 'POST', { headers: { origin: 'http://evil.example' }, body: '{}' })).status).toBe(403);
  });

  it('accepts a Tailscale MagicDNS authority listed in allowedHosts (tailscale-magicdns-allowed)', async () => {
    const { call, sessions } = setup();
    const tailnet = { host: 'gamepc.tail1234.ts.net:13080', origin: 'http://gamepc.tail1234.ts.net:13080' };
    const started = await call(REVIEW_SESSION_PATH, 'POST', { headers: tailnet, body: JSON.stringify({ unit: 'my-post' }) });
    expect(started.status).toBe(200);
    expect(sessions.calls).toContain('start my-post /default');
    const page = await call(REVIEW_PREFIX + '/page.json', 'GET', { headers: { host: tailnet.host } });
    expect(page.status).toBe(200);
  });

  it('refuses the same MagicDNS authority when allowedHosts does not list it (origin-rejected flow)', async () => {
    const { call } = setup({ guardOptions: { allowedHosts: [], bindHost: '127.0.0.1' } });
    const answer = await call(REVIEW_SESSION_PATH, 'POST', { headers: { host: 'gamepc.tail1234.ts.net:13080' }, body: '{"unit":"my-post"}' });
    expect(answer.status).toBe(403);
    expect(answer.json().ok).toBe(false);
  });
});

describe('review route failures', () => {
  it('answers readable 500 JSON when a handler throws', async () => {
    const throwing = fakeSessions();
    throwing.sessions.start = async () => {
      throw new Error('boom');
    };
    const { call } = setup({}, throwing);
    const answer = await call(REVIEW_SESSION_PATH, 'POST', { body: JSON.stringify({ unit: 'my-post' }) });
    expect(answer.status).toBe(500);
    expect(answer.json()).toEqual({ ok: false, error: '审阅路由处理失败：boom' });
  });
});

describe('start body parsing', () => {
  it('parses unit and optional root', () => {
    expect(parseStartBody(Buffer.from('{"unit":"my-post","root":"/r"}'))).toEqual({ ok: true, unit: 'my-post', root: '/r' });
    expect(parseStartBody(Buffer.from('{"unit":"my-post","root":null}'))).toEqual({ ok: true, unit: 'my-post', root: null });
    expect(parseStartBody(null)).toEqual({ ok: false, error: '请求体超过 64 KiB' });
  });
});
