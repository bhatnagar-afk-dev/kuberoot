<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { EventsView, ViewPhase, reopenOnReconnect } from '../resources.svelte';
  import RowGrid from '../RowGrid.svelte';
  import { compareNewest, displayColumns } from '../rows';
  import type { DetailTabProps } from './types';

  let { ref, live }: DetailTabProps = $props();

  // The tab is re-created for each object, so the view's ref never changes.
  const view = untrack(() => new EventsView(ref));
  onMount(() => {
    void view.open();
    return () => view.close();
  });
  reopenOnReconnect(view, () => live);

  // Every event is about this object, so its Object column says nothing.
  const columns = $derived(displayColumns(view.columns, false).filter((c) => c.name !== 'Object'));
  const phase = $derived(view.status.phase);
  const message = $derived.by(() => {
    if (view.rows.length > 0) return undefined;
    if (phase === ViewPhase.ViewLoading) return 'Loading events…';
    if (phase === ViewPhase.ViewError) return undefined; // the banner says why
    return 'No events';
  });
</script>

{#if view.status.message}
  <div class="banner" role="status">{view.status.message}</div>
{/if}
<RowGrid {columns} rows={view.rows} {message} order={compareNewest} wide="Message" />

<style>
  .banner {
    margin: 8px 12px;
    padding: 8px 12px;
    border: 1px solid var(--warn);
    border-radius: var(--radius);
    background: var(--warn-bg);
  }
</style>
