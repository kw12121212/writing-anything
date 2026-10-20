/**
 * Browser half of the dsh-writing-viewer plugin.
 *
 * Written in the lazy-CJS bundle protocol (window.__ModuleLoader__.load with
 * a factory returning cordis-plugin exports), the same zero-dependency stance
 * as @liustack/modsearch's client: no dsh package imports, `react` acquired
 * through the loader's `require`. The only bundled imports are the pure view
 * model (./overview) and the yaml parser it uses, which tsdown inlines into
 * the iife — the emitted file stays one plain script that needs no network.
 *
 * It contributes exactly two slot entries:
 *
 * - a root-scoped `main` keyed panel (key `dsh-writing-viewer`), which
 *   ctx.layout.selectPanel selects and which renders the read-only overview
 *   dashboard (Overview Dashboard): work cards, workspace-level counts,
 *   validate diagnostics and degradation notes, and — for the selected work —
 *   the unit tree with the selected unit's detail. A toolbar shows the
 *   project directory being viewed and lets the author switch to another
 *   absolute project path and back to the default;
 * - a `sidebar.panellist` list entry with the same id, so the sidebar owns a
 *   button that selects the panel.
 *
 * The panel never writes: its only controls are refresh, path switching and
 * selection. Author confirmation and review stay in wa-cli's Reading View.
 */
import {
  overviewModel,
  unitDetail,
  unitTree,
  type OverviewModel,
  type ProgressView,
  type TreeNode,
  type UnitDetail,
  type WorkCard,
} from './overview';
import type { SnapshotRouteBody } from './snapshot';

declare global {
  interface Window {
    __ModuleLoader__?: {
      load(definition: { id: string; factory: (require: (id: string) => unknown) => unknown }): void;
    };
  }
}

const PANEL_ID = 'dsh-writing-viewer';
const PACKAGE_NAME = 'dsh-writing-viewer';
const ROUTE_PATH = '/dsh-writing-viewer/snapshot';

/** Panel container width below which the unit tree and detail stack. */
const STACK_CONTAINER_WIDTH = 640;
/** Viewport width below which DSH collapses its sidebar to the 56px rail. */
const STACK_VIEWPORT_WIDTH = 1024;

interface PanelLabels {
  panelTitle: string;
  subtitle: string;
  loading: string;
  refresh: string;
  projectDir: string;
  pathPlaceholder: string;
  switchPath: string;
  restoreDefault: string;
  sourceConfig: string;
  sourceRegistry: string;
  sourceCwd: string;
  sourcePanel: string;
  candidates: string;
  probedYes: string;
  probedNo: string;
  emptyTitle: string;
  emptyHint: string;
  noWorksTitle: string;
  noWorksHint: string;
  loadFailed: string;
  retry: string;
  sources: string;
  pendingDecisions: string;
  pendingReviews: string;
  diagnostics: string;
  unavailable: string;
  degradedTitle: string;
  diagnosticsTitle: string;
  works: string;
  genre: string;
  stage: string;
  progress: string;
  blockers: string;
  units: string;
  unitTree: string;
  unitDetail: string;
  type: string;
  commands: string;
  artifacts: string;
  none: string;
  selectUnit: string;
}

