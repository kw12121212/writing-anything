import { cp, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { fullSnapshot, realSnapshotFs, registerSnapshotRoute } from './host';
import { buildSnapshot, buildSnapshotPayload } from './snapshot';
import type { CliRunner } from './wacli';

const TESTDATA = join(dirname(fileURLToPath(import.meta.url)), '..', 'testdata');
const FIXTURE_ROOT = join(TESTDATA, 'workspace');
const CLI_DIR = join(TESTDATA, 'cli');

/** A fake CLI runner answering from the canned real wa-cli captures. */
function cannedRunner(): CliRunner & { calls: string[][] } {
  const calls: string[][] = [];
  return {
    calls,
    async run(args: readonly string[]): Promise<{ stdout: string } | null> {
      calls.push([...args]);
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
}

/** A runner modelling a host without wa-cli on PATH. */
const unavailableRunner: CliRunner = { run: async () => null };

async function tempCopyOfFixture(): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), 'wa-snapshot-'));
  await cp(FIXTURE_ROOT, root, { recursive: true });
  return root;
}

describe('buildSnapshotPayload on the multi-work fixture', () => {
  it('aggregates config, works, unit artifacts, ledgers, sources, packs, decisions and diagnostics', async () => {
    const runner = cannedRunner();
    const { payload, degraded } = await buildSnapshotPayload(realSnapshotFs(), runner, FIXTURE_ROOT);

    expect(degraded).toEqual([]);
    expect(payload.config?.path).toBe('config.yaml');
    expect(payload.config?.text).toContain('gate_level: strict');
    expect(payload.decisions?.text).toContain('dec-1');
    expect(payload.handoffs?.text).toContain('建立工作区快照测试夹具');
    expect(payload.authorReviews?.text).toContain('channel: preview');
    expect(payload.packLock?.path).toBe('packs.lock.yaml');
    expect(payload.packs.map((p) => p.path)).toEqual(['packs/lean-style.yaml']);
    expect(payload.sources.map((s) => s.id)).toEqual(['src-migration', 'src-note']);
    expect(payload.sources[0]?.original?.text).toContain('事故率下降了 40%');

    expect(payload.works.map((w) => w.id)).toEqual(['my-novel', 'my-post']);
    const novel = payload.works[0]!;
    expect(novel.workYaml?.text).toContain('genre: novel');
    expect(Object.keys(novel.ledgers).sort()).toEqual(['canon', 'knowledge', 'state', 'threads']);
    expect(novel.ledgers.canon?.text).toContain('lin-che');
    expect(novel.ledgers.threads?.text).toContain('log-thread');
    expect(novel.timeline?.map((row) => row.unit)).toEqual(['my-novel/scene-1', 'my-novel/scene-2']);
    // Root unit plus every directory-owning child; structural-only units
    // (part/chapter) stay in cli.status.units.
    expect(novel.units.map((u) => u.ref)).toEqual(['my-novel', 'my-novel/scene-1', 'my-novel/scene-2']);
    const scene1 = novel.units[1]!;
    expect(scene1.type).toBe('scene');
    expect(scene1.stage).toBe('brief');
    expect(scene1.blockers.length).toBeGreaterThan(0);
    expect(scene1.artifacts.outline?.text).toContain('pov: lin-che');
    expect(scene1.artifacts.outline?.text).toContain('plants:');

    const post = payload.works[1]!;
    const root = post.units[0]!;
    expect(root.ref).toBe('my-post');
    expect(root.type).toBe('article');
    expect(root.draft?.format).toBe('markdown');
    expect(root.draft?.text).toContain('契约测试');
    expect(root.artifacts.brief?.text).toContain('msg-boundary');
    expect(root.artifacts.outline?.text).toContain('situation');
    expect(root.artifacts.claims?.text).toContain('事故率');
    expect(root.artifacts.feedback?.text).toContain('开头再直接一点');
    expect(root.artifacts.provenance?.path).toBe('works/my-post/provenance.yaml');

    // Embedded validate diagnostics as the error-state data.
    expect(payload.cli.validate?.valid).toBe(false);
    expect(payload.cli.validate?.diagnostics.length).toBe(5);
    expect(payload.cli.next?.pendingAuthorReviews).toEqual([]);
    expect(payload.cli.status?.sources).toBe(2);

    // The bridge always puts the subcommand first and pins --dir and --json.
    expect(runner.calls).toContainEqual(['status', '--dir', FIXTURE_ROOT, '--json']);
    expect(runner.calls).toContainEqual(['validate', '--dir', FIXTURE_ROOT, '--json']);
    expect(runner.calls).toContainEqual(['next', '--dir', FIXTURE_ROOT, '--json']);
    expect(runner.calls).toContainEqual(['timeline', 'my-novel', '--dir', FIXTURE_ROOT, '--json']);
  });

  it('degrades per area when wa-cli is unavailable but still serves file-parsed data', async () => {
    const { payload, degraded } = await buildSnapshotPayload(realSnapshotFs(), unavailableRunner, FIXTURE_ROOT);
    expect(payload.cli.status).toBeNull();
    expect(payload.cli.validate).toBeNull();
    expect(payload.cli.next).toBeNull();
    expect(payload.works.map((w) => w.id)).toEqual(['my-novel', 'my-post']);
    const post = payload.works[1]!;
    expect(post.units[0]!.draft?.text).toContain('契约测试');
    // Directory walk alone cannot know types, stages or blockers.
    expect(post.units[0]!.type).toBeNull();
    expect(post.units[0]!.stage).toBeNull();
    expect(post.units[0]!.blockers).toEqual([]);
    expect(post.timeline).toBeNull();
    expect(degraded.length).toBe(3);
    for (const note of degraded) expect(note).toContain('降级');
  });

  it('annotates degradation when status output is missing mapped fields', async () => {
    const runner: CliRunner = {
      run: async (args: readonly string[]) => ({
        stdout:
          args[0] === 'status'
            ? JSON.stringify({ valid: false, diagnostics: [], changedPaths: [], units: [{ unit: 'my-post', work: 'my-post' }] })
            : JSON.stringify({ valid: true, diagnostics: [], changedPaths: [], steps: [], pendingAuthorReviews: [] }),
      }),
    };
    const { payload, degraded } = await buildSnapshotPayload(realSnapshotFs(), runner, FIXTURE_ROOT);
    expect(degraded.join('\n')).toContain('缺少字段 config');
    expect(degraded.join('\n')).toContain('sources');
    expect(payload.cli.status?.config).toBeNull();
    const post = payload.works.find((w) => w.id === 'my-post')!;
    expect(post.units[0]!.stage).toBeNull();
  });

  it('degrades per area when wa-cli stdout is not JSON, without failing', async () => {
    const garbageRunner: CliRunner = { run: async () => ({ stdout: 'not-json{{' }) };
    const { payload, degraded } = await buildSnapshotPayload(realSnapshotFs(), garbageRunner, FIXTURE_ROOT);
    expect(payload.cli.status).toBeNull();
    expect(payload.cli.validate).toBeNull();
    expect(payload.works.map((w) => w.id)).toEqual(['my-novel', 'my-post']);
    expect(degraded.length).toBe(3);
    expect(degraded.join('\n')).toContain('无法解析');
  });
});

