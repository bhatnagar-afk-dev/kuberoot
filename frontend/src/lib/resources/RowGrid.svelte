<script lang="ts">
  import { onMount } from 'svelte';
  import { clock, type Row } from './resources.svelte';
  import { comparator, compareDefault, displayText, visibleRange, type DisplayColumn } from './rows';

  let {
    columns,
    rows,
    message,
    order = compareDefault,
    wide,
    selected,
    onselect,
  }: {
    columns: DisplayColumn[];
    rows: Row[];
    /** Shown instead of the rows, e.g. "Loading…" or "No pods". */
    message?: string;
    /** Order before the user picks a column, and for ties. */
    order?: (a: Row, b: Row) => number;
    /** The column that gets the most room; the object name by default. */
    wide?: string;
    /** UID of the selected row. */
    selected?: string;
    /** Makes rows selectable (click, ↑/↓). */
    onselect?: (row: Row) => void;
  } = $props();

  const ROW_HEIGHT = 28;

  onMount(() => clock.use());

  let sortBy: number | null = $state(null); // index into columns
  let desc = $state(false);

  const sorted = $derived.by(() => {
    const col = sortBy === null ? undefined : columns[sortBy];
    return rows.toSorted(col ? comparator(col, desc, order) : order);
  });

  function toggleSort(i: number) {
    if (sortBy === i) {
      if (desc) sortBy = null; // third click: back to default order
      desc = !desc;
    } else {
      sortBy = i;
      desc = false;
    }
  }

  let scroller: HTMLDivElement;
  let scrollTop = $state(0);
  let viewportHeight = $state(0);
  const range = $derived(visibleRange(scrollTop, viewportHeight, ROW_HEIGHT, sorted.length));

  const template = $derived(columns.map(width).join(' '));

  function width(c: DisplayColumn): string {
    switch (c.kind) {
      case 'namespace':
        return 'minmax(120px, 1.2fr)';
      case 'age':
        return '72px';
      case 'cell':
        if (wide === undefined ? c.isName : c.name === wide) return 'minmax(220px, 3fr)';
        return c.type === 'integer' || c.type === 'number' ? 'minmax(64px, 0.6fr)' : 'minmax(90px, 1fr)';
    }
  }

  function onkeydown(e: KeyboardEvent) {
    if (!onselect || (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') || sorted.length === 0) return;
    e.preventDefault();
    const i = sorted.findIndex((r) => r.uid === selected);
    const next = i < 0 ? 0 : Math.min(sorted.length - 1, Math.max(0, i + (e.key === 'ArrowDown' ? 1 : -1)));
    onselect(sorted[next]);
    // Keep the row in view, below the sticky header.
    const top = next * ROW_HEIGHT;
    const bottom = top + 2 * ROW_HEIGHT;
    if (top < scroller.scrollTop) scroller.scrollTop = top;
    else if (bottom > scroller.scrollTop + viewportHeight) scroller.scrollTop = bottom - viewportHeight;
  }
</script>

<div
  class="scroll"
  role="grid"
  tabindex={onselect ? 0 : -1}
  aria-rowcount={sorted.length}
  bind:this={scroller}
  bind:clientHeight={viewportHeight}
  onscroll={(e) => (scrollTop = e.currentTarget.scrollTop)}
  {onkeydown}
>
  <div class="row head" style:grid-template-columns={template} role="row">
    {#each columns as col, i (col.name + i)}
      <button
        class="cell"
        role="columnheader"
        tabindex="-1"
        onclick={() => toggleSort(i)}
        aria-sort={sortBy === i ? (desc ? 'descending' : 'ascending') : 'none'}
      >
        {col.name}
        {#if sortBy === i}<span class="arrow">{desc ? '▾' : '▴'}</span>{/if}
      </button>
    {/each}
  </div>

  {#if message}
    <p class="empty">{message}</p>
  {:else}
    <div class="body" style:height="{sorted.length * ROW_HEIGHT}px">
      {#each sorted.slice(range.start, range.end) as row, i (row.uid)}
        <!-- svelte-ignore a11y_click_events_have_key_events (the grid handles ↑/↓) -->
        <div
          class="row"
          class:selectable={!!onselect}
          class:selected={row.uid === selected}
          role="row"
          tabindex="-1"
          aria-selected={row.uid === selected}
          style:grid-template-columns={template}
          style:transform="translateY({(range.start + i) * ROW_HEIGHT}px)"
          onclick={() => onselect?.(row)}
        >
          {#each columns as col, j (j)}
            {@const text = displayText(col, row, clock.now)}
            <div class="cell" role="gridcell" title={text}>{text}</div>
          {/each}
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .scroll {
    flex: 1;
    min-height: 0;
    overflow: auto;
    border-top: 1px solid var(--border);
  }
  .scroll:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .row {
    display: grid;
    height: 28px;
    min-width: fit-content;
    align-items: center;
  }
  .body {
    position: relative;
  }
  .body .row {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    border-bottom: 1px solid var(--border);
  }
  .body .row:hover { background: var(--hover); }
  .body .row.selectable { cursor: default; }
  .body .row.selected { background: var(--selected); }
  .head {
    position: sticky;
    top: 0;
    z-index: 1;
    background: var(--bg);
    border-bottom: 1px solid var(--border);
  }
  .cell {
    padding: 0 12px;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
    font-size: 13px;
  }
  .head .cell {
    height: 100%;
    border: 0;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    text-align: left;
    text-transform: uppercase;
    cursor: pointer;
  }
  .head .cell:hover { color: var(--text); }
  .arrow { margin-left: 2px; }
  .empty {
    padding: 24px 16px;
    margin: 0;
    color: var(--muted);
  }
</style>
