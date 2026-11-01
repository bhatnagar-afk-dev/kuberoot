<script lang="ts">
  import { clusters, shortPath, Phase, type Context } from './clusters.svelte';
  import StatusDot from './StatusDot.svelte';

  let { context }: { context: Context } = $props();

  const status = $derived(clusters.status(context.id));
  let showDetail = $state(false);

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

<section class="detail">
  <header>
    <h1>{context.name}</h1>
    <div class="phase">
      <StatusDot phase={status.phase} />
      {phaseLabel[status.phase] ?? status.phase}
    </div>
  </header>

  <div class="actions">
    {#if status.phase === Phase.PhaseConnected}
      <button onclick={() => clusters.disconnect(context.id)}>Disconnect</button>
    {:else if status.phase === Phase.PhaseError}
      <button class="primary" onclick={() => clusters.connect(context.id)}>Retry</button>
      <button onclick={() => clusters.disconnect(context.id)}>Dismiss</button>
    {:else if status.phase === Phase.PhaseDisconnected}
      <button class="primary" onclick={() => clusters.connect(context.id)}>Connect</button>
    {/if}
  </div>

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

  <p class="next">Resource browsing arrives in M2.</p>
</section>

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
  .next {
    margin-top: 32px;
    color: var(--muted);
  }
</style>
