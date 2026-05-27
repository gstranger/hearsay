# Dual-Runtime Architecture — Design Document

> Date: 2026-05-26  
> Status: Draft  
> Scope: Build-tag architecture enabling hearsay to compile to both native Go binaries and Cloudflare Workers (WASM/Durable Objects)

---

## 1. Purpose

`hearsay` currently compiles to a single long-lived Go binary. This design splits the codebase so it compiles to two targets from one source tree:

1. **Native binary** — `go build ./cmd/hearsay` produces a standalone binary running `net/http.ListenAndServe`, using SQLite or PostgreSQL for storage. Self-hosters deploy it however they want (VM, container, local machine).

2. **Cloudflare Worker** — `GOOS=js GOARCH=wasm go build ./cmd/hearsay` produces a WASM bundle deployable to Cloudflare Workers. Uses Durable Objects (one per namespace) for compute and D1 for storage. Exposes the same REST + SSE API surface.

The SaaS control plane (accounts, billing, API keys, routing) is a **separate service** and out of scope for this design. This design covers only the coordination engine.

---

## 2. Architecture Principle: Minimize Build-Tagged Surface

The vast majority of the codebase compiles identically to both targets. Only three components are swapped:

| Component | Native | WASM |
|---|---|---|
| **Entrypoint** | `func main()` → `http.ListenAndServe` | `init()` exports DO class + Worker `fetch` handler |
| **Storage** | `sqlite` or `postgres` provider | `d1` provider |
| **Sweeper** | `time.NewTicker(60s)` goroutine | DO `state.alarm()` — self-rescheduling |

Everything else — route registration, claim/release/query/A2A handlers, SSE streaming, conflict detection, mailbox logic, CLI commands — is shared with zero build tags.

---

## 3. Package Layout

```
pkg/hearsay/              # UNCHANGED — pure logic, no build tags, cross-compiles
  provider.go             # Provider interface (SubscribeEvents added)
  client.go
  claims.go
  activeclaims.go
  mailbox.go
  config.go
  validation.go

internal/
  server/
    server.go             # Route registration, HTTP handlers (shared)
    sse.go                # SSE event streaming handler (shared)
    sweeper_native.go     //go:build !wasm — goroutine + time.Ticker
    sweeper_wasm.go       //go:build wasm  — DO alarm()

  a2a/                    # UNCHANGED — http.Handler based, pure logic
  mailbox/                # UNCHANGED

  sqlite/                 //go:build !wasm
  postgres/               //go:build !wasm
  managed/                //go:build !wasm
  memory/                 # UNCHANGED — tests only, no build tags needed

  d1/                     //go:build wasm — NEW
    d1.go                 # D1 bindings, same Provider contract as sqlite.go

  do/                     //go:build wasm — NEW
    do.go                  # Durable Object: fetch handler, alarm, SSE fan-out

cmd/hearsay/
  main_native.go          //go:build !wasm
    # func main() — net/http ListenAndServe, signal handling, CLI dispatch
  main_wasm.go            //go:build wasm
    # init() — registers DO class, exports Worker fetch handler
  cmd_*.go                # CLI command implementations (claim, release, query, etc.) — shared
```

---

## 4. Component Details

### 4.1 Entrypoint: Native (`main_native.go`)

```go
//go:build !wasm

package main

func main() {
    // Unchanged from current cmd/hearsay/main.go
    // CLI subcommand dispatch (init, claim, release, query, serve, watch, status, etc.)
    // http.ListenAndServe for serve subcommand
    // Graceful shutdown on SIGINT/SIGTERM
}
```

### 4.2 Entrypoint: Worker (`main_wasm.go`)

```go
//go:build wasm

package main

func init() {
    durable_objects.Register("CoordinatorDO", do.NewCoordinatorDO)

    js.Global().Set("fetch", js.FuncOf(func(this js.Value, args []js.Value) any {
        request := args[0]
        url := request.Get("url").String()

        // Extract namespace from URL path: /ns/<namespace>/claim, /ns/<namespace>/events, etc.
        namespace := extractNamespace(url)

        // Route to the right Durable Object
        doStub := durable_objects.Get(
            durable_objects.Namespace("COORDINATOR_DO"),
            namespace,
        )
        return doStub.Fetch(request)
    }))
}
```

The CLI subcommands are not available in the WASM build — only the server runtime.

### 4.3 Storage: D1 Provider (`internal/d1/d1.go`)

Copies `internal/sqlite/sqlite.go` almost verbatim. Same schema, same tables, same SQL.

Differences:
- `database/sql` replaced with D1 Worker binding API (`env.D1.Prepare(...).Bind(...).Run()`)
- `Subscribe()` still polls internally (500ms), but is not the primary push mechanism — SSE handles that
- Schema gains a `seq INTEGER NOT NULL` column on `messages` for SSE sequence numbers

