/**
 * Browser half of the dsh-writing-viewer plugin.
 *
 * Written in the lazy-CJS bundle protocol (window.__ModuleLoader__.load with
 * a factory returning cordis-plugin exports), the same zero-dependency stance
 * as @liustack/modsearch's client: no dsh package imports, `react` acquired
 * through the loader's `require`. The only bundled imports are the pure view
 * models and helpers (./overview, ./artifactview, ./yamlhighlight,
 * ./markdownview) and the yaml parser they use, which tsdown inlines into
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
 *   the unit detail's artifact (Artifact) entries open the artifact viewer:
 *   YAML artifacts show the semantic card view (Semantic Card View) and the
 *   highlighted source view with one toggle that preserves the scroll
 *   position, draft Markdown shows the rendered reading view and the
 *   highlighted source view, and claim (Claim) source references jump into
 *   the sources list, which shows each source's metadata card and original;
 * - a `sidebar.panellist` list entry with the same id, so the sidebar owns a
 *   button that selects the panel.
 *
 * The panel never writes: its only controls are refresh, path switching,
 * selection and view toggles. Author confirmation and review stay in wa-cli's
 * Reading View.
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
import { artifactTextOf, cachedArtifactView, sourceEntriesOf, type ArtifactView, type CardRow } from './artifactview';
import { foldYaml, highlightYaml, type YamlTokenClass } from './yamlhighlight';
import { treeKeyAction, visibleTreeNodes } from './treekeys';
import { highlightMarkdownSource, renderMarkdown, type MdBlock, type MdTokenClass } from './markdownview';
import { decisionEntries, queueTextOf, reviewEntries } from './queueview';
import { parseSetupStatus, setupLabelsFor, setupViewModel, type SetupStatus } from './setupview';
import type { ArtifactText } from './artifacts';
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
/** The setup route exists only while the host's enableSetup switch is on;
 * a failed probe keeps the whole setup section hidden. */
const SETUP_ROUTE = '/dsh-writing-viewer/setup';

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
  openArtifact: string;
  semanticView: string;
  sourceView: string;
  readingView: string;
  backToDetail: string;
  parseErrorTitle: string;
  noCardsHint: string;
  sourcesView: string;
  backToOverview: string;
  originalDoc: string;
  originalMissing: string;
  sourcesEmpty: string;
  decisionsView: string;
  reviewsView: string;
  question: string;
  options: string;
  recommended: string;
  rationale: string;
  blockingTag: string;
  relatedUnits: string;
  channel: string;
  confirmedAt: string;
  reviewedAt: string;
  decisionsEmpty: string;
  reviewsEmpty: string;
  queueParseErrorTitle: string;
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
    openArtifact: '打开',
    semanticView: '语义卡片',
    sourceView: '源码高亮',
    readingView: '阅读视图',
    backToDetail: '返回单元详情',
    parseErrorTitle: '产物 YAML 解析失败，已显示源码',
    noCardsHint: '没有可卡片化的字段，可切换源码视图查看原文',
    sourcesView: '素材清单',
    backToOverview: '返回总览',
    originalDoc: '原文',
    originalMissing: '未存档原文',
    sourcesEmpty: '工作区还没有登记素材（Source）',
    decisionsView: '决策队列',
    reviewsView: '复核记录',
    question: '问题',
    options: '选项',
    recommended: '推荐',
    rationale: '理由',
    blockingTag: '阻塞',
    relatedUnits: '关联单元',
    channel: '渠道',
    confirmedAt: '确认时间',
    reviewedAt: '复核时间',
    decisionsEmpty: '决策队列为空，或工作区还没有 decisions.yaml',
    reviewsEmpty: '没有复核记录，或工作区还没有 author-reviews.yaml',
    queueParseErrorTitle: '清单 YAML 解析失败',
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
    openArtifact: 'Open',
    semanticView: 'Semantic cards',
    sourceView: 'Source highlight',
    readingView: 'Reading view',
    backToDetail: 'Back to unit detail',
    parseErrorTitle: 'Artifact YAML failed to parse; showing the source',
    noCardsHint: 'No cardable fields; switch to the source view to read the raw text',
    sourcesView: 'Sources',
    backToOverview: 'Back to overview',
    originalDoc: 'Original',
    originalMissing: 'No archived original',
    sourcesEmpty: 'This workspace has no registered sources yet',
    decisionsView: 'Decision queue',
    reviewsView: 'Review records',
    question: 'Question',
    options: 'Options',
    recommended: 'Recommended',
    rationale: 'Rationale',
    blockingTag: 'Blocking',
    relatedUnits: 'Units',
    channel: 'Channel',
    confirmedAt: 'Confirmed at',
    reviewedAt: 'Reviewed at',
    decisionsEmpty: 'The decision queue is empty, or decisions.yaml does not exist yet',
    reviewsEmpty: 'No review records, or author-reviews.yaml does not exist yet',
    queueParseErrorTitle: 'The queue YAML failed to parse',
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

/** Which top-level panel view is showing. */
export type PanelView = 'overview' | 'sources' | 'decisions' | 'reviews';

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
  /** Top-level view: the overview or the sources list. */
  view: PanelView;
  /** The artifact opened from a unit detail list, if any. */
  openArtifact: { unit: string; name: string } | null;
  /** View toggle of the open artifact (reading view for Markdown). */
  viewMode: 'semantic' | 'source';
  /** View toggle for the sources' original documents. */
  sourcesMode: 'reading' | 'source';
  /** Source (Source) id highlighted in the sources view. */
  selectedSource: string | null;
  /** Which large-YAML fold regions the author expanded (lazy bodies). */
  openFolds: Record<string, boolean> | null;
  /** Which tree nodes the author collapsed (keyboard ←/→); empty = full tree. */
  treeCollapsed: Record<string, boolean> | null;
  /** Setup section status (null while the host serves no setup route). */
  setup: SetupStatus | null;
  /** Client-side in-flight flag for the explicit enable action. */
  setupBusy: boolean;
}

