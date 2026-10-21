/**
 * Zero-dependency Markdown reading view and source highlighter.
 *
 * The reading view produces a structured block list — never an HTML string —
 * and the client renders it through createElement with text children, so
 * every character of the author's draft stays escaped text: no HTML or script
 * in a draft can execute (the read-only panel renders, it never interprets).
 * The block set is the restricted one the roadmap details call for: headings,
 * lists, code blocks, quotes and tables.
 *
 * The source view reuses the same line walk to color structural Markdown
 * (headings, quote and list markers, fenced code, inline code and emphasis).
 */

/** One block of the rendered reading view. */
export type MdBlock =
  | { type: 'heading'; level: number; text: string }
  | { type: 'paragraph'; text: string }
  | { type: 'list'; ordered: boolean; items: string[] }
  | { type: 'code'; lang: string | null; text: string }
  | { type: 'quote'; blocks: MdBlock[] }
  | { type: 'table'; header: string[]; rows: string[][] };

/** Token class for one span of highlighted Markdown source. */
export type MdTokenClass = 'md-heading' | 'md-quote' | 'md-list' | 'md-code' | 'md-inline-code' | 'md-emphasis' | 'md-table' | 'plain';

/** One colored span of a source line. */
export interface MdToken {
  text: string;
  cls: MdTokenClass;
}

/** One highlighted source line. */
export interface MdLine {
  tokens: MdToken[];
}

/** Safe indexed access: out-of-range reads as the empty string. */
function at(lines: readonly string[], index: number): string {
  return lines[index] ?? '';
}

