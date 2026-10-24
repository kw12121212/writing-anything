/**
 * Pure view model for the panel's review (审阅) control in the unit detail.
 *
 * The author clicks「审阅」(or a「待审阅」marker), the panel calls the
 * review_start tool, and on success the detail area shows the review view
 * (审阅视图, Review View), rendered natively from the review_page tool's data
 * (see ./reviewmodel), with「刷新」and「结束审阅」buttons. Failures stay
 * readable: a start failure shows wa-cli's reason, and a session that ended
 * elsewhere offers a restart. Everything rendered is derived here, pure of
 * the DOM.

 */

/** Client-side review state, one per mounted panel. */
export interface ReviewPanelState {
  /** Unit the review belongs to; null when no review was started. */
  unit: string | null;
  phase: 'idle' | 'starting' | 'active' | 'error' | 'ended';
  /** Readable failure text (phase error). */
  error: string | null;
  /** Changes on every successful start so the view reloads its data. */
  startedAt: string | null;
}

export const IDLE_REVIEW: ReviewPanelState = { unit: null, phase: 'idle', error: null, startedAt: null };

export interface ReviewLabels {
  review: string;
  starting: string;
  end: string;
  restart: string;
  failed: string;
  ended: string;
  viewTitle: string;
  refresh: string;
  enlarge: string;
  restore: string;
  loadingPage: string;
  pageFailed: string;
  originHuman: string;
  originAgent: string;
  originEdited: string;
  originUnknown: string;
  originColors: string;
  noDraft: string;
  failuresTitle: string;
  feedbackTitle: string;
  feedbackUnit: string;
  feedbackQuote: string;
  wholeUnit: string;
  clearQuote: string;
  feedbackText: string;
  feedbackKind: string;
  kinds: Record<string, string>;
  submitFeedback: string;
  marks: Record<string, string>;
  selectionInvalid: string;
  feedbackEmpty: string;
  feedbackSaved: string;
  confirmTitle: string;
  confirmEmpty: string;
  option: string;
  recommended: string;
  answer: string;
  confirmation: string;
  confirm: string;
  reject: string;
  needConfirmation: string;
  needAnswer: string;
  confirmed: string;
  reviewTitle: string;
  reviewEmpty: string;
  reviewSelected: string;
  needKeys: string;
  reviewed: string;
  pending: string;
  cover: string;
  unreferenced: string;
  mood: string;
  infographic: string;
  illustrationOrigin: string;
  illustrationClaims: string;
  illustrationSources: string;
  illustrationUnavailable: string;
}

