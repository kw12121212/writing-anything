import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  authorReviewRequest,
  confirmKey,
  confirmRequest,
  envelopeMessage,
  feedbackAfterSelection,
  feedbackRequest,
  illustrationLayout,
  illustrationSrc,
  initialConfirmDraft,
  initialFeedback,
  parseReviewPage,
  pendingCountOf,
  pendingIndex,
  pendingOf,
  REVIEW_ILLUSTRATION_ROUTE,
  selectedReviewKeys,
  selectionOutcome,
  sessionGone,
  type PageConfirm,
} from './reviewmodel';

const TESTDATA = join(dirname(fileURLToPath(import.meta.url)), '..', 'testdata');

/** page.json captured from the real wa-cli reading view service. */
async function realPage(): Promise<unknown> {
  return JSON.parse(await readFile(join(TESTDATA, 'cli', 'page-my-post.json'), 'utf8'));
}

/** pendingConfirmations and pendingAuthorReviews from the real next fixture. */
async function realNext(): Promise<{ pendingConfirmations: unknown[]; pendingAuthorReviews: unknown[] }> {
  return JSON.parse(await readFile(join(TESTDATA, 'cli', 'next.json'), 'utf8'));
}

const decision: PageConfirm = {
  kind: 'decision', unit: '', id: 'dec-1', label: 'L', detail: '', options: [{ id: 'a', text: '允许' }, { id: 'b', text: '保持不变' }],
  recommend: 'b', needsAnswer: false, canReject: false,
};

describe('page.json parsing', () => {
  it('parses the real wa-cli page data with every field it renders', async () => {
    const page = parseReviewPage(await realPage());
    expect(page).not.toBeNull();
    expect(page!.title).toBe('my-post');
    expect(page!.units.map((u) => u.ref)).toEqual(['my-post']);
    const unit = page!.units[0]!;
    expect(unit.hasDraft).toBe(true);
    expect(unit.paragraphs.map((p) => [p.index, p.origin])).toEqual([[1, 'agent'], [2, 'agent'], [3, 'agent']]);
    expect(unit.global[0]).toMatchObject({ kind: 'lint', severity: 'warning' });
    expect(page!.confirms.map((c) => [c.kind, c.id, c.recommend, c.options.map((o) => o.id)])).toEqual([['decision', 'dec-1', 'b', ['a', 'b']]]);
    expect(page!.strict).toBe(true);
    expect(page!.reviews).toEqual([]);
  });

  it('rejects non-page bodies and skips malformed entries without failing the view', () => {
    expect(parseReviewPage(null)).toBeNull();
    expect(parseReviewPage({ valid: false, diagnostics: [] })).toBeNull();
    const page = parseReviewPage({
      units: [null, { ref: '' }, { ref: 'u', paragraphs: [{ text: 't', origin: '' }, { nope: 1 }], global: [7, { kind: 'review', label: 'x' }] }],
      confirms: [{ kind: 'decision', options: [{ id: '' }, { id: 'a', text: 'A' }] }, 'x'],
      strict: false,
      reviews: [{ key: 'k', label: 'l' }],
      failures: [{ command: 'c' }],
    });
    expect(page!.units).toHaveLength(1);
    expect(page!.units[0]!.paragraphs).toEqual([{ index: 1, line: 0, text: 't', origin: null, findings: [], illustrations: [] }]);
    expect(page!.units[0]!.illustrations).toEqual([]);
    expect(page!.units[0]!.global).toEqual([{ kind: 'review', label: 'x', severity: '', message: '', suggestion: '' }]);
    expect(page!.confirms[0]!.options).toEqual([{ id: 'a', text: 'A' }]);
    // A non-strict page never shows the review queue.
    expect(page!.reviews).toEqual([]);
    expect(page!.failures).toEqual([{ at: '', command: 'c', exit: 0 }]);
  });
});

