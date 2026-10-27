import { describe, expect, it } from 'vitest';
import {
  createReviewSessions,
  diagnosticsOfEnvelope,
  upstreamOfEnvelope,
  validReviewUnit,
  type ReviewChild,
  type ReviewTimers,
} from './reviewsession';

/** A controllable child: tests push stdout chunks and close it by hand. */
class FakeChild implements ReviewChild {
  killed = 0;
  private dataListeners: ((chunk: Buffer | string) => void)[] = [];
  private closeListeners: ((code: number | null) => void)[] = [];
  private errorListeners: ((error: unknown) => void)[] = [];
  stdout = { on: (_event: 'data', listener: (chunk: Buffer | string) => void) => this.dataListeners.push(listener) };
  on(event: 'close' | 'error', listener: (arg: never) => void): unknown {
    if (event === 'close') this.closeListeners.push(listener as (code: number | null) => void);
    else this.errorListeners.push(listener as (error: unknown) => void);
    return this;
  }
  kill(): unknown {
    this.killed += 1;
    return true;
  }
  emit(chunk: string): void {
    for (const l of this.dataListeners) l(Buffer.from(chunk, 'utf8'));
  }
  close(code: number | null): void {
    for (const l of this.closeListeners) l(code);
  }
  fail(error: unknown): void {
    for (const l of this.errorListeners) l(error);
  }
}

/** Manual timers: `fire(ms)` runs every pending timer armed for that delay. */
function manualTimers(): ReviewTimers & { fire(ms: number): void; pending(): number } {
  let next = 0;
  const armed = new Map<number, { fn: () => void; ms: number }>();
  return {
    set(fn, ms) {
      next += 1;
      armed.set(next, { fn, ms });
      return next;
    },
    clear(handle) {
      armed.delete(handle as number);
    },
    fire(ms) {
      for (const [id, entry] of [...armed]) {
        if (entry.ms !== ms) continue;
        armed.delete(id);
        entry.fn();
      }
    },
    pending: () => armed.size,
  };
}

const ENVELOPE = (port: number, token: string): string =>
  JSON.stringify({ valid: true, diagnostics: [], preview: { unit: 'my-post', url: `http://127.0.0.1:${port}/?token=${token}`, path: null }, feedback: null }) + '\n';

function harness() {
  const children: FakeChild[] = [];
  const spawned: { binary: string; args: readonly string[] }[] = [];
  const timers = manualTimers();
  const sessions = createReviewSessions({
    binary: '/opt/wa-cli',
    spawn: (binary, args) => {
      spawned.push({ binary, args });
      const child = new FakeChild();
      children.push(child);
      return child;
    },
    now: () => '2026-10-21T00:00:00Z',
    timers,
    startTimeoutMs: 10_000,
    idleMs: 1_800_000,
  });
  return { sessions, children, spawned, timers };
}

describe('review session start', () => {
  it('spawns wa-cli preview once and keeps the token out of the summary (start-ok)', async () => {
    const { sessions, children, spawned } = harness();
    const started = sessions.start({ unit: 'my-post', root: '/home/me/book' });
    children[0]!.emit(ENVELOPE(41234, 'abcdef0123456789abcdef0123456789'));
    const result = await started;
    expect(result).toEqual({ ok: true, session: { unit: 'my-post', root: '/home/me/book', startedAt: '2026-10-21T00:00:00Z' } });
    expect(JSON.stringify(result)).not.toContain('abcdef0123456789');
    expect(JSON.stringify(result)).not.toContain('41234');
    expect(spawned).toEqual([{ binary: '/opt/wa-cli', args: ['preview', 'my-post', '--serve', '--json', '--dir', '/home/me/book'] }]);
    expect(sessions.status()).toEqual({ unit: 'my-post', root: '/home/me/book', startedAt: '2026-10-21T00:00:00Z' });
    expect(sessions.upstream()).toEqual({ port: 41234, token: 'abcdef0123456789abcdef0123456789' });
  });

  it('accepts the envelope split across chunks and keeps draining afterwards', async () => {
    const { sessions, children } = harness();
    const started = sessions.start({ unit: 'my-post', root: '/r' });
    const line = ENVELOPE(5000, 'tok');
    children[0]!.emit(line.slice(0, 20));
    children[0]!.emit(line.slice(20));
    expect((await started).ok).toBe(true);
    // Later event lines (one per saved feedback) are drained without effect.
    children[0]!.emit('{"feedback":{"id":"fb-1"}}\n'.repeat(1000));
    expect(sessions.upstream()?.port).toBe(5000);
  });

  it('replaces a running session: the old child is killed, one child remains (start-replaces)', async () => {
    const { sessions, children } = harness();
    const first = sessions.start({ unit: 'my-post', root: '/r' });
    children[0]!.emit(ENVELOPE(5000, 'one'));
    await first;
    const second = sessions.start({ unit: 'my-post/intro', root: '/r' });
    expect(children[0]!.killed).toBe(1);
    children[1]!.emit(ENVELOPE(5001, 'two'));
    await second;
    expect(sessions.status()?.unit).toBe('my-post/intro');
    expect(sessions.upstream()).toEqual({ port: 5001, token: 'two' });
    expect(children[1]!.killed).toBe(0);
  });

  it('reports wa-cli diagnostics from a failure envelope and leaves no session (start-bad-unit)', async () => {
    const { sessions, children } = harness();
    const started = sessions.start({ unit: 'nope', root: '/r' });
    // wa-cli prints its failure envelope and exits 1; the line may arrive unterminated.
    children[0]!.emit(JSON.stringify({ valid: false, preview: null, diagnostics: [{ code: 'unit.not-found', message: '找不到单元 nope' }] }));
    children[0]!.close(1);
    const result = await started;
    expect(result).toEqual({ ok: false, error: '找不到单元 nope', diagnostics: [{ code: 'unit.not-found', message: '找不到单元 nope' }] });
    expect(sessions.status()).toBeNull();
    expect(sessions.upstream()).toBeNull();
    expect(children[0]!.killed).toBe(1);
  });

  it('times out without a first line, kills the child and reports the reason', async () => {
    const { sessions, children, timers } = harness();
    const started = sessions.start({ unit: 'my-post', root: '/r' });
    timers.fire(10_000);
    const result = await started;
    expect(result.ok).toBe(false);
    expect(result.ok === false && result.error).toContain('10 秒');
    expect(children[0]!.killed).toBe(1);
    expect(sessions.status()).toBeNull();
  });

  it('reports a binary that cannot start (spawn error event or throw)', async () => {
    const { sessions, children } = harness();
    const started = sessions.start({ unit: 'my-post', root: '/r' });
    children[0]!.fail(Object.assign(new Error('spawn wa-cli ENOENT'), { code: 'ENOENT' }));
    const result = await started;
    expect(result.ok === false && result.error).toContain('ENOENT');
    const throwing = createReviewSessions({ binary: 'x', spawn: () => { throw new Error('boom'); }, now: () => 't', timers: manualTimers() });
    expect(await throwing.start({ unit: 'u', root: '/r' })).toEqual({ ok: false, error: 'wa-cli 无法启动：Error: boom', diagnostics: [] });
  });

  it('rejects a first line that is not an envelope, or a non-loopback url', async () => {
    const { sessions, children } = harness();
    const a = sessions.start({ unit: 'u', root: '/r' });
    children[0]!.emit('not json\n');
    expect((await a).ok).toBe(false);
    const b = sessions.start({ unit: 'u', root: '/r' });
    children[1]!.emit(JSON.stringify({ valid: true, preview: { url: 'http://10.0.0.5:80/?token=x' } }) + '\n');
    expect(await b).toEqual({ ok: false, error: '阅读页服务没有给出本地地址', diagnostics: [] });
  });
});

