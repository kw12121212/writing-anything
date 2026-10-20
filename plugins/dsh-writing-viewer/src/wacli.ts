/**
 * Read-only wa-cli bridge for the snapshot: structural state comes from
 * `wa-cli status --json`, diagnostics from `wa-cli validate --json`, the
 * pending author-review summary from `wa-cli next --json`, and the novel
 * timeline from `wa-cli timeline <work> --json`.
 *
 * wa-cli is the single source of truth for anything derived; this module only
 * maps its output and degrades per area when the binary is missing, times
 * out, or does not speak the expected shape. Exit codes are not treated as
 * failure: status and validate legitimately exit 1 while still printing a
 * full JSON envelope describing an invalid workspace.
 */

/** Result of one CLI invocation: parsed stdout JSON, or null when unavailable. */
export interface CliOutcome<T> {
  /** Parsed JSON envelope, or null when the command could not run / parse. */
  output: T | null;
  /** Human-readable reason recorded in the snapshot when output is null. */
  reason: string | null;
}

/** Injected executor: runs the wa-cli binary, no shell, argument list only.
 * The optional cwd lets the setup path run commands inside a workspace-free
 * temporary directory (the snapshot itself always spawns with the host cwd). */
export interface CliRunner {
  run(args: readonly string[], options?: { cwd?: string }): Promise<{ stdout: string } | null>;
}

/** A wa-cli status --json units[] row, as far as the snapshot consumes it. */
export interface StatusUnit {
  unit: string;
  work: string;
  type: string | null;
  parent: string | null;
  stage: string | null;
  blockers: unknown[];
  progress: { done: number; total: number } | null;
  commands: string[];
}

/** A wa-cli status --json envelope, as far as the snapshot consumes it. */
export interface StatusOutput {
  workspace: string | null;
  config: Record<string, unknown> | null;
  sources: number | null;
  works: number | null;
  units: StatusUnit[];
}

/** A wa-cli validate --json envelope. */
export interface ValidateOutput {
  valid: boolean | null;
  diagnostics: unknown[];
  checked: string[];
}

/** A wa-cli next --json envelope (pending author-review summary). */
export interface NextOutput {
  steps: unknown[];
  pendingAuthorReviews: unknown[];
}

