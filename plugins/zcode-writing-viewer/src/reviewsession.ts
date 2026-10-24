/**
 * Review session (审阅会话) management for the host half.
 *
 * One session per host: the author's「审阅」click starts
 * `wa-cli preview <unit> --serve --json --dir <root>`, the first stdout line
 * (the output envelope) names the loopback URL with port and token, and the
 * proxy then forwards the reading view through same-origin plugin routes.
 * The token never leaves this module except through `upstream()`, which only
 * the proxy calls; no route body, log line, or client state carries it.
 *
 * Bounded child lifecycle (dsh-plugin-structure/review-child-lifecycle):
 * one spawn per start, a fixed first-line deadline, stdout drained for the
 * child's whole life, and a kill on every way a session ends — explicit end,
 * idle expiry, replacement by another start, child exit, plugin dispose.
 * The module performs no filesystem access at all; every workspace write
 * comes from the wa-cli child when the author submits in the reading view.
 */

/** Milliseconds the child has to print its first envelope line. */
export const REVIEW_START_TIMEOUT_MS = 10_000;

/** Milliseconds without any proxied request before a session ends. */
export const REVIEW_IDLE_MS = 30 * 60 * 1000;

/** Upper bound on buffered stdout while waiting for the first line. */
const FIRST_LINE_CAP = 1024 * 1024;

/** The slice of a spawned child the session needs. */
export interface ReviewChild {
  stdout: { on(event: 'data', listener: (chunk: Buffer | string) => void): unknown } | null;
  /** 'close' (not 'exit'): fires once stdout is fully read, so a failure
   * envelope printed just before exit is never lost. */
  on(event: 'close', listener: (code: number | null) => void): unknown;
  on(event: 'error', listener: (error: unknown) => void): unknown;
  kill(): unknown;
}

/** Spawn seam: argument list only, never a shell. */
export type ReviewSpawn = (binary: string, args: readonly string[]) => ReviewChild;

/** Timer seam so tests can drive deadlines without waiting. */
export interface ReviewTimers {
  set(fn: () => void, ms: number): unknown;
  clear(handle: unknown): void;
}

/** What the routes may show about a session: never the token or the port. */
export interface ReviewSessionSummary {
  unit: string;
  root: string;
  startedAt: string;
}

/** Outcome of one start. Failures carry readable text and wa-cli diagnostics. */
export type ReviewStartResult =
  | { ok: true; session: ReviewSessionSummary }
  | { ok: false; error: string; diagnostics: { code: string; message: string }[] };

/** Loopback upstream of the current session, for the proxy only. */
export interface ReviewUpstream {
  port: number;
  token: string;
}

export interface ReviewSessions {
  start(request: { unit: string; root: string }): Promise<ReviewStartResult>;
  /** Summary of the running session, or null. */
  status(): ReviewSessionSummary | null;
  /** Upstream of the running session, refreshing its idle deadline; null when none. */
  upstream(): ReviewUpstream | null;
  /** End the running (or starting) session, killing its child. */
  end(): void;
  /** End everything; the plugin is going away. */
  dispose(): void;
}

export interface ReviewSessionOptions {
  binary: string;
  spawn: ReviewSpawn;
  now: () => string;
  timers?: ReviewTimers;
  startTimeoutMs?: number;
  idleMs?: number;
}

const realTimers: ReviewTimers = {
  set: (fn, ms) => {
    const handle = setTimeout(fn, ms);
    (handle as { unref?: () => void }).unref?.();
    return handle;
  },
  clear: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

/** Read port and token off the first envelope's `preview.url`, accepting only
 * a loopback http URL that carries a token. */
export function upstreamOfEnvelope(envelope: unknown): ReviewUpstream | null {
  const url = (envelope as { preview?: { url?: unknown } } | null)?.preview?.url;
  if (typeof url !== 'string') return null;
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return null;
  }
  if (parsed.protocol !== 'http:' || parsed.hostname !== '127.0.0.1') return null;
  const port = Number(parsed.port);
  const token = parsed.searchParams.get('token');
  if (!Number.isInteger(port) || port < 1 || port > 65535 || token === null || token === '') return null;
  return { port, token };
}

/** Readable diagnostics of a failed envelope (wa-cli prints one even on exit 1). */
export function diagnosticsOfEnvelope(envelope: unknown): { code: string; message: string }[] {
  const list = (envelope as { diagnostics?: unknown } | null)?.diagnostics;
  if (!Array.isArray(list)) return [];
  const out: { code: string; message: string }[] = [];
  for (const entry of list) {
    const code = (entry as { code?: unknown })?.code;
    const message = (entry as { message?: unknown })?.message;
    if (typeof code === 'string' && typeof message === 'string') out.push({ code, message });
  }
  return out;
}

/** Unit refs are schema-constrained ids joined by '/'; anything shaped like a
 * flag or carrying control characters never reaches the wa-cli argument list. */
