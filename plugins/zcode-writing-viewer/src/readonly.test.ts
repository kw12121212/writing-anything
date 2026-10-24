/**
 * Static read-only scan: the viewer path (snapshot, tools, review, panel)
 * must contain zero write-capable fs calls. The declared write surface is
 * exactly the setup pair — the adapter file must exist, so a rename cannot
 * silently widen the exemption.
 */
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const SRC = new URL('.', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1');

/** Modules allowed to hold write-capable node:fs / node:fs/promises calls. */
export const SETUP_WRITE_MODULES = ['setupfs.ts', 'setup.ts'] as const;

const WRITE_FS_PATTERNS = [
  /from 'node:fs'[^;]*\b(writeFile|appendFile|rename|rm|unlink|mkdir|rmdir|chmod|cp|copyFile|open)\b/s,
  /\bfsPromises\.(writeFile|appendFile|rename|rm|unlink|mkdir|rmdir|chmod|cp|copyFile|open)\b/,
  /\bfsp\.(writeFile|appendFile|rename|rm|unlink|mkdir|rmdir|chmod|cp|copyFile|open)\b/,
  /\bfs\.(writeFile|appendFile|renameSync|rmSync|unlinkSync|mkdirSync|chmodSync|copyFileSync)\b/,
];

function sourceFiles(): string[] {
  return readdirSync(SRC).filter((name) => name.endsWith('.ts') && !name.endsWith('.test.ts'));
}

describe('static read-only scan', () => {
  it('declared write modules exist', () => {
    for (const name of SETUP_WRITE_MODULES) {
      expect(existsSync(SRC + name), name).toBe(true);
    }
  });

  it('no module outside the declared write surface touches write-capable fs APIs', () => {
    const offenders: string[] = [];
    for (const name of sourceFiles()) {
      if ((SETUP_WRITE_MODULES as readonly string[]).includes(name)) continue;
      const text = readFileSync(SRC + name, 'utf8');
      if (WRITE_FS_PATTERNS.some((pattern) => pattern.test(text))) offenders.push(name);
    }
    expect(offenders).toEqual([]);
  });

  it('runtime sources never register a dispose event listener (the DSH lesson does not apply here, the memory does: cleanup rides process exit)', () => {
    for (const name of sourceFiles()) {
      const text = readFileSync(SRC + name, 'utf8');
      expect(text.includes("on('dispose'"), name).toBe(false);
      expect(text.includes('on("dispose"'), name).toBe(false);
    }
  });
});
