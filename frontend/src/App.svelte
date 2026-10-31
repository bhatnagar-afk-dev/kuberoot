<script lang="ts">
  import { onMount } from 'svelte';
  import { SystemService } from '../bindings/github.com/bhatnagar-afk-dev/kuberoot/app';
  import type { Info } from '../bindings/github.com/bhatnagar-afk-dev/kuberoot/internal/version';
  import { clusters } from './lib/clusters.svelte';
  import Sidebar from './lib/Sidebar.svelte';
  import ContextDetail from './lib/ContextDetail.svelte';
  import Sources from './lib/Sources.svelte';

  let info: Info | null = $state(null);
  let view: 'context' | 'sources' = $state('context');

  onMount(async () => {
    info = await SystemService.Version();
    await clusters.init();
  });
</script>

<div class="shell">
  <Sidebar onManageSources={() => (view = 'sources')} />

  <main class="content">
    {#if view === 'sources'}
      <Sources onClose={() => (view = 'context')} />
    {:else if clusters.active}
      {#key clusters.active.id}
        <ContextDetail context={clusters.active} />
      {/key}
    {:else if clusters.loaded}
      <div class="welcome">
        <h1>Welcome to KubeRoot</h1>
        <p>
          {clusters.contexts.length > 0
            ? 'Select a context on the left to connect.'
            : 'No kubeconfig found. Add a kubeconfig file or folder to get started.'}
        </p>
      </div>
    {/if}
  </main>

  <footer class="status">
    {#if info}
      {info.version} · {info.platform} · {info.goVersion}
    {/if}
  </footer>
</div>

<style>
  .shell {
    display: grid;
    grid-template-columns: 260px 1fr;
    grid-template-rows: 1fr auto;
    height: 100%;
  }
  :global(.sidebar) {
    grid-row: 1 / 3;
  }
  .content {
    min-width: 0;
    overflow-y: auto;
  }
  .welcome {
    padding: 48px 32px;
  }
  .welcome h1 {
    margin: 0 0 8px;
    font-size: 22px;
    font-weight: 600;
  }
  .welcome p { color: var(--muted); }
  .status {
    padding: 6px 16px;
    border-top: 1px solid var(--border);
    color: var(--muted);
    font-size: 12px;
  }
</style>