export function validReviewUnit(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  const unit = value.trim();
  if (unit === '' || unit.length > 512 || unit.startsWith('-')) return null;
  for (let i = 0; i < unit.length; i += 1) {
    if (unit.charCodeAt(i) < 0x20 || unit.charCodeAt(i) === 0x7f) return null;
  }
  return unit;
}

interface Running {
  child: ReviewChild;
  summary: ReviewSessionSummary;
  upstream: ReviewUpstream | null;
  idle: unknown;
  ended: boolean;
}

export function createReviewSessions(options: ReviewSessionOptions): ReviewSessions {
  const timers = options.timers ?? realTimers;
  const startTimeout = options.startTimeoutMs ?? REVIEW_START_TIMEOUT_MS;
  const idleMs = options.idleMs ?? REVIEW_IDLE_MS;
  let current: Running | null = null;

  const stop = (run: Running): void => {
    if (run.ended) return;
    run.ended = true;
    if (run.idle !== null) timers.clear(run.idle);
    run.idle = null;
    try {
      run.child.kill();
    } catch {
      // Already gone: nothing left to stop.
    }
    if (current === run) current = null;
  };

  const armIdle = (run: Running): void => {
    if (run.idle !== null) timers.clear(run.idle);
    run.idle = timers.set(() => stop(run), idleMs);
  };

  return {
    start(request) {
      if (current !== null) stop(current);
      let child: ReviewChild;
      try {
        child = options.spawn(options.binary, ['preview', request.unit, '--serve', '--json', '--dir', request.root]);
      } catch (error) {
        return Promise.resolve({ ok: false, error: `wa-cli 无法启动：${String(error)}`, diagnostics: [] });
      }
      const run: Running = {
        child,
        summary: { unit: request.unit, root: request.root, startedAt: options.now() },
        upstream: null,
        idle: null,
        ended: false,
      };
      current = run;
      return new Promise<ReviewStartResult>((resolve) => {
        let buffered = '';
        let settled = false;
        const settle = (result: ReviewStartResult): void => {
          if (settled) return;
          settled = true;
          timers.clear(deadline);
          if (!result.ok) stop(run);
          resolve(result);
        };
        const deadline = timers.set(
          () => settle({ ok: false, error: `阅读页服务没有在 ${Math.round(startTimeout / 1000)} 秒内就绪`, diagnostics: [] }),
          startTimeout,
        );
        const firstLine = (line: string): void => {
          let envelope: unknown = null;
          try {
            envelope = JSON.parse(line);
          } catch {
            settle({ ok: false, error: '阅读页服务的首行输出不是 JSON 输出信封', diagnostics: [] });
            return;
          }
          const upstream = upstreamOfEnvelope(envelope);
          if (upstream === null) {
            const diagnostics = diagnosticsOfEnvelope(envelope);
            settle({
              ok: false,
              error: diagnostics.length > 0 ? diagnostics.map((d) => d.message).join('；') : '阅读页服务没有给出本地地址',
              diagnostics,
            });
            return;
          }
          if (run.ended) {
            settle({ ok: false, error: '审阅已被另一次启动替换', diagnostics: [] });
            return;
          }
          run.upstream = upstream;
          armIdle(run);
          settle({ ok: true, session: { ...run.summary } });
        };
        child.stdout?.on('data', (chunk) => {
          // After the first line the stream is only drained, so the child
          // never blocks on a full pipe while it keeps serving.
          if (settled) return;
          buffered += typeof chunk === 'string' ? chunk : chunk.toString('utf8');
          const newline = buffered.indexOf('\n');
          if (newline >= 0) firstLine(buffered.slice(0, newline));
          else if (buffered.length > FIRST_LINE_CAP) settle({ ok: false, error: '阅读页服务的首行输出过长', diagnostics: [] });
        });
        child.on('error', (error) => {
          settle({ ok: false, error: `wa-cli 无法启动：${String((error as Error)?.message ?? error)}`, diagnostics: [] });
          stop(run);
        });
        child.on('close', (code) => {
          // An unterminated last line is still the envelope wa-cli printed.
          if (!settled && buffered.trim() !== '') firstLine(buffered.trim());
          settle({ ok: false, error: `阅读页服务提前退出（退出码 ${code === null ? '—' : String(code)}）`, diagnostics: [] });
          // A child that exits by itself ends its session too.
          run.ended = true;
          if (run.idle !== null) timers.clear(run.idle);
          run.idle = null;
          if (current === run) current = null;
        });
      });
    },
    status() {
      return current !== null && current.upstream !== null ? { ...current.summary } : null;
    },
    upstream() {
      if (current === null || current.upstream === null) return null;
      armIdle(current);
      return { ...current.upstream };
    },
    end() {
      if (current !== null) stop(current);
    },
    dispose() {
      if (current !== null) stop(current);
    },
  };
}
