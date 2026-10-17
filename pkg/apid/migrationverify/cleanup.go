// adr:1573 — server-driven migration checks and durable restoration.
package migrationverify

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Cancel durably fences subsequent work, including an in-flight worker's save.
// Keep its lease intact: cleanup cannot overtake a park that may still commit.
func (b Backend) Cancel(ctx context.Context, id string) (api.MigrationVerification, error) {
	if _, err := uuid.Parse(id); err != nil {
		return api.MigrationVerification{}, state.ErrNotFound
	}
	for range api.MigrationVerificationCancelAttempts {
		r, err := b.Store.GetMigrationVerification(ctx, b.AccountID, b.AppID, id)
		if err != nil {
			return r.Report, err
		}
		if r.Report.Status != "pending" || r.Report.CancelRequestedAt != nil {
			return b.observe(ctx, r.Report)
		}
		now := time.Now().UTC()
		r.Report.CancelRequestedAt = &now
		r.NextAttemptAt = now
		r, err = b.Store.SaveMigrationVerification(ctx, r, r.Version, nil)
		if errors.Is(err, state.ErrConflict) {
			continue
		}
		return r.Report, err
	}
	return api.MigrationVerification{}, state.ErrConflict
}

func stop(r *state.MigrationVerificationRecord, status, reason string) {
	r.StopStatus, r.StopReason = status, reason
	r.Report.Status, r.Report.Phase, r.Report.Reason = "pending", "restoring", reason
	if r.RestoreRequired {
		r.Report.RecoveryStatus = "pending"
	}
}

func (b Backend) cleanup(ctx context.Context, r state.MigrationVerificationRecord) (api.MigrationVerification, error) {
	// Cleanup has its own bounded lease budget; the verification deadline has
	// ended qualification, not the obligation to stop tasks and restore service.
	ctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), r.LeaseUntil.Add(-api.MigrationVerificationPersistTimeout))
	defer cancel()
	stopped := true
	for _, id := range []string{r.Report.DependencyTaskID, r.Report.ColdDependencyTaskID} {
		if id == "" {
			continue
		}
		task, err := b.Tasks.RequestAppTaskCancellation(ctx, b.AccountID, b.AppID, id, time.Now().UTC())
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return b.retry(ctx, r, err)
		}
		stopped = stopped && task.Status.Terminal()
	}
	if r.RestoreRequired {
		status, err := b.Restore(ctx, r.Report)
		if err != nil {
			return b.retry(ctx, r, err)
		}
		switch status {
		case "restored", "superseded", "blocked":
			r.Report.RecoveryStatus = status
			r.RestoreRequired = false
		default:
			r.Report.RecoveryStatus = "pending"
		}
	}
	if stopped && !r.RestoreRequired {
		now := time.Now().UTC()
		r.Report.Status, r.Report.Phase, r.Report.Reason = r.StopStatus, "complete", r.StopReason
		r.Report.CompletedAt = &now
	}
	r.Failures = 0
	r.NextAttemptAt = time.Now().Add(api.MigrationVerificationStepInterval)
	return b.save(ctx, r, nil)
}
