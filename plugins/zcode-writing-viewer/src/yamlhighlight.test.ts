import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { foldYaml, highlightLine, highlightYaml } from './yamlhighlight';

const TESTDATA = join(dirname(fileURLToPath(import.meta.url)), '..', 'testdata');

/** Classes of one line's tokens, aligned with the token texts. */
function classesOf(line: { indent: string; tokens: { text: string; cls: string }[] }): string[] {
  return line.tokens.map((token) => token.cls);
}

describe('highlightLine token coverage', () => {
  it('colors keys, scalars and the separating colon', () => {
    const line = highlightLine('audience: 维护大型单体服务的工程师');
    expect(line.tokens.map((t) => [t.cls, t.text])).toEqual([
      ['key', 'audience'],
      ['structure', ':'],
      ['structure', ' '],
      ['scalar', '维护大型单体服务的工程师'],
    ]);
  });

  it('colors list dashes as structure and quoted scalars whole', () => {
    const line = highlightLine('  - id: "msg one"');
    expect(line.indent).toBe('  ');
    const classes = classesOf(line);
    expect(classes).toContain('structure');
    expect(line.tokens.some((t) => t.cls === 'key' && t.text === 'id')).toBe(true);
    expect(line.tokens.some((t) => t.cls === 'scalar' && t.text === ' "msg one"')).toBe(true);
  });

  it('colors full-line and trailing comments', () => {
    const whole = highlightLine('# 写作简报（Brief）');
    expect(whole.tokens).toEqual([{ text: '# 写作简报（Brief）', cls: 'comment' }]);
    const trailing = highlightLine('purpose: 说服读者 # 为什么');
    const comment = trailing.tokens.find((t) => t.cls === 'comment');
    expect(comment?.text).toBe('# 为什么');
    const scalar = trailing.tokens.find((t) => t.cls === 'scalar');
    expect(scalar?.text).toContain('说服读者');
  });

  it('colors anchors and aliases', () => {
    const anchor = highlightLine('base: &defaults value');
    expect(anchor.tokens.some((t) => t.cls === 'anchor' && t.text.trim().startsWith('&'))).toBe(true);
    const alias = highlightLine('next: *defaults');
    expect(alias.tokens.some((t) => t.cls === 'alias' && t.text.trim().startsWith('*'))).toBe(true);
  });

  it('colors array and mapping structure in flow style', () => {
    const line = highlightLine('length: {min: 1500, max: 2500}');
    const structures = line.tokens.filter((t) => t.cls === 'structure').map((t) => t.text.trim()).join('');
    expect(structures).toContain('{');
    expect(structures).toContain('}');
    expect(line.tokens.some((t) => t.cls === 'scalar' && t.text.includes('1500'))).toBe(true);
  });

  it('never loses text, including malformed lines', () => {
    const hostile = ['key: [unclosed', '  - : : -', 'plain text without colon', '   ', 'a: "unterminated', '* & [] {} # x'];
    for (const text of hostile) {
      const line = highlightLine(text);
      expect(line.indent + line.tokens.map((t) => t.text).join('')).toBe(text);
    }
  });

  it('round-trips the real fixture documents', async () => {
    const brief = await readFile(join(TESTDATA, 'workspace', '.writing-anything', 'works', 'my-post', 'brief.yaml'), 'utf8');
    const lines = brief.split('\n');
    highlightYaml(brief).forEach((line, index) => {
      expect(line.indent + line.tokens.map((t) => t.text).join('')).toBe(lines[index] ?? '');
    });
  });
});

describe('foldYaml', () => {
  it('groups by top-level key and merges the comment header into the first region', async () => {
    const brief = await readFile(join(TESTDATA, 'workspace', '.writing-anything', 'works', 'my-post', 'brief.yaml'), 'utf8');
    const folds = foldYaml(brief);
    // Header comment + schema_version form the first region, then one per key.
    expect(folds[0]?.lines[0]).toBe(brief.split('\n')[0]);
    expect(folds.some((fold) => fold.header === 'audience: 维护大型单体服务的工程师')).toBe(true);
    expect(folds.some((fold) => fold.header === 'key_messages:')).toBe(true);
    // Every input line lands in exactly one region.
    const total = folds.reduce((sum, fold) => sum + fold.lines.length, 0);
    expect(total).toBe(brief.split('\n').length);
    // Region bodies are contiguous slices of the input.
    const flattened = folds.flatMap((fold) => fold.lines).join('\n');
    expect(flattened).toBe(brief);
  });

  it('keeps indented continuation lines with their top-level key', () => {
    const text = 'a:\n  - 1\n  - 2\nb: 3\n';
    const folds = foldYaml(text);
    expect(folds).toHaveLength(2);
    expect(folds[0]?.lines).toEqual(['a:', '  - 1', '  - 2']);
    expect(folds[1]?.lines).toEqual(['b: 3', '']);
  });
});
