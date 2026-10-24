/**
 * Panel half of the zcode-writing-viewer plugin: the MCP Apps page the
 * server serves as ui://zcode-writing-viewer/panel.html.
 *
 * One official `App` (from @modelcontextprotocol/ext-apps) connects to the
 * host; every further round trip goes through `callServerTool` /
 * `readServerResource` against this plugin's own MCP server — the same
 * server that serves this page. There is no second connection and no use of
 * the legacy window.zcode bridge.
 *
 * All display derivation lives in the pure view-model modules ported from
 * the DSH plugin (overview, treekeys, artifactview, markdownview,
 * yamlhighlight, queueview, reviewmodel, reviewview, setupview); this entry
 * owns only state, events and rendering. Rendering uses createElement with
 * plain-text children only — workspace text is never parsed as HTML
 * (client-content-rendering-safety), and the page makes no network requests
 * beyond its own server's tools and resources.
 */
import { App } from '@modelcontextprotocol/ext-apps';
import { parse as parseYaml } from 'yaml';

import { artifactTextOf, artifactView, cachedArtifactView, sourceEntriesOf, type CardRow, type SemanticCard } from './artifactview';
import type { ArtifactText } from './artifacts';
import { highlightMarkdownSource, renderMarkdown, type MdBlock } from './markdownview';
import { foldYaml, highlightYaml } from './yamlhighlight';
import {
  decisionEntries,
  queueTextOf,
  reviewEntries,
  type DecisionView,
  type ReviewRecordView,
} from './queueview';
import { NEW_WORK_COMMAND, overviewModel, unitDetail, unitTree } from './overview';
import type { SnapshotRouteBody } from './snapshot';
import { treeKeyAction, visibleTreeNodes } from './treekeys';
import {
  authorReviewRequest,
  confirmKey,
  confirmRequest,
  envelopeMessage,
  feedbackAfterSelection,
  feedbackRequest,
  illustrationLayout,
  illustrationSrc,
  initialConfirmDraft,
  initialFeedback,
  pendingOf,
  parseReviewPage,
  selectedReviewKeys,
  selectionOutcome,
  sessionGone,
  type ConfirmDraft,
  type FeedbackDraft,
  type PageIllustration,
  type PageUnit,
  type ReviewPage,
} from './reviewmodel';
import { IDLE_REVIEW, overlayDismissKey, reviewAfterStart, reviewAfterStatus, reviewEnded, reviewLabelsFor, reviewOverlayShown, reviewStartBody, reviewViewModel, type ReviewPanelState } from './reviewview';
import { parseSetupStatus, setupLabelsFor, setupViewModel } from './setupview';

/** Milliseconds between view reports; the panel reports, the model reads. */
const REPORT_DEBOUNCE_MS = 400;

/** Session status poll cadence while a review is active. */
const SESSION_POLL_MS = 30_000;

// ---------------------------------------------------------------------------
// Bilingual labels (zh first, en fallback — host locale picks).

interface Labels {
  loading: string;
  emptyTitle: string;
  emptyBody: string;
  errorTitle: string;
  retry: string;
  noWorks: string;
  toolbarRoot: string;
  toolbarSource: string;
  refresh: string;
  pathPlaceholder: string;
  switchPath: string;
  restoreDefault: string;
  reading: string;
  countsSources: string;
  countsDecisions: string;
  countsReviews: string;
  countsDiagnostics: string;
  unavailable: string;
  diagnosticsTitle: string;
  degradedTitle: string;
  worksTitle: string;
  workUnits: string;
  workStage: string;
  workProgress: string;
  workBlockers: string;
  pendingBadge: string;
  sourcesTitle: string;
  sourcesEmpty: string;
  decisionsTitle: string;
  reviewsTitle: string;
  queueEmpty: string;
  queueBroken: string;
  queueRecommend: string;
  queueBlocking: string;
  queueUnits: string;
  reviewChannel: string;
  backToOverview: string;
  treeTitle: string;
  detailTitle: string;
  detailType: string;
  detailStage: string;
  detailProgress: string;
  detailBlockers: string;
  detailCommands: string;
  detailArtifacts: string;
  open: string;
  viewSemantic: string;
  viewSource: string;
  viewReading: string;
  parseError: string;
  claimsSourceJump: string;
  artifactLoading: string;
  pageFailed: string;
}

const TEXT: Record<'zh' | 'en', Labels> = {
  zh: {
    loading: '正在读取工作区…',
    emptyTitle: '未找到工作区',
    emptyBody: '在下列候选目录下都没有找到 .writing-anything/ 工作区。可运行下面的命令初始化：',
    errorTitle: '读取失败',
    retry: '重试',
    noWorks: '工作区还没有作品。可运行下面的命令创建：',
    toolbarRoot: '项目目录',
    toolbarSource: '来源',
    refresh: '刷新',
    pathPlaceholder: '输入另一个项目目录的绝对路径…',
    switchPath: '切换',
    restoreDefault: '恢复默认',
    reading: '读取中…',
    countsSources: '素材',
    countsDecisions: '待决决策',
    countsReviews: '待复核',
    countsDiagnostics: 'validate 诊断',
    unavailable: '—',
    diagnosticsTitle: '校验诊断',
    degradedTitle: '降级原因',
    worksTitle: '作品',
    workUnits: '单元',
    workStage: '阶段',
    workProgress: '进度',
    workBlockers: '阻塞',
    pendingBadge: '待审阅',
    sourcesTitle: '素材清单',
    sourcesEmpty: '还没有素材',
    decisionsTitle: '决策队列（Decision Queue）',
    reviewsTitle: '作者复核记录',
    queueEmpty: '没有记录',
    queueBroken: '文件无法解析',
    queueRecommend: '推荐',
    queueBlocking: '阻塞中',
    queueUnits: '关联单元',
    reviewChannel: '渠道',
    backToOverview: '返回总览',
    treeTitle: '单元（Unit）树',
    detailTitle: '单元详情',
    detailType: '类型',
    detailStage: '阶段',
    detailProgress: '进度',
    detailBlockers: '阻塞项',
    detailCommands: '下一步命令',
    detailArtifacts: '产物（Artifact）',
    open: '打开',
    viewSemantic: '语义卡片视图',
    viewSource: '源码高亮视图',
    viewReading: '阅读视图',
    parseError: '产物解析失败，显示源码：',
    claimsSourceJump: '查看素材',
    artifactLoading: '正在读取产物…',
    pageFailed: '阅读页数据读取失败',
  },
  en: {
    loading: 'Loading the workspace…',
    emptyTitle: 'No workspace found',
    emptyBody: 'No .writing-anything/ workspace exists under the probed candidates. Initialize one with:',
    errorTitle: 'Could not load',
    retry: 'Retry',
    noWorks: 'The workspace has no works yet. Create one with:',
    toolbarRoot: 'Project directory',
    toolbarSource: 'source',
    refresh: 'Refresh',
    pathPlaceholder: 'Absolute path of another project directory…',
    switchPath: 'Switch',
    restoreDefault: 'Restore default',
    reading: 'Reading…',
    countsSources: 'Sources',
    countsDecisions: 'Open decisions',
    countsReviews: 'Pending reviews',
    countsDiagnostics: 'validate diagnostics',
    unavailable: '—',
    diagnosticsTitle: 'Validation diagnostics',
    degradedTitle: 'Degraded areas',
    worksTitle: 'Works',
    workUnits: 'units',
    workStage: 'stage',
    workProgress: 'progress',
    workBlockers: 'blockers',
    pendingBadge: 'Pending review',
    sourcesTitle: 'Sources',
    sourcesEmpty: 'No sources yet',
    decisionsTitle: 'Decision queue',
    reviewsTitle: 'Author review records',
    queueEmpty: 'Nothing recorded',
    queueBroken: 'The file cannot be parsed',
    queueRecommend: 'recommended',
    queueBlocking: 'blocking',
    queueUnits: 'units',
    reviewChannel: 'channel',
    backToOverview: 'Back to overview',
    treeTitle: 'Unit tree',
    detailTitle: 'Unit detail',
    detailType: 'Type',
    detailStage: 'Stage',
    detailProgress: 'Progress',
    detailBlockers: 'Blockers',
    detailCommands: 'Next commands',
    detailArtifacts: 'Artifacts',
    open: 'Open',
    viewSemantic: 'Semantic cards',
    viewSource: 'Source highlight',
    viewReading: 'Reading view',
    parseError: 'The artifact failed to parse; showing source:',
    claimsSourceJump: 'View source',
    artifactLoading: 'Reading the artifact…',
    pageFailed: 'Could not load the reading view data',
  },
};

