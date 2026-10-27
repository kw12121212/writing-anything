/**
 * Pure view model for the overview dashboard and unit-tree navigation.
 *
 * Everything here derives display data from one snapshot route body and
 * nothing else: no fetching, no React, no DOM. The client bundle imports it
 * (tsdown inlines it, together with the yaml parser, into the iife), and the
 * tests drive it with real snapshots built from testdata.
 *
 * Structural state follows wa-cli: the unit tree and per-unit stage,
 * progress, blockers and commands come from the `status --json` rows the
 * snapshot embeds. When that area is degraded the tree falls back to the
 * hierarchy recorded in work.yaml, and finally to the directory-owning units
 * the snapshot walked, so the panel always has something to navigate.
 * Hand-edited YAML that does not parse never throws — it simply yields no
 * data for the field it would have supplied.
 */
import { parse } from 'yaml';
import type { SnapshotRouteBody, UnitSnapshot, WorkSnapshot } from './snapshot';
import type { CandidateProbe, RootSource } from './root';
import type { StatusUnit } from './wacli';

/** One blocker (Blocker) as the panel shows it. */
export interface BlockerView {
  code: string;
  message: string;
  path: string | null;
}

/** One validate diagnostic as the panel shows it. */
export interface DiagnosticView {
  code: string;
  severity: string;
  message: string;
  path: string | null;
}

/** Progress of an aggregated unit: lifecycle units done out of total. */
export interface ProgressView {
  done: number;
  total: number;
}

/** One work (Work) card on the overview dashboard. */
export interface WorkCard {
  id: string;
  genre: string | null;
  type: string | null;
  stage: string | null;
  progress: ProgressView | null;
  unitCount: number;
  blockerCount: number;
  /** The first few blocker messages across the work, in tree order. */
  blockerSummary: string[];
}

/** Workspace-level counts; null means the area that supplies it is unavailable. */
export interface WorkspaceSummary {
  sources: number;
  pendingDecisions: number | null;
  pendingReviews: number | null;
  diagnostics: number | null;
  diagnosticsBySeverity: Record<string, number>;
}

/** The whole dashboard model for one snapshot route body. */
export interface OverviewModel {
  /** missing: no workspace located; no-works: workspace without works; ready: works to show. */
  state: 'missing' | 'no-works' | 'ready';
  root: string | null;
  source: RootSource | null;
  workspaceDir: string | null;
  candidates: readonly CandidateProbe[];
  /** Guidance command for the empty states. */
  emptyCommand: string | null;
  /** Why an explicitly requested project path could not be used, if it could not. */
  rootError: string | null;
  summary: WorkspaceSummary | null;
  diagnostics: DiagnosticView[];
  degraded: string[];
  works: WorkCard[];
}

/** One row of the flattened unit tree, in pre-order. */
export interface TreeNode {
  ref: string;
  /** Unit id inside the work; the work id for the root unit. */
  label: string;
  type: string | null;
  stage: string | null;
  depth: number;
  aggregated: boolean;
  progress: ProgressView | null;
  blockerCount: number;
}

/** One artifact (Artifact) file the selected unit owns. */
export interface ArtifactEntry {
  name: string;
  path: string;
  format: 'yaml' | 'markdown';
}

/** Detail of the selected unit. */
export interface UnitDetail {
  ref: string;
  type: string | null;
  stage: string | null;
  progress: ProgressView | null;
  blockers: BlockerView[];
  commands: string[];
  artifacts: ArtifactEntry[];
}

/** Command suggested when a workspace has no works yet. */
export const NEW_WORK_COMMAND = 'wa-cli new <genre> <id>';

/** How many blocker messages a work card shows. */
const BLOCKER_SUMMARY_LIMIT = 2;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function asString(value: unknown): string | null {
  return typeof value === 'string' && value !== '' ? value : null;
}

/** Parse YAML text, treating any parse failure as "no data". */
function parseYaml(text: string | undefined | null): unknown {
  if (typeof text !== 'string') return null;
  try {
    return parse(text);
  } catch {
    return null;
  }
}

