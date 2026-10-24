/**
 * Zero-dependency YAML syntax highlighter for the artifact source view.
 *
 * A line-level tokenizer in the read-only tradition of this plugin: it never
 * parses (malformed YAML still highlights — the semantic view is what reports
 * parse errors), never throws, and never loses text: joining every token's
 * text reproduces the input line exactly. Token classes cover what the
 * roadmap details require — keys, scalars, array/map structure, comments,
 * anchors and aliases — plus a plain class for everything else.
 *
 * The large-YAML folding is here too: foldYaml groups lines under their
 * top-level key so the client can render each region collapsed instead of
 * mounting a novel-length listing at once.
 */

/** Token class → one color role in the source view. */
export type YamlTokenClass = 'key' | 'scalar' | 'structure' | 'comment' | 'anchor' | 'alias' | 'plain';

/** One colored span of a line. */
export interface YamlToken {
  text: string;
  cls: YamlTokenClass;
}

/** One highlighted line; `indent` carries the leading spaces separately. */
export interface YamlLine {
  indent: string;
  tokens: YamlToken[];
}

/** A foldable region: the lines under one top-level key or list item. */
export interface YamlFold {
  /** Header text of the region (the `key:` line or first list item line). */
  header: string;
  /** All lines of the region, header included, verbatim. */
  lines: string[];
}

/** Split a scalar into anchor/alias/quoted parts. */
function scalarTokens(raw: string): YamlToken[] {
  const tokens: YamlToken[] = [];
  let rest = raw;
  while (rest !== '') {
    // Anchor &name or alias *name at the value head.
    const bind = rest.match(/^(\s*[&*][^\s,[\]{}]+)/);
    if (bind !== null && bind[1] !== undefined) {
      tokens.push({ text: bind[1], cls: bind[1].trim().startsWith('&') ? 'anchor' : 'alias' });
      rest = rest.slice(bind[1].length);
      continue;
    }
    // Quoted scalar (single or double), kept whole including its quotes.
    const quote = rest.match(/^(\s*"([^"\\]|\\.)*"|\s*'[^']*')/);
    if (quote !== null && quote[1] !== undefined) {
      tokens.push({ text: quote[1], cls: 'scalar' });
      rest = rest.slice(quote[1].length);
      continue;
    }
    // Flow structure characters get their own class.
    const flow = rest.match(/^[\s,[\]{}]+/);
    if (flow !== null) {
      tokens.push({ text: flow[0], cls: 'structure' });
      rest = rest.slice(flow[0].length);
      continue;
    }
    // Plain scalar: everything up to the next structure character, flow
    // comma, or a trailing comment.
    const plain = rest.match(/^[^,[\]{}]+?(?=\s*(?:[,[\]{}]|\s#|$))/);
    if (plain !== null && plain[0].trim() !== '') {
      tokens.push({ text: plain[0], cls: 'scalar' });
      rest = rest.slice(plain[0].length);
      continue;
    }
    // Anything else (stray character) is plain, so text is never dropped.
    const first = rest[0] ?? '';
    tokens.push({ text: first, cls: 'plain' });
    rest = rest.slice(first.length);
  }
  return tokens;
}

/** Highlight one line; never throws and never loses characters. */
export function highlightLine(line: string): YamlLine {
  const indentMatch = line.match(/^[ \t]*/);
  const indent = indentMatch === null ? '' : indentMatch[0];
  let rest = line.slice(indent.length);
  if (rest.trim() === '' || rest.trimStart().startsWith('#')) {
    return rest === '' ? { indent, tokens: [] } : { indent, tokens: [{ text: rest, cls: 'comment' }] };
  }
  const tokens: YamlToken[] = [];
  // List item dash (with any further nested dashes, e.g. nested lists).
  while (true) {
    const dash = rest.match(/^(-\s+)/);
    if (dash === null || dash[1] === undefined) break;
    tokens.push({ text: dash[1], cls: 'structure' });
    rest = rest.slice(dash[1].length);
  }
  // key: value — the key runs to the first `: ` or trailing colon outside quotes.
  const keyMatch = rest.match(/^("(?:[^"\\]|\\.)*"|'[^']*'|[^:#]+?)\s*:(?=\s|$)/);
  if (keyMatch !== null && keyMatch[1] !== undefined && keyMatch[0] !== undefined) {
    tokens.push({ text: keyMatch[1], cls: 'key' });
    tokens.push({ text: rest.slice(keyMatch[1].length, keyMatch[0].length), cls: 'structure' });
    rest = rest.slice(keyMatch[0].length);
  }
  // Trailing comment splits off first; the value before it is a scalar.
  const commentAt = rest.indexOf(' #');
  let value = rest;
  let comment = '';
  if (commentAt >= 0) {
    value = rest.slice(0, commentAt + 1);
    comment = rest.slice(commentAt + 1);
  }
  if (value.trim() !== '') tokens.push(...scalarTokens(value));
  if (comment !== '') tokens.push({ text: comment, cls: 'comment' });
  return { indent, tokens };
}

/** Highlight a whole document into lines. */
export function highlightYaml(text: string): YamlLine[] {
  return text.split('\n').map((line) => highlightLine(line));
}

/** A top-level mapping key line (unindented, not a comment or list item). */
function isTopLevelKey(line: string): boolean {
  return /^[^\s#-]/.test(line);
}

/** Whether every line collected so far is a comment or blank (a doc header). */
function isCommentHeader(lines: string[]): boolean {
  return lines.every((line) => line.trim() === '' || line.trimStart().startsWith('#'));
}

/**
 * Group document lines into foldable regions by top-level key. Leading
 * comment lines merge into the first key's region (they are its header in
 * wa-cli's artifacts), and indented, list, comment and blank lines attach to
 * the region above them, so every input line lands in exactly one region.
 */
export function foldYaml(text: string): YamlFold[] {
  const folds: YamlFold[] = [];
  let current: YamlFold | null = null;
  for (const line of text.split('\n')) {
    if (current !== null && isTopLevelKey(line) && !isCommentHeader(current.lines)) {
      current = null;
    }
    if (current === null) {
      current = { header: line, lines: [line] };
      folds.push(current);
      continue;
    }
    current.lines.push(line);
  }
  return folds;
}
