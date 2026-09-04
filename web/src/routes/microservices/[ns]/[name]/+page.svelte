<script lang="ts">
  import { page } from "$app/stores";
  import { poll } from "$lib/poll.svelte.js";
  import type { Microservice, PodInfo, Point, ServiceMetricSignal, ServiceMetricsReport, ServiceSeverity } from "$lib/types.js";
  import { fmtCpu, fmtMem } from "$lib/format.js";
  import StatCard from "$lib/components/StatCard.svelte";
  import Pill from "$lib/components/Pill.svelte";
  import TimeChart from "$lib/components/TimeChart.svelte";

  type Resp = { microservice: Microservice; pods: PodInfo[]; history: Point[] };
  const q = poll<Resp>(() => `/api/microservices/${$page.params.ns}/${$page.params.name}`, 5000);
  let metricsWindow = $state("24h");
  const metrics = poll<ServiceMetricsReport>(
    () => `/api/microservices/${$page.params.ns}/${$page.params.name}/metrics?range=${metricsWindow}`,
    60000,
  );
  const windows = ["1h", "6h", "24h", "7d", "21d"];

  const tone = (severity: ServiceSeverity) => ({
    healthy: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300",
    warning: "border-amber-500/30 bg-amber-500/10 text-amber-300",
    critical: "border-rose-500/30 bg-rose-500/10 text-rose-300",
    unknown: "border-slate-500/30 bg-slate-500/10 text-slate-400",
  })[severity];

  // The API already says what each signal is measured in; the UI must not keep
  // a second, divergent opinion keyed off the signal name.
  const isPercent = (signal: ServiceMetricSignal) => signal.unit.trim().startsWith("%");

  function metricValue(signal: ServiceMetricSignal): string {
    if (signal.current == null) return "—";
    if (!isPercent(signal)) return Math.round(signal.current).toString();
    return `${signal.current.toFixed(signal.key === "error_rate" ? 2 : 1)}%`;
  }

  function thresholdLabel(signal: ServiceMetricSignal): string {
    if (signal.warning == null) return `critical ${signal.critical}`;
    return `warning ${signal.warning} · critical ${signal.critical}`;
  }

  const stateNote = (signal: ServiceMetricSignal) =>
    signal.state === "no_data"
      ? "Not measured for this workload."
      : signal.state === "error"
        ? signal.error
        : "";
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

  <div class="mt-8 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h2 class="text-xs font-semibold uppercase tracking-wider text-slate-400">Service health</h2>
      <p class="mt-1 text-xs text-slate-500">Prometheus-backed signals and alert thresholds</p>
    </div>
    <div class="flex rounded-lg border border-white/10 bg-slate-900/60 p-1">
      {#each windows as window}
        <button
          type="button"
          onclick={() => metricsWindow = window}
          class="rounded-md px-2.5 py-1 text-xs transition-colors {metricsWindow === window ? 'bg-indigo-500 text-white' : 'text-slate-400 hover:text-slate-200'}"
        >{window}</button>
      {/each}
    </div>
  </div>

  {#if metrics.data && metrics.data.window !== metricsWindow}
    <p class="mt-4 text-sm text-slate-400">Loading {metricsWindow} service metrics…</p>
  {:else if metrics.error}
    <div class="mt-4 rounded-xl border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">
      Long-range service metrics are unavailable ({metrics.error}). Configure Prometheus on the Observability page.
    </div>
  {:else if !metrics.data}
    <p class="mt-4 text-sm text-slate-400">Loading long-range service metrics…</p>
  {:else}
    <div class="mt-4 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
      {#each metrics.data.signals as signal}
        <section class="rounded-2xl border border-white/5 bg-slate-900/60 p-4">
          <div class="flex items-start justify-between gap-2">
            <div class="text-xs font-medium uppercase tracking-wide text-slate-400">{signal.label}</div>
            <span class={`rounded-full border px-2 py-0.5 text-[10px] uppercase ${tone(signal.severity)}`}>{signal.severity}</span>
          </div>
          <div class="mt-2 text-2xl font-semibold tabular-nums">{metricValue(signal)}</div>
          {#if signal.key === "error_rate" && signal.difference != null}
            <div class="mt-1 text-xs {signal.difference > 0 ? 'text-rose-300' : 'text-emerald-300'}">
              {signal.difference > 0 ? "+" : ""}{signal.difference.toFixed(2)} percentage points vs week before
            </div>
          {:else}
            <div class="mt-1 text-xs text-slate-500">{thresholdLabel(signal)}</div>
          {/if}
          {#if signal.state === "error"}
            <p class="mt-2 text-xs text-amber-300">{signal.error}</p>
          {:else if signal.state === "no_data"}
            <p class="mt-2 text-xs text-slate-500">{stateNote(signal)}</p>
          {/if}
        </section>
      {/each}
    </div>

    {#if !metrics.data.compared}
      <p class="mt-3 text-xs text-slate-500">
        The week-before overlay is only shown up to the 7d window; beyond that it would overlap the current series
        rather than compare against it.
      </p>
    {/if}

    <div class="mt-5 grid gap-5 xl:grid-cols-2">
      {#each metrics.data.signals as signal}
        <section>
          <div class="mb-2 flex items-baseline justify-between gap-3">
            <h3 class="text-sm font-medium text-slate-300">{signal.label}</h3>
            <span class="text-[11px] text-slate-500">{signal.unit}</span>
          </div>
          <TimeChart data={signal.points} height={180} unit={isPercent(signal) ? "%" : ""} lines={signal.lines} />
          <p class="mt-1 text-[11px] leading-5 text-slate-500">{signal.description}</p>
        </section>
      {/each}
    </div>
  {/if}

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
