import { describe, expect, it } from 'vitest';

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

describe('panelModel', () => {
  async function model(): Promise<(labels: Record<string, string>, data: unknown, loadError: string | null) => Record<string, unknown>> {
    const plugin = await loadClientPlugin();
    return (plugin['__test'] as { panelModel: unknown })['panelModel'] as never;
  }

  it('shows loading before the first answer, and an error with retry after a failed load', async () => {
    const panelModel = await model();
    const labels = { panelTitle: 't', subtitle: 's', loadFailed: 'failed' } as Record<string, string>;
    expect(panelModel(labels, null, null)['state']).toBe('loading');
    const errored = panelModel(labels, null, 'boom');
    expect(errored['state']).toBe('error');
    expect(errored['errorText']).toBe('failed: boom');
  });

  it('reports the located root and its source', async () => {
    const panelModel = await model();
    const labels = { panelTitle: 't', subtitle: 's', sourceRegistry: 'registry' } as Record<string, string>;
    const located = panelModel(
      labels,
      {
        root: '/proj',
        source: 'workspace-registry',
        workspaceDir: '/proj/.writing-anything',
        workspaceExists: true,
        candidates: [{ source: 'workspace-registry', path: '/proj', workspaceExists: true }],
      },
      null,
    );
    expect(located['state']).toBe('located');
    expect(located['root']).toBe('/proj');
    expect(located['sourceLabel']).toBe('registry');
  });

  it('renders the empty state with the wa-cli init hint when nothing was located', async () => {
    const panelModel = await model();
    const labels = { panelTitle: 't', subtitle: 's', emptyHint: 'hint' } as Record<string, string>;
    const empty = panelModel(
      labels,
      { root: null, source: null, workspaceDir: null, workspaceExists: false, candidates: [], emptyState: { command: 'wa-cli init' } },
      null,
    );
    expect(empty['state']).toBe('empty');
    expect(empty['hintCommand']).toBe('wa-cli init');
    expect(empty['errorText']).toBeNull();
  });
});
