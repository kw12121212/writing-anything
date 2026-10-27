import { describe, expect, it } from 'vitest';
import {
  COPY_TEXT,
  copyPart,
  primaryDiagnostic,
  segmentLabel,
  IDLE_COPY,
  browserClipboardEnv,
  copyPending,
  copyRouteUrl,
  copyStateAfter,
  embeddedCount,
  parseCopyBody,
  plainTextOf,
  runCopy,
  workOf,
  type ClipboardEnv,
  type CopyResult,
} from './copyview';

const FRAGMENT =
  '<section style="a"><h1 style="b">周六的店</h1><p style="c">店里&lt;新来&gt;了&amp;一台<strong style="">烘豆机</strong>。<br>第二行</p>' +
  '<section style="d"><img src="data:image/png;base64,AAA&#43;/=" alt="店门口" style="e"><p style="f">连接池</p></section>' +
  '<section style="g"><p style="h">【配图 2】外</p><p style="i">https://x.cn/a.png</p></section><ul style="j"><li style="k">一</li><li style="k">二</li></ul></section>';

/** A recording clipboard double. */
function fakeEnv(opts: { rich: boolean; writeFails?: boolean; selection?: boolean }) {
  const calls: string[] = [];
  const written: { html: string; text: string }[] = [];
  const env: ClipboardEnv = {
    canWriteRich: () => opts.rich,
    writeRich: async (html, text) => {
      calls.push('writeRich');
      const [h, t] = await Promise.all([html, text]);
      if (opts.writeFails) throw new Error('NotAllowedError');
      written.push({ html: h, text: t });
    },
    selectAndCopy: (html) => {
      calls.push('select:' + html.length);
      return opts.selection ?? true;
    },
  };
  return { env, calls, written };
}

const ok = (html = FRAGMENT, images = 2): Promise<CopyResult> => Promise.resolve({ ok: true, html, images, segments: [html] });
// The real wa-cli order: a warning per skipped unit first, then the deciding error.
const refusedDiagnostics = [
  { code: 'export.unaccepted', severity: 'warning', message: '单元 my-post 没有导出：尚未验收', next: ['wa-cli review show my-post --dir /r --json'] },
  { code: 'export.nothing-accepted', severity: 'error', message: '作品 my-post 没有已验收的单元，无法导出', next: ['wa-cli next --dir /r --json'] },
];
const refused: CopyResult = { ok: false, error: '作品 my-post 没有已验收的单元，无法导出', diagnostics: refusedDiagnostics };

describe('copy helpers', () => {
  it('builds the route URL for the work and the shown directory', () => {
    expect(copyRouteUrl('my-post', null)).toBe('/dsh-writing-viewer/copy?work=my-post');
    expect(copyRouteUrl('my post', '/home/a b')).toBe('/dsh-writing-viewer/copy?work=my+post&root=%2Fhome%2Fa+b');
    expect(workOf('my-post/intro/x')).toBe('my-post');
    expect(workOf('my-post')).toBe('my-post');
  });

  it('parses the route body tolerantly', () => {
    expect(parseCopyBody({ ok: true, html: '<p>x</p>', images: 3 }, 200)).toEqual({ ok: true, html: '<p>x</p>', images: 3, segments: ['<p>x</p>'] });
    expect(parseCopyBody({ ok: true, html: '<p>x</p>' }, 200)).toEqual({ ok: true, html: '<p>x</p>', images: 0, segments: ['<p>x</p>'] });
    expect(parseCopyBody({ ok: true, html: '<p>a</p><p>b</p>', segments: ['<p>a</p>', 2, '<p>b</p>'] }, 200)).toEqual({ ok: true, html: '<p>a</p><p>b</p>', images: 0, segments: ['<p>a</p>', '<p>b</p>'] });
    expect(parseCopyBody(null, 502)).toEqual({ ok: false, error: '复制路由返回了无法解析的内容（HTTP 502）', diagnostics: [] });
    expect(parseCopyBody({ ok: false, diagnostics: [{ code: 'x', message: 'm', next: ['n', 1] }, null] }, 200)).toEqual({
      ok: false, error: '复制失败（HTTP 200）', diagnostics: [{ code: 'x', severity: '', message: 'm', next: ['n'] }],
    });
    expect(parseCopyBody({ ok: false, error: 'forbidden' }, 403)).toEqual({ ok: false, error: 'forbidden', diagnostics: [] });
  });

  it('derives plain text and the embedded count from a wa-cli fragment', () => {
    expect(plainTextOf(FRAGMENT)).toBe('周六的店\n\n店里<新来>了&一台烘豆机。\n第二行\n\n连接池\n\n【配图 2】外\n\nhttps://x.cn/a.png\n\n一\n\n二');
    expect(plainTextOf('<p>&#26085;&#x672c;&nbsp;&unknown;</p>')).toBe('日本 &unknown;');
    expect(embeddedCount(FRAGMENT)).toBe(1);
    expect(embeddedCount('<p>no</p>')).toBe(0);
  });
});

