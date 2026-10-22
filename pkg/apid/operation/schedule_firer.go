// adr: 20261018161833
package operation

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	scheduleFireInterval = 10 * time.Second
	scheduleFireBatch    = 100
	// A claimed row is skipped by other replicas until its lease lapses, so a
	// replica that dies mid-batch delays those fires by at most this long.
	scheduleFireLease = time.Minute
)

// Refusals the firer records instead of admitting.
const (
	scheduleRefusalClosed    = "operation schedules are not enabled for this account"
	scheduleRefusalAdmission = "new operation admission is disabled for this preview cohort"
)

// RunScheduleFirer fires due Operation schedules until ctx ends. Every apid
// replica runs it; leases give each due schedule to one replica.
func (s *Service) RunScheduleFirer(ctx context.Context) {
	if _, ok := s.Host.Store().(state.OperationScheduleStore); !ok {
		return
	}
	ticker := time.NewTicker(scheduleFireInterval)
	defer ticker.Stop()
	for {
		if _, err := s.FireDueOperationSchedules(ctx, time.Now()); err != nil && ctx.Err() == nil {
			s.Host.Logger().Warn("operation schedule fire sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// FireDueOperationSchedules fires one batch of due schedules and returns how
// many it settled. A fire that fails keeps its lease and is retried once the
// lease lapses; the fire time is unchanged, so the retry admits the same
// Operation.
func (s *Service) FireDueOperationSchedules(ctx context.Context, now time.Time) (int, error) {
	store, ok := s.Host.Store().(state.OperationScheduleStore)
	if !ok {
		return 0, nil
	}
	claimed, err := store.ClaimDueOperationSchedules(ctx, now, scheduleFireBatch, scheduleFireLease)
	if err != nil {
		return 0, err
	}
	var errs error
	fired := 0
	for _, schedule := range claimed {
		refusal := s.scheduleFireRefusal(ctx, schedule)
		after, err := store.FireOperationSchedule(ctx, state.OperationScheduleFire{Schedule: schedule, Refusal: refusal, Now: time.Now()})
		switch {
		case err != nil:
			s.Metrics.scheduleFire("error")
			errs = errors.Join(errs, err)
			continue
		case after.LastError != "":
			s.Metrics.scheduleFire("refused")
		default:
			s.Metrics.scheduleFire("admitted")
		}
		fired++
	}
	return fired, errs
}

// scheduleFireRefusal applies apid's own gates, which the store cannot see:
// the schedule rollout and the preview admission policy. Store-side refusals
// (plan limits, a suspended customer, a deleted definition) are recorded by
// the store.
func (s *Service) scheduleFireRefusal(ctx context.Context, schedule state.OperationSchedule) string {
	if !api.OperationSchedulesEnabled(schedule.AccountID) {
		return scheduleRefusalClosed
	}
	store, ok := s.Host.Store().(state.OperationStore)
	if !ok {
		return ""
	}
	def, err := store.OperationDefinitionByID(ctx, schedule.AccountID, schedule.DefinitionID)
	if err != nil {
		return ""
	}
	if !s.OperationTenantKindAdmission(schedule.AccountID, def.AppID, def.Scope, schedule.PlatformTenantID, operations.DefinitionExecutionKind(def.Spec)) {
		return scheduleRefusalAdmission
	}
	return ""
}
