import { describe, expect, it } from 'vitest';
import type { CliRunner } from './wacli';
import {
  collectNext,
  collectStatus,
  collectTimeline,
  collectValidate,
  mapNext,
  mapStatus,
  mapTimeline,
  mapValidate,
  novelWorksFromStatus,
  type CliOutcome,
} from './wacli';

const fullStatus = {
  workspace: '/w',
  config: { genre_defaults: {} },
  sources: 3,
  works: 2,
  units: [
    { unit: 'my-novel', work: 'my-novel', type: 'novel', parent: null, stage: 'brief', blockers: [], progress: null, commands: [] },
    { unit: 'my-novel/part-1', work: 'my-novel', type: 'part', parent: 'my-novel', stage: null, blockers: [{ code: 'b', message: 'm' }], progress: { done: 1, total: 2 }, commands: ['wa-cli next --json'] },
  ],
};

describe('wa-cli output mappers', () => {
  it('maps a complete status envelope without degradation', () => {
    const outcome = mapStatus(fullStatus);
    expect(outcome.reason).toBeNull();
    expect(outcome.output?.units).toHaveLength(2);
    expect(outcome.output?.units[1]?.progress).toEqual({ done: 1, total: 2 });
    expect(outcome.output?.units[1]?.blockers).toEqual([{ code: 'b', message: 'm' }]);
  });

  it('degrades status when required fields are missing and fails on non-objects', () => {
    const partial = mapStatus({ units: [{ unit: 'a', work: 'a' }] });
    expect(partial.output?.units).toHaveLength(1);
    expect(partial.reason).toContain('config');
    expect(partial.reason).toContain('sources');
    expect(partial.reason).toContain('works');
    const broken = mapStatus('nope');
    expect(broken.output).toBeNull();
    expect(broken.reason).toContain('status');
  });

  it('degrades validate when valid/diagnostics are missing', () => {
    const ok = mapValidate({ valid: true, diagnostics: [], checked: ['a'] });
    expect(ok.reason).toBeNull();
    expect(ok.output?.checked).toEqual(['a']);
    const partial = mapValidate({ checked: 'x' });
    expect(partial.reason).toContain('valid');
    expect(partial.reason).toContain('diagnostics');
    expect(partial.output?.checked).toEqual([]);
  });

  it('degrades next only for a missing pendingAuthorReviews or pendingConfirmations field', () => {
    expect(mapNext({ steps: [], pendingConfirmations: [] }).reason).toContain('pendingAuthorReviews');
    expect(mapNext({ steps: [], pendingConfirmations: [] }).reason).not.toContain('pendingConfirmations');
    const old = mapNext({ steps: [], pendingAuthorReviews: [] });
    expect(old.reason).toContain('pendingConfirmations');
    expect(old.reason).not.toContain('pendingAuthorReviews');
    expect(old.output?.pendingConfirmations).toEqual([]);
    const full = mapNext({ steps: [{ do: 'it' }], pendingAuthorReviews: [{ unit: 'u' }], pendingConfirmations: [{ kind: 'decision', work: 'w' }] });
    expect(full.reason).toBeNull();
    expect(full.output?.pendingConfirmations).toEqual([{ kind: 'decision', work: 'w' }]);
    expect(mapNext(null).output).toBeNull();
  });

  it('maps timeline rows and degrades entries without a unit', () => {
    const ok = mapTimeline({ timeline: [{ unit: 's1', time: 'dawn', after: ['e1', 5], position: 3 }] });
    expect(ok.reason).toBeNull();
    expect(ok.output?.timeline[0]).toEqual({ unit: 's1', time: 'dawn', after: ['e1'], position: 3 });
    const defaulted = mapTimeline({ timeline: [{ unit: 's2' }] });
    expect(defaulted.output?.timeline[0]?.position).toBe(0);
    const degraded = mapTimeline({ timeline: [{ time: 'night' }] });
    expect(degraded.output?.timeline).toHaveLength(0);
    expect(degraded.reason).toContain('unit');
  });

  it('lists novel root works from status, or null when status is degraded', () => {
    const mixed = mapStatus({
      ...fullStatus,
      units: [
        ...fullStatus.units,
        { unit: 'my-post', work: 'my-post', type: 'article', parent: null, stage: 'done', blockers: [], progress: null, commands: [] },
      ],
    });
    expect(novelWorksFromStatus(mixed)).toEqual(['my-novel']);
    const degraded: CliOutcome<never> = { output: null, reason: 'status: down' };
    expect(novelWorksFromStatus(degraded as unknown as CliOutcome<never>)).toBeNull();
  });
});

describe('wa-cli collect runners', () => {
  const calls: string[][] = [];
  const runner = (stdout: string | null): CliRunner => ({
    async run(args: readonly string[]) {
      calls.push([...args]);
      return stdout === null ? null : { stdout };
    },
  });

  it('runs read-only commands with --dir and --json and maps their output', async () => {
    const ok = await collectStatus(runner(JSON.stringify(fullStatus)), '/w');
    expect(ok.reason).toBeNull();
    expect(calls[0]).toEqual(['status', '--dir', '/w', '--json']);
    const timeline = await collectTimeline(runner(JSON.stringify({ timeline: [] })), '/w', 'my-novel');
    expect(timeline.reason).toBeNull();
    expect(calls[1]).toEqual(['timeline', 'my-novel', '--dir', '/w', '--json']);
    const next = await collectNext(runner(JSON.stringify({ pendingAuthorReviews: [], pendingConfirmations: [] })), '/w');
    expect(next.reason).toBeNull();
    const validate = await collectValidate(runner(JSON.stringify({ valid: true, diagnostics: [] })), '/w');
    expect(validate.reason).toBeNull();
  });

  it('degrades per area when the binary is unavailable or the output is unparseable', async () => {
    const unavailable = await collectStatus(runner(null), '/w');
    expect(unavailable.output).toBeNull();
    expect(unavailable.reason).toContain('status');
    expect(unavailable.reason).toContain('降级');
    const unparseable = await collectValidate(runner('not json at all'), '/w');
    expect(unparseable.output).toBeNull();
    expect(unparseable.reason).toContain('无法解析');
  });
});