let lang: 'zh' | 'en' = 'zh';
function labels(): Labels {
  return TEXT[lang];
}

// ---------------------------------------------------------------------------
// Host bridge: one App, handlers installed before connect.

type AnyRecord = Record<string, unknown>;

const app = new App({ name: 'zcode-writing-viewer', version: '0.1.0' }, {});

/** Sequential tool-call ids: only the newest call's answer lands. */
let callSeq = 0;

async function callTool<T>(name: string, args: AnyRecord = {}): Promise<T | null> {
  const seq = ++callSeq;
  try {
    const raw = await app.callServerTool({ name, arguments: args });
    if (seq !== callSeq) return null;
    return ((raw as { structuredContent?: T }).structuredContent ?? null);
  } catch {
    return null;
  }
}

interface ResourceContents {
  uri?: string;
  mimeType?: string;
  text?: string;
  blob?: string;
}

async function readResource(uri: string): Promise<ResourceContents[] | null> {
  try {
    const raw = await app.readServerResource({ uri });
    if (raw !== null && typeof raw === 'object' && Array.isArray((raw as { contents?: unknown }).contents)) {
      return (raw as { contents: ResourceContents[] }).contents;
    }
    return null;
  } catch {
    return null;
  }
}

/** Illustration bytes → data URL cache (available images only). */
const illustrationCache = new Map<string, string | null>();

async function illustrationDataUrl(unit: string, id: string): Promise<string | null> {
  const uri = illustrationSrc(unit, id);
  if (illustrationCache.has(uri)) return illustrationCache.get(uri) ?? null;
  const contents = await readResource(uri);
  const blob = contents?.[0]?.blob ?? null;
  const mime = contents?.[0]?.mimeType ?? 'image/png';
  const url = blob === null ? null : `data:${mime};base64,${blob}`;
  illustrationCache.set(uri, url);
  return url;
}

// ---------------------------------------------------------------------------
// Panel state.

interface PanelState {
  /** Snapshot request in flight with no data yet (first open). */
  firstLoad: boolean;
  /** A refresh/switch is in flight; keep old data visible. */
  refreshing: boolean;
  body: SnapshotRouteBody | null;
  error: string | null;
  /** The panel path switch: absolute path or null (default location). */
  panelRoot: string | null;
  rootInput: string;
  selectedWork: string | null;
  selectedUnit: string | null;
  collapsed: Record<string, boolean>;
  /** Which full-screen view: overview | sources | decisions | reviews. */
  view: 'overview' | 'sources' | 'decisions' | 'reviews';
  /** Open artifact of the selected unit: {name, mode}. */
  artifact: { name: string; mode: 'semantic' | 'source' | 'reading' } | null;
  /** Selected source entry id in the sources view. */
  selectedSource: string | null;
  review: ReviewPanelState;
  reviewEnlarged: boolean;
  page: ReviewPage | null;
  pageError: string | null;
  feedback: FeedbackDraft | null;
  confirms: Record<string, ConfirmDraft>;
  unticked: Record<string, boolean>;
  notice: string | null;
  setupStatus: ReturnType<typeof parseSetupStatus>;
  setupInFlight: boolean;
}

const state: PanelState = {
  firstLoad: true,
  refreshing: false,
  body: null,
  error: null,
  panelRoot: null,
  rootInput: '',
  selectedWork: null,
  selectedUnit: null,
  collapsed: {},
  view: 'overview',
  artifact: null,
  selectedSource: null,
  review: IDLE_REVIEW,
  reviewEnlarged: false,
  page: null,
  pageError: null,
  feedback: null,
  confirms: {},
  unticked: {},
  notice: null,
  setupStatus: null,
  setupInFlight: false,
};

let treeFocusRef: string | null = null;

// ---------------------------------------------------------------------------
// Data actions.

async function loadSnapshot(): Promise<void> {
  state.refreshing = true;
  if (state.body === null) state.firstLoad = true;
  render();
  const body = await callTool<SnapshotRouteBody>('workspace_snapshot', state.panelRoot === null ? {} : { root: state.panelRoot });
  state.refreshing = false;
  state.firstLoad = false;
  if (body === null) {
    state.error = labels().errorTitle;
  } else {
    state.error = null;
    state.body = body;
    const pending = pendingOf(
      (body.snapshot?.cli?.next ?? null) as { pendingConfirmations?: unknown[]; pendingAuthorReviews?: unknown[] } | null,
      body.degraded ?? [],
    );
    void pending;
    // Keep selection valid for the new body.
    const model = overviewModel(body);
    if (model.state === 'ready') {
      const works = model.works.map((w) => w.id);
      if (state.selectedWork === null || !works.includes(state.selectedWork)) {
        state.selectedWork = works[0] ?? null;
        state.selectedUnit = state.selectedWork;
      } else if (state.selectedUnit !== null) {
        const tree = unitTree(body, state.selectedWork);
        if (!tree.some((n) => n.ref === state.selectedUnit)) state.selectedUnit = state.selectedWork;
      }
    } else {
      state.selectedWork = null;
      state.selectedUnit = null;
    }
    state.artifact = state.artifact === null || state.selectedUnit === null ? null : keepArtifact(state.artifact);
  }
  render();
  scheduleReport();
}

function keepArtifact(current: { name: string; mode: 'semantic' | 'source' | 'reading' }) {
  const detail = state.body === null || state.selectedUnit === null ? null : unitDetail(state.body, state.selectedUnit);
  if (detail === null) return null;
  return detail.artifacts.some((entry) => entry.name === current.name) ? current : null;
}

async function startReview(unit: string): Promise<void> {
  state.selectedUnit = unit;
  state.review = { ...IDLE_REVIEW, unit, phase: 'starting' };
  state.page = null;
  state.pageError = null;
  state.feedback = null;
  state.confirms = {};
  state.unticked = {};
  render();
  const outcome = await callTool<{ ok: boolean; session?: { startedAt: string }; error?: string; diagnostics?: { message: string }[] }>('review_start', reviewStartBody(unit, state.panelRoot));
  if (outcome === null) {
    state.review = { unit, phase: 'error', error: labels().errorTitle, startedAt: null };
  } else if (outcome.ok === true && outcome.session != null) {
    state.review = reviewAfterStart(unit, { ok: true, session: { unit, root: state.panelRoot ?? '', startedAt: outcome.session.startedAt } });
    await loadReviewPage();
  } else {
    const diags = Array.isArray(outcome.diagnostics) ? outcome.diagnostics.map((d) => d.message).join('；') : '';
    state.review = { unit, phase: 'error', error: diags !== '' ? diags : String(outcome.error ?? labels().errorTitle), startedAt: null };
  }
  render();
  scheduleReport();
}

async function loadReviewPage(): Promise<void> {
  if (state.review.unit === null) return;
  const result = await callTool<{ ok: boolean; status: number; body: string }>('review_page');
  if (result === null) {
    state.pageError = labels().pageFailed;
    return;
  }
  if (result.ok !== true || typeof result.body !== 'string') {
    state.pageError = envelopeMessage(result.status, null);
    if (sessionGone(result.status, null)) state.review = reviewEnded(state.review);
    return;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(result.body);
  } catch {
    state.pageError = labels().pageFailed;
    return;
  }
  const page = parseReviewPage(parsed);
  if (page === null) {
    state.pageError = labels().pageFailed;
    return;
  }
  state.page = page;
  state.pageError = null;
  // Seed the drafts for newly listed confirm items.
  for (const item of page.confirms) {
    const key = confirmKey(item);
    if (state.confirms[key] === undefined) state.confirms[key] = initialConfirmDraft(item);
  }
  if (state.feedback === null && state.review.unit !== null) state.feedback = initialFeedback(state.review.unit);
}

