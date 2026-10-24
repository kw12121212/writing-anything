/**
 * Workspace snapshot aggregation: the read-only JSON the snapshot route serves.
 *
 * Two data sources, per the milestone's settled hybrid decision: structural
 * state (unit tree, stages, blockers, progress, validate diagnostics, pending
 * author reviews, novel timelines) comes from wa-cli's read-only --json
 * commands via the injected runner, while every artifact file is carried as
 * full text read straight from .writing-anything/ at the paths its JSON Schema
 * governs. Each request rebuilds the whole snapshot — no cache, no watchers —
 * so a manual refresh always reflects the files on disk.
 *
 * The aggregation itself never writes anything; the only filesystem access is
 * directory listing and file reading through the injected SnapshotFs, and
 * symlinked directories and files are skipped rather than followed.
 */
import {
  readArtifact,
  readDraft,
  readUnitArtifacts,
  readWorkLedgers,
  realFiles,
  realSubdirs,
  type ArtifactText,
  type SnapshotFs,
} from './artifacts';
import {
  collectNext,
  collectStatus,
  collectTimeline,
  collectValidate,
  novelWorksFromStatus,
  type CliOutcome,
  type CliRunner,
  type NextOutput,
  type StatusOutput,
  type StatusUnit,
  type TimelineOutput,
  type ValidateOutput,
} from './wacli';
import { WORKSPACE_DIR, workspaceDirOf, type CandidateProbe, type RootSource } from './root';

/** Degradation notes: one human-readable line per missing CLI area. */
export type Degraded = string[];

/** One registered source (Source) from the workspace source library. */
export interface SourceSnapshot {
  id: string;
  sourceYaml: ArtifactText;
  original: ArtifactText | null;
}

/** One unit (Unit) with its artifacts as full text plus merged structural state. */
export interface UnitSnapshot {
  /** Unit reference, e.g. my-post or my-novel/scene-1. */
  ref: string;
  /** Unit id inside the work ('' means the root unit). */
  id: string;
  type: string | null;
  parent: string | null;
  stage: string | null;
  blockers: unknown[];
  progress: { done: number; total: number } | null;
  commands: string[];
  /** Workspace-relative unit directory path. */
  dir: string;
  artifacts: Record<string, ArtifactText>;
  draft: ArtifactText | null;
}

/** One work (Work) with its unit tree and work-level ledgers. */
export interface WorkSnapshot {
  id: string;
  /** Workspace-relative work directory path, e.g. works/my-post. */
  dir: string;
  workYaml: ArtifactText | null;
  units: UnitSnapshot[];
  ledgers: Record<string, ArtifactText>;
  /** Derived timeline rows for novel works, from wa-cli timeline --json. */
  timeline: TimelineOutput['timeline'] | null;
}

/** The aggregated snapshot payload served when a workspace is located. */
export interface SnapshotPayload {
  config: ArtifactText | null;
  decisions: ArtifactText | null;
  authorReviews: ArtifactText | null;
  packLock: ArtifactText | null;
  handoffs: ArtifactText | null;
  packs: ArtifactText[];
  sources: SourceSnapshot[];
  works: WorkSnapshot[];
  cli: {
    status: StatusOutput | null;
    validate: ValidateOutput | null;
    next: NextOutput | null;
    timelines: Record<string, TimelineOutput | null>;
  };
}

/** The full body the workspace_snapshot tool returns. */
export interface SnapshotRouteBody {
  ok: boolean;
  root: string | null;
  source: RootSource | null;
  workspaceDir: string | null;
  workspaceExists: boolean;
  candidates: readonly CandidateProbe[];
  emptyState: { command: string } | null;
  degraded: Degraded;
  snapshot: SnapshotPayload | null;
  /** Present only when the request named a project path that could not be used. */
  rootError?: string;
}

function reasons(...outcomes: CliOutcome<unknown>[]): string[] {
  const notes: string[] = [];
  for (const outcome of outcomes) {
    if (outcome.reason !== null && !notes.includes(outcome.reason)) notes.push(outcome.reason);
  }
  return notes;
}

/** Merge structural state from a status row onto a directory-walked unit. */
function mergeStatusUnit(unit: UnitSnapshot, row: StatusUnit | undefined): void {
  if (row === undefined) return;
  unit.type = row.type;
  unit.parent = row.parent;
  unit.stage = row.stage;
  unit.blockers = row.blockers;
  unit.progress = row.progress;
  unit.commands = row.commands;
}

/** Build the units of one work from its directories plus mapped status rows. */
async function buildWorkUnits(
  fs: SnapshotFs,
  absWorkspace: string,
  workId: string,
  statusByRef: Map<string, StatusUnit>,
): Promise<UnitSnapshot[]> {
  const relWorkDir = `works/${workId}`;
  const absWorkDir = `${absWorkspace}/${relWorkDir}`;
  const units: UnitSnapshot[] = [];
  const root: UnitSnapshot = {
    ref: workId,
    id: '',
    type: null,
    parent: null,
    stage: null,
    blockers: [],
    progress: null,
    commands: [],
    dir: relWorkDir,
    artifacts: await readUnitArtifacts(fs, absWorkDir, relWorkDir),
    draft: await readDraft(fs, absWorkDir, relWorkDir),
  };
  mergeStatusUnit(root, statusByRef.get(workId));
  units.push(root);
  const childIds = (await realSubdirs(fs, `${absWorkDir}/units`)) ?? [];
  for (const childId of childIds) {
    const relUnitDir = `${relWorkDir}/units/${childId}`;
    const absUnitDir = `${absWorkspace}/${relUnitDir}`;
    const child: UnitSnapshot = {
      ref: `${workId}/${childId}`,
      id: childId,
      type: null,
      parent: null,
      stage: null,
      blockers: [],
      progress: null,
      commands: [],
      dir: relUnitDir,
      artifacts: await readUnitArtifacts(fs, absUnitDir, relUnitDir),
      draft: await readDraft(fs, absUnitDir, relUnitDir),
    };
    mergeStatusUnit(child, statusByRef.get(child.ref));
    units.push(child);
  }
  return units;
}

