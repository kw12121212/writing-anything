/**
 * Pure view model for the native review view (审阅视图, Review View) and the
 * panel's「待审阅」markers.
 *
 * The review view renders wa-cli's reading view (阅读页, Reading View) data —
 * GET /page.json through the same-origin proxy — with the panel's own
 * components instead of an iframe. Everything the view needs is derived here,
 * pure of React, the DOM and fetch (dsh-plugin-structure/client-view-model-
 * module): parsing the page data, the request bodies the three submit
 * endpoints take (the same bodies wa-cli's own page script sends), the
 * single-paragraph selection rule for quoted feedback, and which works and
 * units carry pending author confirmations according to `wa-cli next`.
 * Workspace text stays plain strings; the client renders it as text nodes.
 */

/** Same-origin proxy paths of the reading view service. */
export const REVIEW_DATA_ROUTE = '/dsh-writing-viewer/review/page.json';
export const REVIEW_FEEDBACK_ROUTE = '/dsh-writing-viewer/review/feedback';
export const REVIEW_CONFIRM_ROUTE = '/dsh-writing-viewer/review/confirm';
export const REVIEW_AUTHOR_REVIEW_ROUTE = '/dsh-writing-viewer/review/author-review';
export const REVIEW_ILLUSTRATION_ROUTE = '/dsh-writing-viewer/review/illustration';

export interface PageFinding {
  kind: 'lint' | 'review';
  label: string;
  severity: string;
  message: string;
  suggestion: string;
}

export interface PageParagraph {
  index: number;
  line: number;
  text: string;
  /** human, agent or agent-draft-human-edited; null when it cannot be derived. */
  origin: string | null;
  findings: PageFinding[];
  /** Ids of the illustrations this paragraph references, in order. */
  illustrations: string[];
}

/** One registered illustration (配图（Illustration）) or cover image
 * (封面图（Cover Image）) of a unit, as page.json lists it. */
export interface PageIllustration {
  id: string;
  /** body or cover. */
  role: string;
  alt: string;
  title: string | null;
  /** mood (氛围图) or infographic (信息图). */
  content: string;
  /** agent or author. */
  origin: string;
  width: number;
  height: number;
  claims: string[];
  sources: string[];
  referenced: boolean;
  /** The image file exists and matches its registration; only then is it loaded. */
  available: boolean;
}

export interface PageUnit {
  ref: string;
  stage: string;
  hasDraft: boolean;
  notes: string[];
  global: PageFinding[];
  paragraphs: PageParagraph[];
  illustrations: PageIllustration[];
}

export interface PageOption {
  id: string;
  text: string;
}

export interface PageConfirm {
  kind: string;
  unit: string;
  id: string;
  label: string;
  detail: string;
  options: PageOption[];
  recommend: string;
  needsAnswer: boolean;
  canReject: boolean;
}

export interface PageReview {
  key: string;
  label: string;
}

export interface PageFailure {
  at: string;
  command: string;
  exit: number;
}

