// Package appevents is apid's customer app events surface: publishing
// (single and batch), schemas and their rollout, subscriptions, deliveries
// and receipts, backlog and storage, retention, replay and backfill, circuit
// and routing retries, and recovery with its notifications. It was lifted out
// of cmd/apid by ADR-977. The live SSE event stream stays in cmd/apid with
// the session and auth plumbing it needs.
//
// The package holds no state. Everything it needs from apid goes through
// Host, which cmd/apid implements by reading its own fields at call time.
package appevents

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what the app events surface needs from apid.
type Host interface {
	httpkit.AppLoader
	Store() state.Store
	WorkflowRuntimeEnabled() bool
	Logger() *slog.Logger
	EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any)
	// Notify sends a pg_notify wake-up.
	Notify(ctx context.Context, channel, payload string) error
	// PublishLimiter is the per-account event publish rate; nil admits
	// everything.
	PublishLimiter() *events.PublishLimiter
}

// Service is the app events surface.
type Service struct {
	Host Host
}
