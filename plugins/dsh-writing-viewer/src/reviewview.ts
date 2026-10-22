/**
 * Pure view model for the panel's review (审阅) control in the unit detail.
 *
 * The author clicks「审阅」, the client POSTs the session start route, and on
 * success the detail area embeds the same-origin proxied reading view in an
 * iframe with an「结束审阅」button. Failures stay readable: a refused origin
 * (the usual cause over Tailscale is the MagicDNS authority missing from
 * allowedHosts) names the authority to allow, a start failure shows wa-cli's
 * reason, and a session that ended elsewhere offers a restart. Everything
 * rendered is derived here, pure of React and the DOM.
 */

export const REVIEW_SESSION_ROUTE = '/dsh-writing-viewer/review-session';
export const REVIEW_SESSION_END_ROUTE = '/dsh-writing-viewer/review-session/end';
/** The iframe source: the proxy's page path (trailing slash so the reading
 * view's relative submits resolve under the proxy prefix). */
export const REVIEW_FRAME_SRC = '/dsh-writing-viewer/review/';

/** Client-side review state, one per mounted panel. */
export interface ReviewPanelState {
  /** Unit the review belongs to; null when no review was started. */
  unit: string | null;
  phase: 'idle' | 'starting' | 'active' | 'error' | 'ended';
  /** Readable failure text (phase error). */
  error: string | null;
  /** The failure was the Request Origin Guard refusing this origin. */
  forbidden: boolean;
  /** Changes on every successful start so the iframe reloads. */
  startedAt: string | null;
}

export const IDLE_REVIEW: ReviewPanelState = { unit: null, phase: 'idle', error: null, forbidden: false, startedAt: null };

export interface ReviewLabels {
  review: string;
  starting: string;
  end: string;
  restart: string;
  failed: string;
  forbidden: string;
  ended: string;
  frameTitle: string;
}

const REVIEW_TEXT: Record<'zh' | 'en', ReviewLabels> = {
  zh: {
    review: '审阅',
    starting: '正在启动阅读页…',
    end: '结束审阅',
    restart: '重新审阅',
    failed: '审阅启动失败',
    forbidden: '审阅路由被请求来源校验拒绝：请在插件配置 allowedHosts 中放行当前访问的地址 {host}（例如 Tailscale MagicDNS 名与端口），重启 DSH 后重试',
    ended: '审阅已结束（结束审阅、空闲超时、被另一单元的审阅替换或阅读页服务退出）',
    frameTitle: '阅读页（Reading View）',
  },
  en: {
    review: 'Review',
    starting: 'Starting the reading view…',
    end: 'End review',
    restart: 'Review again',
    failed: 'Review failed to start',
    forbidden: 'The review routes were refused by the request origin guard: add the address you are using, {host} (for example a Tailscale MagicDNS name and port), to the plugin config allowedHosts, restart DSH and retry',
    ended: 'The review has ended (ended explicitly, idle timeout, replaced by another unit\'s review, or the reading view exited)',
    frameTitle: 'Reading view',
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

/** The start request body: the unit plus the directory the panel shows. */
export function reviewStartBody(unit: string, root: string | null): string {
  return JSON.stringify(root === null ? { unit } : { unit, root });
}

/** Fold one start answer (HTTP status plus parsed JSON, or null) into state. */
export function reviewAfterStart(unit: string, status: number, body: unknown): ReviewPanelState {
  const raw = (body ?? {}) as { ok?: unknown; error?: unknown; session?: { startedAt?: unknown } | null };
  if (status === 403) {
    return { unit, phase: 'error', error: typeof raw.error === 'string' ? raw.error : null, forbidden: true, startedAt: null };
  }
  if (raw.ok === true && raw.session !== null && typeof raw.session === 'object') {
    const startedAt = typeof raw.session.startedAt === 'string' ? raw.session.startedAt : String(Date.now());
    return { unit, phase: 'active', error: null, forbidden: false, startedAt };
  }
  const error = typeof raw.error === 'string' && raw.error !== '' ? raw.error : `HTTP ${status}`;
  return { unit, phase: 'error', error, forbidden: false, startedAt: null };
}

/** Fold a status poll after the iframe loaded: the review has ended when
 * there is no session at all or the one session belongs to another unit
 * (another tab started a different review; a summary without a unit is a
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
  /** iframe for the detail's unit; key changes per start so it reloads. */
  frame: { src: string; key: string; title: string } | null;
  showEnd: boolean;
  endedText: string | null;
}

/** Derive what the detail renders for unit `ref` from the panel's review state. */
export function reviewViewModel(state: ReviewPanelState, ref: string, labels: ReviewLabels, host: string): ReviewViewModel {
  const mine = state.unit === ref;
  const phase = mine ? state.phase : 'idle';
  const errorText = phase !== 'error'
    ? null
    : state.forbidden
      ? labels.forbidden.replace('{host}', host) + (state.error === null ? '' : `（${state.error}）`)
      : `${labels.failed}：${state.error ?? ''}`;
  return {
    showStart: phase !== 'active',
    startDisabled: state.phase === 'starting',
    busyText: phase === 'starting' ? labels.starting : null,
    errorText,
    frame: phase === 'active' ? { src: REVIEW_FRAME_SRC, key: 'review-' + (state.startedAt ?? ''), title: labels.frameTitle } : null,
    showEnd: phase === 'active',
    endedText: phase === 'ended' ? labels.ended : null,
  };
}
