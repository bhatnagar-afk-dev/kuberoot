<script lang="ts" module>
  type Tab = 'describe' | 'yaml' | 'events' | 'pods';

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
  import { onDestroy, untrack } from 'svelte';
  import DescribeTab from './detail/DescribeTab.svelte';
  import EventsTab from './detail/EventsTab.svelte';
  import PodsTab from './detail/PodsTab.svelte';
  import YamlTab from './detail/YamlTab.svelte';
  import { PodsView, ViewPhase, podsType, selectsPods, type ObjectRef, type ResourceType, type Row } from './resources.svelte';

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

  const allTabs: { id: Tab; label: string }[] = [
    { id: 'describe', label: 'Describe' },
    { id: 'yaml', label: 'YAML' },
    { id: 'events', label: 'Events' },
    { id: 'pods', label: 'Pods' },
  ];

  function tabsOf(t: ResourceType) {
    // Nothing records events about an event, so an Event has no Events tab.
    const isEvent = t.resource === 'events' && (t.group === '' || t.group === 'events.k8s.io');
    return allTabs.filter((x) => (x.id !== 'events' || !isEvent) && (x.id !== 'pods' || selectsPods(t)));
  }

  function refOf(t: ResourceType, o: Row): ObjectRef {
    return { contextId, type: t, namespace: o.namespace ?? '', name: o.name, uid: o.uid };
  }

  const ref: ObjectRef = $derived(refOf(type, object));

  // The Pods tab's view lives here rather than in the tab, so a pod opened
  // from it stays live and Back returns to the same rows. It opens when the
  // tab is first shown and closes when the panel moves to another object.
  let podsView: PodsView | undefined = $state.raw();
  // A pod opened from the Pods tab, shown instead of the object until Back.
  let pod: Row | undefined = $state.raw();

  let viewFor = untrack(() => object.uid);
  $effect(() => {
    const uid = object.uid;
    untrack(() => {
      if (uid === viewFor) return;
      viewFor = uid;
      pod = undefined;
      podsView?.close();
      podsView = undefined;
    });
  });
  onDestroy(() => podsView?.close());

  // The pod's live row; once it is gone, the last known state stays.
  const livePod = $derived(pod && podsView?.rows.find((r) => r.uid === pod!.uid));
  $effect(() => {
    if (livePod) pod = livePod;
  });

  const shownType = $derived(pod ? podsType : type);
  const shownObject = $derived(pod ?? object);
  const shownDeleted = $derived(pod ? !livePod && podsView?.status.phase !== ViewPhase.ViewLoading : deleted);
  const shownRef = $derived(refOf(shownType, shownObject));
  const tabs = $derived(tabsOf(shownType));
  // Falls back without forgetting the choice, so the next object that has
  // the tab opens on it again (and Back from a pod returns to Pods).
  const shown: Tab = $derived(tabs.some((t) => t.id === tab) ? tab : 'describe');
  const live = $derived(connected && !shownDeleted);

  $effect(() => {
    if (shown !== 'pods' || podsView) return;
    untrack(() => {
      podsView = new PodsView(ref);
      void podsView.open();
    });
  });

  // Views die with the connection they were opened on.
  let wasConnected = untrack(() => connected);
  $effect(() => {
    const now = connected;
    untrack(() => {
      if (now && !wasConnected) void podsView?.reopen();
      wasConnected = now;
    });
  });

  // A Service's selector can change; workload selectors cannot. Only a new
  // version of the same object counts: another object gets a new view.
  let podsFor = untrack(() => ({ uid: object.uid, rv: object.resourceVersion }));
  $effect(() => {
    const now = { uid: object.uid, rv: object.resourceVersion };
    untrack(() => {
      const changed = now.uid === podsFor.uid && now.rv !== podsFor.rv;
      podsFor = now;
      if (changed && type.group === '' && type.resource === 'services' && !deleted) podsView?.reopenSoon();
    });
  });

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

<aside class="panel" style:width="{width}px" bind:this={panel} aria-label="{shownType.kind} {shownObject.name}">
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="resize" onpointerdown={startResize} title="Drag to resize"></div>
  {#if pod}
    <button class="back" onclick={() => (pod = undefined)} title="Back to {type.kind.toLowerCase()} {object.name}">
      ‹ {type.kind} {object.name}
    </button>
  {/if}
  <header>
    <div class="title">
      <span class="kind">{shownType.kind}</span>
      <h3 title={shownObject.name}>{shownObject.name}</h3>
      {#if shownObject.namespace}<span class="ns" title="Namespace">{shownObject.namespace}</span>{/if}
    </div>
    <button class="close" onclick={onclose} title="Close (Esc)" aria-label="Close details">✕</button>
  </header>
  <div class="tabs" role="tablist">
    {#each tabs as t (t.id)}
      <button role="tab" aria-selected={shown === t.id} class:active={shown === t.id} onclick={() => (tab = t.id)}>
        {t.label}
      </button>
    {/each}
  </div>
  {#if shownDeleted}
    <div class="banner" role="status">This {shownType.kind.toLowerCase()} was deleted. Showing its last known state.</div>
  {:else if !connected}
    <div class="banner" role="status">Disconnected. Showing the last loaded details.</div>
  {/if}
  <div class="body" role="tabpanel">
    <!-- A new object gets fresh tabs, so nothing from the last one shows. -->
    {#key shownObject.uid}
      {#if shown === 'describe'}
        <DescribeTab ref={shownRef} resourceVersion={shownObject.resourceVersion} {live} />
      {:else if shown === 'yaml'}
        <YamlTab ref={shownRef} resourceVersion={shownObject.resourceVersion} {live} />
      {:else if shown === 'events'}
        <EventsTab ref={shownRef} resourceVersion={shownObject.resourceVersion} {live} />
      {:else if podsView}
        <PodsTab view={podsView} onopen={(r) => (pod = r)} />
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
  .back {
    align-self: flex-start;
    margin: 8px 8px 0;
    padding: 2px 4px;
    border-radius: 4px;
    font-size: 12px;
  }
  .back:hover { background: var(--hover); color: var(--text); }
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
