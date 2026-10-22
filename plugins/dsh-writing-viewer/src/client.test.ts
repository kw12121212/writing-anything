import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { realSnapshotFs } from './host';
import { buildSnapshot, type SnapshotRouteBody } from './snapshot';
import type { CliRunner } from './wacli';

const TESTDATA = join(dirname(fileURLToPath(import.meta.url)), '..', 'testdata');

interface LoadDefinition {
  id: string;
  factory: (require: (id: string) => unknown) => unknown;
}

interface Captured {
  definition: LoadDefinition;
}

let captured: Captured | null = null;

// The client entry executes window.__ModuleLoader__.load at module top level,
// so the stub must exist on the global object before the dynamic import runs.
// A static import would hoist past this assignment.
;(globalThis as unknown as { window: unknown }).window = {
  __ModuleLoader__: {
    load: (definition: LoadDefinition) => {
      captured = { definition };
    },
  },
};

const fakeReact = {
  createElement: (...args: unknown[]) => ({ kind: 'element', args }),
  useState: (initial: unknown) => [initial, () => {}],
  useCallback: (fn: unknown) => fn,
  useEffect: () => {},
  useRef: (initial: unknown) => ({ current: initial }),
  useMemo: (fn: () => unknown) => fn(),
};

async function loadClientPlugin(): Promise<Record<string, unknown>> {
  // The client entry evaluates once per module registry: the first import
  // executes it and captures the definition; later tests reuse that result.
  if (cachedPlugin === null) {
    await import('./client');
    const got = captured as Captured | null;
    if (got === null) throw new Error('client entry did not call window.__ModuleLoader__.load');
    cachedPlugin = got.definition.factory((id: string) => {
      if (id === 'react') return fakeReact;
      throw new Error('unexpected require: ' + id);
    }) as Record<string, unknown>;
  }
  return cachedPlugin;
}

let cachedPlugin: Record<string, unknown> | null = null;

function slotCollector() {
  const registrations: { slot: string; options: Record<string, unknown>; component: unknown }[] = [];
  const slots = {
    inject: (name: string, start: (...args: unknown[]) => Generator | void) => {
      const maybeGenerator = start();
      if (maybeGenerator !== null && typeof maybeGenerator === 'object' && typeof (maybeGenerator as Generator).next === 'function') {
        const generator = maybeGenerator as Generator;
        let step = generator.next();
        while (!step.done) step = generator.next();
      }
    },
    register: (options: Record<string, unknown>, component: unknown) => {
      registrations.push({ slot: String(options['name']), options, component });
      return () => {};
    },
  };
  return { slots, registrations };
}

describe('client bundle definition', () => {
  it('loads under the package id and returns a cordis-plugin export set', async () => {
    const plugin = await loadClientPlugin();
    expect(captured?.definition.id).toBe('dsh-writing-viewer');
    expect(typeof plugin['apply']).toBe('function');
    expect(plugin['inject']).toEqual(['slots', 'sidebarRightTabs']);
  });

  it('registers the main global panel and the sidebar entry under one panel id', async () => {
    const plugin = await loadClientPlugin();
    const { slots, registrations } = slotCollector();
    (plugin['apply'] as (ctx: { slots: unknown }) => void)({ slots });
    const main = registrations.find((r) => r.slot === 'main');
    const sidebar = registrations.find((r) => r.slot === 'sidebar.panellist');
    expect(main).toBeDefined();
    expect(main?.options['key']).toBe('dsh-writing-viewer');
    expect(typeof main?.component).toBe('function');
    expect(sidebar).toBeDefined();
    expect(sidebar?.options['id']).toBe('dsh-writing-viewer');
    expect(typeof sidebar?.options['label'] === 'function' ? (sidebar.options['label'] as () => string)() : null).toBeTruthy();
    expect(typeof sidebar?.component).toBe('function');
  });

  it('registers a per-conversation right-sidebar pane when the host provides the service', async () => {
    const plugin = await loadClientPlugin();
    const { slots, registrations } = slotCollector();
    const tabDefinitions: Record<string, unknown>[] = [];
    const sidebarRightTabs = {
      register: (definition: Record<string, unknown>) => {
        tabDefinitions.push(definition);
      },
    };
    (plugin['apply'] as (ctx: { slots: unknown; sidebarRightTabs?: unknown }) => void)({ slots, sidebarRightTabs });
    // Static page-type registration: id, kind, thunked title, guide entry box.
    expect(tabDefinitions).toHaveLength(1);
    const tab = tabDefinitions[0]!;
    expect(tab['id']).toBe('dsh-writing-viewer');
    expect(tab['kind']).toBe('writing-workspace');
    // The host passes the page address to title; the thunk ignores it but must
    // still answer (a page type's address is sidebar://<kind>).
    expect((tab['title'] as (address: string) => string)('sidebar://writing-workspace')).toBeTruthy();
    const guide = tab['guide'] as { id: string; order: number; title: () => string }[];
    expect(guide[0]!.id).toBe('open');
    expect(guide[0]!.order).toBe(90);
    expect(guide[0]!.title()).toBeTruthy();
    // The pane body registers under the same id in the right-pane seat and is
    // the SAME panel component the left main seat registered (design: reuse).
    const pane = registrations.find((r) => r.slot === 'sidebar.right.pane.tab');
    expect(pane).toBeDefined();
    expect(pane?.options['key']).toBe('dsh-writing-viewer');
    expect(pane?.component).toBe(registrations.find((r) => r.slot === 'main')?.component);
  });

  it('keeps the left entries when a directly-driven ctx provides no sidebarRightTabs', async () => {
    // Unit-level ctx-shape tolerance only: in a real host, exports.inject is a
    // hard activation gate, so a composition without the service never reaches
    // apply at all (documented in README) — this test covers the defensive
    // branch for direct callers and future host shapes.
    const plugin = await loadClientPlugin();
    const { slots, registrations } = slotCollector();
    (plugin['apply'] as (ctx: { slots: unknown }) => void)({ slots });
    expect(registrations.some((r) => r.slot === 'sidebar.right.pane.tab')).toBe(false);
    expect(registrations.find((r) => r.slot === 'main')).toBeDefined();
    expect(registrations.find((r) => r.slot === 'sidebar.panellist')).toBeDefined();
  });

  it('stays inert with an empty export set when the host has no react', async () => {
    const got = (captured as Captured | null)?.definition;
    if (got === undefined) throw new Error('client definition not captured');
    const plugin = got.factory((id: string) => {
      throw new Error('no module on this host: ' + id);
    }) as Record<string, unknown>;
    expect(typeof plugin['apply']).toBe('function');
    expect(plugin['inject']).toEqual([]);
    expect((plugin['apply'] as () => void)()).toBeUndefined();
    expect(plugin['__test']).toBeDefined();
  });

  it('renders the registered panel component in its loading state', async () => {
    const plugin = await loadClientPlugin();
    const { slots, registrations } = slotCollector();
    (plugin['apply'] as (ctx: { slots: unknown }) => void)({ slots });
    const panel = registrations.find((r) => r.slot === 'main')!.component as () => { kind: string; args: unknown[] };
    const rendered = panel();
    expect(rendered.kind).toBe('element');
    expect(JSON.stringify(rendered.args)).toContain('dsh-writing-viewer');
  });

  it('stays inert when the host declares no slots service', async () => {
    const plugin = await loadClientPlugin();
    expect(() => (plugin['apply'] as (ctx: { slots: unknown }) => void)({ slots: undefined })).not.toThrow();
  });
});

describe('labelsFor', () => {
  it('picks the label set from the active language or the document', async () => {
    const plugin = await loadClientPlugin();
    const test = plugin['__test'] as { labelsFor: (active?: string | null) => Record<string, string> };
    expect(test.labelsFor('zh-CN')['panelTitle']).toBe('写作工作区');
    expect(test.labelsFor('en-US')['panelTitle']).toBe('Writing Workspace');
    expect(test.labelsFor(null)['emptyTitle']).toBeTruthy();
    expect(test.labelsFor(null)['retry']).toBeTruthy();
  });
});

interface Node {
  type: string;
  props: Record<string, unknown> | null;
  children: unknown[];
}

/** A createElement stand-in that keeps the rendered tree inspectable. */
const h = (type: string, props: Record<string, unknown> | null, ...children: unknown[]): Node => ({ type, props, children });

function walk(node: unknown, visit: (n: Node) => void): void {
  if (Array.isArray(node)) {
    for (const child of node) walk(child, visit);
    return;
  }
  if (node === null || typeof node !== 'object') return;
  const n = node as Node;
  visit(n);
  walk(n.children, visit);
}

function textOf(node: unknown): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(textOf).join(' ');
  if (node === null || typeof node !== 'object') return '';
  return textOf((node as Node).children);
}

/** Like textOf but joins without separators, so adjacent spans concatenate. */
function concatText(node: unknown): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(concatText).join('');
  if (node === null || typeof node !== 'object') return '';
  return concatText((node as Node).children);
}

function elements(node: unknown, type: string): Node[] {
  const out: Node[] = [];
  walk(node, (n) => {
    if (n.type === type) out.push(n);
  });
  return out;
}

interface TestHooks {
  overviewModel: (body: SnapshotRouteBody) => any;
  reconcileSelection: (body: SnapshotRouteBody, model: any, work: string | null, unit: string | null) => any;
  renderPanel: (h: unknown, state: Record<string, unknown>, actions: Record<string, unknown>) => unknown;
  routeUrl: (root: string | null) => string;
  shouldStack: (container: number | null, viewport: number | null) => boolean;
  labelsFor: (active?: string | null) => Record<string, string>;
  stageTitle: (stage: string | null, active?: string | null) => string;
  nextViewMode: (mode: 'semantic' | 'source') => 'semantic' | 'source';
  nextReadingMode: (mode: 'reading' | 'source') => 'reading' | 'source';
  artifactTextOf: (body: SnapshotRouteBody, unitRef: string, name: string) => { path: string; format: string; text: string } | null;
  sourceEntriesOf: (body: SnapshotRouteBody) => { id: string }[];
  FOLD_LINE_THRESHOLD: number;
  ROUTE_PATH: string;
  SETUP_ROUTE: string;
  parseSetupStatus: (body: unknown) => any;
  setupViewModel: (status: any, actionInFlight: boolean, labels: any) => any;
  setupLabelsFor: (active?: string | null) => any;
}

async function hooks(): Promise<TestHooks> {
  return (await loadClientPlugin())['__test'] as TestHooks;
}

const noop = (): void => {};
const actions = { refresh: noop, setPathInput: noop, switchTo: noop, restoreDefault: noop, selectWork: noop, selectUnit: noop, openArtifactView: noop, closeArtifactView: noop, toggleViewMode: noop, showSources: noop, showQueue: noop, backToOverview: noop, toggleSourcesMode: noop, enableSetup: noop };

async function fixtureBody(): Promise<SnapshotRouteBody> {
  const root = join(TESTDATA, 'workspace');
  const runner: CliRunner = {
    async run(args: readonly string[]) {
      const file = args[0] === 'timeline' ? `timeline-${args[1]}.json` : `${args[0]}.json`;
      return { stdout: await readFile(join(TESTDATA, 'cli', file), 'utf8') };
    },
  };
  return buildSnapshot(realSnapshotFs(), runner, [{ source: 'panel-input', path: root, workspaceExists: true }], { root, source: 'panel-input' });
}

/** page.json captured from the real wa-cli reading view service on the
 * fixture workspace (wa-cli preview my-post --serve, GET /page.json). */
const PAGE_STRICT = {
  title: 'my-post', strict: true, confirms: [], failures: [],
  units: [{ ref: 'my-post', stage: 'review', hasDraft: true, notes: [], global: [], paragraphs: [{ index: 1, line: 1, text: '正文', origin: 'human', findings: [] }] }],
};

async function pageFixture(): Promise<Record<string, unknown>> {
  return JSON.parse(await readFile(join(TESTDATA, 'cli', 'page-my-post.json'), 'utf8')) as Record<string, unknown>;
}

async function stateFor(body: SnapshotRouteBody | null, extra: Record<string, unknown> = {}): Promise<Record<string, unknown>> {
  const t = await hooks();
  const model = body === null ? null : t.overviewModel(body);
  const picked = body === null || model === null ? null : t.reconcileSelection(body, model, null, null);
  return {
    labels: t.labelsFor('en'),
    loading: false,
    loadError: null,
    body,
    model,
    selectedWork: picked?.work ?? null,
    selectedUnit: picked?.unit ?? null,
    tree: picked?.tree ?? [],
    detail: picked?.detail ?? null,
    stacked: false,
    pathInput: '',
    panelRoot: null,
    view: 'overview',
    openArtifact: null,
    viewMode: 'semantic',
    sourcesMode: 'reading',
    selectedSource: null,
    openFolds: {},
    treeCollapsed: {},
    setup: null,
    setupBusy: false,
    ...extra,
  };
}

