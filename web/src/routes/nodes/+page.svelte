<script lang="ts">
  import { poll } from "$lib/poll.svelte";
  import type { NodeInfo } from "$lib/types";
  import { fmtMem } from "$lib/format";
  import UsageBar from "$lib/components/UsageBar.svelte";
  import Pill from "$lib/components/Pill.svelte";

  type Resp = { updated_at: number; nodes: NodeInfo[] };
  const q = poll<Resp>("/api/nodes", 5000);
</script>

<h1 class="text-2xl font-semibold">Nodes</h1>

{#if q.error}
  <p class="mt-2 text-sm text-slate-400">Waiting for collector… ({q.error})</p>
{:else if !q.data}
  <p class="mt-2 text-sm text-slate-400">Loading…</p>
{:else}
  <p class="mt-1 text-xs text-slate-400">{q.data.nodes.length} nodes · pods-per-node and per-node utilization</p>

  <div class="mt-5 overflow-hidden rounded-2xl border border-white/5">
    <table class="w-full text-sm">
      <thead class="bg-slate-900/60 text-left text-xs uppercase tracking-wide text-slate-400">
        <tr>
          <th class="px-4 py-3">Node</th><th class="px-4 py-3">Status</th><th class="px-4 py-3">Roles</th>
          <th class="px-4 py-3">Pods</th><th class="px-4 py-3">CPU</th><th class="px-4 py-3">Memory</th>
          <th class="px-4 py-3">Microservices</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-white/5">
        {#each q.data.nodes as n}
          <tr class="bg-slate-900/30 hover:bg-slate-800/40">
            <td class="px-4 py-3">
              <a class="text-indigo-300 hover:underline" href="/nodes/{n.name}">{n.name}</a>
              <div class="font-mono text-[11px] text-slate-500">{n.kubelet_version}</div>
            </td>
            <td class="px-4 py-3"><Pill tone={n.ready ? "ok" : "crit"}>{n.ready ? "Ready" : "NotReady"}</Pill></td>
            <td class="px-4 py-3">{#each n.roles as r}<span class="mr-1"><Pill tone="muted">{r}</Pill></span>{/each}</td>
            <td class="px-4 py-3 tabular-nums">{n.pod_count}</td>
            <td class="min-w-40 px-4 py-3">
              <div class="mb-1 text-xs text-slate-400">{n.cpu_pct}%</div><UsageBar pct={n.cpu_pct} />
            </td>
            <td class="min-w-40 px-4 py-3">
              <div class="mb-1 text-xs text-slate-400">{n.mem_pct}% · {fmtMem(n.mem_used)}</div><UsageBar pct={n.mem_pct} />
            </td>
            <td class="px-4 py-3 text-xs text-slate-400">
              {n.workloads.length} · {n.workloads.slice(0, 3).join(", ")}{n.workloads.length > 3 ? "…" : ""}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
