import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config.ts";

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: "jsdom",
      setupFiles: ["./tests/setup.ts"],
      include: ["tests/**/*.test.{ts,tsx}"],
      restoreMocks: true,
      coverage: {
        provider: "v8",
        include: ["src/**/*.{ts,tsx}"],
        // точка входа только монтирует App; типы окружения - не код
        exclude: ["src/main.tsx", "src/vite-env.d.ts"],
        reporter: ["text", "html", "lcov", "json-summary"],
        thresholds: { lines: 80 },
      },
    },
  }),
);
