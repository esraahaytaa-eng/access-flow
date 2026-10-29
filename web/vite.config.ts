import { defineConfig } from "vitest/config";

export default defineConfig({
  // Dev only: the Go app serves the built files and the API on one origin (A2).
  server: { proxy: { "/api": "http://localhost:8080" } },
  // The tests also run inside the image build, often on a busy machine: generous timeouts keep them deterministic.
  test: { environment: "jsdom", restoreMocks: true, setupFiles: ["src/test-setup.ts"], testTimeout: 60_000 },
});
