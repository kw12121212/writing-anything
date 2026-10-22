/**
 * Same-origin proxy for the wa-cli reading view (阅读页, Reading View).
 *
 * The reading view listens on 127.0.0.1 only, so an author reaching DSH over
 * the network (for example a Tailscale MagicDNS name over plain http) cannot
 * open it. This module forwards a fixed set of paths under one prefix route
 * to the current review session's loopback server and does nothing else
 * (dsh-plugin-structure/review-proxy-thin):
 *
 * - only the page (GET /) and the three submit endpoints are forwarded;
 * - the session token is injected here and whatever token the browser sent
 *   is dropped, so the token never reaches the browser and a forged one is
 *   never forwarded;
 * - the upstream body is passed through byte for byte (rewriting the inline
 *   script would break the CSP hash wa-cli computes for it);
 * - response headers pass through except X-Frame-Options, which is dropped,
 *   and the CSP `frame-ancestors 'none'`, which becomes `'self'` so the panel
 *   can embed the page from the same origin.
 *
 * Workspace writes happen only inside the wa-cli child; nothing here touches
 * the filesystem.
 */
import type { ReviewUpstream } from './reviewsession';

/** The prefix route the host registers (exact paths may not end in '/'). */
export const REVIEW_PREFIX = '/dsh-writing-viewer/review';

/** Request bodies above this are refused before forwarding (wa-cli's own limit). */
export const REVIEW_BODY_LIMIT = 64 * 1024;

/** The submit endpoints the reading view posts to, by relative path. */
const SUBMIT_PATHS = new Set(['feedback', 'confirm', 'author-review']);

/** Where one request under the prefix goes. */
export type ReviewTarget =
  | { kind: 'redirect' }
  | { kind: 'page' }
  | { kind: 'submit'; path: string }
  | { kind: 'unknown' };

/** Map a pathname to its target; anything outside the fixed set is unknown. */
export function reviewTargetOf(pathname: string): ReviewTarget {
  if (pathname === REVIEW_PREFIX) return { kind: 'redirect' };
  if (pathname === REVIEW_PREFIX + '/') return { kind: 'page' };
  if (!pathname.startsWith(REVIEW_PREFIX + '/')) return { kind: 'unknown' };
  const rest = pathname.slice(REVIEW_PREFIX.length + 1);
  return SUBMIT_PATHS.has(rest) ? { kind: 'submit', path: rest } : { kind: 'unknown' };
}

/** Upstream response headers that describe the hop rather than the payload,
 * or that the host's own stack (gzip, chunking) re-derives. */
const HOP_HEADERS = new Set(['content-length', 'content-encoding', 'transfer-encoding', 'connection', 'keep-alive']);

/**
 * The response headers the browser receives: the upstream set minus hop
 * headers and X-Frame-Options, with the CSP's frame-ancestors relaxed from
 * 'none' to 'self'. Every other directive (script hash, connect-src 'self')
 * is left exactly as wa-cli sent it.
 */
export function proxiedHeaders(upstream: Iterable<[string, string]>): Record<string, string> {
  // Only the headers named below are touched. Anything else upstream sends
  // passes through verbatim — including a hypothetical 3xx `location`, which
  // could one day carry a tokenized loopback URL. wa-cli's reading view never
  // redirects (every endpoint answers the page, 200 JSON, or a 4xx envelope),
  // so this is unreachable today; if wa-cli ever grows redirects, `location`
  // must join the drop list before this proxy forwards one.
  const out: Record<string, string> = {};
  for (const [rawName, value] of upstream) {
    const name = rawName.toLowerCase();
    if (HOP_HEADERS.has(name) || name === 'x-frame-options') continue;
    out[name] = name === 'content-security-policy' ? value.replace(/frame-ancestors\s+'none'/i, "frame-ancestors 'self'") : value;
  }
  return out;
}

/** Minimal readable request stream: async iteration over body chunks. */
export interface BodySource {
  [Symbol.asyncIterator](): AsyncIterator<Buffer | string>;
}

/** Read a request body up to `limit` bytes; null when it is larger. */
export async function readBody(source: BodySource, limit = REVIEW_BODY_LIMIT): Promise<Buffer | null> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of source) {
    const buffer = typeof chunk === 'string' ? Buffer.from(chunk, 'utf8') : chunk;
    size += buffer.length;
    if (size > limit) return null;
    chunks.push(buffer);
  }
  return Buffer.concat(chunks);
}

