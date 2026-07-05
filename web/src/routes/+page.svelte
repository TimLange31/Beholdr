<script lang="ts">
  import { poll } from "$lib/poll.svelte.js";
  import type { Cluster, Point } from "$lib/types.js";
  import { fmtCpu, fmtMem, fmtTime } from "$lib/format.js";
  import StatCard from "$lib/components/StatCard.svelte";
  import UsageBar from "$lib/components/UsageBar.svelte";
  import TimeChart from "$lib/components/TimeChart.svelte";

  type Resp = { updated_at: number; metrics_available: boolean; cluster: Cluster; history: Point[] };
  const q = poll<Resp>("/api/cluster", 5000);
</script>

<h1 class="text-2xl font-semibold">Cluster overview</h1>

{#if q.error}
  <p class="mt-2 text-sm text-slate-400">Waiting for collector… ({q.error})</p>
{:else if !q.data}
  <p class="mt-2 text-sm text-slate-400">Loading…</p>
{:else}
  {@const c = q.data.cluster}
  <p class="mt-1 text-xs text-slate-400">Updated {fmtTime(q.data.updated_at)}</p>

  {#if !q.data.metrics_available}
    <div class="mt-4 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-2.5 text-sm text-amber-300">
      metrics-server not detected — CPU/memory usage reads 0. Install metrics-server for live utilization.
    </div>
  {/if}

  <div class="mt-5 grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-5">
    <StatCard label="Nodes" value={`${c.nodes_ready}/${c.nodes_total}`} sub="ready / total" />
    <StatCard label="Microservices" value={c.microservices_total} />
    <StatCard
      label="Pods"
      value={c.pods_total}
      sub={Object.entries(c.pods_by_phase).map(([k, v]) => `${v} ${k}`).join(" · ")}
    />
    <div class="rounded-2xl border border-white/5 bg-slate-900/60 p-5">
      <div class="text-xs font-medium uppercase tracking-wide text-slate-400">CPU</div>
      <div class="mt-1.5 text-3xl font-semibold tabular-nums">{c.cpu_pct}%</div>
      <div class="mt-1 text-xs text-slate-400">{fmtCpu(c.cpu_used)} / {fmtCpu(c.cpu_capacity)}</div>
      <div class="mt-3"><UsageBar pct={c.cpu_pct} /></div>
    </div>
    <div class="rounded-2xl border border-white/5 bg-slate-900/60 p-5">
      <div class="text-xs font-medium uppercase tracking-wide text-slate-400">Memory</div>
      <div class="mt-1.5 text-3xl font-semibold tabular-nums">{c.mem_pct}%</div>
      <div class="mt-1 text-xs text-slate-400">{fmtMem(c.mem_used)} / {fmtMem(c.mem_capacity)}</div>
      <div class="mt-3"><UsageBar pct={c.mem_pct} /></div>
    </div>
  </div>

  <h2 class="mb-3 mt-8 text-xs font-semibold uppercase tracking-wider text-slate-400">Utilization history</h2>
  <TimeChart
    data={q.data.history}
    unit="%"
    lines={[
      { key: "cpu_pct", label: "CPU %", color: "#818cf8" },
      { key: "mem_pct", label: "Memory %", color: "#10b981" },
    ]}
  />

  <h2 class="mb-3 mt-8 text-xs font-semibold uppercase tracking-wider text-slate-400">Running pods</h2>
  <TimeChart
    data={q.data.history}
    height={160}
    lines={[{ key: "pods_running", label: "Running pods", color: "#f59e0b" }]}
  />
{/if}