describe('buildSnapshot route body', () => {
  it('serves a readable missing-workspace state with the wa-cli init hint', async () => {
    const body = await buildSnapshot(realSnapshotFs(), unavailableRunner, [], null);
    expect(body.ok).toBe(true);
    expect(body.workspaceExists).toBe(false);
    expect(body.snapshot).toBeNull();
    expect(body.emptyState).toEqual({ command: 'wa-cli init' });
    expect(body.degraded).toEqual([]);
  });

  it('locates the workspace and repeats the resolution trail', async () => {
    const body = await buildSnapshot(realSnapshotFs(), unavailableRunner, [], {
      root: FIXTURE_ROOT,
      source: 'config',
    });
    expect(body.root).toBe(FIXTURE_ROOT);
    expect(body.source).toBe('config');
    expect(body.workspaceExists).toBe(true);
    expect(body.workspaceDir).toBe(join(FIXTURE_ROOT, '.writing-anything'));
    expect(body.snapshot?.works.map((w) => w.id)).toEqual(['my-novel', 'my-post']);
  });

  it('re-reads the workspace on every request: a draft edit shows up immediately', async () => {
    const root = await tempCopyOfFixture();
    const before = await buildSnapshotPayload(realSnapshotFs(), unavailableRunner, root);
    expect(before.payload.works[1]!.units[0]!.draft?.text).not.toContain('第二天清晨');
    await writeFile(join(root, '.writing-anything/works/my-post/draft.md'), '第二天清晨的段落。\n', 'utf8');
    const after = await buildSnapshotPayload(realSnapshotFs(), unavailableRunner, root);
    expect(after.payload.works[1]!.units[0]!.draft?.text).toContain('第二天清晨的段落');
  });

  it('skips symlinked unit directories and artifact files instead of following them', async () => {
    const root = await tempCopyOfFixture();
    const outside = await mkdtemp(join(tmpdir(), 'wa-outside-'));
    await writeFile(join(outside, 'outline.yaml'), 'leaked: true\n', 'utf8');
    await writeFile(join(outside, 'brief.yaml'), 'leaked: true\n', 'utf8');
    // A symlinked child unit directory must not become a unit.
    await symlink(outside, join(root, '.writing-anything/works/my-novel/units/evil'));
    // A symlinked artifact file must read as absent, not as the outside file.
    await rm(join(root, '.writing-anything/works/my-post/brief.yaml'));
    await symlink(join(outside, 'brief.yaml'), join(root, '.writing-anything/works/my-post/brief.yaml'));
    const { payload } = await buildSnapshotPayload(realSnapshotFs(), unavailableRunner, root);
    const novel = payload.works.find((w) => w.id === 'my-novel')!;
    expect(novel.units.map((u) => u.ref)).not.toContain('my-novel/evil');
    const post = payload.works.find((w) => w.id === 'my-post')!;
    expect(post.units[0]!.artifacts.brief).toBeUndefined();
    for (const unit of post.units) {
      for (const artifact of Object.values(unit.artifacts)) {
        expect(artifact.text).not.toContain('leaked');
      }
    }
  });

  it('serves the missing-workspace body through the registered snapshot route', async () => {
    const routes: { path: string; handler: (req: unknown, res: unknown) => Promise<void> }[] = [];
    registerSnapshotRoute(
      { webServer: { register: (route: (typeof routes)[number]) => routes.push(route) } } as Parameters<typeof registerSnapshotRoute>[0],
      async () => buildSnapshot(realSnapshotFs(), unavailableRunner, [], null),
    );
    expect(routes[0]!.path).toBe('/dsh-writing-viewer/snapshot');
    const written: { status: number; body?: string }[] = [];
    const res = {
      writeHead: (status: number) => {
        written.push({ status });
        return res;
      },
      end: (body?: string) => {
        written[written.length - 1]!.body = body;
      },
    };
    await routes[0]!.handler({ method: 'GET' }, res);
    const body = JSON.parse(written[0]!.body!) as { ok: boolean; workspaceExists: boolean; emptyState: { command: string }; snapshot: unknown };
    expect(body.ok).toBe(true);
    expect(body.workspaceExists).toBe(false);
    expect(body.emptyState).toEqual({ command: 'wa-cli init' });
    expect(body.snapshot).toBeNull();
  });
});

