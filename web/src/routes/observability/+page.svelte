<script lang="ts">
  import { poll } from "$lib/poll.svelte.js";
  import type { IntegrationProvider, IntegrationStatus } from "$lib/types.js";
  import { fmtTime } from "$lib/format.js";

  const q = poll<IntegrationStatus>("/api/integrations", 10000);

  const title = (name: string) => ({
    prometheus: "Prometheus",
    elasticsearch: "Elasticsearch",
    "otel-collector": "OpenTelemetry Collector",
  })[name] ?? name;

  const state = (provider: IntegrationProvider) => {
    if (!provider.configured) return { label: "Not configured", style: "text-slate-400 bg-slate-500/10 border-slate-500/30" };
    if (provider.reachable) return { label: "Connected", style: "text-emerald-300 bg-emerald-500/10 border-emerald-500/30" };
    return { label: "Unavailable", style: "text-rose-300 bg-rose-500/10 border-rose-500/30" };
  };
</script>

<h1 class="text-2xl font-semibold">Observability</h1>
<p class="mt-1 max-w-3xl text-sm text-slate-400">
  Beholdr connects the telemetry systems; it does not duplicate their storage. Metrics remain in Prometheus,
  logs and traces remain in Elasticsearch, and application telemetry enters through OpenTelemetry.
</p>

{#if q.error}
  <p class="mt-5 text-sm text-rose-300">Could not load integration status: {q.error}</p>
{:else if !q.data}
  <p class="mt-5 text-sm text-slate-400">Loading integrations…</p>
{:else}
  <p class="mt-3 text-xs text-slate-500">
    {q.data.updated_at ? `Checked ${fmtTime(q.data.updated_at)}` : "Waiting for the first connectivity check"}
  </p>

  <div class="mt-5 grid gap-4 lg:grid-cols-3">
    {#each q.data.providers as provider}
      {@const providerState = state(provider)}
      <section class="rounded-2xl border border-white/5 bg-slate-900/60 p-5">
        <div class="flex items-start justify-between gap-3">
          <div>
            <h2 class="font-semibold">{title(provider.name)}</h2>
            <p class="mt-1 text-xs uppercase tracking-wide text-slate-500">{provider.signal}</p>
          </div>
          <span class={`rounded-full border px-2.5 py-1 text-xs ${providerState.style}`}>
            {providerState.label}
          </span>
        </div>

        {#if provider.reachable}
          <p class="mt-5 text-sm text-slate-300">Health check completed in {provider.latency_ms} ms.</p>
        {:else if provider.error}
          <p class="mt-5 text-sm text-rose-300">{provider.error}</p>
        {:else}
          <p class="mt-5 text-sm text-slate-400">Add the endpoint through Beholdr's deployment configuration.</p>
        {/if}
      </section>
    {/each}
  </div>
{/if}

<section class="mt-8 rounded-2xl border border-white/5 bg-slate-900/40 p-5">
  <h2 class="font-semibold">Rollout boundary</h2>
  <p class="mt-2 max-w-4xl text-sm leading-6 text-slate-400">
    This release only discovers and verifies the shared telemetry backends. It does not annotate namespaces,
    inject agents, restart workloads, or modify application resources. Workload onboarding will remain an
    explicit, auditable GitOps action when that rollout is enabled later.
  </p>
</section>
