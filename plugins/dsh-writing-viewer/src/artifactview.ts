/**
 * Pure view model for the artifact (Artifact) semantic card view.
 *
 * The panel's artifact viewer organizes a YAML artifact by its schema fields
 * instead of dumping the file: brief, outline and claims get the three named
 * layouts the roadmap details call for, source.yaml gets the metadata layout
 * the sources view reuses, and every other artifact falls back to a generic
 * field-card walk so no artifact ever renders blank. Named layouts end with a
 * generic tail pass too, and shapes the card rows cannot express (numeric
 * arrays, mixed arrays, deep records) degrade to a JSON text row — the rule
 * throughout is that no field value is silently dropped. Hand-edited YAML
 * that does not parse never throws — it yields a parseError the client shows
 * above the highlighted source view (the malformed-yaml fallback).
 *
 * No fetching, no React, no DOM: the client bundle imports this and tsdown
 * inlines it, and the tests drive it with the canned testdata artifacts.
 */
import { parse } from 'yaml';
import type { ArtifactText } from './artifacts';
import type { SnapshotRouteBody, SourceSnapshot } from './snapshot';

/** One row of a semantic card. `sources` rows carry clickable source ids. */
export type CardRow =
  | { key: string; label: string; type: 'text'; text: string }
  | { key: string; label: string; type: 'list'; items: string[] }
  | { key: string; label: string; type: 'sources'; ids: string[] }
  | { key: string; label: string; type: 'group'; rows: CardRow[] };

/** One semantic card: a titled group of rows. */
export interface SemanticCard {
  id: string;
  title: string | null;
  rows: CardRow[];
}

/** Which layout the view uses; generic is the field-walk fallback. */
export type ArtifactLayout = 'brief' | 'outline' | 'claims' | 'source' | 'generic';

