<script lang="ts">
  import { SvelteSet } from 'svelte/reactivity';
  import { typeKey, type NavItem, type NavState, type Selection } from './navigation.svelte';

  let {
    nav,
    selected,
    onselect,
  }: {
    nav: NavState;
    /** typeKey of the selected type. */
    selected: string;
    onselect: (s: Selection) => void;
  } = $props();

  // Sections start open and folders (CRD API groups) closed, except the one
  // holding the selected type.
  const closedSections = new SvelteSet<string>();
  const openFolders = new SvelteSet<string>();

  function holdsSelected(folder: NavItem): boolean {
    return (folder.items ?? []).some((it) => it.type && typeKey(it.type) === selected);
  }

  function toggle(set: SvelteSet<string>, key: string) {
    if (set.has(key)) set.delete(key);
    else set.add(key);
  }
</script>

{#snippet entry(item: NavItem)}
  {#if item.type}
    {@const type = item.type}
    <button
      class="item"
      class:selected={typeKey(type) === selected}
      title="{item.title} ({type.group || 'core'}/{type.version})"
      onclick={() => onselect({ title: item.title, type })}
    >
      {item.title}
    </button>
  {/if}
{/snippet}

<nav aria-label="Resource types">
  {#if nav.stale}
    <div class="notice" role="status">
      <span>Resource types changed</span>
      <button class="link" disabled={nav.loading} onclick={() => nav.refresh()}>Refresh</button>
    </div>
  {/if}
  {#if nav.error}
    <div class="notice warn" role="status">
      <span>{nav.error}</span>
      <button class="link" disabled={nav.loading} onclick={() => nav.load()}>Retry</button>
    </div>
  {/if}
  {#if nav.failed.length > 0}
    <div class="notice warn" role="status" title={nav.failed.join('\n')}>
      <span>Couldn't load {nav.failed.length === 1 ? nav.failed[0] : `${nav.failed.length} API groups`}</span>
    </div>
  {/if}

  {#if nav.groups.length === 0 && nav.loading}
    <p class="muted">Loading…</p>
  {/if}

  {#each nav.groups as group (group.title)}
    {@const open = !closedSections.has(group.title)}
    <section>
      <button class="section" aria-expanded={open} onclick={() => toggle(closedSections, group.title)}>
        <span class="caret" class:open>▸</span>{group.title}
      </button>
      {#if open}
        {#each group.items as item (item.title)}
          {#if item.type}
            {@render entry(item)}
          {:else}
            {@const folderOpen = openFolders.has(item.title) || holdsSelected(item)}
            <button class="folder" aria-expanded={folderOpen} onclick={() => toggle(openFolders, item.title)}>
              <span class="caret" class:open={folderOpen}>▸</span>{item.title}
            </button>
            {#if folderOpen}
              <div class="nested">
                {#each item.items ?? [] as child (child.title)}
                  {@render entry(child)}
                {/each}
              </div>
            {/if}
          {/if}
        {/each}
      {/if}
    </section>
  {/each}
</nav>

<style>
  nav {
    width: 210px;
    flex: none;
    overflow-y: auto;
    padding: 8px 6px 16px;
    border-right: 1px solid var(--border);
  }
  section { margin-bottom: 4px; }
  button {
    display: flex;
    align-items: center;
    width: 100%;
    padding: 4px 8px;
    border: 0;
    border-radius: 5px;
    background: none;
    color: var(--text);
    font: inherit;
    font-size: 13px;
    text-align: left;
    cursor: pointer;
  }
  button:hover { background: var(--hover); }
  .section {
    color: var(--muted);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .folder {
    padding-left: 18px;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .item {
    display: block;
    padding-left: 22px;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .nested .item { padding-left: 36px; }
  .item.selected {
    background: var(--accent);
    color: #fff;
  }
  .caret {
    display: inline-block;
    width: 12px;
    margin-right: 2px;
    font-size: 10px;
    transition: transform 0.1s;
  }
  .caret.open { transform: rotate(90deg); }
  .notice {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 8px;
    margin: 0 2px 8px;
    padding: 6px 8px;
    border: 1px solid var(--accent);
    border-radius: var(--radius);
    font-size: 12px;
  }
  .notice.warn {
    border-color: var(--warn);
    background: var(--warn-bg);
  }
  .notice span { overflow-wrap: anywhere; }
  .link {
    display: inline;
    width: auto;
    padding: 0;
    color: var(--accent);
    font-size: 12px;
  }
  .link:hover { background: none; text-decoration: underline; }
  .muted {
    margin: 8px;
    color: var(--muted);
    font-size: 12px;
  }
</style>