/** The fetch slice the proxy uses (Node's global fetch in production). */
export type ProxyFetch = (
  url: string,
  init: { method: string; headers: Record<string, string>; body?: Uint8Array; redirect: 'manual' },
) => Promise<{ status: number; headers: Iterable<[string, string]>; arrayBuffer(): Promise<ArrayBuffer> }>;

/** One proxied answer, ready for the route handler to write. */
export interface ProxyAnswer {
  status: number;
  headers: Record<string, string>;
  body: Uint8Array | string;
}

/** Readable answer for a submit the reading view's script can display: it
 * shows `diagnostics[].message` when `valid` is false. */
function submitRefusal(status: number, code: string, message: string): ProxyAnswer {
  return {
    status,
    headers: { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' },
    body: JSON.stringify({ valid: false, diagnostics: [{ severity: 'error', code, message }], feedback: null }),
  };
}

/** Readable page when there is nothing to show inside the iframe. */
function pageNotice(status: number, title: string, hint: string): ProxyAnswer {
  const escape = (text: string): string => text.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c] as string);
  return {
    status,
    headers: {
      'content-type': 'text/html; charset=utf-8',
      'cache-control': 'no-store',
      'content-security-policy': "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'self'",
    },
    body: `<!doctype html><html lang="zh"><meta charset="utf-8"><title>${escape(title)}</title>` +
      `<body style="font:14px/1.6 system-ui,sans-serif;padding:16px"><p><strong>${escape(title)}</strong></p><p>${escape(hint)}</p></body></html>`,
  };
}

export const NO_SESSION_TITLE = '没有进行中的审阅';
const NO_SESSION_HINT = '在插件面板的单元详情中点「审阅」重新开始。';

/**
 * Answer one request under the prefix. `upstream` is the running session's
 * loopback endpoint (null when none); `body` is the already-read request
 * body for submits. The caller has already passed the origin guard.
 */
export async function proxyReview(
  method: string,
  target: ReviewTarget,
  upstream: ReviewUpstream | null,
  body: Buffer | null,
  fetchLike: ProxyFetch,
): Promise<ProxyAnswer> {
  if (target.kind === 'unknown') return { status: 404, headers: { 'cache-control': 'no-store' }, body: '' };
  if (target.kind === 'redirect') {
    return { status: 308, headers: { location: REVIEW_PREFIX + '/', 'cache-control': 'no-store' }, body: '' };
  }
  const wanted = target.kind === 'page' ? 'GET' : 'POST';
  if (method !== wanted) return { status: 405, headers: { allow: wanted, 'cache-control': 'no-store' }, body: '' };
  if (upstream === null) {
    return target.kind === 'page'
      ? pageNotice(409, NO_SESSION_TITLE, NO_SESSION_HINT)
      : submitRefusal(409, 'review.no-session', NO_SESSION_TITLE + '：' + NO_SESSION_HINT);
  }
  if (target.kind === 'submit' && body === null) {
    return submitRefusal(400, 'input.invalid-value', `请求体超过 ${REVIEW_BODY_LIMIT / 1024} KiB`);
  }
  const base = `http://127.0.0.1:${upstream.port}`;
  const url = target.kind === 'page' ? `${base}/?token=${encodeURIComponent(upstream.token)}` : `${base}/${target.path}`;
  const headers: Record<string, string> = target.kind === 'page'
    ? {}
    : { 'content-type': 'application/json', 'x-wa-token': upstream.token };
  try {
    const response = await fetchLike(url, {
      method: wanted,
      headers,
      ...(target.kind === 'submit' && body !== null ? { body: new Uint8Array(body) } : {}),
      redirect: 'manual',
    });
    return { status: response.status, headers: proxiedHeaders(response.headers), body: new Uint8Array(await response.arrayBuffer()) };
  } catch {
    // No raw error text: a fetch failure message could one day carry the
    // upstream URL, and the page URL holds the session token.
    const reason = '阅读页服务无法访问（可能已退出或超时），请结束审阅后重新审阅';
    return target.kind === 'page'
      ? pageNotice(502, NO_SESSION_TITLE, reason)
      : submitRefusal(502, 'review.upstream-unavailable', reason);
  }
}
