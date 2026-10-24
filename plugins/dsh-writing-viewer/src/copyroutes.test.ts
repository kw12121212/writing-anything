/**
 * The copy route driven through a real node:http server with the host's
 * dispatch semantics (dsh-plugin-structure/host-route-dispatch-harness),
 * against a recording wa-cli runner double.
 */
import { createServer, request, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import type { AddressInfo } from 'node:net';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { COPY_ROUTE, copyArgs, copyBodyOf, registerCopyRoute, validCopyWork } from './copyroutes';
import { COPY_STDOUT_CAP, spawnCliRunner } from './host';
import type { CliRunner } from './wacli';

/** A raw GET with headers fetch would drop (Host, Sec-Fetch-Site). */
function rawGet(url: string, headers: Record<string, string>): Promise<{ status: number; cacheControl: string | undefined; body: string }> {
  return new Promise((resolve, reject) => {
    const req = request(url, { method: 'GET', headers }, (res) => {
      let body = '';
      res.setEncoding('utf8');
      res.on('data', (chunk: string) => (body += chunk));
      res.on('end', () => resolve({ status: res.statusCode ?? 0, cacheControl: res.headers['cache-control'], body }));
    });
    req.on('error', reject);
    req.end();
  });
}

function listen(server: Server): Promise<number> {
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve((server.address() as AddressInfo).port)));
}

const FRAGMENT = '<section style="x"><p style="y">正文</p><section style=""><img src="data:image/png;base64,AA" alt="图" style=""><p style="">图说</p></section></section>';

