<script lang="ts">
  import { clusters, shortPath, type Context } from './clusters.svelte';
  import StatusDot from './StatusDot.svelte';

  let { onManageSources }: { onManageSources: () => void } = $props();

  let filter = $state('');

  const groups = $derived.by(() => {
    const q = filter.trim().toLowerCase();
    const bySource = new Map<string, Context[]>();
    for (const c of clusters.contexts) {
      if (q && ![c.name, c.cluster, c.server].some((v) => v.toLowerCase().includes(q))) continue;
      const list = bySource.get(c.source) ?? [];
      list.push(c);
      bySource.set(c.source, list);
    }
    return [...bySource.entries()];
  });
</script>

<aside class="sidebar">
  <div class="brand">KubeRoot</div>

  <input
    class="filter"
    type="search"
    placeholder="Filter contexts"
    aria-label="Filter contexts"
    bind:value={filter}
  />

  <nav class="list" aria-label="Contexts">
    {#each groups as [source, contexts] (source)}
      <div class="group">
        <div class="source" title={source}>{shortPath(source)}</div>
        {#each contexts as c (c.id)}
          <button
            class="context"
            class:active={c.id === clusters.activeId}
            onclick={() => clusters.select(c.id)}
            title={c.server}
          >
            <StatusDot phase={clusters.status(c.id).phase} />
            <span class="name">{c.name}</span>
          </button>
        {/each}
      </div>
    {:else}
      {#if clusters.loaded}
        <p class="empty">
          {filter ? 'No contexts match.' : 'No kubeconfig contexts found. Add a kubeconfig file or folder.'}
        </p>
      {/if}
    {/each}
  </nav>

  <div class="footer">
    <div class="count">
      {clusters.connectedCount}/{clusters.maxConnections} connected
    </div>
    <div class="actions">
      <button onclick={() => clusters.pickSource(false)}>+ File</button>
      <button onclick={() => clusters.pickSource(true)}>+ Folder</button>
      <button onclick={onManageSources} title="Manage kubeconfig sources">Sources</button>
    </div>
  </div>
</aside>

<style>
  .sidebar {
    display: flex;
    flex-direction: column;
    min-height: 0;
    background: var(--surface);
    border-right: 1px solid var(--border);
  }
  .brand {
    /* Leaves room for the macOS inset title bar and acts as a drag handle. */
    padding: 48px 16px 12px;
    font-weight: 600;
    font-size: 16px;
    color: var(--accent);
    --wails-draggable: drag;
  }
  .filter {
    margin: 0 12px 8px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--text);
    font: inherit;
  }
  .list {
    flex: 1;
    overflow-y: auto;
    padding: 0 8px 8px;
  }
  .source {
    padding: 10px 8px 4px;
    font-size: 11px;
    color: var(--muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .context {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 5px 8px;
    border: 0;
    border-radius: 6px;
    background: none;
    color: var(--text);
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .context:hover { background: var(--hover); }
  .context.active { background: var(--selected); }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .empty {
    padding: 8px;
    color: var(--muted);
  }
  .footer {
    padding: 8px 12px 12px;
    border-top: 1px solid var(--border);
  }
  .count {
    margin-bottom: 6px;
    font-size: 12px;
    color: var(--muted);
  }
  .actions {
    display: flex;
    gap: 6px;
  }
  .actions button {
    flex: 1;
    padding: 4px 6px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: var(--text);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
  }
  .actions button:hover { background: var(--hover); }
</style>