const TEXT: { zh: PanelLabels; en: PanelLabels } = {
  zh: {
    panelTitle: '写作工作区',
    subtitle: 'writing-anything 工作区总览（只读面板）',
    loading: '读取中…',
    refresh: '刷新',
    projectDir: '项目目录',
    pathPlaceholder: '输入项目目录的绝对路径',
    switchPath: '切换',
    restoreDefault: '恢复默认',
    sourceConfig: '插件配置',
    sourceRegistry: 'DSH 工作区注册表',
    sourceCwd: '宿主进程目录',
    sourcePanel: '面板输入',
    candidates: '探测记录',
    probedYes: '有工作区',
    probedNo: '无工作区',
    emptyTitle: '未找到 writing-anything 工作区',
    emptyHint: '在要查看的项目目录运行下面的命令创建工作区，然后刷新本面板：',
    noWorksTitle: '工作区里还没有作品',
    noWorksHint: '用下面的命令创建第一个作品（Work），然后刷新本面板：',
    loadFailed: '读取工作区快照失败',
    retry: '重试',
    sources: '素材',
    pendingDecisions: '待决决策',
    pendingReviews: '待复核',
    diagnostics: '诊断',
    unavailable: '—',
    degradedTitle: '部分数据降级',
    diagnosticsTitle: 'validate 诊断',
    works: '作品',
    genre: '文体',
    stage: '阶段',
    progress: '进度',
    blockers: '阻塞项',
    units: '单元',
    unitTree: '单元树',
    unitDetail: '单元详情',
    type: '类型',
    commands: '下一步命令',
    artifacts: '产物',
    none: '无',
    selectUnit: '在单元树中选择一个单元查看详情',
  },
  en: {
    panelTitle: 'Writing Workspace',
    subtitle: 'writing-anything workspace overview (read-only panel)',
    loading: 'Loading…',
    refresh: 'Refresh',
    projectDir: 'Project directory',
    pathPlaceholder: 'Absolute path of a project directory',
    switchPath: 'Switch',
    restoreDefault: 'Restore default',
    sourceConfig: 'plugin config',
    sourceRegistry: 'DSH workspace registry',
    sourceCwd: 'host process directory',
    sourcePanel: 'panel input',
    candidates: 'Probes',
    probedYes: 'workspace',
    probedNo: 'no workspace',
    emptyTitle: 'No writing-anything workspace found',
    emptyHint: 'Create one in the project directory you want to view, then refresh this panel:',
    noWorksTitle: 'This workspace has no works yet',
    noWorksHint: 'Create the first work with the command below, then refresh this panel:',
    loadFailed: 'Failed to load the workspace snapshot',
    retry: 'Retry',
    sources: 'Sources',
    pendingDecisions: 'Pending decisions',
    pendingReviews: 'Pending reviews',
    diagnostics: 'Diagnostics',
    unavailable: '—',
    degradedTitle: 'Some data is degraded',
    diagnosticsTitle: 'validate diagnostics',
    works: 'Works',
    genre: 'Genre',
    stage: 'Stage',
    progress: 'Progress',
    blockers: 'Blockers',
    units: 'Units',
    unitTree: 'Unit tree',
    unitDetail: 'Unit detail',
    type: 'Type',
    commands: 'Next commands',
    artifacts: 'Artifacts',
    none: 'None',
    selectUnit: 'Select a unit in the tree to see its detail',
  },
};

const STAGE_TITLES: { zh: Record<string, string>; en: Record<string, string> } = {
  zh: { brief: '写作简报', outline: '大纲', draft: '草稿', review: '审稿与验收', revision: '修订', done: '完成' },
  en: { brief: 'Brief', outline: 'Outline', draft: 'Draft', review: 'Review', revision: 'Revision', done: 'Done' },
};

function activeLanguage(active?: string | null): 'zh' | 'en' {
  const lang = String(
    active || (typeof document !== 'undefined' && document.documentElement.lang) ||
      (typeof navigator !== 'undefined' && navigator.language) || 'en',
  ).toLowerCase();
  return lang.indexOf('zh') === 0 ? 'zh' : 'en';
}

function labelsFor(active?: string | null): PanelLabels {
  return TEXT[activeLanguage(active)];
}

function stageTitle(stage: string | null, active?: string | null): string {
  if (stage === null) return '—';
  return STAGE_TITLES[activeLanguage(active)][stage] ?? stage;
}

function sourceLabel(source: string | null, labels: PanelLabels): string | null {
  if (source === 'config') return labels.sourceConfig;
  if (source === 'workspace-registry') return labels.sourceRegistry;
  if (source === 'process-cwd') return labels.sourceCwd;
  if (source === 'panel-input') return labels.sourcePanel;
  return null;
}

/** Snapshot route URL, with the panel's path switch as the `root` parameter. */
function routeUrl(panelRoot: string | null): string {
  return panelRoot === null ? ROUTE_PATH : ROUTE_PATH + '?root=' + encodeURIComponent(panelRoot);
}

function progressText(progress: ProgressView | null): string | null {
  return progress === null ? null : progress.done + '/' + progress.total;
}

