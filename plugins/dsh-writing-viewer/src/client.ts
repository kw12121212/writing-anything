/**
 * Browser half of the dsh-writing-viewer plugin.
 *
 * Hand-written in the lazy-CJS bundle protocol (window.__ModuleLoader__.load
 * with a factory returning cordis-plugin exports), the same zero-dependency
 * stance as @liustack/modsearch's client: no dsh package imports, `react`
 * acquired through the loader's `require`. It contributes exactly two slot
 * entries:
 *
 * - a root-scoped `main` keyed panel (key `dsh-writing-viewer`), which
 *   ctx.layout.selectPanel selects and which renders the minimal read-only
 *   panel: the located project directory, the `.writing-anything/` status,
 *   every candidate probed, and — when nothing was located — the empty-state
 *   guidance to run `wa-cli init` rather than an error;
 * - a `sidebar.panellist` list entry with the same id, so the sidebar owns a
 *   button that selects the panel.
 */
export {};

declare global {
  interface Window {
    __ModuleLoader__?: {
      load(definition: { id: string; factory: (require: (id: string) => unknown) => unknown }): void;
    };
  }
}

const PANEL_ID = 'dsh-writing-viewer';
const PACKAGE_NAME = 'dsh-writing-viewer';
const ROUTE_PATH = '/dsh-writing-viewer/workspace';

interface PanelLabels {
  panelTitle: string;
  subtitle: string;
  loading: string;
  locatedRoot: string;
  locatedSource: string;
  workspaceFound: string;
  workspaceDir: string;
  candidates: string;
  sourceConfig: string;
  sourceRegistry: string;
  sourceCwd: string;
  probedYes: string;
  probedNo: string;
  emptyTitle: string;
  emptyHint: string;
  loadFailed: string;
  retry: string;
}

type LabelSet = PanelLabels;

const TEXT: { zh: LabelSet; en: LabelSet } = {
  zh: {
    panelTitle: '写作工作区',
    subtitle: 'writing-anything 工作区定位（只读面板）',
    loading: '定位中…',
    locatedRoot: '项目目录',
    locatedSource: '来源',
    workspaceFound: '已找到工作区',
    workspaceDir: '工作区目录',
    candidates: '探测记录',
    sourceConfig: '插件配置',
    sourceRegistry: 'DSH 工作区注册表',
    sourceCwd: '宿主进程目录',
    probedYes: '有',
    probedNo: '无',
    emptyTitle: '未找到 writing-anything 工作区',
    emptyHint: '在要查看的项目目录运行下面的命令创建工作区，然后刷新本面板：',
    loadFailed: '读取定位结果失败',
    retry: '重试',
  },
  en: {
    panelTitle: 'Writing Workspace',
    subtitle: 'writing-anything workspace location (read-only panel)',
    loading: 'Locating…',
    locatedRoot: 'Project directory',
    locatedSource: 'Source',
    workspaceFound: 'Workspace found',
    workspaceDir: 'Workspace directory',
    candidates: 'Probes',
    sourceConfig: 'plugin config',
    sourceRegistry: 'DSH workspace registry',
    sourceCwd: 'host process directory',
    probedYes: 'yes',
    probedNo: 'no',
    emptyTitle: 'No writing-anything workspace found',
    emptyHint: 'Create one in the project directory you want to view, then reopen this panel:',
    loadFailed: 'Failed to load the location result',
    retry: 'Retry',
  },
};

function labelsFor(active?: string | null): LabelSet {
  const lang = String(
    active || (typeof document !== 'undefined' && document.documentElement.lang) ||
      (typeof navigator !== 'undefined' && navigator.language) || 'en',
  ).toLowerCase();
  return lang.indexOf('zh') === 0 ? TEXT.zh : TEXT.en;
}

interface CandidateView {
  source: string;
  path: string;
  workspaceExists: boolean;
}

interface RouteBody {
  root: string | null;
  source: string | null;
  workspaceDir: string | null;
  workspaceExists: boolean;
  candidates?: CandidateView[];
  emptyState?: { command: string } | null;
}

interface PanelModel {
  state: 'loading' | 'located' | 'empty' | 'error';
  title: string;
  subtitle: string;
  root: string | null;
  sourceLabel: string | null;
  workspaceDir: string | null;
  candidates: CandidateView[];
  hintCommand: string | null;
  errorText: string | null;
}

