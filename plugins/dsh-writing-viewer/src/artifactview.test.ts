import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { realSnapshotFs } from './host';
import { artifactTextOf, artifactView, cachedArtifactView, layoutFor, sourceEntriesOf, type CardRow } from './artifactview';
import { buildSnapshot, type SnapshotRouteBody } from './snapshot';
import type { CliRunner } from './wacli';

const TESTDATA = join(dirname(fileURLToPath(import.meta.url)), '..', 'testdata');
const FIXTURE_ROOT = join(TESTDATA, 'workspace');
const CLI_DIR = join(TESTDATA, 'cli');

const cannedRunner: CliRunner = {
  async run(args: readonly string[]): Promise<{ stdout: string } | null> {
    const command = args[0];
    if (typeof command !== 'string') return null;
    const file = command === 'timeline' ? `timeline-${args[1]}.json` : `${command}.json`;
    try {
      return { stdout: await readFile(join(CLI_DIR, file), 'utf8') };
    } catch {
      return null;
    }
  },
};

async function fixtureBody(): Promise<SnapshotRouteBody> {
  return buildSnapshot(realSnapshotFs(), cannedRunner, [{ source: 'workspace-registry', path: FIXTURE_ROOT, workspaceExists: true }], {
    root: FIXTURE_ROOT,
    source: 'workspace-registry',
  });
}

async function artifactText(name: string): Promise<string> {
  return readFile(join(FIXTURE_ROOT, '.writing-anything', 'works', 'my-post', `${name}.yaml`), 'utf8');
}

/** All rows of a view flattened one level deep (group children included). */
function flatRows(rows: CardRow[]): CardRow[] {
  const out: CardRow[] = [];
  for (const row of rows) {
    out.push(row);
    if (row.type === 'group') out.push(...row.rows);
  }
  return out;
}

describe('layoutFor', () => {
  it('maps the three named layouts and source, everything else generic', () => {
    expect(layoutFor('brief')).toBe('brief');
    expect(layoutFor('outline')).toBe('outline');
    expect(layoutFor('claims')).toBe('claims');
    expect(layoutFor('source')).toBe('source');
    expect(layoutFor('feedback')).toBe('generic');
    expect(layoutFor('threads')).toBe('generic');
  });
});

describe('artifactView brief layout', () => {
  it('organizes audience, purpose, premise and length into the base card', async () => {
    const view = artifactView('brief', await artifactText('brief'));
    expect(view.parseError).toBeNull();
    expect(view.cards[0]?.id).toBe('brief');
    const rows = flatRows(view.cards[0]?.rows ?? []);
    const text = (key: string): string | undefined => rows.find((row) => row.key === key && row.type === 'text')?.type === 'text'
      ? (rows.find((row) => row.key === key && row.type === 'text') as { text: string }).text
      : undefined;
    expect(text('audience')).toBe('维护大型单体服务的工程师');
    expect(text('purpose')).toBe('说服读者用契约测试守住重写边界');
    expect(text('length')).toBe('1500–2500');
    const premise = rows.find((row) => row.key === 'premise' && row.type === 'group');
    expect(premise).toBeDefined();
    expect(premise?.type === 'group' ? premise.rows.find((row) => row.type === 'sources') : null).toMatchObject({ ids: ['src-migration'] });
  });

  it('gives each key message its own card with clickable source refs', async () => {
    const view = artifactView('brief', await artifactText('brief'));
    const message = view.cards.find((card) => card.id === 'key-message-msg-boundary');
    expect(message?.title).toBe('重写项目的安全网是契约测试，不是加班');
    const sources = flatRows(message?.rows ?? []).find((row) => row.type === 'sources');
    expect(sources).toMatchObject({ ids: ['src-migration'] });
  });
});