/** Everything the panel renders from, gathered by the stateful component. */
interface PanelState {
  labels: PanelLabels;
  loading: boolean;
  loadError: string | null;
  body: SnapshotRouteBody | null;
  model: OverviewModel | null;
  selectedWork: string | null;
  selectedUnit: string | null;
  tree: TreeNode[];
  detail: UnitDetail | null;
  stacked: boolean;
  pathInput: string;
  panelRoot: string | null;
}

/** Event handlers the rendered tree binds to. */
interface PanelActions {
  refresh(): void;
  setPathInput(value: string): void;
  switchTo(path: string): void;
  restoreDefault(): void;
  selectWork(id: string): void;
  selectUnit(ref: string): void;
}

type H = (type: string, props: Record<string, unknown> | null, ...children: unknown[]) => unknown;

const COLOR = {
  primary: 'var(--dsw-alias-label-primary, #1f2328)',
  secondary: 'var(--dsw-alias-label-secondary, #57606a)',
  tertiary: 'var(--dsw-alias-label-tertiary, #8b93a1)',
  border: 'var(--dsw-alias-border-l2, #e5e7eb)',
  card: 'var(--dsw-alias-bg-layer-1, #ffffff)',
  subtle: 'var(--dsw-alias-bg-layer-2, #f3f4f6)',
  selected: 'var(--dsw-alias-interactive-bg-active, #e8eefc)',
  brand: 'var(--dsw-alias-brand-primary, #4d6bfe)',
  error: 'var(--dsw-alias-state-error-primary, #dc2626)',
  warn: 'var(--dsw-alias-state-warn-primary, #d97706)',
  success: 'var(--dsw-alias-state-success-primary, #16a34a)',
};

const MONO = 'var(--ds-font-family-code, ui-monospace, Menlo, Consolas, monospace)';

function buttonStyle(primary = false): Record<string, string> {
  return {
    padding: '3px 10px',
    fontSize: '12px',
    lineHeight: '20px',
    borderRadius: '6px',
    cursor: 'pointer',
    border: '1px solid ' + (primary ? COLOR.brand : COLOR.border),
    background: primary ? COLOR.brand : COLOR.card,
    color: primary ? 'var(--dsw-alias-label-primary-foreground, #ffffff)' : COLOR.primary,
  };
}

function codeStyle(): Record<string, string> {
  return { fontFamily: MONO, fontSize: '12px', overflowWrap: 'anywhere' };
}

function sectionTitle(h: H, key: string, text: string): unknown {
  return h('h3', { key, style: { margin: '4px 0', fontSize: '13px', fontWeight: '600', color: COLOR.secondary } }, text);
}

function renderToolbar(h: H, s: PanelState, a: PanelActions): unknown {
  const labels = s.labels;
  const source = s.body === null ? null : sourceLabel(s.body.source, labels);
  const current = s.body?.root ?? s.panelRoot;
  return h(
    'div',
    { key: 'toolbar', style: { display: 'flex', flexDirection: 'column', gap: '8px', paddingBottom: '10px', borderBottom: '1px solid ' + COLOR.border } },
    h(
      'div',
      { key: 'head', style: { display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '6px 12px' } },
      h('h2', { key: 'title', style: { margin: 0, fontSize: '15px' } }, labels.panelTitle),
      h('span', { key: 'subtitle', style: { color: COLOR.tertiary } }, labels.subtitle),
      h('span', { key: 'spacer', style: { flex: '1 1 auto' } }),
      h('button', { key: 'refresh', type: 'button', style: buttonStyle(), onClick: a.refresh, disabled: s.loading }, labels.refresh),
    ),
    h(
      'div',
      { key: 'current', style: { display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '4px 10px' } },
      h('strong', { key: 'label' }, labels.projectDir + ':'),
      h('code', { key: 'path', style: codeStyle() }, current ?? '—'),
      source === null ? null : h('span', { key: 'source', style: { color: COLOR.tertiary } }, source),
      // Old data stays visible while a refresh or switch is in flight; say so.
      s.loading && s.model !== null ? h('span', { key: 'busy', role: 'status', style: { color: COLOR.brand } }, labels.loading) : null,
    ),
    h(
      'form',
      {
        key: 'switch',
        style: { display: 'flex', flexWrap: 'wrap', gap: '6px' },
        onSubmit: (event: { preventDefault?: () => void }) => {
          event?.preventDefault?.();
          a.switchTo(s.pathInput);
        },
      },
      h('input', {
        key: 'input',
        type: 'text',
        value: s.pathInput,
        placeholder: labels.pathPlaceholder,
        'aria-label': labels.pathPlaceholder,
        spellCheck: false,
        onChange: (event: { target?: { value?: string } }) => a.setPathInput(String(event?.target?.value ?? '')),
        style: { flex: '1 1 240px', minWidth: '0', padding: '3px 8px', fontFamily: MONO, fontSize: '12px', lineHeight: '20px', color: COLOR.primary, background: COLOR.card, border: '1px solid ' + COLOR.border, borderRadius: '6px' },
      }),
      h('button', { key: 'go', type: 'submit', style: buttonStyle(true), disabled: s.pathInput.trim() === '' }, labels.switchPath),
      s.panelRoot === null ? null : h('button', { key: 'reset', type: 'button', style: buttonStyle(), onClick: a.restoreDefault }, labels.restoreDefault),
    ),
  );
}

