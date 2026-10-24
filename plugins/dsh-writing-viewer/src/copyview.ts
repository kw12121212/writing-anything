/**
 * 「复制图文」 (copy-illustrated): the panel copies one whole work as rich
 * text — the paste-ready fragment wa-cli renders with
 * `wa-cli export <work> --format paste-html --print --json` (accepted units
 * only, usable illustrations inlined as data URIs, no cover image and no
 * AI-generated Content Label). The panel never renders the body or judges
 * acceptance itself; it only fetches that fragment from the host's read-only
 * copy route and writes it to the clipboard as text/html plus text/plain.
 *
 * Clipboard writes need the click's user activation. The async Clipboard API
 * path therefore builds its ClipboardItem synchronously inside the click with
 * promised blobs, so the activation carries over the fetch. Outside a secure
 * context (http over Tailscale) that API is missing and the fallback selects
 * an offscreen copy of the fragment and runs execCommand('copy'); when the
 * browser refuses that after the fetch, the fragment is kept and the next
 * click copies it synchronously. The fallback never inserts the fragment into
 * the DOM (dsh-plugin-structure/client-content-rendering-safety): a copy
 * event listener hands both flavours to clipboardData while an offscreen
 * textarea holding the plain text provides the selection.
 */

export const COPY_ROUTE = '/dsh-writing-viewer/copy';

/** One wa-cli diagnostic as the panel shows it. */
export interface CopyDiagnostic {
  code: string;
  severity: string;
  message: string;
  next: string[];
}

/**
 * The diagnostic a failure shows: the first error-level one, else the first
 * with a message. wa-cli lists warnings (export.unaccepted per skipped unit)
 * before the error that decides the outcome (export.nothing-accepted), so
 * "the first diagnostic" would show a warning's cause and next step.
 */
export function primaryDiagnostic(diagnostics: readonly CopyDiagnostic[]): CopyDiagnostic | undefined {
  return diagnostics.find((d) => d.severity === 'error' && d.message !== '') ?? diagnostics.find((d) => d.message !== '');
}

/** What the host's copy route answered, parsed. */
export type CopyResult =
  /** `segments` holds the parts the WeChat editor can take one paste at a time (at most 7 images each); always at least one. */
  | { ok: true; html: string; images: number; segments: string[] }
  | { ok: false; error: string; diagnostics: CopyDiagnostic[] };

/** How one copy attempt ended. */
export type CopyOutcome =
  | { kind: 'copied'; via: 'clipboard' | 'selection'; embedded: number; placeholders: number }
  | { kind: 'refused'; error: string; diagnostics: CopyDiagnostic[] }
  /** The fragment arrived but the browser refused the write: copy it again on the next click. */
  | { kind: 'retry'; html: string; images: number }
  /** More than one segment: nothing is written; the panel offers one button per segment plus「复制全文」. */
  | { kind: 'segmented'; html: string; images: number; segments: string[] };

/** The panel's copy state. `pending` holds a fetched fragment the browser refused to copy. */
export interface CopyState {
  work: string | null;
  busy: boolean;
  notice: { text: string; ok: boolean; next: string[] } | null;
  pending: { work: string; html: string; images: number } | null;
  /** The work's segments while it has more than one (each copied by its own button), else null. */
  segments: { work: string; html: string; images: number; parts: string[] } | null;
}

export const IDLE_COPY: CopyState = { work: null, busy: false, notice: null, pending: null, segments: null };

export interface CopyLabels {
  copy: string;
  copyAll: string;
  segmented: string;
  copiedPart: string;
  copying: string;
  copied: string;
  embedded: string;
  placeholders: string;
  failed: string;
  retry: string;
}