describe('panel routing helpers', () => {
  it('requests the snapshot route, with the panel path as an encoded root parameter', async () => {
    const t = await hooks();
    expect(t.ROUTE_PATH).toBe('/dsh-writing-viewer/snapshot');
    expect(t.routeUrl(null)).toBe('/dsh-writing-viewer/snapshot');
    expect(t.routeUrl('/home/me/my book')).toBe('/dsh-writing-viewer/snapshot?root=%2Fhome%2Fme%2Fmy%20book');
  });

  it('stacks below a 1024px viewport or a 640px panel', async () => {
    const t = await hooks();
    expect(t.shouldStack(900, 1440)).toBe(false);
    expect(t.shouldStack(968, 1023)).toBe(true);
    expect(t.shouldStack(600, 1440)).toBe(true);
    expect(t.shouldStack(null, null)).toBe(false);
  });

  it('localizes known stages and keeps unknown ones verbatim', async () => {
    const t = await hooks();
    expect(t.stageTitle('outline', 'zh-CN')).toBe('大纲');
    expect(t.stageTitle('draft', 'en')).toBe('Draft');
    expect(t.stageTitle('custom-stage', 'en')).toBe('custom-stage');
    expect(t.stageTitle(null)).toBe('—');
  });
});

describe('latestGate', () => {
  it('lets only the most recent request settle', async () => {
    const t = (await hooks()) as unknown as { latestGate: () => { begin(): () => boolean } };
    const gate = t.latestGate();
    const first = gate.begin();
    expect(first()).toBe(true);
    const second = gate.begin();
    expect(first()).toBe(false);
    expect(second()).toBe(true);
  });
});

describe('reconcileSelection', () => {
  it('defaults to the first work and its root, and keeps a selection that still exists', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const model = t.overviewModel(body);
    const first = t.reconcileSelection(body, model, null, null);
    expect(first.work).toBe('my-novel');
    expect(first.unit).toBe('my-novel');
    expect(first.detail.ref).toBe('my-novel');
    const kept = t.reconcileSelection(body, model, 'my-novel', 'my-novel/ch-1');
    expect(kept.unit).toBe('my-novel/ch-1');
    const gone = t.reconcileSelection(body, model, 'deleted-work', 'deleted-work/x');
    expect(gone.work).toBe('my-novel');
    expect(gone.unit).toBe('my-novel');
  });
});

describe('renderPanel', () => {
  it('shows loading before the first answer and an error with retry after a failed load', async () => {
    const t = await hooks();
    const loading = t.renderPanel(h, await stateFor(null, { loading: true }), actions);
    expect(textOf(loading)).toContain('Loading');
    // The first-load hint is an announced status region, distinct from the
    // empty and error states: no empty-state guidance, no failure copy.
    const status = elements(loading, 'p').find((n) => n.props?.['role'] === 'status' && n.props?.['data-loading'] === 'first')!;
    expect(status).toBeDefined();
    expect(status.props?.['aria-busy']).toBe('true');
    expect(textOf(loading)).not.toContain('No writing-anything workspace found');
    expect(textOf(loading)).not.toContain('Failed to load');
    const failed = t.renderPanel(h, await stateFor(null, { loadError: 'boom' }), actions);
    expect(textOf(failed)).toContain('Failed to load the workspace snapshot: boom');
    expect(elements(failed, 'button').map(textOf)).toContain('Retry');
  });

  it('renders the dashboard, the unit tree and the selected unit detail', async () => {
    const t = await hooks();
    const tree = t.renderPanel(h, await stateFor(await fixtureBody()), actions);
    const text = textOf(tree);
    for (const expected of ['my-novel', 'my-post', 'Genre novel', 'Pending decisions', 'Blockers: 6', 'part-1', 'ch-1', 'scene-2', '0/2', 'Unit detail', 'panel input']) {
      expect(text).toContain(expected);
    }
    const layout = elements(tree, 'div').find((n) => n.props?.['data-layout'] !== undefined);
    expect(layout?.props?.['data-layout']).toBe('split');
    const stacked = t.renderPanel(h, await stateFor(await fixtureBody(), { stacked: true }), actions);
    expect(elements(stacked, 'div').find((n) => n.props?.['data-layout'] !== undefined)?.props?.['data-layout']).toBe('stacked');
  });

  it('binds the refresh button to the refresh action and flags an in-flight reload', async () => {
    const t = await hooks();
    let refreshed = 0;
    const bound = { ...actions, refresh: () => { refreshed += 1; } };
    const tree = t.renderPanel(h, await stateFor(await fixtureBody(), { loading: true }), bound);
    const refresh = elements(tree, 'button').find((b) => textOf(b) === 'Refresh')!;
    expect(refresh.props?.['disabled']).toBe(true);
    (refresh.props?.['onClick'] as () => void)();
    expect(refreshed).toBe(1);
    // Old data stays on screen during the reload, with a visible status.
    expect(elements(tree, 'span').some((n) => n.props?.['role'] === 'status' && textOf(n) === 'Loading…')).toBe(true);
    expect(textOf(tree)).toContain('my-novel');
  });

  it('lists validate diagnostics and degradation notes while still showing the works', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const degradedBody = { ...body, degraded: ['next: wa-cli 不可用或执行失败，该部分数据降级'] };
    degradedBody.snapshot = { ...body.snapshot!, cli: { ...body.snapshot!.cli, next: null } };
    const tree = t.renderPanel(h, await stateFor(degradedBody), actions);
    const text = textOf(tree);
    expect(text).toContain('validate diagnostics (error 5)');
    expect(text).toContain('outline.missing-reason');
    expect(text).toContain('Some data is degraded (1)');
    expect(text).toContain('next: wa-cli 不可用或执行失败');
    expect(text).toContain('my-post');
    // The unavailable pending-review count reads as unavailable, not zero.
    const tiles = elements(tree, 'div').filter((n) => textOf(n).startsWith('Pending reviews'));
    expect(textOf(tiles[tiles.length - 1])).toBe('Pending reviews —');
  });

  it('offers no editing, writing or confirming controls', async () => {
    const t = await hooks();
    const tree = t.renderPanel(h, await stateFor(await fixtureBody(), { panelRoot: '/x' }), actions);
    // The single input is the project path; nothing else accepts text.
    expect(elements(tree, 'input')).toHaveLength(1);
    expect(elements(tree, 'textarea')).toHaveLength(0);
    expect(elements(tree, 'select')).toHaveLength(0);
    walk(tree, (n) => expect(n.props?.['contentEditable']).toBeUndefined());
    const labels = elements(tree, 'button').map((b) => textOf(b));
    // Read-only controls only: refresh, path switching, work/unit selection,
    // artifact opening, sources navigation, and the two queue tiles. Nothing
    // edits, writes or confirms. The exceptions are the review entries: the
    // detail's single Review button and the「待审阅」markers, which only start
    // a review session (review-native-view, review-pending-discovery).
    const allowed =
      /^(Refresh|Switch|Restore default)$|^(my-novel|my-post)\b|^(my-novel|part-1|ch-1|scene-1|scene-2)\b|^Sources \d+$|^Pending decisions \d+( —)?$|^Pending reviews \d+( —)?$|^(brief|outline|claims|feedback|provenance|draft|work)\s+\S+\s+Open$|^(src-migration|src-note)$|^Review$|^Pending review · \d+$|^● \d+$/;
    for (const label of labels) expect(label, label).toMatch(allowed);
    expect(labels.filter((label) => label === 'Review')).toHaveLength(1);
  });

  it('shows the init guidance and the path error when no workspace was found', async () => {
    const t = await hooks();
    const body = { ...(await buildSnapshot(realSnapshotFs(), { run: async () => null }, [], null)), rootError: 'not absolute' };
    const tree = t.renderPanel(h, await stateFor(body, { panelRoot: 'x' }), actions);
    const text = textOf(tree);
    expect(text).toContain('No writing-anything workspace found');
    expect(text).toContain('not absolute');
    expect(text).toContain('wa-cli init');
    expect(elements(tree, 'button').map(textOf)).toContain('Restore default');
  });

  it('keeps every control outside the unit tree in the natural tab order', async () => {
    const t = await hooks();
    const tree = t.renderPanel(h, await stateFor(await fixtureBody()), actions);
    const outside = elements(tree, 'button').filter((b) => b.props?.['data-tree-ref'] === undefined);
    expect(outside.length).toBeGreaterThan(0);
    for (const button of outside) expect(button.props?.['tabIndex']).toBeUndefined();
  });
});

describe('keyboard navigation', () => {
  function treeButtons(rendered: unknown): Node[] {
    const nav = elements(rendered, 'nav').find((n) => n.props?.['data-tree'] === 'units');
    expect(nav).toBeDefined();
    const out: Node[] = [];
    const collect = (node: unknown): void => {
      if (Array.isArray(node)) {
        for (const child of node) collect(child);
        return;
      }
      if (node === null || typeof node !== 'object') return;
      const n = node as Node;
      // Tree nodes only; the「待审阅」markers beside them are separate buttons.
      if (n.type === 'button' && n.props?.['data-tree-ref'] !== undefined) out.push(n);
      collect(n.children);
    };
    collect((nav as Node).children);
    return out;
  }

  it('gives the tree one roving tab stop, per-node key handlers and expand state', async () => {
    const t = await hooks();
    const rendered = t.renderPanel(h, await stateFor(await fixtureBody()), actions);
    const nodes = treeButtons(rendered);
    expect(nodes.map((n) => n.props?.['data-tree-ref'])).toEqual(['my-novel', 'my-novel/part-1', 'my-novel/ch-1', 'my-novel/scene-1', 'my-novel/scene-2']);
    const tabStops = nodes.filter((n) => n.props?.['tabIndex'] === 0);
    expect(tabStops.map((n) => n.props?.['data-tree-ref'])).toEqual(['my-novel']);
    for (const node of nodes) {
      expect(node.props?.['onKeyDown'], 'onKeyDown on ' + String(node.props?.['data-tree-ref'])).toBeTypeOf('function');
    }
    // Nodes with children announce their expansion; leaves announce nothing.
    expect(nodes.find((n) => n.props?.['data-tree-ref'] === 'my-novel/part-1')?.props?.['aria-expanded']).toBe('true');
    expect(nodes.find((n) => n.props?.['data-tree-ref'] === 'my-novel/scene-1')?.props?.['aria-expanded']).toBeUndefined();
  });

  it('hides the subtree of a collapsed node and flags the node collapsed', async () => {
    const t = await hooks();
    const rendered = t.renderPanel(h, await stateFor(await fixtureBody(), { treeCollapsed: { 'my-novel/part-1': true } }), actions);
    const nodes = treeButtons(rendered);
    expect(nodes.map((n) => n.props?.['data-tree-ref'])).toEqual(['my-novel', 'my-novel/part-1']);
    expect(nodes.find((n) => n.props?.['data-tree-ref'] === 'my-novel/part-1')?.props?.['aria-expanded']).toBe('false');
  });

  it('routes tree keydowns through the treeKey action with the node ref', async () => {
    const t = await hooks();
    const calls: { event: unknown; ref: string }[] = [];
    const bound = { ...actions, treeKey: (event: unknown, ref: string) => { calls.push({ event, ref }); } };
    const rendered = t.renderPanel(h, await stateFor(await fixtureBody()), bound);
    const part = treeButtons(rendered).find((n) => n.props?.['data-tree-ref'] === 'my-novel/part-1')!;
    const event = { key: 'ArrowRight' };
    (part.props?.['onKeyDown'] as (event: unknown) => void)(event);
    expect(calls).toEqual([{ event, ref: 'my-novel/part-1' }]);
  });
});

