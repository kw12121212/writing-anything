/**
 * End-to-end review proxy check against a real wa-cli binary.
 *
 * Builds wa-cli from this repository (skipped when `go` is unavailable),
 * creates a workspace with one drafted work, mounts the real review routes on
 * a node:http server that dispatches like the DSH host webServer (exact
 * first, then longest prefix), and drives one full session: start, proxied
 * page, proxied feedback submit, end. The workspace afterwards differs from
 * its starting state only by the feedback the author submitted and wa-cli's
 * own audit log.
 */
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from 'node:fs';
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { reviewRootResolver, spawnReviewChild } from './host';
import { registerReviewRoutes } from './reviewroutes';
import { createReviewSessions, type ReviewSessions } from './reviewsession';
import type { ProxyFetch } from './reviewproxy';

const REPO = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..');

function hasGo(): boolean {
  try {
    execFileSync('go', ['version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

/** Relative path → content hash for every file under the workspace. */
function inventory(root: string): Map<string, string> {
  const out = new Map<string, string>();
  const walk = (dir: string, rel: string): void => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const abs = join(dir, entry.name);
      const relPath = rel === '' ? entry.name : `${rel}/${entry.name}`;
      if (entry.isDirectory()) walk(abs, relPath);
      else if (entry.isFile()) out.set(relPath, `${statSync(abs).mtimeMs}:${createHash('sha256').update(readFileSync(abs)).digest('hex')}`);
    }
  };
  walk(root, '');
  return out;
}

describe.skipIf(!hasGo())('review proxy end to end (real wa-cli)', () => {
  let tmp = '';
  let waCli = '';
  let project = '';
  let server: Server;
  let base = '';
  let sessions: ReviewSessions;

  beforeAll(async () => {
    tmp = mkdtempSync(join(tmpdir(), 'dsh-review-e2e-'));
    waCli = join(tmp, 'wa-cli');
    execFileSync('go', ['build', '-o', waCli, './cmd/wa-cli'], { cwd: REPO, stdio: 'ignore' });
    project = join(tmp, 'book');
    execFileSync(waCli, ['init', '--dir', project], { stdio: 'ignore' });
    execFileSync(waCli, ['new', 'minimal', 'my-post', '--dir', project], { stdio: 'ignore' });
    execFileSync(waCli, ['draft', 'write', 'my-post', '--dir', project], { input: '连接池很重要。连接池要复用。\n', stdio: ['pipe', 'ignore', 'ignore'] });

    sessions = createReviewSessions({ binary: waCli, spawn: spawnReviewChild, now: () => new Date().toISOString() });
    const routes: { kind: string; path: string; handler: (req: IncomingMessage, res: ServerResponse) => Promise<void> }[] = [];
    registerReviewRoutes({ register: (route) => routes.push(route as (typeof routes)[number]) }, {
      sessions,
      resolveRoot: reviewRootResolver(() => null, undefined, () => tmp),
      fetchLike: ((url, init) => fetch(url, init as RequestInit)) as ProxyFetch,
      guardOptions: { allowedHosts: [], bindHost: '127.0.0.1' },
    });
    server = createServer((req, res) => {
      const pathname = new URL(req.url ?? '/', 'http://x').pathname;
      const route =
        routes.find((r) => r.kind === 'exact' && r.path === pathname) ??
        routes.find((r) => r.kind === 'prefix' && (pathname === r.path || pathname.startsWith(r.path + '/')));
      if (route === undefined) {
        res.writeHead(404).end();
        return;
      }
      void route.handler(req, res);
    });
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
    const address = server.address() as { port: number };
    base = `http://127.0.0.1:${address.port}`;
  }, 120_000);

  afterAll(async () => {
    sessions?.dispose();
    await new Promise<void>((resolve) => (server === undefined ? resolve() : server.close(() => resolve())));
    if (tmp !== '') rmSync(tmp, { recursive: true, force: true });
  });

  it('starts a session, proxies the page and a feedback submit, then ends it', async () => {
    const workspace = join(project, '.writing-anything');
    const before = inventory(workspace);

    const started = await fetch(`${base}/dsh-writing-viewer/review-session`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ unit: 'my-post', root: project }),
    });
    const startBody = (await started.json()) as { ok: boolean; session: { unit: string; root: string } };
    expect(startBody.ok, JSON.stringify(startBody)).toBe(true);
    expect(startBody.session).toMatchObject({ unit: 'my-post', root: project });
    expect(JSON.stringify(startBody)).not.toMatch(/token|127\.0\.0\.1/);

    const page = await fetch(`${base}/dsh-writing-viewer/review/`);
    const html = await page.text();
    expect(page.status).toBe(200);
    expect(html).toContain('连接池很重要');
    expect(html).toContain("fetch('feedback'");
    expect(page.headers.get('x-frame-options')).toBeNull();
    expect(page.headers.get('content-security-policy')).toContain("frame-ancestors 'self'");
    expect(page.headers.get('content-security-policy')).toContain("script-src 'sha256-");
    expect(html).not.toContain('127.0.0.1');
    const token = sessions.upstream()?.token;
    expect(token).toBeTruthy();
    expect(html).not.toContain(token as string);

    // Browse-only so far: nothing but wa-cli's audit log may have changed.
    const browsed = inventory(workspace);
    for (const [path, stamp] of before) if (path !== 'audit.log') expect(browsed.get(path), path).toBe(stamp);
    expect([...browsed.keys()].filter((p) => !before.has(p) && p !== 'audit.log')).toEqual([]);

    // The page posts by relative path, which resolves under the proxy prefix.
    const submit = new URL('feedback', `${base}/dsh-writing-viewer/review/`).toString();
    const saved = await fetch(submit, {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'x-wa-token': 'forged' },
      body: JSON.stringify({ unit: 'my-post', quote: '连接池很重要', text: '补一个例子' }),
    });
    const savedBody = (await saved.json()) as { valid: boolean; feedback: { id: string } | null };
    expect(saved.status, JSON.stringify(savedBody)).toBe(200);
    expect(savedBody.feedback?.id).toBe('fb-1');
    const listed = execFileSync(waCli, ['feedback', 'list', 'my-post', '--dir', project, '--json'], { encoding: 'utf8' });
    expect(listed).toContain('补一个例子');

    // A confirmation through the proxy is recorded exactly like one made on the
    // reading view directly: confirmation channel preview (proxy-confirm-channel).
    execFileSync(waCli, ['decision', 'add', '--question', '标题用哪个？', '--option', '甲', '--option', '乙', '--unit', 'my-post', '--id', 'dec-1', '--dir', project], { stdio: 'ignore' });
    const confirmUrl = new URL('confirm', `${base}/dsh-writing-viewer/review/`).toString();
    const confirmed = await fetch(confirmUrl, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ kind: 'decision', id: 'dec-1', option: 'a', confirmation: '作者：用甲' }),
    });
    const confirmedBody = (await confirmed.json()) as { valid: boolean; confirmation: { kind: string; id: string; channel: string } | null };
    expect(confirmed.status, JSON.stringify(confirmedBody)).toBe(200);
    expect(confirmedBody.confirmation).toMatchObject({ kind: 'decision', id: 'dec-1', channel: 'preview' });
    expect(readFileSync(join(workspace, 'decisions.yaml'), 'utf8')).toContain('channel: preview');

    const ended = await fetch(`${base}/dsh-writing-viewer/review-session/end`, { method: 'POST' });
    expect(((await ended.json()) as { session: unknown }).session).toBeNull();
    const gone = await fetch(`${base}/dsh-writing-viewer/review/`);
    expect(gone.status).toBe(409);
    expect(await gone.text()).toContain('没有进行中的审阅');

    // Apart from the submitted feedback and the audit log, the workspace is unchanged.
    const after = inventory(workspace);
    const changed = [...after.keys()].filter((path) => after.get(path) !== before.get(path));
    // (decisions.yaml was created by the test's own wa-cli decision add, then confirmed through the proxy.)
    expect(changed.filter((path) => path !== 'audit.log').sort()).toEqual(['decisions.yaml', 'works/my-post/feedback.yaml']);
  }, 60_000);
});
