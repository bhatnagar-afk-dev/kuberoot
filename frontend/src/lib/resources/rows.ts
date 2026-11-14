// Pure helpers for resource tables: applying batches, formatting, sorting,
// filtering and virtual scrolling. Kept free of Svelte state so they are easy
// to reason about and reuse.
import type { Column, Row, RowBatch } from '../../../bindings/kuberoot.in/kuberoot/internal/resources';

/** Applies a batch to rows keyed by UID. A reset batch replaces everything. */
export function applyBatch(rows: Map<string, Row>, batch: RowBatch): void {
  if (batch.reset) rows.clear();
  for (const uid of batch.deletes ?? []) rows.delete(uid);
  for (const r of batch.upserts ?? []) rows.set(r.uid, r);
}

/** Formats an age like `kubectl get` does (e.g. 45s, 7m12s, 3h20m, 12d). */
export function formatAge(created: string, now: number): string {
  const t = Date.parse(created);
  if (Number.isNaN(t) || t <= 0) return '<unknown>';
  const s = Math.max(0, Math.floor((now - t) / 1000));
  const m = Math.floor(s / 60);
  const h = Math.floor(m / 60);
  const d = Math.floor(h / 24);
  const y = Math.floor(d / 365);
  if (s < 120) return `${s}s`;
  if (m < 10) return s % 60 ? `${m}m${s % 60}s` : `${m}m`;
  if (m < 180) return `${m}m`;
  if (h < 8) return m % 60 ? `${h}h${m % 60}m` : `${h}h`;
  if (h < 48) return `${h}h`;
  if (h < 192) return h % 24 ? `${d}d${h % 24}h` : `${d}d`;
  if (y < 2) return `${d}d`;
  if (y < 8) return d % 365 ? `${y}y${d % 365}d` : `${y}y`;
  return `${y}y`;
}

/** Text shown for a server-formatted cell. */
export function cellText(v: unknown): string {
  if (v === null || v === undefined) return '';
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

/**
 * A displayed column: a server column (by index into Row.cells), or the
 * Namespace column the UI adds when a view spans namespaces.
 */
export type DisplayColumn =
  | { kind: 'namespace'; name: string }
  | { kind: 'age'; name: string; index: number }
  | { kind: 'cell'; name: string; index: number; type: string; isName: boolean };

/**
 * Picks the columns to show: priority-0 server columns, with Namespace
 * inserted first when withNamespace is set. Age columns are computed from the
 * creation time so they stay current.
 */
export function displayColumns(columns: Column[], withNamespace: boolean): DisplayColumn[] {
  const out: DisplayColumn[] = [];
  const name = nameIndex(columns);
  columns.forEach((c, index) => {
    if (c.priority > 0) return;
    if (c.type === 'date' && c.name.toLowerCase() === 'age') {
      out.push({ kind: 'age', name: c.name, index });
    } else {
      out.push({ kind: 'cell', name: c.name, index, type: c.type, isName: index === name });
    }
  });
  if (withNamespace) out.unshift({ kind: 'namespace', name: 'Namespace' });
  return out;
}

/** The index of the object-name column, or -1. */
function nameIndex(columns: Column[]): number {
  const i = columns.findIndex((c) => c.format === 'name');
  return i >= 0 ? i : columns.findIndex((c) => c.name.toLowerCase() === 'name');
}

export function displayText(col: DisplayColumn, row: Row, now: number): string {
  switch (col.kind) {
    case 'namespace':
      return row.namespace ?? '';
    case 'age':
      return formatAge(row.created, now);
    case 'cell':
      return cellText(row.cells?.[col.index]);
  }
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

/** Default order: namespace, then name. */
export function compareDefault(a: Row, b: Row): number {
  return collator.compare(a.namespace ?? '', b.namespace ?? '') || collator.compare(a.name, b.name);
}

/** Newest first, e.g. for events. */
export function compareNewest(a: Row, b: Row): number {
  return Date.parse(b.created) - Date.parse(a.created) || compareDefault(a, b);
}

/** Returns a comparator for col, falling back to the default order on ties. */
export function comparator(
  col: DisplayColumn,
  desc: boolean,
  tieBreak: (a: Row, b: Row) => number = compareDefault,
): (a: Row, b: Row) => number {
  const dir = desc ? -1 : 1;
  let cmp: (a: Row, b: Row) => number;
  switch (col.kind) {
    case 'namespace':
      cmp = (a, b) => collator.compare(a.namespace ?? '', b.namespace ?? '');
      break;
    case 'age':
      // Ascending age: youngest (latest created) first.
      cmp = (a, b) => Date.parse(b.created) - Date.parse(a.created);
      break;
    case 'cell': {
      const i = col.index;
      if (col.type === 'integer' || col.type === 'number') {
        cmp = (a, b) => num(a.cells?.[i]) - num(b.cells?.[i]);
      } else {
        cmp = (a, b) => collator.compare(cellText(a.cells?.[i]), cellText(b.cells?.[i]));
      }
    }
  }
  return (a, b) => dir * cmp(a, b) || tieBreak(a, b);
}

function num(v: unknown): number {
  const n = typeof v === 'number' ? v : Number(v);
  return Number.isNaN(n) ? -Infinity : n;
}

/** Keeps rows whose name, namespace or any cell contains text (case-insensitive). */
export function filterRows(rows: Row[], text: string): Row[] {
  const q = text.trim().toLowerCase();
  if (!q) return rows;
  return rows.filter(
    (r) =>
      r.name.toLowerCase().includes(q) ||
      (r.namespace ?? '').toLowerCase().includes(q) ||
      (r.cells ?? []).some((c) => cellText(c).toLowerCase().includes(q)),
  );
}

/** The slice of rows to render for a fixed-height virtual list. */
export function visibleRange(
  scrollTop: number,
  viewportHeight: number,
  rowHeight: number,
  total: number,
  overscan = 10,
): { start: number; end: number } {
  const start = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan);
  const end = Math.min(total, Math.ceil((scrollTop + viewportHeight) / rowHeight) + overscan);
  return { start, end: Math.max(start, end) };
}