describe('artifact views', () => {
  /** Panel state focused on one unit, with the artifact viewer wired in. */
  async function unitState(
    body: SnapshotRouteBody,
    extra: Record<string, unknown> = {},
  ): Promise<Record<string, unknown>> {
    const t = await hooks();
    const model = t.overviewModel(body);
    const picked = t.reconcileSelection(body, model, 'my-post', 'my-post');
    return {
      labels: t.labelsFor('en'),
      loading: false,
      loadError: null,
      body,
      model,
      selectedWork: 'my-post',
      selectedUnit: 'my-post',
      tree: picked?.tree ?? [],
      detail: picked?.detail ?? null,
      stacked: false,
      pathInput: '',
      panelRoot: null,
      view: 'overview',
      openArtifact: null,
      viewMode: 'semantic',
      sourcesMode: 'reading',
      selectedSource: null,
      setup: null,
      setupBusy: false,
      ...extra,
    };
  }

  /** Actions that record what the rendered tree invoked. */
  function recordingActions(): { actions: Record<string, unknown>; calls: string[] } {
    const calls: string[] = [];
    const actions = {
      refresh: () => calls.push('refresh'),
      setPathInput: () => calls.push('setPathInput'),
      switchTo: () => calls.push('switchTo'),
      restoreDefault: () => calls.push('restoreDefault'),
      selectWork: () => calls.push('selectWork'),
      selectUnit: () => calls.push('selectUnit'),
      openArtifactView: (unit: string, name: string) => calls.push(`open:${unit}/${name}`),
      closeArtifactView: () => calls.push('close'),
      toggleViewMode: () => calls.push('toggle-view'),
      showSources: (id: string | null) => calls.push(`sources:${id}`),
      showQueue: (view: 'decisions' | 'reviews') => calls.push(`queue:${view}`),
      backToOverview: () => calls.push('back-overview'),
      toggleSourcesMode: () => calls.push('toggle-sources'),
    };
    return { actions, calls };
  }

  it('lists unit artifacts as openable entries bound to openArtifactView', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const { actions, calls } = recordingActions();
    const tree = t.renderPanel(h, await unitState(body), actions);
    const brief = elements(tree, 'button').find((button) => button.props?.['data-open-artifact'] === 'brief');
    expect(brief).toBeDefined();
    (brief?.props?.['onClick'] as () => void)();
    expect(calls).toEqual(['open:my-post/brief']);
  });

  it('renders brief semantic cards and toggles to the highlighted source view', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const state = await unitState(body, { openArtifact: { unit: 'my-post', name: 'brief' } });
    const semantic = t.renderPanel(h, state, actions);
    const viewer = elements(semantic, 'section').find((n) => n.props?.['data-artifact-viewer'] === 'brief');
    expect(viewer).toBeDefined();
    expect(textOf(viewer)).toContain('维护大型单体服务的工程师');
    expect(textOf(viewer)).toContain('audience:');
    // Cards carry the key message and its clickable source reference.
    const sourceRef = elements(semantic, 'button').find((button) => button.props?.['data-source-ref'] === 'src-migration');
    expect(sourceRef).toBeDefined();

    const source = t.renderPanel(h, { ...state, viewMode: 'source' }, actions);
    const highlighted = elements(source, 'div').find((n) => n.props?.['data-yaml-source'] === 'flat');
    expect(highlighted).toBeDefined();
    // Each line's spans concatenate back to the verbatim source line.
    const lineTexts = elements(highlighted, 'div').map((line) => concatText(line));
    expect(lineTexts).toContain('audience: 维护大型单体服务的工程师');
    // Keys and scalars are colored as separate token spans.
    const tokenClasses = elements(highlighted, 'span').map((span) => String(span.props?.['data-yaml-token']));
    expect(tokenClasses).toContain('key');
    expect(tokenClasses).toContain('scalar');
    expect(tokenClasses).toContain('comment');
    // The toggle button reports the mode it would leave.
    const toggle = elements(source, 'button').find((button) => button.props?.['data-view-toggle'] === 'source');
    expect(toggle).toBeDefined();
  });

  it('shows the draft as a rendered reading view and a highlighted source view', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const state = await unitState(body, { openArtifact: { unit: 'my-post', name: 'draft' } });
    const reading = t.renderPanel(h, state, actions);
    const viewer = elements(reading, 'section').find((n) => n.props?.['data-artifact-viewer'] === 'draft');
    expect(viewer).toBeDefined();
    expect(elements(reading, 'p').map(textOf).join('\n')).toContain('重写大型单体最怕的不是写新代码');
    const source = t.renderPanel(h, { ...state, viewMode: 'source' }, actions);
    const highlighted = elements(source, 'div').find((n) => n.props?.['data-md-source'] === 'true');
    expect(highlighted).toBeDefined();
    expect(textOf(highlighted)).toContain('契约测试');
  });

  it('falls back to the source view with a readable alert when YAML fails to parse', async () => {
    const t = await hooks();
    const body = JSON.parse(JSON.stringify(await fixtureBody())) as SnapshotRouteBody;
    const post = body.snapshot!.works.find((work) => work.id === 'my-post')!;
    post.units.find((unit) => unit.ref === 'my-post')!.artifacts['brief']!.text = 'audience: [unclosed\n  bad:\n- x: {';
    const state = await unitState(body, { openArtifact: { unit: 'my-post', name: 'brief' } });
    const tree = t.renderPanel(h, state, actions);
    const alert = elements(tree, 'p').find((p) => p.props?.['data-parse-error'] === 'true');
    expect(alert).toBeDefined();
    expect(textOf(alert)).toContain('failed to parse');
    // The source view shows despite the semantic mode being requested.
    expect(elements(tree, 'div').some((n) => n.props?.['data-yaml-source'] === 'flat')).toBe(true);
    // No card content is invented for the broken artifact.
    expect(textOf(elements(tree, 'section').find((n) => n.props?.['data-artifact-viewer'] === 'brief'))).not.toContain('维护大型单体服务的工程师');
  });

  it('folds a large YAML source by top-level key and keeps small sources flat', async () => {
    const t = await hooks();
    const body = JSON.parse(JSON.stringify(await fixtureBody())) as SnapshotRouteBody;
    const post = body.snapshot!.works.find((work) => work.id === 'my-post')!;
    const lines = Array.from({ length: t.FOLD_LINE_THRESHOLD + 10 }, (_, i) => `key${i}: value${i}`);
    post.units.find((unit) => unit.ref === 'my-post')!.artifacts['brief']!.text = lines.join('\n');
    const state = await unitState(body, { openArtifact: { unit: 'my-post', name: 'brief' }, viewMode: 'source' });
    const tree = t.renderPanel(h, state, actions);
    const folded = elements(tree, 'div').find((n) => n.props?.['data-yaml-source'] === 'folded');
    expect(folded).toBeDefined();
    const folds = elements(tree, 'details').filter((n) => n.props?.['data-fold'] !== undefined);
    expect(folds.length).toBeGreaterThan(10);
    expect(textOf(folds[0])).toContain('key0: value0');

    const small = await unitState(await fixtureBody(), { openArtifact: { unit: 'my-post', name: 'brief' }, viewMode: 'source' });
    const smallTree = t.renderPanel(h, small, actions);
    expect(elements(smallTree, 'div').some((n) => n.props?.['data-yaml-source'] === 'flat')).toBe(true);
  });

  it('jumps from a claims card source reference to the open source entry', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const { actions, calls } = recordingActions();
    const state = await unitState(body, { openArtifact: { unit: 'my-post', name: 'claims' } });
    const tree = t.renderPanel(h, state, actions);
    const ref = elements(tree, 'button').find((button) => button.props?.['data-source-ref'] === 'src-note');
    expect(ref).toBeDefined();
    (ref?.props?.['onClick'] as () => void)();
    expect(calls).toEqual(['sources:src-note']);

    // With the sources view open and that source selected, its entry is open
    // and shows the metadata card plus the original reading view.
    const sourcesState = await unitState(body, { view: 'sources', selectedSource: 'src-note' });
    const sources = t.renderPanel(h, sourcesState, actions);
    const view = elements(sources, 'section').find((n) => n.props?.['data-sources-view'] === 'true');
    expect(view).toBeDefined();
    const entry = elements(sources, 'details').find((n) => n.props?.['data-source-entry'] === 'src-note');
    expect(entry?.props?.['open']).toBe(true);
    expect(textOf(entry)).toContain('author-note');
    expect(textOf(entry)).toContain('作者亲历说明');
    // The other entry stays collapsed but present.
    const other = elements(sources, 'details').find((n) => n.props?.['data-source-entry'] === 'src-migration');
    expect(other?.props?.['open']).toBeUndefined();
  });

  it('switches the sources originals between reading and highlighted source', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const state = await unitState(body, { view: 'sources' });
    const reading = t.renderPanel(h, state, actions);
    expect(elements(reading, 'p').map(textOf).join('\n')).toContain('迁移');
    expect(elements(reading, 'div').some((n) => n.props?.['data-md-source'] === 'true')).toBe(false);

    const highlighted = t.renderPanel(h, { ...state, sourcesMode: 'source' }, actions);
    expect(elements(highlighted, 'div').some((n) => n.props?.['data-md-source'] === 'true')).toBe(true);
    const modeButton = elements(highlighted, 'button').find((button) => button.props?.['data-sources-mode'] === 'source');
    expect(modeButton).toBeDefined();
  });

  it('offers no editing, writing or confirming controls in the artifact and sources views', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const allowed =
      /^(Refresh|Switch|Restore default)$|^(my-novel|my-post)\b|^(my-novel|part-1|ch-1|scene-1|scene-2)\b|^Sources \d+$|^Pending decisions \d+( —)?$|^Pending reviews \d+( —)?$|^(brief|outline|claims|feedback|provenance|draft|work)\s+\S+\s+Open$|^(src-migration|src-note)$|^(Back to unit detail|Back to overview|Semantic cards|Source highlight|Reading view)$|^Review$|^Pending review · \d+$|^● \d+$/;
    for (const extra of [
      { openArtifact: { unit: 'my-post', name: 'brief' } },
      { openArtifact: { unit: 'my-post', name: 'brief' }, viewMode: 'source' },
      { openArtifact: { unit: 'my-post', name: 'draft' }, viewMode: 'source' },
      { view: 'sources', selectedSource: 'src-note' },
      { view: 'sources', sourcesMode: 'source' },
    ]) {
      const tree = t.renderPanel(h, await unitState(body, extra), actions);
      expect(elements(tree, 'input')).toHaveLength(1);
      expect(elements(tree, 'textarea')).toHaveLength(0);
      expect(elements(tree, 'select')).toHaveLength(0);
      walk(tree, (n) => expect(n.props?.['contentEditable']).toBeUndefined());
      for (const button of elements(tree, 'button')) {
        expect(textOf(button), textOf(button)).toMatch(allowed);
      }
    }
  });

  it('toggles view modes through the exported pure helpers', async () => {
    const t = await hooks();
    expect(t.nextViewMode('semantic')).toBe('source');
    expect(t.nextViewMode('source')).toBe('semantic');
    expect(t.nextReadingMode('reading')).toBe('source');
    expect(t.nextReadingMode('source')).toBe('reading');
  });
});
describe('ViewerPanel wiring (runnable react)', () => {
  /**
   * A hooks-capable react stand-in: state slots persist across renders,
   * setters re-render synchronously, memoized callbacks keep their identity,
   * effects re-run only when their deps changed, and function refs receive
   * one shared fake DOM node so scrollTop capture/restore is observable.
   * It drives the real ViewerPanel end to end against a stubbed fetch.
   */
  interface MountedPanel {
    tree(): unknown;
    node: { scrollTop: number; clientWidth: number };
    restore(): void;
  }

  async function mountPanel(body: SnapshotRouteBody, fetchImpl?: typeof fetch): Promise<MountedPanel> {
    const plugin = await loadClientPlugin();
    void plugin;
    if (captured === null) throw new Error('client definition not captured');

    const hooks: unknown[] = [];
    const setters: Record<number, unknown> = {};
    const memoSlots: unknown[] = [];
    const callbackSlots: unknown[] = [];
    const effectSlots: ({ deps?: unknown[]; cleanup?: unknown } | undefined)[] = [];
    const fakeNode = { scrollTop: 0, clientWidth: 900 };
    let hookIndex = 0;
    let rendering = false;
    let dirty = false;
    let current: unknown = null;
    let componentFn: (react: unknown) => unknown = () => null;

    const react = {
      createElement: (...args: unknown[]) => ({ kind: 'element', args }),
      useState: (initial: unknown): [unknown, (value: unknown) => void] => {
        const slot = hookIndex++;
        if (!(slot in hooks)) hooks[slot] = typeof initial === 'function' ? (initial as () => unknown)() : initial;
        if (setters[slot] === undefined) {
          setters[slot] = (value: unknown) => {
            const next = typeof value === 'function' ? (value as (prev: unknown) => unknown)(hooks[slot]) : value;
            if (Object.is(next, hooks[slot])) return;
            hooks[slot] = next;
            rerender();
          };
        }
        return [hooks[slot], setters[slot] as (value: unknown) => void];
      },
      useRef: (initial: unknown): { current: unknown } => {
        const slot = hookIndex++;
        if (!(slot in hooks)) hooks[slot] = { current: initial };
        return hooks[slot] as { current: unknown };
      },
      useCallback: (fn: unknown): unknown => {
        const slot = hookIndex++;
        if (callbackSlots[slot] === undefined) callbackSlots[slot] = fn;
        return callbackSlots[slot];
      },
      useMemo: (fn: () => unknown, deps?: unknown[]): unknown => {
        const slot = hookIndex++;
        const prev = memoSlots[slot] as { deps?: unknown[]; value: unknown } | undefined;
        const changed =
          prev === undefined ||
          deps === undefined ||
          deps.length !== prev.deps?.length ||
          deps.some((dep, i) => !Object.is(dep, prev.deps?.[i]));
        if (changed) memoSlots[slot] = { deps, value: fn() };
        return (memoSlots[slot] as { value: unknown }).value;
      },
      useEffect: (effect: () => unknown, deps?: unknown[]): void => {
        const slot = hookIndex++;
        const previous = effectSlots[slot];
        const changed =
          previous === undefined ||
          deps === undefined ||
          deps.length !== previous.deps?.length ||
          deps.some((dep, i) => !Object.is(dep, previous.deps?.[i]));
        effectSlots[slot] = { deps, cleanup: previous?.cleanup };
        if (!changed) return;
        if (typeof previous?.cleanup === 'function') (previous.cleanup as () => void)();
        const cleanup = effect();
        effectSlots[slot] = { deps, cleanup };
      },
    };

    const attachRefs = (): void => {
      const visit = (value: unknown): void => {
        if (Array.isArray(value)) {
          value.forEach(visit);
          return;
        }
        if (value === null || typeof value !== 'object' || (value as { kind?: string }).kind !== 'element') return;
        const args = (value as { args: unknown[] }).args;
        const props = args[1] as Record<string, unknown> | null;
        if (typeof props?.['ref'] === 'function') (props['ref'] as (n: unknown) => void)(fakeNode);
        args.slice(2).forEach(visit);
      };
      visit(current);
    };

    const render = (): void => {
      rendering = true;
      hookIndex = 0;
      try {
        current = componentFn(react);
      } finally {
        rendering = false;
      }
      attachRefs();
    };

    const runEffects = (): void => {
      rendering = true;
      try {
        // Effects append to effectSlots while running; a setState inside an
        // effect marks dirty and the pass re-renders, like react's flush.
        for (let pass = 0; pass < 6 && dirty; pass += 1) {
          dirty = false;
          render();
        }
      } finally {
        rendering = false;
      }
    };

    const rerender = (): void => {
      if (rendering) {
        dirty = true;
        return;
      }
      render();
      queueMicrotask(() => runEffects());
    };

    // The panel component must close over THIS runnable react: the cached
    // plugin's factory received the noop fakeReact and drives no state.
    const runnablePlugin = (captured as unknown as Captured).definition.factory((id: string) => {
      if (id === 'react') return react;
      throw new Error('unexpected require: ' + id);
    }) as Record<string, unknown>;
    const { slots, registrations } = slotCollector();
    (runnablePlugin['apply'] as (ctx: { slots: unknown }) => void)({ slots });
    const registered = registrations.find((r) => r.slot === 'main')!.component as (react: unknown) => unknown;
    componentFn = registered;

    const originalFetch = globalThis.fetch;
    globalThis.fetch = (fetchImpl ?? (async () => ({ ok: true, json: async () => body }))) as unknown as typeof fetch;
    render();
    runEffects();
    return {
      tree: () => current,
      node: fakeNode,
      restore: () => {
        globalThis.fetch = originalFetch;
      },
    };
  }

  /** Convert the runnable-react element shape into the Node shape helpers use. */
  function toNode(value: unknown): unknown {
    if (Array.isArray(value)) return value.map(toNode);
    if (value === null || typeof value !== 'object') return value;
    const element = value as { kind?: string; args?: unknown[] };
    if (element.kind === 'element' && Array.isArray(element.args)) {
      const [type, props, ...children] = element.args as [string, Record<string, unknown> | null, ...unknown[]];
      return { type, props, children: children.map(toNode) } as Node;
    }
    return value;
  }

  async function settle(): Promise<void> {
    await new Promise((resolve) => setTimeout(resolve, 10));
  }

  function click(panel: MountedPanel, find: (button: Node) => boolean): void {
    const tree = toNode(panel.tree());
    const button = elements(tree, 'button').find(find);
    if (button === undefined) throw new Error('button not found');
    (button.props?.['onClick'] as () => void)();
  }

  it('preserves the scroll position across the artifact view-mode toggle', async () => {
    const panel = await mountPanel(await fixtureBody());
    await settle();
    // Navigate to my-post's root unit and open its brief.
    click(panel, (button) => textOf(button).startsWith('my-post'));
    await settle();
    click(panel, (button) => button.props?.['data-open-artifact'] === 'brief');
    await settle();
    panel.node.scrollTop = 300;
    click(panel, (button) => button.props?.['data-view-toggle'] === 'semantic');
    await settle();
    const source = toNode(panel.tree()) as Node;
    const highlighted = elements(source, 'div').find((n) => n.props?.['data-yaml-source'] === 'flat');
    expect(highlighted).toBeDefined();
    // The captured offset was restored after the new view rendered.
    expect(panel.node.scrollTop).toBe(300);
    // Toggling back captures the new offset too.
    panel.node.scrollTop = 120;
    click(panel, (button) => button.props?.['data-view-toggle'] === 'source');
    await settle();
    expect(panel.node.scrollTop).toBe(120);
    expect(elements(toNode(panel.tree()) as Node, 'section').some((n) => n.props?.['data-artifact-viewer'] === 'brief')).toBe(true);
    panel.restore();
  });

  it('wires a claims source reference into the open sources entry', async () => {
    const panel = await mountPanel(await fixtureBody());
    await settle();
    click(panel, (button) => textOf(button).startsWith('my-post'));
    await settle();
    click(panel, (button) => button.props?.['data-open-artifact'] === 'claims');
    await settle();
    click(panel, (button) => button.props?.['data-source-ref'] === 'src-note');
    await settle();
    const tree = toNode(panel.tree()) as Node;
    expect(elements(tree, 'section').some((n) => n.props?.['data-sources-view'] === 'true')).toBe(true);
    const entry = elements(tree, 'details').find((n) => n.props?.['data-source-entry'] === 'src-note');
    expect(entry?.props?.['open']).toBe(true);
    expect(textOf(entry)).toContain('author-note');
    panel.restore();
  });

  it('renders large YAML fold bodies lazily and fully after expanding', async () => {
    const body = JSON.parse(JSON.stringify(await fixtureBody())) as SnapshotRouteBody;
    const post = body.snapshot!.works.find((work) => work.id === 'my-post')!;
    post.units.find((unit) => unit.ref === 'my-post')!.artifacts['brief']!.text =
      'big:\n' + Array.from({ length: 139 }, (_, i) => `  k${i}: v${i}`).join('\n');
    const panel = await mountPanel(body);
    await settle();
    click(panel, (button) => textOf(button).startsWith('my-post'));
    await settle();
    click(panel, (button) => button.props?.['data-open-artifact'] === 'brief');
    await settle();
    click(panel, (button) => button.props?.['data-view-toggle'] === 'semantic');
    await settle();
    const folded = toNode(panel.tree()) as Node;
    expect(elements(folded, 'div').some((n) => n.props?.['data-yaml-source'] === 'folded')).toBe(true);
    const big = elements(folded, 'details').find((n) => n.props?.['data-fold'] === 'big:');
    expect(big).toBeDefined();
    expect(big?.props?.['data-fold-open']).toBe('false');
    // A closed fold mounts its summary plus a line-count placeholder only.
    expect(elements(big, 'div').length).toBe(0);
    expect(big !== undefined ? elements(big, 'span').find((n) => n.props?.['data-fold-pending'] === '140') : undefined).toBeDefined();
    (big?.props?.['onToggle'] as (event: { target: { open: boolean } }) => void)({ target: { open: true } });
    await settle();
    const expanded = toNode(panel.tree()) as Node;
    const reopened = elements(expanded, 'details').find((n) => n.props?.['data-fold'] === 'big:');
    expect(reopened?.props?.['data-fold-open']).toBe('true');
    expect(elements(reopened, 'div').length).toBeGreaterThan(100);
    expect(concatText(reopened)).toContain('k138: v138');
    panel.restore();
  });

  function pressKey(panel: { tree: () => unknown }, find: (button: Node) => boolean, key: string): void {
    const tree = toNode(panel.tree()) as Node;
    const button = elements(tree, 'button').find(find);
    if (button === undefined) throw new Error('tree button not found for key ' + key);
    (button.props?.['onKeyDown'] as (event: { key: string; preventDefault: () => void }) => void)({ key, preventDefault: () => {} });
  }

  function unitDetailSection(tree: Node): Node {
    const detail = elements(tree, 'section').find((n) => n.props?.['key'] === 'detail');
    if (detail === undefined) throw new Error('unit detail section not found');
    return detail;
  }

  it('drives the tree with arrow keys alone: selection, detail, collapse, expand', async () => {
    const panel = await mountPanel(await fixtureBody());
    await settle();
    // ↓ from the selected root moves the selection to the first child; the
    // detail area follows without any mouse interaction.
    pressKey(panel, (b) => b.props?.['data-tree-ref'] === 'my-novel', 'ArrowDown');
    await settle();
    let tree = toNode(panel.tree()) as Node;
    expect(textOf(unitDetailSection(tree))).toContain('my-novel/part-1');
    expect(elements(tree, 'button').find((b) => b.props?.['data-tree-ref'] === 'my-novel/part-1')?.props?.['tabIndex']).toBe(0);
    // → descends into the expanded child.
    pressKey(panel, (b) => b.props?.['data-tree-ref'] === 'my-novel/part-1', 'ArrowRight');
    await settle();
    tree = toNode(panel.tree()) as Node;
    expect(textOf(unitDetailSection(tree))).toContain('my-novel/ch-1');
    // ← collapses the expanded chapter; its scenes disappear and ↓ at the end
    // of the visible list keeps the selection.
    pressKey(panel, (b) => b.props?.['data-tree-ref'] === 'my-novel/ch-1', 'ArrowLeft');
    await settle();
    tree = toNode(panel.tree()) as Node;
    expect(elements(tree, 'button').some((b) => b.props?.['data-tree-ref'] === 'my-novel/scene-1')).toBe(false);
    expect(elements(tree, 'button').find((b) => b.props?.['data-tree-ref'] === 'my-novel/ch-1')?.props?.['aria-expanded']).toBe('false');
    pressKey(panel, (b) => b.props?.['data-tree-ref'] === 'my-novel/ch-1', 'ArrowDown');
    await settle();
    tree = toNode(panel.tree()) as Node;
    expect(textOf(unitDetailSection(tree))).toContain('my-novel/ch-1');
    // → on the collapsed chapter expands it again; the scenes return.
    pressKey(panel, (b) => b.props?.['data-tree-ref'] === 'my-novel/ch-1', 'ArrowRight');
    await settle();
    tree = toNode(panel.tree()) as Node;
    expect(elements(tree, 'button').some((b) => b.props?.['data-tree-ref'] === 'my-novel/scene-1')).toBe(true);
    expect(elements(tree, 'button').find((b) => b.props?.['data-tree-ref'] === 'my-novel/ch-1')?.props?.['aria-expanded']).toBe('true');
    panel.restore();
  });

  it('hides the setup section while the host serves no setup route', async () => {
    const urlFetch = (async (input: unknown) => {
      const url = String(input);
      if (url.includes('/dsh-writing-viewer/setup')) return { ok: false, status: 404, json: async () => ({}) };
      return { ok: true, json: async () => await fixtureBody() };
    }) as unknown as typeof fetch;
    const panel = await mountPanel(await fixtureBody(), urlFetch);
    await settle();
    const tree = toNode(panel.tree()) as Node;
    expect(elements(tree, 'section').some((n) => n.props?.['data-setup-section'] === 'true')).toBe(false);
    panel.restore();
  });

  it('probes the setup route, runs the explicit enable action and lands in the enabled state', async () => {
    let enabled = false;
    let posts = 0;
    const idleStatus = { ok: true, enabled: true, busy: false, cli: { ready: false, path: null, version: null, resolved: null, external: null }, skills: { registered: false, names: [] }, lastError: null };
    const enabledStatus = { ok: true, enabled: true, busy: false, cli: { ready: true, path: '/inst/wa-cli', version: 'v0.3.0', resolved: '/inst/wa-cli', external: '/usr/bin/wa-cli' }, skills: { registered: true, names: ['wa-auto'] }, lastError: null };
    const urlFetch = (async (input: unknown, init?: { method?: string }) => {
      const url = String(input);
      if (url.includes('/dsh-writing-viewer/setup')) {
        if ((init?.method ?? 'GET') === 'POST') {
          posts += 1;
          enabled = true;
        }
        return { ok: true, json: async () => (enabled ? enabledStatus : idleStatus) };
      }
      return { ok: true, json: async () => await fixtureBody() };
    }) as unknown as typeof fetch;
    const panel = await mountPanel(await fixtureBody(), urlFetch);
    await settle();
    let tree = toNode(panel.tree()) as Node;
    const section = elements(tree, 'section').find((n) => n.props?.['data-setup-section'] === 'true');
    expect(section).toBeDefined();
    expect(section?.props?.['data-setup-state']).toBe('idle');
    const enable = elements(tree, 'button').find((button) => button.props?.['data-setup-enable'] === 'true');
    expect(enable).toBeDefined();
    (enable?.props?.['onClick'] as () => void)();
    await settle();
    expect(posts).toBe(1);
    tree = toNode(panel.tree()) as Node;
    const done = elements(tree, 'section').find((n) => n.props?.['data-setup-section'] === 'true');
    expect(done?.props?.['data-setup-state']).toBe('enabled');
    expect(textOf(done)).toContain('wa-auto');
    panel.restore();
  });

  it('carries the panel root on setup probes and re-probes when it changes', async () => {
    const setupUrls: string[] = [];
    const urlFetch = (async (input: unknown, init?: { method?: string }) => {
      const url = String(input);
      if (url.includes('/dsh-writing-viewer/setup')) {
        setupUrls.push((init?.method ?? 'GET') + ' ' + url);
        return {
          ok: true,
          json: async () => ({
            ok: true, enabled: true, busy: false,
            cli: { ready: false, path: null, version: null, resolved: null, external: null, pluginInstalled: false },
            skills: { registered: false, names: [] },
            lastError: null,
          }),
        };
      }
      return { ok: true, json: async () => await fixtureBody() };
    }) as unknown as typeof fetch;
    const panel = await mountPanel(await fixtureBody(), urlFetch);
    await settle();
    expect(setupUrls).toEqual(['GET /dsh-writing-viewer/setup']);
    // Switching the panel project re-probes the setup route with the root.
    const input = elements(toNode(panel.tree()) as Node, 'input').find((n) => n.props?.['aria-label'] !== undefined);
    (input?.props?.['onChange'] as (event: { target?: { value?: string } }) => void)({ target: { value: '/home/me/project' } });
    await settle();
    // The switch control is a form submit; drive its onSubmit like a browser.
    const form = elements(toNode(panel.tree()) as Node, 'form').find((n) => n !== undefined);
    (form?.props?.['onSubmit'] as (event: { preventDefault?: () => void }) => void)({ preventDefault: () => {} });
    await settle();
    expect(setupUrls.some((entry) => entry === 'GET /dsh-writing-viewer/setup?root=%2Fhome%2Fme%2Fproject')).toBe(true);
    panel.restore();
  });

  it('shows a readable setup error line and keeps the enable button usable', async () => {
    const failing = (async (input: unknown) => {
      const url = String(input);
      if (url.includes('/dsh-writing-viewer/setup')) {
        return {
          ok: true,
          json: async () => ({
            ok: true,
            enabled: true,
            busy: false,
            cli: { ready: false, path: null, version: null, resolved: null, external: null },
            skills: { registered: false, names: [] },
            lastError: 'SHA256SUMS 中没有记录，拒绝安装',
          }),
        };
      }
      return { ok: true, json: async () => await fixtureBody() };
    }) as unknown as typeof fetch;
    const panel = await mountPanel(await fixtureBody(), failing);
    await settle();
    const tree = toNode(panel.tree()) as Node;
    const section = elements(tree, 'section').find((n) => n.props?.['data-setup-section'] === 'true');
    expect(section?.props?.['data-setup-state']).toBe('error');
    const alert = elements(section, 'p').find((n) => n.props?.['data-setup-error'] === 'true');
    expect(textOf(alert)).toContain('SHA256SUMS');
    const enable = elements(section, 'button').find((button) => button.props?.['data-setup-enable'] === 'true');
    expect(enable?.props?.['disabled']).toBe(false);
    panel.restore();
  });

  it('starts, renders the native review view, detects an ended session, and ends a review through the session routes', async () => {
    const fixture = await fixtureBody();
    const page = await pageFixture();
    const requests: { url: string; method: string; body: string | null }[] = [];
    let sessionLive = true;
    let startedUnit: string | null = null;
    const stub = (async (input: unknown, init?: { method?: string; body?: string }) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      requests.push({ url, method, body: init?.body ?? null });
      if (url === '/dsh-writing-viewer/review-session' && method === 'POST') {
        startedUnit = (JSON.parse(init?.body ?? '{}') as { unit: string }).unit;
        return { ok: true, status: 200, json: async () => ({ ok: true, session: { unit: startedUnit, root: fixture.root, startedAt: 't' + requests.length } }) };
      }
      if (url === '/dsh-writing-viewer/review-session') {
        return { ok: true, status: 200, json: async () => ({ ok: true, session: sessionLive ? { unit: startedUnit, root: fixture.root, startedAt: 't1' } : null }) };
      }
      if (url === '/dsh-writing-viewer/review/page.json') return { ok: true, status: 200, json: async () => page };
      if (url === '/dsh-writing-viewer/review-session/end') return { ok: true, status: 200, json: async () => ({ ok: true, session: null }) };
      if (url.includes('/dsh-writing-viewer/setup')) return { ok: false, status: 404, json: async () => ({}) };
      return { ok: true, status: 200, json: async () => fixture };
    }) as unknown as typeof fetch;
    const panel = await mountPanel(fixture, stub);
    await settle();
    const selected = (elements(toNode(panel.tree()), 'button').find((b) => b.props?.['data-review-start'] !== undefined)?.props?.['data-review-start']) as string;
    expect(selected).toBeTruthy();
    click(panel, (b) => b.props?.['data-review-start'] === selected);
    await settle();
    const start = requests.find((r) => r.url === '/dsh-writing-viewer/review-session' && r.method === 'POST');
    expect(JSON.parse(start!.body!)).toEqual({ unit: selected, root: fixture.root });
    // The view loads page.json through the proxy and re-checks the session.
    expect(requests.some((r) => r.url === '/dsh-writing-viewer/review/page.json' && r.method === 'GET')).toBe(true);
    let tree = toNode(panel.tree()) as Node;
    expect(elements(tree, 'iframe')).toHaveLength(0);
    expect(elements(tree, 'section').some((n) => n.props?.['data-review-view'] === selected)).toBe(true);
    expect(elements(tree, 'p').some((p) => p.props?.['data-review-para'] === 'my-post#1')).toBe(true);
    // A refresh while the session runs keeps the view; once it is gone the
    // panel shows the ended notice with a restart entry.
    click(panel, (b) => b.props?.['data-review-refresh'] === 'true');
    await settle();
    expect(elements(toNode(panel.tree()), 'section').some((n) => n.props?.['data-review-view'] === selected)).toBe(true);
    sessionLive = false;
    click(panel, (b) => b.props?.['data-review-refresh'] === 'true');
    await settle();
    tree = toNode(panel.tree()) as Node;
    expect(elements(tree, 'section').some((n) => n.props?.['data-review-view'] !== undefined)).toBe(false);
    expect(elements(tree, 'p').some((p) => p.props?.['data-review-ended'] === 'true')).toBe(true);
    // Restart, then end: the end route is posted and the snapshot refetched.
    sessionLive = true;
    click(panel, (b) => b.props?.['data-review-start'] === selected);
    await settle();
    const snapshotsBefore = requests.filter((r) => r.url.startsWith('/dsh-writing-viewer/snapshot')).length;
    click(panel, (b) => b.props?.['data-review-end'] === selected);
    await settle();
    expect(requests.some((r) => r.url === '/dsh-writing-viewer/review-session/end' && r.method === 'POST')).toBe(true);
    expect(requests.filter((r) => r.url.startsWith('/dsh-writing-viewer/snapshot')).length).toBe(snapshotsBefore + 1);
    expect(elements(toNode(panel.tree()), 'section').some((n) => n.props?.['data-review-view'] !== undefined)).toBe(false);
    panel.restore();
  });

  it('ends the review view when the session was replaced for another unit (native-session-replaced)', async () => {
    const fixture = await fixtureBody();
    const page = await pageFixture();
    let startedUnit: string | null = null;
    const stub = (async (input: unknown, init?: { method?: string; body?: string }) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/dsh-writing-viewer/review-session' && method === 'POST') {
        startedUnit = (JSON.parse(init?.body ?? '{}') as { unit: string }).unit;
        return { ok: true, status: 200, json: async () => ({ ok: true, session: { unit: startedUnit, root: fixture.root, startedAt: 't1' } }) };
      }
      // A second tab replaced the session for another unit: the summary is
      // non-null but names that unit, not the one this panel is reviewing.
      if (url === '/dsh-writing-viewer/review-session') {
        return { ok: true, status: 200, json: async () => ({ ok: true, session: { unit: 'my-novel/vol-1/scene-9', root: fixture.root, startedAt: 't2' } }) };
      }
      if (url === '/dsh-writing-viewer/review/page.json') return { ok: true, status: 200, json: async () => page };
      if (url === '/dsh-writing-viewer/review-session/end') return { ok: true, status: 200, json: async () => ({ ok: true, session: null }) };
      if (url.includes('/dsh-writing-viewer/setup')) return { ok: false, status: 404, json: async () => ({}) };
      return { ok: true, status: 200, json: async () => fixture };
    }) as unknown as typeof fetch;
    const panel = await mountPanel(fixture, stub);
    await settle();
    click(panel, (b) => b.props?.['data-review-start'] !== undefined);
    await settle();
    expect(startedUnit).toBeTruthy();
    expect(startedUnit).not.toBe('my-novel/vol-1/scene-9');
    // The status check after the data load sees another unit's session.
    const tree = toNode(panel.tree()) as Node;
    expect(elements(tree, 'section').some((n) => n.props?.['data-review-view'] !== undefined)).toBe(false);
    expect(textOf(tree)).not.toContain('scene-9');
    expect(elements(tree, 'p').some((p) => p.props?.['data-review-ended'] === 'true')).toBe(true);
    expect(elements(tree, 'button').some((b) => b.props?.['data-review-start'] !== undefined)).toBe(true);
    panel.restore();
  });

  /** A fetch stub for one live my-post review: the real page.json plus
   * recorded submits answered by `answer(path, body)`. */
  function reviewStub(fixture: SnapshotRouteBody, page: Record<string, unknown>, answer: (path: string, body: Record<string, unknown>) => { status: number; body: unknown }) {
    const requests: { url: string; method: string; body: string | null }[] = [];
    let startedUnit: string | null = null;
    const stub = (async (input: unknown, init?: { method?: string; body?: string }) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      requests.push({ url, method, body: init?.body ?? null });
      if (url === '/dsh-writing-viewer/review-session' && method === 'POST') {
        startedUnit = (JSON.parse(init?.body ?? '{}') as { unit: string }).unit;
        return { ok: true, status: 200, json: async () => ({ ok: true, session: { unit: startedUnit, root: fixture.root, startedAt: 't1' } }) };
      }
      if (url === '/dsh-writing-viewer/review-session') return { ok: true, status: 200, json: async () => ({ ok: true, session: { unit: startedUnit, root: fixture.root, startedAt: 't1' } }) };
      if (url === '/dsh-writing-viewer/review/page.json') return { ok: true, status: 200, json: async () => page };
      if (url.startsWith('/dsh-writing-viewer/review/') && method === 'POST') {
        const reply = answer(url.slice('/dsh-writing-viewer/review/'.length), JSON.parse(init?.body ?? '{}') as Record<string, unknown>);
        return { ok: reply.status === 200, status: reply.status, json: async () => reply.body };
      }
      if (url.includes('/dsh-writing-viewer/setup')) return { ok: false, status: 404, json: async () => ({}) };
      return { ok: true, status: 200, json: async () => fixture };
    }) as unknown as typeof fetch;
    return { stub, requests, started: () => startedUnit };
  }

  function typeInto(panel: MountedPanel, type: string, find: (n: Node) => boolean, value: string): void {
    const node = elements(toNode(panel.tree()), type).find(find);
    if (node === undefined) throw new Error(type + ' not found');
    (node.props?.['onChange'] as (e: unknown) => void)({ target: { value } });
  }

  it('starts the review of a pending work from its marker and confirms a decision through the proxy (discovery-open, native-confirm-decision)', async () => {
    const fixture = await fixtureBody();
    const page = await pageFixture();
    const { stub, requests, started } = reviewStub(fixture, page, () => ({ status: 200, body: { valid: true, confirmation: { kind: 'decision', id: 'dec-1', channel: 'preview' } } }));
    const panel = await mountPanel(fixture, stub);
    await settle();
    click(panel, (b) => b.props?.['data-review-pending-work'] === 'my-post');
    await settle();
    expect(started()).toBe('my-post');
    expect(requests.filter((r) => r.url === '/dsh-writing-viewer/review-session' && r.method === 'POST')).toHaveLength(1);
    // Confirming without the author's words is held back on the client.
    click(panel, (b) => b.props?.['data-review-confirm-action'] === 'confirm');
    await settle();
    expect(requests.some((r) => r.url.endsWith('/confirm'))).toBe(false);
    expect(textOf(toNode(panel.tree()))).toContain("Fill in the summary of the author's words first");
    typeInto(panel, 'select', (n) => n.props?.['data-review-option'] === 'decision::dec-1', 'a');
    typeInto(panel, 'input', (n) => n.props?.['data-review-confirmation'] === 'decision::dec-1', '作者：用甲');
    const loadsBefore = requests.filter((r) => r.url.endsWith('/page.json')).length;
    click(panel, (b) => b.props?.['data-review-confirm-action'] === 'confirm');
    await settle();
    const post = requests.find((r) => r.url === '/dsh-writing-viewer/review/confirm');
    expect(JSON.parse(post!.body!)).toEqual({ kind: 'decision', unit: '', id: 'dec-1', action: 'confirm', option: 'a', answer: '', confirmation: '作者：用甲' });
    expect(textOf(toNode(panel.tree()))).toContain('Author confirmation recorded (channel preview)');
    // A saved submit reloads the data.
    expect(requests.filter((r) => r.url.endsWith('/page.json')).length).toBe(loadsBefore + 1);
    panel.restore();
  });

  it('quotes a single-paragraph selection, refuses a cross-paragraph one, and keeps input when wa-cli refuses (native-feedback, native-selection-invalid, native-submit-failed)', async () => {
    const fixture = await fixtureBody();
    const page = await pageFixture();
    const replies: { status: number; body: unknown }[] = [
      { status: 400, body: { valid: false, diagnostics: [{ code: 'feedback.ambiguous-quote', message: '引文在正文中出现多次' }] } },
      { status: 200, body: { valid: true, feedback: { id: 'fb-2' } } },
    ];
    const { stub, requests } = reviewStub(fixture, page, () => replies.shift()!);
    const win = (globalThis as unknown as { window: Record<string, unknown> }).window;
    const para = (unit: string, index: number) => ({
      nodeType: 3,
      parentElement: { nodeType: 1, closest: () => ({ getAttribute: (name: string) => (name === 'data-review-unit' ? unit : String(index)) }) },
    });
    let selection = { anchorNode: para('my-post', 1), focusNode: para('my-post', 2), toString: () => 'across' };
    win['getSelection'] = () => selection;
    try {
      const panel = await mountPanel(fixture, stub);
      await settle();
      click(panel, (b) => b.props?.['data-review-start'] !== undefined);
      await settle();
      const units = () => elements(toNode(panel.tree()), 'div').find((n) => n.props?.['data-review-units'] === 'true')!;
      (units().props?.['onMouseUp'] as () => void)();
      await settle();
      typeInto(panel, 'textarea', (n) => n.props?.['data-review-feedback-text'] === 'true', '补一个例子');
      click(panel, (b) => b.props?.['data-review-feedback-submit'] === '');
      await settle();
      expect(requests.some((r) => r.url.endsWith('/feedback'))).toBe(false);
      expect(elements(toNode(panel.tree()), 'p').some((p) => p.props?.['data-review-selection-invalid'] === 'true')).toBe(true);
      // The hint shows once, not again as the submit's result line.
      expect(elements(toNode(panel.tree()), 'span').some((n) => n.props?.['data-review-notice'] !== undefined)).toBe(false);
      // The author can always fall back to the whole unit after an invalid selection.
      click(panel, (b) => b.props?.['data-review-clear-quote'] === 'true');
      await settle();
      expect(elements(toNode(panel.tree()), 'p').some((p) => p.props?.['data-review-selection-invalid'] === 'true')).toBe(false);
      // A selection inside one paragraph becomes the quote.
      const quote = (page.units as { paragraphs: { text: string }[] }[])[0]!.paragraphs[0]!.text.slice(0, 6);
      selection = { anchorNode: para('my-post', 1), focusNode: para('my-post', 1), toString: () => quote };
      (units().props?.['onKeyUp'] as () => void)();
      await settle();
      expect(textOf(elements(toNode(panel.tree()), 'div').find((n) => n.props?.['data-review-quote'] === 'set'))).toContain(quote);
      click(panel, (b) => b.props?.['data-review-feedback-submit'] === '');
      await settle();
      const first = requests.filter((r) => r.url === '/dsh-writing-viewer/review/feedback');
      expect(JSON.parse(first[0]!.body!)).toEqual({ unit: 'my-post', quote, text: '补一个例子', kind: 'suggestion', mark: '' });
      // The refusal is shown and the typed text stays.
      let tree = toNode(panel.tree()) as Node;
      expect(textOf(tree)).toContain('引文在正文中出现多次');
      expect(elements(tree, 'textarea')[0]!.props?.['value']).toBe('补一个例子');
      click(panel, (b) => b.props?.['data-review-feedback-submit'] === '');
      await settle();
      tree = toNode(panel.tree()) as Node;
      expect(textOf(tree)).toContain('Feedback saved fb-2');
      expect(elements(tree, 'textarea')[0]!.props?.['value']).toBe('');
      expect(textOf(elements(tree, 'div').find((n) => n.props?.['data-review-quote'] === 'none'))).toContain('whole unit');
      panel.restore();
    } finally {
      delete win['getSelection'];
    }
  });

  it('reviews the ticked author-review items through the proxy and refreshes to the empty notice (native-author-review)', async () => {
    const fixture = await fixtureBody();
    let reviewed = false;
    const page = () => ({
      ...PAGE_STRICT,
      reviews: reviewed ? [] : [{ key: 'acceptance:my-post', label: '验收 my-post' }, { key: 'acceptance:my-post/intro', label: '验收 intro' }],
    });
    const requests: { url: string; method: string; body: string | null }[] = [];
    let startedUnit: string | null = null;
    const stub = (async (input: unknown, init?: { method?: string; body?: string }) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      requests.push({ url, method, body: init?.body ?? null });
      if (url === '/dsh-writing-viewer/review-session' && method === 'POST') {
        startedUnit = (JSON.parse(init?.body ?? '{}') as { unit: string }).unit;
        return { ok: true, status: 200, json: async () => ({ ok: true, session: { unit: startedUnit, root: fixture.root, startedAt: 't1' } }) };
      }
      if (url === '/dsh-writing-viewer/review-session') return { ok: true, status: 200, json: async () => ({ ok: true, session: { unit: startedUnit, root: fixture.root, startedAt: 't1' } }) };
      if (url === '/dsh-writing-viewer/review/page.json') return { ok: true, status: 200, json: async () => page() };
      if (url === '/dsh-writing-viewer/review/author-review') {
        reviewed = true;
        return { ok: true, status: 200, json: async () => ({ valid: true, authorReview: { reviewed: [{ key: 'acceptance:my-post' }], channel: 'preview' } }) };
      }
      if (url.includes('/dsh-writing-viewer/setup')) return { ok: false, status: 404, json: async () => ({}) };
      return { ok: true, status: 200, json: async () => fixture };
    }) as unknown as typeof fetch;
    const panel = await mountPanel(fixture, stub);
    await settle();
    click(panel, (b) => b.props?.['data-review-start'] !== undefined);
    await settle();
    // Untick the second item; only the first is sent.
    const box = elements(toNode(panel.tree()), 'input').find((n) => n.props?.['data-review-key'] === 'acceptance:my-post/intro')!;
    (box.props?.['onChange'] as (e: unknown) => void)({ target: { checked: false } });
    await settle();
    const loadsBefore = requests.filter((r) => r.url.endsWith('/page.json')).length;
    click(panel, (b) => b.props?.['data-review-author-review-submit'] === 'true');
    await settle();
    const post = requests.find((r) => r.url === '/dsh-writing-viewer/review/author-review');
    expect(post?.method).toBe('POST');
    expect(JSON.parse(post!.body!)).toEqual({ keys: ['acceptance:my-post'] });
    expect(requests.filter((r) => r.url.endsWith('/page.json')).length).toBe(loadsBefore + 1);
    const section = elements(toNode(panel.tree()), 'section').find((n) => n.props?.['data-review-author-review'] === 'true');
    expect(textOf(section)).toContain('No confirmations wait for author review');
    panel.restore();
  });

  it('toggles origin coloring in the review view', async () => {
    const fixture = await fixtureBody();
    const page = await pageFixture();
    const { stub } = reviewStub(fixture, page, () => ({ status: 200, body: { valid: true } }));
    const panel = await mountPanel(fixture, stub);
    await settle();
    click(panel, (b) => b.props?.['data-review-start'] !== undefined);
    await settle();
    // The paragraph's wrapper carries the origin color as its left border.
    const border = (): string => {
      const wrapper = elements(toNode(panel.tree()), 'div').find((d) =>
        String((d.props?.['style'] as Record<string, string> | undefined)?.['borderLeft'] ?? '') !== '' &&
        elements(d, 'p').some((p) => p.props?.['data-review-para'] === 'my-post#1'));
      return String((wrapper?.props?.['style'] as Record<string, string>)['borderLeft']);
    };
    const colored = border();
    expect(colored).toContain('--dsw-alias-brand-primary');
    const toggle = elements(toNode(panel.tree()), 'input').find((n) => n.props?.['data-review-origin-colors'] === 'true')!;
    expect(toggle.props?.['checked']).toBe(true);
    (toggle.props?.['onChange'] as () => void)();
    await settle();
    expect(border()).toContain('--dsw-alias-border-l2');
    expect(elements(toNode(panel.tree()), 'input').find((n) => n.props?.['data-review-origin-colors'] === 'true')!.props?.['checked']).toBe(false);
    panel.restore();
  });

  it('ends the review view when a submit finds the session gone', async () => {
    const fixture = await fixtureBody();
    const page = await pageFixture();
    const { stub } = reviewStub(fixture, page, () => ({ status: 409, body: { valid: false, diagnostics: [{ code: 'review.no-session', message: '没有进行中的审阅' }] } }));
    const panel = await mountPanel(fixture, stub);
    await settle();
    click(panel, (b) => b.props?.['data-review-start'] !== undefined);
    await settle();
    click(panel, (b) => b.props?.['data-review-feedback-submit'] === 'good');
    await settle();
    const tree = toNode(panel.tree()) as Node;
    expect(elements(tree, 'section').some((n) => n.props?.['data-review-view'] !== undefined)).toBe(false);
    expect(elements(tree, 'p').some((p) => p.props?.['data-review-ended'] === 'true')).toBe(true);
    panel.restore();
  });

  it('re-checks the session when the window regains focus and shows the ended notice', async () => {
    const fixture = await fixtureBody();
    const page = await pageFixture();
    const win = (globalThis as unknown as { window: Record<string, unknown> }).window;
    const listeners: Record<string, (() => void)[]> = {};
    win['addEventListener'] = (name: string, fn: () => void) => (listeners[name] ??= []).push(fn);
    win['removeEventListener'] = (name: string, fn: () => void) => {
      listeners[name] = (listeners[name] ?? []).filter((f) => f !== fn);
    };
    let live = true;
    let startedUnit: string | null = null;
    const stub = (async (input: unknown, init?: { method?: string; body?: string }) => {
      const url = String(input);
      if (url === '/dsh-writing-viewer/review-session' && init?.method === 'POST') {
        startedUnit = (JSON.parse(init?.body ?? '{}') as { unit: string }).unit;
        return { ok: true, status: 200, json: async () => ({ ok: true, session: { unit: startedUnit, startedAt: 't1' } }) };
      }
      if (url === '/dsh-writing-viewer/review-session') return { ok: true, status: 200, json: async () => ({ ok: true, session: live ? { unit: startedUnit, root: fixture.root, startedAt: 't1' } : null }) };
      if (url === '/dsh-writing-viewer/review/page.json') return { ok: true, status: 200, json: async () => page };
      if (url.includes('/dsh-writing-viewer/setup')) return { ok: false, status: 404, json: async () => ({}) };
      return { ok: true, status: 200, json: async () => fixture };
    }) as unknown as typeof fetch;
    try {
      const panel = await mountPanel(fixture, stub);
      await settle();
      click(panel, (b) => b.props?.['data-review-start'] !== undefined);
      await settle();
      expect(elements(toNode(panel.tree()), 'section').some((n) => n.props?.['data-review-view'] !== undefined)).toBe(true);
      expect(listeners['focus']?.length).toBe(1);
      live = false;
      listeners['focus']![0]!();
      await settle();
      const tree = toNode(panel.tree()) as Node;
      expect(elements(tree, 'section').some((n) => n.props?.['data-review-view'] !== undefined)).toBe(false);
      expect(elements(tree, 'p').some((p) => p.props?.['data-review-ended'] === 'true')).toBe(true);
      // Leaving the active phase removes the listener again.
      expect(listeners['focus']?.length ?? 0).toBe(0);
      panel.restore();
    } finally {
      delete win['addEventListener'];
      delete win['removeEventListener'];
    }
  });

  it('shows the origin refusal when the start route answers 403', async () => {
    const fixture = await fixtureBody();
    const stub = (async (input: unknown, init?: { method?: string }) => {
      const url = String(input);
      if (url === '/dsh-writing-viewer/review-session' && init?.method === 'POST') {
        return { ok: false, status: 403, json: async () => ({ ok: false, error: 'Host gamepc.tail1234.ts.net:13080 is not allowed' }) };
      }
      if (url.includes('/dsh-writing-viewer/setup')) return { ok: false, status: 404, json: async () => ({}) };
      return { ok: true, status: 200, json: async () => fixture };
    }) as unknown as typeof fetch;
    const panel = await mountPanel(fixture, stub);
    await settle();
    click(panel, (b) => b.props?.['data-review-start'] !== undefined);
    await settle();
    const tree = toNode(panel.tree()) as Node;
    expect(elements(tree, 'iframe')).toHaveLength(0);
    const alert = elements(tree, 'p').find((p) => p.props?.['data-review-error'] === 'forbidden');
    expect(textOf(alert)).toContain('allowedHosts');
    expect(textOf(alert)).toContain('gamepc.tail1234.ts.net:13080');
    panel.restore();
  });
});

