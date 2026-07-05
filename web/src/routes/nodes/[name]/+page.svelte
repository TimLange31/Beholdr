<script lang="ts">
  import { page } from "$app/stores";
  import { poll } from "$lib/poll.svelte";
  import type { NodeInfo, Point } from "$lib/types";
  import { fmtCpu, fmtMem } from "$lib/format";
  import StatCard from "$lib/components/StatCard.svelte";
  import UsageBar from "$lib/components/UsageBar.svelte";
  import Pill from "$lib/components/Pill.svelte";
  import TimeChart from "$lib/components/TimeChart.svelte";

  type Resp = { node: NodeInfo; history: Point[] };
  const q = poll<Resp>(() => `/api/nodes/${$page.params.name}`, 5000);
</script>

<a class="text-xs text-indigo-300 hover:underline" href="/nodes">← Nodes</a>

{#if q.error}
  <p class="mt-2 text-sm text-slate-400">Not found ({q.error})</p>
{:else if !q.data}
  <p class="mt-2 text-sm text-slate-400">Loading…</p>
{:else}
  {@const n = q.data.node}
  <h1 class="mt-1 text-2xl font-semibold">{n.name}</h1>
  <p class="mt-1 text-xs text-slate-400">
    {n.roles.join(", ")} · {n.kubelet_version} ·
    <span class="ml-1"><Pill tone={n.ready ? "ok" : "crit"}>{n.ready ? "Ready" : "NotReady"}</Pill></span>
  </p>

  <div class="mt-5 grid grid-cols-2 gap-4 md:grid-cols-4">
    <StatCard label="Pods on node" value={n.pod_count} />
    <StatCard label="Microservices" value={n.workloads.length} />
    <div class="rounded-2xl border border-white/5 bg-slate-900/60 p-5">
      <div class="text-xs font-medium uppercase tracking-wide text-slate-400">CPU</div>
      <div class="mt-1.5 text-3xl font-semibold tabular-nums">{n.cpu_pct}%</div>
      <div class="mt-1 text-xs text-slate-400">{fmtCpu(n.cpu_used)} / {fmtCpu(n.cpu_capacity)}</div>
      <div class="mt-3"><UsageBar pct={n.cpu_pct} /></div>
    </div>
    <div class="rounded-2xl border border-white/5 bg-slate-900/60 p-5">
      <div class="text-xs font-medium uppercase tracking-wide text-slate-400">Memory</div>
      <div class="mt-1.5 text-3xl font-semibold tabular-nums">{n.mem_pct}%</div>
      <div class="mt-1 text-xs text-slate-400">{fmtMem(n.mem_used)} / {fmtMem(n.mem_capacity)}</div>
      <div class="mt-3"><UsageBar pct={n.mem_pct} /></div>
    </div>
  </div>

  <h2 class="mb-3 mt-8 text-xs font-semibold uppercase tracking-wider text-slate-400">Node utilization history</h2>
  <TimeChart
    data={q.data.history}
    unit="%"
    lines={[
      { key: "cpu_pct", label: "CPU %", color: "#818cf8" },
      { key: "mem_pct", label: "Memory %", color: "#10b981" },
    ]}
  />

  <h2 class="mb-3 mt-8 text-xs font-semibold uppercase tracking-wider text-slate-400">Pods on this node</h2>
  <div class="overflow-hidden rounded-2xl border border-white/5">
    <table class="w-full text-sm">
      <thead class="bg-slate-900/60 text-left text-xs uppercase tracking-wide text-slate-400">
        <tr><th class="px-4 py-3">Pod</th><th class="px-4 py-3">Microservice</th><th class="px-4 py-3">Phase</th>
          <th class="px-4 py-3">CPU</th><th class="px-4 py-3">Memory</th><th class="px-4 py-3">Restarts</th></tr>
      </thead>
      <tbody class="divide-y divide-white/5">
        {#each n.pods ?? [] as p (p.namespace + p.name)}
          <tr class="bg-slate-900/30 hover:bg-slate-800/40">
            <td class="px-4 py-3 font-mono text-[12px]">{p.name}</td>
            <td class="px-4 py-3"><a class="text-indigo-300 hover:underline" href="/microservices/{p.namespace}/{p.workload}">{p.workload}</a></td>
            <td class="px-4 py-3"><Pill tone={p.phase === "Running" ? "ok" : "warn"}>{p.phase}</Pill></td>
            <td class="px-4 py-3 tabular-nums">{fmtCpu(p.cpu_used)}</td>
            <td class="px-4 py-3 tabular-nums">{fmtMem(p.mem_used)}</td>
            <td class="px-4 py-3 tabular-nums">{p.restarts}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
