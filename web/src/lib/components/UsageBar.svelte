<script lang="ts">
  import { usageColor } from "$lib/format.js";
  let { pct, label = "" }: { pct: number; label?: string } = $props();
</script>

<div>
  {#if label}
    <div class="mb-1 text-xs text-slate-400">{label} — {pct}%</div>
  {/if}
  <div class="h-2 w-full overflow-hidden rounded-full bg-slate-800">
    <div
      class="h-full rounded-full transition-all duration-500"
      style="width: {Math.min(pct, 100)}%; background: {usageColor(pct)};"
    ></div>
  </div>
</div>
