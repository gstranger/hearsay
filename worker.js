import './wasm_exec.js';
import hearsayModule from './hearsay.wasm';

// Go's WASM runtime (Go 1.24+) yields back to JS during select{} in main(),
// so go.run() returns control after init() exports globalThis.handleRequest.
// The module can finish evaluating while Go is running.
const go = new Go();
const instance = new WebAssembly.Instance(hearsayModule, go.importObject);
go.run(instance);

export default {
  fetch(request, env) {
    if (!globalThis.handleRequest) {
      return new Response(
        JSON.stringify({ error: 'hearsay is starting up' }),
        { status: 503, headers: { 'Content-Type': 'application/json', 'Retry-After': '2' } },
      );
    }
    return globalThis.handleRequest(
      request,
      env.HEARSAY_D1,
      env.HEARSAY_MANAGED_URL,
      env.HEARSAY_MANAGED_TOKEN,
    );
  },
};