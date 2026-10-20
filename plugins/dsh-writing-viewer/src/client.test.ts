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
    expect(plugin['inject']).toEqual(['slots']);
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
  ROUTE_PATH: string;
}

async function hooks(): Promise<TestHooks> {
  return (await loadClientPlugin())['__test'] as TestHooks;
}

const noop = (): void => {};
const actions = { refresh: noop, setPathInput: noop, switchTo: noop, restoreDefault: noop, selectWork: noop, selectUnit: noop };

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
    const allowed = /^(Refresh|Switch|Restore default)$|^(my-novel|my-post)\b|^(my-novel|part-1|ch-1|scene-1|scene-2)\b/;
    for (const label of labels) expect(label, label).toMatch(allowed);
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
});
