//go:build wasm

package main

import (
	"context"
	"net/http"
	"strings"
	"syscall/js"

	"github.com/gstranger/hearsay/internal/a2a"
	"github.com/gstranger/hearsay/internal/d1"
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
	request := args[0]
	d1Binding := args[1]
	url := request.Get("url").String()

	if globalProvider == nil {
		if d1Binding.IsUndefined() {
			return newResponse(500, `{"error":"D1 binding not provided"}`)
		}
		globalProvider = d1.New(d1Binding)
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