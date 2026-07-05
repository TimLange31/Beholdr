// Reactive polling helper built on Svelte 5 runes. Call inside a component's
// init; it refetches `url` every `intervalMs` and exposes reactive getters.
export function poll<T>(url: string | (() => string), intervalMs = 5000) {
  let data = $state<T | null>(null);
  let error = $state<string | null>(null);
  let loading = $state(true);

  const resolve = () => (typeof url === "function" ? url() : url);

  $effect(() => {
    let alive = true;
    const tick = async () => {
      try {
        const r = await fetch(resolve());
        if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
        const j = (await r.json()) as T;
        if (alive) { data = j; error = null; }
      } catch (e) {
        if (alive) error = e instanceof Error ? e.message : String(e);
      } finally {
        if (alive) loading = false;
      }
    };
    tick();
    const id = setInterval(tick, intervalMs);
    return () => { alive = false; clearInterval(id); };
  });

  return {
    get data() { return data; },
    get error() { return error; },
    get loading() { return loading; },
  };
}