/** The reading view data of one review session. */
export interface ReviewPage {
  title: string;
  units: PageUnit[];
  confirms: PageConfirm[];
  strict: boolean;
  reviews: PageReview[];
  failures: PageFailure[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function list(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function finding(value: unknown): PageFinding | null {
  if (!isRecord(value)) return null;
  return {
    kind: value.kind === 'review' ? 'review' : 'lint',
    label: str(value.label),
    severity: str(value.severity),
    message: str(value.message),
    suggestion: str(value.suggestion),
  };
}

function findings(value: unknown): PageFinding[] {
  return list(value).map(finding).filter((f): f is PageFinding => f !== null);
}

function strings(value: unknown): string[] {
  return list(value).filter((v): v is string => typeof v === 'string' && v !== '');
}

function illustration(value: unknown): PageIllustration | null {
  if (!isRecord(value) || typeof value.id !== 'string' || value.id === '') return null;
  return {
    id: value.id,
    role: value.role === 'cover' ? 'cover' : 'body',
    alt: str(value.alt),
    title: typeof value.title === 'string' ? value.title : null,
    content: value.content === 'infographic' ? 'infographic' : 'mood',
    origin: str(value.origin),
    width: typeof value.width === 'number' ? value.width : 0,
    height: typeof value.height === 'number' ? value.height : 0,
    claims: strings(value.claims),
    sources: strings(value.sources),
    referenced: value.referenced === true,
    available: value.available === true,
  };
}

/**
 * Parse a page.json body. Returns null when it is not reading view data at
 * all (no units array); malformed entries inside are skipped rather than
 * failing the whole view.
 */
export function parseReviewPage(body: unknown): ReviewPage | null {
  if (!isRecord(body) || !Array.isArray(body.units)) return null;
  const units: PageUnit[] = [];
  for (const raw of body.units) {
    if (!isRecord(raw) || typeof raw.ref !== 'string' || raw.ref === '') continue;
    const paragraphs: PageParagraph[] = [];
    for (const p of list(raw.paragraphs)) {
      if (!isRecord(p) || typeof p.text !== 'string') continue;
      paragraphs.push({
        index: typeof p.index === 'number' ? p.index : paragraphs.length + 1,
        line: typeof p.line === 'number' ? p.line : 0,
        text: p.text,
        origin: typeof p.origin === 'string' && p.origin !== '' ? p.origin : null,
        findings: findings(p.findings),
        illustrations: strings(p.illustrations),
      });
    }
    units.push({
      ref: raw.ref,
      stage: str(raw.stage),
      hasDraft: raw.hasDraft === true,
      notes: list(raw.notes).filter((n): n is string => typeof n === 'string'),
      global: findings(raw.global),
      paragraphs,
      illustrations: list(raw.illustrations).map(illustration).filter((i): i is PageIllustration => i !== null),
    });
  }
  const confirms: PageConfirm[] = [];
  for (const c of list(body.confirms)) {
    if (!isRecord(c) || typeof c.kind !== 'string') continue;
    confirms.push({
      kind: c.kind,
      unit: str(c.unit),
      id: str(c.id),
      label: str(c.label),
      detail: str(c.detail),
      options: list(c.options)
        .filter(isRecord)
        .map((o) => ({ id: str(o.id), text: str(o.text) }))
        .filter((o) => o.id !== ''),
      recommend: str(c.recommend),
      needsAnswer: c.needsAnswer === true,
      canReject: c.canReject === true,
    });
  }
  const strict = body.strict === true;
  return {
    title: str(body.title),
    units,
    confirms,
    strict,
    reviews: strict
      ? list(body.reviews).filter(isRecord).map((r) => ({ key: str(r.key), label: str(r.label) })).filter((r) => r.key !== '')
      : [],
    failures: list(body.failures)
      .filter(isRecord)
      .map((f) => ({ at: str(f.at), command: str(f.command), exit: typeof f.exit === 'number' ? f.exit : 0 })),
  };
}

/**
 * The image address of one illustration: always the fixed same-origin proxy
 * route plus the URL-encoded unit and id. Addresses in the draft or in
 * page.json are never used.
 */
export function illustrationSrc(unit: string, id: string): string {
  return `${REVIEW_ILLUSTRATION_ROUTE}?unit=${encodeURIComponent(unit)}&id=${encodeURIComponent(id)}`;
}

/** Where a unit's illustrations show in the review view. */
export interface IllustrationLayout {
  /** Cover images, shown at the head of the unit (only a work's root unit has one). */
  covers: PageIllustration[];
  /** Body illustrations after each paragraph, keyed by paragraph index, in the paragraph's reference order. */
  byParagraph: Record<number, PageIllustration[]>;
  /** Body illustrations the draft does not reference, listed at the end of the unit. */
  unreferenced: PageIllustration[];
}

/** Lay out a unit's illustrations: covers first, body images after the
 * paragraphs that reference them, unreferenced body images last. Ids a
 * paragraph references but the unit does not list are skipped (wa-cli
 * reports them in the unit's notes). */
export function illustrationLayout(unit: PageUnit): IllustrationLayout {
  const byId = new Map(unit.illustrations.map((i) => [i.id, i] as const));
  const byParagraph: Record<number, PageIllustration[]> = {};
  for (const p of unit.paragraphs) {
    const items = p.illustrations.map((id) => byId.get(id)).filter((i): i is PageIllustration => i !== undefined && i.role === 'body');
    if (items.length > 0) byParagraph[p.index] = items;
  }
  return {
    covers: unit.illustrations.filter((i) => i.role === 'cover'),
    byParagraph,
    unreferenced: unit.illustrations.filter((i) => i.role === 'body' && !i.referenced),
  };
}

/** Readable text of a wa-cli (or proxy) refusal envelope. */
export function envelopeMessage(status: number, body: unknown): string {
  const diagnostics = isRecord(body) ? list(body.diagnostics) : [];
  const messages = diagnostics.filter(isRecord).map((d) => str(d.message)).filter((m) => m !== '');
  if (messages.length > 0) return messages.join('；');
  if (isRecord(body) && typeof body.error === 'string' && body.error !== '') return body.error;
  return `HTTP ${status}`;
}

/** Whether a proxy answer says the session is gone (no session, or the
 * upstream service could not be reached). */
export function sessionGone(status: number, body: unknown): boolean {
  if (status !== 409 && status !== 502) return false;
  const codes = (isRecord(body) ? list(body.diagnostics) : []).filter(isRecord).map((d) => d.code);
  return codes.includes('review.no-session') || codes.includes('review.upstream-unavailable');
}

/** Where a selection endpoint sits: the paragraph it is inside, if any. */
export interface ParagraphAnchor {
  unit: string;
  index: number;
}

/** What the author's current selection means for quoted feedback. */
export type SelectionOutcome =
  | { kind: 'none' }
  | { kind: 'quote'; unit: string; quote: string }
  | { kind: 'invalid' };

/**
 * Apply the single-paragraph rule: a quote must start and end inside the
 * same paragraph of the review view. An empty selection means nothing; a
 * selection that spans paragraphs or leaves the text is invalid.
 */
export function selectionOutcome(anchor: ParagraphAnchor | null, focus: ParagraphAnchor | null, text: string): SelectionOutcome {
  if (text.trim() === '') return { kind: 'none' };
  if (anchor === null || focus === null || anchor.unit !== focus.unit || anchor.index !== focus.index) return { kind: 'invalid' };
  return { kind: 'quote', unit: anchor.unit, quote: text };
}

/** The feedback composer's state. */
export interface FeedbackDraft {
  unit: string;
  /** Selected text; empty means the feedback is about the whole unit. */
  quote: string;
  text: string;
  kind: string;
  /** The last selection broke the single-paragraph rule. */
  invalidSelection: boolean;
}

export const FEEDBACK_KINDS = ['suggestion', 'must-fix', 'question', 'praise'] as const;
export const QUICK_MARKS = ['stuck', 'good', 'question'] as const;

export function initialFeedback(unit: string): FeedbackDraft {
  return { unit, quote: '', text: '', kind: 'suggestion', invalidSelection: false };
}

/** Fold a selection outcome into the composer. */
export function feedbackAfterSelection(draft: FeedbackDraft, outcome: SelectionOutcome): FeedbackDraft {
  if (outcome.kind === 'none') return draft;
  if (outcome.kind === 'invalid') return { ...draft, invalidSelection: true };
  return { ...draft, unit: outcome.unit, quote: outcome.quote, invalidSelection: false };
}

/** A submit that may be sent, or the reason it is held back. */
export type SubmitPlan = { ok: true; body: string } | { ok: false; reason: 'selection' | 'empty' | 'confirmation' | 'answer' | 'keys' };

/** POST /feedback body; mark is '' for plain feedback. */
export function feedbackRequest(draft: FeedbackDraft, mark: string): SubmitPlan {
  if (draft.invalidSelection) return { ok: false, reason: 'selection' };
  if (mark === '' && draft.text.trim() === '') return { ok: false, reason: 'empty' };
  return { ok: true, body: JSON.stringify({ unit: draft.unit, quote: draft.quote, text: draft.text, kind: draft.kind, mark }) };
}

/** One confirm item's inputs. */
export interface ConfirmDraft {
  option: string;
  answer: string;
  confirmation: string;
}

/** Stable key of a confirm item within one page. */
export function confirmKey(item: PageConfirm): string {
  return item.kind + ':' + item.unit + ':' + item.id;
}

/** The inputs an item starts with: the recommended option preselected, as
 * on wa-cli's own page. */
export function initialConfirmDraft(item: PageConfirm): ConfirmDraft {
  const recommended = item.options.some((o) => o.id === item.recommend) ? item.recommend : (item.options[0]?.id ?? '');
  return { option: recommended, answer: '', confirmation: '' };
}

/** POST /confirm body for one item and action. */
export function confirmRequest(item: PageConfirm, draft: ConfirmDraft, action: 'confirm' | 'reject'): SubmitPlan {
  if (draft.confirmation.trim() === '') return { ok: false, reason: 'confirmation' };
  if (action === 'confirm' && item.needsAnswer && draft.answer.trim() === '') return { ok: false, reason: 'answer' };
  return {
    ok: true,
    body: JSON.stringify({
      kind: item.kind,
      unit: item.unit,
      id: item.id,
      action,
      option: item.options.length > 0 ? draft.option : '',
      answer: item.needsAnswer ? draft.answer : '',
      confirmation: draft.confirmation,
    }),
  };
}

/** The keys the「复核所选」button sends: every listed item checked by
 * default, minus the ones the author unticked. */
export function selectedReviewKeys(page: ReviewPage, unticked: Record<string, boolean>): string[] {
  return page.reviews.map((r) => r.key).filter((key) => unticked[key] !== true);
}

/** POST /author-review body. */
export function authorReviewRequest(keys: string[]): SubmitPlan {
  return keys.length === 0 ? { ok: false, reason: 'keys' } : { ok: true, body: JSON.stringify({ keys }) };
}

/** One item the panel marks as「待审阅」. */
export interface PendingItem {
  work: string;
  /** Unit refs the item belongs to; the work's root unit for work-level items. */
  units: string[];
  label: string;
}

/** Pending author confirmations and author reviews by work and by unit. */
export interface PendingIndex {
  byWork: Record<string, number>;
  byUnit: Record<string, number>;
}

/**
 * Index the `wa-cli next` pending items. `confirmations` and `reviews` are
 * the raw pendingConfirmations and pendingAuthorReviews arrays; null means
 * the area is degraded, which yields no markers at all.
 */
export function pendingIndex(confirmations: unknown[] | null, reviews: unknown[] | null): PendingIndex | null {
  if (confirmations === null || reviews === null) return null;
  const items: PendingItem[] = [];
  for (const raw of confirmations) {
    if (!isRecord(raw) || typeof raw.work !== 'string' || raw.work === '') continue;
    const units = list(raw.units).filter((u): u is string => typeof u === 'string' && u !== '');
    items.push({ work: raw.work, units: units.length > 0 ? units : [raw.work], label: str(raw.label) });
  }
  for (const raw of reviews) {
    if (!isRecord(raw) || typeof raw.work !== 'string' || raw.work === '') continue;
    // An author review's ref is a unit for acceptances and checkpoints and
    // an entry id for canon decisions; only unit-shaped refs of this work
    // mark a unit, anything else marks the work's root.
    const ref = str(raw.ref);
    const unit = ref === raw.work || ref.startsWith(raw.work + '/') ? ref : raw.work;
    items.push({ work: raw.work, units: [unit], label: str(raw.ref) });
  }
  const byWork: Record<string, number> = {};
  const byUnit: Record<string, number> = {};
  for (const item of items) {
    byWork[item.work] = (byWork[item.work] ?? 0) + 1;
    for (const unit of item.units) byUnit[unit] = (byUnit[unit] ?? 0) + 1;
  }
  return { byWork, byUnit };
}

/** The pending count of one tree node, or 0 for units the tree does not hold
 * (their items count on the work card only). */
export function pendingCountOf(index: PendingIndex | null, ref: string): number {
  return index?.byUnit[ref] ?? 0;
}

/** The pending index of a snapshot: from its `next` area, or null (no
 * markers) when that area or either pending field is degraded. */
export function pendingOf(
  next: { pendingConfirmations?: unknown[]; pendingAuthorReviews?: unknown[] } | null | undefined,
  degraded: readonly string[],
): PendingIndex | null {
  if (next === null || next === undefined) return null;
  if (degraded.some((note) => note.startsWith('next:') && (note.includes('pendingConfirmations') || note.includes('pendingAuthorReviews')))) {
    return null;
  }
  return pendingIndex(next.pendingConfirmations ?? null, next.pendingAuthorReviews ?? null);
}
