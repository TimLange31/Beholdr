<script lang="ts">
  import { page } from "$app/stores";
  import { poll } from "$lib/poll.svelte.js";
  import type { Microservice, PodInfo, Point } from "$lib/types.js";
  import { fmtCpu, fmtMem } from "$lib/format.js";
  import StatCard from "$lib/components/StatCard.svelte";
  import Pill from "$lib/components/Pill.svelte";
  import TimeChart from "$lib/components/TimeChart.svelte";

  type Resp = { microservice: Microservice; pods: PodInfo[]; history: Point[] };
  const q = poll<Resp>(() => `/api/microservices/${$page.params.ns}/${$page.params.name}`, 5000);
</script>

<a class="text-xs text-indigo-300 hover:underline" href="/microservices">← Microservices</a>

{#if q.error}
  <p class="mt-2 text-sm text-slate-400">Not found ({q.error})</p>
{:else if !q.data}
  <p class="mt-2 text-sm text-slate-400">Loading…</p>
{:else}
  {@const m = q.data.microservice}
  <h1 class="mt-1 text-2xl font-semibold">{m.name}</h1>
  <p class="mt-1 text-xs text-slate-400">{m.namespace} · {m.kind}</p>

  <div class="mt-5 grid grid-cols-2 gap-4 md:grid-cols-4 xl:grid-cols-5">
    <div class="rounded-2xl border border-white/5 bg-slate-900/60 p-5">
      <div class="text-xs font-medium uppercase tracking-wide text-slate-400">Replicas</div>
      <div class="mt-1.5 text-3xl font-semibold tabular-nums">{m.ready_replicas}/{m.desired_replicas}</div>
      <div class="mt-1 text-xs text-slate-400">{m.running_pods} running pods</div>
    </div>
    <StatCard label="CPU (sum)" value={fmtCpu(m.cpu_used)} sub={m.cpu_util_pct != null ? `${m.cpu_util_pct}% of requests` : ""} />
    <StatCard label="Memory (sum)" value={fmtMem(m.mem_used)} />
    <StatCard label="Spread" value={`${m.nodes.length} nodes`} />
    {#if m.hpa}
      <div class="rounded-2xl border border-white/5 bg-slate-900/60 p-5">
        <div class="text-xs font-medium uppercase tracking-wide text-slate-400">Autoscaler</div>
        <div class="mt-1.5 text-3xl font-semibold tabular-nums">{m.hpa.current}</div>
        <div class="mt-1 text-xs text-slate-400">
          range {m.hpa.min}–{m.hpa.max} · desired {m.hpa.desired}
          {#if m.hpa.target_cpu_pct != null}· CPU {m.hpa.current_cpu_pct ?? "?"}%/{m.hpa.target_cpu_pct}%{/if}
        </div>
      </div>
    {/if}
  </div>

  <h2 class="mb-3 mt-8 text-xs font-semibold uppercase tracking-wider text-slate-400">Scaling history</h2>
  <TimeChart
    data={q.data.history}
    height={180}
    lines={[
      { key: "replicas_desired", label: "Desired replicas", color: "#94a3b8" },
      { key: "replicas_ready", label: "Ready replicas", color: "#10b981" },
    ]}
  />

  <h2 class="mb-3 mt-8 text-xs font-semibold uppercase tracking-wider text-slate-400">CPU history</h2>
  <TimeChart data={q.data.history} height={180} lines={[{ key: "cpu_used", label: "CPU (m)", color: "#818cf8" }]} />

  <h2 class="mb-3 mt-8 text-xs font-semibold uppercase tracking-wider text-slate-400">Pods ({q.data.pods.length})</h2>
  <div class="overflow-hidden rounded-2xl border border-white/5">
    <table class="w-full text-sm">
      <thead class="bg-slate-900/60 text-left text-xs uppercase tracking-wide text-slate-400">
        <tr><th class="px-4 py-3">Pod</th><th class="px-4 py-3">Node</th><th class="px-4 py-3">Phase</th>
          <th class="px-4 py-3">CPU</th><th class="px-4 py-3">Memory</th><th class="px-4 py-3">Restarts</th></tr>
      </thead>
      <tbody class="divide-y divide-white/5">
        {#each q.data.pods as p (p.name)}
          <tr class="bg-slate-900/30 hover:bg-slate-800/40">
            <td class="px-4 py-3 font-mono text-[12px]">{p.name}</td>
            <td class="px-4 py-3"><a class="text-indigo-300 hover:underline" href="/nodes/{p.node}">{p.node}</a></td>
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
