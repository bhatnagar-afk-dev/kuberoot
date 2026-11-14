<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { ResourceService } from '../../../../bindings/kuberoot.in/kuberoot/app';
  import CodeEditor from '../../editor/CodeEditor.svelte';
  import { Fetcher } from './fetcher.svelte';
  import TextState from './TextState.svelte';
  import type { DetailTabProps } from './types';

  let { ref, resourceVersion, live }: DetailTabProps = $props();

  const describe = new Fetcher();
  $effect(() => {
    void resourceVersion;
    if (live) untrack(() => describe.refresh(() => ResourceService.Describe(ref)));
  });
  onDestroy(() => describe.dispose());
</script>

<TextState fetcher={describe} what="description">
  <CodeEditor value={describe.text ?? ''} language="text" readOnly />
</TextState>