/** A wa-cli timeline --json envelope. */
export interface TimelineOutput {
  timeline: { unit: string; time: string | null; after: string[]; position: number }[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function asString(value: unknown): string | null {
  return typeof value === 'string' ? value : null;
}

function asNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

/** Run one wa-cli read command with --dir <root> and parse its JSON envelope. */
export async function runCliJson<T>(
  runner: CliRunner,
  root: string,
  args: readonly string[],
  area: string,
): Promise<CliOutcome<T>> {
  // wa-cli takes the subcommand first: usage is `wa-cli status --dir D --json`;
  // a leading --dir is rejected with usage.missing-command.
  const result = await runner.run([...args, '--dir', root, '--json']);
  if (result === null) {
    return { output: null, reason: `${area}: wa-cli 不可用或执行失败，该部分数据降级` };
  }
  try {
    return { output: JSON.parse(result.stdout) as T, reason: null };
  } catch (error) {
    return { output: null, reason: `${area}: wa-cli 输出无法解析（${String(error)}），该部分数据降级` };
  }
}

/** Map a raw status envelope onto the consumed shape; missing fields degrade. */
export function mapStatus(raw: unknown): CliOutcome<StatusOutput> {
  if (!isRecord(raw)) return { output: null, reason: 'status: 输出不是对象，该部分数据降级' };
  const units: StatusUnit[] = [];
  const rawUnits = asArray(raw.units);
  for (const entry of rawUnits) {
    if (!isRecord(entry)) continue;
    const progress = isRecord(entry.progress)
      ? { done: asNumber(entry.progress.done) ?? 0, total: asNumber(entry.progress.total) ?? 0 }
      : null;
    units.push({
      unit: asString(entry.unit) ?? '',
      work: asString(entry.work) ?? '',
      type: asString(entry.type),
      parent: asString(entry.parent),
      stage: asString(entry.stage),
      blockers: asArray(entry.blockers),
      progress,
      commands: asArray(entry.commands).filter((c): c is string => typeof c === 'string'),
    });
  }
  const output: StatusOutput = {
    workspace: asString(raw.workspace),
    config: isRecord(raw.config) ? raw.config : null,
    sources: asNumber(raw.sources),
    works: asNumber(raw.works),
    units,
  };
  const missing: string[] = [];
  if (output.units.length === 0 && rawUnits.length > 0) missing.push('units');
  if (output.config === null) missing.push('config');
  if (output.sources === null) missing.push('sources');
  if (output.works === null) missing.push('works');
  if (missing.length > 0) {
    return { output, reason: `status: 缺少字段 ${missing.join('、')}，该部分按降级标注呈现` };
  }
  return { output, reason: null };
}

/** Map a raw validate envelope onto the consumed shape; missing fields degrade. */
export function mapValidate(raw: unknown): CliOutcome<ValidateOutput> {
  if (!isRecord(raw)) return { output: null, reason: 'validate: 输出不是对象，该部分数据降级' };
  const output: ValidateOutput = {
    valid: typeof raw.valid === 'boolean' ? raw.valid : null,
    diagnostics: asArray(raw.diagnostics),
    checked: asArray(raw.checked).filter((c): c is string => typeof c === 'string'),
  };
  const missing: string[] = [];
  if (output.valid === null) missing.push('valid');
  if (!Array.isArray(raw.diagnostics)) missing.push('diagnostics');
  if (missing.length > 0) {
    return { output, reason: `validate: 缺少字段 ${missing.join('、')}，该部分按降级标注呈现` };
  }
  return { output, reason: null };
}

/** Map a raw next envelope onto the consumed shape. */
export function mapNext(raw: unknown): CliOutcome<NextOutput> {
  if (!isRecord(raw)) return { output: null, reason: 'next: 输出不是对象，该部分数据降级' };
  const pending = asArray(raw.pendingAuthorReviews);
  return {
    output: {
      steps: asArray(raw.steps),
      pendingAuthorReviews: pending,
    },
    reason: raw.pendingAuthorReviews === undefined ? 'next: 缺少字段 pendingAuthorReviews，待复核摘要按降级标注呈现' : null,
  };
}

/** Map a raw timeline envelope onto the consumed shape. */
export function mapTimeline(raw: unknown): CliOutcome<TimelineOutput> {
  if (!isRecord(raw)) return { output: null, reason: 'timeline: 输出不是对象，该部分数据降级' };
  const rows: TimelineOutput['timeline'] = [];
  for (const entry of asArray(raw.timeline)) {
    if (!isRecord(entry)) continue;
    const unit = asString(entry.unit);
    if (unit === null) continue;
    rows.push({
      unit,
      time: asString(entry.time),
      after: asArray(entry.after).filter((a): a is string => typeof a === 'string'),
      position: asNumber(entry.position) ?? 0,
    });
  }
  if (rows.length === 0 && asArray(raw.timeline).length > 0) {
    return { output: { timeline: rows }, reason: 'timeline: 条目缺少 unit，该部分按降级标注呈现' };
  }
  return { output: { timeline: rows }, reason: null };
}

/** The read-only wa-cli commands the snapshot runs for one workspace root. */
export interface CliState {
  status: CliOutcome<StatusOutput>;
  validate: CliOutcome<ValidateOutput>;
  next: CliOutcome<NextOutput>;
  timelines: Record<string, CliOutcome<TimelineOutput>>;
}

/** Run and map the workspace-level status command. */
export async function collectStatus(runner: CliRunner, root: string): Promise<CliOutcome<StatusOutput>> {
  const raw = await runCliJson<unknown>(runner, root, ['status'], 'status');
  return raw.output === null ? { output: null, reason: raw.reason } : mapStatus(raw.output);
}

/** Run and map the workspace-level validate command. */
export async function collectValidate(runner: CliRunner, root: string): Promise<CliOutcome<ValidateOutput>> {
  const raw = await runCliJson<unknown>(runner, root, ['validate'], 'validate');
  return raw.output === null ? { output: null, reason: raw.reason } : mapValidate(raw.output);
}

/** Run and map the workspace-level next command (pending author-review summary). */
export async function collectNext(runner: CliRunner, root: string): Promise<CliOutcome<NextOutput>> {
  const raw = await runCliJson<unknown>(runner, root, ['next'], 'next');
  return raw.output === null ? { output: null, reason: raw.reason } : mapNext(raw.output);
}

/** Run and map the per-work timeline command for one novel work. */
export async function collectTimeline(
  runner: CliRunner,
  root: string,
  work: string,
): Promise<CliOutcome<TimelineOutput>> {
  const raw = await runCliJson<unknown>(runner, root, ['timeline', work], `timeline:${work}`);
  return raw.output === null ? { output: null, reason: raw.reason } : mapTimeline(raw.output);
}

/**
 * Work ids of root units whose type is novel, from mapped status output.
 * Returns null when status is degraded and the tree shape is unknown.
 */
export function novelWorksFromStatus(status: CliOutcome<StatusOutput>): string[] | null {
  if (status.output === null) return null;
  const works: string[] = [];
  for (const unit of status.output.units) {
    if (unit.parent === null && unit.type === 'novel') works.push(unit.work);
  }
  return works;
}
