/**
 * Large-workspace performance probe for the panel (novel with volumes,
 * chapters and scenes, generated through wa-cli by scripts/perf-check.mjs).
 *
 * It times the real host aggregation (buildSnapshot over the generated
 * workspace with a real wa-cli) and the client render path (dashboard model,
 * unit tree, first-screen renderPanel, unit switching, and the artifact
 * views of a large YAML), and logs every phase as a `perf:` line. The run is
 * evidence, not a gate: there is no hard numeric threshold — the recorded
 * numbers land in docs/evidence/perf-check.txt via the driver script.
 *
 * Set LARGE_WS_DIR (workspace project dir) and optionally LARGE_WA_CLI; the
 * test skips when LARGE_WS_DIR is absent so `pnpm test` stays fast, while
 * `pnpm run perf:check` always sets it.
 */
import { performance } from 'node:perf_hooks';
import { describe, expect, it } from 'vitest';
import { realSnapshotFs, spawnCliRunner } from './host';
import { buildSnapshot, type SnapshotRouteBody } from './snapshot';

const wsDir = process.env.LARGE_WS_DIR ?? null;
const waCli = process.env.LARGE_WA_CLI ?? 'wa-cli';

// The client entry runs window.__ModuleLoader__.load at module top level; a
// recording stub must exist before the dynamic import (same stance as
// client.test.ts). Only the pure render hook is used here.
interface LoadDefinition {
  id: string;
  factory: (require: (id: string) => unknown) => unknown;
}
let captured: { definition: LoadDefinition } | null = null;
;(globalThis as unknown as { window: unknown }).window = {
  __ModuleLoader__: { load: (definition: LoadDefinition) => { captured = { definition }; } },
};

type H = (type: string, props: Record<string, unknown> | null, ...children: unknown[]) => unknown;
const h: H = (type, props, ...children) => ({ type, props, children });

interface PerfHooks {
  overviewModel: (body: SnapshotRouteBody) => any;
  reconcileSelection: (body: SnapshotRouteBody, model: any, work: string | null, unit: string | null) => any;
  renderPanel: (h: unknown, state: Record<string, unknown>, actions: Record<string, unknown>) => unknown;
  labelsFor: (active?: string | null) => Record<string, string>;
}

async function hooks(): Promise<PerfHooks> {
  if (captured === null) await import('./client');
  const plugin = (captured as { definition: LoadDefinition }).definition.factory(() => ({})) as { __test: PerfHooks };
  return plugin.__test;
}

function ms(start: number): string {
  return (performance.now() - start).toFixed(0) + 'ms';
}

const noop = (): void => {};
const actions = { refresh: noop, setPathInput: noop, switchTo: noop, restoreDefault: noop, selectWork: noop, selectUnit: noop, openArtifactView: noop, closeArtifactView: noop, toggleViewMode: noop, showSources: noop, backToOverview: noop, toggleSourcesMode: noop, toggleFold: noop, treeKey: noop };

function countNodes(node: unknown): number {
  if (Array.isArray(node)) return node.reduce((sum: number, child) => sum + countNodes(child), 0);
  if (node === null || typeof node !== 'object') return 0;
  const n = node as { children?: unknown };
  return 1 + countNodes(n.children);
}