describe('illustrations', () => {
  const page = () => parseReviewPage({
    units: [{
      ref: 'my-post',
      paragraphs: [
        { index: 1, text: '第一段', illustrations: [] },
        { index: 2, text: '第二段', illustrations: ['fig-1', 'ghost', 7] },
      ],
      illustrations: [
        { id: 'cover', role: 'cover', alt: '周六的店', title: null, content: 'mood', origin: 'agent', width: 1800, height: 766, claims: [], sources: [], referenced: false, available: true, url: 'illustration?unit=my-post&id=cover' },
        { id: 'fig-1', role: 'body', alt: '延迟对比', title: '优化前后', content: 'infographic', origin: 'agent', width: 1600, height: 900, claims: ['c1'], sources: [], referenced: true, available: true, url: 'http://evil.example/x.png' },
        { id: 'fig-2', role: 'body', alt: '未用', title: null, content: 'mood', origin: 'author', width: 10, height: 10, claims: [], sources: ['s1'], referenced: false, available: false, url: null },
        { role: 'body' },
      ],
    }],
  })!;

  it('parses units[].illustrations and paragraphs[].illustrations (preview-page-json)', () => {
    const u = page().units[0]!;
    expect(u.illustrations.map((i) => i.id)).toEqual(['cover', 'fig-1', 'fig-2']);
    expect(u.illustrations[1]).toEqual({ id: 'fig-1', role: 'body', alt: '延迟对比', title: '优化前后', content: 'infographic', origin: 'agent', width: 1600, height: 900, claims: ['c1'], sources: [], referenced: true, available: true });
    expect(u.paragraphs[1]!.illustrations).toEqual(['fig-1', 'ghost']);
  });

  it('lays out covers first, body images after their paragraph and unreferenced ones last (native-illustrations)', () => {
    const layout = illustrationLayout(page().units[0]!);
    expect(layout.covers.map((i) => i.id)).toEqual(['cover']);
    expect(Object.keys(layout.byParagraph)).toEqual(['2']);
    expect(layout.byParagraph[2]!.map((i) => i.id)).toEqual(['fig-1']);
    expect(layout.unreferenced.map((i) => i.id)).toEqual(['fig-2']);
  });

  it('builds the image address from the fixed proxy route only, never from page.json', () => {
    expect(illustrationSrc('my-post', 'fig-1')).toBe(REVIEW_ILLUSTRATION_ROUTE + '?unit=my-post&id=fig-1');
    expect(illustrationSrc('my-post/s 1', 'a&b')).toBe('/dsh-writing-viewer/review/illustration?unit=my-post%2Fs%201&id=a%26b');
  });

  it('lays out nothing for a unit without illustrations (native-no-illustrations)', () => {
    const layout = illustrationLayout(parseReviewPage({ units: [{ ref: 'u', paragraphs: [{ text: 't' }] }] })!.units[0]!);
    expect(layout).toEqual({ covers: [], byParagraph: {}, unreferenced: [] });
  });
});

describe('envelopes', () => {
  it('reads wa-cli diagnostics, plugin errors, or the status', () => {
    expect(envelopeMessage(400, { valid: false, diagnostics: [{ message: '引文出现两次' }, { message: '另一条' }] })).toBe('引文出现两次；另一条');
    expect(envelopeMessage(403, { ok: false, error: 'Host not allowed' })).toBe('Host not allowed');
    expect(envelopeMessage(500, null)).toBe('HTTP 500');
  });

  it('recognizes only the proxy answers that mean the session is gone', () => {
    expect(sessionGone(409, { diagnostics: [{ code: 'review.no-session' }] })).toBe(true);
    expect(sessionGone(502, { diagnostics: [{ code: 'review.upstream-unavailable' }] })).toBe(true);
    expect(sessionGone(409, { diagnostics: [{ code: 'preview.closing' }] })).toBe(false);
    expect(sessionGone(400, { diagnostics: [{ code: 'review.no-session' }] })).toBe(false);
  });
});

describe('feedback', () => {
  it('quotes only a selection inside one paragraph (native-selection-invalid)', () => {
    const p1 = { unit: 'my-post', index: 1 };
    expect(selectionOutcome(p1, p1, '连接池很重要')).toEqual({ kind: 'quote', unit: 'my-post', quote: '连接池很重要' });
    expect(selectionOutcome(p1, { unit: 'my-post', index: 2 }, 'across')).toEqual({ kind: 'invalid' });
    expect(selectionOutcome(p1, { unit: 'my-post/intro', index: 1 }, 'across')).toEqual({ kind: 'invalid' });
    expect(selectionOutcome(null, p1, 'outside')).toEqual({ kind: 'invalid' });
    expect(selectionOutcome(p1, p1, '   ')).toEqual({ kind: 'none' });
  });

  it('holds back an invalid selection or an empty plain submit and builds the wa-cli body otherwise', () => {
    const draft = initialFeedback('my-post');
    expect(feedbackAfterSelection(draft, { kind: 'none' })).toBe(draft);
    const invalid = feedbackAfterSelection(draft, { kind: 'invalid' });
    expect(feedbackRequest({ ...invalid, text: 'x' }, '')).toEqual({ ok: false, reason: 'selection' });
    expect(feedbackRequest(draft, '')).toEqual({ ok: false, reason: 'empty' });
    const quoted = feedbackAfterSelection(invalid, { kind: 'quote', unit: 'my-post/intro', quote: '连接池很重要' });
    expect(quoted).toMatchObject({ unit: 'my-post/intro', quote: '连接池很重要', invalidSelection: false });
    const plan = feedbackRequest({ ...quoted, text: '补一个例子' }, '');
    expect(plan.ok && JSON.parse(plan.body)).toEqual({ unit: 'my-post/intro', quote: '连接池很重要', text: '补一个例子', kind: 'suggestion', mark: '' });
    // A quick mark needs no text.
    const mark = feedbackRequest(draft, 'stuck');
    expect(mark.ok && JSON.parse(mark.body).mark).toBe('stuck');
  });
});