async function pollSession(): Promise<void> {
  if (state.review.phase !== 'active') return;
  const result = await callTool<{ session: { unit?: string } | null }>('review_status');
  if (result !== null) {
    const next = reviewAfterStatus(state.review, result);
    if (next !== state.review) {
      state.review = next;
      render();
    }
  }
}

async function endReview(): Promise<void> {
  await callTool('review_end');
  state.review = IDLE_REVIEW;
  state.reviewEnlarged = false;
  state.page = null;
  await loadSnapshot();
}

async function submitReview(action: 'feedback' | 'confirm' | 'author-review', bodyJson: string): Promise<void> {
  let body: unknown;
  try {
    body = JSON.parse(bodyJson);
  } catch {
    state.notice = labels().errorTitle;
    render();
    return;
  }
  const result = await callTool<{ ok: boolean; status: number; body: string | { diagnostics?: { message: string }[] } }>('review_submit', { action, body });
  if (result === null) {
    state.notice = labels().errorTitle;
  } else if (result.ok === true) {
    state.notice = null;
    await loadReviewPage();
  } else {
    const raw = typeof result.body === 'string' ? result.body : JSON.stringify(result.body ?? {});
    state.notice = envelopeMessage(result.status, safeJsonParse(raw));
    if (sessionGone(result.status, null)) state.review = reviewEnded(state.review);
  }
  render();
}

function safeJsonParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

async function probeSetup(): Promise<void> {
  const status = await callTool<unknown>('setup_status', state.panelRoot === null ? {} : { root: state.panelRoot });
  const parsed = parseSetupStatus(status);
  if (parsed !== null) {
    state.setupStatus = parsed;
    render();
  }
}

async function runSetupEnable(): Promise<void> {
  if (state.setupInFlight) return;
  state.setupInFlight = true;
  render();
  await callTool('setup_enable');
  state.setupInFlight = false;
  await probeSetup();
}

// ---------------------------------------------------------------------------
// View reporting (the model's get_view reads what the panel reports).

let reportTimer: ReturnType<typeof setTimeout> | null = null;

function scheduleReport(): void {
  if (reportTimer !== null) clearTimeout(reportTimer);
  reportTimer = setTimeout(() => {
    void callTool('report_view', {
      viewId: 'writing-workspace',
      root: state.body?.root ?? null,
      unit: state.selectedUnit,
      view: state.review.phase === 'active' ? 'review' : state.view,
      selection: null,
    });
  }, REPORT_DEBOUNCE_MS);
}

// ---------------------------------------------------------------------------
// Rendering. Plain createElement; text children only.

type Child = string | Node | null | undefined | false;
function el(tag: string, attrs: Record<string, string | boolean | ((event: Event) => void)> = {}, ...children: Child[]): HTMLElement {
  const node = document.createElement(tag);
  for (const [name, value] of Object.entries(attrs)) {
    if (typeof value === 'function') {
      node.addEventListener(name.replace(/^on/, ''), value as EventListener);
    } else if (typeof value === 'boolean') {
      if (value) node.setAttribute(name, '');
    } else if (name === 'class') {
      node.className = value;
    } else {
      node.setAttribute(name, value);
    }
  }
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue;
    node.append(typeof child === 'string' ? document.createTextNode(child) : child);
  }
  return node;
}

function code(text: string): HTMLElement {
  return el('code', { class: 'wv-code' }, text);
}

function button(label: string, onClick: () => void, opts: { class?: string; disabled?: boolean; title?: string } = {}): HTMLElement {
  const node = el('button', { type: 'button', class: opts.class ?? 'wv-btn', ...(opts.disabled ? { disabled: true } : {}) }, label);
  node.addEventListener('click', onClick);
  return node;
}

const root = (): HTMLElement => document.getElementById('root') as HTMLElement;

function applyTheme(): void {
  const ctx = app.getHostContext() as { theme?: string; styles?: { variables?: Record<string, string> } } | undefined;
  const theme = ctx?.theme === 'dark' ? 'dark' : 'light';
  document.documentElement.dataset.theme = theme;
  const style = ctx?.styles?.variables ?? {};
  const map: [string, string][] = [
    ['--wv-bg', style['--color-background-primary'] ?? (theme === 'dark' ? '#16181d' : '#ffffff')],
    ['--wv-bg-2', style['--color-background-secondary'] ?? (theme === 'dark' ? '#1e2128' : '#f6f8fa')],
    ['--wv-text', style['--color-text-primary'] ?? (theme === 'dark' ? '#e6e8eb' : '#1f2328')],
    ['--wv-text-muted', style['--color-text-secondary'] ?? (theme === 'dark' ? '#9aa4b2' : '#6e7781')],
    ['--wv-border', style['--color-border-primary'] ?? (theme === 'dark' ? '#333942' : '#d1d9e0')],
    ['--wv-accent', style['--color-text-info'] ?? (theme === 'dark' ? '#6cb6ff' : '#0969da')],
    ['--wv-danger', style['--color-text-danger'] ?? (theme === 'dark' ? '#ff8182' : '#cf222e')],
    ['--wv-success', style['--color-text-success'] ?? (theme === 'dark' ? '#57ab5a' : '#1a7f37')],
    ['--wv-warning', style['--color-text-warning'] ?? (theme === 'dark' ? '#e3b341' : '#9a6700')],
  ];
  for (const [name, value] of map) document.documentElement.style.setProperty(name, value);
}

function render(): void {
  applyTheme();
  const host = root();
  host.replaceChildren();
  host.append(renderShell());
  // Re-focus the tree node the keyboard navigation moved to.
  if (treeFocusRef !== null) {
    const node = host.querySelector(`[data-tree-ref="${CSS.escape(treeFocusRef)}"]`) as HTMLElement | null;
    node?.focus();
  }
  // The enlarged overlay grabs focus when it covers the panel (Esc dismiss).
  const overlay = host.querySelector('[data-review-overlay]') as HTMLElement | null;
  overlay?.focus();
}

function renderShell(): HTMLElement {
  const L = labels();
  const wrap = el('div', { class: 'wv-shell' });
  wrap.append(renderToolbar());
  if (state.notice !== null) wrap.append(el('div', { class: 'wv-notice', role: 'status' }, state.notice));
  if (state.error !== null && state.body === null) {
    wrap.append(el('div', { class: 'wv-state' }, el('h2', {}, L.errorTitle), el('div', { class: 'wv-error' }, state.error), button(L.retry, () => void loadSnapshot())));
    return wrap;
  }
  if (state.firstLoad && state.body === null) {
    wrap.append(el('div', { class: 'wv-state', role: 'status', 'aria-busy': 'true' }, L.loading));
    return wrap;
  }
  const body = state.body;
  if (body === null) return wrap;
  const model = overviewModel(body);
  if (model.state === 'missing') {
    wrap.append(renderEmpty(body));
    return wrap;
  }
  if (state.setupStatus !== null) wrap.append(renderSetup());
  if (model.state === 'no-works') {
    wrap.append(el('div', { class: 'wv-state' }, el('h2', {}, L.noWorks), code(NEW_WORK_COMMAND)));
    return wrap;
  }
  if (state.view === 'sources') {
    wrap.append(renderSources(body));
    return wrap;
  }
  if (state.view === 'decisions') {
    wrap.append(renderDecisions(body));
    return wrap;
  }
  if (state.view === 'reviews') {
    wrap.append(renderReviewRecords(body));
    return wrap;
  }
  wrap.append(renderCounts(model, body));
  wrap.append(renderWorks(model, body));
  if (state.selectedWork !== null) {
    const treeAndDetail = el('div', { class: 'wv-columns' },
      renderTree(body, state.selectedWork),
      renderDetail(body),
    );
    wrap.append(treeAndDetail);
  }
  if (reviewOverlayShown(state.review, state.reviewEnlarged)) {
    wrap.append(renderReviewOverlay());
  }
  return wrap;
}

