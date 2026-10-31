<script lang="ts">
  import { onMount } from 'svelte';
  import { SystemService } from '../bindings/github.com/bhatnagar-afk-dev/kuberoot/app';
  import type { Info } from '../bindings/github.com/bhatnagar-afk-dev/kuberoot/internal/version';

  let info: Info | null = $state(null);

  onMount(async () => {
    info = await SystemService.Version();
  });
</script>

<div class="shell">
  <aside class="sidebar">
    <div class="brand">KubeRoot</div>
    <p class="hint">Clusters will appear here (M1).</p>
  </aside>

  <main class="content">
    <h1>Welcome to KubeRoot</h1>
    <p class="muted">An open-source Kubernetes desktop client.</p>
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
    grid-template-columns: 240px 1fr;
    grid-template-rows: 1fr auto;
    height: 100%;
  }
  .sidebar {
    grid-row: 1 / 3;
    background: var(--surface);
    border-right: 1px solid var(--border);
    /* Leave room for the macOS inset title bar. */
    padding: 48px 16px 16px;
    --wails-draggable: drag;
  }
  .brand {
    font-weight: 600;
    font-size: 16px;
    color: var(--accent);
  }
  .hint, .muted {
    color: var(--muted);
  }
  .content {
    padding: 48px 32px;
  }
  h1 {
    margin: 0 0 8px;
    font-size: 22px;
    font-weight: 600;
  }
  .status {
    padding: 6px 16px;
    border-top: 1px solid var(--border);
    color: var(--muted);
    font-size: 12px;
  }
</style>
