/**
 * Pure view model for the panel's「内置技能与 CLI」setup section.
 *
 * The section exists only while the server serves the setup_status tool —
 * the panel probes it once on mount and keeps the section hidden unless a
 * well-formed status comes back (an error means `enableSetup` is off and the
 * server registered no setup tools). Everything rendered is derived here,
 * pure of the DOM, per the panel module conventions.
 */

/** One row of the skill source report mirror: `external` is where an
 * external same-name SKILL.md was found (existence only — which copy ZCode
 * actually loads is the host's precedence, not claimed here). */
export interface SkillReportRow {
  name: string;
  external: { source: string; path: string } | null;
}

/** Client-side mirror of the setup_status tool result. */
export interface SetupStatus {
  ok: true;
  enabled: true;
  busy: boolean;
  cli: { ready: boolean; path: string | null; version: string | null; resolved: string | null; pluginInstalled: boolean };
  skills: { bundled: boolean; names: string[]; report: SkillReportRow[] | null };
  lastError: string | null;
}

/** Parse a tool result into a SetupStatus, or null when it is not a setup
 * status at all (tool missing, snapshot body, malformed). */
export function parseSetupStatus(body: unknown): SetupStatus | null {
  if (body === null || typeof body !== 'object') return null;
  const raw = body as Record<string, unknown>;
  if (raw.ok !== true || raw.enabled !== true) return null;
  const cli = raw.cli as SetupStatus['cli'] | undefined;
  const skills = raw.skills as SetupStatus['skills'] | undefined;
  if (cli === null || typeof cli !== 'object' || typeof cli.ready !== 'boolean') return null;
  if (skills === null || typeof skills !== 'object' || !Array.isArray(skills.names)) return null;
  const reportRows: SkillReportRow[] = [];
  if (Array.isArray(skills.report)) {
    for (const row of skills.report) {
      if (row === null || typeof row !== 'object') continue;
      const entry = row as unknown as Record<string, unknown>;
      if (typeof entry.name !== 'string') continue;
      const ext = entry.external as Record<string, unknown> | null | undefined;
      reportRows.push({
        name: entry.name,
        external:
          ext !== null && typeof ext === 'object' && typeof ext.source === 'string' && typeof ext.path === 'string'
            ? { source: ext.source, path: ext.path }
            : null,
      });
    }
  }
  return {
    ok: true,
    enabled: true,
    busy: raw.busy === true,
    cli: {
      ready: cli.ready,
      path: typeof cli.path === 'string' ? cli.path : null,
      version: typeof cli.version === 'string' ? cli.version : null,
      resolved: typeof cli.resolved === 'string' ? cli.resolved : null,
      pluginInstalled: cli.pluginInstalled === true,
    },
    skills: {
      bundled: skills.bundled === true,
      names: skills.names.filter((n): n is string => typeof n === 'string'),
      report: Array.isArray(skills.report) ? reportRows : null,
    },
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
  skillsBundled: string;
  failed: string;
  sourceTable: string;
  externalSource: string;
  bundledSource: string;
  pluginInstalled: string;
  notPluginInstalled: string;
}

const SETUP_TEXT: Record<'zh' | 'en', SetupLabels> = {
  zh: {
    title: '内置技能与 CLI（Setup）',
    notEnabled: '尚未启用：点击下方按钮安装内置 wa-cli（技能已随插件捆绑）',
    enabled: '已启用：内置 CLI 就绪，技能随插件捆绑',
    busy: '启用进行中…',
    enable: '启用内置技能与 CLI',
    cliReady: '内置 CLI',
    cliMissing: '内置 CLI 未就绪',
    version: '版本',
    resolvedTo: 'wa-cli 解析为',
    skillsBundled: '随插件捆绑的技能',
    failed: '启用失败',
    sourceTable: '技能来源对照',
    externalSource: '外部',
    bundledSource: '捆绑',
    pluginInstalled: '插件安装',
    notPluginInstalled: '非插件安装',
  },
  en: {
    title: 'Built-in skills & CLI (setup)',
    notEnabled: 'Not enabled yet: click below to install the built-in wa-cli (the skills ship with the plugin)',
    enabled: 'Enabled: built-in CLI ready, skills bundled with the plugin',
    busy: 'Enabling…',
    enable: 'Enable built-in skills & CLI',
    cliReady: 'Built-in CLI',
    cliMissing: 'Built-in CLI not ready',
    version: 'Version',
    resolvedTo: 'wa-cli resolves to',
    skillsBundled: 'Skills bundled with the plugin',
    failed: 'Enable failed',
    sourceTable: 'Skill source report',
    externalSource: 'External',
    bundledSource: 'Bundled',
    pluginInstalled: 'installed by this plugin',
    notPluginInstalled: 'not installed by this plugin',
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
  skillsLine: string;
  error: string | null;
  /** The enable button renders disabled unless this is true. */
  canEnable: boolean;
  skillNames: string[];
  /** Provenance of the ready CLI: plugin-installed (marker verified) or not. */
  provenanceLine: string | null;
  /** Source report rows as display text; null when degraded/absent. */
  reportRows: { name: string; text: string }[] | null;
}

/** Derive the whole section view model from the status plus the panel-side
 * in-flight flag (the button follows the panel action, not only the
 * server-reported busy state, so a second click can never queue). */
export function setupViewModel(status: SetupStatus, actionInFlight: boolean, labels: SetupLabels): SetupViewModel {
  const complete = status.cli.ready;
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
  const skillsLine = status.skills.bundled
    ? `${labels.skillsBundled}（${status.skills.names.length}）`
    : labels.skillsBundled;
  return {
    state,
    headline: state === 'busy' ? labels.busy : state === 'enabled' ? labels.enabled : labels.notEnabled,
    cliLine,
    resolvedLine,
    skillsLine,
    error: state === 'error' ? status.lastError : null,
    canEnable: !actionInFlight && !status.busy,
    skillNames: [...status.skills.names],
    provenanceLine: status.cli.ready
      ? status.cli.pluginInstalled
        ? labels.pluginInstalled
        : labels.notPluginInstalled
      : null,
    reportRows:
      status.skills.report === null
        ? null
        : status.skills.report.map((row) => ({
            name: row.name,
            text:
              row.external === null
                ? `${row.name} → ${labels.bundledSource}`
                : `${row.name} → ${labels.externalSource} ${row.external.source}（${row.external.path}）`,
          })),
  };
}
