<script lang="ts">
  // Global, always-polling collector status. Independent of any one page's
  // data fetch: a data endpoint can keep serving the last known-good
  // snapshot (200 OK) even while every subsequent collection is failing, so
  // per-page fetch errors alone can't be trusted to surface this.
  import { poll } from "$lib/poll.svelte.js";
  import type { Health } from "$lib/types.js";
  import { fmtTime } from "$lib/format.js";

  const q = poll<Health>("/api/health", 5000);
</script>

{#if q.data && !q.data.ready}
  <div class="mb-5 rounded-lg border border-rose-500/40 bg-rose-500/10 px-4 py-2.5 text-sm text-rose-300">
    <div class="font-medium">
      {#if q.data.last_success}
        Collector data is stale — last successful collection {fmtTime(q.data.last_success)}.
      {:else}
        Waiting for the first successful collection…
      {/if}
    </div>
    {#if q.data.last_error}
      <div class="mt-0.5 text-rose-400/80">{q.data.last_error}</div>
    {/if}
  </div>
{/if}