describe('copy route through the host dispatch', () => {
  const runs: string[][] = [];
  let stdout: string | null = '';
  let front: Server;
  let base = '';

  beforeAll(async () => {
    const runner: CliRunner = {
      run: async (args) => {
        runs.push([...args]);
        return stdout === null ? null : { stdout };
      },
    };
    const routes: { kind: string; path: string; handler: (req: IncomingMessage, res: ServerResponse) => Promise<void> }[] = [];
    registerCopyRoute({ register: (route) => routes.push(route as (typeof routes)[number]) }, {
      runner,
      resolveRoot: async (root) => (root === 'relative' ? { ok: false, error: '项目路径必须是绝对路径：relative' } : { ok: true, root: root ?? '/default' }),
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
  });

  it('runs wa-cli once with the read-only paste-html arguments and returns only the fragment (copy-accepted-work)', async () => {
    runs.length = 0;
    stdout = JSON.stringify({
      valid: true, diagnostics: [], changedPaths: [],
      export: { work: 'my-post', format: 'paste-html', path: null, html: FRAGMENT, images: [{ index: 1 }, { index: 2 }], assets: [], disclosure: { text: 'AI' } },
    });
    const response = await fetch(`${base}${COPY_ROUTE}?work=my-post&root=%2Fhome%2Fa`);
    expect(response.status).toBe(200);
    expect(response.headers.get('cache-control')).toBe('no-store');
    expect(await response.json()).toEqual({ ok: true, html: FRAGMENT, images: 2, segments: [FRAGMENT], diagnostics: [] });
    expect(runs).toEqual([['export', 'my-post', '--format', 'paste-html', '--print', '--json', '--dir', '/home/a']]);
  });

  it('passes wa-cli diagnostics through in their real order and reports the error when nothing is accepted (copy-nothing-accepted)', async () => {
    runs.length = 0;
    // Real wa-cli order: export.unaccepted warnings first, the deciding error last.
    stdout = JSON.stringify({
      valid: false, changedPaths: [], export: null,
      diagnostics: [
        { code: 'export.unaccepted', severity: 'warning', message: '单元 my-post 没有导出：尚未验收', path: '.writing-anything/works/my-post', next: ['wa-cli review show my-post --dir /default --json'] },
        { code: 'export.nothing-accepted', severity: 'error', message: '没有已验收的单元', next: ['wa-cli next --dir /default --json', 'wa-cli status --dir /default --json'] },
      ],
    });
    const body = await (await fetch(`${base}${COPY_ROUTE}?work=my-post`)).json();
    expect(body).toEqual({
      ok: false, error: '没有已验收的单元',
      diagnostics: [
        { code: 'export.unaccepted', severity: 'warning', message: '单元 my-post 没有导出：尚未验收', next: ['wa-cli review show my-post --dir /default --json'] },
        { code: 'export.nothing-accepted', severity: 'error', message: '没有已验收的单元', next: ['wa-cli next --dir /default --json', 'wa-cli status --dir /default --json'] },
      ],
    });
    expect(runs).toEqual([copyArgs('my-post', '/default')]);
  });

  it('refuses cross-site and foreign-host requests without calling wa-cli (copy-route-origin-guard)', async () => {
    runs.length = 0;
    const headerSets: Record<string, string>[] = [{ 'sec-fetch-site': 'cross-site' }, { host: 'evil.example:80' }, { origin: 'http://evil.example' }];
    for (const headers of headerSets) {
      const response = await rawGet(`${base}${COPY_ROUTE}?work=my-post`, headers);
      expect(response.status).toBe(403);
      expect(response.cacheControl).toBe('no-store');
      expect(JSON.parse(response.body).ok).toBe(false);
    }
    expect(runs).toEqual([]);
  });

  it('rejects bad work references, a bad root and other methods without calling wa-cli (copy-bad-work-ref)', async () => {
    runs.length = 0;
    for (const work of ['-rf', 'a%0Ab', '', 'my-post%2Fintro']) {
      const response = await fetch(`${base}${COPY_ROUTE}?work=${work}`);
      expect(response.status).toBe(400);
      expect(await response.json()).toEqual({ ok: false, error: 'work 必须是作品 id', diagnostics: [] });
    }
    const badRoot = await fetch(`${base}${COPY_ROUTE}?work=my-post&root=relative`);
    expect([badRoot.status, (await badRoot.json()).error]).toEqual([400, '项目路径必须是绝对路径：relative']);
    const post = await fetch(`${base}${COPY_ROUTE}?work=my-post`, { method: 'POST' });
    expect([post.status, post.headers.get('allow')]).toEqual([405, 'GET']);
    expect(runs).toEqual([]);
  });

  it('answers readable failures when wa-cli is unavailable or its output does not parse', async () => {
    stdout = null;
    expect((await (await fetch(`${base}${COPY_ROUTE}?work=my-post`)).json()).error).toBe('wa-cli 不可用、超时或输出超过插件的体积上限');
    stdout = '{"valid": tr';
    expect((await (await fetch(`${base}${COPY_ROUTE}?work=my-post`)).json()).error).toBe('wa-cli 的输出无法解析（可能超过插件的体积上限）');
  });
});

describe('copy route output budget', () => {
  it('fits the largest --print envelope wa-cli can produce (two copies of 32 MiB as escaped base64)', () => {
    const embedBudget = 32 * 1024 * 1024; // export.PasteEmbedBudget
    const base64 = Math.ceil(embedBudget / 3) * 4;
    const escaped = base64 * (1 + 9 / 64); // "+" → "\u0026#43;" for one character in 64
    expect(COPY_STDOUT_CAP).toBeGreaterThan(2 * escaped + 1024 * 1024);
  });
});

describe('copy route helpers', () => {
  it('validates work references like the review guard and refuses sub-units', () => {
    expect(validCopyWork(' my-post ')).toBe('my-post');
    for (const bad of [undefined, null, 3, '', '-x', 'a\u0007b', 'my-post/intro', 'x'.repeat(513)]) expect(validCopyWork(bad)).toBeNull();
  });

  it('maps an envelope without html to its first diagnostic message', () => {
    expect(copyBodyOf(JSON.stringify({ diagnostics: [], export: null }))).toEqual({ ok: false, error: 'wa-cli 没有给出可粘贴正文', diagnostics: [] });
    expect(copyBodyOf('null')).toEqual({ ok: false, error: 'wa-cli 没有给出可粘贴正文', diagnostics: [] });
    expect(copyBodyOf(JSON.stringify({ export: { html: '<p>x</p>' } }))).toEqual({ ok: true, html: '<p>x</p>', images: 0, segments: ['<p>x</p>'], diagnostics: [] });
    expect(copyBodyOf(JSON.stringify({ export: { html: '<p>a</p><p>b</p>', segments: ['<p>a</p>', '<p>b</p>'], images: [] } }))).toEqual({ ok: true, html: '<p>a</p><p>b</p>', images: 0, segments: ['<p>a</p>', '<p>b</p>'], diagnostics: [] });
  });
});

describe('spawnCliRunner output decoding', () => {
  // A child that writes a long CJK text in small writes, so multi-byte
  // characters straddle pipe chunk boundaries.
  const script = "const s='配图正文'.repeat(40000); let i=0; (function w(){ while(i<s.length){ const part=Buffer.from(s.slice(i,i+7)); i+=7; if(!process.stdout.write(part.subarray(0,5))||!process.stdout.write(part.subarray(5))){ process.stdout.once('drain',w); return; } } })();";

  it('decodes the collected bytes once, never garbling split characters', async () => {
    const outcome = await spawnCliRunner(process.execPath).run(['-e', script]);
    expect(outcome?.stdout).toBe('配图正文'.repeat(40000));
  });

  it('caps the captured bytes and honours the timeout', async () => {
    const capped = await spawnCliRunner(process.execPath, { stdoutCap: 1000 }).run(['-e', script]);
    expect(Buffer.byteLength(capped?.stdout ?? '', 'utf8')).toBeLessThanOrEqual(1000 + 3);
    const slow = await spawnCliRunner(process.execPath, { timeoutMs: 200 }).run(['-e', 'setTimeout(()=>{}, 5000)']);
    expect(slow).toBeNull();
  });
});
