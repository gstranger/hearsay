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
	js.Global().Set("fetch", js.FuncOf(handleFetch))
}

// main is required by the Go compiler but the program runs entirely
// via the fetch callback registered in init().
func main() {
	select {}
}

func handleFetch(this js.Value, args []js.Value) any {
	request := args[0]
	url := request.Get("url").String()

	// Initialize provider on first request
	if globalProvider == nil {
		d1Binding := js.Global().Get("HEARSAY_D1")
		if d1Binding.IsUndefined() {
			return newResponse(500, `{"error":"HEARSAY_D1 binding not found"}`)
		}
		globalProvider = d1.New(d1Binding)
		client := hearsay.NewClient(globalProvider, "default")
		globalServer = server.New(client, globalProvider, false, "", server.LogFormatText, 0, 0, hearsay.AuditOff, "default")
	}

	// Extract namespace from URL path. URLs look like:
	//   /ns/acme-project/claim  → namespace="acme-project", path="/claim"
	path := strings.TrimPrefix(url, "http://localhost")
	path = strings.TrimPrefix(path, "https://localhost")
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
		// In WASM, we can't await promises directly. For v1, assume small bodies
		// where body.text() resolves synchronously via the Cloudflare runtime.
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