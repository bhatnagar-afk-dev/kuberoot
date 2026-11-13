<script lang="ts">
  import { onMount, untrack, type Snippet } from 'svelte';
  import { ResourceView, ViewPhase, clock, type ViewSpec } from './resources.svelte';
  import {
    comparator,
    compareDefault,
    displayColumns,
    displayText,
    filterRows,
    visibleRange,
    type DisplayColumn,
  } from './rows';

  let {
    spec,
    title,
    connected,
    tools,
  }: {
    spec: ViewSpec;
    title: string;
    /** Whether the cluster is connected; the view reopens when it reconnects. */
    connected: boolean;
    /** Extra controls for the toolbar, e.g. a namespace picker. */
    tools?: Snippet;
  } = $props();

  const ROW_HEIGHT = 28;

  // A new context or type is a new component instance (keyed by the parent);
  // a namespace change reopens the view and keeps the rows until it's ready.
  const view = untrack(() => new ResourceView(spec));
  $effect(() => {
    const namespaces = spec.namespaces;
    untrack(() => {
      if (!sameList(namespaces, view.spec.namespaces)) void view.setNamespaces(namespaces);
    });
  });

  function sameList(a: string[], b: string[]): boolean {
    return a.length === b.length && a.every((x, i) => x === b[i]);
  }

  onMount(() => {
    void view.open();
    const stopClock = clock.use();
    return () => {
      stopClock();
      view.close();
    };
  });

  // A new connection means the old session's view is dead: reopen it.
  let wasConnected = untrack(() => connected);
  $effect(() => {
    const now = connected;
    untrack(() => {
      if (now && !wasConnected) void view.reopen();
      wasConnected = now;
    });
  });

  let filter = $state('');
  let sortBy: number | null = $state(null); // index into columns
  let desc = $state(false);

  const withNamespace = $derived(view.spec.type.namespaced && view.spec.namespaces.length !== 1);
  const columns: DisplayColumn[] = $derived(displayColumns(view.columns, withNamespace));
  const shown = $derived.by(() => {
    const rows = filterRows(view.rows, filter);
    const col = sortBy === null ? undefined : columns[sortBy];
    return rows.toSorted(col ? comparator(col, desc) : compareDefault);
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

  let scrollTop = $state(0);
  let viewportHeight = $state(0);
  const range = $derived(visibleRange(scrollTop, viewportHeight, ROW_HEIGHT, shown.length));

  const template = $derived(columns.map(width).join(' '));

  function width(c: DisplayColumn): string {
    switch (c.kind) {
      case 'namespace':
        return 'minmax(120px, 1.2fr)';
      case 'age':
        return '72px';
      case 'cell':
        if (c.index === nameIndex(view.columns)) return 'minmax(220px, 3fr)';
        return c.type === 'integer' || c.type === 'number' ? 'minmax(64px, 0.6fr)' : 'minmax(90px, 1fr)';
    }
  }

  function nameIndex(cols: { format?: string; name: string }[]): number {
    const i = cols.findIndex((c) => c.format === 'name');
    return i >= 0 ? i : cols.findIndex((c) => c.name.toLowerCase() === 'name');
  }

  const phase = $derived(view.status.phase);
  const loading = $derived(phase === ViewPhase.ViewLoading && view.rows.length === 0);
</script>

<section class="table" class:stale={phase === ViewPhase.ViewStale}>
  <div class="toolbar">
    <h2>{title}</h2>
    <span class="count">
      {#if !loading}
        {shown.length === view.rows.length ? view.rows.length : `${shown.length} of ${view.rows.length}`}
      {/if}
    </span>
    {#if phase === ViewPhase.ViewLoading && view.rows.length > 0}
      <span class="muted">Updating…</span>
    {/if}
    <div class="tools">
      {@render tools?.()}
      <input type="search" placeholder="Filter" bind:value={filter} spellcheck="false" aria-label="Filter rows" />
    </div>
  </div>

  {#if phase === ViewPhase.ViewStale}
    <div class="banner" role="status">Disconnected. Showing the last known {title.toLowerCase()}.</div>
  {:else if view.status.message}
    <div class="banner warn" role="status">{view.status.message}</div>
  {/if}

  <div
    class="scroll"
    role="grid"
    aria-rowcount={shown.length}
    bind:clientHeight={viewportHeight}
    onscroll={(e) => (scrollTop = e.currentTarget.scrollTop)}
  >
    <div class="row head" style:grid-template-columns={template} role="row">
      {#each columns as col, i (col.name + i)}
        <button class="cell" role="columnheader" onclick={() => toggleSort(i)} aria-sort={sortBy === i ? (desc ? 'descending' : 'ascending') : 'none'}>
          {col.name}
          {#if sortBy === i}<span class="arrow">{desc ? '▾' : '▴'}</span>{/if}
        </button>
      {/each}
    </div>

    {#if loading}
      <p class="empty">Loading {title.toLowerCase()}…</p>
    {:else if shown.length === 0 && phase !== ViewPhase.ViewError}
      <p class="empty">{view.rows.length === 0 ? `No ${title.toLowerCase()}` : 'No rows match the filter'}</p>
    {:else}
      <div class="body" style:height="{shown.length * ROW_HEIGHT}px">
        {#each shown.slice(range.start, range.end) as row, i (row.uid)}
          <div
            class="row"
            role="row"
            style:grid-template-columns={template}
            style:transform="translateY({(range.start + i) * ROW_HEIGHT}px)"
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
</section>

<style>
  .table {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }
  .table.stale .scroll { opacity: 0.55; }
  .toolbar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 16px;
  }
  h2 {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
  }
  .count, .muted {
    color: var(--muted);
    font-size: 12px;
  }
  .tools {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-left: auto;
  }
  input {
    width: 220px;
    padding: 5px 10px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  .banner {
    margin: 0 16px 8px;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  .banner.warn {
    border-color: var(--warn);
    background: var(--warn-bg);
  }
  .scroll {
    flex: 1;
    min-height: 0;
    overflow: auto;
    border-top: 1px solid var(--border);
  }
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