/** The semantic view of one YAML artifact. */
export interface ArtifactView {
  /** Artifact name as the detail list addresses it ('brief', 'claims', ...). */
  name: string;
  layout: ArtifactLayout;
  /** Non-null when the YAML text failed to parse; cards is empty then. */
  parseError: string | null;
  cards: SemanticCard[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function asString(value: unknown): string | null {
  return typeof value === 'string' && value !== '' ? value : null;
}

function asIds(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((id): id is string => typeof id === 'string' && id !== '') : [];
}

/** Parse YAML text, reporting a failure as a parseError instead of throwing. */
function parseArtifact(text: string): { doc: unknown | null; error: string | null } {
  try {
    return { doc: parse(text), error: null };
  } catch (cause) {
    return { doc: null, error: String((cause as Error)?.message ?? cause) };
  }
}

function textRow(key: string, value: unknown): CardRow | null {
  if (value === null || value === undefined) return null;
  if (typeof value === 'string') return value === '' ? null : { key, label: key, type: 'text', text: value };
  if (typeof value === 'number' || typeof value === 'boolean') return { key, label: key, type: 'text', text: String(value) };
  return null;
}

function listRow(key: string, value: unknown): CardRow | null {
  const items = asIds(value);
  return items.length === 0 ? null : { key, label: key, type: 'list', items };
}

function sourcesRow(key: string, value: unknown): CardRow | null {
  const ids = asIds(value);
  return ids.length === 0 ? null : { key, label: key, type: 'sources', ids };
}

/** Scalar fields of a record as text rows, in the order given. */
function scalarRows(record: Record<string, unknown>, fields: readonly string[]): CardRow[] {
  const rows: CardRow[] = [];
  for (const field of fields) {
    if (!(field in record)) continue;
    const row = textRow(field, record[field]);
    if (row !== null) rows.push(row);
  }
  return rows;
}

/** Any shape as one JSON text row, so no field value is silently dropped. */
function fallbackRow(key: string, value: unknown): CardRow | null {
  const plain = textRow(key, value);
  if (plain !== null) return plain;
  try {
    return { key, label: key, type: 'text', text: JSON.stringify(value) ?? String(value) };
  } catch {
    return { key, label: key, type: 'text', text: String(value) };
  }
}

/** An array as one row: string arrays as a list, source ids clickable; all
 * records return null so callers fan them out; anything else falls back to JSON. */
function arrayRow(key: string, value: unknown): CardRow | null {
  if (!Array.isArray(value) || value.length === 0) return null;
  if (value.every((item) => typeof item === 'string')) {
    if (key === 'sources' || key === 'uses') return sourcesRow(key, value);
    return listRow(key, value);
  }
  if (value.every((item) => isRecord(item))) return null;
  return fallbackRow(key, value);
}

/**
 * Rows for one nested record (premise, pov, one state change...): scalars as
 * text, string arrays as list, `sources` clickable, and any deeper shape as
 * a JSON text row — an empty group can no longer hide content.
 */
function nestedRows(record: Record<string, unknown>, key: string, label?: string): CardRow {
  const rows: CardRow[] = [];
  for (const [field, value] of Object.entries(record)) {
    if (Array.isArray(value)) {
      const row = arrayRow(field, value);
      if (row !== null) rows.push(row);
    } else if (isRecord(value)) {
      const row = fallbackRow(field, value);
      if (row !== null) rows.push(row);
    } else {
      const row = textRow(field, value);
      if (row !== null) rows.push(row);
    }
  }
  return { key, label: label ?? key, type: 'group', rows };
}

/**
 * Generic tail rows for the fields a named layout did not consume, so schema
 * additions and hand-written extras stay visible (mirrors outlineCards).
 */
function tailRows(record: Record<string, unknown>, covered: readonly string[], keyPrefix: string): CardRow[] {
  const rows: CardRow[] = [];
  for (const [field, value] of Object.entries(record)) {
    if (field === 'schema_version' || covered.includes(field)) continue;
    if (Array.isArray(value)) {
      if (value.every((item) => isRecord(item))) {
        const groups = value.map((item, index) => nestedRows(item, `${keyPrefix}-${field}-${index}`, field));
        if (groups.length > 0) rows.push({ key: field, label: field, type: 'group', rows: groups });
      } else {
        const row = arrayRow(field, value);
        if (row !== null) rows.push(row);
      }
    } else if (isRecord(value)) {
      rows.push(nestedRows(value, field));
    } else {
      const row = textRow(field, value);
      if (row !== null) rows.push(row);
    }
  }
  return rows;
}

/** brief.yaml: one base card (custom fields plus the generic tail) and one card per key message. */
function briefCards(doc: Record<string, unknown>): SemanticCard[] {
  const rows: CardRow[] = [];
  rows.push(...scalarRows(doc, ['audience', 'purpose', 'theme', 'method']));
  if (isRecord(doc.premise)) rows.push(nestedRows(doc.premise, 'premise'));
  if (isRecord(doc.pov)) rows.push(nestedRows(doc.pov, 'pov'));
  if (isRecord(doc.length)) {
    const length = doc.length as Record<string, unknown>;
    const text = [length.min, length.max].every((n) => typeof n === 'number') ? `${length.min}–${length.max}` : null;
    if (text !== null) rows.push({ key: 'length', label: 'length', type: 'text', text });
  }
  const avoid = listRow('avoid', doc.avoid);
  if (avoid !== null) rows.push(avoid);
  const sources = sourcesRow('sources', doc.sources);
  if (sources !== null) rows.push(sources);
  rows.push(...tailRows(doc, ['audience', 'purpose', 'theme', 'method', 'premise', 'pov', 'length', 'avoid', 'sources', 'key_messages'], 'brief'));
  const messages = Array.isArray(doc.key_messages) ? doc.key_messages : [];
  const cards: SemanticCard[] = messages.length === 0 && rows.length === 0 ? [] : [{ id: 'brief', title: null, rows }];
  messages.forEach((raw, index) => {
    if (!isRecord(raw)) return;
    const id = asString(raw.id) ?? String(index);
    const text = asString(raw.text);
    if (id === null && text === null) return;
    const messageRows: CardRow[] = [];
    const idRow = textRow('id', raw.id);
    if (idRow !== null) messageRows.push(idRow);
    if (text !== null) messageRows.push({ key: 'text', label: 'text', type: 'text', text });
    const messageSources = sourcesRow('sources', raw.sources);
    if (messageSources !== null) messageRows.push(messageSources);
    // decision and any extra field stay visible through the tail.
    messageRows.push(...tailRows(raw, ['id', 'text', 'sources'], 'key-message-' + id));
    cards.push({ id: 'key-message-' + id, title: text, rows: messageRows });
  });
  return cards;
}

/**
 * outline.yaml: one card per section in file order. Unconsumed fields ride
 * along through the same tail pass, so scene-contract extensions stay visible.
 */
function outlineCards(doc: Record<string, unknown>): SemanticCard[] {
  const cards: SemanticCard[] = [];
  const sections = Array.isArray(doc.sections) ? doc.sections : [];
  sections.forEach((raw, index) => {
    if (!isRecord(raw)) return;
    const id = asString(raw.id) ?? String(index);
    const rows: CardRow[] = [];
    const idRow = textRow('id', raw.id);
    if (idRow !== null) rows.push(idRow);
    const role = textRow('role', raw.role);
    if (role !== null) rows.push(role);
    const covers = listRow('covers', raw.covers);
    if (covers !== null) rows.push(covers);
    const uses = sourcesRow('uses', raw.uses);
    if (uses !== null) rows.push(uses);
    const reason = textRow('reason', raw.reason);
    if (reason !== null) rows.push(reason);
    rows.push(...tailRows(raw, ['id', 'title', 'role', 'covers', 'uses', 'reason'], 'section-' + id));
    cards.push({ id: 'section-' + id, title: asString(raw.title), rows });
  });
  return cards;
}

/** claims.yaml: one card per claim, its source references clickable, tail fields visible. */
function claimsCards(doc: Record<string, unknown>): SemanticCard[] {
  const cards: SemanticCard[] = [];
  const claims = Array.isArray(doc.claims) ? doc.claims : [];
  claims.forEach((raw, index) => {
    if (!isRecord(raw)) return;
    const id = asString(raw.id) ?? String(index);
    const text = asString(raw.text);
    if (id === null && text === null) return;
    const rows: CardRow[] = [];
    const idRow = textRow('id', raw.id);
    if (idRow !== null) rows.push(idRow);
    const kind = textRow('kind', raw.kind);
    if (kind !== null) rows.push(kind);
    const sources = sourcesRow('sources', raw.sources);
    if (sources !== null) rows.push(sources);
    rows.push(...tailRows(raw, ['id', 'kind', 'text', 'sources'], 'claim-' + id));
    cards.push({ id: 'claim-' + id, title: text, rows });
  });
  return cards;
}

/** source.yaml metadata, reused by the sources view. */
function sourceCards(doc: Record<string, unknown>): SemanticCard[] {
  const covered = ['id', 'type', 'title', 'original_name', 'file', 'sha256', 'bytes', 'url', 'tier'];
  const rows = scalarRows(doc, covered);
  rows.push(...tailRows(doc, covered, 'source'));
  return rows.length === 0 ? [] : [{ id: 'source', title: asString(doc.title), rows }];
}

/** Generic fallback: organize any parsed YAML into field cards. */
function genericCards(name: string, doc: unknown): SemanticCard[] {
  if (!isRecord(doc)) {
    if (Array.isArray(doc) && doc.length > 0 && doc.every(isRecord)) {
      return doc.map((item, index) => ({ id: `${name}-${index}`, title: null, rows: itemRows(item) }));
    }
    const text = doc === null || doc === undefined ? null : JSON.stringify(doc);
    return text === null ? [] : [{ id: name, title: null, rows: [{ key: 'value', label: 'value', type: 'text', text }] }];
  }
  const rows: CardRow[] = [];
  const cards: SemanticCard[] = [];
  for (const [field, value] of Object.entries(doc)) {
    if (field === 'schema_version') continue;
    if (Array.isArray(value)) {
      if (value.every((item) => isRecord(item))) {
        value.forEach((item, index) => {
          cards.push({ id: `${name}-${field}-${index}`, title: asString(item.id) ?? asString(item.title), rows: itemRows(item) });
        });
      } else {
        const row = arrayRow(field, value);
        if (row !== null) rows.push(row);
      }
    } else if (isRecord(value)) {
      rows.push(nestedRows(value, field));
    } else {
      const row = textRow(field, value);
      if (row !== null) rows.push(row);
    }
  }
  if (rows.length > 0) cards.unshift({ id: name, title: null, rows });
  return cards;
}

/** Rows of one record item inside a generic artifact (one nesting level, JSON fallbacks). */
function itemRows(item: Record<string, unknown>): CardRow[] {
  const rows: CardRow[] = [];
  for (const [field, value] of Object.entries(item)) {
    if (field === 'schema_version') continue;
    if (Array.isArray(value)) {
      if (value.every((entry) => isRecord(entry))) {
        const groups = value.map((entry, index) => nestedRows(entry, `${field}-${index}`, field));
        if (groups.length > 0) rows.push({ key: field, label: field, type: 'group', rows: groups });
      } else {
        const row = arrayRow(field, value);
        if (row !== null) rows.push(row);
      }
    } else if (isRecord(value)) {
      rows.push(nestedRows(value, field));
    } else {
      const row = textRow(field, value);
      if (row !== null) rows.push(row);
    }
  }
  return rows;
}

/** Pick the named layout for an artifact name; everything else is generic. */
export function layoutFor(name: string): ArtifactLayout {
  if (name === 'brief' || name === 'outline' || name === 'claims' || name === 'source') return name;
  return 'generic';
}

/** Build the semantic view of one YAML artifact; never throws. */
export function artifactView(name: string, text: string): ArtifactView {
  const layout = layoutFor(name);
  const base: ArtifactView = { name, layout, parseError: null, cards: [] };
  // An empty or whitespace-only artifact is no data, not corruption: the
  // client shows the no-cards hint instead of a parse error.
  if (text.trim() === '') return base;
  const { doc, error } = parseArtifact(text);
  if (error !== null) return { ...base, parseError: error };
  if (doc === null || doc === undefined) return base;
  if (!isRecord(doc)) return { ...base, cards: genericCards(name, doc) };
  if (layout === 'brief') return { ...base, cards: briefCards(doc) };
  if (layout === 'outline') return { ...base, cards: outlineCards(doc) };
  if (layout === 'claims') return { ...base, cards: claimsCards(doc) };
  if (layout === 'source') return { ...base, cards: sourceCards(doc) };
  return { ...base, cards: genericCards(name, doc) };
}

/**
 * Per-artifact-object view cache: snapshot bodies are stable between fetches,
 * so re-renders (each path-input keystroke, selection clicks) reuse the
 * parsed view instead of re-parsing every artifact on every render.
 */
const viewCache = new WeakMap<object, ArtifactView>();

/** artifactView memoized by artifact object identity. */
export function cachedArtifactView(name: string, artifact: { text: string }): ArtifactView {
  const hit = viewCache.get(artifact);
  if (hit !== undefined && hit.name === name) return hit;
  const view = artifactView(name, artifact.text);
  viewCache.set(artifact, view);
  return view;
}

/** Full text of one artifact of a unit, by the name the detail list uses. */
export function artifactTextOf(body: SnapshotRouteBody, unitRef: string, name: string): ArtifactText | null {
  const snapshot = body.snapshot;
  if (snapshot === null || snapshot === undefined) return null;
  const workId = unitRef.split('/')[0] ?? unitRef;
  const work = snapshot.works.find((w) => w.id === workId);
  const unit = work?.units.find((u) => u.ref === unitRef);
  if (unit === undefined) return null;
  if (name === 'draft') return unit.draft;
  return unit.artifacts[name] ?? null;
}

/** The registered sources (Source) in snapshot order. */
export function sourceEntriesOf(body: SnapshotRouteBody): SourceSnapshot[] {
  return body.snapshot?.sources ?? [];
}
