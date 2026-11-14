<script lang="ts" module>
  type Tab = 'describe' | 'yaml' | 'events';

  // Kept for the app session, so moving between objects stays on one tab.
  let tab: Tab = $state('describe');

  const WIDTH_KEY = 'kuberoot.detailWidth';
  const MIN_WIDTH = 320;

  function savedWidth(): number {
    try {
      const w = Number(localStorage.getItem(WIDTH_KEY));
      if (w >= MIN_WIDTH) return w;
    } catch {
      // Storage unavailable: use the default.
    }
    return 560;
  }
</script>

<script lang="ts">
  import DescribeTab from './detail/DescribeTab.svelte';
  import EventsTab from './detail/EventsTab.svelte';
  import YamlTab from './detail/YamlTab.svelte';
  import type { ObjectRef, ResourceType, Row } from './resources.svelte';

  let {
    contextId,
    type,
    object,
    deleted,
    connected,
    onclose,
  }: {
    contextId: string;
    type: ResourceType;
    /** The selected row: live, or the last known state once deleted. */
    object: Row;
    deleted: boolean;
    connected: boolean;
    onclose: () => void;
  } = $props();

  const tabs: { id: Tab; label: string }[] = [
    { id: 'describe', label: 'Describe' },
    { id: 'yaml', label: 'YAML' },
    { id: 'events', label: 'Events' },
  ];

  const ref: ObjectRef = $derived({
    contextId,
    type,
    namespace: object.namespace ?? '',
    name: object.name,
    uid: object.uid,
  });
  const live = $derived(connected && !deleted);

  let width = $state(savedWidth());
  let panel: HTMLElement;

  function startResize(e: PointerEvent) {
    const handle = e.currentTarget as HTMLElement;
    handle.setPointerCapture(e.pointerId);
    const startX = e.clientX;
    const startWidth = width;
    const max = () => Math.max(MIN_WIDTH, (panel.parentElement?.clientWidth ?? 1200) * 0.75);
    const move = (ev: PointerEvent) => {
      width = Math.round(Math.min(max(), Math.max(MIN_WIDTH, startWidth + startX - ev.clientX)));
    };
    const up = () => {
      handle.removeEventListener('pointermove', move);
      handle.removeEventListener('pointerup', up);
      try {
        localStorage.setItem(WIDTH_KEY, String(width));
      } catch {
        // Not remembered; fine.
      }
    };
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', up);
  }
</script>

<aside class="panel" style:width="{width}px" bind:this={panel} aria-label="{type.kind} {object.name}">
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="resize" onpointerdown={startResize} title="Drag to resize"></div>
  <header>
    <div class="title">
      <span class="kind">{type.kind}</span>
      <h3 title={object.name}>{object.name}</h3>
      {#if object.namespace}<span class="ns" title="Namespace">{object.namespace}</span>{/if}
    </div>
    <button class="close" onclick={onclose} title="Close (Esc)" aria-label="Close details">✕</button>
  </header>
  <div class="tabs" role="tablist">
    {#each tabs as t (t.id)}
      <button role="tab" aria-selected={tab === t.id} class:active={tab === t.id} onclick={() => (tab = t.id)}>
        {t.label}
      </button>
    {/each}
  </div>
  {#if deleted}
    <div class="banner" role="status">This {type.kind.toLowerCase()} was deleted. Showing its last known state.</div>
  {:else if !connected}
    <div class="banner" role="status">Disconnected. Showing the last loaded details.</div>
  {/if}
  <div class="body" role="tabpanel">
    <!-- A new object gets fresh tabs, so nothing from the last one shows. -->
    {#key object.uid}
      {#if tab === 'describe'}
        <DescribeTab {ref} resourceVersion={object.resourceVersion} {live} />
      {:else if tab === 'yaml'}
        <YamlTab {ref} resourceVersion={object.resourceVersion} {live} />
      {:else}
        <EventsTab {ref} resourceVersion={object.resourceVersion} {live} />
      {/if}
    {/key}
  </div>
</aside>

<style>
  .panel {
    position: relative;
    display: flex;
    flex-direction: column;
    flex: none;
    min-width: 320px;
    max-width: 75%;
    min-height: 0;
    border-left: 1px solid var(--border);
    background: var(--surface);
  }
  .resize {
    position: absolute;
    top: 0;
    bottom: 0;
    left: -3px;
    width: 6px;
    cursor: col-resize;
    z-index: 2;
  }
  .resize:hover { background: var(--accent); opacity: 0.4; }
  header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 12px 6px;
  }
  .title {
    display: flex;
    align-items: baseline;
    gap: 8px;
    min-width: 0;
    flex: 1;
  }
  .kind, .ns {
    color: var(--muted);
    font-size: 12px;
    white-space: nowrap;
  }
  .ns::before { content: 'in '; }
  h3 {
    margin: 0;
    font-size: 14px;
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  button {
    border: 0;
    background: none;
    color: var(--muted);
    font: inherit;
    cursor: pointer;
  }
  .close {
    padding: 2px 6px;
    border-radius: 4px;
  }
  .close:hover { background: var(--hover); color: var(--text); }
  .tabs {
    display: flex;
    gap: 4px;
    padding: 0 8px;
    border-bottom: 1px solid var(--border);
  }
  .tabs button {
    padding: 6px 8px;
    border-bottom: 2px solid transparent;
    font-size: 13px;
  }
  .tabs button:hover { color: var(--text); }
  .tabs button.active {
    border-bottom-color: var(--accent);
    color: var(--text);
    font-weight: 600;
  }
  .banner {
    margin: 8px 12px 0;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
  }
  .body {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }
</style>
