import { sveltekit } from "@sveltejs/kit/vite";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [tailwindcss(), sveltekit()],
  server: {
    // Proxy API calls to the Go backend during local dev.
    proxy: {
      "/api": "http://localhost:8000",
    },
  },
});
