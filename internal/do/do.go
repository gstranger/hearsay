//go:build wasm

package do

import (
	"context"
	"syscall/js"
	"time"

	"github.com/gstranger/hearsay/internal/d1"
	"github.com/gstranger/hearsay/internal/server"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

// CoordinatorDO is a Durable Object that handles all coordination for one namespace.
// Each namespace gets its own DO instance. In the WASM build, the Worker-level
// fetch handler creates a server.Server per request rather than routing through
// DO Fetch — the DO is used primarily for the alarm-based sweeper. Full DO
// isolation with request forwarding is a follow-on optimization.
type CoordinatorDO struct {
	provider *d1.Provider
	server   *server.Server
}

// NewCoordinatorDO is the DO constructor. Called by Cloudflare when a DO instance
// is created for a namespace.
func NewCoordinatorDO(state js.Value, env js.Value) *CoordinatorDO {
	d1Binding := env.Get("HEARSAY_D1")
	provider := d1.New(d1Binding)
	client := hearsay.NewClient(provider, "default")

	srv := server.New(client, provider, false, "", server.LogFormatText, 0, 0, hearsay.AuditOff, "")

	// Start the sweeper alarm (fires every 60s)
	state.Get("storage").Call("setAlarm", time.Now().Add(60*time.Second))

	return &CoordinatorDO{
		provider: provider,
		server:   srv,
	}
}

// Fetch handles every HTTP request routed to this DO.
// For the initial implementation, the Worker fetch handler in main_wasm.go
// handles requests directly. This method exists as a hook for when we
// transition to full DO-based request routing.
func (d *CoordinatorDO) Fetch(request js.Value) js.Value {
	return js.Null()
}

// Alarm is called by Cloudflare when the alarm fires.
func (d *CoordinatorDO) Alarm(state js.Value) {
	ctx := context.Background()
	d.provider.ReleaseExpired(ctx, "default", time.Now())
	state.Get("storage").Call("setAlarm", time.Now().Add(60*time.Second))
}