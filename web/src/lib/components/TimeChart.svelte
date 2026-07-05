<script lang="ts">
  import type { Point } from "$lib/types.js";
  import { fmtTime } from "$lib/format.js";

  type Line = { key: string; label: string; color: string };
  let {
    data = [],
    lines,
    unit = "",
    height = 220,
  }: { data: Point[]; lines: Line[]; unit?: string; height?: number } = $props();

  const W = 820;
  const padL = 40, padR = 12, padT = 12, padB = 22;

  const maxY = $derived.by(() => {
    if (unit === "%") return 100;
    let m = 0;
    for (const p of data) for (const l of lines) m = Math.max(m, p[l.key] ?? 0);
    return m <= 0 ? 1 : m * 1.15;
  });

  const x = (i: number) =>
    padL + (data.length <= 1 ? 0 : (i / (data.length - 1)) * (W - padL - padR));
  const y = (v: number) =>
    padT + (1 - v / maxY) * (height - padT - padB);

  function linePath(key: string): string {
    return data
      .map((p, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(p[key] ?? 0).toFixed(1)}`)
      .join(" ");
  }
  function areaPath(key: string): string {
    if (data.length === 0) return "";
    const top = data
      .map((p, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(p[key] ?? 0).toFixed(1)}`)
      .join(" ");
    return `${top} L${x(data.length - 1).toFixed(1)},${y(0).toFixed(1)} L${x(0).toFixed(1)},${y(0).toFixed(1)} Z`;
  }

  const ticks = $derived([0, 0.25, 0.5, 0.75, 1].map((f) => f * maxY));
  const fmtY = (v: number) => (unit === "%" ? `${Math.round(v)}%` : v >= 1000 ? `${(v / 1000).toFixed(0)}k` : `${Math.round(v)}`);
</script>

<div class="rounded-2xl border border-white/5 bg-slate-900/60 p-4">
  <div class="mb-2 flex flex-wrap gap-4">
    {#each lines as l}
      <div class="flex items-center gap-1.5 text-xs text-slate-300">
        <span class="inline-block h-2.5 w-2.5 rounded-full" style="background:{l.color}"></span>{l.label}
      </div>
    {/each}
  </div>

  {#if data.length === 0}
    <div class="flex items-center justify-center text-sm text-slate-500" style="height:{height}px">
      collecting history…
    </div>
  {:else}
    <svg viewBox="0 0 {W} {height}" class="w-full" style="height:{height}px" preserveAspectRatio="none">
      <defs>
        {#each lines as l}
          <linearGradient id="grad-{l.key}" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color={l.color} stop-opacity="0.35" />
            <stop offset="100%" stop-color={l.color} stop-opacity="0" />
          </linearGradient>
        {/each}
      </defs>

      {#each ticks as t}
        <line x1={padL} x2={W - padR} y1={y(t)} y2={y(t)} stroke="#1e293b" stroke-width="1" />
        <text x="4" y={y(t) + 3} font-size="10" fill="#64748b">{fmtY(t)}</text>
      {/each}

      {#each lines as l}
        <path d={areaPath(l.key)} fill="url(#grad-{l.key})" />
        <path d={linePath(l.key)} fill="none" stroke={l.color} stroke-width="2" vector-effect="non-scaling-stroke" />
      {/each}
    </svg>
    <div class="mt-1 flex justify-between text-[10px] text-slate-500">
      <span>{fmtTime(data[0].t)}</span>
      <span>{fmtTime(data[data.length - 1].t)}</span>
    </div>
  {/if}
</div>
