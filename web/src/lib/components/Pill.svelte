<script lang="ts">
  let { tone = "muted", children }: { tone?: "ok" | "warn" | "crit" | "muted"; children: any } = $props();
  const tones: Record<string, string> = {
    ok: "bg-emerald-500/15 text-emerald-400",
    warn: "bg-amber-500/15 text-amber-400",
    crit: "bg-rose-500/15 text-rose-400",
    muted: "bg-slate-700/40 text-slate-300",
  };
</script>

<span class="inline-block rounded-full px-2.5 py-0.5 text-xs font-semibold {tones[tone]}">
  {@render children()}
</span>
