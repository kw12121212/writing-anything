import { describe, expect, it } from 'vitest';
import { proxiedHeaders, proxyReview, readBody, REVIEW_BODY_LIMIT, reviewTargetOf, type ProxyFetch } from './reviewproxy';

const UPSTREAM_CSP =
  "default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-AbC='; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

const PAGE_JSON = '{"title":"my-post","units":[{"ref":"my-post","paragraphs":[{"text":"连接池很重要"}]}]}';

/** Records every upstream call and answers like wa-cli's reading view service. */
function fakeUpstream(status = 200, body = PAGE_JSON, headers: [string, string][] = []) {
  const calls: { url: string; init: Parameters<ProxyFetch>[1] }[] = [];
  const fetchLike: ProxyFetch = async (url, init) => {
    calls.push({ url, init });
    return {
      status,
      headers: [
        ['Content-Type', 'application/json; charset=utf-8'],
        ['Cache-Control', 'no-store'],
        ['Referrer-Policy', 'no-referrer'],
        ['X-Frame-Options', 'DENY'],
        ['X-Content-Type-Options', 'nosniff'],
        ['Content-Security-Policy', UPSTREAM_CSP],
        ['Content-Length', String(Buffer.byteLength(body))],
        ...headers,
      ] as [string, string][],
      arrayBuffer: async () => new TextEncoder().encode(body).buffer as ArrayBuffer,
    };
  };
  return { fetchLike, calls };
}

const UP = { port: 41234, token: 'session-token-0123456789abcdef' };

async function* chunks(...parts: string[]): AsyncGenerator<Buffer> {
  for (const part of parts) yield Buffer.from(part, 'utf8');
}

describe('review proxy targets', () => {
  it('maps only page.json and the three submit paths under the prefix; the HTML page is unknown', () => {
    expect(reviewTargetOf('/dsh-writing-viewer/review/page.json')).toEqual({ kind: 'data' });
    expect(reviewTargetOf('/dsh-writing-viewer/review')).toEqual({ kind: 'unknown' });
    expect(reviewTargetOf('/dsh-writing-viewer/review/')).toEqual({ kind: 'unknown' });
    expect(reviewTargetOf('/dsh-writing-viewer/review/index.html')).toEqual({ kind: 'unknown' });
    expect(reviewTargetOf('/dsh-writing-viewer/review/feedback')).toEqual({ kind: 'submit', path: 'feedback' });
    expect(reviewTargetOf('/dsh-writing-viewer/review/confirm')).toEqual({ kind: 'submit', path: 'confirm' });
    expect(reviewTargetOf('/dsh-writing-viewer/review/author-review')).toEqual({ kind: 'submit', path: 'author-review' });
    for (const other of ['/dsh-writing-viewer/review/../setup', '/dsh-writing-viewer/review/feedback/x', '/dsh-writing-viewer/review/favicon.ico', '/dsh-writing-viewer/reviewx', '/']) {
      expect(reviewTargetOf(other), other).toEqual({ kind: 'unknown' });
    }
  });
});

describe('review proxy headers', () => {
  it('drops only hop headers and leaves security headers as wa-cli sent them', () => {
    const out = proxiedHeaders([
      ['X-Frame-Options', 'DENY'],
      ['Content-Security-Policy', UPSTREAM_CSP],
      ['Content-Length', '10'],
      ['Content-Encoding', 'gzip'],
      ['Transfer-Encoding', 'chunked'],
      ['Cache-Control', 'no-store'],
      ['Referrer-Policy', 'no-referrer'],
    ]);
    expect(out['x-frame-options']).toBe('DENY');
    expect(out['content-length']).toBeUndefined();
    expect(out['content-encoding']).toBeUndefined();
    expect(out['transfer-encoding']).toBeUndefined();
    expect(out['content-security-policy']).toBe(UPSTREAM_CSP);
    expect(out['cache-control']).toBe('no-store');
    expect(out['referrer-policy']).toBe('no-referrer');
  });
});