describe('artifactView outline layout', () => {
  it('cards follow section order with covers as list and uses as sources', async () => {
    const view = artifactView('outline', await artifactText('outline'));
    expect(view.parseError).toBeNull();
    expect(view.cards.map((card) => card.id)).toEqual(['section-intro', 'section-contract']);
    expect(view.cards[0]?.title).toBe('引入');
    const rows = flatRows(view.cards[0]?.rows ?? []);
    expect(rows.find((row) => row.key === 'covers' && row.type === 'list')).toMatchObject({ items: ['msg-boundary'] });
    expect(rows.find((row) => row.key === 'uses' && row.type === 'sources')).toMatchObject({ ids: ['src-migration'] });
    expect(rows.find((row) => row.key === 'role' && row.type === 'text')).toMatchObject({ text: 'situation' });
  });

  it('keeps scene-contract extension fields of a novel section', async () => {
    const text = await readFile(join(FIXTURE_ROOT, '.writing-anything', 'works', 'my-novel', 'units', 'scene-1', 'outline.yaml'), 'utf8');
    const view = artifactView('outline', text);
    const scene = view.cards[0];
    expect(scene).toBeDefined();
    const rows = flatRows(scene?.rows ?? []);
    expect(rows.find((row) => row.key === 'characters' && row.type === 'list')).toMatchObject({ items: ['lin-che'] });
    expect(rows.find((row) => row.key === 'reveal' && row.type === 'text')).toMatchObject({ text: '日志的真正主人' });
    expect(rows.find((row) => row.key === 'location' && row.type === 'text')).toMatchObject({ text: 'lighthouse' });
    expect(rows.find((row) => row.key === 'plants' && row.type === 'list')).toMatchObject({ items: ['log-thread'] });
    expect(rows.find((row) => row.key === 'time' && row.type === 'text')).toMatchObject({ text: '第 1 夜' });
  });
});

describe('artifactView claims layout', () => {
  it('shows one card per claim with kind and clickable source refs', async () => {
    const view = artifactView('claims', await artifactText('claims'));
    expect(view.parseError).toBeNull();
    expect(view.cards).toHaveLength(1);
    const claim = view.cards[0]!;
    expect(claim.id).toBe('claim-claim-metrics');
    expect(claim.title).toContain('事故率下降了 40%');
    const rows = flatRows(claim.rows);
    expect(rows.find((row) => row.key === 'kind' && row.type === 'text')).toMatchObject({ text: 'data' });
    expect(rows.find((row) => row.key === 'sources' && row.type === 'sources')).toMatchObject({ ids: ['src-note'] });
  });
});

