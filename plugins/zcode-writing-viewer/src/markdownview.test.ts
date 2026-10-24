import { describe, expect, it } from 'vitest';
import { highlightMarkdownLine, highlightMarkdownSource, renderMarkdown } from './markdownview';

describe('renderMarkdown reading view', () => {
  it('renders headings, paragraphs, lists, code blocks, quotes and tables', () => {
    const text = [
      '# 标题一',
      '',
      '第一段正文。',
      '',
      '- 项目一',
      '- 项目二',
      '',
      '1. 步骤一',
      '',
      '```go',
      'func main() {}',
      '```',
      '',
      '> 引用的一行',
      '> 引用的两行',
      '',
      '| 列 A | 列 B |',
      '| --- | --- |',
      '| 1 | 2 |',
      '',
      '结尾一段。',
    ].join('\n');
    const blocks = renderMarkdown(text);
    expect(blocks).toEqual([
      { type: 'heading', level: 1, text: '标题一' },
      { type: 'paragraph', text: '第一段正文。' },
      { type: 'list', ordered: false, items: ['项目一', '项目二'] },
      { type: 'list', ordered: true, items: ['步骤一'] },
      { type: 'code', lang: 'go', text: 'func main() {}' },
      {
        type: 'quote',
        blocks: [{ type: 'paragraph', text: '引用的一行\n引用的两行' }],
      },
      { type: 'table', header: ['列 A', '列 B'], rows: [['1', '2']] },
      { type: 'paragraph', text: '结尾一段。' },
    ]);
  });

  it('keeps every line in some block, including unknown constructs', () => {
    const blocks = renderMarkdown('一段 | 里有竖线但不是表格\n仍然一段');
    expect(blocks).toEqual([{ type: 'paragraph', text: '一段 | 里有竖线但不是表格\n仍然一段' }]);
  });

  it('produces structured blocks only — no HTML string can appear', () => {
    const blocks = renderMarkdown('<script>alert(1)</script>\n\n**<b>bold</b>**');
    // The draft text lands in paragraph text as-is: the renderer's output is
    // a block tree of plain strings, never markup, so createElement keeps
    // every character escaped text and no draft HTML can execute.
    expect(blocks[0]).toMatchObject({ type: 'paragraph', text: '<script>alert(1)</script>' });
    const allowed = new Set(['heading', 'paragraph', 'list', 'code', 'quote', 'table']);
    for (const block of blocks) {
      expect(allowed.has(block.type)).toBe(true);
    }
  });

  it('breaks the list when the marker kind switches', () => {
    const blocks = renderMarkdown('1. a\n2. b\n- c\n- d');
    expect(blocks).toEqual([
      { type: 'list', ordered: true, items: ['a', 'b'] },
      { type: 'list', ordered: false, items: ['c', 'd'] },
    ]);
  });

  it('closes an unterminated fence at end of input without losing content', () => {
    const blocks = renderMarkdown('```\ninside code');
    expect(blocks).toEqual([{ type: 'code', lang: null, text: 'inside code' }]);
  });
});

describe('highlightMarkdownSource', () => {
  it('colors structural lines and keeps text intact', () => {
    const text = ['# 标题', '> 引用', '- 项目', '| a | b |', '普通段落'];
    const lines = highlightMarkdownSource(text.join('\n'));
    // The first non-plain token class names each line's structure.
    const structural = lines.map((line) => line.tokens.find((token) => token.cls !== 'plain')?.cls ?? 'plain');
    expect(structural).toEqual(['md-heading', 'md-quote', 'md-list', 'md-table', 'plain']);
    lines.forEach((line, index) => {
      expect(line.tokens.map((token) => token.text).join('')).toBe(text[index]);
    });
  });

  it('marks fenced code lines inside and out', () => {
    const lines = highlightMarkdownSource('```js\ncode line\n```\nafter');
    expect(lines.map((line) => line.tokens[0]?.cls)).toEqual(['md-code', 'md-code', 'md-code', 'plain']);
  });

  it('colors inline code spans and emphasis runs without losing characters', () => {
    const line = highlightMarkdownLine('用 `wa-cli` 运行 **重要** 命令', false);
    expect(line.tokens.map((token) => token.cls)).toContain('md-inline-code');
    expect(line.tokens.map((token) => token.cls)).toContain('md-emphasis');
    expect(line.tokens.map((token) => token.text).join('')).toBe('用 `wa-cli` 运行 **重要** 命令');
  });

  it('keeps unpaired markers as plain text so nothing is dropped', () => {
    const line = highlightMarkdownLine('单个 ` 反引号和 * 星号', false);
    expect(line.tokens.map((token) => token.text).join('')).toBe('单个 ` 反引号和 * 星号');
    expect(line.tokens.every((token) => token.cls === 'plain')).toBe(true);
  });
});
