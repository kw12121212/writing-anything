/**
 * Review routes: the session status/start/end routes and the reading-view
 * proxy prefix route. Every handler runs the Request Origin Guard first and
 * a refused request neither starts a session nor reaches the upstream.
 */
import { guardRequest, type GuardOptions } from './routeguard';
import { proxyReview, readBody, REVIEW_PREFIX, reviewTargetOf, type BodySource, type ProxyFetch } from './reviewproxy';
import { validReviewUnit, type ReviewSessions } from './reviewsession';

export const REVIEW_SESSION_PATH = '/dsh-writing-viewer/review-session';
export const REVIEW_SESSION_END_PATH = '/dsh-writing-viewer/review-session/end';

/** Resolve the project directory a session starts in: the panel's explicit
 * root when given (validated like the snapshot's `root`), else the default
 * location. Failure is readable text and spawns nothing. */
export type ReviewRootResolver = (root: string | null) => Promise<{ ok: true; root: string } | { ok: false; error: string }>;

export interface ReviewRouteDeps {
  sessions: ReviewSessions;
  resolveRoot: ReviewRootResolver;
  fetchLike: ProxyFetch;
  guardOptions: GuardOptions;
}

type ReviewRequest = BodySource & { method?: string; url?: string; headers?: Record<string, unknown> };

interface ReviewResponse {
  writeHead(status: number, headers?: Record<string, string>): { end(body?: string | Uint8Array): void };
}

function json(res: ReviewResponse, status: number, body: unknown): void {
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' }).end(JSON.stringify(body));
}

function pathnameOf(url: string | undefined): string {
  try {
    return new URL(url ?? '/', 'http://x').pathname;
  } catch {
    return '/';
  }
}

/** Parse the start request body: `{ unit, root? }` as one JSON object. */
export function parseStartBody(raw: Buffer | null): { ok: true; unit: string; root: string | null } | { ok: false; error: string } {
  if (raw === null) return { ok: false, error: '请求体超过 64 KiB' };
  let body: unknown;
  try {
    body = JSON.parse(raw.toString('utf8'));
  } catch {
    return { ok: false, error: '请求体必须是一个 JSON 对象' };
  }
  if (body === null || typeof body !== 'object' || Array.isArray(body)) return { ok: false, error: '请求体必须是一个 JSON 对象' };
  const fields = body as { unit?: unknown; root?: unknown };
  const unit = validReviewUnit(fields.unit);
  if (unit === null) return { ok: false, error: 'unit 必须是单元引用' };
  if (fields.root !== undefined && fields.root !== null && typeof fields.root !== 'string') return { ok: false, error: 'root 必须是字符串' };
  return { ok: true, unit, root: typeof fields.root === 'string' ? fields.root : null };
}

/** Register the session routes and the proxy prefix route. */
export function registerReviewRoutes(server: { register(route: unknown): unknown }, deps: ReviewRouteDeps): void {
  const guarded = (handler: (req: ReviewRequest, res: ReviewResponse) => Promise<void>) =>
    async (req: ReviewRequest, res: ReviewResponse): Promise<void> => {
      const verdict = guardRequest(req.headers ?? {}, deps.guardOptions);
      if (!verdict.ok) {
        json(res, 403, { ok: false, error: verdict.reason });
        return;
      }
      try {
        await handler(req, res);
      } catch (error) {
        // An aborted upload or an unexpected failure still answers readable
        // JSON instead of surfacing to the host as an unhandled route error.
        try {
          json(res, 500, { ok: false, error: `审阅路由处理失败：${String((error as Error)?.message ?? error)}` });
        } catch {
          // The response was already started; nothing more can be written.
        }
      }
    };

  server.register({
    name: 'dsh-writing-viewer-review-session',
    kind: 'exact',
    path: REVIEW_SESSION_PATH,
    handler: guarded(async (req, res) => {
      const method = req.method ?? 'GET';
      if (method === 'GET') {
        json(res, 200, { ok: true, session: deps.sessions.status() });
        return;
      }
      if (method !== 'POST') {
        res.writeHead(405, { allow: 'GET, POST' }).end();
        return;
      }
      const parsed = parseStartBody(await readBody(req));
      if (!parsed.ok) {
        json(res, 400, { ok: false, error: parsed.error });
        return;
      }
      const root = await deps.resolveRoot(parsed.root);
      if (!root.ok) {
        json(res, 400, { ok: false, error: root.error });
        return;
      }
      const started = await deps.sessions.start({ unit: parsed.unit, root: root.root });
      // Same convention as the snapshot route: the panel always parses the
      // body, and `ok: false` plus readable text is the failure shape.
      json(res, 200, started.ok ? { ok: true, session: started.session } : { ok: false, error: started.error, diagnostics: started.diagnostics });
    }),
  });

  server.register({
    name: 'dsh-writing-viewer-review-session-end',
    kind: 'exact',
    path: REVIEW_SESSION_END_PATH,
    handler: guarded(async (req, res) => {
      if ((req.method ?? 'GET') !== 'POST') {
        res.writeHead(405, { allow: 'POST' }).end();
        return;
      }
      deps.sessions.end();
      json(res, 200, { ok: true, session: null });
    }),
  });

  server.register({
    name: 'dsh-writing-viewer-review',
    kind: 'prefix',
    path: REVIEW_PREFIX,
    handler: guarded(async (req, res) => {
      const method = req.method ?? 'GET';
      const target = reviewTargetOf(pathnameOf(req.url));
      // Only page and submit targets can ever reach the upstream, so only
      // they refresh the idle deadline (upstream() re-arms it). A 405 method
      // mismatch on one of those targets still counts as session activity;
      // 404 and 308 answers never do. That asymmetry is deliberate.
      const body = target.kind === 'submit' && method === 'POST' ? await readBody(req) : null;
      const upstream = target.kind === 'page' || target.kind === 'submit' ? deps.sessions.upstream() : null;
      const answer = await proxyReview(method, target, upstream, body, deps.fetchLike);
      res.writeHead(answer.status, answer.headers).end(answer.body);
    }),
  });
}
