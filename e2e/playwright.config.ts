import { defineConfig } from "@playwright/test";

// One serial smoke test against the isolated stack ./e2e.sh starts. No traces, screenshots or
// videos: they could hold the activation link or a session cookie. Output stays in the container.
export default defineConfig({
  testDir: ".",
  outputDir: "/tmp/e2e-results",
  timeout: 180_000,
  expect: { timeout: 15_000 },
  retries: 0,
  workers: 1,
  reporter: "list",
  use: {
    baseURL: process.env.BASE_URL ?? "http://localhost:8080",
    actionTimeout: 15_000,
    locale: "tr-TR",
    timezoneId: "Europe/Istanbul",
    trace: "off",
    screenshot: "off",
    video: "off",
  },
});
