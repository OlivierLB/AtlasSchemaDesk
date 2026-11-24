import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe("browser session ownership", () => {
  it("does not reuse a possibly cloned session when browser ownership locks are unavailable", async () => {
    const memory = new Map([
      ["sulcata-session", "possibly-owned-by-another-tab"],
    ]);
    vi.stubGlobal("sessionStorage", {
      getItem: (key: string) => memory.get(key),
      setItem: (key: string, value: string) => memory.set(key, value),
    });
    vi.stubGlobal("navigator", {});
    vi.stubGlobal(
      "BroadcastChannel",
      class {
        onmessage: unknown;
        postMessage() {
          /* A suspended owner may not respond. */
        }
        close() {
          /* No resources in the test. */
        }
      },
    );
    const fetch = vi.fn<typeof globalThis.fetch>(
      async () =>
        new Response(JSON.stringify({ id: "fresh-isolated-session" }), {
          status: 201,
        }),
    );
    vi.stubGlobal("fetch", fetch);
    const { initializeSession } = await import("./api");
    expect(await initializeSession()).toBe("fresh-isolated-session");
    expect(fetch).toHaveBeenCalledOnce();
    expect(fetch.mock.calls[0][0]).toBe("/api/sessions");
    expect(memory.get("sulcata-session")).toBe("fresh-isolated-session");
  });
});
