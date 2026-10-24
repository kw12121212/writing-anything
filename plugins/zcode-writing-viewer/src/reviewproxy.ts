/**
 * Thin forwarder between the review tools and the wa-cli reading view
 * (阅读页, Reading View) service.
 *
 * The reading view listens on 127.0.0.1 only; the panel never reaches it
 * directly. It renders the review view (审阅视图, Review View) natively from
 * the service's data, so this module forwards only the data endpoint and the
 * three submit endpoints to the current review session's loopback server and
 * does nothing else (zcode-plugin-structure/review-proxy-thin):
 *
 * - only the page.json data read, the illustration read (配图
 *   （Illustration） images, with just the unit and id values) and the
 *   feedback / confirm / author-review submits are forwarded; anything else
 *   is refused without touching the network;
 * - the session token is injected here and whatever token the caller sent is
 *   dropped, so the token never leaves the server process and a forged one
 *   is never forwarded;
 * - the upstream body is passed through byte for byte (image bytes too);
 * - response headers pass through except hop headers.
 *
 * Workspace writes happen only inside the wa-cli child; nothing here touches
 * the filesystem.
 */
import type { ReviewUpstream } from './reviewsession';

/** Submit bodies above this are refused before forwarding (wa-cli's own limit). */
export const REVIEW_BODY_LIMIT = 64 * 1024;

/** The submit endpoints the review view posts to, by action name. */
export const SUBMIT_ACTIONS = ['feedback', 'confirm', 'author-review'] as const;

/** Where one review request goes. */
export type ReviewTarget =
  | { kind: 'data' }
  | { kind: 'image'; unit: string; id: string }
  | { kind: 'submit'; action: (typeof SUBMIT_ACTIONS)[number] }
  | { kind: 'unknown' };

/**
 * Map caller-supplied values to a target. The image target keeps only the
 * unit and id values — every other value, including a caller-sent token, is
 * dropped here. Anything outside the fixed set is unknown and never reaches
 * the network.
 */
export function reviewTargetOf(
  kind: unknown,
  fields: { unit?: unknown; id?: unknown; action?: unknown } = {},
): ReviewTarget {
  if (kind === 'data') return { kind: 'data' };
  if (kind === 'image') {
    const unit = typeof fields.unit === 'string' ? fields.unit : '';
    const id = typeof fields.id === 'string' ? fields.id : '';
    return { kind: 'image', unit, id };
  }
  if (kind === 'submit') {
    const action = fields.action;
    return SUBMIT_ACTIONS.includes(action as (typeof SUBMIT_ACTIONS)[number])
      ? { kind: 'submit', action: action as (typeof SUBMIT_ACTIONS)[number] }
      : { kind: 'unknown' };
  }
  return { kind: 'unknown' };
}

/** Upstream response headers that describe the hop rather than the payload. */
const HOP_HEADERS = new Set(['content-length', 'content-encoding', 'transfer-encoding', 'connection', 'keep-alive']);

/**
 * The response headers the caller receives: the upstream set minus hop
 * headers. Security headers pass through unchanged.
 */
export function proxiedHeaders(upstream: Iterable<[string, string]>): Record<string, string> {
  // Only hop headers are dropped. Anything else upstream sends passes through
  // verbatim — including a hypothetical 3xx `location`, which could one day
  // carry a tokenized loopback URL. wa-cli's reading view service never
  // redirects (every endpoint answers 200 data or JSON, or a 4xx envelope),
  // so this is unreachable today; if wa-cli ever grows redirects, `location`
  // must join the drop list before this forwarder relays one.
  const out: Record<string, string> = {};
  for (const [rawName, value] of upstream) {
    const name = rawName.toLowerCase();
    if (!HOP_HEADERS.has(name)) out[name] = value;
  }
  return out;
}

/** The fetch slice the forwarder uses (Node's global fetch in production). */
export type ProxyFetch = (
  url: string,
  init: { method: string; headers: Record<string, string>; body?: Uint8Array; redirect: 'manual' },
) => Promise<{ status: number; headers: Iterable<[string, string]>; arrayBuffer(): Promise<ArrayBuffer> }>;

/** One forwarded answer, ready for the tool handler to return. */
export interface ProxyAnswer {
  status: number;
  headers: Record<string, string>;
  body: Uint8Array | string;
}

/** Readable JSON answer when the request cannot reach wa-cli. It mirrors
 * wa-cli's envelope (`valid` false with `diagnostics[].message`) so the
 * panel shows the message the same way it shows wa-cli's own refusals. */
function refusal(status: number, code: string, message: string): ProxyAnswer {
  return {
    status,
    headers: { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' },
    body: JSON.stringify({ valid: false, diagnostics: [{ severity: 'error', code, message }] }),
  };
}

export const NO_SESSION_TITLE = '没有进行中的审阅';
const NO_SESSION_HINT = '在插件面板的单元详情中点「审阅」重新开始。';

/**
 * Answer one review request. `upstream` is the running session's loopback
 * endpoint (null when none); `body` is the already-read request body for
 * submits. The method is fixed per target kind — callers cannot choose it.
 */
export async function proxyReview(
  target: ReviewTarget,
  upstream: ReviewUpstream | null,
  body: Uint8Array | null,
  fetchLike: ProxyFetch,
): Promise<ProxyAnswer> {
  if (target.kind === 'unknown') return refusal(400, 'review.unknown-target', '未知的审阅请求目标');
  if (upstream === null) return refusal(409, 'review.no-session', NO_SESSION_TITLE + '：' + NO_SESSION_HINT);
  if (target.kind === 'submit') {
    if (body === null) return refusal(400, 'input.invalid-value', `请求体超过 ${REVIEW_BODY_LIMIT / 1024} KiB`);
    if (body.byteLength > REVIEW_BODY_LIMIT) {
      return refusal(400, 'input.invalid-value', `请求体超过 ${REVIEW_BODY_LIMIT / 1024} KiB`);
    }
  }
  const method = target.kind === 'submit' ? 'POST' : 'GET';
  const base = `http://127.0.0.1:${upstream.port}`;
  const token = `token=${encodeURIComponent(upstream.token)}`;
  const url = target.kind === 'data'
    ? `${base}/page.json?${token}`
    : target.kind === 'image'
      ? `${base}/illustration?unit=${encodeURIComponent(target.unit)}&id=${encodeURIComponent(target.id)}&${token}`
      : `${base}/${target.action}`;
  const headers: Record<string, string> = target.kind === 'submit'
    ? { 'content-type': 'application/json', 'x-wa-token': upstream.token }
    : {};
  try {
    const response = await fetchLike(url, {
      method,
      headers,
      ...(target.kind === 'submit' && body !== null ? { body } : {}),
      redirect: 'manual',
    });
    return { status: response.status, headers: proxiedHeaders(response.headers), body: new Uint8Array(await response.arrayBuffer()) };
  } catch {
    // No raw error text: a fetch failure message could one day carry the
    // upstream URL, and the data URL holds the session token.
    return refusal(502, 'review.upstream-unavailable', '阅读页服务无法访问（可能已退出或超时），请结束审阅后重新审阅');
  }
}