describe('review proxy forwarding', () => {
  it('forwards page.json with the session token and passes the body through byte for byte (proxy-page-json)', async () => {
    const { fetchLike, calls } = fakeUpstream();
    const answer = await proxyReview('GET', { kind: 'data' }, UP, null, fetchLike);
    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe('http://127.0.0.1:41234/page.json?token=session-token-0123456789abcdef');
    expect(calls[0]!.init.method).toBe('GET');
    expect(calls[0]!.init.headers).toEqual({});
    expect(answer.status).toBe(200);
    expect(Buffer.from(answer.body as Uint8Array).toString('utf8')).toBe(PAGE_JSON);
    expect(answer.headers['content-type']).toBe('application/json; charset=utf-8');
    expect(answer.headers['content-security-policy']).toBe(UPSTREAM_CSP);
    // The browser sees neither the token nor the loopback address.
    expect(JSON.stringify(answer.headers)).not.toContain(UP.token);
    expect(JSON.stringify(answer.headers)).not.toContain('127.0.0.1');
  });

  it('forwards a submit with the injected token, never a browser-sent one (proxy-feedback, proxy-forged-token-ignored)', async () => {
    const { fetchLike, calls } = fakeUpstream(200, '{"valid":true,"feedback":{"id":"fb-1"}}');
    const body = Buffer.from('{"unit":"my-post","quote":"连接池很重要","text":"补一个例子"}', 'utf8');
    const answer = await proxyReview('POST', { kind: 'submit', path: 'feedback' }, UP, body, fetchLike);
    expect(calls[0]!.url).toBe('http://127.0.0.1:41234/feedback');
    expect(calls[0]!.init.headers).toEqual({ 'content-type': 'application/json', 'x-wa-token': UP.token });
    expect(Buffer.from(calls[0]!.init.body as Uint8Array).toString('utf8')).toBe(body.toString('utf8'));
    expect(answer.status).toBe(200);
    expect(Buffer.from(answer.body as Uint8Array).toString('utf8')).toContain('fb-1');
  });

  it('passes upstream refusals through unchanged (status and body)', async () => {
    const { fetchLike } = fakeUpstream(400, '{"valid":false,"diagnostics":[{"code":"feedback.ambiguous-quote"}]}');
    const answer = await proxyReview('POST', { kind: 'submit', path: 'feedback' }, UP, Buffer.from('{}'), fetchLike);
    expect(answer.status).toBe(400);
    expect(Buffer.from(answer.body as Uint8Array).toString('utf8')).toContain('feedback.ambiguous-quote');
  });

  it('answers a readable JSON notice without touching any upstream when there is no session (proxy-no-session)', async () => {
    const { fetchLike, calls } = fakeUpstream();
    const page = await proxyReview('GET', { kind: 'data' }, null, null, fetchLike);
    expect(page.status).toBe(409);
    expect(page.headers['content-type']).toBe('application/json; charset=utf-8');
    expect(JSON.parse(String(page.body)).diagnostics[0]).toMatchObject({ code: 'review.no-session' });
    expect(String(page.body)).toContain('没有进行中的审阅');
    const submit = await proxyReview('POST', { kind: 'submit', path: 'feedback' }, null, Buffer.from('{}'), fetchLike);
    expect(submit.status).toBe(409);
    const parsed = JSON.parse(String(submit.body));
    expect(parsed.valid).toBe(false);
    expect(parsed.diagnostics[0].code).toBe('review.no-session');
    expect(parsed.diagnostics[0].message).toContain('没有进行中的审阅');
    expect(calls).toHaveLength(0);
  });

  it('refuses an oversized body, unknown paths and wrong methods without forwarding', async () => {
    const { fetchLike, calls } = fakeUpstream();
    const big = await proxyReview('POST', { kind: 'submit', path: 'feedback' }, UP, null, fetchLike);
    expect(big.status).toBe(400);
    expect(JSON.parse(String(big.body)).diagnostics[0].code).toBe('input.invalid-value');
    expect((await proxyReview('GET', { kind: 'unknown' }, UP, null, fetchLike)).status).toBe(404);
    expect((await proxyReview('POST', { kind: 'data' }, UP, null, fetchLike)).status).toBe(405);
    expect((await proxyReview('GET', { kind: 'submit', path: 'confirm' }, UP, null, fetchLike)).status).toBe(405);
    expect(calls).toHaveLength(0);
  });

  it('reports an unreachable upstream readably', async () => {
    const failing: ProxyFetch = async () => {
      throw new Error('connect ECONNREFUSED');
    };
    const page = await proxyReview('GET', { kind: 'data' }, UP, null, failing);
    expect(page.status).toBe(502);
    expect(JSON.parse(String(page.body)).diagnostics[0].code).toBe('review.upstream-unavailable');
    expect(String(page.body)).toContain('阅读页服务无法访问');
    expect(String(page.body)).not.toContain('ECONNREFUSED');
    expect(String(page.body)).not.toContain(UP.token);
    const submit = await proxyReview('POST', { kind: 'submit', path: 'confirm' }, UP, Buffer.from('{}'), failing);
    expect(JSON.parse(String(submit.body)).diagnostics[0].code).toBe('review.upstream-unavailable');
  });
});

describe('review proxy body reading', () => {
  it('reads up to the limit and refuses anything larger', async () => {
    expect((await readBody(chunks('{"a":', '1}')))?.toString('utf8')).toBe('{"a":1}');
    expect(await readBody(chunks('x'.repeat(REVIEW_BODY_LIMIT), 'y'))).toBeNull();
    expect((await readBody(chunks('x'.repeat(REVIEW_BODY_LIMIT))))?.length).toBe(REVIEW_BODY_LIMIT);
  });
});
