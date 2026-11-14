<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { Fetcher } from './fetcher.svelte';

  // Loading and error states around a fetched text view. The last loaded text
  // stays visible when a reload fails.
  let { fetcher, what, children }: { fetcher: Fetcher; what: string; children: Snippet } = $props();
</script>

{#if fetcher.error}
  <div class="banner warn" role="status">Couldn't load the {what}: {fetcher.error}</div>
{/if}
{#if fetcher.text !== null}
  <div class="text">{@render children()}</div>
{:else if fetcher.loading}
  <p class="muted">Loading…</p>
{/if}

<style>
  .text {
    flex: 1;
    min-height: 0;
  }
  .banner {
    margin: 8px 12px;
    padding: 8px 12px;
    border: 1px solid var(--warn);
    border-radius: var(--radius);
    background: var(--warn-bg);
    overflow-wrap: anywhere;
  }
  .muted {
    margin: 0;
    padding: 16px 12px;
    color: var(--muted);
  }
</style>