describe.skipIf(wsDir === null)('large workspace performance probe', () => {
  it('aggregates and renders a multi-volume novel workspace without interactive-freeze-scale work', async () => {
    const t = await hooks();
    const dir = wsDir as string;

    // Host aggregation over the real workspace with a real wa-cli.
    const snapStart = performance.now();
    const body = await buildSnapshot(realSnapshotFs(), spawnCliRunner(waCli), [{ source: 'panel-input', path: dir, workspaceExists: true }], { root: dir, source: 'panel-input' });
    const snapTime = ms(snapStart);
    expect(body.degraded).toEqual([]);
    expect(body.snapshot?.cli.status?.units.length).toBeGreaterThan(150);

    // Client model + tree.
    const modelStart = performance.now();
    const model = t.overviewModel(body);
    const modelTime = ms(modelStart);
    expect(model.state).toBe('ready');
    const work = model.works[0].id as string;
    const treeStart = performance.now();
    const treeNodes = countUnitTree(body, work);
    const treeTime = ms(treeStart);
    expect(treeNodes).toBeGreaterThan(150);

    // First screen: full panel render from a fresh state.
    const picked = t.reconcileSelection(body, model, null, null);
    const firstState = { labels: t.labelsFor('en'), loading: false, loadError: null, body, model, selectedWork: picked.work, selectedUnit: picked.unit, tree: picked.tree, detail: picked.detail, stacked: false, pathInput: '', panelRoot: null, view: 'overview', openArtifact: null, viewMode: 'semantic', sourcesMode: 'reading', selectedSource: null, openFolds: null, treeCollapsed: null };
    const firstStart = performance.now();
    const first = t.renderPanel(h, firstState, actions);
    const firstTime = ms(firstStart);
    expect(countNodes(first)).toBeGreaterThan(1000);

    // Unit switching: re-render with a different selected scene, as the
    // detail area follows arrow-key navigation.
    const sceneRefs = (picked.tree as { ref: string }[]).filter((n) => (n as { label?: string }).label?.startsWith('sc-')).map((n) => n.ref);
    expect(sceneRefs.length).toBeGreaterThan(50);
    const firstScene = sceneRefs[0];
    if (firstScene === undefined) throw new Error('no scene refs in the generated tree');
    const switchStart = performance.now();
    let lastNodeCount = 0;
    for (const ref of sceneRefs) {
      const next = t.reconcileSelection(body, model, work, ref);
      lastNodeCount = countNodes(t.renderPanel(h, { ...firstState, selectedUnit: ref, tree: next.tree, detail: next.detail }, actions));
    }
    const switchTime = ms(switchStart);
    const perSwitch = ((performance.now() - switchStart) / sceneRefs.length).toFixed(1) + 'ms';
    expect(lastNodeCount).toBeGreaterThan(1000);

    // Large-YAML artifact views: a novel-scale outline folded in the source
    // view and carded in the semantic view. The artifact is injected into the
    // root unit — the one unit whose directory exists in a freshly generated
    // workspace (scene directories appear once they own artifacts).
    const bigYaml = 'outline:\n' + Array.from({ length: 40 }, (_, section) =>
      `  section-${section}:\n` + Array.from({ length: 30 }, (_, beat) => `    beat-${beat}: 第 ${section}-${beat} 拍：人物推进与转折，场景目标与冲突升级，收束在下一拍钩子上。\n`).join('')).join('');
    const bigBody = JSON.parse(JSON.stringify(body)) as SnapshotRouteBody;
    const rootUnit = bigBody.snapshot!.works[0]!.units[0]!;
    expect(rootUnit.ref).toBe(work);
    rootUnit.artifacts['outline'] = { path: rootUnit.artifacts['outline']?.path ?? '.writing-anything/outline.yaml', format: 'yaml', text: bigYaml };
    const bigModel = t.overviewModel(bigBody);
    const bigPicked = t.reconcileSelection(bigBody, bigModel, work, work);
    const openState = { ...firstState, body: bigBody, model: bigModel, selectedUnit: work, tree: bigPicked.tree, detail: bigPicked.detail, openArtifact: { unit: work, name: 'outline' }, viewMode: 'source' as const };
    const srcStart = performance.now();
    const sourceView = t.renderPanel(h, openState, actions);
    const srcTime = ms(srcStart);
    const semStart = performance.now();
    const semanticView = t.renderPanel(h, { ...openState, viewMode: 'semantic' }, actions);
    const semTime = ms(semStart);
    expect(countNodes(sourceView)).toBeGreaterThan(500);
    expect(countNodes(semanticView)).toBeGreaterThan(500);

    console.log(`perf: buildSnapshot(220 units, real wa-cli) ${snapTime}`);
    console.log(`perf: overviewModel+workCards ${modelTime}`);
    console.log(`perf: unitTree(${treeNodes} nodes) ${treeTime}`);
    console.log(`perf: first-screen renderPanel ${firstTime} (${countNodes(first)} nodes)`);
    console.log(`perf: unit switching ${sceneRefs.length} renders ${switchTime} (~${perSwitch}/switch)`);
    console.log(`perf: large YAML source view (folded, ${bigYaml.split('\n').length} lines) ${srcTime}`);
    console.log(`perf: large YAML semantic view ${semTime}`);
  }, 120_000);
});

/** Node count of one work's tree without importing the client bundle twice. */
function countUnitTree(body: SnapshotRouteBody, workId: string): number {
  const work = body.snapshot?.works.find((w) => w.id === workId);
  const rows = body.snapshot?.cli.status?.units.filter((row) => row.work === workId) ?? [];
  if (rows.length > 0) return rows.length;
  return work?.units.length ?? 0;
}