function renderToolbar(): HTMLElement {
  const L = labels();
  const bar = el('div', { class: 'wv-toolbar' });
  const body = state.body;
  if (body !== null && body.root !== null) {
    bar.append(
      el('span', { class: 'wv-toolbar-root' }, `${L.toolbarRoot}：`, el('b', {}, body.root), `（${L.toolbarSource} ${body.source ?? '—'}）`),
    );
  }
  if (state.refreshing) bar.append(el('span', { class: 'wv-muted', role: 'status' }, L.reading));
  bar.append(button(L.refresh, () => void loadSnapshot()));
  const input = el('input', {
    class: 'wv-input',
    type: 'text',
    placeholder: L.pathPlaceholder,
    value: state.rootInput,
  }) as HTMLInputElement;
  input.addEventListener('input', () => {
    state.rootInput = input.value;
  });
  input.addEventListener('keydown', (event) => {
    if ((event as KeyboardEvent).key === 'Enter') switchPath();
  });
  bar.append(input, button(L.switchPath, switchPath));
  if (state.panelRoot !== null) bar.append(button(L.restoreDefault, restoreDefault, { class: 'wv-btn wv-btn-quiet' }));
  return bar;
}

function switchPath(): void {
  const value = state.rootInput.trim();
  if (value === '') return;
  state.panelRoot = value;
  state.artifact = null;
  void loadSnapshot().then(() => {
    if (state.body?.rootError !== undefined && state.body?.rootError !== null) {
      state.rootInput = '';
    }
  });
}

function restoreDefault(): void {
  state.panelRoot = null;
  state.rootInput = '';
  state.artifact = null;
  void loadSnapshot();
}

function renderEmpty(body: SnapshotRouteBody): HTMLElement {
  const L = labels();
  const wrap = el('div', { class: 'wv-state' }, el('h2', {}, L.emptyTitle));
  const rootError = (body as { rootError?: string }).rootError;
  if (typeof rootError === 'string' && rootError !== '') {
    wrap.append(el('div', { class: 'wv-error' }, rootError));
  } else {
    wrap.append(el('p', {}, L.emptyBody));
  }
  wrap.append(code('wa-cli init'));
  const probes = el('ul', { class: 'wv-list' });
  for (const candidate of body.candidates ?? []) {
    probes.append(el('li', {}, `${candidate.path} — ${candidate.workspaceExists ? '✓' : '✗'}`));
  }
  if (probes.childNodes.length > 0) wrap.append(probes);
  return wrap;
}

function renderCounts(model: ReturnType<typeof overviewModel>, body: SnapshotRouteBody): HTMLElement {
  const L = labels();
  const pending = pendingOf(
    (body.snapshot?.cli?.next ?? null) as { pendingConfirmations?: unknown[]; pendingAuthorReviews?: unknown[] } | null,
    body.degraded ?? [],
  );
  const summary = model.summary ?? { sources: 0, pendingDecisions: null, pendingReviews: null, diagnostics: null, diagnosticsBySeverity: {} };
  const box = el('div', { class: 'wv-counts' });
  const tile = (labelText: string, value: string, onClick?: () => void): HTMLElement => {
    const node = el('div', { class: `wv-tile${onClick === undefined ? '' : ' wv-tile-link'}`, ...(onClick !== undefined ? { tabindex: '0', role: 'button' } : {}) },
      el('div', { class: 'wv-tile-value' }, value),
      el('div', { class: 'wv-tile-label' }, labelText),
    );
    if (onClick !== undefined) {
      node.addEventListener('click', onClick);
      node.addEventListener('keydown', (event) => {
        if ((event as KeyboardEvent).key === 'Enter' || (event as KeyboardEvent).key === ' ') {
          event.preventDefault();
          onClick();
        }
      });
    }
    return node;
  };
  box.append(tile(L.countsSources, String(summary.sources), () => {
    state.view = 'sources';
    render();
    scheduleReport();
  }));
  box.append(tile(L.countsDecisions, summary.pendingDecisions === null ? L.unavailable : String(summary.pendingDecisions), () => {
    state.view = 'decisions';
    render();
    scheduleReport();
  }));
  box.append(tile(L.countsReviews, summary.pendingReviews === null ? L.unavailable : String(summary.pendingReviews), () => {
    state.view = 'reviews';
    render();
    scheduleReport();
  }));
  box.append(tile(L.countsDiagnostics, summary.diagnostics === null ? L.unavailable : String(summary.diagnostics)));
  void pending;
  if (Object.keys(summary.diagnosticsBySeverity).length > 0) {
    const list = el('details', { class: 'wv-details' }, el('summary', {}, L.diagnosticsTitle));
    for (const diag of model.diagnostics) {
      list.append(el('div', { class: `wv-diag wv-sev-${diag.severity}` }, `[${diag.severity}] ${diag.code} ${diag.message}${diag.path === null ? '' : `（${diag.path}）`}`));
    }
    box.append(list);
  }
  if ((body.degraded ?? []).length > 0) {
    const list = el('details', { class: 'wv-details' }, el('summary', {}, L.degradedTitle));
    for (const note of body.degraded ?? []) list.append(el('div', { class: 'wv-diag' }, note));
    box.append(list);
  }
  return box;
}

function renderWorks(model: ReturnType<typeof overviewModel>, body: SnapshotRouteBody): HTMLElement {
  const L = labels();
  const pending = pendingOf(
    (body.snapshot?.cli?.next ?? null) as { pendingConfirmations?: unknown[]; pendingAuthorReviews?: unknown[] } | null,
    body.degraded ?? [],
  );
  const wrap = el('div', { class: 'wv-works' }, el('h2', {}, L.worksTitle));
  for (const work of model.works) {
    const count = pending?.byWork[work.id] ?? 0;
    const card = el('div', {
      class: `wv-card${state.selectedWork === work.id ? ' wv-card-active' : ''}`,
      tabindex: '0',
      role: 'button',
    },
      el('div', { class: 'wv-card-title' }, el('b', {}, work.id), work.genre === null ? '' : ` · ${work.genre}`),
      el('div', { class: 'wv-card-row' }, `${L.workUnits} ${work.unitCount}`, work.stage === null ? '' : ` · ${L.workStage} ${work.stage}`,
        work.progress === null ? '' : ` · ${L.workProgress} ${work.progress.done}/${work.progress.total}`,
        ` · ${L.workBlockers} ${work.blockerCount}`),
      ...work.blockerSummary.map((line) => el('div', { class: 'wv-muted' }, line)),
    );
    if (count > 0) {
      const badge = el('span', { class: 'wv-badge', role: 'button', tabindex: '0' }, `${L.pendingBadge} · ${count}`);
      badge.addEventListener('click', (event) => {
        event.stopPropagation();
        void startReview(work.id);
      });
      badge.addEventListener('keydown', (event) => {
        if ((event as KeyboardEvent).key === 'Enter') {
          event.stopPropagation();
          void startReview(work.id);
        }
      });
      card.append(badge);
    }
    const select = (): void => {
      state.selectedWork = work.id;
      state.selectedUnit = work.id;
      state.artifact = null;
      render();
      scheduleReport();
    };
    card.addEventListener('click', select);
    card.addEventListener('keydown', (event) => {
      if ((event as KeyboardEvent).key === 'Enter' || (event as KeyboardEvent).key === ' ') {
        event.preventDefault();
        select();
      }
    });
    wrap.append(card);
  }
  return wrap;
}