```sql
CREATE TABLE IF NOT EXISTS messages (
  namespace TEXT NOT NULL,
  offset   INTEGER NOT NULL,
  seq      INTEGER NOT NULL,  -- NEW: per-namespace monotonic sequence for SSE replay
  type     TEXT NOT NULL,
  agent_id TEXT,
  payload  TEXT,
  timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (namespace, offset)
);
```

### 4.4 Durable Object (`internal/do/do.go`)

One DO instance per namespace. Handles all coordination for that namespace.

```go
type CoordinatorDO struct {
    state    *DurableObjectState  // Cloudflare DO state
    provider *d1.Provider         // D1 storage
    clients  *FanOut              // Connected SSE clients
    sweeper  *WasmSweeper         // DO alarm-based sweeper
}

func (d *CoordinatorDO) Fetch(request *Request) *Response {
    // Route by URL path using the same handler functions from internal/server/
    // /claim       → server.handleClaim (shared)
    // /release     → server.handleRelease (shared)
    // /claims      → server.handleQueryClaims (shared)
    // /events      → server.handleSSE (shared) — SSE streaming with fan-out
    // /mailbox     → server.handleGetMailbox (shared)
    // ...etc...
}

func (d *CoordinatorDO) Alarm() {
    // Called by Cloudflare when the alarm fires
    d.provider.ReleaseExpired(ctx, namespace, time.Now())
    d.state.Storage().SetAlarm(time.Now().Add(60 * time.Second))
}
```

Key design point: the `Fetch` method dispatches to the **same handler functions** as the native binary. The handlers operate on a `Provider` interface and produce HTTP responses — they don't care if they're inside a DO or a native server.

### 4.5 SSE Fan-Out

The SSE handler (`internal/server/sse.go`) is shared between runtimes. It receives events from `Provider.SubscribeEvents()` and writes them to the response. In the native binary, one handler handles all clients. In the DO, the same handler runs inside the DO for that namespace.

```go
// internal/server/sse.go — SHARED, no build tags

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
    namespace := r.URL.Query().Get("namespace")
    since := parseInt(r.URL.Query().Get("since"), 0)

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.WriteHeader(http.StatusOK)

    flusher := w.(http.Flusher)
    events, _ := s.provider.SubscribeEvents(r.Context(), namespace, since)

    // Replay events since the last known sequence number
    // Then stream new events as they arrive
    for event := range events {
        fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Payload)
        flusher.Flush()
    }
}
```

In the DO, multiple agents connected to the same namespace share the same `CoordinatorDO` instance — each SSE response gets its own goroutine reading from the fan-out. In the native binary, each SSE connection gets its own goroutine reading from the provider's subscription.

### 4.6 Sweeper

**Native** (`sweeper_native.go`):

```go
//go:build !wasm

func runSweeper(ctx context.Context, provider hearsay.Provider, namespace string) {
    ticker := time.NewTicker(60 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            provider.ReleaseExpired(ctx, namespace, time.Now())
        case <-ctx.Done():
            return
        }
    }
}
```

**WASM** (`sweeper_wasm.go`):

```go
//go:build wasm

type WasmSweeper struct {
    do *DurableObjectState
}

func (s *WasmSweeper) Start() {
    s.do.Storage().SetAlarm(time.Now().Add(60 * time.Second))
}
```

---

## 5. Provider Interface Additions

```go
// pkg/hearsay/provider.go

type Event struct {
    Seq       int64           `json:"seq"`
    Type      EventType       `json:"type"`
    Payload   json.RawMessage `json:"payload"`
    Timestamp time.Time       `json:"timestamp"`
}

type EventType string

const (
    EventClaim     EventType = "claim"
    EventRelease   EventType = "release"
    EventHeartbeat EventType = "heartbeat"
    EventMailbox   EventType = "mailbox_send"
)

// SubscribeEvents returns a channel of events for the given namespace,
// starting from the specified sequence number. If since is 0, only new
// events are streamed (no replay).
//
// The channel is closed when ctx is cancelled.
SubscribeEvents(ctx context.Context, namespace string, since int64) (<-chan Event, error)
```

Implementations:
- **SQLite/Postgres**: query `messages` for seq > since, stream results, then poll for new messages
- **D1**: same pattern via D1 bindings
- **DO**: the DO's internal fan-out is the channel — no polling needed since the DO handles all writes for that namespace

---

## 6. SSE Protocol

Agents connect:

```
GET /events?namespace=project-x&since=42 HTTP/1.1
Authorization: Bearer <token>

HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-cache

event: claim
data: {"seq":43,"type":"claim","payload":{"claim_id":"c1","agent_id":"bot-1","resource":"file://src/api.go",...}}

event: mailbox
data: {"seq":44,"type":"mailbox","payload":{"message_id":"m1","from":"bot-2","type":"yield_request",...}}

event: release
data: {"seq":45,"type":"release","payload":{"claim_id":"c1","outcome":"succeeded"}}
```

