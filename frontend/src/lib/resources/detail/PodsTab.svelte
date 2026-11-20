<script lang="ts">
  import RowGrid from '../RowGrid.svelte';
  import { ViewPhase, type PodsView, type Row } from '../resources.svelte';
  import { displayColumns } from '../rows';

  let {
    view,
    onopen,
  }: {
    /** Owned by the panel, so a pod opened from here stays live. */
    view: PodsView;
    onopen: (pod: Row) => void;
  } = $props();

  let selected: string | undefined = $state();

  // Every pod is in the object's namespace.
  const columns = $derived(displayColumns(view.columns, false));
  const phase = $derived(view.status.phase);
  // With no rows, an error (e.g. "this service does not select pods") is the
  // message itself; with rows, it is a banner above them.
  const failed = $derived(phase === ViewPhase.ViewError && view.rows.length === 0);
  const message = $derived.by(() => {
    if (view.rows.length > 0) return undefined;
    if (failed) return sentence(view.status.message ?? 'Could not list pods');
    if (phase === ViewPhase.ViewLoading) return 'Loading pods…';
    return 'No pods';
  });

  function sentence(s: string): string {
    return s.charAt(0).toUpperCase() + s.slice(1);
  }
</script>

{#if view.status.message && !failed}
  <div class="banner" role="status">{view.status.message}</div>
{/if}
<RowGrid {columns} rows={view.rows} {message} {selected} onselect={(r) => (selected = r.uid)} {onopen} />

<style>
  .banner {
    margin: 8px 12px;
    padding: 8px 12px;
    border: 1px solid var(--warn);
    border-radius: var(--radius);
    background: var(--warn-bg);
  }
</style>