describe('queue views', () => {
  /** Actions that record what the rendered tree invoked. */
  function recordingActions(): { actions: Record<string, unknown>; calls: string[] } {
    const calls: string[] = [];
    const actions = {
      refresh: () => calls.push('refresh'),
      setPathInput: () => calls.push('setPathInput'),
      switchTo: () => calls.push('switchTo'),
      restoreDefault: () => calls.push('restoreDefault'),
      selectWork: () => calls.push('selectWork'),
      selectUnit: () => calls.push('selectUnit'),
      openArtifactView: (unit: string, name: string) => calls.push(`open:${unit}/${name}`),
      closeArtifactView: () => calls.push('close'),
      toggleViewMode: () => calls.push('toggle-view'),
      showSources: (id: string | null) => calls.push(`sources:${id}`),
      showQueue: (view: 'decisions' | 'reviews') => calls.push(`queue:${view}`),
      backToOverview: () => calls.push('back-overview'),
      toggleSourcesMode: () => calls.push('toggle-sources'),
    };
    return { actions, calls };
  }

  /** Overview-ready state so the summary tiles render. */
  async function overviewState(body: SnapshotRouteBody, extra: Record<string, unknown> = {}): Promise<Record<string, unknown>> {
    const t = await hooks();
    const model = t.overviewModel(body);
    const picked = t.reconcileSelection(body, model, null, null);
    return {
      labels: t.labelsFor('en'),
      loading: false,
      loadError: null,
      body,
      model,
      selectedWork: picked?.work ?? null,
      selectedUnit: picked?.unit ?? null,
      tree: picked?.tree ?? [],
      detail: picked?.detail ?? null,
      stacked: false,
      pathInput: '',
      panelRoot: null,
      view: 'overview',
      openArtifact: null,
      viewMode: 'semantic',
      sourcesMode: 'reading',
      selectedSource: null,
      setup: null,
      setupBusy: false,
      ...extra,
    };
  }

  it('renders the decision and review tiles as navigation buttons', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const { actions, calls } = recordingActions();
    const tree = t.renderPanel(h, await overviewState(body), actions);
    const decisions = elements(tree, 'button').find((button) => button.props?.['data-open-decisions'] === 'true');
    const reviews = elements(tree, 'button').find((button) => button.props?.['data-open-reviews'] === 'true');
    expect(decisions).toBeDefined();
    expect(reviews).toBeDefined();
    (decisions?.props?.['onClick'] as () => void)();
    (reviews?.props?.['onClick'] as () => void)();
    expect(calls).toEqual(['queue:decisions', 'queue:reviews']);
  });

  it('renders the decision queue with every field and the recommendation mark', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const tree = t.renderPanel(h, await overviewState(body, { view: 'decisions' }), actions);
    const view = elements(tree, 'section').find((n) => n.props?.['data-decisions-view'] === 'true');
    expect(view).toBeDefined();
    const text = textOf(view);
    expect(text).toContain('dec-1');
    expect(text).toContain('open');
    expect(text).toContain('事故率下降了 40%');
    expect(text).toContain('允许');
    expect(text).toContain('保持不变');
    expect(text).toContain('Recommended');
    expect(text).toContain('Blocking');
    expect(text).toContain('my-post');
    // The only button in the queue view is the back-to-overview control.
    const buttons = elements(view, 'button');
    expect(buttons).toHaveLength(1);
    expect(textOf(buttons[0])).toContain('Back to overview');
  });

  it('renders the author-review records with every field', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const tree = t.renderPanel(h, await overviewState(body, { view: 'reviews' }), actions);
    const view = elements(tree, 'section').find((n) => n.props?.['data-reviews-view'] === 'true');
    expect(view).toBeDefined();
    const text = textOf(view);
    expect(text).toContain('acceptance');
    expect(text).toContain('my-post');
    expect(text).toContain('preview');
    expect(text).toContain('2026-10-19');
    expect(elements(view, 'button')).toHaveLength(1);
  });

  it('shows an empty hint when decisions.yaml is absent and an alert when it fails to parse', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const missing = { ...body, snapshot: { ...body.snapshot!, decisions: null } } as SnapshotRouteBody;
    const empty = t.renderPanel(h, await overviewState(missing, { view: 'decisions' }), actions);
    const emptyView = elements(empty, 'section').find((n) => n.props?.['data-decisions-view'] === 'true');
    expect(textOf(emptyView)).toContain('decision queue is empty');
    const malformed = { ...body, snapshot: { ...body.snapshot!, decisions: { path: 'decisions.yaml', format: 'yaml' as const, text: 'decisions: [' } } } as SnapshotRouteBody;
    const broken = t.renderPanel(h, await overviewState(malformed, { view: 'decisions' }), actions);
    const brokenView = elements(broken, 'section').find((n) => n.props?.['data-decisions-view'] === 'true');
    const alert = elements(brokenView, 'p').find((n) => n.props?.['data-parse-error'] === 'true' && n.props?.role === 'alert');
    expect(alert).toBeDefined();
    expect(textOf(alert)).toContain('failed to parse');
  });
});

