//go:build wasm

package main

import (
	"context"
	"net/http"
	"strings"
	"syscall/js"

	"github.com/gstranger/hearsay/internal/d1"
	"github.com/gstranger/hearsay/internal/server"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

var (
	globalProvider hearsay.Provider
	globalServer   *server.Server
)

func init() {
	// Export handleRequest so the JS wrapper (worker.js) can call it.
	js.Global().Set("handleRequest", js.FuncOf(handleRequest))
}

// main is required by the Go compiler but the program runs entirely
// via the request handler exported in init().
func main() {
	select {}
}

// handleRequest is called by the JS worker.js wrapper with:
//
//	handleRequest(request, d1Binding)
//
// args[0]: the Cloudflare Request object
// args[1]: the D1 binding (HEARSAY_D1) passed explicitly so we don't use global scope
func handleRequest(this js.Value, args []js.Value) any {
	request := args[0]
	d1Binding := args[1]
	url := request.Get("url").String()

	// Initialize provider on first request
	if globalProvider == nil {
		if d1Binding.IsUndefined() {
			return newResponse(500, `{"error":"D1 binding not provided"}`)
		}
		globalProvider = d1.New(d1Binding)
		client := hearsay.NewClient(globalProvider, "default")
		globalServer = server.New(client, globalProvider, false, "", server.LogFormatText, 0, 0, hearsay.AuditOff, "default")
	}

	// Extract namespace from URL path. URLs look like:
	//   /ns/acme-project/claim  → namespace="acme-project", path="/claim"
	path := url
	// Strip protocol if present
	path = strings.TrimPrefix(path, "http://")
	path = strings.TrimPrefix(path, "https://")
	// Strip host (everything before the first / after protocol)
	if idx := strings.Index(path, "/"); idx >= 0 {
		path = path[idx:]
	}

	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 4)
	if len(parts) < 3 || parts[0] != "ns" {
		return newResponse(400, `{"error":"invalid path, expected /ns/<namespace>/..."}`)
	}
	namespace := parts[1]
	remainingPath := "/" + strings.Join(parts[2:], "/")

	// Build an http.Request from the JS Request
	method := request.Get("method").String()
	body := ""
	if request.Get("body").Truthy() {
		bodyPromise := request.Call("text")
		if bodyPromise.Truthy() && bodyPromise.Type() == js.TypeString {
			body = bodyPromise.String()
		}
	}

	goReq, err := http.NewRequestWithContext(context.Background(), method, remainingPath, strings.NewReader(body))
	if err != nil {
		return newResponse(400, `{"error":"invalid request"}`)
	}

	// Copy headers
	headers := request.Get("headers")
	if headers.Truthy() {
		headerIter := headers.Call("entries")
		for {
			entry := headerIter.Call("next")
			if entry.Get("done").Bool() {
				break
			}
			kv := entry.Get("value")
			key := kv.Index(0).String()
			value := kv.Index(1).String()
			goReq.Header.Set(key, value)
		}
	}

	// Add namespace as query parameter so handlers can read it via r.URL.Query().Get("namespace")
	goReq.Header.Set("X-Hearsay-Namespace", namespace)
	goReq.URL.RawQuery = "namespace=" + namespace

	// Serve via shared server.Server
	rec := &wasmResponseRecorder{
		headers:    http.Header{},
		statusCode: 200,
	}
	globalServer.ServeHTTP(rec, goReq)

	return newResponse(rec.statusCode, rec.body.String())
}

// wasmResponseRecorder implements http.ResponseWriter for WASM.
type wasmResponseRecorder struct {
	headers    http.Header
	body       strings.Builder
	statusCode int
}

func (w *wasmResponseRecorder) Header() http.Header         { return w.headers }
func (w *wasmResponseRecorder) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *wasmResponseRecorder) WriteHeader(code int)        { w.statusCode = code }

func newResponse(statusCode int, body string) js.Value {
	headers := js.Global().Get("Headers").New()
	headers.Call("set", "Content-Type", "application/json")

	init := js.Global().Get("Object").New()
	init.Set("status", statusCode)
	init.Set("headers", headers)

	return js.Global().Get("Response").New(body, init)
}