describe('confirmations', () => {
  it('preselects the recommended option and requires the author summary', () => {
    expect(confirmKey(decision)).toBe('decision::dec-1');
    const draft = initialConfirmDraft(decision);
    expect(draft).toEqual({ option: 'b', answer: '', confirmation: '' });
    expect(initialConfirmDraft({ ...decision, recommend: 'zz' }).option).toBe('a');
    expect(initialConfirmDraft({ ...decision, options: [] }).option).toBe('');
    expect(confirmRequest(decision, draft, 'confirm')).toEqual({ ok: false, reason: 'confirmation' });
    const plan = confirmRequest(decision, { ...draft, option: 'a', confirmation: '作者：用甲' }, 'confirm');
    expect(plan.ok && JSON.parse(plan.body)).toEqual({ kind: 'decision', unit: '', id: 'dec-1', action: 'confirm', option: 'a', answer: '', confirmation: '作者：用甲' });
  });

  it('requires an answer only to confirm an open question, and sends reject without one', () => {
    const question: PageConfirm = { ...decision, options: [], needsAnswer: true, canReject: true };
    const filled = { option: '', answer: '', confirmation: '作者原话' };
    expect(confirmRequest(question, filled, 'confirm')).toEqual({ ok: false, reason: 'answer' });
    const reject = confirmRequest(question, filled, 'reject');
    expect(reject.ok && JSON.parse(reject.body)).toMatchObject({ action: 'reject', answer: '', option: '' });
    const answered = confirmRequest(question, { ...filled, answer: '改成乙' }, 'confirm');
    expect(answered.ok && JSON.parse(answered.body).answer).toBe('改成乙');
  });

  it('sends every listed review key except the unticked ones', () => {
    const page = parseReviewPage({ units: [], strict: true, reviews: [{ key: 'a', label: '' }, { key: 'b', label: '' }] })!;
    expect(selectedReviewKeys(page, {})).toEqual(['a', 'b']);
    expect(selectedReviewKeys(page, { a: true })).toEqual(['b']);
    expect(authorReviewRequest([])).toEqual({ ok: false, reason: 'keys' });
    const plan = authorReviewRequest(['b']);
    expect(plan.ok && JSON.parse(plan.body)).toEqual({ keys: ['b'] });
  });
});

describe('pending markers', () => {
  it('indexes the real next fixture by work and unit (discovery-badge)', async () => {
    const next = await realNext();
    const index = pendingOf(next, []);
    expect(index!.byWork).toEqual({ 'my-novel': 2, 'my-post': 1 });
    expect(pendingCountOf(index, 'my-post')).toBe(1);
    expect(pendingCountOf(index, 'my-novel/scene-1')).toBe(1);
    expect(pendingCountOf(index, 'my-novel/scene-2')).toBe(1);
    expect(pendingCountOf(index, 'my-novel')).toBe(0);
  });

  it('marks author reviews on their unit, work-level ones on the root', () => {
    const index = pendingIndex([], [
      { kind: 'acceptance', work: 'my-post', ref: 'my-post/intro' },
      { kind: 'canon-decision', work: 'my-novel', ref: 'canon-3' },
      { kind: 'volume-checkpoint', work: 'my-novel', ref: 'my-novel/part-1' },
      { kind: 'junk' },
    ]);
    expect(index!.byUnit).toEqual({ 'my-post/intro': 1, 'my-novel': 1, 'my-novel/part-1': 1 });
    expect(index!.byWork).toEqual({ 'my-post': 1, 'my-novel': 2 });
    // A confirmation without units marks the work's root.
    expect(pendingIndex([{ work: 'w', units: [] }], [])!.byUnit).toEqual({ w: 1 });
  });

  it('shows no markers when the next area or a pending field is degraded (discovery-degraded)', async () => {
    const next = await realNext();
    expect(pendingOf(null, [])).toBeNull();
    expect(pendingOf(next, ['next: 缺少字段 pendingConfirmations（待审阅标记），该部分按降级标注呈现'])).toBeNull();
    expect(pendingOf(next, ['next: 缺少字段 pendingAuthorReviews（待复核摘要），该部分按降级标注呈现'])).toBeNull();
    // Notes about other areas leave the markers alone.
    expect(pendingOf(next, ['validate: 缺少字段 diagnostics'])).not.toBeNull();
    expect(pendingIndex(null, [])).toBeNull();
    expect(pendingCountOf(null, 'my-post')).toBe(0);
  });
});