function renderCandidates(h: H, s: PanelState): unknown {
  const candidates = s.model?.candidates ?? [];
  if (candidates.length === 0) return null;
  const rows = candidates.map((candidate, index) =>
    h(
      'li',
      { key: 'probe-' + index, style: { display: 'flex', flexWrap: 'wrap', gap: '4px 10px', alignItems: 'baseline', padding: '2px 0' } },
      h('code', { key: 'path', style: codeStyle() }, candidate.path),
      h('span', { key: 'meta', style: { color: COLOR.tertiary } },
        (sourceLabel(candidate.source, s.labels) ?? candidate.source) + ' · ' + (candidate.workspaceExists ? s.labels.probedYes : s.labels.probedNo)),
    ),
  );
  return h('div', { key: 'candidates' }, sectionTitle(h, 'title', s.labels.candidates), h('ul', { key: 'list', style: { margin: 0, paddingLeft: '18px' } }, rows));
}

function renderEmpty(h: H, s: PanelState, title: string, hint: string, command: string): unknown[] {
  const out: unknown[] = [h('p', { key: 'empty-title', style: { margin: 0, fontWeight: '600' } }, title)];
  if (s.model?.rootError) {
    out.push(h('p', { key: 'root-error', role: 'alert', style: { margin: 0, color: COLOR.error, overflowWrap: 'anywhere' } }, s.model.rootError));
  }
  out.push(
    h('p', { key: 'empty-hint', style: { margin: 0 } }, hint),
    h('code', { key: 'empty-command', style: { ...codeStyle(), alignSelf: 'flex-start', padding: '4px 8px', background: COLOR.subtle, borderRadius: '6px' } }, command),
    renderCandidates(h, s),
  );
  return out;
}

function countTile(h: H, key: string, label: string, value: number | null, s: PanelState, tone?: string): unknown {
  return h(
    'div',
    { key, style: { display: 'flex', flexDirection: 'column', minWidth: '96px', padding: '8px 12px', background: COLOR.subtle, border: '1px solid ' + COLOR.border, borderRadius: '8px' } },
    h('span', { key: 'label', style: { color: COLOR.tertiary, fontSize: '12px' } }, label),
    h('strong', { key: 'value', style: { fontSize: '18px', lineHeight: '26px', color: value !== null && value > 0 && tone ? tone : COLOR.primary } },
      value === null ? s.labels.unavailable : String(value)),
  );
}

