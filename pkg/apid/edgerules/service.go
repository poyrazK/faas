// Package edgerules is apid's edge rules surface: per-app edge rules and
// their versions, rule lists, events, stats and edge protection, plus the
// fleet convergence that publishes rule generations to serving gateways and
// waits for their acknowledgements. It was lifted out of cmd/apid by ADR-977.
//
// Everything it needs from apid goes through Host, which cmd/apid implements
// by reading its own fields at call time.
package edgerules

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// Notifier is the pg_notify surface edge rule convergence uses to announce a
// new generation and collect gateway acknowledgements.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
	Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error)
}

// Host is what the edge rules surface needs from apid.
type Host interface {
	httpkit.AppLoader
	Store() state.Store
	Logger() *slog.Logger
	EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any)
	Notifier() Notifier
	// PromQL is nil when no Prometheus endpoint is configured.
	PromQL() *promql.Client
	// PlatformFlagEnabled decides a platform flag (ADR-1700) for acct.
	PlatformFlagEnabled(flagKey string, acct state.Account) bool
}

// Service is the edge rules surface.
type Service struct {
	Host Host

	// SettingCheck refuses an edge rule kind whose capability is not released
	// to the account (ADR-20261023115741); nil allows every kind.
	SettingCheck func(acct state.Account, kind string) *api.Problem

	// FleetRequired is true on named multi-box control planes. Those
	// deployments must see at least one active serving gateway before a
	// policy mutation can commit; legacy single-box/dev installs have no
	// compute registry and apply the same generation locally without fleet
	// ACKs.
	FleetRequired bool

	// Metrics counts convergence skips and failures; nil records nothing.
	Metrics *Metrics
}