export const COPY_TEXT: { zh: CopyLabels; en: CopyLabels } = {
  zh: {
    copy: '复制图文',
    copyAll: '复制全文',
    segmented: '段：公众号每次粘贴最多 7 张图，请依次点「复制第 n 段」逐段粘贴；粘贴到飞书等在线文档时可以用「复制全文」',
    copiedPart: '已复制',
    copying: '正在复制…',
    copied: '已复制，可以到公众号编辑器或在线文档中粘贴',
    embedded: '内嵌配图',
    placeholders: '处配图未能内嵌（「【配图 n】」占位），请按导出的配图清单补传',
    failed: '复制失败',
    retry: '浏览器拒绝了这次复制，请再点一次「复制图文」；仍不行时用 wa-cli export --format paste-html 导出可粘贴页复制',
  },
  en: {
    copy: 'Copy with images',
    copyAll: 'Copy all',
    segmented: 'parts: the WeChat editor takes at most 7 images per paste, so copy and paste the parts one by one; use Copy all for online documents',
    copiedPart: 'Copied',
    copying: 'Copying…',
    copied: 'Copied — paste it into the WeChat editor or an online document',
    embedded: 'images embedded',
    placeholders: 'image(s) not embedded (「【配图 n】」 placeholders); upload them from the exported image list',
    failed: 'Copy failed',
    retry: 'The browser refused this copy; click Copy with images again, or export the paste-ready page with wa-cli export --format paste-html',
  },
};

export function copyLabelsFor(active?: string | null): CopyLabels {
  const lang = String(
    active || (typeof document !== 'undefined' && document.documentElement.lang) ||
      (typeof navigator !== 'undefined' && navigator.language) || 'en',
  ).toLowerCase();
  return lang.indexOf('zh') === 0 ? COPY_TEXT.zh : COPY_TEXT.en;
}

/** The label of segment `index` (0-based): its embedded image range, counted across the parts. */
export function segmentLabel(parts: readonly string[], index: number, active?: string | null): string {
  let first = 1;
  for (let i = 0; i < index; i += 1) first += embeddedCount(parts[i] ?? '');
  const last = first + embeddedCount(parts[index] ?? '') - 1;
  const zh = copyLabelsFor(active) === COPY_TEXT.zh;
  return zh ? `复制第 ${index + 1} 段（图 ${first}–${last}）` : `Copy part ${index + 1} (images ${first}–${last})`;
}

/** The work a unit reference belongs to (the part before the first `/`). */
export function workOf(ref: string): string {
  const slash = ref.indexOf('/');
  return slash < 0 ? ref : ref.slice(0, slash);
}

/** The copy route URL for one work in the directory the panel shows. */
export function copyRouteUrl(work: string, root: string | null): string {
  const query = new URLSearchParams({ work });
  if (root !== null) query.set('root', root);
  return COPY_ROUTE + '?' + query.toString();
}

function diagnosticsOf(value: unknown): CopyDiagnostic[] {
  if (!Array.isArray(value)) return [];
  const out: CopyDiagnostic[] = [];
  for (const entry of value) {
    if (entry === null || typeof entry !== 'object') continue;
    const d = entry as { code?: unknown; message?: unknown; next?: unknown };
    out.push({
      code: typeof d.code === 'string' ? d.code : '',
      severity: typeof (d as { severity?: unknown }).severity === 'string' ? (d as { severity: string }).severity : '',
      message: typeof d.message === 'string' ? d.message : '',
      next: Array.isArray(d.next) ? d.next.filter((n): n is string => typeof n === 'string') : [],
    });
  }
  return out;
}

/** Parse the copy route body; anything unexpected is a readable failure. */
export function parseCopyBody(body: unknown, status: number): CopyResult {
  if (body === null || typeof body !== 'object') return { ok: false, error: `复制路由返回了无法解析的内容（HTTP ${status}）`, diagnostics: [] };
  const b = body as { ok?: unknown; html?: unknown; images?: unknown; segments?: unknown; error?: unknown; diagnostics?: unknown };
  if (b.ok === true && typeof b.html === 'string') {
    const parts = Array.isArray(b.segments) ? b.segments.filter((p): p is string => typeof p === 'string') : [];
    return { ok: true, html: b.html, images: typeof b.images === 'number' ? b.images : 0, segments: parts.length > 0 ? parts : [b.html] };
  }
  return { ok: false, error: typeof b.error === 'string' ? b.error : `复制失败（HTTP ${status}）`, diagnostics: diagnosticsOf(b.diagnostics) };
}

const ENTITIES: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ' };

function decodeEntities(text: string): string {
  return text.replace(/&(#x[0-9a-f]+|#[0-9]+|[a-z]+);/gi, (whole, name: string) => {
    if (name[0] === '#') {
      const code = name[1] === 'x' || name[1] === 'X' ? parseInt(name.slice(2), 16) : parseInt(name.slice(1), 10);
      return Number.isFinite(code) && code >= 0 && code <= 0x10ffff ? String.fromCodePoint(code) : whole;
    }
    return ENTITIES[name.toLowerCase()] ?? whole;
  });
}

