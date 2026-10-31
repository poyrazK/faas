package managedpostgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RuntimeConnectionReport contains private configuration evidence from one
// authenticated workload. It is not an independent attestation of guest code.
type RuntimeConnectionReport api.DataAPIRuntimeConnectionReport

// RuntimeConnectionAuthority comes only from the workload verifier, never JSON.
type RuntimeConnectionAuthority struct {
	AccountID, AppID, InstanceID string
	TokenExpiresAt               time.Time
}

type RuntimeConnectionStore interface {
	RecordRuntimeConnections(context.Context, RuntimeConnectionAuthority, RuntimeConnectionReport, time.Time) error
}

func (s *BindingService) RecordRuntimeConnections(ctx context.Context, authority RuntimeConnectionAuthority, report RuntimeConnectionReport) error {
	store, ok := s.bindings.(RuntimeConnectionStore)
	if !ok {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, api.DataAPIRuntimeConnectionReportTimeout)
	defer cancel()
	return store.RecordRuntimeConnections(ctx, authority, report, s.now())
}

func (r RuntimeConnectionReport) valid(a RuntimeConnectionAuthority, now time.Time) bool {
	for _, id := range []string{a.AccountID, a.AppID, a.InstanceID, r.BindingID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	return r.Version == 1 && validSHA256(r.DatabaseIdentity) && r.DatabaseGeneration > 0 && r.CredentialGeneration > 0 && len(r.Role) > 0 && len(r.Role) <= 63 &&
		(r.State == "observed" || r.State == "unknown") && r.RuntimeModel == "postgrest-14.3-pools-v1" &&
		r.EngineConnections >= 1 && r.RefreshConnections >= 0 && r.RefreshConnections <= 1 &&
		r.EngineConnections+r.RefreshConnections <= api.DataAPIMaxPoolConnections &&
		!r.ObservedAt.IsZero() && !r.ObservedAt.After(now) && now.Before(r.ObservedAt.Add(api.DataAPIConnectionBudgetMaxAge)) &&
		now.Before(a.TokenExpiresAt)
}

func (s *PostgresStore) RecordRuntimeConnections(ctx context.Context, a RuntimeConnectionAuthority, r RuntimeConnectionReport, now time.Time) error {
	if !r.valid(a, now) {
		return ErrInvalid
	}
	expires := minTime(r.ObservedAt.Add(api.DataAPIConnectionBudgetMaxAge), a.TokenExpiresAt)
	count, err := sqlc.New().RecordDataAPIRuntimeConnections(ctx, s.pool, sqlc.RecordDataAPIRuntimeConnectionsParams{
		AccountID: a.AccountID, AppID: a.AppID, InstanceID: a.InstanceID, BindingID: r.BindingID,
		DatabaseIdentity: r.DatabaseIdentity, DatabaseGeneration: r.DatabaseGeneration, CredentialGeneration: r.CredentialGeneration, Role: r.Role,
		State: r.State, RuntimeModel: r.RuntimeModel, EngineConnections: r.EngineConnections, RefreshConnections: r.RefreshConnections,
		ObservedAt: healthTimestamp(r.ObservedAt), ExpiresAt: healthTimestamp(expires), Now: healthTimestamp(now),
		HeartbeatSeconds: int32(state.DefaultHeartbeatStaleness.Seconds()),
	})
	if err != nil {
		return mapPostgresError(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