describe('setup section', () => {
  /** A well-formed setup status for the view-model and render tests. */
  function status(overrides: Record<string, unknown> = {}): Record<string, unknown> {
    return {
      ok: true,
      enabled: true,
      busy: false,
      cli: { ready: true, path: '/inst/wa-cli', version: 'v0.3.0', resolved: '/usr/bin/wa-cli', external: '/usr/bin/wa-cli', pluginInstalled: false },
      skills: {
        registered: true,
        names: ['wa-auto', 'wa-init'],
        report: [
          { name: 'wa-auto', winner: 'external', source: 'user-agents', path: '/home/u/.agents/skills/wa-auto/SKILL.md', fallbackReady: true },
          { name: 'wa-init', winner: 'builtin', source: 'dsh-writing-viewer', path: null, fallbackReady: true },
        ],
      },
      lastError: null,
      ...overrides,
    };
  }

  it('parses only well-formed setup statuses', async () => {
    const t = await hooks();
    const parsed = t.parseSetupStatus(status());
    expect(parsed?.cli.path).toBe('/inst/wa-cli');
    expect(parsed?.skills.names).toEqual(['wa-auto', 'wa-init']);
    expect(t.parseSetupStatus({ ok: true })).toBe(null);
    expect(t.parseSetupStatus({ ok: false, error: 'x' })).toBe(null);
    expect(t.parseSetupStatus(null)).toBe(null);
    // A snapshot body must not be mistaken for a setup status.
    expect(t.parseSetupStatus(await fixtureBody())).toBe(null);
  });

  it('derives idle, busy, enabled and error view states', async () => {
    const t = await hooks();
    const labels = t.setupLabelsFor('en');
    const idle = t.setupViewModel(
      { ok: true, enabled: true, busy: false, cli: { ready: false, path: null, version: null, resolved: null, external: null }, skills: { registered: false, names: [] }, lastError: null },
      false,
      labels,
    );
    expect(idle.state).toBe('idle');
    expect(idle.canEnable).toBe(true);
    expect(idle.headline).toContain('Not enabled yet');
    const busy = t.setupViewModel(
      { ok: true, enabled: true, busy: true, cli: { ready: false, path: null, version: null, resolved: null, external: null }, skills: { registered: false, names: [] }, lastError: null },
      false,
      labels,
    );
    expect(busy.state).toBe('busy');
    expect(busy.canEnable).toBe(false);
    const clientBusy = t.setupViewModel(
      { ok: true, enabled: true, busy: false, cli: { ready: false, path: null, version: null, resolved: null, external: null }, skills: { registered: false, names: [] }, lastError: null },
      true,
      labels,
    );
    expect(clientBusy.state).toBe('busy');
    expect(clientBusy.canEnable).toBe(false);
    const enabled = t.setupViewModel(t.parseSetupStatus(status())!, false, labels);
    expect(enabled.state).toBe('enabled');
    expect(enabled.cliLine).toContain('/inst/wa-cli');
    expect(enabled.cliLine).toContain('v0.3.0');
    expect(enabled.resolvedLine).toContain('/usr/bin/wa-cli');
    expect(enabled.externalLine).toContain('/usr/bin/wa-cli');
    expect(enabled.headline).toContain('fallback');
    expect(enabled.skillsLine).toContain('2');
    // When the external binary is NOT what resolves (builtin dir ahead on
    // PATH), the active-external line must stay hidden — a "外部版本生效"
    // claim next to a builtin resolved line would be self-contradictory.
    const builtinAhead = t.setupViewModel(
      t.parseSetupStatus(status({ cli: { ready: true, path: '/inst/wa-cli', version: 'v0.3.0', resolved: '/inst/wa-cli', external: '/usr/bin/wa-cli' } }))!,
      false,
      labels,
    );
    expect(builtinAhead.resolvedLine).toContain('/inst/wa-cli');
    expect(builtinAhead.externalLine).toBe(null);
    // Provenance line reflects the marker verdict; the report renders one
    // row per registered skill (external winner with path, builtin winner).
    const marked = t.setupViewModel(t.parseSetupStatus(status({ cli: { ready: true, path: '/inst/wa-cli', version: 'v0.3.0', resolved: '/usr/bin/wa-cli', external: '/usr/bin/wa-cli', pluginInstalled: true } }))!, false, labels);
    expect(marked.provenanceLine).toBe(labels.pluginInstalled);
    expect(marked.reportRows).not.toBeNull();
    expect(marked.reportRows![0]!.text).toContain('user-agents');
    expect(marked.reportRows![0]!.text).toContain('/home/u/.agents/skills/wa-auto/SKILL.md');
    expect(marked.reportRows![0]!.text).toContain(labels.fallbackReady);
    expect(marked.reportRows![1]!.text).toContain('dsh-writing-viewer');
    expect(t.setupViewModel(t.parseSetupStatus(status())!, false, labels).provenanceLine).toBe(labels.notPluginInstalled);
    const failed = t.setupViewModel(t.parseSetupStatus(status({ cli: { ready: false, path: null, version: null, resolved: null, external: null }, skills: { registered: false, names: [] }, lastError: '校验失败' }))!, false, labels);
    expect(failed.state).toBe('error');
    expect(failed.error).toBe('校验失败');
    expect(failed.canEnable).toBe(true);
  });

  it('renders the section with the explicit enable button and no other write controls', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const tree = t.renderPanel(h, await stateFor(body, { setup: t.parseSetupStatus(status()), setupBusy: false }), actions);
    const section = elements(tree, 'section').find((n) => n.props?.['data-setup-section'] === 'true');
    expect(section).toBeDefined();
    expect(section?.props?.['data-setup-state']).toBe('enabled');
    expect(textOf(section)).toContain('/inst/wa-cli');
    expect(textOf(section)).toContain('/usr/bin/wa-cli');
    const buttons = elements(section, 'button');
    expect(buttons).toHaveLength(1);
    expect(buttons[0]?.props?.['data-setup-enable']).toBe('true');
    // The source report table renders one row per registered skill.
    const report = elements(section, 'details').find((n) => n.props?.['data-setup-report'] === 'true');
    expect(report).toBeDefined();
    const rows = elements(report, 'li');
    expect(rows.some((row) => row.props?.['data-setup-report-row'] === 'wa-auto' && textOf(row).includes('user-agents'))).toBe(true);
    expect(rows.some((row) => row.props?.['data-setup-report-row'] === 'wa-init' && textOf(row).includes('Built-in'))).toBe(true);
    // The provenance line shows the marker verdict.
    expect(elements(section, 'p').some((n) => n.props?.['data-setup-line'] === 'provenance' && textOf(n).includes('not installed'))).toBe(true);
    // Without a status the section is absent — the switch is off.
    const plain = t.renderPanel(h, await stateFor(body), actions);
    expect(elements(plain, 'section').some((n) => n.props?.['data-setup-section'] === 'true')).toBe(false);
  });

  it('hides the source report when the host delivers none (degraded)', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    const noReport = status({ skills: { registered: true, names: ['wa-auto'], report: null } });
    const tree = t.renderPanel(h, await stateFor(body, { setup: t.parseSetupStatus(noReport), setupBusy: false }), actions);
    const section = elements(tree, 'section').find((n) => n.props?.['data-setup-section'] === 'true');
    expect(section).toBeDefined();
    expect(elements(section, 'details').some((n) => n.props?.['data-setup-report'] === 'true')).toBe(false);
  });
});

