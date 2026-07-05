<script lang="ts">
  let { label, value, sub = "" }: { label: string; value: string | number; sub?: string } = $props();
</script>

<div class="rounded-2xl border border-white/5 bg-slate-900/60 p-5">
  <div class="text-xs font-medium uppercase tracking-wide text-slate-400">{label}</div>
  <div class="mt-1.5 text-3xl font-semibold tabular-nums">{value}</div>
  {#if sub}
    <div class="mt-1 text-xs text-slate-400">{sub}</div>
  {/if}
</div>
