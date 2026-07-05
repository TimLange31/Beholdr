<script lang="ts">
  import { poll } from "$lib/poll.svelte";
  import type { Microservice } from "$lib/types";
  import { fmtCpu, fmtMem } from "$lib/format";
  import Pill from "$lib/components/Pill.svelte";

  type Resp = { updated_at: number; microservices: Microservice[] };
  const q = poll<Resp>("/api/microservices", 5000);
  let filter = $state("");

  const rows = $derived(
    (q.data?.microservices ?? []).filter((m) =>
      (m.name + m.namespace).toLowerCase().includes(filter.toLowerCase())
    )
  );
</script>

<h1 class="text-2xl font-semibold">Microservices</h1>

{#if q.error}
  <p class="mt-2 text-sm text-slate-400">Waiting for collector… ({q.error})</p>
{:else if !q.data}
  <p class="mt-2 text-sm text-slate-400">Loading…</p>
{:else}
  <p class="mt-1 text-xs text-slate-400">{q.data.microservices.length} workloads · scaling, autoscaling and per-service utilization</p>

  <input
    placeholder="filter by name / namespace…"
    bind:value={filter}
    class="mt-4 w-72 rounded-lg border border-white/10 bg-slate-900/60 px-3 py-2 text-sm outline-none focus:border-indigo-500"
  />

  <div class="mt-4 overflow-hidden rounded-2xl border border-white/5">
    <table class="w-full text-sm">
      <thead class="bg-slate-900/60 text-left text-xs uppercase tracking-wide text-slate-400">
        <tr>
          <th class="px-4 py-3">Microservice</th><th class="px-4 py-3">Namespace</th><th class="px-4 py-3">Replicas</th>
          <th class="px-4 py-3">Autoscaling</th><th class="px-4 py-3">CPU</th><th class="px-4 py-3">Memory</th>
          <th class="px-4 py-3">Req util</th><th class="px-4 py-3">Nodes</th><th class="px-4 py-3">Restarts</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-white/5">
        {#each rows as m (m.key)}
          <tr class="bg-slate-900/30 hover:bg-slate-800/40">
            <td class="px-4 py-3">
              <a class="text-indigo-300 hover:underline" href="/microservices/{m.namespace}/{m.name}">{m.name}</a>
              <div class="text-[11px] text-slate-500">{m.kind}</div>
            </td>
            <td class="px-4 py-3 text-slate-400">{m.namespace}</td>
            <td class="px-4 py-3">
              <Pill tone={m.ready_replicas >= m.desired_replicas ? "ok" : "warn"}>{m.ready_replicas}/{m.desired_replicas}</Pill>
            </td>
            <td class="px-4 py-3 text-xs text-slate-400">
              {#if m.hpa}HPA {m.hpa.min}–{m.hpa.max}{m.hpa.target_cpu_pct ? ` @${m.hpa.target_cpu_pct}%` : ""}{:else}—{/if}
            </td>
            <td class="px-4 py-3 tabular-nums">{fmtCpu(m.cpu_used)}</td>
            <td class="px-4 py-3 tabular-nums">{fmtMem(m.mem_used)}</td>
            <td class="px-4 py-3 tabular-nums">{m.cpu_util_pct != null ? `${m.cpu_util_pct}%` : "—"}</td>
            <td class="px-4 py-3 text-slate-400">{m.nodes.length}</td>
            <td class="px-4 py-3">{#if m.restarts > 0}<Pill tone="warn">{m.restarts}</Pill>{:else}{m.restarts}{/if}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