function sourceLabel(source: string | null, labels: LabelSet): string | null {
  if (source === 'config') return labels.sourceConfig;
  if (source === 'workspace-registry') return labels.sourceRegistry;
  if (source === 'process-cwd') return labels.sourceCwd;
  return null;
}

function panelModel(labels: LabelSet, data: RouteBody | null, loadError: string | null): PanelModel {
  const base = { title: labels.panelTitle, subtitle: labels.subtitle, candidates: [] as CandidateView[] };
  if (loadError !== null) {
    return { ...base, state: 'error', root: null, sourceLabel: null, workspaceDir: null, hintCommand: null, errorText: labels.loadFailed + ': ' + loadError };
  }
  if (data === null) {
    return { ...base, state: 'loading', root: null, sourceLabel: null, workspaceDir: null, hintCommand: null, errorText: null };
  }
  const candidates = Array.isArray(data.candidates) ? data.candidates : [];
  if (data.root !== null && data.workspaceExists) {
    return {
      ...base,
      state: 'located',
      root: data.root,
      sourceLabel: sourceLabel(data.source, labels),
      workspaceDir: data.workspaceDir,
      candidates,
      hintCommand: null,
      errorText: null,
    };
  }
  return {
    ...base,
    state: 'empty',
    root: null,
    sourceLabel: null,
    workspaceDir: null,
    candidates,
    hintCommand: data.emptyState?.command ?? 'wa-cli init',
    errorText: null,
  };
}

const CARD_STYLE: Record<string, string> = {
  display: 'flex',
  flexDirection: 'column',
  gap: '10px',
  padding: '20px 24px',
  maxWidth: '720px',
  margin: '0 auto',
  color: 'var(--dsw-alias-label-primary, #1f2328)',
  fontSize: '13px',
  lineHeight: '20px',
};

function rowStyle(): Record<string, string> {
  return {
    display: 'flex',
    flexWrap: 'wrap',
    gap: '6px 12px',
    alignItems: 'baseline',
    borderBottom: '1px solid var(--dsw-alias-border-l2, #e5e7eb)',
    paddingBottom: '8px',
  };
}

function monoStyle(): Record<string, string> {
  return {
    fontFamily: 'var(--ds-font-family-code, ui-monospace, Menlo, Consolas, monospace)',
    fontSize: '12px',
    overflowWrap: 'anywhere',
  };
}