describe('runCopy', () => {
  it('writes text/html and text/plain through the async clipboard (copy-accepted-work)', async () => {
    const { env, calls, written } = fakeEnv({ rich: true });
    const outcome = await runCopy(ok(), env);
    expect(outcome).toEqual({ kind: 'copied', via: 'clipboard', embedded: 1, placeholders: 1 });
    expect(calls).toEqual(['writeRich']);
    expect(written).toEqual([{ html: FRAGMENT, text: plainTextOf(FRAGMENT) }]);
  });

  it('calls the clipboard write synchronously, before the fetch settles', () => {
    const { env, calls } = fakeEnv({ rich: true });
    let resolve: (r: CopyResult) => void = () => {};
    void runCopy(new Promise((r) => (resolve = r)), env);
    // No await yet: the user activation of the click must still be live.
    expect(calls).toEqual(['writeRich']);
    resolve({ ok: true, html: FRAGMENT, images: 1, segments: [FRAGMENT] });
  });

  it('falls back to the selection copy when the async write is refused', async () => {
    const { env, calls } = fakeEnv({ rich: true, writeFails: true });
    expect(await runCopy(ok(), env)).toEqual({ kind: 'copied', via: 'selection', embedded: 1, placeholders: 1 });
    expect(calls).toEqual(['writeRich', 'select:' + FRAGMENT.length]);
  });

  it('uses the selection copy outside a secure context (copy-insecure-context)', async () => {
    const { env, calls } = fakeEnv({ rich: false });
    expect(await runCopy(ok(FRAGMENT, 1), env)).toEqual({ kind: 'copied', via: 'selection', embedded: 1, placeholders: 0 });
    expect(calls).toEqual(['select:' + FRAGMENT.length]);
  });

  it('keeps the fragment for a second click when the browser refuses both ways', async () => {
    const { env } = fakeEnv({ rich: false, selection: false });
    const outcome = await runCopy(ok(), env);
    expect(outcome).toEqual({ kind: 'retry', html: FRAGMENT, images: 2 });
    const state = copyStateAfter('my-post', outcome, COPY_TEXT.zh);
    expect(state.pending).toEqual({ work: 'my-post', html: FRAGMENT, images: 2 });
    expect(state.notice).toEqual({ text: COPY_TEXT.zh.retry, ok: false, next: [] });
    const again = fakeEnv({ rich: false, selection: true });
    expect(copyPending(state.pending!, again.env)).toEqual({ kind: 'copied', via: 'selection', embedded: 1, placeholders: 1 });
    expect(copyPending(state.pending!, fakeEnv({ rich: false, selection: false }).env).kind).toBe('retry');
  });

  it('writes nothing and reports the diagnostic when wa-cli refuses (copy-nothing-accepted)', async () => {
    for (const rich of [true, false]) {
      const { env, calls, written } = fakeEnv({ rich });
      const outcome = await runCopy(Promise.resolve(refused), env);
      expect(outcome).toEqual({ kind: 'refused', error: '作品 my-post 没有已验收的单元，无法导出', diagnostics: refusedDiagnostics });
      expect(written).toEqual([]);
      expect(calls.some((c) => c.startsWith('select'))).toBe(false);
      const state = copyStateAfter('my-post', outcome, COPY_TEXT.zh);
      expect(state).toEqual({
        work: 'my-post', busy: false, pending: null, segments: null,
        notice: { text: '复制失败：export.nothing-accepted 作品 my-post 没有已验收的单元，无法导出', ok: false, next: ['wa-cli next --dir /r --json'] },
      });
    }
    expect(copyStateAfter('w', { kind: 'refused', error: 'boom', diagnostics: [] }, COPY_TEXT.en).notice?.text).toBe('Copy failed：boom');
  });

  it('picks the first error-level diagnostic, else the first with a message', () => {
    expect(primaryDiagnostic(refusedDiagnostics)?.code).toBe('export.nothing-accepted');
    expect(primaryDiagnostic([{ code: 'a', severity: 'warning', message: '', next: [] }, { code: 'b', severity: 'warning', message: 'm', next: [] }])?.code).toBe('b');
    expect(primaryDiagnostic([])).toBeUndefined();
    expect(parseCopyBody({ ok: false, diagnostics: [{ code: 'x', severity: 'error', message: 'm' }] }, 200)).toEqual({
      ok: false, error: '复制失败（HTTP 200）', diagnostics: [{ code: 'x', severity: 'error', message: 'm', next: [] }],
    });
  });

  it('writes nothing for a multi-part work and offers its parts (copy-segments)', async () => {
    const parts = ['<section><img src="data:image/png;base64,A"><img src="data:image/png;base64,B"></section>', '<section><img src="data:image/png;base64,C"></section>'];
    for (const rich of [true, false]) {
      const { env, calls, written } = fakeEnv({ rich });
      const outcome = await runCopy(Promise.resolve({ ok: true, html: parts.join(''), images: 3, segments: parts }), env);
      expect(outcome).toEqual({ kind: 'segmented', html: parts.join(''), images: 3, segments: parts });
      expect(written).toEqual([]);
      expect(calls.some((c) => c.startsWith('select'))).toBe(false);
    }
    const state = copyStateAfter('my-post', { kind: 'segmented', html: parts.join(''), images: 3, segments: parts }, COPY_TEXT.zh);
    expect(state.segments).toEqual({ work: 'my-post', html: parts.join(''), images: 3, parts });
    expect(state.notice?.text).toBe('2 ' + COPY_TEXT.zh.segmented);
    expect([segmentLabel(parts, 0, 'zh'), segmentLabel(parts, 1, 'zh'), segmentLabel(parts, 1, 'en')]).toEqual(['复制第 1 段（图 1–2）', '复制第 2 段（图 3–3）', 'Copy part 2 (images 3–3)']);
    // Copying one part writes just that part and keeps the parts for the next click.
    const { env, written } = fakeEnv({ rich: true });
    const one = await copyPart(parts[1]!, 1, env);
    expect(one).toEqual({ kind: 'copied', via: 'clipboard', embedded: 1, placeholders: 0 });
    expect(written.map((w) => w.html)).toEqual([parts[1]]);
    const after = copyStateAfter('my-post', one, COPY_TEXT.zh, state.segments, '复制第 2 段（图 3–3）');
    expect(after.segments).toBe(state.segments);
    expect(after.notice?.text).toBe('已复制 复制第 2 段（图 3–3） · ' + COPY_TEXT.zh.copied + ' · 内嵌配图 1');
    // A refused part copy keeps the parts (its own button retries) instead of a pending fragment.
    const retry = copyStateAfter('my-post', { kind: 'retry', html: parts[0]!, images: 2 }, COPY_TEXT.zh, state.segments, 'x');
    expect([retry.pending, retry.segments]).toEqual([null, state.segments]);
  });

  it('summarizes a successful copy with embedded and placeholder counts', () => {
    expect(copyStateAfter('w', { kind: 'copied', via: 'clipboard', embedded: 2, placeholders: 0 }, COPY_TEXT.zh).notice).toEqual({
      text: COPY_TEXT.zh.copied + ' · 内嵌配图 2', ok: true, next: [],
    });
    expect(copyStateAfter('w', { kind: 'copied', via: 'selection', embedded: 1, placeholders: 3 }, COPY_TEXT.zh).notice?.text).toBe(
      COPY_TEXT.zh.copied + ' · 内嵌配图 1 · 3 ' + COPY_TEXT.zh.placeholders,
    );
    expect(IDLE_COPY).toEqual({ work: null, busy: false, notice: null, pending: null, segments: null });
  });
});