describe('review session end', () => {
  async function running() {
    const h = harness();
    const started = h.sessions.start({ unit: 'my-post', root: '/r' });
    h.children[0]!.emit(ENVELOPE(5000, 'tok'));
    await started;
    return h;
  }

  it('ends explicitly and kills the child (end-explicit)', async () => {
    const { sessions, children } = await running();
    sessions.end();
    expect(children[0]!.killed).toBe(1);
    expect(sessions.status()).toBeNull();
    expect(sessions.upstream()).toBeNull();
  });

  it('ends after the idle limit with no proxied request (end-idle), and each proxied request re-arms it', async () => {
    const { sessions, children, timers } = await running();
    expect(timers.pending()).toBe(1);
    sessions.upstream();
    expect(timers.pending()).toBe(1);
    timers.fire(1_800_000);
    expect(children[0]!.killed).toBe(1);
    expect(sessions.status()).toBeNull();
  });

  it('ends when the child exits by itself', async () => {
    const { sessions, children, timers } = await running();
    children[0]!.close(0);
    expect(sessions.status()).toBeNull();
    expect(timers.pending()).toBe(0);
  });

  it('dispose kills the running child (end-dispose)', async () => {
    const { sessions, children } = await running();
    sessions.dispose();
    expect(children[0]!.killed).toBe(1);
    expect(sessions.status()).toBeNull();
  });
});

describe('review session helpers', () => {
  it('reads port and token only from a loopback http url with a token', () => {
    expect(upstreamOfEnvelope({ preview: { url: 'http://127.0.0.1:8080/?token=t' } })).toEqual({ port: 8080, token: 't' });
    expect(upstreamOfEnvelope({ preview: { url: 'http://127.0.0.1:8080/' } })).toBeNull();
    expect(upstreamOfEnvelope({ preview: { url: 'https://127.0.0.1:8080/?token=t' } })).toBeNull();
    expect(upstreamOfEnvelope({ preview: { url: 'nonsense' } })).toBeNull();
    expect(upstreamOfEnvelope({ preview: { url: null } })).toBeNull();
    expect(upstreamOfEnvelope(null)).toBeNull();
  });

  it('collects well-formed diagnostics only', () => {
    expect(diagnosticsOfEnvelope({ diagnostics: [{ code: 'a', message: 'b' }, { code: 1 }, null] })).toEqual([{ code: 'a', message: 'b' }]);
    expect(diagnosticsOfEnvelope({})).toEqual([]);
  });

  it('accepts unit refs and refuses flag-shaped or control-character input', () => {
    expect(validReviewUnit(' my-novel/scene-1 ')).toBe('my-novel/scene-1');
    expect(validReviewUnit('--output=x')).toBeNull();
    expect(validReviewUnit('-x')).toBeNull();
    expect(validReviewUnit('a\nb')).toBeNull();
    expect(validReviewUnit('')).toBeNull();
    expect(validReviewUnit('x'.repeat(513))).toBeNull();
    expect(validReviewUnit(42)).toBeNull();
  });
});
