/**
 * Read-only artifact inventory and full-text reading for the snapshot.
 *
 * The file names below are the writing-anything artifact surface: each one is
 * governed by a JSON Schema registered in wa-cli's internal/schema, and the
 * patterns here mirror those registrations (unit artifacts live both at the
 * work root unit and under units/<id>/, work-level ledgers only at the work
 * root). The reader never writes, never follows symlinks (a symlinked unit or
 * artifact is skipped, matching wa-cli's own artifact.not-found handling), and
 * reports what exists as full text — structural interpretation stays with
 * wa-cli's --json output.
 */

/** One artifact file carried in the snapshot as full text. */
export interface ArtifactText {
  /** Workspace-relative path with / separators, e.g. works/my-post/brief.yaml. */
  path: string;
  /** yaml | markdown */
  format: 'yaml' | 'markdown';
  /** Full UTF-8 text of the file. */
  text: string;
}

/** A directory entry as the snapshot walker needs it (no fs dependency). */
export interface DirEntry {
  name: string;
  isDirectory: boolean;
  isSymbolicLink: boolean;
}

/** The read-only filesystem surface the snapshot needs, injected for tests. */
export interface SnapshotFs {
  /** Entries of a directory, or null when it does not exist / is not a real directory. */
  readDir(path: string): Promise<DirEntry[] | null>;
  /** Full text of a regular file (symlinks and missing files yield null). */
  readFile(path: string): Promise<string | null>;
}

/** Unit-scoped artifact file names (root unit dir and units/<id>/ alike). */
export const UNIT_ARTIFACTS: readonly { file: string; kind: string; format: 'yaml' }[] = [
  { file: 'brief.yaml', kind: 'brief', format: 'yaml' },
  { file: 'outline.yaml', kind: 'outline', format: 'yaml' },
  { file: 'plan.yaml', kind: 'plan', format: 'yaml' },
  { file: 'provenance.yaml', kind: 'provenance', format: 'yaml' },
  { file: 'claims.yaml', kind: 'claims', format: 'yaml' },
  { file: 'feedback.yaml', kind: 'feedback', format: 'yaml' },
  { file: 'review.yaml', kind: 'review', format: 'yaml' },
  { file: 'acceptance.yaml', kind: 'acceptance', format: 'yaml' },
  { file: 'closeout.yaml', kind: 'closeout', format: 'yaml' },
  { file: 'marketing.yaml', kind: 'marketing', format: 'yaml' },
  { file: 'metrics.yaml', kind: 'metrics', format: 'yaml' },
  { file: 'summary.yaml', kind: 'summary', format: 'yaml' },
  { file: 'edits.yaml', kind: 'edits', format: 'yaml' },
  { file: 'checkpoint.yaml', kind: 'checkpoint', format: 'yaml' },
];

/** Work-level ledger file names that only exist at the work root unit. */
export const WORK_LEDGERS: readonly { file: string; kind: string }[] = [
  { file: 'canon.yaml', kind: 'canon' },
  { file: 'threads.yaml', kind: 'threads' },
  { file: 'state.yaml', kind: 'state' },
  { file: 'knowledge.yaml', kind: 'knowledge' },
];

/** The draft body file name inside a unit directory. */
export const DRAFT_FILE = 'draft.md';

/** Read one artifact as full text; missing files and symlinks yield null. */
export async function readArtifact(
  fs: SnapshotFs,
  absDir: string,
  relDir: string,
  file: string,
  format: 'yaml' | 'markdown',
): Promise<ArtifactText | null> {
  const text = await fs.readFile(`${absDir}/${file}`);
  if (text === null) return null;
  return { path: relDir === '' ? file : `${relDir}/${file}`, format, text };
}

/** Read every present unit artifact of a unit directory, keyed by kind. */
export async function readUnitArtifacts(
  fs: SnapshotFs,
  absUnitDir: string,
  relUnitDir: string,
): Promise<Record<string, ArtifactText>> {
  const artifacts: Record<string, ArtifactText> = {};
  for (const { file, kind, format } of UNIT_ARTIFACTS) {
    const artifact = await readArtifact(fs, absUnitDir, relUnitDir, file, format);
    if (artifact !== null) artifacts[kind] = artifact;
  }
  return artifacts;
}

/** Read the draft body of a unit directory, when present. */
export async function readDraft(
  fs: SnapshotFs,
  absUnitDir: string,
  relUnitDir: string,
): Promise<ArtifactText | null> {
  return readArtifact(fs, absUnitDir, relUnitDir, DRAFT_FILE, 'markdown');
}

/** Read the work-level ledgers (canon, threads, state, knowledge), keyed by kind. */
export async function readWorkLedgers(
  fs: SnapshotFs,
  absWorkDir: string,
  relWorkDir: string,
): Promise<Record<string, ArtifactText>> {
  const ledgers: Record<string, ArtifactText> = {};
  for (const { file, kind } of WORK_LEDGERS) {
    const artifact = await readArtifact(fs, absWorkDir, relWorkDir, file, 'yaml');
    if (artifact !== null) ledgers[kind] = artifact;
  }
  return ledgers;
}

/**
 * List the real (non-symlink) subdirectory names of a directory, or null when
 * the directory does not exist or is not a real directory.
 */
export async function realSubdirs(fs: SnapshotFs, absDir: string): Promise<string[] | null> {
  const entries = await fs.readDir(absDir);
  if (entries === null) return null;
  return entries
    .filter((entry) => entry.isDirectory && !entry.isSymbolicLink)
    .map((entry) => entry.name)
    .sort();
}

/**
 * List the base names of real (non-symlink) files with a suffix inside a
 * directory, or null when the directory does not exist. Pack files live as
 * *.yaml directly inside packs/, not as directories.
 */
export async function realFiles(
  fs: SnapshotFs,
  absDir: string,
  suffix: string,
): Promise<string[] | null> {
  const entries = await fs.readDir(absDir);
  if (entries === null) return null;
  return entries
    .filter((entry) => !entry.isDirectory && !entry.isSymbolicLink && entry.name.endsWith(suffix))
    .map((entry) => entry.name)
    .sort();
}
