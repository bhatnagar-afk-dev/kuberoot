<script lang="ts">
  import { clusters, shortPath } from './clusters.svelte';

  let { onClose }: { onClose: () => void } = $props();

  // Default sources that don't exist on this device are noise; hide them.
  const visible = $derived(
    clusters.sources.filter((s) => s.origin === 'user' || s.hidden || !s.missing || s.error),
  );

  const originLabel: Record<string, string> = {
    env: '$KUBECONFIG',
    default: 'Auto-discovered',
    user: 'Added by you',
  };
</script>

<section class="sources">
  <header>
    <h1>Kubeconfig sources</h1>
    <button onclick={onClose}>Done</button>
  </header>
  <p class="hint">
    KubeRoot reads these files and folders (including immediate subfolders) and never modifies them.
  </p>

  <ul>
    {#each visible as s (s.path)}
      <li class:hidden={s.hidden}>
        <div class="main">
          <div class="path mono" title={s.path}>{shortPath(s.path)}</div>
          <div class="meta">
            {originLabel[s.origin] ?? s.origin} · {s.kind}
            {#if s.hidden}
              · hidden
            {:else if s.missing}
              · not found
            {:else}
              · {s.contexts} context{s.contexts === 1 ? '' : 's'}
            {/if}
          </div>
          {#if s.error}<div class="error">{s.error}</div>{/if}
        </div>
        {#if s.hidden}
          <button onclick={() => clusters.showSource(s.path)}>Show</button>
        {:else if s.origin === 'user'}
          <button onclick={() => clusters.removeSource(s.path)}>Remove</button>
        {:else}
          <button onclick={() => clusters.removeSource(s.path)}>Hide</button>
        {/if}
      </li>
    {/each}
  </ul>

  <div class="add">
    <button onclick={() => clusters.pickSource(false)}>Add file…</button>
    <button onclick={() => clusters.pickSource(true)}>Add folder…</button>
  </div>
  {#if clusters.error}<p class="error" role="alert">{clusters.error}</p>{/if}
</section>

<style>
  .sources {
    max-width: 760px;
    padding: 48px 32px 32px;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  h1 {
    margin: 0;
    font-size: 22px;
    font-weight: 600;
  }
  .hint { color: var(--muted); }
  ul {
    margin: 16px 0;
    padding: 0;
    list-style: none;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  li {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 14px;
  }
  li + li { border-top: 1px solid var(--border); }
  li.hidden .path { color: var(--muted); }
  .main {
    flex: 1;
    min-width: 0;
  }
  .path {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .meta {
    font-size: 12px;
    color: var(--muted);
  }
  .error {
    margin-top: 4px;
    font-size: 12px;
    color: var(--warn);
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 13px;
  }
  .add {
    display: flex;
    gap: 8px;
  }
  button {
    padding: 6px 14px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
    cursor: pointer;
  }
  button:hover { background: var(--hover); }
</style>
