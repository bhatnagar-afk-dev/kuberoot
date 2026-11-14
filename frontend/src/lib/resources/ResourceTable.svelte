<script lang="ts">
  import { onMount, untrack, type Snippet } from 'svelte';
  import DetailPanel from './DetailPanel.svelte';
  import RowGrid from './RowGrid.svelte';
  import { ResourceView, ViewPhase, reopenOnReconnect, type Row, type ViewSpec } from './resources.svelte';
  import { displayColumns, filterRows } from './rows';

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

  // A new context or type is a new component instance (keyed by the parent);
  // a namespace change reopens the view and keeps the rows until it's ready.
  const view = untrack(() => new ResourceView(spec));
  $effect(() => {
    const namespaces = spec.namespaces;
    untrack(() => {
      if (sameList(namespaces, view.spec.namespaces)) return;
      // An object outside the new namespaces isn't deleted, just not shown.
      if (selected && namespaces.length > 0 && !namespaces.includes(selected.namespace ?? '')) selected = undefined;
      void view.setNamespaces(namespaces);
    });
  });

  function sameList(a: string[], b: string[]): boolean {
    return a.length === b.length && a.every((x, i) => x === b[i]);
  }

  onMount(() => {
    void view.open();
    return () => view.close();
  });

  // A new connection means the old session's view is dead: reopen it.
  reopenOnReconnect(view, () => connected);

  let filter = $state('');

  const withNamespace = $derived(view.spec.type.namespaced && view.spec.namespaces.length !== 1);
  const columns = $derived(displayColumns(view.columns, withNamespace));
  const shown = $derived(filterRows(view.rows, filter));

  // The selected object. Once it is gone from the rows, the panel keeps its
  // last known state and says it was deleted.
  let selected: Row | undefined = $state.raw();
  const current = $derived(selected && view.rows.find((r) => r.uid === selected!.uid));
  $effect(() => {
    if (current) selected = current;
  });

  function onkeydown(e: KeyboardEvent) {
    if (e.key !== 'Escape' || e.defaultPrevented || !selected) return;
    const t = e.target as HTMLElement | null;
    if (t?.closest('input, textarea, [contenteditable="true"]')) return;
    selected = undefined;
  }

  const phase = $derived(view.status.phase);
  const loading = $derived(phase === ViewPhase.ViewLoading && view.rows.length === 0);
  const message = $derived.by(() => {
    if (loading) return `Loading ${title.toLowerCase()}…`;
    if (shown.length > 0 || phase === ViewPhase.ViewError) return undefined;
    return view.rows.length === 0 ? `No ${title.toLowerCase()}` : 'No rows match the filter';
  });
</script>

<svelte:window {onkeydown} />

<div class="table">
  <section class="main" class:stale={phase === ViewPhase.ViewStale}>
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

    <RowGrid {columns} rows={shown} {message} selected={selected?.uid} onselect={(r) => (selected = r)} />
  </section>

  {#if selected}
    <DetailPanel
      contextId={view.spec.contextId}
      type={view.spec.type}
      object={selected}
      deleted={!current && phase !== ViewPhase.ViewLoading}
      {connected}
      onclose={() => (selected = undefined)}
    />
  {/if}
</div>

<style>
  .table {
    display: flex;
    flex: 1;
    min-width: 0;
    min-height: 0;
  }
  .main {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    min-height: 0;
  }
  .main.stale :global(.scroll) { opacity: 0.55; }
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
</style>
