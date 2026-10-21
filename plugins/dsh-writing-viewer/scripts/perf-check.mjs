#!/usr/bin/env node
/**
 * Drive the large-workspace performance check end to end and record the
 * numbers as evidence:
 *
 *   1. build wa-cli from the repository checkout (go build, no install);
 *   2. generate a fresh multi-volume novel workspace into a temp directory
 *      (scripts/gen-large-workspace.mjs, wa-cli-created so it is valid);
 *   3. run the vitest probe src/largeperf.test.ts against that workspace
 *      (LARGE_WS_DIR / LARGE_WA_CLI), which logs one `perf:` line per phase;
 *   4. append the dated numbers to docs/evidence/perf-check.txt.
 *
 * The probe is evidence, not a gate — there is no numeric threshold; a run
 * fails only when generation, the test suite, or the probe itself fails.
 *
 * Usage: node scripts/perf-check.mjs [--volumes 3] [--chapters 8] [--scenes 8]
 */
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, appendFileSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const pkgDir = dirname(fileURLToPath(import.meta.url));
const pkgRoot = resolve(pkgDir, '..');
const repoRoot = resolve(pkgRoot, '..', '..');

function argValue(name, fallback) {
  const index = process.argv.indexOf(name);
  return index === -1 ? fallback : process.argv[index + 1];
}
const volumes = argValue('--volumes', '3');
const chapters = argValue('--chapters', '8');
const scenes = argValue('--scenes', '8');

function fail(message) {
  console.error(`perf-check: ${message}`);
  process.exit(1);
}

/** Find go on PATH or at the usual /usr/local/go location. */
function goBinary() {
  const probe = spawnSync('go', ['version'], { encoding: 'utf8' });
  if (probe.status === 0) return 'go';
  const fallback = '/usr/local/go/bin/go';
  const fallbackProbe = spawnSync(fallback, ['version'], { encoding: 'utf8' });
  if (fallbackProbe.status === 0) return fallback;
  fail('no go toolchain found on PATH or at /usr/local/go/bin/go');
}

const temp = mkdtempSync(join(tmpdir(), 'dsh-writing-viewer-perf-'));
try {
  const waCli = join(temp, 'wa-cli');
  const built = spawnSync(goBinary(), ['build', '-o', waCli, './cmd/wa-cli'], { cwd: repoRoot, encoding: 'utf8', env: { ...process.env, PATH: `/usr/local/go/bin:${process.env.PATH ?? ''}` } });
  if (built.status !== 0) fail(`go build failed:\n${built.stderr}`);

  const wsDir = join(temp, 'workspace');
  const generated = spawnSync(process.execPath, [join(pkgDir, 'gen-large-workspace.mjs'), '--dir', wsDir, '--wa-cli', waCli, '--volumes', volumes, '--chapters', chapters, '--scenes', scenes], { encoding: 'utf8' });
  process.stdout.write(generated.stdout);
  if (generated.status !== 0) fail(`workspace generation failed:\n${generated.stderr}`);

  const tested = spawnSync('pnpm', ['exec', 'vitest', 'run', 'src/largeperf.test.ts'], {
    cwd: pkgRoot,
    encoding: 'utf8',
    env: { ...process.env, LARGE_WS_DIR: wsDir, LARGE_WA_CLI: waCli },
  });
  process.stdout.write(tested.stdout);
  if (tested.stderr) process.stderr.write(tested.stderr);
  if (tested.status !== 0) fail('vitest run of the performance probe failed');

  const perfLines = (tested.stdout.match(/^perf: .*$/gm) ?? []);
  if (perfLines.length < 6) fail(`expected at least 6 perf: lines, got ${perfLines.length} — the probe did not run`);
  if (!/Tests\s+\d+ passed/.test(tested.stdout)) fail('probe suite reported no passing tests');

  const evidenceDir = join(pkgRoot, 'docs', 'evidence');
  mkdirSync(evidenceDir, { recursive: true });
  const stamp = new Date().toISOString();
  const record = [
    `# ${stamp} — volumes=${volumes} chapters=${chapters} scenes=${scenes} (generated via wa-cli, node ${process.version})`,
    ...perfLines,
    '',
  ].join('\n');
  appendFileSync(join(evidenceDir, 'perf-check.txt'), record);
  console.log(`recorded ${perfLines.length} measurements in docs/evidence/perf-check.txt`);
} finally {
  rmSync(temp, { recursive: true, force: true });
}
