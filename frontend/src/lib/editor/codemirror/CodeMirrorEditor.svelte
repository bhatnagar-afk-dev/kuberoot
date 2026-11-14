<script lang="ts">
  import { onMount } from 'svelte';
  import type { CodeEditorProps } from '../types';
  import type { Editor } from './setup';

  let { value, language, readOnly, onChange }: CodeEditorProps = $props();

  let host: HTMLDivElement;
  let editor: Editor | undefined = $state();

  onMount(() => {
    let destroyed = false;
    // Loaded on first use, so CodeMirror isn't in the startup bundle.
    void import('./setup').then(({ createEditor }) => {
      if (destroyed) return;
      editor = createEditor(host, { value, language, readOnly, onChange: (v) => onChange?.(v) });
    });
    return () => {
      destroyed = true;
      editor?.destroy();
    };
  });

  $effect(() => editor?.setValue(value));
  $effect(() => editor?.setLanguage(language));
  $effect(() => editor?.setReadOnly(readOnly));
</script>

<div class="editor" bind:this={host}></div>

<style>
  .editor {
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }
</style>