function renderSummary(h: H, s: PanelState): unknown[] {
  const model = s.model as OverviewModel;
  const summary = model.summary;
  const out: unknown[] = [];
  if (summary !== null) {
    out.push(
      h(
        'div',
        { key: 'summary', style: { display: 'flex', flexWrap: 'wrap', gap: '8px' } },
        countTile(h, 'sources', s.labels.sources, summary.sources, s),
        countTile(h, 'decisions', s.labels.pendingDecisions, summary.pendingDecisions, s, COLOR.warn),
        countTile(h, 'reviews', s.labels.pendingReviews, summary.pendingReviews, s, COLOR.warn),
        countTile(h, 'diagnostics', s.labels.diagnostics, summary.diagnostics, s, COLOR.error),
      ),
    );
  }
  if (model.degraded.length > 0) {
    out.push(
      h(
        'details',
        { key: 'degraded', style: { color: COLOR.warn } },
        h('summary', { key: 's', style: { cursor: 'pointer' } }, s.labels.degradedTitle + ' (' + model.degraded.length + ')'),
        h('ul', { key: 'l', style: { margin: '4px 0', paddingLeft: '18px' } }, model.degraded.map((note, i) => h('li', { key: 'd' + i }, note))),
      ),
    );
  }
  if (model.diagnostics.length > 0) {
    const severityText = Object.entries(summary?.diagnosticsBySeverity ?? {}).map(([k, v]) => k + ' ' + v).join(' · ');
    out.push(
      h(
        'details',
        { key: 'diagnostics' },
        h('summary', { key: 's', style: { cursor: 'pointer' } }, s.labels.diagnosticsTitle + ' (' + severityText + ')'),
        h(
          'ul',
          { key: 'l', style: { margin: '4px 0', paddingLeft: '18px', display: 'flex', flexDirection: 'column', gap: '4px' } },
          model.diagnostics.map((d, i) =>
            h(
              'li',
              { key: 'g' + i },
              h('span', { key: 'sev', style: { color: d.severity === 'error' ? COLOR.error : COLOR.warn, fontWeight: '600' } }, d.severity),
              ' ',
              h('code', { key: 'code', style: codeStyle() }, d.code),
              ' ',
              h('span', { key: 'msg' }, d.message),
              d.path === null ? null : h('div', { key: 'path' }, h('code', { style: { ...codeStyle(), color: COLOR.tertiary } }, d.path)),
            ),
          ),
        ),
      ),
    );
  }
  return out;
}

function renderWorkCard(h: H, s: PanelState, a: PanelActions, card: WorkCard): unknown {
  const selected = card.id === s.selectedWork;
  const progress = progressText(card.progress);
  return h(
    'button',
    {
      key: 'work-' + card.id,
      type: 'button',
      'aria-pressed': selected,
      onClick: () => a.selectWork(card.id),
      style: {
        display: 'flex', flexDirection: 'column', gap: '4px', textAlign: 'left', cursor: 'pointer', padding: '10px 12px',
        color: COLOR.primary, background: selected ? COLOR.selected : COLOR.card,
        border: '1px solid ' + (selected ? COLOR.brand : COLOR.border), borderRadius: '8px', font: 'inherit',
      },
    },
    h('strong', { key: 'id', style: { fontSize: '14px', overflowWrap: 'anywhere' } }, card.id),
    h('span', { key: 'meta', style: { color: COLOR.tertiary, fontSize: '12px' } },
      s.labels.genre + ' ' + (card.genre ?? '—') + ' · ' + s.labels.units + ' ' + card.unitCount),
    h('span', { key: 'stage' }, s.labels.stage + ': ' + stageTitle(card.stage), progress === null ? null : ' · ' + s.labels.progress + ' ' + progress),
    h('span', { key: 'blockers', style: { color: card.blockerCount > 0 ? COLOR.error : COLOR.success } },
      s.labels.blockers + ': ' + card.blockerCount),
    card.blockerSummary.length === 0
      ? null
      : h('ul', { key: 'summary', style: { margin: 0, paddingLeft: '16px', color: COLOR.secondary, fontSize: '12px' } },
          card.blockerSummary.map((message, i) => h('li', { key: 'b' + i, style: { overflowWrap: 'anywhere' } }, message))),
  );
}

