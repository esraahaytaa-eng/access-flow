import { cleanup, configure } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library only cleans up automatically when Vitest globals are on.
afterEach(cleanup);

// findBy* and waitFor give up after 1 s by default; under load a first render can take longer.
configure({ asyncUtilTimeout: 15_000 });
