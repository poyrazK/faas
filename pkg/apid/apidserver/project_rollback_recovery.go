package apidserver

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) observeProjectRollbackTraffic(ctx context.Context, op *state.ProjectRollbackRecord) {
	store, ok := s.store.(state.ProjectRollbackHTTPRecoveryStore)
	if !ok || len(op.HTTPWorkloads) == 0 {
		return
	}
	observed, err := store.ProjectRollbackHTTPRecovery(ctx, op.ID)
	if err != nil {
		s.log.Warn("read project rollback traffic verification", "operation", op.ID, "err", err)
	}
	projectRollbackTraffic(op, observed, err == nil)
}

func projectRollbackTraffic(op *state.ProjectRollbackRecord, observed map[string]api.ProjectRollbackTraffic, available bool) {
	op.RecoveryMS = nil
	var lastSuccess time.Time
	verified := 0
	for i := range op.Steps {
		step := &op.Steps[i]
		if !slices.Contains(op.HTTPWorkloads, step.AppID) {
			continue
		}
		traffic, found := observed[step.AttemptID]
		if step.PreparationStatus == "released" && traffic.RoutingCommittedAt == nil {
			step.Traffic = nil
			continue
		}
		switch {
		case !available || !found && step.Status == "rolled_back" ||
			step.Status == "failed" && traffic.RoutingCommittedAt == nil:
			traffic.Status = "unavailable"
		case !found:
			traffic.Status = "awaiting_cutover"
		}
		step.Traffic = &traffic
		if traffic.Status == "verified" && traffic.FirstSuccessAt != nil {
			verified++
			if traffic.FirstSuccessAt.After(lastSuccess) {
				lastSuccess = *traffic.FirstSuccessAt
			}
		}
	}
	if op.Status == "complete" && verified != 0 && verified == len(op.HTTPWorkloads) &&
		op.CreatedAt != nil && !lastSuccess.Before(*op.CreatedAt) {
		ms := lastSuccess.Sub(*op.CreatedAt).Milliseconds()
		op.RecoveryMS = &ms
	}
}
