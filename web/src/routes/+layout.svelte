<script lang="ts">
  import "../app.css";
  import Sidebar from "$lib/components/Sidebar.svelte";
  import HealthBanner from "$lib/components/HealthBanner.svelte";
  let { children } = $props();
</script>

<div class="flex min-h-screen">
  <Sidebar />
  <main class="mx-auto w-full max-w-[1400px] flex-1 px-8 py-7">
    <HealthBanner />
    {@render children()}
  </main>
</div>
