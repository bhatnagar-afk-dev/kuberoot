<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { clusters, shortPath, Phase, type Context } from './clusters.svelte';
  import StatusDot from './StatusDot.svelte';
  import NamespacePicker from './resources/NamespacePicker.svelte';
  import Nav from './resources/Nav.svelte';
  import ResourceTable from './resources/ResourceTable.svelte';
  import {
    navigation,
    savedNamespaces,
    saveNamespaces,
    select,
    selection,
    typeKey,
    type Selection,
  } from './resources/navigation.svelte';
  import type { ViewSpec } from './resources/resources.svelte';

  let { context }: { context: Context } = $props();

  const status = $derived(clusters.status(context.id));
  const connected = $derived(status.phase === Phase.PhaseConnected);
  let showDetail = $state(false);
  let showInfo = $state(false);

  // Once connected, keep the browser shown (dimmed while disconnected) so the
  // last rows stay visible. The parent re-creates this component per context.
  let browsing = $state(false);
  $effect(() => {
    if (connected) browsing = true;
  });

  const nav = untrack(() => navigation(context.id));
  // Load the tree on connect and again on every reconnect.
  $effect(() => {
    if (connected) untrack(() => void nav.load());
  });

  let current: Selection = $state(untrack(() => selection(context.id)));
  let namespaces: string[] = $state([]);
  let namespacesLoaded = $state(false);
  onMount(() => {
    void savedNamespaces(context.id).then((ns) => {
      namespaces = ns;
      namespacesLoaded = true;
    });
  });

  function choose(s: Selection) {
    current = s;
    select(context.id, s);
  }

  function setNamespaces(ns: string[]) {
    namespaces = ns;
    saveNamespaces(context.id, ns);
  }

  // Cluster-scoped types ignore namespaces, so changing them doesn't reopen
  // the view.
  const spec: ViewSpec = $derived({
    contextId: context.id,
    type: current.type,
    namespaces: current.type.namespaced ? namespaces : [],
  });

  const phaseLabel: Record<string, string> = {
    [Phase.PhaseDisconnected]: 'Disconnected',
    [Phase.PhaseConnecting]: 'Connecting…',
    [Phase.PhaseConnected]: 'Connected',
    [Phase.PhaseError]: 'Error',
  };

  const authLabel: Record<string, string> = {
    token: 'Bearer token',
    cert: 'Client certificate',
    basic: 'Basic auth',
    exec: 'Exec plugin',
    authProvider: 'Auth provider',
    none: 'None',
  };
</script>