function renderTree(h: H, s: PanelState, a: PanelActions): unknown {
  return h(
    'nav',
    { key: 'tree', 'aria-label': s.labels.unitTree, style: { flex: s.stacked ? '0 0 auto' : '0 0 260px', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '2px' } },
    sectionTitle(h, 'title', s.labels.unitTree),
    s.tree.map((node) => {
      const selected = node.ref === s.selectedUnit;
      const progress = progressText(node.progress);
      return h(
        'button',
        {
          key: 'node-' + node.ref,
          type: 'button',
          'aria-current': selected ? 'true' : undefined,
          onClick: () => a.selectUnit(node.ref),
          style: {
            display: 'flex', alignItems: 'baseline', gap: '6px', textAlign: 'left', cursor: 'pointer', font: 'inherit',
            padding: '3px 8px', paddingLeft: 8 + node.depth * 16 + 'px', border: 'none', borderRadius: '6px',
            color: COLOR.primary, background: selected ? COLOR.selected : 'transparent',
          },
        },
        h('span', { key: 'label', style: { fontWeight: node.depth === 0 ? '600' : '400', overflowWrap: 'anywhere' } }, node.label),
        node.type === null ? null : h('span', { key: 'type', style: { color: COLOR.tertiary, fontSize: '12px' } }, node.type),
        h('span', { key: 'spacer', style: { flex: '1 1 auto' } }),
        progress === null ? null : h('span', { key: 'progress', title: s.labels.progress, style: { fontSize: '12px', color: COLOR.secondary, fontFamily: MONO } }, progress),
        node.blockerCount === 0 ? null : h('span', { key: 'blockers', title: s.labels.blockers, style: { fontSize: '12px', color: COLOR.error } }, '●' + node.blockerCount),
      );
    }),
  );
}

function renderDetail(h: H, s: PanelState): unknown {
  const d = s.detail;
  const labels = s.labels;
  const children: unknown[] = [sectionTitle(h, 'title', labels.unitDetail)];
  if (d === null) {
    children.push(h('p', { key: 'hint', style: { margin: 0, color: COLOR.tertiary } }, labels.selectUnit));
  } else {
    const progress = progressText(d.progress);
    const row = (key: string, label: string, value: unknown): unknown =>
      h('div', { key, style: { display: 'flex', flexWrap: 'wrap', gap: '4px 10px', alignItems: 'baseline' } }, h('strong', { key: 'l' }, label + ':'), value);
    children.push(
      h('code', { key: 'ref', style: { ...codeStyle(), fontSize: '13px' } }, d.ref),
      row('type', labels.type, d.type ?? '—'),
      row('stage', labels.stage, stageTitle(d.stage) + (d.stage === null ? '' : ' (' + d.stage + ')')),
      progress === null ? null : row('progress', labels.progress, progress),
      sectionTitle(h, 'blockers-title', labels.blockers + ' (' + d.blockers.length + ')'),
      d.blockers.length === 0
        ? h('p', { key: 'no-blockers', style: { margin: 0, color: COLOR.success } }, labels.none)
        : h('ul', { key: 'blockers', style: { margin: 0, paddingLeft: '18px', display: 'flex', flexDirection: 'column', gap: '4px' } },
            d.blockers.map((b, i) =>
              h('li', { key: 'b' + i },
                h('code', { key: 'code', style: { ...codeStyle(), color: COLOR.error } }, b.code), ' ',
                h('span', { key: 'msg', style: { overflowWrap: 'anywhere' } }, b.message)))),
      sectionTitle(h, 'commands-title', labels.commands),
      d.commands.length === 0
        ? h('p', { key: 'no-commands', style: { margin: 0, color: COLOR.tertiary } }, labels.none)
        : h('ul', { key: 'commands', style: { margin: 0, paddingLeft: '18px' } }, d.commands.map((c, i) => h('li', { key: 'c' + i }, h('code', { style: codeStyle() }, c)))),
      sectionTitle(h, 'artifacts-title', labels.artifacts + ' (' + d.artifacts.length + ')'),
      d.artifacts.length === 0
        ? h('p', { key: 'no-artifacts', style: { margin: 0, color: COLOR.tertiary } }, labels.none)
        : h('ul', { key: 'artifacts', style: { margin: 0, paddingLeft: '18px' } },
            d.artifacts.map((f, i) =>
              h('li', { key: 'f' + i },
                h('strong', { key: 'n' }, f.name), ' ',
                h('code', { key: 'p', style: { ...codeStyle(), color: COLOR.tertiary } }, f.path)))),
    );
  }
  return h('section', { key: 'detail', style: { flex: '1 1 auto', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '6px' } }, children);
}