function renderTree(body: SnapshotRouteBody, workId: string): HTMLElement {
  const L = labels();
  const nodes = unitTree(body, workId);
  const pending = pendingOf(
    (body.snapshot?.cli?.next ?? null) as { pendingConfirmations?: unknown[]; pendingAuthorReviews?: unknown[] } | null,
    body.degraded ?? [],
  );
  const wrap = el('div', { class: 'wv-tree', role: 'tree', 'aria-label': L.treeTitle }, el('h2', {}, L.treeTitle));
  const list = el('ul', { class: 'wv-tree-list' });
  for (const node of visibleTreeNodes(nodes, state.collapsed)) {
    const count = pendingCount(pending, node.ref);
    const item = el('li', { class: 'wv-tree-item', role: 'treeitem' });
    const row = el('div', {
      class: `wv-tree-row${state.selectedUnit === node.ref ? ' wv-tree-selected' : ''}`,
      tabindex: state.selectedUnit === node.ref ? '0' : '-1',
      'data-tree-ref': node.ref,
      'aria-current': state.selectedUnit === node.ref ? 'true' : 'false',
      ...(node.aggregated ? { 'aria-expanded': state.collapsed[node.ref] === true ? 'false' : 'true' } : {}),
      style: `padding-left:${8 + node.depth * 16}px`,
    },
      node.aggregated ? (state.collapsed[node.ref] === true ? '▸ ' : '▾ ') : '· ',
      el('span', {}, node.label),
      node.aggregated && node.progress !== null ? el('span', { class: 'wv-muted' }, ` ${node.progress.done}/${node.progress.total}`) : null,
      node.blockerCount > 0 ? el('span', { class: 'wv-muted' }, ` ⚠${node.blockerCount}`) : null,
      count > 0 ? el('span', { class: 'wv-badge wv-badge-small', role: 'button', tabindex: '0' }, `● ${count}`) : null,
    );
    row.addEventListener('click', () => {
      state.selectedUnit = node.ref;
      state.artifact = null;
      render();
      scheduleReport();
    });
    row.addEventListener('keydown', (event) => {
      const key = (event as KeyboardEvent).key;
      const action = treeKeyAction(nodes, state.collapsed, node.ref, key);
      if (action.kind === 'none') return;
      event.preventDefault();
      if (action.kind === 'select') {
        state.selectedUnit = action.ref;
        treeFocusRef = action.ref;
      } else {
        state.collapsed[action.ref] = action.collapsed;
        treeFocusRef = action.ref;
      }
      render();
      scheduleReport();
    });
    item.append(row);
    list.append(item);
  }
  wrap.append(list);
  return wrap;
}

function pendingCount(pending: ReturnType<typeof pendingOf>, ref: string): number {
  return pending?.byUnit[ref] ?? 0;
}

function renderDetail(body: SnapshotRouteBody): HTMLElement {
  const L = labels();
  const wrap = el('div', { class: 'wv-detail' }, el('h2', {}, L.detailTitle));
  if (state.selectedUnit === null) return wrap;
  const detail = unitDetail(body, state.selectedUnit);
  if (detail === null) return wrap;
  const pending = pendingOf(
    (body.snapshot?.cli?.next ?? null) as { pendingConfirmations?: unknown[]; pendingAuthorReviews?: unknown[] } | null,
    body.degraded ?? [],
  );
  const pendingCountHere = pending?.byUnit[detail.ref] ?? 0;
  const rl = reviewLabelsFor(lang === 'zh' ? 'zh' : 'en');
  const reviewView = reviewViewModel(state.review, detail.ref, rl);
  if (reviewView.active) {
    wrap.append(renderReviewDetail(rl));
    return wrap;
  }
  wrap.append(
    el('div', { class: 'wv-card-row' },
      `${L.detailType} ${detail.type ?? '—'} · ${L.detailStage} ${detail.stage ?? '—'}`,
      detail.progress === null ? '' : ` · ${L.detailProgress} ${detail.progress.done}/${detail.progress.total}`),
  );
  if (pendingCountHere > 0) {
    wrap.append(el('div', { class: 'wv-card-row' }, `${rl.pending} · ${pendingCountHere}`));
  }
  if (reviewView.showStart) {
    wrap.append(button(rl.review, () => void startReview(detail.ref), { disabled: reviewView.startDisabled }));
  }
  if (reviewView.busyText !== null) wrap.append(el('div', { role: 'status' }, reviewView.busyText));
  if (reviewView.errorText !== null) wrap.append(el('div', { class: 'wv-error' }, reviewView.errorText));
  if (reviewView.endedText !== null) {
    wrap.append(el('div', { class: 'wv-notice' }, reviewView.endedText), button(rl.restart, () => void startReview(detail.ref)));
  }
  if (detail.blockers.length > 0) {
    const list = el('details', { class: 'wv-details', open: true }, el('summary', {}, L.detailBlockers));
    for (const blocker of detail.blockers) {
      list.append(el('div', { class: 'wv-diag' }, `${blocker.code} ${blocker.message}${blocker.path === null ? '' : `（${blocker.path}）`}`));
    }
    wrap.append(list);
  }
  if (detail.commands.length > 0) {
    wrap.append(el('h3', {}, L.detailCommands));
    for (const command of detail.commands) wrap.append(el('div', {}, code(command)));
  }
  wrap.append(el('h3', {}, L.detailArtifacts));
  const list = el('ul', { class: 'wv-list' });
  for (const entry of detail.artifacts) {
    const open = button(L.open, () => {
      state.artifact = { name: entry.name, mode: entry.format === 'markdown' ? 'reading' : 'semantic' };
      render();
      scheduleReport();
    });
    list.append(el('li', {}, `${entry.name}（${entry.format}）`, open));
  }
  wrap.append(list);
  if (state.artifact !== null) wrap.append(renderArtifact(body, detail.ref, state.artifact));
  return wrap;
}

// ---------------------------------------------------------------------------
// Artifact views (semantic cards / yaml highlight / markdown reading).

function renderArtifact(body: SnapshotRouteBody, unitRef: string, current: { name: string; mode: 'semantic' | 'source' | 'reading' }): HTMLElement {
  const L = labels();
  const artifact = artifactTextOf(body, unitRef, current.name);
  const wrap = el('div', { class: 'wv-artifact' }, el('h3', {}, current.name));
  if (artifact === null) return wrap;
  const isMarkdown = artifact.format === 'markdown';
  const modes: ('semantic' | 'source' | 'reading')[] = isMarkdown ? ['reading', 'source'] : ['semantic', 'source'];
  const modeOf = (mode: string): mode is 'semantic' | 'source' | 'reading' => modes.includes(mode as 'semantic');
  const mode = modeOf(current.mode) ? current.mode : (modes[0] as 'semantic' | 'source' | 'reading');
  const modeLabel: Record<string, string> = { semantic: L.viewSemantic, source: L.viewSource, reading: L.viewReading };
  const scroller = (): HTMLElement | null => wrap.querySelector('.wv-artifact-body') as HTMLElement | null;
  const savedScroll = scroller()?.scrollTop ?? 0;
  const switcher = el('div', { class: 'wv-modes', role: 'group' });
  for (const candidate of modes) {
    const node = button(modeLabel[candidate] ?? candidate, () => {
      state.artifact = { name: current.name, mode: candidate };
      render();
    }, { class: `wv-btn${mode === candidate ? ' wv-btn-active' : ''}` });
    switcher.append(node);
  }
  wrap.append(switcher);
  const bodyNode = el('div', { class: 'wv-artifact-body' });
  const cached = cachedArtifactView(current.name, artifact);
  if (mode === 'source' || (mode === 'semantic' && !isMarkdown && cached.parseError !== null)) {
    if (mode === 'semantic') wrap.append(el('div', { class: 'wv-error' }, L.parseError));
    bodyNode.append(renderYamlOrMarkdownSource(artifact));
  } else if (mode === 'reading') {
    bodyNode.append(renderMarkdownBlocks(artifact.text));
  } else if (isMarkdown) {
    bodyNode.append(renderMarkdownBlocks(artifact.text));
  } else {
    const view = artifactView(current.name, artifact.text);
    if (view.parseError !== null) {
      wrap.append(el('div', { class: 'wv-error' }, `${L.parseError} ${view.parseError}`));
      bodyNode.append(renderYamlOrMarkdownSource(artifact));
    } else {
      bodyNode.append(renderSemanticCards(view.name, view.cards));
    }
  }
  wrap.append(bodyNode);
  bodyNode.scrollTop = savedScroll;
  return wrap;
}

function renderSemanticCards(name: string, cards: SemanticCard[]): HTMLElement {
  const wrap = el('div', { class: 'wv-cards' });
  for (const card of cards) {
    const node = el('div', { class: 'wv-card' }, el('div', { class: 'wv-card-title' }, card.title ?? card.id));
    for (const row of card.rows) node.append(renderCardRow(row));
    wrap.append(node);
  }
  return wrap;
}

