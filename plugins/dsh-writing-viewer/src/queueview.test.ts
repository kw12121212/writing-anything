import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { decisionEntries, queueTextOf, reviewEntries } from './queueview';
import type { SnapshotRouteBody } from './snapshot';

const TESTDATA = new URL('../testdata/workspace/.writing-anything/', import.meta.url);

describe('decisionEntries', () => {
  it('parses the committed fixture with every display field', () => {
    const text = readFileSync(new URL('decisions.yaml', TESTDATA), 'utf8');
    const { entries, parseError } = decisionEntries(text);
    expect(parseError).toBeNull();
    expect(entries).toHaveLength(1);
    const decision = entries[0]!;
    expect(decision.id).toBe('dec-1');
    expect(decision.status).toBe('open');
    expect(decision.question).toContain('事故率下降了 40%');
    expect(decision.options.map((option) => option.id)).toEqual(['a', 'b']);
    expect(decision.options[0]!.text).toBe('允许');
    expect(decision.recommendation).toBe('b');
    expect(decision.options[0]!.recommended).toBe(false);
    expect(decision.options[1]!.recommended).toBe(true);
    expect(decision.rationale).toContain('亲历素材');
    expect(decision.blocking).toBe(true);
    expect(decision.units).toEqual(['my-post']);
  });

  it('treats null text, empty text, and missing decisions key as no data', () => {
    expect(decisionEntries(null)).toEqual({ entries: [], parseError: null });
    expect(decisionEntries(undefined)).toEqual({ entries: [], parseError: null });
    expect(decisionEntries('   \n')).toEqual({ entries: [], parseError: null });
    expect(decisionEntries('schema_version: 1\n')).toEqual({ entries: [], parseError: null });
  });

  it('reports malformed YAML as a readable parse error instead of throwing', () => {
    const { entries, parseError } = decisionEntries('decisions: [');
    expect(entries).toEqual([]);
    expect(typeof parseError).toBe('string');
    expect(parseError!.length).toBeGreaterThan(0);
  });

  it('skips entries without an id and tolerates partial fields', () => {
    const { entries } = decisionEntries([
      'schema_version: 1',
      'decisions:',
      '  - question: 没有 id 的条目',
      '  - id: dec-x',
    ].join('\n'));
    expect(entries).toHaveLength(1);
    expect(entries[0]!.id).toBe('dec-x');
    expect(entries[0]!.question).toBeNull();
    expect(entries[0]!.options).toEqual([]);
    expect(entries[0]!.blocking).toBe(false);
  });
});

describe('reviewEntries', () => {
  it('parses the committed fixture records with every field', () => {
    const text = readFileSync(new URL('author-reviews.yaml', TESTDATA), 'utf8');
    const { entries, parseError } = reviewEntries(text);
    expect(parseError).toBeNull();
    expect(entries.length).toBeGreaterThan(0);
    const review = entries[0]!;
    expect(review.kind).toBe('acceptance');
    expect(review.work).toBe('my-post');
    expect(review.ref).toBe('my-post');
    expect(review.channel).toBe('preview');
    expect(review.confirmedAt).toContain('2026-10-19');
    expect(review.reviewedAt).toContain('2026-10-19');
  });

  it('treats missing text as no data and malformed text as a parse error', () => {
    expect(reviewEntries(null)).toEqual({ entries: [], parseError: null });
    const broken = reviewEntries('reviews: {');
    expect(broken.entries).toEqual([]);
    expect(broken.parseError!.length).toBeGreaterThan(0);
  });

  it('keeps rows only when they identify something', () => {
    const { entries } = reviewEntries(['schema_version: 1', 'reviews:', '  - reviewed_at: "2026-10-19"'].join('\n'));
    // A row with no kind/work/ref carries nothing actionable — dropped.
    expect(entries).toEqual([]);
  });
});

describe('queueTextOf', () => {
  it('reads the two full texts out of a snapshot body, null when absent', () => {
    const body = {
      snapshot: {
        decisions: { path: 'decisions.yaml', format: 'yaml', text: 'schema_version: 1\n' },
        authorReviews: null,
      },
    } as unknown as SnapshotRouteBody;
    expect(queueTextOf(body)).toEqual({ decisions: 'schema_version: 1\n', authorReviews: null });
    expect(queueTextOf({ snapshot: null } as unknown as SnapshotRouteBody)).toEqual({ decisions: null, authorReviews: null });
  });
});
