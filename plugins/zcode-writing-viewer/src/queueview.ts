/**
 * Pure view models for the two author action queues: the decision queue
 * (decisions.yaml) and the author-review records (author-reviews.yaml).
 *
 * The snapshot already carries both files as full text (see ./snapshot);
 * these parsers turn that text into display rows for the panel's queue
 * views. The same discipline as every other view module applies: tolerant
 * per-field extraction, never throw, no fetching, no React, no DOM. A
 * missing file (null text) is no data, an empty file is no data, and text
 * that fails to parse surfaces as a readable parseError the client shows
 * instead of an empty list pretending everything is decided.
 */
import { parse } from 'yaml';

/** One decision option: id plus text, with the recommendation flag. */
export interface DecisionOptionView {
  id: string;
  text: string;
  recommended: boolean;
}

/** One decision queue entry as the panel shows it. */
export interface DecisionView {
  id: string;
  status: string | null;
  question: string | null;
  options: DecisionOptionView[];
  recommendation: string | null;
  rationale: string | null;
  blocking: boolean;
  units: string[];
}

/** One author-review record as the panel shows it. */
export interface ReviewRecordView {
  kind: string | null;
  work: string | null;
  ref: string | null;
  channel: string | null;
  confirmedAt: string | null;
  reviewedAt: string | null;
}

/** Parse outcome: entries plus a readable error when the text would not parse. */
export interface QueueParse<T> {
  entries: T[];
  parseError: string | null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function asString(value: unknown): string | null {
  return typeof value === 'string' && value !== '' ? value : null;
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item !== '') : [];
}

/** Parse YAML text; a missing/empty file is no data, a failure is an error. */
function parseDoc(text: string | null | undefined): { doc: unknown | null; error: string | null; absent: boolean } {
  if (typeof text !== 'string') return { doc: null, error: null, absent: true };
  if (text.trim() === '') return { doc: null, error: null, absent: true };
  try {
    return { doc: parse(text), error: null, absent: false };
  } catch (cause) {
    return { doc: null, error: String((cause as Error)?.message ?? cause), absent: false };
  }
}

/** Parse decisions.yaml text into decision rows; never throws. */
export function decisionEntries(text: string | null | undefined): QueueParse<DecisionView> {
  const { doc, error, absent } = parseDoc(text);
  if (error !== null) return { entries: [], parseError: error };
  if (absent || !isRecord(doc)) return { entries: [], parseError: null };
  const raw = Array.isArray(doc.decisions) ? doc.decisions : [];
  const entries: DecisionView[] = [];
  for (const item of raw) {
    if (!isRecord(item)) continue;
    const id = asString(item.id);
    if (id === null) continue;
    const recommendation = asString(item.recommendation);
    const options: DecisionOptionView[] = Array.isArray(item.options)
      ? item.options.flatMap((option) => {
          if (!isRecord(option)) return [];
          const optionId = asString(option.id);
          if (optionId === null) return [];
          return [{ id: optionId, text: asString(option.text) ?? optionId, recommended: optionId === recommendation }];
        })
      : [];
    entries.push({
      id,
      status: asString(item.status),
      question: asString(item.question),
      options,
      recommendation,
      rationale: asString(item.rationale),
      blocking: item.blocking === true,
      units: asStringArray(item.units),
    });
  }
  return { entries, parseError: null };
}

/** Parse author-reviews.yaml text into review rows; never throws. */
export function reviewEntries(text: string | null | undefined): QueueParse<ReviewRecordView> {
  const { doc, error, absent } = parseDoc(text);
  if (error !== null) return { entries: [], parseError: error };
  if (absent || !isRecord(doc)) return { entries: [], parseError: null };
  const raw = Array.isArray(doc.reviews) ? doc.reviews : [];
  const entries: ReviewRecordView[] = [];
  for (const item of raw) {
    if (!isRecord(item)) continue;
    const view: ReviewRecordView = {
      kind: asString(item.kind),
      work: asString(item.work),
      ref: asString(item.ref),
      channel: asString(item.channel),
      confirmedAt: asString(item.confirmed_at),
      reviewedAt: asString(item.reviewed_at),
    };
    if (view.kind === null && view.ref === null && view.work === null) continue;
    entries.push(view);
  }
  return { entries, parseError: null };
}

/** Full text of decisions.yaml / author-reviews.yaml from a snapshot body. */
export function queueTextOf(body: { snapshot?: { decisions?: { text: string } | null; authorReviews?: { text: string } | null } | null }): {
  decisions: string | null;
  authorReviews: string | null;
} {
  const snapshot = body.snapshot;
  return {
    decisions: snapshot?.decisions?.text ?? null,
    authorReviews: snapshot?.authorReviews?.text ?? null,
  };
}
