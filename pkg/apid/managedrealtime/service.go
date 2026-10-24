// Package managedrealtime is apid's managed realtime surface: endpoints,
// connections, channels and publishes, retained history, inbox and
// notifications, push, schedules, reducers and signals, plus the owner,
// reconcile, drain, fallback and expiry workers. It was lifted out of
// cmd/apid by ADR-977 (named apart from pkg/realtime, which it builds on).
//
// Everything it needs from apid goes through Host, which cmd/apid implements
// by reading its own fields and test hooks at call time.
package managedrealtime

import (
	"bytes"
	"context"
	"github.com/onebox-faas/faas/pkg/middleware"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"filippo.io/age"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Host is what the managed realtime surface needs from apid.
type Host interface {
	httpkit.AppLoader
	Store() state.Store
	Logger() *slog.Logger
	EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any)
	// Ops is nil when apid runs without operational metrics.
	Ops() *wire.OpsMetrics
	// SecretRecipient seals push targets and provider configs; nil when the
	// host secrets are not installed.
	SecretRecipient() *age.X25519Recipient
	HostIdentitiesForUnseal(ctx context.Context) []*age.X25519Identity
	// ResolveAndCheckEgress applies the tenant egress policy to a callback URL.
	ResolveAndCheckEgress(ctx context.Context, rawURL string) *api.Problem
}

// Service is the managed realtime surface. apid sets Owner, Registrar and
// the history preview at boot (see its With* setters); DrainWake is a
// 1-buffered channel created by newServer.
type Service struct {
	AccessPreviewEnabled  bool
	TicketsPreviewEnabled bool
	ClaimWaitOnce         sync.Once
	ClaimWaitSlots        chan struct{}
	AuthLimiter           *middleware.Limiter
	Host                  Host

	// Owner routes customer-facing connection operations to the node that
	// owns a live socket; the local Unix client is the same-box fast path.
	Owner RealtimeOwner
	// Registrar is optional in split-box deployments; when set, durable
	// endpoint writes are mirrored onto the local or leased owner.
	Registrar      RealtimeEndpointRegistrar
	BackendSignals RealtimeBackendSignalLimiter
	// DrainWake nudges the durable drain worker after a new operation is
	// committed; its ticker remains the restart/recovery backstop.
	DrainWake chan struct{}
	// HistoryPreviewEnabled keeps retained messages a storage preview until
	// channel grants and the versioned reconnect protocol are ready.
	HistoryPreviewEnabled bool
	HistoryMetrics        *ManagedRealtimeHistoryMetrics
	// OccupancyPreviewEnabled serves channel occupancy (ADR-1324).
	OccupancyPreviewEnabled bool
	// UsageEnabled serves the account's realtime usage (ADR-1323), recorded
	// only by realtimed nodes with the same flag.
	UsageEnabled bool
	// FairUseEnabled sends each endpoint's account connection limit to
	// realtimed (ADR-1323); off, endpoints register without one.
	FairUseEnabled bool
	// LiveRoutes lists the live-query keys an app's endpoints route
	// (ADR-20261016151947): those of live databases bound to the app. Nil routes none.
	LiveRoutes func(ctx context.Context, accountID, appID string) ([]realtime.LiveRoute, error)
	// PreviewReleased decides a realtime preview platform flag for a request
	// (ADR 20261021080640); legacy is the environment gate that decides until
	// an operator writes the flag. Nil uses legacy.
	PreviewReleased func(r *http.Request, acct state.Account, flagKey string, legacy bool) bool
	// EndpointPreviews are the per-account preview decisions sent with an
	// endpoint's registration. A nil result sends none, so realtimed's own
	// switches decide.
	EndpointPreviews func(ctx context.Context, accountID, appID string) (*realtime.EndpointPreviews, error)
	// RealtimeReleased decides the realtime capability flag for an
	// endpoint's account (ADR-20261023115741). An endpoint without it, and
	// without live routes, is withdrawn from realtimed. Nil releases it.
	RealtimeReleased func(ctx context.Context, accountID, appID string) bool

	publishIdempotencySweep atomic.Uint64
}

// Registry is the registry the metrics were registered on, so apid can tell
// whether WithOpsMetrics needs to rebind them.
func (m *ManagedRealtimeHistoryMetrics) Registry() *prometheus.Registry { return m.registry }

// idempotencyReserver is the optional store capability idempotent publishes
// use to release a reservation whose outcome must not be replayed.
type idempotencyReserver interface {
	ReserveIdempotent(ctx context.Context, accountID, key string, abandonAfter time.Duration) (state.IdempotencyReservation, error)
	ReleaseIdempotent(ctx context.Context, accountID, key string) error
}

// publishCaptureWriter records a publish response so it can be stored for
// idempotent replay while still streaming it to the caller.
type publishCaptureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *publishCaptureWriter) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *publishCaptureWriter) Write(b []byte) (int, error) {
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}