window.__ModuleLoader__?.load({
  id: PACKAGE_NAME,
  factory: (require: (id: string) => unknown) => {
    const module = { exports: {} as Record<string, unknown> };
    const exports = module.exports;

    let react: any;
    try {
      react = require('react');
    } catch (error) {
      console.error(`[dsh-writing-viewer] panel skipped: host has no react (${String(error)})`);
      exports.apply = () => {};
      exports.inject = [];
      exports.__test = { panelModel, labelsFor, PANEL_ID, ROUTE_PATH };
      return module.exports;
    }

    function ViewerPanelIcon(): unknown {
      return react.createElement(
        'svg',
        { width: 16, height: 16, viewBox: '0 0 16 16', fill: 'none', 'aria-hidden': 'true' },
        react.createElement('path', {
          d: 'M3 2.5A1.5 1.5 0 0 1 4.5 1H12a1.5 1.5 0 0 1 1.5 1.5v9a.5.5 0 0 1-.5.5H4.5A.5.5 0 0 0 4 12.5v.5a.5.5 0 0 0 .5.5H13a.5.5 0 0 1 0 1H4.5A1.5 1.5 0 0 1 3 12.5v-10Z',
          stroke: 'currentColor',
          'stroke-width': '1',
        }),
        react.createElement('path', { d: 'M5.5 4.5h5M5.5 7h5M5.5 9.5h3', stroke: 'currentColor', 'stroke-width': '1', 'stroke-linecap': 'round' }),
      );
    }

    function ViewerPanel(): unknown {
      const state = react.useState({ data: null as RouteBody | null, error: null as string | null });
      const data = state[0].data;
      const error = state[0].error;
      const setState = state[1];

      const load = react.useCallback(() => {
        setState({ data: null, error: null });
        fetch(ROUTE_PATH)
          .then((response: Response) =>
            response.json().then((body: unknown) => {
              const parsed = body as RouteBody & { error?: string };
              if (!response.ok) throw new Error(String(parsed.error ?? response.status));
              return parsed;
            }),
          )
          .then((body) => setState({ data: body as RouteBody, error: null }))
          .catch((cause: unknown) => setState({ data: null, error: String((cause as Error)?.message ?? cause) }));
      }, [setState]);

      react.useEffect(() => {
        load();
      }, [load]);

      const labels = labelsFor();
      const model = panelModel(labels, data, error);
      const children: unknown[] = [
        react.createElement('h2', { key: 'title', style: { margin: 0, fontSize: '15px' } }, model.title),
        react.createElement('p', { key: 'subtitle', style: { margin: 0, color: 'var(--dsw-alias-label-tertiary, #8b93a1)' } }, model.subtitle),
      ];

      if (model.state === 'loading') {
        children.push(react.createElement('p', { key: 'loading', style: { margin: 0 } }, labels.loading));
      } else if (model.state === 'error') {
        children.push(
          react.createElement('p', { key: 'error', style: { margin: 0, color: 'var(--dsw-alias-state-error-primary, #dc2626)', overflowWrap: 'anywhere' } }, model.errorText ?? labels.loadFailed),
          react.createElement('button', { key: 'retry', type: 'button', onClick: load }, labels.retry),
        );
      } else if (model.state === 'located') {
        children.push(
          react.createElement(
            'div',
            { key: 'root', style: rowStyle() },
            react.createElement('strong', null, model.root === null ? '' : labels.locatedRoot + ':'),
            react.createElement('code', { style: monoStyle() }, model.root ?? ''),
            react.createElement('span', { style: { color: 'var(--dsw-alias-label-tertiary, #8b93a1)' } }, (model.sourceLabel ?? '') + ' · ' + labels.workspaceFound),
          ),
          react.createElement(
            'div',
            { key: 'wsdir', style: rowStyle() },
            react.createElement('span', null, labels.workspaceDir + ':'),
            react.createElement('code', { style: monoStyle() }, model.workspaceDir ?? ''),
          ),
        );
      } else {
        children.push(
          react.createElement('p', { key: 'empty-title', style: { margin: 0, fontWeight: 600 } }, labels.emptyTitle),
          react.createElement('p', { key: 'empty-hint', style: { margin: 0 } }, labels.emptyHint),
          react.createElement('code', { key: 'empty-command', style: { ...monoStyle(), display: 'inline-block', padding: '4px 8px', background: 'var(--dsw-alias-bg-layer-2, #f3f4f6)', borderRadius: '6px' } }, model.hintCommand ?? 'wa-cli init'),
        );
      }

      if (model.candidates.length > 0) {
        const rows: unknown[] = [react.createElement('dt', { key: 'dt', style: { fontWeight: 600, padding: '4px 0' } }, labels.candidates)];
        for (let index = 0; index < model.candidates.length; index += 1) {
          const candidate = model.candidates[index] as CandidateView;
          rows.push(
            react.createElement(
              'dd',
              { key: 'probe-' + index, style: { margin: 0, padding: '2px 0', display: 'flex', flexWrap: 'wrap', gap: '6px 10px', alignItems: 'baseline' } },
              react.createElement('code', { style: monoStyle() }, candidate.path),
              react.createElement('span', { style: { color: 'var(--dsw-alias-label-tertiary, #8b93a1)' } },
                (sourceLabel(candidate.source, labels) ?? candidate.source) + ' · ' + (candidate.workspaceExists ? labels.probedYes : labels.probedNo)),
            ),
          );
        }
        children.push(react.createElement('dl', { key: 'probes', style: { margin: '8px 0 0' } }, rows));
      }

      return react.createElement('div', { 'data-dsh-writing-viewer': 'panel', style: CARD_STYLE }, children);
    }

    function apply(ctx: { slots?: any }): void {
      const slots = ctx?.slots;
      if (slots === null || slots === undefined) {
        console.error('[dsh-writing-viewer] panel skipped: host declares no slots service');
        return;
      }
      slots.inject('main', function* () {
        yield slots.register({ name: 'main', key: PANEL_ID }, ViewerPanel);
      });
      slots.inject('sidebar.panellist', () => {
        slots.register(
          { name: 'sidebar.panellist', id: PANEL_ID, order: 60, label: () => labelsFor().panelTitle },
          ViewerPanelIcon,
        );
      });
    }

    exports.apply = apply;
    exports.inject = ['slots'];
    // Exposed for the package's tests only; not part of the plugin contract.
    exports.__test = { panelModel, labelsFor, PANEL_ID, ROUTE_PATH };
    return module.exports;
  },
});