/** Render the whole panel body from gathered state; pure over `h`. */
function renderPanel(h: H, s: PanelState, a: PanelActions): unknown {
  const labels = s.labels;
  const children: unknown[] = [renderToolbar(h, s, a)];
  if (s.loadError !== null) {
    children.push(
      h('p', { key: 'error', role: 'alert', style: { margin: 0, color: COLOR.error, overflowWrap: 'anywhere' } }, labels.loadFailed + ': ' + s.loadError),
      h('button', { key: 'retry', type: 'button', style: { ...buttonStyle(), alignSelf: 'flex-start' }, onClick: a.refresh }, labels.retry),
    );
  } else if (s.model === null) {
    children.push(h('p', { key: 'loading', style: { margin: 0 } }, labels.loading));
  } else if (s.model.state === 'missing') {
    children.push(...renderEmpty(h, s, labels.emptyTitle, labels.emptyHint, s.model.emptyCommand ?? 'wa-cli init'));
  } else {
    children.push(...renderSummary(h, s));
    if (s.model.state === 'no-works') {
      children.push(...renderEmpty(h, s, labels.noWorksTitle, labels.noWorksHint, s.model.emptyCommand ?? 'wa-cli new'));
    } else {
      children.push(
        sectionTitle(h, 'works-title', labels.works + ' (' + s.model.works.length + ')'),
        h('div', { key: 'works', style: { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: '8px' } },
          s.model.works.map((card) => renderWorkCard(h, s, a, card))),
        h('div', {
          key: 'navigation',
          'data-layout': s.stacked ? 'stacked' : 'split',
          style: { display: 'flex', flexDirection: s.stacked ? 'column' : 'row', gap: '16px', paddingTop: '10px', borderTop: '1px solid ' + COLOR.border },
        }, renderTree(h, s, a), renderDetail(h, s)),
      );
    }
  }
  return h(
    'div',
    {
      'data-dsh-writing-viewer': 'panel',
      style: { display: 'flex', flexDirection: 'column', gap: '12px', padding: '16px 20px', maxWidth: '1200px', margin: '0 auto', boxSizing: 'border-box', width: '100%', color: COLOR.primary, fontSize: '13px', lineHeight: '20px' },
    },
    children,
  );
}

/** Whether the tree and detail should stack, from the panel and viewport widths. */
function shouldStack(containerWidth: number | null, viewportWidth: number | null): boolean {
  if (viewportWidth !== null && viewportWidth < STACK_VIEWPORT_WIDTH) return true;
  return containerWidth !== null && containerWidth < STACK_CONTAINER_WIDTH;
}

/**
 * Latest-request gate: each `begin()` returns a check that stays true only
 * until the next `begin()`, so a response for a superseded request is dropped.
 */
function latestGate(): { begin(): () => boolean } {
  let seq = 0;
  return {
    begin() {
      seq += 1;
      const mine = seq;
      return () => mine === seq;
    },
  };
}

/** Pick the selection that survives a reload: keep it when it still exists. */
function reconcileSelection(
  body: SnapshotRouteBody,
  model: OverviewModel,
  selectedWork: string | null,
  selectedUnit: string | null,
): { work: string | null; unit: string | null; tree: TreeNode[]; detail: UnitDetail | null } {
  const work = model.works.some((w) => w.id === selectedWork) ? selectedWork : (model.works[0]?.id ?? null);
  const tree = work === null ? [] : unitTree(body, work);
  const unit = tree.some((n) => n.ref === selectedUnit) ? selectedUnit : (tree[0]?.ref ?? null);
  return { work, unit, tree, detail: unit === null ? null : unitDetail(body, unit) };
}

