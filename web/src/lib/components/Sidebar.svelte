<script lang="ts">
  import { page } from "$app/stores";

  const links = [
    { href: "/", label: "Cluster", icon: "M3 12h18M3 6h18M3 18h18" },
    { href: "/nodes", label: "Nodes", icon: "M4 6h16v4H4zM4 14h16v4H4z" },
    { href: "/microservices", label: "Microservices", icon: "M12 2l4 7-4 7-4-7z M12 14l4 7H8z" },
  ];

  const active = (href: string) =>
    href === "/" ? $page.url.pathname === "/" : $page.url.pathname.startsWith(href);
</script>

<aside class="sticky top-0 flex h-screen w-56 shrink-0 flex-col border-r border-white/5 bg-slate-900/50 px-3 py-6">
  <div class="px-3">
    <div class="flex items-center gap-2">
      <div class="grid h-8 w-8 place-items-center rounded-lg bg-gradient-to-br from-indigo-500 to-violet-600 text-sm font-bold">B</div>
      <div>
        <div class="text-lg font-semibold leading-none">Beholdr</div>
        <div class="text-[11px] text-slate-400">k8s observer</div>
      </div>
    </div>
  </div>

  <nav class="mt-8 flex flex-col gap-1">
    {#each links as l}
      <a
        href={l.href}
        class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors
          {active(l.href) ? 'bg-white/5 text-indigo-300' : 'text-slate-300 hover:bg-white/5'}"
      >
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.8">
          <path d={l.icon} stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        {l.label}
      </a>
    {/each}
  </nav>

  <div class="mt-auto px-3 text-[11px] text-slate-500">v0.1.0</div>
</aside>