function renderCardRow(row: CardRow): HTMLElement {
  const node = el('div', { class: 'wv-card-row' }, el('span', { class: 'wv-card-label' }, `${row.label}：`));
  if (row.type === 'text') {
    node.append(document.createTextNode(row.text));
  } else if (row.type === 'list') {
    node.append(document.createTextNode(row.items.join('、')));
  } else if (row.type === 'sources') {
    row.ids.forEach((id, index) => {
      if (index > 0) node.append(document.createTextNode('、'));
      const jump = el('a', { href: '#', class: 'wv-link' }, id);
      jump.addEventListener('click', (event) => {
        event.preventDefault();
        state.view = 'sources';
        state.selectedSource = id;
        render();
      });
      node.append(jump);
    });
    if (row.ids.length === 0) node.append(document.createTextNode('—'));
  } else {
    const inner = el('div', { class: 'wv-card-group' });
    for (const child of row.rows) inner.append(renderCardRow(child));
    node.append(inner);
  }
  return node;
}

function renderYamlOrMarkdownSource(artifact: ArtifactText): HTMLElement {
  const wrap = el('div', { class: 'wv-source' });
  if (artifact.format === 'markdown') {
    for (const line of highlightMarkdownSource(artifact.text)) {
      const row = el('div', { class: 'wv-line' });
      for (const token of line.tokens) row.append(el('span', { class: token.cls === 'plain' ? 'wv-tok-plain' : `wv-tok-${token.cls}` }, token.text));
      wrap.append(row);
    }
    return wrap;
  }
  const container = el('div', { class: 'wv-source' });
  const folds = foldYaml(artifact.text);
  if (folds.length === 0) {
    appendHighlightedLines(container, highlightYaml(artifact.text));
    return container;
  }
  for (const fold of folds) {
    if (fold.lines.length <= 1) {
      appendHighlightedLines(container, highlightYaml(fold.lines.join('\n')));
      continue;
    }
    const details = el('details', { class: 'wv-fold' }, el('summary', {}, fold.header)) as HTMLDetailsElement;
    const inner = el('div', {});
    details.append(inner);
    details.addEventListener('toggle', () => {
      if (details.open && inner.childNodes.length === 0) {
        appendHighlightedLines(inner, highlightYaml(fold.lines.slice(1).join('\n')));
      }
    });
    container.append(details);
  }
  return wrap;
}

function appendHighlightedLines(target: HTMLElement, lines: { tokens: { text: string; cls: string }[] }[]): void {
  for (const line of lines) {
    const row = el('div', { class: 'wv-line' });
    for (const token of line.tokens) {
      row.append(el('span', { class: token.cls === 'plain' ? 'wv-tok-plain' : `wv-tok-${token.cls}` }, token.text));
    }
    target.append(row);
  }
}

function renderMarkdownBlocks(text: string): HTMLElement {
  const wrap = el('div', { class: 'wv-md' });
  appendBlocks(wrap, renderMarkdown(text));
  return wrap;
}

function appendBlocks(target: HTMLElement, blocks: MdBlock[]): void {
  for (const block of blocks) {
    switch (block.type) {
      case 'heading': {
        target.append(el(`h${Math.min(6, Math.max(1, block.level))}`, { class: 'wv-md-heading' }, block.text));
        break;
      }
      case 'paragraph': {
        target.append(el('p', { class: 'wv-md-paragraph' }, block.text));
        break;
      }
      case 'list': {
        const list = el('ul', { class: 'wv-md-list' });
        for (const item of block.items) list.append(el('li', {}, item));
        target.append(list);
        break;
      }
      case 'code': {
        target.append(el('pre', { class: 'wv-md-code' }, block.text));
        break;
      }
      case 'quote': {
        const quote = el('blockquote', { class: 'wv-md-quote' });
        appendBlocks(quote, block.blocks);
        target.append(quote);
        break;
      }
      case 'table': {
        const table = el('table', { class: 'wv-md-table' });
        const head = el('tr', {});
        for (const cell of block.header) head.append(el('th', {}, cell));
        table.append(el('thead', {}, head));
        const body = el('tbody', {});
        for (const row of block.rows) {
          const tr = el('tr', {});
          for (const cell of row) tr.append(el('td', {}, cell));
          body.append(tr);
        }
        table.append(body);
        target.append(table);
        break;
      }
    }
  }
}

// ---------------------------------------------------------------------------
// List views: sources, decisions, review records.

function renderSources(body: SnapshotRouteBody): HTMLElement {
  const L = labels();
  const wrap = el('div', { class: 'wv-listview' }, el('h2', {}, L.sourcesTitle), button(L.backToOverview, () => {
    state.view = 'overview';
    render();
    scheduleReport();
  }));
  const entries = sourceEntriesOf(body);
  if (entries.length === 0) {
    wrap.append(el('div', { class: 'wv-state' }, L.sourcesEmpty));
    return wrap;
  }
  for (const entry of entries) {
    const open = state.selectedSource === entry.id;
    const card = el('div', { class: `wv-card${open ? ' wv-card-active' : ''}`, tabindex: '0', role: 'button' }, el('div', { class: 'wv-card-title' }, el('b', {}, entry.id)));
    card.addEventListener('click', () => {
      state.selectedSource = open ? null : entry.id;
      render();
    });
    if (open && entry.sourceYaml !== null) {
      const view = artifactView('source', entry.sourceYaml.text);
      if (view.parseError !== null) {
        card.append(el('div', { class: 'wv-error' }, view.parseError));
      } else {
        card.append(renderSemanticCards('source', view.cards));
      }
      if (entry.original !== null) card.append(renderMarkdownBlocks(entry.original.text));
    }
    wrap.append(card);
  }
  return wrap;
}

function renderDecisions(body: SnapshotRouteBody): HTMLElement {
  const L = labels();
  const wrap = el('div', { class: 'wv-listview' }, el('h2', {}, L.decisionsTitle), button(L.backToOverview, () => {
    state.view = 'overview';
    render();
    scheduleReport();
  }));
  const parsed = decisionEntries(queueTextOf(body).decisions);
  if (parsed.parseError !== null) {
    wrap.append(el('div', { class: 'wv-error' }, L.queueBroken));
    return wrap;
  }
  if (parsed.entries.length === 0) {
    wrap.append(el('div', { class: 'wv-state' }, L.queueEmpty));
    return wrap;
  }
  for (const decision of parsed.entries as DecisionView[]) {
    const card = el('div', { class: 'wv-card' },
      el('div', { class: 'wv-card-title' }, el('b', {}, decision.id), ` · ${decision.status ?? '—'}`, decision.blocking ? ` · ${L.queueBlocking}` : ''),
    );
    if (decision.question !== null && decision.question !== '') card.append(el('div', { class: 'wv-card-row' }, decision.question));
    for (const option of decision.options) {
      card.append(el('div', { class: 'wv-card-row' }, `${option.id === decision.recommendation ? '★ ' : ''}${option.id}：${option.text}`));
    }
    if (decision.rationale !== null && decision.rationale !== '') card.append(el('div', { class: 'wv-muted' }, decision.rationale));
    if (decision.units.length > 0) card.append(el('div', { class: 'wv-muted' }, `${L.queueUnits}：${decision.units.join('、')}`));
    wrap.append(card);
  }
  return wrap;
}

function renderReviewRecords(body: SnapshotRouteBody): HTMLElement {
  const L = labels();
  const wrap = el('div', { class: 'wv-listview' }, el('h2', {}, L.reviewsTitle), button(L.backToOverview, () => {
    state.view = 'overview';
    render();
    scheduleReport();
  }));
  const parsed = reviewEntries(queueTextOf(body).authorReviews);
  if (parsed.parseError !== null) {
    wrap.append(el('div', { class: 'wv-error' }, L.queueBroken));
    return wrap;
  }
  if (parsed.entries.length === 0) {
    wrap.append(el('div', { class: 'wv-state' }, L.queueEmpty));
    return wrap;
  }
  for (const record of parsed.entries as ReviewRecordView[]) {
    wrap.append(el('div', { class: 'wv-card' },
      el('div', { class: 'wv-card-title' }, el('b', {}, record.kind ?? '—'), ` · ${record.work ?? '?'}/${record.ref ?? '?'}`),
      el('div', { class: 'wv-card-row' }, `${L.reviewChannel} ${record.channel ?? '—'}${record.confirmedAt === null ? '' : ` · ${record.confirmedAt}`}${record.reviewedAt === null ? '' : ` → ${record.reviewedAt}`}`),
    ));
  }
  return wrap;
}