describe('unit review controls', () => {
  async function reviewState(review: Record<string, unknown>): Promise<Record<string, unknown>> {
    const t = await hooks();
    const body = await fixtureBody();
    const model = t.overviewModel(body);
    const picked = t.reconcileSelection(body, model, 'my-post', 'my-post');
    return stateFor(body, { selectedWork: 'my-post', selectedUnit: 'my-post', tree: picked.tree, detail: picked.detail, review });
  }

  function reviewActions() {
    const calls: string[] = [];
    return {
      calls,
      actions: { ...actions, startReview: (ref: string) => calls.push('start ' + ref), endReview: () => calls.push('end') },
    };
  }

  it('offers a keyboard-activatable Review button in the unit detail (open-unit)', async () => {
    const t = await hooks();
    const { calls, actions: bound } = reviewActions();
    const tree = t.renderPanel(h, await reviewState({ unit: null, phase: 'idle', error: null, forbidden: false, startedAt: null }), bound);
    const button = elements(tree, 'button').find((b) => b.props?.['data-review-start'] === 'my-post');
    expect(button).toBeDefined();
    expect(button!.props?.['type']).toBe('button');
    expect(textOf(button)).toBe('Review');
    (button!.props?.['onClick'] as () => void)();
    expect(calls).toEqual(['start my-post']);
    expect(elements(tree, 'iframe')).toHaveLength(0);
  });

  function activeData(page: Record<string, unknown>, extra: Record<string, unknown> = {}): Record<string, unknown> {
    return {
      page, loading: false, error: null, feedback: { unit: 'my-post', quote: '', text: '', kind: 'suggestion', invalidSelection: false },
      confirmDrafts: {}, unticked: {}, notices: {}, busy: null, originColors: true, ...extra,
    };
  }

  const ACTIVE = { unit: 'my-post', phase: 'active', error: null, forbidden: false, startedAt: 't1' };

  it('renders the review view natively with an End review button while active (native-render)', async () => {
    const t = await hooks();
    const { calls, actions: bound } = reviewActions();
    const page = await pageFixture();
    const tree = t.renderPanel(h, { ...(await reviewState(ACTIVE)), reviewData: activeData(page) }, bound);
    expect(elements(tree, 'iframe')).toHaveLength(0);
    const view = elements(tree, 'section').find((n) => n.props?.['data-review-view'] === 'my-post');
    expect(view).toBeDefined();
    // Paragraphs carry their origin outside the selectable text element.
    const paras = elements(view, 'p').filter((p) => p.props?.['data-review-para'] !== undefined);
    expect(paras.map((p) => p.props?.['data-review-para'])).toEqual(['my-post#1', 'my-post#2', 'my-post#3']);
    expect(textOf(paras[0])).toBe((page.units as { paragraphs: { text: string }[] }[])[0]!.paragraphs[0]!.text);
    expect(elements(view, 'span').filter((n) => n.props?.['data-review-origin'] === 'agent')).toHaveLength(3);
    // The unit-level lint finding and the note are listed.
    expect(elements(view, 'li').some((n) => n.props?.['data-review-finding'] === 'lint')).toBe(true);
    expect(textOf(view)).toContain('lint.inactive-terms');
    // The pending decision with both options, the recommended one marked.
    const confirm = elements(view, 'div').find((n) => n.props?.['data-review-confirm'] === 'decision::dec-1');
    expect(confirm).toBeDefined();
    const options = elements(confirm, 'option');
    expect(options.map((o) => o.props?.['value'])).toEqual(['a', 'b']);
    expect(options.find((o) => o.props?.['data-recommended'] === 'true')?.props?.['value']).toBe('b');
    expect(elements(confirm, 'select')[0]!.props?.['value']).toBe('b');
    expect(elements(confirm, 'input').some((n) => n.props?.['data-review-confirmation'] === 'decision::dec-1')).toBe(true);
    // strict with nothing to review shows the empty line.
    expect(textOf(elements(view, 'section').find((n) => n.props?.['data-review-author-review'] === 'true'))).toContain('No confirmations wait for author review');
    const end = elements(tree, 'button').find((b) => b.props?.['data-review-end'] === 'my-post');
    expect(textOf(end)).toBe('End review');
    (end!.props?.['onClick'] as () => void)();
    expect(calls).toEqual(['end']);
    // The review view takes the detail area; the artifact list steps aside.
    expect(elements(tree, 'button').some((b) => b.props?.['data-open-artifact'] !== undefined)).toBe(false);
  });

  it('renders workspace text as plain text nodes, never as markup (native-text-safety)', async () => {
    const t = await hooks();
    const page = {
      title: 'my-post', strict: false, reviews: [], failures: [], confirms: [],
      units: [{ ref: 'my-post', stage: 'draft', hasDraft: true, notes: [], global: [], paragraphs: [
        { index: 1, line: 1, text: '<script>alert(1)</script><b>粗</b>', origin: 'human', findings: [{ kind: 'review', label: '<i>x</i>', severity: '', message: '<img src=x>', suggestion: '' }] },
      ] }],
    };
    const tree = t.renderPanel(h, { ...(await reviewState(ACTIVE)), reviewData: activeData(page) }, reviewActions().actions);
    const para = elements(tree, 'p').find((p) => p.props?.['data-review-para'] === 'my-post#1');
    expect(para!.children).toEqual(['<script>alert(1)</script><b>粗</b>']);
    for (const type of ['script', 'b', 'i', 'img']) expect(elements(tree, type)).toHaveLength(0);
    walk(tree, (n) => expect(n.props?.['dangerouslySetInnerHTML']).toBeUndefined());
    expect(textOf(tree)).toContain('<img src=x>');
  });

  it('lists recent failures, a strict review queue, and keeps notices and the invalid-selection hint visible', async () => {
    const t = await hooks();
    const calls: string[] = [];
    const bound = {
      ...reviewActions().actions,
      submitAuthorReview: () => calls.push('review'),
      toggleReviewKey: (key: string, checked: boolean) => calls.push(`tick ${key} ${checked}`),
      submitFeedback: (mark: string) => calls.push(`feedback ${mark}`),
      submitConfirm: (key: string, action: string) => calls.push(`confirm ${key} ${action}`),
    };
    const page = {
      title: 'my-post', strict: true,
      reviews: [{ key: 'acceptance:my-post', label: '验收 my-post' }, { key: 'volume:my-novel/part-1', label: '卷末检查点' }],
      failures: [{ at: '2026-10-22T06:47:38Z', command: 'review record', exit: 1 }],
      confirms: [{ kind: 'state', unit: '', id: 'state-1', label: '状态变更提议', detail: '', options: [], recommend: '', needsAnswer: false, canReject: true },
        { kind: 'decision', unit: '', id: 'dec-2', label: '开放问题', detail: '', options: [], recommend: '', needsAnswer: true, canReject: false }],
      units: [{ ref: 'my-post', stage: 'draft', hasDraft: false, notes: [], global: [], paragraphs: [] }],
    };
    const data = activeData(page, {
      unticked: { 'volume:my-novel/part-1': true },
      notices: { feedback: { text: 'Feedback saved fb-1', ok: true }, 'state::state-1': { text: 'wa-cli 拒绝', ok: false } },
      feedback: { unit: 'my-post', quote: '', text: '', kind: 'suggestion', invalidSelection: true },
    });
    const tree = t.renderPanel(h, { ...(await reviewState(ACTIVE)), reviewData: data }, bound);
    expect(textOf(elements(tree, 'section').find((n) => n.props?.['data-review-failures'] === 'true'))).toContain('review record');
    const boxes = elements(tree, 'input').filter((n) => n.props?.['data-review-key'] !== undefined);
    expect(boxes.map((b) => [b.props?.['data-review-key'], b.props?.['checked']])).toEqual([['acceptance:my-post', true], ['volume:my-novel/part-1', false]]);
    (boxes[1]!.props?.['onChange'] as (e: unknown) => void)({ target: { checked: true } });
    (elements(tree, 'button').find((b) => b.props?.['data-review-author-review-submit'] === 'true')!.props?.['onClick'] as () => void)();
    // A state proposal can be rejected; a question needing an answer gets an answer box.
    const state = elements(tree, 'div').find((n) => n.props?.['data-review-confirm'] === 'state::state-1');
    expect(elements(state, 'button').map(textOf)).toEqual(['Confirm', 'Reject']);
    (elements(state, 'button')[1]!.props?.['onClick'] as () => void)();
    expect(textOf(elements(state, 'span').find((n) => n.props?.['data-review-notice'] === 'error'))).toBe('wa-cli 拒绝');
    const question = elements(tree, 'div').find((n) => n.props?.['data-review-confirm'] === 'decision::dec-2');
    expect(elements(question, 'input').some((n) => n.props?.['data-review-answer'] === 'decision::dec-2')).toBe(true);
    expect(elements(question, 'button').map(textOf)).toEqual(['Confirm']);
    expect(elements(tree, 'p').some((p) => p.props?.['data-review-selection-invalid'] === 'true')).toBe(true);
    expect(textOf(tree)).toContain('No draft yet');
    expect(textOf(tree)).toContain('Feedback saved fb-1');
    const quick = elements(tree, 'button').filter((b) => b.props?.['data-review-feedback-submit'] !== undefined);
    expect(quick.map((b) => b.props?.['data-review-feedback-submit'])).toEqual(['', 'stuck', 'good', 'question']);
    (quick[1]!.props?.['onClick'] as () => void)();
    expect(calls).toEqual(['tick volume:my-novel/part-1 true', 'review', 'confirm state::state-1 reject', 'feedback stuck']);
  });

  it('shows loading and a readable data failure inside the review view', async () => {
    const t = await hooks();
    const tree = t.renderPanel(h, { ...(await reviewState(ACTIVE)), reviewData: activeData(null as unknown as Record<string, unknown>, { page: null, loading: true, error: 'HTTP 500' }) }, reviewActions().actions);
    expect(elements(tree, 'span').some((n) => n.props?.['data-review-loading'] === 'true')).toBe(true);
    expect(textOf(elements(tree, 'p').find((p) => p.props?.['data-review-page-error'] === 'true'))).toContain('HTTP 500');
    expect(elements(tree, 'button').find((b) => b.props?.['data-review-refresh'] === 'true')?.props?.['disabled']).toBe(true);
  });

  it('shows the origin-guard refusal with the allowedHosts hint instead of a blank review view (native-origin-rejected)', async () => {
    const t = await hooks();
    const tree = t.renderPanel(h, await reviewState({ unit: 'my-post', phase: 'error', error: 'host not allowed', forbidden: true, startedAt: null }), reviewActions().actions);
    expect(elements(tree, 'iframe')).toHaveLength(0);
    const alert = elements(tree, 'p').find((p) => p.props?.['data-review-error'] === 'forbidden');
    expect(textOf(alert)).toContain('allowedHosts');
    expect(elements(tree, 'button').find((b) => b.props?.['data-review-start'] === 'my-post')).toBeDefined();
  });

  it('marks pending works and units and starts their review on activation (discovery-badge)', async () => {
    const t = await hooks();
    const calls: string[] = [];
    const bound = { ...reviewActions().actions, reviewPending: (ref: string) => calls.push('pending ' + ref) };
    const body = await fixtureBody();
    const model = t.overviewModel(body);
    const picked = t.reconcileSelection(body, model, 'my-novel', 'my-novel');
    const tree = t.renderPanel(h, await stateFor(body, { selectedWork: 'my-novel', selectedUnit: 'my-novel', tree: picked.tree, detail: picked.detail }), bound);
    const cards = elements(tree, 'button').filter((b) => b.props?.['data-review-pending-work'] !== undefined);
    expect(cards.map((b) => [b.props?.['data-review-pending-work'], textOf(b)])).toEqual([['my-novel', 'Pending review · 2'], ['my-post', 'Pending review · 1']]);
    const units = elements(tree, 'button').filter((b) => b.props?.['data-review-pending-unit'] !== undefined);
    expect(units.map((b) => b.props?.['data-review-pending-unit'])).toEqual(['my-novel/scene-1', 'my-novel/scene-2']);
    expect(units[0]!.props?.['type']).toBe('button');
    (cards[1]!.props?.['onClick'] as () => void)();
    (units[1]!.props?.['onClick'] as () => void)();
    expect(calls).toEqual(['pending my-post', 'pending my-novel/scene-2']);
  });

  it('shows no pending markers when the next area is degraded (discovery-degraded)', async () => {
    const t = await hooks();
    const body = await fixtureBody();
    body.degraded = ['next: 缺少字段 pendingConfirmations（待审阅标记），该部分按降级标注呈现'];
    const tree = t.renderPanel(h, await stateFor(body), reviewActions().actions);
    expect(elements(tree, 'button').some((b) => b.props?.['data-review-pending-work'] !== undefined || b.props?.['data-review-pending-unit'] !== undefined)).toBe(false);
    expect(textOf(tree)).toContain('pendingConfirmations');
  });

  it('shows the ended notice with a restart entry (session-gone)', async () => {
    const t = await hooks();
    const tree = t.renderPanel(h, await reviewState({ unit: 'my-post', phase: 'ended', error: null, forbidden: false, startedAt: 't1' }), reviewActions().actions);
    expect(elements(tree, 'iframe')).toHaveLength(0);
    expect(textOf(elements(tree, 'p').find((p) => p.props?.['data-review-ended'] === 'true'))).toContain('ended');
    expect(textOf(elements(tree, 'button').find((b) => b.props?.['data-review-start'] === 'my-post'))).toBe('Review again');
  });
});