window.__ModuleLoader__?.load({
  id: PACKAGE_NAME,
  factory: (require: (id: string) => unknown) => {
    const module = { exports: {} as Record<string, unknown> };
    const exports = module.exports;
    const testHooks = { latestGate, overviewModel, reconcileSelection, renderPanel, routeUrl, shouldStack, labelsFor, stageTitle, PANEL_ID, ROUTE_PATH };

    let react: any;
    try {
      react = require('react');
    } catch (error) {
      console.error(`[dsh-writing-viewer] panel skipped: host has no react (${String(error)})`);
      exports.apply = () => {};
      exports.inject = [];
      exports.__test = testHooks;
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
      const [load, setLoad] = react.useState({ loading: true, body: null as SnapshotRouteBody | null, error: null as string | null });
      const [panelRoot, setPanelRoot] = react.useState(null as string | null);
      const [pathInput, setPathInput] = react.useState('');
      const [selection, setSelection] = react.useState({ work: null as string | null, unit: null as string | null });
      const [stacked, setStacked] = react.useState(false);
      const containerRef = react.useRef(null);
      // Only the latest request may land: a slow answer for a path the author
      // already switched away from must not overwrite the newer one.
      const gate = react.useRef(null);
      if (gate.current === null) gate.current = latestGate();

      const fetchSnapshot = react.useCallback((root: string | null) => {
        const current = (gate.current as ReturnType<typeof latestGate>).begin();
        const settle = (next: { loading: boolean; body: SnapshotRouteBody | null; error: string | null }): void => {
          if (current()) setLoad(next);
        };
        setLoad((prev: { body: SnapshotRouteBody | null }) => ({ loading: true, body: prev.body, error: null }));
        fetch(routeUrl(root))
          .then((response: Response) =>
            response.json().then((body: unknown) => {
              const parsed = body as SnapshotRouteBody & { error?: string };
              if (!response.ok || parsed.ok === false) throw new Error(String(parsed.error ?? response.status));
              return parsed;
            }),
          )
          .then((body) => settle({ loading: false, body, error: null }))
          .catch((cause: unknown) => settle({ loading: false, body: null, error: String((cause as Error)?.message ?? cause) }));
      }, [setLoad, gate]);

      react.useEffect(() => {
        fetchSnapshot(panelRoot);
      }, [fetchSnapshot, panelRoot]);

      react.useEffect(() => {
        const element = containerRef.current as HTMLElement | null;
        const measure = (): void => {
          const viewport = typeof window !== 'undefined' && typeof window.innerWidth === 'number' ? window.innerWidth : null;
          setStacked(shouldStack(element?.clientWidth ?? null, viewport));
        };
        measure();
        if (element === null || typeof ResizeObserver === 'undefined') return undefined;
        const observer = new ResizeObserver(measure);
        observer.observe(element);
        return () => observer.disconnect();
      }, [setStacked]);

      const body = load.body as SnapshotRouteBody | null;
      const model = react.useMemo(() => (body === null ? null : overviewModel(body)), [body]);
      const picked = react.useMemo(
        () => (body === null || model === null ? null : reconcileSelection(body, model, selection.work, selection.unit)),
        [body, model, selection.work, selection.unit],
      );

      const state: PanelState = {
        labels: labelsFor(),
        loading: load.loading,
        loadError: load.error,
        body,
        model,
        selectedWork: picked?.work ?? null,
        selectedUnit: picked?.unit ?? null,
        tree: picked?.tree ?? [],
        detail: picked?.detail ?? null,
        stacked,
        pathInput,
        panelRoot,
      };
      const actions: PanelActions = {
        refresh: () => fetchSnapshot(panelRoot),
        setPathInput,
        switchTo: (path: string) => {
          const trimmed = path.trim();
          if (trimmed === '') return;
          setPathInput(trimmed);
          setSelection({ work: null, unit: null });
          // Re-requesting the same path still refreshes it.
          if (trimmed === panelRoot) fetchSnapshot(trimmed);
          else setPanelRoot(trimmed);
        },
        restoreDefault: () => {
          setPathInput('');
          setSelection({ work: null, unit: null });
          setPanelRoot(null);
        },
        selectWork: (id: string) => setSelection({ work: id, unit: null }),
        selectUnit: (ref: string) => setSelection((prev: { work: string | null }) => ({ work: prev.work ?? picked?.work ?? null, unit: ref })),
      };
      return react.createElement('div', { ref: containerRef, style: { width: '100%', height: '100%', overflow: 'auto' } },
        renderPanel(react.createElement, state, actions));
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
    exports.__test = testHooks;
    return module.exports;
  },
});