describe('browserClipboardEnv', () => {
  class FakeItem {
    constructor(public items: Record<string, Promise<Blob>>) {}
  }

  it('needs a secure context, navigator.clipboard.write and ClipboardItem for the rich path', () => {
    const write = async () => {};
    expect(browserClipboardEnv({ isSecureContext: true, navigator: { clipboard: { write } }, ClipboardItem: FakeItem, Blob }).canWriteRich()).toBe(true);
    expect(browserClipboardEnv({ isSecureContext: false, navigator: { clipboard: { write } }, ClipboardItem: FakeItem, Blob }).canWriteRich()).toBe(false);
    expect(browserClipboardEnv({ isSecureContext: true, navigator: {}, ClipboardItem: FakeItem, Blob }).canWriteRich()).toBe(false);
    expect(browserClipboardEnv({ isSecureContext: true, navigator: { clipboard: { write } }, Blob }).canWriteRich()).toBe(false);
  });

  it('writes one ClipboardItem with text/html and text/plain blobs', async () => {
    const items: unknown[][] = [];
    const env = browserClipboardEnv({
      isSecureContext: true, Blob, ClipboardItem: FakeItem,
      navigator: { clipboard: { write: async (list) => void items.push(list) } },
    });
    await env.writeRich(Promise.resolve('<p>甲</p>'), Promise.resolve('甲'));
    expect(items).toHaveLength(1);
    const item = items[0]![0] as FakeItem;
    const html = await item.items['text/html']!;
    const text = await item.items['text/plain']!;
    expect([html.type, await html.text(), text.type, await text.text()]).toEqual(['text/html', '<p>甲</p>', 'text/plain', '甲']);
  });

  it('copies both flavours through a copy event without putting the fragment into the DOM', () => {
    const log: string[] = [];
    const data: Record<string, string> = {};
    const area = { value: '', attrs: {} as Record<string, string>, setAttribute(n: string, v: string) { this.attrs[n] = v; }, select() { log.push('select'); } };
    let listener: ((event: { clipboardData?: { setData(t: string, v: string): void } | null; preventDefault(): void }) => void) | null = null;
    const created: string[] = [];
    const make = (result: boolean | 'throw', clipboardData = true) => browserClipboardEnv({
      document: {
        body: { appendChild: () => log.push('append'), removeChild: () => log.push('remove') },
        createElement: (tag: string) => {
          created.push(tag);
          return area;
        },
        addEventListener: (_type, l) => {
          listener = l;
          log.push('listen');
        },
        removeEventListener: (_type, l) => {
          if (listener === l) listener = null;
          log.push('unlisten');
        },
        execCommand: (c: string) => {
          log.push('exec:' + c);
          if (result === 'throw') throw new Error('denied');
          // The browser fires the copy event synchronously inside execCommand.
          listener?.({ clipboardData: clipboardData ? { setData: (t, v) => (data[t] = v) } : null, preventDefault: () => log.push('prevent') });
          return result;
        },
      },
    });
    expect(make(true).selectAndCopy('<p>甲</p>', '甲')).toBe(true);
    expect(data).toEqual({ 'text/html': '<p>甲</p>', 'text/plain': '甲' });
    // Only a textarea is created, and it holds the plain text, never the markup.
    expect(created).toEqual(['textarea']);
    expect(area.value).toBe('甲');
    expect(area.attrs['style']).toContain('left: -100000px');
    expect(log).toEqual(['listen', 'append', 'select', 'exec:copy', 'prevent', 'unlisten', 'remove']);
    expect(listener).toBeNull();
    log.length = 0;
    expect(make(false).selectAndCopy('x', 'x')).toBe(false);
    expect(make('throw').selectAndCopy('x', 'x')).toBe(false);
    // execCommand claiming success without a clipboardData is not a copy.
    expect(make(true, false).selectAndCopy('x', 'x')).toBe(false);
    expect(log.filter((l) => l === 'remove')).toHaveLength(3);
    expect(browserClipboardEnv({}).selectAndCopy('x', 'x')).toBe(false);
  });
});
