<script lang="ts">
  // The app's only code and text viewer. To switch editors, add a sibling
  // folder implementing CodeEditorProps and change this import.
  import Editor from './codemirror/CodeMirrorEditor.svelte';
  import type { CodeEditorProps } from './types';

  let props: CodeEditorProps = $props();
</script>

<Editor {...props} />