/** Event handlers the rendered tree binds to. */
interface PanelActions {
  refresh(): void;
  setPathInput(value: string): void;
  switchTo(path: string): void;
  restoreDefault(): void;
  selectWork(id: string): void;
  selectUnit(ref: string): void;
  openArtifactView(unit: string, name: string): void;
  closeArtifactView(): void;
  toggleViewMode(): void;
  showSources(sourceId: string | null): void;
  showQueue(view: 'decisions' | 'reviews'): void;
  backToOverview(): void;
  toggleSourcesMode(): void;
  toggleFold(key: string, open: boolean): void;
  /** Run the explicit setup enable action (single-flight on the client too). */
  enableSetup(): void;
  /** One key pressed on a tree node: move selection or collapse/expand. */
  treeKey(event: unknown, ref: string): void;
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

/** YAML source lines above this count render folded by top-level key. */
const FOLD_LINE_THRESHOLD = 120;

/** The other view mode: semantic cards ⇄ highlighted source. */
export function nextViewMode(mode: 'semantic' | 'source'): 'semantic' | 'source' {
  return mode === 'semantic' ? 'source' : 'semantic';
}

/** The other original-document mode: rendered reading ⇄ highlighted source. */
export function nextReadingMode(mode: 'reading' | 'source'): 'reading' | 'source' {
  return mode === 'reading' ? 'source' : 'reading';
}

/** Color role per YAML token class. */
const YAML_TOKEN_COLOR: Record<YamlTokenClass, string> = {
  key: COLOR.brand,
  scalar: COLOR.primary,
  structure: COLOR.secondary,
  comment: COLOR.tertiary,
  anchor: COLOR.warn,
  alias: COLOR.success,
  plain: COLOR.primary,
};

/** Color role per Markdown token class. */
const MD_TOKEN_COLOR: Record<MdTokenClass, string> = {
  'md-heading': COLOR.brand,
  'md-quote': COLOR.secondary,
  'md-list': COLOR.brand,
  'md-code': COLOR.success,
  'md-inline-code': COLOR.success,
  'md-emphasis': COLOR.primary,
  'md-table': COLOR.secondary,
  plain: COLOR.primary,
};

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

function renderSummary(h: H, s: PanelState, a: PanelActions): unknown[] {
  const model = s.model as OverviewModel;
  const summary = model.summary;
  const out: unknown[] = [];
  if (summary !== null) {
    // The sources tile opens the sources list view — the jump target the
    // claim cards' source references share. The two queue tiles open the
    // decision queue and the author-review records the counts stand for.
    const tileButton = (key: string, dataAttr: string, onClick: () => void, tile: unknown): unknown =>
      h(
        'button',
        { key, type: 'button', [dataAttr]: 'true', onClick, style: { font: 'inherit', cursor: 'pointer', padding: 0, border: 'none', textAlign: 'left', color: 'inherit', background: 'transparent' } },
        tile,
      );
    out.push(
      h(
        'div',
        { key: 'summary', style: { display: 'flex', flexWrap: 'wrap', gap: '8px' } },
        tileButton('sources-button', 'data-open-sources', () => a.showSources(null), countTile(h, 'sources', s.labels.sources, summary.sources, s)),
        tileButton('decisions-button', 'data-open-decisions', () => a.showQueue('decisions'), countTile(h, 'decisions', s.labels.pendingDecisions, summary.pendingDecisions, s, COLOR.warn)),
        tileButton('reviews-button', 'data-open-reviews', () => a.showQueue('reviews'), countTile(h, 'reviews', s.labels.pendingReviews, summary.pendingReviews, s, COLOR.warn)),
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
  const all: TreeNode[] = s.tree;
  // Keyboard navigation keeps one roving tab stop (the selected node); the
  // arrows move within the tree, Tab leaves it for the next control.
  const visible = visibleTreeNodes(all, s.treeCollapsed ?? {});
  const hasChildren = (ref: string): boolean => {
    const index = all.findIndex((node) => node.ref === ref);
    const node = all[index];
    const next = all[index + 1];
    return node !== undefined && next !== undefined && next.depth > node.depth;
  };
  return h(
    'nav',
    { key: 'tree', 'aria-label': s.labels.unitTree, 'data-tree': 'units', style: { flex: s.stacked ? '0 0 auto' : '0 0 260px', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '2px' } },
    sectionTitle(h, 'title', s.labels.unitTree),
    visible.map((node) => {
      const selected = node.ref === s.selectedUnit;
      const expandable = hasChildren(node.ref);
      const nodeCollapsed = (s.treeCollapsed ?? {})[node.ref] === true;
      const progress = progressText(node.progress);
      return h(
        'button',
        {
          key: 'node-' + node.ref,
          type: 'button',
          'data-tree-ref': node.ref,
          tabIndex: selected ? 0 : -1,
          'aria-current': selected ? 'true' : undefined,
          'aria-expanded': expandable ? (nodeCollapsed ? 'false' : 'true') : undefined,
          onClick: () => a.selectUnit(node.ref),
          onKeyDown: (event: unknown) => a.treeKey(event, node.ref),
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

/** One row (or nested group) of a semantic card. */
function renderCardRows(h: H, s: PanelState, a: PanelActions, rows: CardRow[], keyPrefix: string, depth: number): unknown[] {
  const out: unknown[] = [];
  rows.forEach((row, index) => {
    const key = `${keyPrefix}-${index}`;
    const label = h('strong', { key: key + '-l', style: { color: COLOR.secondary, fontWeight: '500' } }, row.label + ':');
    if (row.type === 'text') {
      out.push(h('div', { key, style: { display: 'flex', flexWrap: 'wrap', gap: '4px 8px', alignItems: 'baseline' } }, label, h('span', { key: key + '-v', style: { overflowWrap: 'anywhere' } }, row.text)));
    } else if (row.type === 'list') {
      out.push(
        h('div', { key, style: { display: 'flex', flexDirection: 'column', gap: '2px' } },
          label,
          h('ul', { key: key + '-v', style: { margin: 0, paddingLeft: '16px' } }, row.items.map((item, i) => h('li', { key: key + '-' + i, style: { overflowWrap: 'anywhere' } }, item)))),
      );
    } else if (row.type === 'sources') {
      // The one interactive element of a card: each source id jumps to the
      // sources view. It only navigates — never edits or confirms.
      out.push(
        h('div', { key, style: { display: 'flex', flexDirection: 'column', gap: '2px' } },
          label,
          h('div', { key: key + '-v', style: { display: 'flex', flexWrap: 'wrap', gap: '4px' } },
            row.ids.map((id, i) =>
              h('button', {
                key: key + '-' + i,
                type: 'button',
                'data-source-ref': id,
                onClick: () => a.showSources(id),
                style: { ...buttonStyle(), padding: '1px 8px' },
              }, id)))),
      );
    } else {
      out.push(
        h('div', { key, style: { display: 'flex', flexDirection: 'column', gap: '2px', paddingLeft: depth === 0 ? '0' : '10px', borderLeft: depth === 0 ? 'none' : '2px solid ' + COLOR.border } },
          label,
          ...renderCardRows(h, s, a, row.rows, key, depth + 1)),
      );
    }
  });
  return out;
}

/** The semantic card view of one artifact. */
function renderSemanticCards(h: H, s: PanelState, a: PanelActions, view: ArtifactView): unknown[] {
  if (view.cards.length === 0) {
    return [h('p', { key: 'no-cards', style: { margin: 0, color: COLOR.tertiary } }, s.labels.noCardsHint)];
  }
  return view.cards.map((card, index) =>
    h(
      'div',
      { key: 'card-' + index, 'data-card': card.id, style: { display: 'flex', flexDirection: 'column', gap: '6px', padding: '8px 12px', background: COLOR.card, border: '1px solid ' + COLOR.border, borderRadius: '8px' } },
      card.title === null ? null : h('strong', { key: 't', style: { overflowWrap: 'anywhere' } }, card.title),
      ...renderCardRows(h, s, a, card.rows, 'card-' + index, 0),
    ),
  );
}

/** One highlighted YAML line as spans. */
function yamlLineNode(h: H, line: { indent: string; tokens: { text: string; cls: YamlTokenClass }[] }, key: string): unknown {
  return h(
    'div',
    { key, style: { whiteSpace: 'pre', minHeight: '18px' } },
    line.indent,
    line.tokens.map((token, i) => h('span', { key: i, 'data-yaml-token': token.cls, style: { color: YAML_TOKEN_COLOR[token.cls] } }, token.text)),
  );
}

/**
 * The YAML source view: highlighted verbatim, folded by top-level key once
 * the document exceeds FOLD_LINE_THRESHOLD lines. Fold bodies render lazily —
 * a closed region mounts only its summary line, so a novel-sized artifact
 * does not mount thousands of line nodes up front.
 */
function renderYamlSource(h: H, s: PanelState, a: PanelActions, text: string, keyPrefix: string): unknown {
  const lineCount = text.split('\n').length;
  if (lineCount <= FOLD_LINE_THRESHOLD) {
    return h(
      'div',
      { key: keyPrefix + '-lines', 'data-yaml-source': 'flat', style: { ...codeStyle(), background: COLOR.subtle, border: '1px solid ' + COLOR.border, borderRadius: '8px', padding: '8px 10px', overflowX: 'auto' } },
      highlightYaml(text).map((line, i) => yamlLineNode(h, line, 'l' + i)),
    );
  }
  return h(
    'div',
    { key: keyPrefix + '-folds', 'data-yaml-source': 'folded', style: { ...codeStyle(), display: 'flex', flexDirection: 'column', gap: '4px' } },
    foldYaml(text).map((fold, i) => {
      const key = `${i}:${fold.header.trim().slice(0, 40)}`;
      const open = s.openFolds?.[key] === true;
      return h(
        'details',
        {
          key: 'f' + i,
          'data-fold': fold.header.trim().slice(0, 40),
          'data-fold-open': open ? 'true' : 'false',
          open: open ? true : undefined,
          onToggle: (event: { target?: { open?: boolean } }) => a.toggleFold(key, event?.target?.open === true),
          style: { background: COLOR.subtle, border: '1px solid ' + COLOR.border, borderRadius: '8px', padding: '4px 10px' },
        },
        h('summary', { key: 's', style: { cursor: 'pointer', color: COLOR.brand, overflowWrap: 'anywhere' } }, fold.header.trim() || '…'),
        open
          ? fold.lines.map((line, j) => yamlLineNode(h, highlightYaml(line)[0] ?? { indent: '', tokens: [] }, 'l' + j))
          : h('span', { key: 'pending', 'data-fold-pending': String(fold.lines.length), style: { color: COLOR.tertiary } }, fold.lines.length + ' lines'),
      );
    }),
  );
}

/** Rendered Markdown blocks; text children stay plain text in createElement. */
function renderMarkdownBlocks(h: H, blocks: MdBlock[], keyPrefix: string): unknown[] {
  return blocks.map((block, index) => {
    const key = `${keyPrefix}-${index}`;
    switch (block.type) {
      case 'heading': {
        const tag = block.level <= 1 ? 'h3' : block.level === 2 ? 'h4' : 'h5';
        return h(tag, { key, 'data-md': 'heading', style: { margin: '6px 0 2px' } }, block.text);
      }
      case 'paragraph':
        return h('p', { key, 'data-md': 'paragraph', style: { margin: '4px 0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' } }, block.text);
      case 'list':
        return h(
          block.ordered ? 'ol' : 'ul',
          { key, 'data-md': 'list', style: { margin: '4px 0', paddingLeft: '20px' } },
          block.items.map((item, i) => h('li', { key: key + '-' + i, style: { overflowWrap: 'anywhere' } }, item)),
        );
      case 'code':
        return h('pre', { key, 'data-md': 'code', style: { ...codeStyle(), margin: '4px 0', padding: '8px 10px', background: COLOR.subtle, border: '1px solid ' + COLOR.border, borderRadius: '8px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' } }, block.text);
      case 'quote':
        return h('blockquote', { key, 'data-md': 'quote', style: { margin: '4px 0', padding: '2px 10px', borderLeft: '3px solid ' + COLOR.border, color: COLOR.secondary } }, renderMarkdownBlocks(h, block.blocks, key));
      case 'table':
        return h(
          'table',
          { key, 'data-md': 'table', style: { borderCollapse: 'collapse', margin: '4px 0', fontSize: '12px' } },
          h('thead', { key: 'h' }, h('tr', { key: 'r' }, block.header.map((cell, i) => h('th', { key: 'c' + i, style: { border: '1px solid ' + COLOR.border, padding: '2px 8px', textAlign: 'left' } }, cell)))),
          h('tbody', { key: 'b' }, block.rows.map((row, r) => h('tr', { key: 'r' + r }, row.map((cell, c) => h('td', { key: 'c' + c, style: { border: '1px solid ' + COLOR.border, padding: '2px 8px' } }, cell))))),
        );
    }
  });
}

/** Highlighted Markdown source lines. */
function renderMarkdownSource(h: H, text: string, keyPrefix: string): unknown {
  return h(
    'div',
    { key: keyPrefix + '-lines', 'data-md-source': 'true', style: { ...codeStyle(), background: COLOR.subtle, border: '1px solid ' + COLOR.border, borderRadius: '8px', padding: '8px 10px', overflowX: 'auto' } },
    highlightMarkdownSource(text).map((line, i) =>
      h('div', { key: 'l' + i, style: { whiteSpace: 'pre', minHeight: '18px' } },
        line.tokens.map((token, j) => h('span', { key: j, 'data-md-token': token.cls, style: { color: MD_TOKEN_COLOR[token.cls], fontStyle: token.cls === 'md-quote' ? 'italic' : 'normal' } }, token.text)))),
  );
}

/**
 * The artifact viewer for one opened artifact: the view toggle in a header,
 * semantic cards or the rendered reading view on one side, highlighted
 * source on the other. A parse failure forces the source view with an alert.
 */
function renderArtifactViewer(h: H, s: PanelState, a: PanelActions, artifact: ArtifactText, path: string): unknown {
  const labels = s.labels;
  const isMarkdown = artifact.format === 'markdown';
  const view = isMarkdown ? null : cachedArtifactView(s.openArtifact?.name ?? '', artifact);
  const parseError = view?.parseError ?? null;
  const mode = parseError !== null ? 'source' : s.viewMode;
  const toggleLabel = isMarkdown ? (mode === 'semantic' ? labels.readingView : labels.sourceView) : mode === 'semantic' ? labels.semanticView : labels.sourceView;
  const body: unknown[] = [];
  if (parseError !== null) {
    body.push(h('p', { key: 'parse-error', role: 'alert', 'data-parse-error': 'true', style: { margin: 0, color: COLOR.error, overflowWrap: 'anywhere' } }, labels.parseErrorTitle + ' — ' + parseError));
  }
  if (isMarkdown) {
    body.push(mode === 'semantic' ? renderMarkdownBlocks(h, renderMarkdown(artifact.text), 'md') : [renderMarkdownSource(h, artifact.text, 'md')]);
  } else if (view !== null) {
    body.push(mode === 'semantic' ? renderSemanticCards(h, s, a, view) : [renderYamlSource(h, s, a, artifact.text, 'yaml')]);
  }
  return h(
    'section',
    { key: 'artifact-viewer', 'data-artifact-viewer': s.openArtifact?.name ?? '', 'aria-label': s.openArtifact?.name ?? '', style: { display: 'flex', flexDirection: 'column', gap: '8px', minWidth: 0 } },
    h(
      'div',
      { key: 'header', style: { display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '4px 10px' } },
      h('strong', { key: 'name' }, s.openArtifact?.name ?? ''),
      h('code', { key: 'path', style: { ...codeStyle(), color: COLOR.tertiary } }, path),
      h('span', { key: 'spacer', style: { flex: '1 1 auto' } }),
      h('button', { key: 'back', type: 'button', style: buttonStyle(), onClick: a.closeArtifactView }, labels.backToDetail),
      h('button', { key: 'toggle', type: 'button', 'data-view-toggle': mode, 'aria-pressed': mode === 'source', disabled: parseError !== null, style: buttonStyle(mode === 'source'), onClick: a.toggleViewMode }, toggleLabel),
    ),
    ...body,
  );
}

/** The sources list: every registered source with its metadata card and original. */
function renderSources(h: H, s: PanelState, a: PanelActions): unknown {
  const entries = s.body === null ? [] : sourceEntriesOf(s.body);
  const children: unknown[] = [
    h(
      'div',
      { key: 'header', style: { display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '4px 10px' } },
      sectionTitle(h, 'title', s.labels.sourcesView + ' (' + entries.length + ')'),
      h('span', { key: 'spacer', style: { flex: '1 1 auto' } }),
      h('button', { key: 'mode', type: 'button', 'data-sources-mode': s.sourcesMode, style: buttonStyle(s.sourcesMode === 'source'), onClick: a.toggleSourcesMode }, s.sourcesMode === 'reading' ? s.labels.readingView : s.labels.sourceView),
      h('button', { key: 'back', type: 'button', style: buttonStyle(), onClick: a.backToOverview }, s.labels.backToOverview),
    ),
  ];
  if (entries.length === 0) {
    children.push(h('p', { key: 'empty', style: { margin: 0, color: COLOR.tertiary } }, s.labels.sourcesEmpty));
    return h('section', { key: 'sources', 'data-sources-view': 'true', style: { display: 'flex', flexDirection: 'column', gap: '8px' } }, children);
  }
  children.push(
    ...entries.map((entry, index) => {
      const selected = entry.id === s.selectedSource;
      const meta = cachedArtifactView('source', entry.sourceYaml);
      const body: unknown[] =
        meta.parseError !== null
          ? [h('p', { key: 'parse-error', role: 'alert', 'data-parse-error': 'true', style: { margin: 0, color: COLOR.error, overflowWrap: 'anywhere' } }, s.labels.parseErrorTitle + ' — ' + meta.parseError)]
          : [...renderSemanticCards(h, s, a, meta)];
      if (entry.original === null) {
        body.push(h('p', { key: 'no-original', style: { margin: 0, color: COLOR.tertiary } }, s.labels.originalMissing));
      } else {
        body.push(
          sectionTitle(h, 'original-title', s.labels.originalDoc),
          s.sourcesMode === 'reading'
            ? renderMarkdownBlocks(h, renderMarkdown(entry.original.text), 'src-' + index)
            : renderMarkdownSource(h, entry.original.text, 'src-' + index),
        );
      }
      return h(
        'details',
        { key: 'src-' + index, 'data-source-entry': entry.id, open: selected ? true : undefined, style: { padding: '6px 12px', background: selected ? COLOR.selected : COLOR.card, border: '1px solid ' + (selected ? COLOR.brand : COLOR.border), borderRadius: '8px' } },
        h('summary', { key: 's', style: { cursor: 'pointer', fontWeight: '600', overflowWrap: 'anywhere' } }, entry.id),
        h('div', { key: 'body', style: { display: 'flex', flexDirection: 'column', gap: '8px', paddingTop: '6px' } }, ...body),
      );
    }),
  );
  return h('section', { key: 'sources', 'data-sources-view': 'true', style: { display: 'flex', flexDirection: 'column', gap: '8px' } }, children);
}

/** The decision queue: one card per recorded decision, all fields read-only. */
function renderDecisions(h: H, s: PanelState, a: PanelActions): unknown {
  const labels = s.labels;
  const texts = s.body === null ? { decisions: null, authorReviews: null } : queueTextOf(s.body);
  const { entries, parseError } = decisionEntries(texts.decisions);
  const children: unknown[] = [
    h(
      'div',
      { key: 'header', style: { display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '4px 10px' } },
      sectionTitle(h, 'title', labels.decisionsView + ' (' + entries.length + ')'),
      h('span', { key: 'spacer', style: { flex: '1 1 auto' } }),
      h('button', { key: 'back', type: 'button', style: buttonStyle(), onClick: a.backToOverview }, labels.backToOverview),
    ),
  ];
  if (parseError !== null) {
    children.push(h('p', { key: 'parse-error', role: 'alert', 'data-parse-error': 'true', style: { margin: 0, color: COLOR.error, overflowWrap: 'anywhere' } }, labels.queueParseErrorTitle + ' — ' + parseError));
    return h('section', { key: 'decisions', 'data-decisions-view': 'true', style: { display: 'flex', flexDirection: 'column', gap: '8px' } }, children);
  }
  if (entries.length === 0) {
    children.push(h('p', { key: 'empty', style: { margin: 0, color: COLOR.tertiary } }, labels.decisionsEmpty));
    return h('section', { key: 'decisions', 'data-decisions-view': 'true', style: { display: 'flex', flexDirection: 'column', gap: '8px' } }, children);
  }
  children.push(
    ...entries.map((decision, index) =>
      h(
        'div',
        { key: 'dec-' + index, 'data-decision': decision.id, style: { display: 'flex', flexDirection: 'column', gap: '4px', padding: '8px 12px', background: COLOR.card, border: '1px solid ' + COLOR.border, borderRadius: '8px' } },
        h('div', { key: 'head', style: { display: 'flex', flexWrap: 'wrap', gap: '4px 8px', alignItems: 'baseline' } },
          h('strong', { key: 'id', style: { overflowWrap: 'anywhere' } }, decision.id),
          decision.status === null ? null : h('code', { key: 'status', style: codeStyle() }, decision.status),
          decision.blocking ? h('span', { key: 'blocking', style: { color: COLOR.warn, fontSize: '12px', border: '1px solid ' + COLOR.warn, borderRadius: '6px', padding: '0 6px' } }, labels.blockingTag) : null),
        decision.question === null ? null : h('p', { key: 'question', style: { margin: 0, overflowWrap: 'anywhere' } }, decision.question),
        decision.options.length === 0 ? null : h(
          'ul',
          { key: 'options', style: { margin: 0, paddingLeft: '18px', display: 'flex', flexDirection: 'column', gap: '2px' } },
          decision.options.map((option, i) =>
            h('li', { key: 'o' + i, 'data-option': option.id, style: { overflowWrap: 'anywhere' } },
              h('code', { key: 'id', style: codeStyle() }, option.id), ' ', option.text,
              option.recommended ? h('span', { key: 'rec', 'data-recommended': 'true', style: { color: COLOR.brand, fontSize: '12px', marginLeft: '6px' } }, '← ' + labels.recommended) : null)),
        ),
        decision.rationale === null ? null : h('p', { key: 'rationale', style: { margin: 0, color: COLOR.secondary, overflowWrap: 'anywhere' } }, labels.rationale + '：' + decision.rationale),
        decision.units.length === 0 ? null : h('p', { key: 'units', style: { margin: 0, color: COLOR.tertiary } }, labels.relatedUnits + '：' + decision.units.join('、')),
      ),
    ),
  );
  return h('section', { key: 'decisions', 'data-decisions-view': 'true', style: { display: 'flex', flexDirection: 'column', gap: '8px' } }, children);
}

/** The author-review records: one row card per recorded review. */
function renderReviews(h: H, s: PanelState, a: PanelActions): unknown {
  const labels = s.labels;
  const texts = s.body === null ? { decisions: null, authorReviews: null } : queueTextOf(s.body);
  const { entries, parseError } = reviewEntries(texts.authorReviews);
  const children: unknown[] = [
    h(
      'div',
      { key: 'header', style: { display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '4px 10px' } },
      sectionTitle(h, 'title', labels.reviewsView + ' (' + entries.length + ')'),
      h('span', { key: 'spacer', style: { flex: '1 1 auto' } }),
      h('button', { key: 'back', type: 'button', style: buttonStyle(), onClick: a.backToOverview }, labels.backToOverview),
    ),
  ];
  if (parseError !== null) {
    children.push(h('p', { key: 'parse-error', role: 'alert', 'data-parse-error': 'true', style: { margin: 0, color: COLOR.error, overflowWrap: 'anywhere' } }, labels.queueParseErrorTitle + ' — ' + parseError));
    return h('section', { key: 'reviews', 'data-reviews-view': 'true', style: { display: 'flex', flexDirection: 'column', gap: '8px' } }, children);
  }
  if (entries.length === 0) {
    children.push(h('p', { key: 'empty', style: { margin: 0, color: COLOR.tertiary } }, labels.reviewsEmpty));
    return h('section', { key: 'reviews', 'data-reviews-view': 'true', style: { display: 'flex', flexDirection: 'column', gap: '8px' } }, children);
  }
  const row = (key: string, label: string, value: string | null): unknown =>
    value === null ? null : h('div', { key, style: { display: 'flex', flexWrap: 'wrap', gap: '4px 10px', alignItems: 'baseline' } }, h('strong', { key: 'l' }, label + ':'), h('span', { key: 'v', style: { overflowWrap: 'anywhere' } }, value));
  children.push(
    ...entries.map((review, index) =>
      h(
        'div',
        { key: 'rev-' + index, 'data-review': review.ref ?? review.work ?? String(index), style: { display: 'flex', flexDirection: 'column', gap: '2px', padding: '8px 12px', background: COLOR.card, border: '1px solid ' + COLOR.border, borderRadius: '8px' } },
        row('kind', labels.type, review.kind),
        row('ref', 'ref', review.ref),
        row('work', labels.works, review.work),
        row('channel', labels.channel, review.channel),
        row('confirmed', labels.confirmedAt, review.confirmedAt),
        row('reviewed', labels.reviewedAt, review.reviewedAt),
      ),
    ),
  );
  return h('section', { key: 'reviews', 'data-reviews-view': 'true', style: { display: 'flex', flexDirection: 'column', gap: '8px' } }, children);
}

/** The opt-in setup section: status lines plus the one explicit enable
 * button. Rendered only when the host answered the setup probe. */
function renderSetupSection(h: H, s: PanelState, a: PanelActions): unknown {
  // Tolerant of tests and older states assembled without the field.
  if (s.setup === null || s.setup === undefined) return null;
  const labels = setupLabelsFor();
  const model = setupViewModel(s.setup, s.setupBusy, labels);
  const line = (key: string, text: string, tone?: string): unknown =>
    h('p', { key, 'data-setup-line': key, style: { margin: 0, color: tone ?? COLOR.secondary, overflowWrap: 'anywhere' } }, text);
  return h(
    'section',
    { key: 'setup', 'data-setup-section': 'true', 'data-setup-state': model.state, style: { display: 'flex', flexDirection: 'column', gap: '4px', padding: '8px 12px', background: COLOR.card, border: '1px solid ' + COLOR.border, borderRadius: '8px' } },
    sectionTitle(h, 'title', labels.title),
    h('p', { key: 'headline', 'data-setup-headline': model.state, style: { margin: 0, fontWeight: '600' } }, model.headline),
    line('cli', model.cliLine),
    model.resolvedLine === null ? null : line('resolved', model.resolvedLine),
    model.externalLine === null ? null : line('external', model.externalLine, COLOR.warn),
    line('skills', model.skillsLine),
    model.skillNames.length === 0
      ? null
      : h(
          'details',
          { key: 'skill-names', style: { color: COLOR.tertiary } },
          h('summary', { key: 's', style: { cursor: 'pointer' } }, String(model.skillNames.length)),
          h('ul', { key: 'l', style: { margin: '2px 0', paddingLeft: '18px' } }, model.skillNames.map((name, i) => h('li', { key: 'n' + i, 'data-setup-skill': name }, name))),
        ),
    model.error === null
      ? null
      : h('p', { key: 'error', role: 'alert', 'data-setup-error': 'true', style: { margin: 0, color: COLOR.error, overflowWrap: 'anywhere' } }, labels.failed + '：' + model.error),
    h(
      'button',
      { key: 'enable', type: 'button', 'data-setup-enable': 'true', disabled: !model.canEnable, style: { ...buttonStyle(true), alignSelf: 'flex-start' }, onClick: a.enableSetup },
      labels.enable,
    ),
  );
}

function renderDetail(h: H, s: PanelState, a: PanelActions): unknown {
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
        : h('ul', { key: 'artifacts', style: { margin: 0, paddingLeft: '0', listStyle: 'none', display: 'flex', flexDirection: 'column', gap: '2px' } },
            d.artifacts.map((f, i) =>
              h('li', { key: 'f' + i },
                h('button', {
                  key: 'open',
                  type: 'button',
                  'data-open-artifact': f.name,
                  onClick: () => a.openArtifactView(d.ref, f.name),
                  style: { display: 'flex', flexWrap: 'wrap', gap: '6px', alignItems: 'baseline', cursor: 'pointer', font: 'inherit', textAlign: 'left', padding: '2px 8px', border: 'none', borderRadius: '6px', color: COLOR.primary, background: 'transparent' },
                },
                  h('strong', { key: 'n' }, f.name), ' ',
                  h('code', { key: 'p', style: { ...codeStyle(), color: COLOR.tertiary } }, f.path),
                  h('span', { key: 'go', style: { color: COLOR.brand } }, labels.openArtifact))))),
    );
  }
  return h('section', { key: 'detail', style: { flex: '1 1 auto', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '6px' } }, children);
}

/** Render the whole panel body from gathered state; pure over `h`. */
function renderPanel(h: H, s: PanelState, a: PanelActions): unknown {
  const labels = s.labels;
  const children: unknown[] = [renderToolbar(h, s, a), renderSetupSection(h, s, a)];
  if (s.loadError !== null) {
    children.push(
      h('p', { key: 'error', role: 'alert', style: { margin: 0, color: COLOR.error, overflowWrap: 'anywhere' } }, labels.loadFailed + ': ' + s.loadError),
      h('button', { key: 'retry', type: 'button', style: { ...buttonStyle(), alignSelf: 'flex-start' }, onClick: a.refresh }, labels.retry),
    );
  } else if (s.model === null) {
    // First load (or a reload whose only answer failed): no data to keep on
    // screen, so the loading hint itself is the announced status region —
    // readable, not blank, and distinct from the empty and error states.
    children.push(h('p', { key: 'loading', role: 'status', 'aria-busy': 'true', 'data-loading': 'first', style: { margin: 0 } }, labels.loading));
  } else if (s.model.state === 'missing') {
    children.push(...renderEmpty(h, s, labels.emptyTitle, labels.emptyHint, s.model.emptyCommand ?? 'wa-cli init'));
  } else {
    children.push(...renderSummary(h, s, a));
    const openArtifact = s.openArtifact ?? null;
    if ((s.view ?? 'overview') === 'sources') {
      children.push(renderSources(h, { ...s, sourcesMode: s.sourcesMode ?? 'reading', selectedSource: s.selectedSource ?? null }, a));
    } else if ((s.view ?? 'overview') === 'decisions') {
      children.push(renderDecisions(h, s, a));
    } else if ((s.view ?? 'overview') === 'reviews') {
      children.push(renderReviews(h, s, a));
    } else if (s.model.state === 'no-works') {
      children.push(...renderEmpty(h, s, labels.noWorksTitle, labels.noWorksHint, s.model.emptyCommand ?? 'wa-cli new'));
    } else {
      const opened =
        openArtifact !== null && s.body !== null && openArtifact.unit === s.selectedUnit
          ? artifactTextOf(s.body, openArtifact.unit, openArtifact.name)
          : null;
      const openedPath = opened !== null && s.detail !== null ? (s.detail.artifacts.find((f) => f.name === openArtifact?.name)?.path ?? opened.path) : null;
      children.push(
        sectionTitle(h, 'works-title', labels.works + ' (' + s.model.works.length + ')'),
        h('div', { key: 'works', style: { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: '8px' } },
          s.model.works.map((card) => renderWorkCard(h, s, a, card))),
        h('div', {
          key: 'navigation',
          'data-layout': s.stacked ? 'stacked' : 'split',
          style: { display: 'flex', flexDirection: s.stacked ? 'column' : 'row', gap: '16px', paddingTop: '10px', borderTop: '1px solid ' + COLOR.border },
        }, renderTree(h, s, a),
          opened !== null && openedPath !== null
            ? renderArtifactViewer(h, { ...s, openArtifact, viewMode: s.viewMode ?? 'semantic' }, a, opened, openedPath)
            : renderDetail(h, s, a)),
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
    const testHooks = {
      latestGate, overviewModel, reconcileSelection, renderPanel, routeUrl, shouldStack, labelsFor, stageTitle,
      nextViewMode, nextReadingMode, artifactTextOf, sourceEntriesOf, PANEL_ID, ROUTE_PATH, FOLD_LINE_THRESHOLD,
      SETUP_ROUTE, parseSetupStatus, setupViewModel, setupLabelsFor,
    };

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
      const [view, setView] = react.useState('overview' as PanelView);
      const [openArtifact, setOpenArtifact] = react.useState(null as { unit: string; name: string } | null);
      const [viewMode, setViewMode] = react.useState('semantic' as 'semantic' | 'source');
      const [sourcesMode, setSourcesMode] = react.useState('reading' as 'reading' | 'source');
      const [selectedSource, setSelectedSource] = react.useState(null as string | null);
      const [openFolds, setOpenFolds] = react.useState({} as Record<string, boolean>);
      const [treeCollapsed, setTreeCollapsed] = react.useState({} as Record<string, boolean>);
      const [setup, setSetup] = react.useState({ status: null as SetupStatus | null, busy: false });
      const containerRef = react.useRef(null);
      // Scroll offset captured before a view toggle, restored after the new
      // view has rendered, so switching never jumps back to the top.
      const scrollSave = react.useRef(0);
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

      // Probe the setup route once: 200 with a well-formed status shows the
      // section; anything else (including the host not registering the route
      // because enableSetup is off) keeps the panel exactly as before.
      react.useEffect(() => {
        let cancelled = false;
        fetch(SETUP_ROUTE)
          .then((response: Response) => (response.ok ? (response.json() as Promise<unknown>) : null))
          .then((body: unknown) => {
            if (cancelled) return;
            const status = parseSetupStatus(body);
            if (status !== null) setSetup((prev: { busy: boolean }) => ({ status, busy: prev.busy }));
          })
          .catch(() => undefined);
        return () => {
          cancelled = true;
        };
      }, []);

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

      // After a view toggle re-renders, put the scroll offset back where the
      // author was reading; the effect runs once the new content is mounted.
      react.useEffect(() => {
        const element = containerRef.current as HTMLElement | null;
        if (element !== null && scrollSave.current > 0) element.scrollTop = scrollSave.current;
        scrollSave.current = 0;
      }, [viewMode, sourcesMode]);

      const captureScroll = (): void => {
        const element = containerRef.current as HTMLElement | null;
        scrollSave.current = element?.scrollTop ?? 0;
      };

      // Arrow-key navigation moves DOM focus with the selection, so the tree
      // stays operable without a mouse. The panel owns the only tree markup,
      // and refs are schema-constrained unit ids (no quotes to escape beyond
      // the defensive replace).
      const focusTreeNode = (ref: string): void => {
        if (typeof document === 'undefined') return;
        const node = document.querySelector('[data-tree-ref="' + ref.replace(/"/g, '\\"') + '"]');
        if (node !== null && typeof (node as HTMLElement).focus === 'function') (node as HTMLElement).focus();
      };

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
        view,
        openArtifact,
        viewMode,
        sourcesMode,
        selectedSource,
        openFolds,
        treeCollapsed,
        setup: setup.status,
        setupBusy: setup.busy,
      };
      const actions: PanelActions = {
        refresh: () => fetchSnapshot(panelRoot),
        setPathInput,
        switchTo: (path: string) => {
          const trimmed = path.trim();
          if (trimmed === '') return;
          setPathInput(trimmed);
          setSelection({ work: null, unit: null });
          setOpenArtifact(null);
          setView('overview');
          setSelectedSource(null);
          setOpenFolds({});
          setTreeCollapsed({});
          // Re-requesting the same path still refreshes it.
          if (trimmed === panelRoot) fetchSnapshot(trimmed);
          else setPanelRoot(trimmed);
        },
        restoreDefault: () => {
          setPathInput('');
          setSelection({ work: null, unit: null });
          setOpenArtifact(null);
          setView('overview');
          setSelectedSource(null);
          setOpenFolds({});
          setTreeCollapsed({});
          setPanelRoot(null);
        },
        selectWork: (id: string) => {
          setSelection({ work: id, unit: null });
          setOpenArtifact(null);
        },
        selectUnit: (ref: string) => {
          setSelection((prev: { work: string | null }) => ({ work: prev.work ?? picked?.work ?? null, unit: ref }));
          setOpenArtifact(null);
        },
        openArtifactView: (unit: string, name: string) => {
          setView('overview');
          setOpenArtifact({ unit, name });
          setViewMode('semantic');
        },
        closeArtifactView: () => setOpenArtifact(null),
        toggleViewMode: () => {
          captureScroll();
          setViewMode((prev: 'semantic' | 'source') => nextViewMode(prev));
        },
        showSources: (sourceId: string | null) => {
          setView('sources');
          setSelectedSource(sourceId);
        },
        showQueue: (view: 'decisions' | 'reviews') => {
          setView(view);
        },
        backToOverview: () => {
          setView('overview');
          setSelectedSource(null);
        },
        toggleSourcesMode: () => {
          captureScroll();
          setSourcesMode((prev: 'reading' | 'source') => nextReadingMode(prev));
        },
        toggleFold: (key: string, open: boolean) => {
          setOpenFolds((prev: Record<string, boolean>) => ({ ...prev, [key]: open }));
        },
        enableSetup: () => {
          // Single-flight on the client as well: a second click during an
          // action is ignored before it can queue another POST.
          if (setup.busy) return;
          setSetup((prev: { status: SetupStatus | null }) => ({ status: prev.status, busy: true }));
          fetch(SETUP_ROUTE, { method: 'POST' })
            .then((response: Response) => (response.ok ? (response.json() as Promise<unknown>) : null))
            .then((body: unknown) => {
              const status = parseSetupStatus(body);
              setSetup((prev: { status: SetupStatus | null }) => ({ status: status ?? prev.status, busy: false }));
            })
            .catch(() => {
              setSetup((prev: { status: SetupStatus | null }) => ({ status: prev.status, busy: false }));
            });
        },
        treeKey: (event: unknown, ref: string) => {
          const decision = treeKeyAction(picked?.tree ?? [], treeCollapsed, ref, String((event as { key?: unknown })?.key ?? ''));
          if (decision.kind === 'none') return;
          if (decision.kind === 'select') {
            setSelection((prev: { work: string | null }) => ({ work: prev.work ?? picked?.work ?? null, unit: decision.ref }));
          } else {
            setTreeCollapsed((prev: Record<string, boolean>) => ({ ...prev, [decision.ref]: decision.collapsed }));
          }
          focusTreeNode(decision.ref);
          // The arrows are consumed here so the panel container does not
          // scroll underneath the moving selection.
          (event as { preventDefault?: () => void })?.preventDefault?.();
        },
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