const REVIEW_TEXT: Record<'zh' | 'en', ReviewLabels> = {
  zh: {
    review: '审阅',
    starting: '正在启动阅读页…',
    end: '结束审阅',
    restart: '重新审阅',
    failed: '审阅启动失败',
    ended: '审阅已结束（结束审阅、空闲超时、被另一单元的审阅替换或阅读页服务退出）',
    viewTitle: '审阅视图（Review View）',
    refresh: '刷新',
    enlarge: '放大',
    restore: '还原',
    loadingPage: '正在读取阅读页数据…',
    pageFailed: '阅读页数据读取失败',
    originHuman: '作者',
    originAgent: 'Agent',
    originEdited: 'Agent 起草、作者修改',
    originUnknown: '出处未知',
    originColors: '按出处着色',
    noDraft: '还没有正文',
    failuresTitle: '最近失败的命令',
    feedbackTitle: '提交意见',
    feedbackUnit: '单元',
    feedbackQuote: '引用',
    wholeUnit: '（未选中文字，意见针对整个单元）',
    clearQuote: '改为针对整个单元',
    feedbackText: '意见',
    feedbackKind: '类型',
    kinds: { suggestion: '建议', 'must-fix': '必须修改', question: '疑问', praise: '肯定' },
    submitFeedback: '提交意见',
    marks: { stuck: '阅读卡点', good: '好', question: '疑问' },
    selectionInvalid: '请只在一个段落内选中文字，或改为针对整个单元提交',
    feedbackEmpty: '请先填写意见，或使用快捷标记',
    feedbackSaved: '已保存作者反馈',
    confirmTitle: '待作者确认',
    confirmEmpty: '没有待作者确认的事项',
    option: '选项',
    recommended: '推荐',
    answer: '作者的答复',
    confirmation: '作者原话摘要（必填）',
    confirm: '确认',
    reject: '拒绝',
    needConfirmation: '请先填写作者原话摘要',
    needAnswer: '请先填写作者的答复',
    confirmed: '已记录作者确认（渠道 preview）',
    reviewTitle: '待作者复核',
    reviewEmpty: '没有待作者复核的确认',
    reviewSelected: '复核所选',
    needKeys: '请至少勾选一项',
    reviewed: '已复核（渠道 preview）',
    pending: '待审阅',
    cover: '封面',
    unreferenced: '未引用',
    mood: '氛围图',
    infographic: '信息图',
    illustrationOrigin: '出处',
    illustrationClaims: '论断',
    illustrationSources: '素材',
    illustrationUnavailable: '配图不可用：图片缺失、被改动或未通过校验，请运行 wa-cli illustration list 查看',
  },
  en: {
    review: 'Review',
    starting: 'Starting the reading view…',
    end: 'End review',
    restart: 'Review again',
    failed: 'Review failed to start',
    ended: 'The review has ended (ended explicitly, idle timeout, replaced by another unit\'s review, or the reading view exited)',
    viewTitle: 'Review view',
    refresh: 'Refresh',
    enlarge: 'Enlarge',
    restore: 'Restore',
    loadingPage: 'Loading the reading view data…',
    pageFailed: 'Could not load the reading view data',
    originHuman: 'Author',
    originAgent: 'Agent',
    originEdited: 'Agent draft, author edited',
    originUnknown: 'Unknown origin',
    originColors: 'Color by origin',
    noDraft: 'No draft yet',
    failuresTitle: 'Recent failed commands',
    feedbackTitle: 'Feedback',
    feedbackUnit: 'Unit',
    feedbackQuote: 'Quote',
    wholeUnit: '(no text selected; the feedback is about the whole unit)',
    clearQuote: 'About the whole unit',
    feedbackText: 'Feedback',
    feedbackKind: 'Kind',
    kinds: { suggestion: 'Suggestion', 'must-fix': 'Must fix', question: 'Question', praise: 'Praise' },
    submitFeedback: 'Submit feedback',
    marks: { stuck: 'Stuck here', good: 'Good', question: 'Question' },
    selectionInvalid: 'Select text inside a single paragraph, or submit about the whole unit',
    feedbackEmpty: 'Write the feedback first, or use a quick mark',
    feedbackSaved: 'Feedback saved',
    confirmTitle: 'Pending author confirmation',
    confirmEmpty: 'Nothing waits for the author\'s confirmation',
    option: 'Option',
    recommended: 'recommended',
    answer: 'Author\'s answer',
    confirmation: 'Summary of the author\'s words (required)',
    confirm: 'Confirm',
    reject: 'Reject',
    needConfirmation: 'Fill in the summary of the author\'s words first',
    needAnswer: 'Fill in the author\'s answer first',
    confirmed: 'Author confirmation recorded (channel preview)',
    reviewTitle: 'Pending author review',
    reviewEmpty: 'No confirmations wait for author review',
    reviewSelected: 'Review selected',
    needKeys: 'Tick at least one item',
    reviewed: 'Reviewed (channel preview)',
    pending: 'Pending review',
    cover: 'Cover',
    unreferenced: 'Not referenced',
    mood: 'Mood image',
    infographic: 'Infographic',
    illustrationOrigin: 'origin',
    illustrationClaims: 'claims',
    illustrationSources: 'sources',
    illustrationUnavailable: 'Illustration unavailable: the image is missing, changed or failed validation; run wa-cli illustration list',
  },
};

