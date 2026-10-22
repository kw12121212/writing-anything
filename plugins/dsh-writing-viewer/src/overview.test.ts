import { mkdir, mkdtemp, readFile, rm, writeFile, cp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';
import { realSnapshotFs } from './host';
import { NEW_WORK_COMMAND, overviewModel, pendingDecisionCount, unitDetail, unitTree } from './overview';
import { buildSnapshot, type SnapshotRouteBody } from './snapshot';
import type { CliRunner } from './wacli';

const TESTDATA = join(dirname(fileURLToPath(import.meta.url)), '..', 'testdata');
const FIXTURE_ROOT = join(TESTDATA, 'workspace');
const CLI_DIR = join(TESTDATA, 'cli');

/** A fake CLI runner answering from the canned real wa-cli captures. */
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

/** A runner modelling a host without wa-cli on PATH. */
const unavailableRunner: CliRunner = { run: async () => null };

const tempRoots: string[] = [];

async function tempFixtureCopy(): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), 'wa-overview-'));
  tempRoots.push(root);
  await cp(FIXTURE_ROOT, root, { recursive: true });
  return root;
}

afterEach(async () => {
  await Promise.all(tempRoots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

async function snapshotOf(root: string, runner: CliRunner = cannedRunner): Promise<SnapshotRouteBody> {
  return buildSnapshot(realSnapshotFs(), runner, [{ source: 'workspace-registry', path: root, workspaceExists: true }], {
    root,
    source: 'workspace-registry',
  });
}

describe('overviewModel on the multi-work fixture', () => {
  it('builds one card per work with genre, stage, progress and distinct blockers', async () => {
    const model = overviewModel(await snapshotOf(FIXTURE_ROOT));
    expect(model.state).toBe('ready');
    expect(model.source).toBe('workspace-registry');
    expect(model.works.map((w) => w.id)).toEqual(['my-novel', 'my-post']);

    const novel = model.works[0]!;
    expect(novel.genre).toBe('novel');
    expect(novel.type).toBe('novel');
    expect(novel.stage).toBe('brief');
    // The root is not aggregated; part-1 is the outermost aggregated unit.
    expect(novel.progress).toEqual({ done: 0, total: 2 });
    expect(novel.unitCount).toBe(5);
    // 4 root blockers + the 2 scene blockers; part-1 and ch-1 only repeat them.
    expect(novel.blockerCount).toBe(6);
    expect(novel.blockerSummary).toHaveLength(2);
    expect(novel.blockerSummary[0]).toContain('key_messages');

    const post = model.works[1]!;
    expect(post.genre).toBe('tech-blog');
    expect(post.type).toBe('article');
    expect(post.progress).toBeNull();
    expect(post.unitCount).toBe(1);
    expect(post.blockerCount).toBe(3);
  });

  it('counts sources, pending decisions, pending reviews and diagnostics', async () => {
    const model = overviewModel(await snapshotOf(FIXTURE_ROOT));
    expect(model.summary).toEqual({
      sources: 2,
      pendingDecisions: 1,
      pendingReviews: 0,
      diagnostics: 5,
      diagnosticsBySeverity: { error: 5 },
    });
    expect(model.diagnostics[0]).toMatchObject({ code: 'outline.missing-reason', severity: 'error' });
    expect(model.diagnostics[0]!.path).toContain('scene-1/outline.yaml');
    expect(model.degraded).toEqual([]);
    expect(model.rootError).toBeNull();
  });
});

describe('unitTree', () => {
  it('indents the novel hierarchy from status parent links with progress on aggregated units', async () => {
    const tree = unitTree(await snapshotOf(FIXTURE_ROOT), 'my-novel');
    expect(tree.map((n) => [n.label, n.depth, n.aggregated])).toEqual([
      ['my-novel', 0, false],
      ['part-1', 1, true],
      ['ch-1', 2, true],
      ['scene-1', 3, false],
      ['scene-2', 3, false],
    ]);
    expect(tree[1]!.progress).toEqual({ done: 0, total: 2 });
    expect(tree[1]!.ref).toBe('my-novel/part-1');
    expect(tree[3]!.blockerCount).toBe(1);
    expect(tree[3]!.stage).toBe('brief');
  });

  it('falls back to the work.yaml hierarchy when status is unavailable', async () => {
    const body = await snapshotOf(FIXTURE_ROOT, unavailableRunner);
    const tree = unitTree(body, 'my-novel');
    expect(tree.map((n) => [n.ref, n.depth])).toEqual([
      ['my-novel', 0],
      ['my-novel/part-1', 1],
      ['my-novel/ch-1', 2],
      ['my-novel/scene-1', 3],
      ['my-novel/scene-2', 3],
    ]);
    // Aggregated stages in work.yaml are placeholders and are not shown.
    expect(tree[1]!.aggregated).toBe(true);
    expect(tree[1]!.stage).toBeNull();
    expect(tree[3]!.stage).toBe('brief');
    expect(tree[0]!.stage).toBe('brief');
  });

  it('puts rows whose parent is unknown at the top level instead of hiding them', async () => {
    const body = await snapshotOf(FIXTURE_ROOT);
    const status = body.snapshot!.cli.status!;
    status.units = status.units.filter((row) => row.unit !== 'my-novel/ch-1');
    const tree = unitTree(body, 'my-novel');
    expect(tree.map((n) => n.ref)).toContain('my-novel/scene-1');
    expect(tree.find((n) => n.ref === 'my-novel/scene-1')!.depth).toBe(0);
  });

  it('keeps rows caught in a parent cycle instead of dropping them', async () => {
    const body = await snapshotOf(FIXTURE_ROOT);
    const status = body.snapshot!.cli.status!;
    const a = status.units.find((row) => row.unit === 'my-novel/part-1')!;
    const b = status.units.find((row) => row.unit === 'my-novel/ch-1')!;
    a.parent = 'my-novel/ch-1';
    b.parent = 'my-novel/part-1';
    const refs = unitTree(body, 'my-novel').map((n) => n.ref);
    expect(refs).toHaveLength(5);
    expect(refs).toEqual(expect.arrayContaining(['my-novel/part-1', 'my-novel/ch-1', 'my-novel/scene-1']));
  });

  it('is empty for an unknown work', async () => {
    expect(unitTree(await snapshotOf(FIXTURE_ROOT), 'nope')).toEqual([]);
  });
});

describe('unitDetail', () => {
  it('shows a directory unit with its artifacts and next commands', async () => {
    const detail = unitDetail(await snapshotOf(FIXTURE_ROOT), 'my-post');
    expect(detail?.type).toBe('article');
    expect(detail?.stage).toBe('brief');
    expect(detail?.blockers.map((b) => b.code)).toContain('decision.blocking');
    expect(detail?.commands[0]).toContain('wa-cli brief show my-post');
    const names = detail?.artifacts.map((a) => a.name) ?? [];
    expect(names).toEqual(expect.arrayContaining(['brief', 'outline', 'claims', 'provenance', 'feedback', 'draft']));
    expect(detail?.artifacts.find((a) => a.name === 'draft')).toEqual({ name: 'draft', path: 'works/my-post/draft.md', format: 'markdown' });
  });

  it('shows a structural aggregated unit without a directory', async () => {
    const detail = unitDetail(await snapshotOf(FIXTURE_ROOT), 'my-novel/ch-1');
    expect(detail?.type).toBe('chapter');
    expect(detail?.progress).toEqual({ done: 0, total: 2 });
    expect(detail?.blockers).toHaveLength(2);
    expect(detail?.artifacts).toEqual([]);
  });

  it('returns null for unknown refs and for a body without a snapshot', async () => {
    const body = await snapshotOf(FIXTURE_ROOT);
    expect(unitDetail(body, 'my-novel/nope')).toBeNull();
    expect(unitDetail(body, 'nope')).toBeNull();
    expect(unitDetail({ ...body, snapshot: null }, 'my-post')).toBeNull();
  });
});

describe('overviewModel counts against non-trivial inputs', () => {
  it('counts pending author reviews and groups diagnostics by severity', async () => {
    const body = await snapshotOf(FIXTURE_ROOT);
    const cli = body.snapshot!.cli;
    cli.next = { steps: [], pendingAuthorReviews: [{ kind: 'acceptance', unit: 'my-post' }, { kind: 'canon', unit: 'my-novel' }], pendingConfirmations: [] };
    cli.validate = {
      valid: false,
      checked: [],
      diagnostics: [...cli.validate!.diagnostics, { code: 'lint.long-sentence', severity: 'warning', message: 'w', path: 'works/my-post/draft.md' }],
    };
    const model = overviewModel(body);
    expect(model.summary?.pendingReviews).toBe(2);
    expect(model.summary?.diagnostics).toBe(6);
    expect(model.summary?.diagnosticsBySeverity).toEqual({ error: 5, warning: 1 });
  });

  it('shows a re-read workspace change after refresh', async () => {
    // Without wa-cli the stage comes from work.yaml; editing it and building
    // the snapshot again (what the refresh button does) shows the new stage.
    const root = await tempFixtureCopy();
    const before = unitTree(await snapshotOf(root, unavailableRunner), 'my-novel');
    expect(before.find((n) => n.ref === 'my-novel/scene-1')!.stage).toBe('brief');
    const workYaml = join(root, '.writing-anything/works/my-novel/work.yaml');
    const text = await readFile(workYaml, 'utf8');
    await writeFile(workYaml, text.replace(/(- id: scene-1\n\s+type: scene\n\s+stage: )brief/, '$1outline'));
    const after = unitTree(await snapshotOf(root, unavailableRunner), 'my-novel');
    expect(after.find((n) => n.ref === 'my-novel/scene-1')!.stage).toBe('outline');
  });

  it('reads a partially degraded area as unavailable rather than zero', async () => {
    const body = await snapshotOf(FIXTURE_ROOT);
    body.degraded = [
      'next: 缺少字段 pendingAuthorReviews，待复核摘要按降级标注呈现',
      'validate: 缺少字段 diagnostics，该部分按降级标注呈现',
    ];
    const model = overviewModel(body);
    expect(model.summary?.pendingReviews).toBeNull();
    expect(model.summary?.diagnostics).toBeNull();
    // A next note about the pending confirmations only leaves the review count alone.
    body.degraded = ['next: 缺少字段 pendingConfirmations（待审阅标记），该部分按降级标注呈现'];
    expect(overviewModel(body).summary?.pendingReviews).toBe(0);
    // A validate note about another field leaves the diagnostics count alone.
    body.degraded = ['validate: 缺少字段 valid，该部分按降级标注呈现'];
    expect(overviewModel(body).summary?.diagnostics).toBe(5);
  });
});

describe('overviewModel degraded, empty and missing states', () => {
  it('marks unavailable areas as null and keeps file-derived data when wa-cli is missing', async () => {
    const model = overviewModel(await snapshotOf(FIXTURE_ROOT, unavailableRunner));
    expect(model.state).toBe('ready');
    expect(model.degraded).toHaveLength(3);
    expect(model.summary?.sources).toBe(2);
    expect(model.summary?.pendingDecisions).toBe(1);
    expect(model.summary?.pendingReviews).toBeNull();
    expect(model.summary?.diagnostics).toBeNull();
    expect(model.diagnostics).toEqual([]);
    const novel = model.works[0]!;
    expect(novel.genre).toBe('novel');
    expect(novel.stage).toBe('brief');
    expect(novel.unitCount).toBe(5);
    expect(novel.blockerCount).toBe(0);
  });

  it('falls back to directory units when both status and work.yaml are unusable', async () => {
    const root = await tempFixtureCopy();
    await writeFile(join(root, '.writing-anything/works/my-novel/work.yaml'), 'id: [unclosed\n');
    const body = await snapshotOf(root, unavailableRunner);
    const tree = unitTree(body, 'my-novel');
    expect(tree.map((n) => [n.label, n.depth])).toEqual([
      ['my-novel', 0],
      ['scene-1', 1],
      ['scene-2', 1],
    ]);
    const card = overviewModel(body).works[0]!;
    expect(card.genre).toBeNull();
    expect(card.stage).toBeNull();
  });

  it('reports a workspace without works with the wa-cli new hint', async () => {
    const root = await mkdtemp(join(tmpdir(), 'wa-overview-'));
    tempRoots.push(root);
    await mkdir(join(root, '.writing-anything'));
    await writeFile(join(root, '.writing-anything/config.yaml'), 'schema_version: 1\n');
    const model = overviewModel(await snapshotOf(root, unavailableRunner));
    expect(model.state).toBe('no-works');
    expect(model.emptyCommand).toBe(NEW_WORK_COMMAND);
    expect(model.works).toEqual([]);
    expect(model.summary?.sources).toBe(0);
    expect(model.summary?.pendingDecisions).toBe(0);
  });

  it('reports a missing workspace with the wa-cli init hint and carries the path error', async () => {
    const body = await buildSnapshot(realSnapshotFs(), unavailableRunner, [], null);
    const model = overviewModel({ ...body, rootError: '项目路径必须是绝对路径：x' });
    expect(model.state).toBe('missing');
    expect(model.emptyCommand).toBe('wa-cli init');
    expect(model.rootError).toBe('项目路径必须是绝对路径：x');
    expect(model.summary).toBeNull();
    expect(overviewModel(body).rootError).toBeNull();
  });
});

describe('pendingDecisionCount', () => {
  it('counts open decisions, including flow-style entries', () => {
    expect(pendingDecisionCount('decisions:\n  - {id: a, status: open}\n  - {id: b, status: decided}\n  - id: c\n    status: open\n')).toBe(2);
  });

  it('treats an absent or empty file, or an empty list, as zero', () => {
    expect(pendingDecisionCount('')).toBe(0);
    expect(pendingDecisionCount(null)).toBe(0);
    expect(pendingDecisionCount(undefined)).toBe(0);
    expect(pendingDecisionCount('schema_version: 1\n')).toBe(0);
    expect(pendingDecisionCount('decisions: []\n')).toBe(0);
  });

  it('returns null for text that does not parse or has the wrong shape', () => {
    expect(pendingDecisionCount('decisions: [\n')).toBeNull();
    expect(pendingDecisionCount('decisions: nope\n')).toBeNull();
    expect(pendingDecisionCount('- just a list\n')).toBeNull();
  });
});
