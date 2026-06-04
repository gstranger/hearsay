//go:build wasm

package main

import (
	"context"
	"net/http"
	"strings"
	"syscall/js"

	"github.com/gstranger/hearsay/internal/a2a"
	"github.com/gstranger/hearsay/internal/d1"
	"github.com/gstranger/hearsay/internal/managed"
	"github.com/gstranger/hearsay/internal/server"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

var (
	globalProvider hearsay.Provider
	globalServer   *server.Server
	globalA2A      *a2a.Server
)

func init() {
	js.Global().Set("handleRequest", js.FuncOf(handleRequest))
}

func main() {
	select {}
}

func handleRequest(this js.Value, args []js.Value) any {
	// Argument contract (set by worker.js, load-bearing):
	//   args[0] = Request
	//   args[1] = D1 binding (may be undefined)
	//   args[2] = HEARSAY_MANAGED_URL string (may be undefined)
	//   args[3] = HEARSAY_MANAGED_TOKEN string (may be undefined)
	request := args[0]
	d1Binding := args[1]
	managedURL := safeString(args[2])
	managedToken := safeString(args[3])
	url := request.Get("url").String()

	if globalProvider == nil {
		switch pickProviderKind(managedURL, d1Binding.Truthy()) {
		case ProviderManaged:
			globalProvider = managed.New(managedURL, managedToken)
		case ProviderD1:
			globalProvider = d1.New(d1Binding)
		default:
			return newResponse(500, `{"error":"no provider configured: set HEARSAY_D1 binding or HEARSAY_MANAGED_URL"}`)
		}
		client := hearsay.NewClient(globalProvider, "default")
		globalServer = server.New(client, globalProvider, false, "", server.LogFormatText, 0, 0, hearsay.AuditOff, "default")

		// A2A server — uses the same provider for task storage.
		// Auth is optional on Workers (Cloudflare handles edge auth).
		globalA2A = a2a.NewServer(nil, client, globalProvider, nil, "default")
	}

	// Parse URL: /ns/<namespace>/...rest-path...
	path := url
	path = strings.TrimPrefix(path, "http://")
	path = strings.TrimPrefix(path, "https://")
	if idx := strings.Index(path, "/"); idx >= 0 {
		path = path[idx:]
	}

	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 4)
	if len(parts) < 3 || parts[0] != "ns" {
		return newResponse(400, `{"error":"invalid path, expected /ns/<namespace>/..."}`)
	}
	namespace := parts[1]
	remainingPath := "/" + strings.Join(parts[2:], "/")

	// Build http.Request
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

	goReq.Header.Set("X-Hearsay-Namespace", namespace)
	goReq.URL.RawQuery = "namespace=" + namespace

	rec := &wasmResponseRecorder{
		headers:    http.Header{},
		statusCode: 200,
	}

	// Route: A2A paths go to the A2A server, everything else to REST
	isA2A := remainingPath == "/" && method == http.MethodPost ||
		remainingPath == "/.well-known/agent.json" && method == http.MethodGet

	if isA2A {
		globalA2A.ServeHTTP(rec, goReq)
	} else {
		globalServer.ServeHTTP(rec, goReq)
	}

	return newResponse(rec.statusCode, rec.body.String())
}

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

// safeString returns v.String() when v is a populated JS string,
// or "" when v is undefined/null. Avoids a panic when an env var
// was not configured on the Worker.
func safeString(v js.Value) string {
	if v.IsUndefined() || v.IsNull() {
		return ""
	}
	return v.String()
}