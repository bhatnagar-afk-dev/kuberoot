<script lang="ts">
  import { Phase } from './clusters.svelte';

  let { phase }: { phase: Phase } = $props();
</script>

<span class="dot {phase}" title={phase} aria-label={phase}></span>

<style>
  .dot {
    display: inline-block;
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--dot-off);
  }
  .connected { background: var(--ok); }
  .error { background: var(--warn); }
  .connecting {
    background: var(--accent);
    animation: pulse 1s ease-in-out infinite;
  }
  @keyframes pulse {
    50% { opacity: 0.3; }
  }
  @media (prefers-reduced-motion: reduce) {
    .connecting { animation: none; }
  }
</style>