/** Read the whole .writing-anything/ workspace into the snapshot payload. */
export async function buildSnapshotPayload(
  fs: SnapshotFs,
  runner: CliRunner,
  root: string,
): Promise<{ payload: SnapshotPayload; degraded: Degraded }> {
  const absWorkspace = `${root.replace(/\/+$/, '')}/${WORKSPACE_DIR}`;
  const status = await collectStatus(runner, root);
  const validate = await collectValidate(runner, root);
  const next = await collectNext(runner, root);
  const degraded = reasons(status, validate, next);

  const statusByRef = new Map<string, StatusUnit>();
  if (status.output !== null) {
    for (const row of status.output.units) statusByRef.set(row.unit, row);
  }

  const timelines: Record<string, CliOutcome<TimelineOutput>> = {};
  const novelWorks = novelWorksFromStatus(status);
  if (novelWorks !== null) {
    for (const work of novelWorks) {
      timelines[work] = await collectTimeline(runner, root, work);
    }
  }

  const config = await readArtifact(fs, absWorkspace, '', 'config.yaml', 'yaml');
  const decisions = await readArtifact(fs, absWorkspace, '', 'decisions.yaml', 'yaml');
  const authorReviews = await readArtifact(fs, absWorkspace, '', 'author-reviews.yaml', 'yaml');
  const packLock = await readArtifact(fs, absWorkspace, '', 'packs.lock.yaml', 'yaml');
  const handoffs = await readArtifact(fs, absWorkspace, '', 'handoffs.yaml', 'yaml');
  const packs: ArtifactText[] = [];
  for (const file of (await realFiles(fs, `${absWorkspace}/packs`, '.yaml')) ?? []) {
    const pack = await readArtifact(fs, absWorkspace, '', `packs/${file}`, 'yaml');
    if (pack !== null) packs.push(pack);
  }
  const sources: SourceSnapshot[] = [];
  for (const sourceId of (await realSubdirs(fs, `${absWorkspace}/sources`)) ?? []) {
    const sourceYaml = await readArtifact(fs, absWorkspace, '', `sources/${sourceId}/source.yaml`, 'yaml');
    if (sourceYaml === null) continue;
    const original = await readArtifact(fs, absWorkspace, '', `sources/${sourceId}/original.md`, 'markdown');
    sources.push({ id: sourceId, sourceYaml, original });
  }

  const works: WorkSnapshot[] = [];
  for (const workId of (await realSubdirs(fs, `${absWorkspace}/works`)) ?? []) {
    const relWorkDir = `works/${workId}`;
    const absWorkDir = `${absWorkspace}/${relWorkDir}`;
    const workYaml = await readArtifact(fs, absWorkDir, relWorkDir, 'work.yaml', 'yaml');
    works.push({
      id: workId,
      dir: relWorkDir,
      workYaml,
      units: await buildWorkUnits(fs, absWorkspace, workId, statusByRef),
      ledgers: await readWorkLedgers(fs, `${absWorkspace}/${relWorkDir}`, relWorkDir),
      timeline: timelines[workId]?.output?.timeline ?? null,
    });
  }
  for (const outcome of Object.values(timelines)) degraded.push(...reasons(outcome));

  const payload: SnapshotPayload = {
    config,
    decisions,
    authorReviews,
    packLock,
    handoffs,
    packs,
    sources,
    works,
    cli: {
      status: status.output,
      validate: validate.output,
      next: next.output,
      timelines: Object.fromEntries(
        Object.entries(timelines).map(([work, outcome]) => [work, outcome.output]),
      ),
    },
  };
  return { payload, degraded };
}

/** Build the route body: locate the workspace, then aggregate it read-only. */
export async function buildSnapshot(
  fs: SnapshotFs,
  runner: CliRunner,
  candidates: readonly CandidateProbe[],
  located: { root: string; source: RootSource } | null,
): Promise<SnapshotRouteBody> {
  if (located === null) {
    return {
      ok: true,
      root: null,
      source: null,
      workspaceDir: null,
      workspaceExists: false,
      candidates,
      emptyState: { command: 'wa-cli init' },
      degraded: [],
      snapshot: null,
    };
  }
  const { payload, degraded } = await buildSnapshotPayload(fs, runner, located.root);
  return {
    ok: true,
    root: located.root,
    source: located.source,
    workspaceDir: workspaceDirOf(located.root),
    workspaceExists: true,
    candidates,
    emptyState: null,
    degraded,
    snapshot: payload,
  };
}
