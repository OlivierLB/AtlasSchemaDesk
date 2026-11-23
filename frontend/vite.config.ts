import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  publicDir: false,
  build: { outDir: "../web/dist/app", emptyOutDir: true },
  server: {
    proxy: Object.fromEntries(
      ["/api", "/data"].map((path) => [
        path,
        {
          target: "http://127.0.0.1:8765",
          changeOrigin: true,
          configure(proxy) {
            proxy.on("proxyReq", (request) => {
              request.removeHeader("origin");
            });
          },
        },
      ]),
    ),
  },
});