// ---------------------------------------------------------------------------
// Review view (native, no iframe).

function renderReviewDetail(rl: ReturnType<typeof reviewLabelsFor>): HTMLElement {
  const wrap = el('div', { class: 'wv-review' }, el('h3', {}, rl.viewTitle),
    button(rl.refresh, () => void loadReviewPage().then(render)),
    button(rl.end, () => void endReview()),
    button(rl.enlarge, () => {
      state.reviewEnlarged = true;
      render();
    }),
  );
  if (state.pageError !== null) wrap.append(el('div', { class: 'wv-error' }, state.pageError));
  const page = state.page;
  if (page === null) {
    wrap.append(el('div', { role: 'status' }, rl.loadingPage));
    return wrap;
  }
  for (const unit of page.units) {
    wrap.append(renderReviewUnit(rl, unit, page));
  }
  if (page.failures.length > 0) {
    const list = el('details', { class: 'wv-details' }, el('summary', {}, rl.failuresTitle));
    for (const failure of page.failures) {
      list.append(el('div', { class: 'wv-diag' }, `${failure.command}（exit ${failure.exit}）`));
    }
    wrap.append(list);
  }
  wrap.append(renderConfirmArea(rl, page));
  if (page.strict) wrap.append(renderAuthorReviewArea(rl, page));
  return wrap;
}

function renderReviewUnit(rl: ReturnType<typeof reviewLabelsFor>, unit: PageUnit, page: ReviewPage): HTMLElement {
  const wrap = el('div', { class: 'wv-review-unit' }, el('div', { class: 'wv-card-title' }, el('b', {}, unit.ref), ` · ${unit.stage}`));
  const layout = illustrationLayout(unit);
  for (const cover of layout.covers) {
    wrap.append(renderIllustration(rl, unit.ref, cover, `${rl.cover} · `));
  }
  if (!unit.hasDraft) {
    wrap.append(el('div', { class: 'wv-muted' }, rl.noDraft));
  }
  for (const paragraph of unit.paragraphs) {
    const originClass = paragraph.origin === 'human' ? 'wv-origin-human' : paragraph.origin === 'agent' ? 'wv-origin-agent' : paragraph.origin === 'agent-draft-human-edited' ? 'wv-origin-edited' : 'wv-origin-unknown';
    const originText = paragraph.origin === 'human' ? rl.originHuman : paragraph.origin === 'agent' ? rl.originAgent : paragraph.origin === 'agent-draft-human-edited' ? rl.originEdited : rl.originUnknown;
    const node = el('p', { class: `wv-paragraph ${originClass}`, 'data-paragraph': String(paragraph.index) },
      el('span', { class: 'wv-paragraph-index' }, `${paragraph.index + 1} · ${originText}`),
      document.createTextNode(paragraph.text),
    );
    node.addEventListener('mouseup', () => {
      const selection = document.getSelection();
      if (selection === null || selection.isCollapsed) return;
      const text = selection.toString();
      const anchor = anchorOf(selection.anchorNode);
      const focus = anchorOf(selection.focusNode);
      const outcome = selectionOutcome(anchorOfUnit(anchor), anchorOfUnit(focus), text);
      if (state.feedback !== null && state.review.unit !== null) {
        state.feedback = feedbackAfterSelection(state.feedback, outcome);
        renderFeedbackBar(rl, node);
      }
    });
    wrap.append(node);
    for (const finding of paragraph.findings) {
      wrap.append(el('div', { class: `wv-diag wv-sev-${finding.severity}` }, `[${finding.label}/${finding.severity}] ${finding.message}${finding.suggestion === '' ? '' : ` → ${finding.suggestion}`}`));
    }
    for (const illustration of layout.byParagraph[paragraph.index] ?? []) {
      wrap.append(renderIllustration(rl, unit.ref, illustration, ''));
    }
  }
  for (const note of unit.notes) wrap.append(el('div', { class: 'wv-muted' }, note));
  for (const finding of unit.global) {
    wrap.append(el('div', { class: `wv-diag wv-sev-${finding.severity}` }, `[${finding.label}/${finding.severity}] ${finding.message}`));
  }
  for (const illustration of layout.unreferenced) {
    wrap.append(renderIllustration(rl, unit.ref, illustration, `${rl.unreferenced} · `));
  }
  void page;
  return wrap;
}

function renderIllustration(rl: ReturnType<typeof reviewLabelsFor>, unitRef: string, illustration: PageIllustration, prefix: string): HTMLElement {
  const meta = `${illustration.content === 'mood' ? rl.mood : rl.infographic} · ${rl.illustrationOrigin} ${illustration.origin}` +
    (illustration.claims.length > 0 ? ` · ${rl.illustrationClaims} ${illustration.claims.join('、')}` : '') +
    (illustration.sources.length > 0 ? ` · ${rl.illustrationSources} ${illustration.sources.join('、')}` : '');
  if (!illustration.available) {
    return el('div', { class: 'wv-illustration wv-illustration-missing' }, el('div', { class: 'wv-card-title' }, `${prefix}${illustration.alt}`), el('div', { class: 'wv-muted' }, rl.illustrationUnavailable));
  }
  const img = el('img', { alt: illustration.alt, width: String(illustration.width), height: String(illustration.height) }) as HTMLImageElement;
  img.src = 'data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw==';
  void illustrationDataUrl(unitRef, illustration.id).then((url) => {
    if (url !== null) img.src = url;
  });
  return el('div', { class: 'wv-illustration' }, el('div', { class: 'wv-card-title' }, `${prefix}${illustration.alt}`), img, el('div', { class: 'wv-muted' }, meta));
}

function renderFeedbackBar(rl: ReturnType<typeof reviewLabelsFor>, paragraphNode: HTMLElement): void {
  let bar = document.getElementById('wv-feedback-bar');
  if (bar === null) {
    bar = el('div', { id: 'wv-feedback-bar', class: 'wv-feedback-bar' });
    document.getElementById('root')?.append(bar);
  }
  bar.replaceChildren();
  const draft = state.feedback;
  if (draft === null) return;
  const text = el('textarea', { class: 'wv-input', rows: '2', placeholder: rl.feedbackText }) as HTMLTextAreaElement;
  text.value = draft.text;
  text.addEventListener('input', () => {
    if (state.feedback !== null) state.feedback = { ...state.feedback, text: text.value };
  });
  const kind = el('select', { class: 'wv-input' }) as HTMLSelectElement;
  for (const [value, labelText] of Object.entries(rl.kinds)) {
    const option = el('option', { value }, labelText) as HTMLOptionElement;
    if (draft.kind === value) option.selected = true;
    kind.append(option);
  }
  kind.addEventListener('change', () => {
    if (state.feedback !== null) state.feedback = { ...state.feedback, kind: kind.value };
  });
  const submit = button(rl.submitFeedback, () => {
    const current = state.feedback;
    if (current === null) return;
    const plan = feedbackRequest(current, '');
    if (!plan.ok) {
      state.notice = plan.reason === 'selection' ? rl.selectionInvalid : rl.feedbackEmpty;
      render();
      return;
    }
    state.notice = null;
    void submitReview('feedback', plan.body);
  });
  const quoteLine = draft.quote === '' ? el('div', { class: 'wv-muted' }, rl.wholeUnit) : el('div', { class: 'wv-muted' }, `${rl.feedbackQuote}：${draft.quote}`);
  const clearQuote = draft.quote === '' ? null : button(rl.clearQuote, () => {
    if (state.feedback !== null) {
      state.feedback = { ...state.feedback, quote: '' };
      renderFeedbackBar(rl, paragraphNode);
    }
  });
  bar.append(el('div', { class: 'wv-card-title' }, rl.feedbackTitle), quoteLine, text, kind, submit);
  if (clearQuote !== null) bar.append(clearQuote);
}