function blockerView(raw: unknown): BlockerView {
  if (!isRecord(raw)) return { code: '', message: String(raw), path: null };
  return {
    code: asString(raw.code) ?? '',
    message: asString(raw.message) ?? asString(raw.code) ?? '',
    path: asString(raw.path),
  };
}

function diagnosticView(raw: unknown): DiagnosticView {
  if (!isRecord(raw)) return { code: '', severity: 'error', message: String(raw), path: null };
  return {
    code: asString(raw.code) ?? '',
    severity: asString(raw.severity) ?? 'error',
    message: asString(raw.message) ?? asString(raw.code) ?? '',
    path: asString(raw.path),
  };
}

/** Count decisions whose status is open in decisions.yaml text. */
export function pendingDecisionCount(text: string | null | undefined): number | null {
  if (typeof text !== 'string') return 0;
  // An empty file means no decisions recorded yet; text that fails to parse
  // stays unknown (null) below.
  if (text.trim() === '') return 0;
  const doc = parseYaml(text);
  if (!isRecord(doc)) return null;
  if (doc.decisions === undefined || doc.decisions === null) return 0;
  if (!Array.isArray(doc.decisions)) return null;
  return doc.decisions.filter((d) => isRecord(d) && d.status === 'open').length;
}

/** A unit as recorded in work.yaml's nested `units` hierarchy. */
interface WorkYamlUnit {
  id: string;
  type: string | null;
  stage: string | null;
  children: WorkYamlUnit[];
}

function workYamlUnits(raw: unknown): WorkYamlUnit[] {
  if (!Array.isArray(raw)) return [];
  const out: WorkYamlUnit[] = [];
  for (const entry of raw) {
    if (!isRecord(entry)) continue;
    const id = asString(entry.id);
    if (id === null) continue;
    out.push({ id, type: asString(entry.type), stage: asString(entry.stage), children: workYamlUnits(entry.units) });
  }
  return out;
}

/** The parsed top level of work.yaml, or null when absent or unreadable. */
function parseWorkYaml(work: WorkSnapshot): { genre: string | null; type: string | null; stage: string | null; units: WorkYamlUnit[] } | null {
  const doc = parseYaml(work.workYaml?.text);
  if (!isRecord(doc)) return null;
  return { genre: asString(doc.genre), type: asString(doc.type), stage: asString(doc.stage), units: workYamlUnits(doc.units) };
}

function statusRows(body: SnapshotRouteBody, workId: string): StatusUnit[] | null {
  const status = body.snapshot?.cli.status;
  if (status === null || status === undefined) return null;
  return status.units.filter((row) => row.work === workId);
}

/**
 * Whether a wa-cli area degraded in a way that makes `field` unreliable.
 * Degradation notes are produced by ./wacli with an `<area>:` prefix; an
 * area-wide failure (null output) is checked by the caller.
 */
function fieldDegraded(degraded: readonly string[], area: string, field?: string): boolean {
  return degraded.some((note) => note.startsWith(area + ':') && (field === undefined || note.includes(field)));
}

function sumProgress(items: (ProgressView | null)[]): ProgressView | null {
  let found = false;
  let done = 0;
  let total = 0;
  for (const item of items) {
    if (item === null) continue;
    found = true;
    done += item.done;
    total += item.total;
  }
  return found ? { done, total } : null;
}

/**
 * Build the unit tree of one work, flattened in pre-order with depths.
 *
 * Status rows are the primary source (their parent links are the tree);
 * work.yaml's nested hierarchy is the fallback when status is unavailable;
 * the snapshot's directory-owning units are the last resort.
 */
export function unitTree(body: SnapshotRouteBody, workId: string): TreeNode[] {
  const work = body.snapshot?.works.find((w) => w.id === workId);
  if (work === undefined) return [];
  const rows = statusRows(body, workId);
  if (rows !== null && rows.length > 0) return treeFromStatus(rows, workId);
  const parsed = parseWorkYaml(work);
  if (parsed !== null) return treeFromWorkYaml(work, parsed);
  return work.units.map((unit) => nodeFromSnapshotUnit(unit, workId, unit.id === '' ? 0 : 1));
}

