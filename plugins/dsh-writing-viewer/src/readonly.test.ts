/**
 * Read-only constraint tests for the plugin package.
 *
 * Two complementary checks, because a literal scan alone can miss calls built
 * through helpers and a behavioral check alone says nothing about code paths
 * the fixture never reaches:
 *
 * 1. a static scan over every runtime source file (src/*.ts, tests excluded —
 *    tests may create their own temporary fixtures) asserting none of the
 *    node:fs write APIs, no shell execution, and no spawn with a shell is
 *    present;
 * 2. a behavioral check on the committed fixture: one full snapshot leaves
 *    the file list, every byte of content, and every modification time
 *    exactly as they were.
 */
import { readdir, readFile, stat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { realSnapshotFs } from './host';
import { buildSnapshotPayload } from './snapshot';
import type { CliRunner } from './wacli';

const PKG_DIR = join(dirname(fileURLToPath(import.meta.url)), '..');
const SRC_DIR = join(PKG_DIR, 'src');
const TESTDATA = join(PKG_DIR, 'testdata');

/**
 * What the static check actually looks for: calls to write-capable node:fs
 * APIs (promise, sync, and callback spellings), stream-writing constructors,
 * and shell-mediated or shell-spawning process execution.
 */
const BANNED_PATTERNS: { pattern: RegExp; label: string }[] = [
  { pattern: /\b(writeFile|writeFileSync|appendFile|appendFileSync|copyFile|copyFileSync)\s*\(/, label: 'fs 写文件 API' },
  { pattern: /\b(mkdir|mkdirSync|rmdir|rmdirSync|rm|rmSync|unlink|unlinkSync)\s*\(/, label: 'fs 目录删除/创建 API' },
  { pattern: /\b(rename|renameSync|chmod|chmodSync|truncate|truncateSync|lutimes|utimes)\s*\(/, label: 'fs 文件变更 API' },
  { pattern: /\bcreateWriteStream\s*\(/, label: 'fs 写流' },
  { pattern: /\bopenSync\s*\([^)]*['"][wx]/, label: 'fs 写模式 open' },
  { pattern: /shell:\s*true/, label: 'spawn shell 选项' },
  { pattern: /\bspawn(Sync)?\s*\(\s*['"](sh|bash|zsh|cmd|powershell)['"]/i, label: 'spawn shell 进程' },
  { pattern: /\bexec(Sync|File)?\s*\(/, label: 'child_process exec' },
];

const unavailableRunner: CliRunner = { run: async () => null };

/** Recursive inventory: relative path → size + mtime + content hash. */
async function inventory(root: string): Promise<Map<string, string>> {
  const out = new Map<string, string>();
  async function walk(dir: string, rel: string): Promise<void> {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const abs = join(dir, entry.name);
      const relPath = rel === '' ? entry.name : `${rel}/${entry.name}`;
      if (entry.isDirectory()) {
        await walk(abs, relPath);
        continue;
      }
      if (!entry.isFile()) continue;
      const [info, content] = await Promise.all([stat(abs), readFile(abs)]);
      out.set(relPath, `${info.size}:${info.mtimeMs}:${createHash('sha256').update(content).digest('hex')}`);
    }
  }
  await walk(root, '');
  return out;
}

describe('read-only constraint (static)', () => {
  it('runtime sources contain no write-capable fs or shell execution calls', async () => {
    const files = (await readdir(SRC_DIR)).filter((name) => name.endsWith('.ts') && !name.endsWith('.test.ts'));
    expect(files.length).toBeGreaterThan(0);
    for (const name of files) {
      const source = await readFile(join(SRC_DIR, name), 'utf8');
      for (const { pattern, label } of BANNED_PATTERNS) {
        // Report the offending file with the match so a failure names the call.
        const match = source.match(pattern);
        expect(match, `${name} 不应包含${label}：${match?.[0] ?? ''}`).toBeNull();
      }
    }
  });
});

describe('read-only constraint (behavioral)', () => {
  it('a full snapshot leaves the committed fixture byte-identical with untouched mtimes', async () => {
    const fixtureWorkspace = join(TESTDATA, 'workspace', '.writing-anything');
    const before = await inventory(fixtureWorkspace);
    expect(before.size).toBeGreaterThan(20);
    const { payload } = await buildSnapshotPayload(realSnapshotFs(), unavailableRunner, join(TESTDATA, 'workspace'));
    expect(payload.works.length).toBe(2);
    const after = await inventory(fixtureWorkspace);
    expect([...after.entries()].sort()).toEqual([...before.entries()].sort());
  });
});