/**
 * The text/plain flavour of a wa-cli paste fragment: block ends and br become
 * line breaks, images drop out, entities decode. The fragment is wa-cli's own
 * html/template output (all text escaped, a fixed tag set), so stripping its
 * tags is exact; it is never applied to arbitrary HTML.
 */
export function plainTextOf(html: string): string {
  const text = html
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<\/(p|h[1-6]|li|pre|blockquote)>/gi, '\n\n')
    .replace(/<[^>]*>/g, '');
  return decodeEntities(text).replace(/\n{3,}/g, '\n\n').trim();
}

/** How many images of the fragment are embedded (img elements). */
export function embeddedCount(html: string): number {
  return (html.match(/<img\b/gi) ?? []).length;
}

/** Clipboard capabilities the copy flow uses; injectable for tests. */
export interface ClipboardEnv {
  /** The async Clipboard API with ClipboardItem is usable (secure context). */
  canWriteRich(): boolean;
  /** Write html and text as one ClipboardItem. Called synchronously in the click. */
  writeRich(html: Promise<string>, text: Promise<string>): Promise<void>;
  /** Copy html and text through a copy event and execCommand('copy'), without inserting html into the DOM. */
  selectAndCopy(html: string, text: string): boolean;
}

interface CopyEventLike {
  clipboardData?: { setData(type: string, value: string): void } | null;
  preventDefault(): void;
}

interface ClipboardGlobals {
  isSecureContext?: boolean;
  navigator?: { clipboard?: { write?: (items: unknown[]) => Promise<void> } };
  ClipboardItem?: new (items: Record<string, Promise<Blob>>) => unknown;
  Blob?: typeof Blob;
  document?: {
    body?: { appendChild(node: unknown): unknown; removeChild(node: unknown): unknown } | null;
    createElement(tag: string): unknown;
    addEventListener(type: 'copy', listener: (event: CopyEventLike) => void): void;
    removeEventListener(type: 'copy', listener: (event: CopyEventLike) => void): void;
    execCommand(command: string): boolean;
  };
}

/** The real clipboard environment over the page globals. */
export function browserClipboardEnv(g: ClipboardGlobals): ClipboardEnv {
  return {
    canWriteRich: () =>
      g.isSecureContext === true && typeof g.navigator?.clipboard?.write === 'function' && typeof g.ClipboardItem === 'function' && typeof g.Blob === 'function',
    writeRich: (html, text) => {
      const BlobCtor = g.Blob as typeof Blob;
      const Item = g.ClipboardItem as NonNullable<ClipboardGlobals['ClipboardItem']>;
      const htmlBlob = html.then((h) => new BlobCtor([h], { type: 'text/html' }));
      const textBlob = text.then((t) => new BlobCtor([t], { type: 'text/plain' }));
      // A refused request rejects both; the clipboard may stop at the first,
      // so the other is marked handled here (runCopy reports the refusal).
      htmlBlob.catch(() => undefined);
      textBlob.catch(() => undefined);
      const item = new Item({ 'text/html': htmlBlob, 'text/plain': textBlob });
      return (g.navigator?.clipboard?.write as (items: unknown[]) => Promise<void>)([item]);
    },
    selectAndCopy: (html, text) => {
      const doc = g.document;
      if (doc === undefined || doc.body === null || doc.body === undefined) return false;
      // The selection is plain text in a textarea (its value is never parsed
      // as HTML); the copy listener replaces what the browser would copy with
      // both flavours, so the fragment never enters the DOM.
      const area = doc.createElement('textarea') as { value: string; setAttribute(name: string, value: string): void; select(): void };
      area.setAttribute('style', 'position: fixed; left: -100000px; top: 0; width: 1px; height: 1px; opacity: 0');
      area.setAttribute('aria-hidden', 'true');
      area.setAttribute('readonly', '');
      area.value = text;
      let delivered = false;
      const onCopy = (event: CopyEventLike): void => {
        if (event.clipboardData === null || event.clipboardData === undefined) return;
        event.clipboardData.setData('text/html', html);
        event.clipboardData.setData('text/plain', text);
        event.preventDefault();
        delivered = true;
      };
      doc.addEventListener('copy', onCopy);
      doc.body.appendChild(area);
      try {
        area.select();
        return doc.execCommand('copy') === true && delivered;
      } catch {
        return false;
      } finally {
        doc.removeEventListener('copy', onCopy);
        doc.body.removeChild(area);
      }
    },
  };
}