function treeFromStatus(rows: StatusUnit[], workId: string): TreeNode[] {
  const children = new Map<string | null, StatusUnit[]>();
  const known = new Set(rows.map((row) => row.unit));
  for (const row of rows) {
    // A parent outside the rows (or none) puts the unit at the top level, so
    // a partial status never hides units.
    const parent = row.parent !== null && known.has(row.parent) ? row.parent : null;
    const list = children.get(parent) ?? [];
    list.push(row);
    children.set(parent, list);
  }
  const out: TreeNode[] = [];
  const visited = new Set<string>();
  const emit = (row: StatusUnit, depth: number): void => {
    if (visited.has(row.unit)) return;
    visited.add(row.unit);
    out.push({
      ref: row.unit,
      label: row.unit === workId ? workId : row.unit.slice(workId.length + 1) || row.unit,
      type: row.type,
      stage: row.stage,
      depth,
      aggregated: row.progress !== null,
      progress: row.progress,
      blockerCount: row.blockers.length,
    });
    for (const child of children.get(row.unit) ?? []) emit(child, depth + 1);
  };
  for (const row of children.get(null) ?? []) emit(row, 0);
  // Rows caught in a parent cycle are unreachable from the top level; list
  // them there rather than dropping them.
  for (const row of rows) emit(row, 0);
  return out;
}

function treeFromWorkYaml(work: WorkSnapshot, parsed: NonNullable<ReturnType<typeof parseWorkYaml>>): TreeNode[] {
  const out: TreeNode[] = [
    { ref: work.id, label: work.id, type: parsed.type, stage: parsed.stage, depth: 0, aggregated: false, progress: null, blockerCount: 0 },
  ];
  const walk = (units: WorkYamlUnit[], depth: number): void => {
    for (const unit of units) {
      const aggregated = unit.children.length > 0;
      out.push({
        ref: `${work.id}/${unit.id}`,
        label: unit.id,
        type: unit.type,
        // An aggregated unit's stored stage is only a placeholder; its real
        // (derived) stage needs wa-cli status, which is what is missing here.
        stage: aggregated ? null : unit.stage,
        depth,
        aggregated,
        progress: null,
        blockerCount: 0,
      });
      walk(unit.children, depth + 1);
    }
  };
  walk(parsed.units, 1);
  return out;
}

function nodeFromSnapshotUnit(unit: UnitSnapshot, workId: string, depth: number): TreeNode {
  return {
    ref: unit.ref,
    label: unit.id === '' ? workId : unit.id,
    type: unit.type,
    stage: unit.stage,
    depth,
    aggregated: unit.progress !== null,
    progress: unit.progress,
    blockerCount: unit.blockers.length,
  };
}

function workCard(body: SnapshotRouteBody, work: WorkSnapshot): WorkCard {
  const parsed = parseWorkYaml(work);
  const rows = statusRows(body, work.id);
  const tree = unitTree(body, work.id);
  const rootRow = rows?.find((row) => row.unit === work.id);
  // Aggregated units repeat their descendants' blockers, so the work-level
  // count is over distinct blockers; tree order keeps the summary stable
  // (the root's own blockers first).
  const blockers: BlockerView[] = [];
  const seen = new Set<string>();
  const add = (raw: unknown): void => {
    const view = blockerView(raw);
    const key = `${view.code}\u0000${view.path ?? ''}\u0000${view.message}`;
    if (seen.has(key)) return;
    seen.add(key);
    blockers.push(view);
  };
  if (rows !== null) {
    const byRef = new Map(rows.map((row) => [row.unit, row]));
    for (const node of tree) for (const raw of byRef.get(node.ref)?.blockers ?? []) add(raw);
  } else {
    for (const unit of work.units) for (const raw of unit.blockers) add(raw);
  }
  // A work's progress is its root's when the root aggregates; otherwise the
  // sum over the outermost aggregated units, whose subtrees are disjoint.
  let progress: ProgressView | null = rootRow?.progress ?? null;
  if (progress === null) {
    const outermost: (ProgressView | null)[] = [];
    let coveredDepth = Number.POSITIVE_INFINITY;
    for (const node of tree) {
      if (node.depth <= coveredDepth) coveredDepth = Number.POSITIVE_INFINITY;
      if (node.progress !== null && coveredDepth === Number.POSITIVE_INFINITY) {
        outermost.push(node.progress);
        coveredDepth = node.depth;
      }
    }
    progress = sumProgress(outermost);
  }
  return {
    id: work.id,
    genre: parsed?.genre ?? null,
    type: rootRow?.type ?? parsed?.type ?? null,
    stage: rootRow?.stage ?? parsed?.stage ?? null,
    progress,
    unitCount: tree.length,
    blockerCount: blockers.length,
    blockerSummary: blockers.slice(0, BLOCKER_SUMMARY_LIMIT).map((b) => b.message),
  };
}

