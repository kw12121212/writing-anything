/**
 * The bundled skills ship verbatim from the repository's single source of
 * truth (skills-bundle-sync): every staged SKILL.md is byte-identical to
 * internal/skills/skills, and the roster is exactly the built-in one plus
 * the plugin's own writing-workspace skill.
 */
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { BUNDLED_SKILLS } from './setup';

const SKILLS_DIR = new URL('../skills', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1');
const SOURCE_DIR = new URL('../../../internal/skills/skills', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1');

describe('bundled skills sync', () => {
  it('stages exactly the built-in roster plus the plugin skill', () => {
    const staged = readdirSync(SKILLS_DIR).sort();
    expect(staged).toEqual([...BUNDLED_SKILLS, 'writing-workspace'].sort());
  });

  it('every staged SKILL.md is byte-identical to the repository source', () => {
    for (const name of BUNDLED_SKILLS) {
      const source = readFileSync(`${SOURCE_DIR}/${name}/SKILL.md`);
      const staged = readFileSync(`${SKILLS_DIR}/${name}/SKILL.md`);
      expect(staged.equals(source), name).toBe(true);
    }
  });

  it('the plugin owns a valid writing-workspace skill', () => {
    const text = readFileSync(`${SKILLS_DIR}/writing-workspace/SKILL.md`, 'utf8');
    expect(text.startsWith('---\n')).toBe(true);
    expect(text).toContain('name: writing-workspace');
    expect(existsSync(`${SOURCE_DIR}/writing-workspace`)).toBe(false);
  });
});
