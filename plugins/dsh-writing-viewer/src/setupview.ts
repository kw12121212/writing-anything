/**
 * Pure view model for the panel's「内置技能与 CLI」setup section.
 *
 * The section exists only while the host serves the setup route — the client
 * probes `GET /dsh-writing-viewer/setup` once on mount and keeps the section
 * hidden unless a well-formed status comes back (a 404 means `enableSetup`
 * is off and the host registered no route). Everything rendered is derived
 * here, pure of React and the DOM, per the client module conventions.
 */

/** Client-side mirror of the setup status route body. */
export interface SetupStatus {
  ok: true;
  enabled: true;
  busy: boolean;
  cli: { ready: boolean; path: string | null; version: string | null; resolved: string | null; external: string | null };
  skills: { registered: boolean; names: string[] };
  lastError: string | null;
}

/** Parse a fetch json body into a SetupStatus, or null when the response is
 * not a setup status at all (404 shape, snapshot body, malformed). */
export function parseSetupStatus(body: unknown): SetupStatus | null {
  if (body === null || typeof body !== 'object') return null;
  const raw = body as Record<string, unknown>;
  if (raw.ok !== true || raw.enabled !== true) return null;
  const cli = raw.cli as SetupStatus['cli'] | undefined;
  const skills = raw.skills as SetupStatus['skills'] | undefined;
  if (cli === null || typeof cli !== 'object' || typeof cli.ready !== 'boolean') return null;
  if (skills === null || typeof skills !== 'object' || !Array.isArray(skills.names)) return null;
  return {
    ok: true,
    enabled: true,
    busy: raw.busy === true,
    cli: {
      ready: cli.ready,
      path: typeof cli.path === 'string' ? cli.path : null,
      version: typeof cli.version === 'string' ? cli.version : null,
      resolved: typeof cli.resolved === 'string' ? cli.resolved : null,
      external: typeof cli.external === 'string' ? cli.external : null,
    },
    skills: { registered: skills.registered === true, names: skills.names.filter((n): n is string => typeof n === 'string') },
    lastError: typeof raw.lastError === 'string' && raw.lastError !== '' ? raw.lastError : null,
  };
}

/** Section labels, bilingual like the panel's own text table. */
export interface SetupLabels {
  title: string;
  notEnabled: string;
  enabled: string;
  busy: string;
  enable: string;
  cliReady: string;
  cliMissing: string;
  version: string;
  resolvedTo: string;
  externalActive: string;
  skillsRegistered: string;
  skillsMissing: string;
  failed: string;
}

const SETUP_TEXT: Record<'zh' | 'en', SetupLabels> = {
  zh: {
    title: '内置技能与 CLI（Setup）',
    notEnabled: '尚未启用：点击下方按钮安装内置 wa-cli 并注册内置技能',
    enabled: '已启用：外部技能与 CLI 优先，内置版本作为后备',
    busy: '启用进行中…',
    enable: '启用内置技能与 CLI',
    cliReady: '内置 CLI',
    cliMissing: '内置 CLI 未就绪',
    version: '版本',
    resolvedTo: 'wa-cli 解析为',
    externalActive: '外部版本生效，内置为后备',
    skillsRegistered: '已注册内置技能',
    skillsMissing: '内置技能未注册',
    failed: '启用失败',
  },
  en: {
    title: 'Built-in skills & CLI (setup)',
    notEnabled: 'Not enabled yet: click below to install the built-in wa-cli and register the built-in skills',
    enabled: 'Enabled: external skills and CLI win, the built-in versions serve as fallback',
    busy: 'Enabling…',
    enable: 'Enable built-in skills & CLI',
    cliReady: 'Built-in CLI',
    cliMissing: 'Built-in CLI not ready',
    version: 'Version',
    resolvedTo: 'wa-cli resolves to',
    externalActive: 'External binary active, built-in as fallback',
    skillsRegistered: 'Registered built-in skills',
    skillsMissing: 'Built-in skills not registered',
    failed: 'Enable failed',
  },
};

/** Labels for the active page language, detected like the panel's own text
 * table (document lang, then navigator.language, defaulting to English). */
export function setupLabelsFor(active?: string | null): SetupLabels {
  const lang = String(
    active || (typeof document !== 'undefined' && document.documentElement.lang) ||
      (typeof navigator !== 'undefined' && navigator.language) || 'en',
  ).toLowerCase();
  return lang.indexOf('zh') === 0 ? SETUP_TEXT.zh : SETUP_TEXT.en;
}

/** Derived section state for rendering and tests. */
export interface SetupViewModel {
  /** idle = not enabled yet; busy = action in flight; enabled = complete;
   * error = last action failed (section stays usable for retry). */
  state: 'idle' | 'busy' | 'enabled' | 'error';
  headline: string;
  cliLine: string;
  resolvedLine: string | null;
  externalLine: string | null;
  skillsLine: string;
  error: string | null;
  /** The enable button renders disabled unless this is true. */
  canEnable: boolean;
  skillNames: string[];
}

/** Derive the whole section view model from the status plus the client-side
 * in-flight flag (the button follows the client action, not only the
 * server-reported busy state, so a second click can never queue). */
export function setupViewModel(status: SetupStatus, actionInFlight: boolean, labels: SetupLabels): SetupViewModel {
  const complete = status.cli.ready && status.skills.registered;
  const state: SetupViewModel['state'] = actionInFlight || status.busy
    ? 'busy'
    : status.lastError !== null
      ? 'error'
      : complete
        ? 'enabled'
        : 'idle';
  const cliLine = status.cli.ready
    ? `${labels.cliReady}：${status.cli.path ?? '—'}${status.cli.version === null ? '' : `（${labels.version} ${status.cli.version}）`}`
    : labels.cliMissing;
  const resolvedLine = status.cli.resolved === null ? null : `${labels.resolvedTo}：${status.cli.resolved}`;
  // The active-external line only renders when the external binary is what
  // actually resolves (resolved === external). When the builtin dir already
  // sits ahead of an external wa-cli on PATH, claiming "external active"
  // would contradict the resolved line right above it.
  const externalLine =
    status.cli.external !== null && status.cli.resolved === status.cli.external
      ? `${labels.externalActive}：${status.cli.external}`
      : null;
  const skillsLine = status.skills.registered
    ? `${labels.skillsRegistered}（${status.skills.names.length}）`
    : labels.skillsMissing;
  return {
    state,
    headline: state === 'busy' ? labels.busy : state === 'enabled' ? labels.enabled : labels.notEnabled,
    cliLine,
    resolvedLine,
    externalLine,
    skillsLine,
    error: state === 'error' ? status.lastError : null,
    canEnable: !actionInFlight && !status.busy,
    skillNames: [...status.skills.names],
  };
}
