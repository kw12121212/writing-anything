/**
 * The read-only copy route behind the panel's「复制图文」button. One GET runs
 * the Request Origin Guard, validates the work reference with the same pure
 * argument guard as the review session, resolves the project directory the
 * panel shows, and calls wa-cli exactly once:
 *
 *   wa-cli export <work> --format paste-html --print --json --dir <root>
 *
 * `--print` is wa-cli's read-only mode: no export file, asset directory or
 * image list is written, so the plugin still writes nothing to the project.
 * Only the fragment (`export.html`), its segments (`export.segments`, at most
 * 7 embedded images each), the image count and the diagnostics
 * (code, severity, message, next) go back to the panel.
 */
import { guardRequest, type GuardOptions } from './routeguard';
import { validReviewUnit } from './reviewsession';
import type { ReviewRootResolver } from './reviewroutes';
import type { CliRunner } from './wacli';
import { COPY_ROUTE, primaryDiagnostic, type CopyDiagnostic } from './copyview';

export { COPY_ROUTE };

export interface CopyRouteDeps {
  runner: CliRunner;
  resolveRoot: ReviewRootResolver;
  guardOptions: GuardOptions;
}

interface CopyRequest {
  method?: string;
  url?: string;
  headers?: Record<string, unknown>;
}

interface CopyResponse {
  writeHead(status: number, headers?: Record<string, string>): { end(body?: string): void };
}

function json(res: CopyResponse, status: number, body: unknown): void {
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' }).end(JSON.stringify(body));
}

/** A work reference fit for a wa-cli positional argument: the review guard plus no `/` (a work, not a sub-unit). */
export function validCopyWork(value: unknown): string | null {
  const work = validReviewUnit(value);
  return work === null || work.includes('/') ? null : work;
}

/** The wa-cli argument list for one copy (never through a shell). */
export function copyArgs(work: string, root: string): string[] {
  return ['export', work, '--format', 'paste-html', '--print', '--json', '--dir', root];
}

function diagnosticsOf(value: unknown): CopyDiagnostic[] {
  if (!Array.isArray(value)) return [];
  return value
    .filter((d): d is Record<string, unknown> => d !== null && typeof d === 'object')
    .map((d) => ({
      code: typeof d['code'] === 'string' ? d['code'] : '',
      severity: typeof d['severity'] === 'string' ? d['severity'] : '',
      message: typeof d['message'] === 'string' ? d['message'] : '',
      next: Array.isArray(d['next']) ? (d['next'] as unknown[]).filter((n): n is string => typeof n === 'string') : [],
    }));
}

/** Map wa-cli's stdout to the route body; only the fields the panel needs. */
export function copyBodyOf(stdout: string | null): Record<string, unknown> {
  if (stdout === null) return { ok: false, error: 'wa-cli 不可用、超时或输出超过插件的体积上限', diagnostics: [] };
  let parsed: unknown;
  try {
    parsed = JSON.parse(stdout);
  } catch {
    return { ok: false, error: 'wa-cli 的输出无法解析（可能超过插件的体积上限）', diagnostics: [] };
  }
  const envelope = (parsed ?? {}) as { export?: { html?: unknown; images?: unknown } | null; diagnostics?: unknown };
  const diagnostics = diagnosticsOf(envelope.diagnostics);
  const html = envelope.export?.html;
  if (typeof html !== 'string') {
    return { ok: false, error: primaryDiagnostic(diagnostics)?.message ?? 'wa-cli 没有给出可粘贴正文', diagnostics };
  }
  const images = Array.isArray(envelope.export?.images) ? envelope.export.images.length : 0;
  const raw = (envelope.export as { segments?: unknown } | null | undefined)?.segments;
  const segments = Array.isArray(raw) ? raw.filter((p): p is string => typeof p === 'string') : [];
  return { ok: true, html, images, segments: segments.length > 0 ? segments : [html], diagnostics };
}

/** Register the copy route on an acquired webServer scope. */
export function registerCopyRoute(server: { register(route: unknown): unknown }, deps: CopyRouteDeps): void {
  server.register({
    name: 'dsh-writing-viewer-copy',
    kind: 'exact',
    path: COPY_ROUTE,
    handler: async (req: CopyRequest, res: CopyResponse): Promise<void> => {
      const verdict = guardRequest(req.headers ?? {}, deps.guardOptions);
      if (!verdict.ok) {
        json(res, 403, { ok: false, error: verdict.reason });
        return;
      }
      if ((req.method ?? 'GET') !== 'GET') {
        res.writeHead(405, { allow: 'GET' }).end();
        return;
      }
      let query: URLSearchParams;
      try {
        query = new URL(req.url ?? '/', 'http://x').searchParams;
      } catch {
        query = new URLSearchParams();
      }
      const work = validCopyWork(query.get('work'));
      if (work === null) {
        json(res, 400, { ok: false, error: 'work 必须是作品 id', diagnostics: [] });
        return;
      }
      const root = await deps.resolveRoot(query.get('root'));
      if (!root.ok) {
        json(res, 400, { ok: false, error: root.error, diagnostics: [] });
        return;
      }
      const outcome = await deps.runner.run(copyArgs(work, root.root));
      // Same convention as the snapshot and review routes: the panel always
      // parses the body, and `ok: false` plus readable text is the failure shape.
      json(res, 200, copyBodyOf(outcome?.stdout ?? null));
    },
  });
}
