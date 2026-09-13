import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "/ui/",
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/auth": "http://127.0.0.1:8080",
      "/v1": "http://127.0.0.1:8080",
    },
  },
  build: {
    outDir: "../internal/controlplane/assets/console",
    emptyOutDir: true,
    target: "es2022",
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (/\/node_modules\/(react|react-dom|scheduler)\//.test(id))
            return "react-runtime";
        },
      },
    },
  },
});
