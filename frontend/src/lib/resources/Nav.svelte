<script lang="ts">
  import { tick, untrack } from 'svelte';
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

  // Sections and folders (CRD API groups) start closed, so every section
  // heading fits on screen. The ones holding the selected type are opened
  // when the tree loads; after that the user decides.
  const openSections = new SvelteSet<string>();
  const openFolders = new SvelteSet<string>();

  function isSelected(it: NavItem): boolean {
    return !!it.type && typeKey(it.type) === selected;
  }

  $effect(() => {
    const groups = nav.groups;
    void selected;
    untrack(() => {
      for (const g of groups) {
        for (const it of g.items ?? []) {
          if (isSelected(it)) openSections.add(g.title);
          if ((it.items ?? []).some(isSelected)) {
            openSections.add(g.title);
            openFolders.add(it.title);
          }
        }
      }
    });
  });

  // Show a fade at the bottom while there is more to scroll to.
  let scroller: HTMLElement | undefined = $state();
  let clientHeight = $state(0);
  let more = $state(false);

  function measure() {
    if (scroller) more = scroller.scrollTop + scroller.clientHeight < scroller.scrollHeight - 1;
  }

  $effect(() => {
    // Re-measure when the size or the content changes.
    void clientHeight;
    void nav.groups;
    void nav.stale;
    void nav.error;
    void nav.failed;
    void openSections.size;
    void openFolders.size;
    void tick().then(measure);
  });

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

<div class="wrap">
  <header>
    <span>Resources</span>
    <button
      class="refresh"
      class:spinning={nav.loading}
      disabled={nav.loading}
      title="Reload resource types from the cluster"
      aria-label="Reload resource types"
      onclick={() => nav.refresh()}>↻</button
    >
  </header>
  <nav aria-label="Resource types" bind:this={scroller} bind:clientHeight onscroll={measure}>
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
        <button class="link" disabled={nav.loading} onclick={() => nav.refresh()}>Retry</button>
      </div>
    {/if}

    {#if nav.groups.length === 0 && nav.loading}
      <p class="muted">Loading…</p>
    {/if}

    {#each nav.groups as group (group.title)}
      {@const open = openSections.has(group.title)}
      <section>
        <button class="section" aria-expanded={open} onclick={() => toggle(openSections, group.title)}>
          <span class="caret" class:open>▸</span>{group.title}
        </button>
        {#if open}
          {#each group.items as item (item.title)}
            {#if item.type}
              {@render entry(item)}
            {:else}
              {@const folderOpen = openFolders.has(item.title)}
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
  {#if more}<div class="fade" aria-hidden="true"></div>{/if}
</div>

<style>
  .wrap {
    position: relative;
    display: flex;
    flex-direction: column;
    width: 210px;
    flex: none;
    min-height: 0;
    border-right: 1px solid var(--border);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 8px 4px 14px;
    color: var(--muted);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .refresh {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    padding: 0;
    color: var(--muted);
    font-size: 15px;
  }
  .refresh:hover:not(:disabled) { color: var(--text); }
  .refresh:disabled { cursor: default; }
  .refresh.spinning { animation: spin 0.8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  nav {
    flex: 1;
    min-height: 0;
    min-width: 0;
    overflow-y: auto;
    padding: 4px 6px 16px;
  }
  .fade {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 32px;
    background: linear-gradient(to bottom, transparent, var(--bg));
    pointer-events: none;
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