/** Labels for the active page language, detected like the panel's own table. */
export function reviewLabelsFor(active?: string | null): ReviewLabels {
  const lang = String(
    active || (typeof document !== 'undefined' && document.documentElement.lang) ||
      (typeof navigator !== 'undefined' && navigator.language) || 'en',
  ).toLowerCase();
  return lang.indexOf('zh') === 0 ? REVIEW_TEXT.zh : REVIEW_TEXT.en;
}

/** The start request payload: the unit plus the directory the panel shows. */
export function reviewStartBody(unit: string, root: string | null): { unit: string; root?: string } {
  return root === null ? { unit } : { unit, root };
}

/** The review_start tool's result shape, as the panel receives it. */
export type ReviewStartOutcome =
  | { ok: true; session: { unit: string; root: string; startedAt: string } }
  | { ok: false; error: string };

/** Fold one start answer into state. */
export function reviewAfterStart(unit: string, outcome: ReviewStartOutcome): ReviewPanelState {
  if (outcome.ok === true && outcome.session !== null && typeof outcome.session === 'object') {
    const startedAt = typeof outcome.session.startedAt === 'string' ? outcome.session.startedAt : String(Date.now());
    return { unit, phase: 'active', error: null, startedAt };
  }
  const error = !outcome.ok && typeof outcome.error === 'string' && outcome.error !== '' ? outcome.error : '未知错误';
  return { unit, phase: 'error', error, startedAt: null };
}

/** Fold a status poll (after each data load, on focus or visibility): the review has ended when
 * there is no session at all or the one session belongs to another unit
 * (another session started a different review; a summary without a unit is a
 * malformed payload and degrades the same safe way). */
export function reviewAfterStatus(state: ReviewPanelState, body: unknown): ReviewPanelState {
  if (state.phase !== 'active') return state;
  const session = (body as { session?: unknown } | null)?.session;
  const ours = session !== null && session !== undefined && (session as { unit?: unknown }).unit === state.unit;
  return ours ? state : { ...state, phase: 'ended' };
}

export interface ReviewViewModel {
  /** The「审阅」button for the detail's unit, unless that unit's review shows. */
  showStart: boolean;
  startDisabled: boolean;
  busyText: string | null;
  errorText: string | null;
  /** The review view shows for the detail's unit. */
  active: boolean;
  showEnd: boolean;
  endedText: string | null;
}

/** Derive what the detail renders for unit `ref` from the panel's review state. */
export function reviewViewModel(state: ReviewPanelState, ref: string, labels: ReviewLabels): ReviewViewModel {
  const mine = state.unit === ref;
  const phase = mine ? state.phase : 'idle';
  const errorText = phase !== 'error' ? null : `${labels.failed}：${state.error ?? ''}`;
  return {
    showStart: phase !== 'active',
    startDisabled: state.phase === 'starting',
    busyText: phase === 'starting' ? labels.starting : null,
    errorText,
    active: phase === 'active',
    showEnd: phase === 'active',
    endedText: phase === 'ended' ? labels.ended : null,
  };
}

/** The review data or a submit said the session is gone: show the ended
 * notice with a restart entry. */
export function reviewEnded(state: ReviewPanelState): ReviewPanelState {
  return state.phase === 'active' ? { ...state, phase: 'ended' } : state;
}

/** Whether the enlarged overlay covers the panel: only while its own review
 * session is still active. A session that ends anywhere — explicit end, idle
 * timeout, replacement by another unit — collapses the overlay on its own;
 * starting a review resets the flag so a new session opens in the detail. */
export function reviewOverlayShown(state: ReviewPanelState, enlarged: boolean): boolean {
  return enlarged && state.phase === 'active';
}

/** Whether a keydown collapses the enlarged overlay: the Escape key. Pure so
 * the dismiss decision and the event bookkeeping stay testable without a DOM. */
export function overlayDismissKey(event: unknown): boolean {
  return String((event as { key?: unknown })?.key ?? '') === 'Escape';
}
