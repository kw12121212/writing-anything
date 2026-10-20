#!/usr/bin/env node
/**
 * Generate a large novel (multi-volume, multi-chapter, multi-scene) writing
 * workspace through wa-cli, for the panel's large-workspace performance
 * check. Structure comes from wa-cli itself — every unit add goes through
 * the CLI so the generated workspace is schema-valid, exactly like a real
 * long-form project would be.
 *
 * Usage:
 *   node scripts/gen-large-workspace.mjs --dir <fresh-dir> --wa-cli <binary> \
 *     [--volumes 3] [--chapters 8] [--scenes 8] [--work big-novel]
 *
 * The target directory should be empty (or not exist): the script refuses to
 * touch a directory that already holds a .writing-anything workspace.
 */
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import { isAbsolute, resolve } from 'node:path';

function argValue(name, fallback) {
  const index = process.argv.indexOf(name);
  if (index === -1) return fallback;
  const value = process.argv[index + 1];
  if (value === undefined || value.startsWith('--')) {
    console.error(`missing value for ${name}`);
    process.exit(2);
  }
  return value;
}

const dir = resolve(argValue('--dir', null));
const cli = argValue('--wa-cli', 'wa-cli');
const volumes = Number(argValue('--volumes', '3'));
const chapters = Number(argValue('--chapters', '8'));
const scenes = Number(argValue('--scenes', '8'));
const work = argValue('--work', 'big-novel');

if (!isAbsolute(dir)) {
  console.error('--dir must be an absolute path');
  process.exit(2);
}
if (!Number.isInteger(volumes) || volumes < 1 || !Number.isInteger(chapters) || chapters < 1 || !Number.isInteger(scenes) || scenes < 1) {
  console.error('--volumes/--chapters/--scenes must be positive integers');
  process.exit(2);
}
if (existsSync(resolve(dir, '.writing-anything'))) {
  console.error(`refusing to generate: ${dir} already holds a .writing-anything workspace`);
  process.exit(2);
}

mkdirSync(dir, { recursive: true });

/** Run one wa-cli command; abort with its diagnostics when it fails. */
function run(args) {
  const result = spawnSync(cli, [...args, '--dir', dir, '--json'], { encoding: 'utf8' });
  if (result.status !== 0) {
    console.error(`wa-cli ${args.join(' ')} exited ${result.status}`);
    console.error(result.stderr || result.stdout);
    process.exit(1);
  }
  let envelope;
  try {
    envelope = JSON.parse(result.stdout);
  } catch (error) {
    console.error(`wa-cli ${args.join(' ')} printed unparseable output: ${error}`);
    process.exit(1);
  }
  if (envelope.valid !== true) {
    console.error(`wa-cli ${args.join(' ')} reported diagnostics:`);
    for (const diagnostic of envelope.diagnostics ?? []) console.error(`  ${diagnostic.severity} ${diagnostic.code}: ${diagnostic.message}`);
    process.exit(1);
  }
}

const started = Date.now();
run(['init']);
run(['new', 'novel', work]);
// Unit references are flat: <work>/<unit-id>, where the id is unique within
// the work regardless of depth (same shape as `wa-cli status` reports).
for (let v = 1; v <= volumes; v += 1) {
  const partId = `vol-${v}`;
  run(['unit', 'add', work, 'part', partId]);
  for (let c = 1; c <= chapters; c += 1) {
    const chapterId = `ch-${v}-${c}`;
    run(['unit', 'add', `${work}/${partId}`, 'chapter', chapterId]);
    for (let s = 1; s <= scenes; s += 1) {
      run(['unit', 'add', `${work}/${chapterId}`, 'scene', `sc-${v}-${c}-${s}`]);
    }
  }
}

const unitCount = 1 + volumes + volumes * chapters + volumes * chapters * scenes;
const status = spawnSync(cli, ['status', '--dir', dir, '--json'], { encoding: 'utf8' });
let reported = null;
try {
  reported = JSON.parse(status.stdout).units.length;
} catch {
  reported = null;
}
console.log(`generated ${work} in ${dir}: ${unitCount} units (status reports ${reported}) in ${((Date.now() - started) / 1000).toFixed(1)}s`);