/** Split a fenced-code line into its language and content. */
function fenceOf(line: string): { fence: string; lang: string } | null {
  const match = line.match(/^(\s*)(`{3,}|~{3,})(.*)$/);
  if (match === null) return null;
  return { fence: (match[1] ?? '') + (match[2] ?? ''), lang: (match[3] ?? '').trim() };
}

/** Split one table row into cells (leading/trailing pipes dropped). */
function tableCells(line: string): string[] {
  return line
    .replace(/^\s*\|/, '')
    .replace(/\|\s*$/, '')
    .split('|')
    .map((cell) => cell.trim());
}

/** Whether a line is the `| --- | --- |` separator under a table header. */
function isTableSeparator(line: string): boolean {
  return /^\s*\|?[\s:|-]+\|?\s*$/.test(line) && line.includes('-') && line.includes('|');
}

/**
 * Parse Markdown text into reading-view blocks. Line-based and total: every
 * input line lands in some block, unknown constructs become paragraphs.
 */
export function renderMarkdown(text: string): MdBlock[] {
  const blocks: MdBlock[] = [];
  const lines = text.split('\n');
  let index = 0;
  while (index < lines.length) {
    const line = at(lines, index);
    const fence = fenceOf(line);
    if (fence !== null) {
      const body: string[] = [];
      index += 1;
      while (index < lines.length && fenceOf(at(lines, index)) === null) {
        body.push(at(lines, index));
        index += 1;
      }
      // Closing fence (or EOF); skip it.
      if (index < lines.length) index += 1;
      blocks.push({ type: 'code', lang: fence.lang === '' ? null : fence.lang, text: body.join('\n') });
      continue;
    }
    if (line.trim() === '') {
      index += 1;
      continue;
    }
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading !== null) {
      blocks.push({ type: 'heading', level: (heading[1] ?? '').length, text: (heading[2] ?? '').trim() });
      index += 1;
      continue;
    }
    if (/^\s*>/.test(line)) {
      const quoted: string[] = [];
      while (index < lines.length && /^\s*>/.test(at(lines, index))) {
        quoted.push(at(lines, index).replace(/^\s*>\s?/, ''));
        index += 1;
      }
      blocks.push({ type: 'quote', blocks: renderMarkdown(quoted.join('\n')) });
      continue;
    }
    const list = line.match(/^\s*([-*+]|\d+[.)])\s+/);
    if (list !== null) {
      const ordered = /\d/.test(list[1] ?? '');
      const items: string[] = [];
      // A list runs while the marker kind stays the same: a switch between
      // ordered and unordered markers starts a new list instead of merging.
      const sameKind = (marker: string): boolean => /\d/.test(marker) === ordered;
      while (index < lines.length) {
        const item = at(lines, index).match(/^\s*([-*+]|\d+[.)])\s+(.*)$/);
        if (item === null || !sameKind(item[1] ?? '')) break;
        items.push(item[2] ?? '');
        index += 1;
      }
      blocks.push({ type: 'list', ordered, items });
      continue;
    }
    if (line.includes('|') && index + 1 < lines.length && isTableSeparator(at(lines, index + 1))) {
      const header = tableCells(line);
      index += 2;
      const rows: string[][] = [];
      while (index < lines.length && at(lines, index).includes('|') && at(lines, index).trim() !== '') {
        rows.push(tableCells(at(lines, index)));
        index += 1;
      }
      blocks.push({ type: 'table', header, rows });
      continue;
    }
    const paragraph: string[] = [];
    while (
      index < lines.length &&
      at(lines, index).trim() !== '' &&
      fenceOf(at(lines, index)) === null &&
      !/^(#{1,6})\s+/.test(at(lines, index)) &&
      !/^\s*>/.test(at(lines, index)) &&
      !/^\s*([-*+]|\d+[.)])\s+/.test(at(lines, index))
    ) {
      paragraph.push(at(lines, index));
      index += 1;
    }
    if (paragraph.length === 0) {
      // A table without a separator row, or another unhandled construct:
      // keep the line as a paragraph so it is never dropped.
      paragraph.push(at(lines, index));
      index += 1;
    }
    blocks.push({ type: 'paragraph', text: paragraph.join('\n') });
  }
  return blocks;
}

/**
 * Color the structural parts of one Markdown source line: quote and list
 * markers, headings, fenced code, inline code spans and emphasis runs.
 * Joining the tokens reproduces the line exactly.
 */
export function highlightMarkdownLine(line: string, inFence: boolean): MdLine {
  if (inFence) return { tokens: [{ text: line, cls: 'md-code' }] };
  if (fenceOf(line) !== null) return { tokens: [{ text: line, cls: 'md-code' }] };
  if (/^#{1,6}\s/.test(line)) return { tokens: [{ text: line, cls: 'md-heading' }] };
  if (/^\s*>/.test(line)) return { tokens: [{ text: line, cls: 'md-quote' }] };
  const list = line.match(/^(\s*)([-*+]|\d+[.)])(\s+)([\s\S]*)$/);
  if (list !== null) {
    const tokens: MdToken[] = [
      { text: list[1] ?? '', cls: 'plain' },
      { text: list[2] ?? '', cls: 'md-list' },
      { text: list[3] ?? '', cls: 'plain' },
    ];
    return { tokens: [...tokens, ...inlineTokens(list[4] ?? '')] };
  }
  if (line.includes('|') && (line.trim().startsWith('|') || line.trim().endsWith('|'))) {
    // A table line (header, separator or row): color it as table structure.
    return { tokens: [{ text: line, cls: 'md-table' }] };
  }
  return { tokens: inlineTokens(line) };
}

/** Highlight inline code spans and emphasis runs in plain text. */
function inlineTokens(text: string): MdToken[] {
  const tokens: MdToken[] = [];
  let rest = text;
  while (rest !== '') {
    // Inline code span first: `...` (no nesting inside).
    const start = rest.search(/[`*]/);
    if (start < 0) {
      tokens.push({ text: rest, cls: 'plain' });
      break;
    }
    if (start > 0) tokens.push({ text: rest.slice(0, start), cls: 'plain' });
    const marker = rest[start];
    const paired = marker === '`' ? rest.indexOf('`', start + 1) : rest.indexOf('**', start + 1);
    const end = marker === '`' ? paired : paired >= 0 ? paired + 1 : -1;
    if (end < 0) {
      // Unpaired marker: plain to the end so text is never lost.
      tokens.push({ text: rest.slice(start), cls: 'plain' });
      break;
    }
    tokens.push({ text: rest.slice(start, end + 1), cls: marker === '`' ? 'md-inline-code' : 'md-emphasis' });
    rest = rest.slice(end + 1);
  }
  return tokens;
}

/** Highlight whole Markdown source; tracks fence state across lines. */
export function highlightMarkdownSource(text: string): MdLine[] {
  const out: MdLine[] = [];
  let inFence = false;
  for (const line of text.split('\n')) {
    if (fenceOf(line) !== null) {
      out.push({ tokens: [{ text: line, cls: 'md-code' }] });
      inFence = !inFence;
      continue;
    }
    out.push(highlightMarkdownLine(line, inFence));
  }
  return out;
}
