<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { ResourceService } from '../../../../bindings/kuberoot.in/kuberoot/app';
  import CodeEditor from '../../editor/CodeEditor.svelte';
  import { Fetcher } from './fetcher.svelte';
  import TextState from './TextState.svelte';
  import type { DetailTabProps } from './types';

  let { ref, resourceVersion, live }: DetailTabProps = $props();

  const isSecret = $derived(ref.type.group === '' && ref.type.resource === 'secrets');
  let managedFields = $state(false);
  let revealSecrets = $state(false); // per object: the tab is re-created for each

  const yaml = new Fetcher();
  const load = () => ResourceService.YAML(ref, { managedFields, revealSecrets });

  $effect(() => {
    void resourceVersion;
    if (live) untrack(() => yaml.refresh(load));
  });
  onDestroy(() => yaml.dispose());

  function toggle(set: () => void) {
    set();
    if (live) yaml.now(load);
  }
</script>

<div class="options">
  <label>
    <input type="checkbox" checked={managedFields} disabled={!live} onchange={() => toggle(() => (managedFields = !managedFields))} />
    Managed fields
  </label>
  {#if isSecret}
    <label title="Secret values are hidden until you reveal them">
      <input type="checkbox" checked={revealSecrets} disabled={!live} onchange={() => toggle(() => (revealSecrets = !revealSecrets))} />
      Reveal values
    </label>
  {/if}
</div>
<TextState fetcher={yaml} what="YAML">
  <CodeEditor value={yaml.text ?? ''} language="yaml" readOnly />
</TextState>

<style>
  .options {
    display: flex;
    gap: 16px;
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    color: var(--muted);
    font-size: 12px;
  }
  label {
    display: flex;
    align-items: center;
    gap: 4px;
  }
</style>