/** Derive the dashboard model from one snapshot route body. */
export function overviewModel(body: SnapshotRouteBody): OverviewModel {
  const base = {
    root: body.root,
    source: body.source,
    workspaceDir: body.workspaceDir,
    candidates: Array.isArray(body.candidates) ? body.candidates : [],
    rootError: typeof body.rootError === 'string' ? body.rootError : null,
    degraded: Array.isArray(body.degraded) ? body.degraded : [],
  };
  const snapshot = body.snapshot;
  if (!body.workspaceExists || snapshot === null || snapshot === undefined) {
    return {
      ...base,
      state: 'missing',
      emptyCommand: body.emptyState?.command ?? 'wa-cli init',
      summary: null,
      diagnostics: [],
      works: [],
    };
  }
  const degraded = Array.isArray(body.degraded) ? body.degraded : [];
  const validate = snapshot.cli.validate;
  // A partially degraded area keeps its mapped (empty) arrays; those must
  // read as unavailable, not as zero.
  const diagnosticsKnown = validate !== null && !fieldDegraded(degraded, 'validate', 'diagnostics');
  const diagnostics = validate === null ? [] : validate.diagnostics.map(diagnosticView);
  const bySeverity: Record<string, number> = {};
  for (const d of diagnostics) bySeverity[d.severity] = (bySeverity[d.severity] ?? 0) + 1;
  const next = snapshot.cli.next;
  const summary: WorkspaceSummary = {
    sources: snapshot.sources.length,
    pendingDecisions: pendingDecisionCount(snapshot.decisions?.text),
    pendingReviews: next === null || fieldDegraded(degraded, 'next', 'pendingAuthorReviews') ? null : next.pendingAuthorReviews.length,
    diagnostics: diagnosticsKnown ? diagnostics.length : null,
    diagnosticsBySeverity: bySeverity,
  };
  const works = snapshot.works.map((work) => workCard(body, work));
  return {
    ...base,
    state: works.length === 0 ? 'no-works' : 'ready',
    emptyCommand: works.length === 0 ? NEW_WORK_COMMAND : null,
    summary,
    diagnostics,
    works,
  };
}

/** Detail of one unit: structural state plus the artifact files it owns. */
export function unitDetail(body: SnapshotRouteBody, ref: string): UnitDetail | null {
  const snapshot = body.snapshot;
  if (snapshot === null || snapshot === undefined) return null;
  const workId = ref.split('/')[0] ?? ref;
  const work = snapshot.works.find((w) => w.id === workId);
  if (work === undefined) return null;
  const row = statusRows(body, workId)?.find((r) => r.unit === ref);
  const unit = work.units.find((u) => u.ref === ref);
  const node = unitTree(body, workId).find((n) => n.ref === ref);
  if (row === undefined && unit === undefined && node === undefined) return null;
  const artifacts: ArtifactEntry[] = [];
  if (unit !== undefined) {
    for (const [name, artifact] of Object.entries(unit.artifacts)) {
      artifacts.push({ name, path: artifact.path, format: artifact.format });
    }
    if (unit.draft !== null) artifacts.push({ name: 'draft', path: unit.draft.path, format: unit.draft.format });
  }
  return {
    ref,
    type: row?.type ?? unit?.type ?? node?.type ?? null,
    stage: row?.stage ?? unit?.stage ?? node?.stage ?? null,
    progress: row?.progress ?? unit?.progress ?? null,
    blockers: (row?.blockers ?? unit?.blockers ?? []).map(blockerView),
    commands: row?.commands ?? unit?.commands ?? [],
    artifacts,
  };
}