describe('artifactView source and generic layouts', () => {
  it('organizes source.yaml metadata into one card', async () => {
    const text = await readFile(join(FIXTURE_ROOT, '.writing-anything', 'sources', 'src-note', 'source.yaml'), 'utf8');
    const view = artifactView('source', text);
    expect(view.cards).toHaveLength(1);
    const rows = flatRows(view.cards[0]?.rows ?? []);
    expect(rows.find((row) => row.key === 'type' && row.type === 'text')).toMatchObject({ text: 'author-note' });
    expect(rows.find((row) => row.key === 'file' && row.type === 'text')).toMatchObject({ text: 'original.md' });
  });

  it('falls back to generic field cards for artifacts without a named layout', async () => {
    const view = artifactView('feedback', await artifactText('feedback'));
    expect(view.layout).toBe('generic');
    expect(view.parseError).toBeNull();
    expect(view.cards.length).toBeGreaterThan(0);
    // The feedback list becomes per-item cards; the base card keeps scalars.
    expect(view.cards.some((card) => card.id.startsWith('feedback-feedback-'))).toBe(true);
    const itemCard = view.cards.find((card) => card.id === 'feedback-feedback-0');
    const rows = flatRows(itemCard?.rows ?? []);
    expect(rows.find((row) => row.key === 'kind' && row.type === 'text')).toMatchObject({ text: 'must-fix' });
    expect(rows.find((row) => row.key === 'text' && row.type === 'text')).toMatchObject({ text: '开头再直接一点，别绕' });
  });

  it('reports a parse error and no cards for malformed YAML', () => {
    const view = artifactView('brief', 'audience: [unclosed\n  bad:\n- x: {');
    expect(view.parseError).toBeTruthy();
    expect(view.cards).toEqual([]);
  });

  it('treats an empty artifact as no data instead of a parse failure', () => {
    const view = artifactView('brief', '');
    expect(view.parseError).toBeNull();
    expect(view.cards).toEqual([]);
    const whitespace = artifactView('claims', '\n  \n');
    expect(whitespace.parseError).toBeNull();
    expect(whitespace.cards).toEqual([]);
  });

  it('degrades unsupported generic shapes to JSON text rows instead of dropping them', () => {
    const view = artifactView('metrics', 'schema_version: 1\ntags: [1, 2]\nmixed: ["a", {x: 1}]\nouter:\n  inner:\n    deep: yes\n');
    expect(view.parseError).toBeNull();
    const rows = flatRows(view.cards[0]?.rows ?? []);
    expect(rows.find((row) => row.key === 'tags' && row.type === 'text')).toMatchObject({ text: '[1,2]' });
    expect(rows.find((row) => row.key === 'mixed' && row.type === 'text')).toMatchObject({ text: '["a",{"x":1}]' });
    const outer = rows.find((row) => row.key === 'outer' && row.type === 'group');
    expect(outer).toBeDefined();
    expect(outer?.type === 'group' ? outer.rows.find((row) => row.key === 'inner' && row.type === 'text') : null)
      .toMatchObject({ text: '{"deep":"yes"}' });
  });

  it('shows brief.provenance and key-message decisions through the tail pass', async () => {
    const view = artifactView('brief', await artifactText('brief'));
    const rows = flatRows(view.cards[0]?.rows ?? []);
    const provenance = rows.find((row) => row.key === 'provenance' && row.type === 'group');
    expect(provenance).toBeDefined();
    expect(provenance?.type === 'group' ? JSON.stringify(provenance.rows) : '').toContain('src-migration');
    const decisionBrief = 'schema_version: 1\nkey_messages:\n  - id: m1\n    text: t\n    decision: dec-1\n';
    const withDecision = artifactView('brief', decisionBrief);
    const messageRows = flatRows(withDecision.cards.find((card) => card.id === 'key-message-m1')?.rows ?? []);
    expect(messageRows.find((row) => row.key === 'decision' && row.type === 'text')).toMatchObject({ text: 'dec-1' });
  });

  it('memoizes views per artifact object identity', () => {
    const artifact = { text: 'schema_version: 1\nclaims: []\n' };
    const first = cachedArtifactView('claims', artifact);
    const second = cachedArtifactView('claims', artifact);
    expect(second).toBe(first);
    const other = { text: 'schema_version: 1\nclaims: []\n' };
    expect(cachedArtifactView('claims', other)).not.toBe(first);
  });
});

describe('artifactTextOf and sourceEntriesOf', () => {
  it('finds unit artifacts and the draft by the detail-list name', async () => {
    const body = await fixtureBody();
    const brief = artifactTextOf(body, 'my-post', 'brief');
    expect(brief?.format).toBe('yaml');
    expect(brief?.text).toContain('维护大型单体服务的工程师');
    const draft = artifactTextOf(body, 'my-post', 'draft');
    expect(draft?.format).toBe('markdown');
    expect(draft?.text).toContain('契约测试');
    expect(artifactTextOf(body, 'my-post', 'missing')).toBeNull();
    expect(artifactTextOf(body, 'no-such-work', 'brief')).toBeNull();
  });

  it('lists the registered sources in snapshot order', async () => {
    const entries = sourceEntriesOf(await fixtureBody());
    expect(entries.map((entry) => entry.id)).toEqual(['src-migration', 'src-note']);
    expect(entries[1]?.original?.text).toBeTruthy();
  });
});