/**
 * The audit-log exception, pinned with a writing fake binary: wa-cli appends
 * one line to .writing-anything/audit.log on every invocation — read-only
 * commands included; that is the audit contract of the CLI, not a plugin
 * write. With a wa-cli-shaped binary in place, one snapshot request may
 * touch the audit log and nothing else.
 */
const itPosix = process.platform === 'win32' ? it.skip : it;

describe('fullSnapshot with a spawned wa-cli', () => {
  itPosix('one snapshot appends only the audit log and leaves every artifact untouched', async () => {
    const root = await tempCopyOfFixture();
    const binDir = await mkdtemp(join(tmpdir(), 'wa-fakecli-'));
    const bin = join(binDir, 'wa-cli');
    const script = [
      '#!/bin/sh',
      'dir=',
      'prev=',
      'for a in "$@"; do',
      '  if [ "$prev" = "--dir" ]; then dir=$a; fi',
      '  prev=$a',
      'done',
      'echo "audit $1" >> "$dir/.writing-anything/audit.log"',
      `case "$1" in`,
      `  status) sed "s|<root>|$dir|g" '${CLI_DIR}/status.json' ;;`,
      `  validate) sed "s|<root>|$dir|g" '${CLI_DIR}/validate.json' ;;`,
      `  next) sed "s|<root>|$dir|g" '${CLI_DIR}/next.json' ;;`,
      `  timeline) f="$2"; sed "s|<root>|$dir|g" "${CLI_DIR}/timeline-$f.json" ;;`,
      `esac`,
    ].join('\n');
    await writeFile(bin, script, { mode: 0o755 });

    const before = await inventoryOf(join(root, '.writing-anything'));
    const body = await fullSnapshot(null, { workspaceRoot: root, waCliPath: bin }, '/nowhere');

    expect(body.ok).toBe(true);
    expect(body.workspaceExists).toBe(true);
    expect(body.degraded).toEqual([]);
    expect(body.snapshot?.works.map((w) => w.id)).toEqual(['my-novel', 'my-post']);
    expect(body.snapshot?.cli.validate?.diagnostics.length).toBe(5);

    // status + validate + next + timeline(my-novel): four bounded calls.
    const audit = await readFile(join(root, '.writing-anything/audit.log'), 'utf8');
    expect(audit.trim().split('\n')).toEqual(['audit status', 'audit validate', 'audit next', 'audit timeline']);

    // Everything that existed before is byte-identical; the only new file is
    // the audit log the CLI itself appends to.
    const after = await inventoryOf(join(root, '.writing-anything'));
    for (const [path, fingerprint] of before) {
      expect(after.get(path), path).toBe(fingerprint);
    }
    const added = [...after.keys()].filter((path) => !before.has(path));
    expect(added).toEqual(['audit.log']);

    await rm(binDir, { recursive: true, force: true });
    await rm(root, { recursive: true, force: true });
  });
});

/** Recursive fingerprint map for the audit-exception test. */
async function inventoryOf(root: string): Promise<Map<string, string>> {
  const { readdir, stat } = await import('node:fs/promises');
  const { createHash } = await import('node:crypto');
  const out = new Map<string, string>();
  async function walk(dir: string, rel: string): Promise<void> {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const abs = join(dir, entry.name);
      const relPath = rel === '' ? entry.name : `${rel}/${entry.name}`;
      if (entry.isDirectory()) {
        await walk(abs, relPath);
        continue;
      }
      if (!entry.isFile()) continue;
      const [info, content] = await Promise.all([stat(abs), readFile(abs)]);
      out.set(relPath, `${info.size}:${createHash('sha256').update(content).digest('hex')}`);
    }
  }
  await walk(root, '');
  return out;
}
