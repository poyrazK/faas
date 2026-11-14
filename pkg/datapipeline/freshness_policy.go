package datapipeline

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func validFreshnessTarget(seconds int64) bool {
	return seconds == 0 || seconds >= api.DataPipelineFreshnessTargetMinSeconds && seconds <= api.DataPipelineFreshnessTargetMaxSeconds
}

// FreshnessStatus evaluates confirmation evidence at read time. An old lower
// bound is unconfirmed, not a measurement of how far replication is behind.
func FreshnessStatus(p api.DataPipeline, now time.Time) *api.DataPipelineFreshness {
	if p.FreshnessTargetSeconds == 0 {
		return nil
	}
	f := &api.DataPipelineFreshness{TargetSeconds: p.FreshnessTargetSeconds, State: "unknown", Message: "Freshness cannot currently be confirmed."}
	applied, err := time.Parse(time.RFC3339Nano, p.AppliedThroughAt)
	if err == nil && !applied.After(now) {
		age := int64(now.Sub(applied) / time.Second)
		f.ConfirmedThroughAt, f.ConfirmationAgeSeconds = p.AppliedThroughAt, &age
	}
	if p.DesiredState != "running" || p.State == "deleted" {
		f.State, f.Message = "inactive", "The freshness target is inactive while replication is paused or disconnected."
		return f
	}
	observed, observedErr := time.Parse(time.RFC3339Nano, p.ObservedAt)
	if !p.ObservationFresh || observedErr != nil || observed.After(now) || now.Sub(observed) >= api.DataPipelineObservationMaxAge || p.State != "live" || !validFreshnessTarget(p.FreshnessTargetSeconds) {
		return f
	}
	if f.ConfirmationAgeSeconds == nil {
		f.State, f.Message = "unconfirmed", "No applied checkpoint confirms the requested freshness target. An idle source may already be caught up."
		return f
	}
	if now.Sub(applied) <= time.Duration(p.FreshnessTargetSeconds)*time.Second {
		f.State, f.Message = "confirmed", "An applied source checkpoint confirms the freshness target."
	} else {
		f.State, f.Message = "unconfirmed", "The last confirmed checkpoint is older than the target. This does not prove that customer data is lagging."
	}
	return f
}

func (s *Service) ConfigureFreshness(ctx context.Context, account, id string, r api.DataPipelineFreshnessRequest) error {
	if s == nil || s.Store == nil || s.Allowed == nil || !s.Allowed(ctx, account) {
		return ErrUnavailable
	}
	if !validID(account) || !validID(id) || r.ExpectedRevision < 1 || !validFreshnessTarget(r.TargetSeconds) {
		return ErrInvalid
	}
	store, ok := s.Store.(interface {
		ConfigureFreshness(context.Context, string, string, int64, int64) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.ConfigureFreshness(ctx, account, id, r.ExpectedRevision, r.TargetSeconds)
}

func (s *PostgresStore) ConfigureFreshness(ctx context.Context, account, id string, revision, seconds int64) error {
	n, err := s.q.PipelineSetFreshness(ctx, s.pool, sqlc.PipelineSetFreshnessParams{AccountID: account, ID: id, Revision: revision, TargetSeconds: seconds})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