function copied(via: 'clipboard' | 'selection', html: string, images: number): CopyOutcome {
  const embedded = embeddedCount(html);
  return { kind: 'copied', via, embedded, placeholders: Math.max(0, images - embedded) };
}

/**
 * Run one copy attempt. `request` is the copy route fetch, already started in
 * the click. With the async Clipboard API the ClipboardItem is created before
 * the first await, so the write keeps the click's user activation.
 */
export async function runCopy(request: Promise<CopyResult>, env: ClipboardEnv): Promise<CopyOutcome> {
  if (env.canWriteRich()) {
    const html = request.then((r) => {
      if (!r.ok) throw new Error(r.error);
      // Several segments: write nothing now; each part gets its own button.
      if (r.segments.length > 1) throw new Error('segmented');
      return r.html;
    });
    const text = html.then(plainTextOf);
    // A refused request rejects both blob promises; the outcome is read from
    // `request` below, so these rejections are handled, never unhandled.
    html.catch(() => undefined);
    text.catch(() => undefined);
    let wrote = false;
    try {
      await env.writeRich(html, text);
      wrote = true;
    } catch {
      wrote = false;
    }
    const result = await request;
    if (!result.ok) return { kind: 'refused', error: result.error, diagnostics: result.diagnostics };
    if (result.segments.length > 1) return { kind: 'segmented', html: result.html, images: result.images, segments: result.segments };
    if (wrote) return copied('clipboard', result.html, result.images);
    return env.selectAndCopy(result.html, plainTextOf(result.html)) ? copied('selection', result.html, result.images) : { kind: 'retry', html: result.html, images: result.images };
  }
  const result = await request;
  if (!result.ok) return { kind: 'refused', error: result.error, diagnostics: result.diagnostics };
  if (result.segments.length > 1) return { kind: 'segmented', html: result.html, images: result.images, segments: result.segments };
  return env.selectAndCopy(result.html, plainTextOf(result.html)) ? copied('selection', result.html, result.images) : { kind: 'retry', html: result.html, images: result.images };
}

/** Copy one segment (or the whole text) the panel already holds, in the click that asked for it. */
export function copyPart(html: string, images: number, env: ClipboardEnv): Promise<CopyOutcome> {
  return runCopy(Promise.resolve({ ok: true, html, images, segments: [html] }), env);
}

/** Copy a fragment kept from a refused attempt, synchronously in the new click. */
export function copyPending(pending: { html: string; images: number }, env: ClipboardEnv): CopyOutcome {
  return env.selectAndCopy(pending.html, plainTextOf(pending.html)) ? copied('selection', pending.html, pending.images) : { kind: 'retry', html: pending.html, images: pending.images };
}

/** The panel state after one attempt. `segments` is the multi-part state to keep (a part copy keeps it). */
export function copyStateAfter(work: string, outcome: CopyOutcome, labels: CopyLabels, segments: CopyState['segments'] = null, part?: string): CopyState {
  switch (outcome.kind) {
    case 'copied': {
      let text = (part === undefined ? labels.copied : labels.copiedPart + ' ' + part + ' · ' + labels.copied) + ' · ' + labels.embedded + ' ' + outcome.embedded;
      if (outcome.placeholders > 0) text += ' · ' + outcome.placeholders + ' ' + labels.placeholders;
      return { work, busy: false, notice: { text, ok: true, next: [] }, pending: null, segments };
    }
    case 'refused': {
      const first = primaryDiagnostic(outcome.diagnostics);
      const text = labels.failed + '：' + (first !== undefined ? (first.code !== '' ? first.code + ' ' : '') + first.message : outcome.error);
      return { work, busy: false, notice: { text, ok: false, next: first?.next ?? [] }, pending: null, segments };
    }
    case 'retry':
      // A part is still held in `segments`, so its next click copies it again; a whole copy keeps the fragment in `pending`.
      return { work, busy: false, notice: { text: labels.retry, ok: false, next: [] }, pending: segments === null ? { work, html: outcome.html, images: outcome.images } : null, segments };
    case 'segmented':
      return {
        work, busy: false, pending: null,
        notice: { text: outcome.segments.length + ' ' + labels.segmented, ok: true, next: [] },
        segments: { work, html: outcome.html, images: outcome.images, parts: outcome.segments },
      };
  }
}
