import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// Ядро AI IT-Buddy. В dev и preview /api проксируется на него, CORS не нужен.
// В Docker фронт отдает nginx, он же проксирует /api (см. nginx.conf).
const CORE = process.env.CORE_URL || "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  server: {
    host: "0.0.0.0",
    port: Number(process.env.PORT || 5173),
    strictPort: true,
    proxy: { "/api": CORE, "/readyz": CORE },
  },
  preview: {
    host: "0.0.0.0",
    port: Number(process.env.PORT || 5173),
    proxy: { "/api": CORE, "/readyz": CORE },
  },
});
