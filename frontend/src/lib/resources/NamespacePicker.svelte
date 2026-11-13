<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { ResourceView, ViewPhase, type ResourceType } from './resources.svelte';

  let {
    contextId,
    connected,
    fallback,
    selected,
    disabled = false,
    onchange,
  }: {
    contextId: string;
    connected: boolean;
    /** Offered when namespaces can't be listed: the context's default namespace. */
    fallback: string;
    /** The chosen namespaces; empty means all. */
    selected: string[];
    /** True for cluster-scoped types, which ignore namespaces. */
    disabled?: boolean;
    onchange: (namespaces: string[]) => void;
  } = $props();

  const namespacesType: ResourceType = {
    group: '',
    version: 'v1',
    resource: 'namespaces',
    kind: 'Namespace',
    namespaced: false,
  };

  // Namespaces come from an ordinary live view, so new ones appear on their own.
  const view = untrack(() => new ResourceView({ contextId, type: namespacesType, namespaces: [] }));

  onMount(() => {
    void view.open();
    return () => view.close();
  });

  let wasConnected = untrack(() => connected);
  $effect(() => {
    const now = connected;
    untrack(() => {
      if (now && !wasConnected) void view.reopen();
      wasConnected = now;
    });
  });

  let open = $state(false);
  let query = $state('');
  let added: string[] = $state([]); // names typed by the user
  let root: HTMLElement | undefined = $state();

  const forbidden = $derived(
    view.status.phase === ViewPhase.ViewError && (view.status.forbidden ?? []).includes(''),
  );
  const options = $derived.by(() => {
    const names = new Set(view.rows.map((r) => r.name));
    if (forbidden || (view.status.phase === ViewPhase.ViewError && names.size === 0)) {
      names.add(fallback || 'default');
    }
    for (const ns of [...selected, ...added]) names.add(ns);
    return [...names].sort();
  });
  const shown = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return q ? options.filter((ns) => ns.toLowerCase().includes(q)) : options;
  });
  // Namespace names are DNS labels.
  const typed = $derived(query.trim().toLowerCase());
  const canAdd = $derived(/^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/.test(typed) && !options.includes(typed));

  const label = $derived.by(() => {
    if (disabled) return 'Cluster-scoped';
    if (selected.length === 0) return 'All namespaces';
    if (selected.length <= 2) return selected.join(', ');
    return `${selected.length} namespaces`;
  });

  function toggle(ns: string) {
    const next = selected.includes(ns) ? selected.filter((x) => x !== ns) : [...selected, ns];
    onchange(next.sort());
  }

  function add(ns: string) {
    if (!added.includes(ns)) added = [...added, ns];
    query = '';
    if (!selected.includes(ns)) onchange([...selected, ns].sort());
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      open = false;
    } else if (e.key === 'Enter') {
      if (canAdd) add(typed);
      else if (shown.length === 1) toggle(shown[0]);
    }
  }

  function onpointerdown(e: PointerEvent) {
    if (open && root && !root.contains(e.target as Node)) open = false;
  }
</script>

<svelte:window {onpointerdown} />

<div class="picker" bind:this={root}>
  <button
    class="trigger"
    {disabled}
    title={disabled ? 'This resource type is not namespaced' : selected.join(', ') || 'All namespaces'}
    aria-haspopup="dialog"
    aria-expanded={open}
    onclick={() => (open = !open)}
  >
    <span class="label">{label}</span>
    {#if !disabled}<span class="caret">▾</span>{/if}
  </button>

  {#if open && !disabled}
    <div class="popover" role="dialog" aria-label="Namespaces">
      <!-- svelte-ignore a11y_autofocus -->
      <input
        type="search"
        placeholder={forbidden ? 'Type a namespace' : 'Search or type a namespace'}
        bind:value={query}
        spellcheck="false"
        autofocus
        {onkeydown}
      />
      <label class="opt all">
        <input type="checkbox" checked={selected.length === 0} onchange={() => onchange([])} />
        All namespaces
      </label>
      {#if forbidden}
        <p class="hint">You can't list namespaces on this cluster. Type a name to add it.</p>
      {:else if view.status.phase === ViewPhase.ViewLoading && view.rows.length === 0}
        <p class="hint">Loading namespaces…</p>
      {/if}
      <div class="list">
        {#each shown as ns (ns)}
          <label class="opt">
            <input type="checkbox" checked={selected.includes(ns)} onchange={() => toggle(ns)} />
            <span>{ns}</span>
          </label>
        {/each}
        {#if canAdd}
          <button class="add" onclick={() => add(typed)}>Add “{typed}”</button>
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .picker {
    position: relative;
  }
  .trigger {
    display: flex;
    align-items: center;
    gap: 6px;
    max-width: 240px;
    padding: 5px 10px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
    cursor: pointer;
  }
  .trigger:hover:not(:disabled) { background: var(--hover); }
  .trigger:disabled {
    color: var(--muted);
    cursor: default;
  }
  .label {
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .caret { color: var(--muted); }
  .popover {
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    z-index: 10;
    width: 260px;
    padding: 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    box-shadow: 0 6px 24px rgb(0 0 0 / 0.18);
  }
  input[type='search'] {
    box-sizing: border-box;
    width: 100%;
    margin-bottom: 6px;
    padding: 5px 8px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: var(--text);
    font: inherit;
  }
  .list {
    max-height: 280px;
    overflow: auto;
  }
  .opt {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 6px;
    border-radius: 4px;
    font-size: 13px;
    cursor: pointer;
  }
  .opt span {
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .opt:hover { background: var(--hover); }
  .all {
    margin-bottom: 4px;
    padding-bottom: 8px;
    border-bottom: 1px solid var(--border);
    border-radius: 0;
  }
  .hint {
    margin: 4px 6px 8px;
    color: var(--muted);
    font-size: 12px;
  }
  .add {
    width: 100%;
    padding: 4px 6px;
    border: 0;
    border-radius: 4px;
    background: none;
    color: var(--accent);
    font: inherit;
    font-size: 13px;
    text-align: left;
    cursor: pointer;
  }
  .add:hover { background: var(--hover); }
</style>