function anchorOf(node: Node | null): { index: number } | null {
  let current: Node | null = node;
  while (current !== null) {
    if (current instanceof HTMLElement && current.hasAttribute('data-paragraph')) {
      return { index: Number(current.getAttribute('data-paragraph')) };
    }
    current = current.parentNode;
  }
  return null;
}

function anchorOfUnit(anchor: { index: number } | null): { unit: string; index: number } | null {
  return anchor === null ? null : { unit: state.review.unit ?? '', index: anchor.index };
}

function renderConfirmArea(rl: ReturnType<typeof reviewLabelsFor>, page: ReviewPage): HTMLElement {
  const wrap = el('details', { class: 'wv-details', open: true }, el('summary', {}, rl.confirmTitle));
  if (page.confirms.length === 0) {
    wrap.append(el('div', { class: 'wv-muted' }, rl.confirmEmpty));
    return wrap;
  }
  for (const item of page.confirms) {
    const key = confirmKey(item);
    const draft = state.confirms[key] ?? initialConfirmDraft(item);
    state.confirms[key] = draft;
    const card = el('div', { class: 'wv-card' }, el('div', { class: 'wv-card-title' }, el('b', {}, item.label || `${item.kind}:${item.id}`)));
    if (item.detail !== '') card.append(el('div', { class: 'wv-muted' }, item.detail));
    const current = () => state.confirms[key] ?? draft;
    const optionRow = el('div', { class: 'wv-card-row' }, `${rl.option}：`);
    for (const option of item.options) {
      const radio = el('input', { type: 'radio', name: `opt-${key}`, value: option.id, ...(current().option === option.id ? { checked: true } : {}) }) as HTMLInputElement;
      radio.addEventListener('change', () => {
        state.confirms[key] = { ...current(), option: option.id };
      });
      optionRow.append(radio, document.createTextNode(`${option.id === item.recommend ? '★ ' : ''}${option.text}  `));
    }
    if (item.options.length > 0) card.append(optionRow);
    const answer = el('input', { class: 'wv-input', type: 'text', placeholder: rl.answer }) as HTMLInputElement;
    answer.value = current().answer;
    answer.addEventListener('input', () => {
      state.confirms[key] = { ...current(), answer: answer.value };
    });
    const confirmation = el('input', { class: 'wv-input', type: 'text', placeholder: rl.confirmation }) as HTMLInputElement;
    confirmation.value = current().confirmation;
    confirmation.addEventListener('input', () => {
      state.confirms[key] = { ...current(), confirmation: confirmation.value };
    });
    if (item.needsAnswer) card.append(answer);
    card.append(confirmation);
    card.append(button(rl.confirm, () => {
      const plan = confirmRequest(item, current(), 'confirm');
      if (!plan.ok) {
        state.notice = plan.reason === 'confirmation' ? rl.needConfirmation : rl.needAnswer;
        render();
        return;
      }
      state.notice = null;
      void submitReview('confirm', plan.body);
    }));
    if (item.canReject) {
      card.append(button(rl.reject, () => {
        const plan = confirmRequest(item, current(), 'reject');
        if (!plan.ok) {
          state.notice = rl.needConfirmation;
          render();
          return;
        }
        state.notice = null;
        void submitReview('confirm', plan.body);
      }));
    }
    wrap.append(card);
  }
  return wrap;
}

function renderAuthorReviewArea(rl: ReturnType<typeof reviewLabelsFor>, page: ReviewPage): HTMLElement {
  const wrap = el('details', { class: 'wv-details', open: true }, el('summary', {}, rl.reviewTitle));
  if (page.reviews.length === 0) {
    wrap.append(el('div', { class: 'wv-muted' }, rl.reviewEmpty));
    return wrap;
  }
  for (const item of page.reviews) {
    const row = el('label', { class: 'wv-card-row' });
    const checkbox = el('input', { type: 'checkbox', ...(state.unticked[item.key] === true ? {} : { checked: true }) }) as HTMLInputElement;
    checkbox.addEventListener('change', () => {
      state.unticked[item.key] = !checkbox.checked;
    });
    row.append(checkbox, document.createTextNode(` ${item.label}`));
    wrap.append(row);
  }
  wrap.append(button(rl.reviewSelected, () => {
    const keys = selectedReviewKeys(page, state.unticked);
    if (keys.length === 0) {
      state.notice = rl.needKeys;
      render();
      return;
    }
    const plan = authorReviewRequest(keys);
    if (plan.ok) void submitReview('author-review', plan.body);
  }));
  return wrap;
}

export const panelState = state;

export function renderReviewOverlay(): HTMLElement {
  const rl = reviewLabelsFor(lang === 'zh' ? 'zh' : 'en');
  const overlay = el('div', { class: 'wv-overlay', tabindex: '-1', 'data-review-overlay': '', role: 'dialog', 'aria-modal': 'true' });
  overlay.addEventListener('keydown', (event) => {
    if (!overlayDismissKey(event)) return;
    event.preventDefault();
    event.stopPropagation();
    state.reviewEnlarged = false;
    render();
  });
  const inner = el('div', { class: 'wv-overlay-body' });
  const restore = button(rl.restore, () => {
    state.reviewEnlarged = false;
    render();
  });
  inner.append(restore, renderReviewDetail(rl));
  overlay.append(inner);
  return overlay;
}

// ---------------------------------------------------------------------------
// Setup section.

function renderSetup(): HTMLElement {
  const sl = setupLabelsFor(lang === 'zh' ? 'zh' : 'en');
  const status = state.setupStatus;
  if (status === null) return el('div', {});
  const view = setupViewModel(status, state.setupInFlight, sl);
  const wrap = el('details', { class: 'wv-details' }, el('summary', {}, sl.title));
  wrap.append(el('div', { class: 'wv-card-row' }, view.headline));
  wrap.append(el('div', { class: 'wv-card-row' }, view.cliLine));
  if (view.resolvedLine !== null) wrap.append(el('div', { class: 'wv-card-row' }, view.resolvedLine));
  if (view.provenanceLine !== null) wrap.append(el('div', { class: 'wv-card-row' }, view.provenanceLine));
  wrap.append(el('div', { class: 'wv-card-row' }, view.skillsLine));
  if (view.error !== null) wrap.append(el('div', { class: 'wv-error' }, `${sl.failed}：${view.error}`));
  wrap.append(button(sl.enable, () => void runSetupEnable(), { disabled: !view.canEnable }));
  if (view.reportRows !== null) {
    const table = el('div', { class: 'wv-source-table' }, el('div', { class: 'wv-card-title' }, sl.sourceTable));
    for (const row of view.reportRows) table.append(el('div', { class: 'wv-card-row' }, row.text));
    wrap.append(table);
  }
  return wrap;
}

// ---------------------------------------------------------------------------
// Boot.

function detectLang(): void {
  const locale = (app.getHostContext() as { locale?: string } | undefined)?.locale
    ?? (typeof navigator !== 'undefined' ? navigator.language : 'en');
  lang = String(locale).toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

async function boot(): Promise<void> {
  // Handlers are installed before connect so early notifications are kept.
  app.ontoolresult = (result) => {
    const structured = (result as { structuredContent?: { surface?: string; unit?: string; view?: string } }).structuredContent;
    if (structured?.surface === 'writing-workspace') {
      if (structured.unit !== undefined) {
        const unit = String(structured.unit);
        state.selectedUnit = unit;
        state.selectedWork = unit.split('/')[0] ?? unit;
        state.artifact = null;
      } else if (structured?.view !== undefined) {
        const view = String(structured.view);
        if (view === 'sources' || view === 'decisions' || view === 'reviews') state.view = view;
      }
      render();
      scheduleReport();
    }
  };
  app.onhostcontextchanged = () => {
    detectLang();
    render();
  };
  await app.connect();
  detectLang();
  render();
  await loadSnapshot();
  await probeSetup();
  window.addEventListener('focus', () => void pollSession());
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') void pollSession();
  });
  window.setInterval(() => void pollSession(), SESSION_POLL_MS);
}

// The jsdom test file sets this flag before importing the module so the
// panel renders without a host connection.
declare global { interface Window { __WV_PANEL_TEST__?: boolean } }
if (typeof window === 'undefined' || window.__WV_PANEL_TEST__ !== true) {
  void boot();
}