Event types: `claim`, `release`, `heartbeat`, `mailbox`.

Reconnection: agents track the last received `seq`. On disconnect, reconnect with `?since=<last_seq>`. The server replays events from `last_seq + 1`.

---

## 7. What Is Shared (No Build Tags)

| Package | Reason |
|---|---|
| `pkg/hearsay/provider.go` | Pure interface definitions |
| `pkg/hearsay/client.go` | Operates on Provider interface |
| `pkg/hearsay/claims.go` | Pure conflict detection logic |
| `pkg/hearsay/activeclaims.go` | Pure FilterActiveClaims |
| `pkg/hearsay/mailbox.go` | Pure type definitions |
| `pkg/hearsay/config.go` | Needs minor refactor to not assume filesystem (config passed at init, not loaded from disk in WASM) |
| `pkg/hearsay/validation.go` | Pure validation |
| `internal/server/server.go` | Route registration + handlers — operate on `Provider` + `http.ResponseWriter` |
| `internal/server/sse.go` | SSE handler — same interface |
| `internal/server/middleware.go` | Auth, logging, rate limiting — pure HTTP middleware |
| `internal/a2a/` | All A2A handlers — operate on `Provider` + `http.ResponseWriter` |
| `internal/mailbox/` | Mailbox business logic |
| `cmd/hearsay/cmd_*.go` | CLI command implementations — native-only but no build tags needed (unreachable in WASM) |

---

## 8. Files Changed

| File | Change |
|---|---|
| `pkg/hearsay/provider.go` | Add `Event`, `EventType`, `SubscribeEvents()` to Provider interface |
| `pkg/hearsay/config.go` | Add constructor that accepts config struct directly (not just LoadConfig from disk) |
| `internal/server/server.go` | Add `GET /events` SSE route |
| `internal/server/sse.go` | **New** — SSE handler |
| `internal/server/sweeper_native.go` | **New** — extract sweeper goroutine from `main.go` |
| `internal/server/sweeper_wasm.go` | **New** — DO alarm-based sweeper |
| `internal/sqlite/sqlite.go` | Add build tag `//go:build !wasm`; add `seq` column to messages; implement `SubscribeEvents` |
| `internal/postgres/postgres.go` | Add build tag `//go:build !wasm`; add `seq` column; implement `SubscribeEvents` |
| `internal/d1/d1.go` | **New** — D1 provider, same schema as sqlite |
| `internal/do/do.go` | **New** — Durable Object for namespace-scoped coordination |
| `cmd/hearsay/main_native.go` | **New** — native entrypoint (extracted from current `main.go`) |
| `cmd/hearsay/main_wasm.go` | **New** — Worker entrypoint |
| `cmd/hearsay/main.go` | **Deleted** — split into `main_native.go` + `main_wasm.go` + shared `cmd_*.go` files |
| `go.mod` | No new dependencies. D1/DO bindings use `syscall/js` (stdlib) |

---

## 9. Deployment

### Native (Self-Hosted)

```bash
go build ./cmd/hearsay
./hearsay serve --addr :8080 --provider sqlite
# or --provider postgresql --postgresql-url ...
```

No change from current usage.

### Cloudflare Worker

```bash
GOOS=js GOARCH=wasm go build -o hearsay.wasm ./cmd/hearsay
# Deploy hearsay.wasm via wrangler, configured with:
#   - D1 binding for storage
#   - Durable Object namespace "COORDINATOR_DO" class "CoordinatorDO"
```

---

## 10. Testing Strategy

### Unit Tests

- All existing `pkg/hearsay/` tests continue to pass on both `GOOS=darwin` and `GOOS=js`
- D1 provider passes the same contract test suite (`internal/contract.go`) as SQLite and Postgres
- SSE handler: verify framing, sequence number ordering, replay from `since`

### Integration Tests

- Native binary: end-to-end SSE stream — claim, receive event, reconnect with `since`, verify no missed events
- Worker: same tests via `wrangler dev` or Cloudflare's local DO simulator

### Build Verification

- CI runs `go build ./cmd/hearsay` on native
- CI runs `GOOS=js GOARCH=wasm go build ./cmd/hearsay` and verifies no compilation errors

---

## 11. What This Does NOT Cover

- SaaS control plane (accounts, billing, API routing) — separate service
- A2A SSE streaming (`tasks/sendSubscribe`) — covered in `docs/superpowers/specs/2026-05-26-sse-streaming-design.md`
- WebSocket support — SSE is sufficient for push; WebSocket can be added later
- Production Cloudflare configuration (wrangler.toml, D1 migrations, DO limits) — deployment story, not architecture

---

*End of design document*