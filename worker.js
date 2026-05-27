// Side-effect import: sets globalThis.Go
import './wasm_exec.js';

// Wrangler handles .wasm imports as WebAssembly.Module
import hearsayModule from './hearsay.wasm';

const go = new Go();

// Go's wasm_exec.js sets up the Go runtime (Go is now available globally).
// We schedule the Go runtime in a microtask so the module can finish
// evaluating and export the fetch handler before go.run() blocks.
// On cold start, requests may get 503 while Go boots — the WASM
// scheduler yields to the JS event loop so fetch events still work.
queueMicrotask(() => {
  const instance = new WebAssembly.Instance(hearsayModule, go.importObject);
  go.run(instance);
});

export default {
  async fetch(request, env) {
    if (!globalThis.handleRequest) {
      return new Response(
        JSON.stringify({ error: "hearsay is starting up" }),
        {
          status: 503,
          headers: {
            "Content-Type": "application/json",
            "Retry-After": "2",
          },
        },
      );
    }
    return globalThis.handleRequest(request, env.HEARSAY_D1);
  },
};