{#snippet actions()}
  {#if status.phase === Phase.PhaseConnected}
    <button onclick={() => clusters.disconnect(context.id)}>Disconnect</button>
  {:else if status.phase === Phase.PhaseError}
    <button class="primary" onclick={() => clusters.connect(context.id)}>Retry</button>
    <button onclick={() => clusters.disconnect(context.id)}>Dismiss</button>
  {:else if status.phase === Phase.PhaseDisconnected}
    <button class="primary" onclick={() => clusters.connect(context.id)}>Connect</button>
  {/if}
{/snippet}

{#snippet banner()}
  {#if status.message}
    <div class="banner" class:warn={status.phase === Phase.PhaseError} role="status">
      <p>{status.message}</p>
      {#if status.detail}
        <button class="link" onclick={() => (showDetail = !showDetail)}>
          {showDetail ? 'Hide details' : 'Details'}
        </button>
        {#if showDetail}
          <pre>{status.detail}</pre>
        {/if}
      {/if}
    </div>
  {/if}
{/snippet}

{#snippet info()}
  <dl>
    <dt>Server</dt>
    <dd class="mono">{context.server || '—'}</dd>
    {#if status.serverVersion}
      <dt>Kubernetes</dt>
      <dd>{status.serverVersion}</dd>
    {/if}
    {#if status.identity}
      <dt>Signed in as</dt>
      <dd class="mono">{status.identity}</dd>
    {/if}
    <dt>Cluster</dt>
    <dd>{context.cluster}</dd>
    <dt>User</dt>
    <dd>{context.user}</dd>
    <dt>Namespace</dt>
    <dd>{context.namespace || 'default'}</dd>
    <dt>Auth</dt>
    <dd>
      {authLabel[context.authType] ?? context.authType}
      {#if context.execCmd}<span class="mono">({context.execCmd})</span>{/if}
    </dd>
    <dt>Kubeconfig</dt>
    <dd class="mono" title={context.source}>{shortPath(context.source)}</dd>
  </dl>
{/snippet}

{#if browsing}
  <section class="browser">
    <header class="strip">
      <h1>{context.name}</h1>
      <div class="phase">
        <StatusDot phase={status.phase} />
        {phaseLabel[status.phase] ?? status.phase}
      </div>
      {#if status.serverVersion}<span class="meta">{status.serverVersion}</span>{/if}
      {#if status.identity}<span class="meta mono">{status.identity}</span>{/if}
      <div class="actions">
        <button class="link" onclick={() => (showInfo = !showInfo)}>{showInfo ? 'Hide details' : 'Details'}</button>
        {@render actions()}
      </div>
    </header>
    {#if showInfo || status.message}
      <div class="info">
        {@render banner()}
        {#if showInfo}{@render info()}{/if}
      </div>
    {/if}
    <div class="workspace">
      <Nav {nav} selected={typeKey(current.type)} onselect={choose} />
      {#if namespacesLoaded}
        {#key typeKey(current.type)}
          <ResourceTable {spec} title={current.title} {connected}>
            {#snippet tools()}
              <NamespacePicker
                contextId={context.id}
                {connected}
                fallback={context.namespace}
                selected={namespaces}
                disabled={!current.type.namespaced}
                onchange={setNamespaces}
              />
            {/snippet}
          </ResourceTable>
        {/key}
      {/if}
    </div>
  </section>
{:else}
  <section class="detail">
    <header>
      <h1>{context.name}</h1>
      <div class="phase">
        <StatusDot phase={status.phase} />
        {phaseLabel[status.phase] ?? status.phase}
      </div>
    </header>

    <div class="actions">{@render actions()}</div>
    {@render banner()}
    {@render info()}
  </section>
{/if}

<style>
  .detail {
    max-width: 760px;
    padding: 48px 32px 32px;
  }
  header {
    display: flex;
    align-items: baseline;
    gap: 16px;
  }
  h1 {
    margin: 0;
    font-size: 22px;
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .phase {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--muted);
    white-space: nowrap;
  }
  .actions {
    display: flex;
    gap: 8px;
    margin: 16px 0;
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
  button.primary {
    border-color: var(--accent);
    background: var(--accent);
    color: #fff;
  }
  button.link {
    padding: 0;
    border: 0;
    background: none;
    color: var(--accent);
  }
  .banner {
    margin-bottom: 20px;
    padding: 12px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  .banner.warn {
    border-color: var(--warn);
    background: var(--warn-bg);
  }
  .banner p { margin: 0 0 6px; }
  pre {
    margin: 8px 0 0;
    padding: 10px;
    max-height: 240px;
    overflow: auto;
    border-radius: 6px;
    background: var(--bg);
    font-size: 12px;
    white-space: pre-wrap;
    word-break: break-word;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 8px 20px;
    margin: 0;
  }
  dt { color: var(--muted); }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 13px;
  }
  .browser {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }
  .strip {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 16px 10px;
    border-bottom: 1px solid var(--border);
  }
  .strip h1 { font-size: 16px; }
  .strip .actions {
    margin: 0 0 0 auto;
    align-items: center;
  }
  .strip button { padding: 4px 12px; }
  .meta {
    color: var(--muted);
    font-size: 12px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .workspace {
    display: flex;
    flex: 1;
    min-height: 0;
  }
  .workspace > :global(.table) { min-width: 0; }
  .info {
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .info .banner { margin-bottom: 12px; }
  .info .banner:last-child { margin-bottom: 0; }
</style>
