package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	managedRealtimeDrainWorkerInterval = time.Second
	managedRealtimeDrainWorkerBatch    = 16
	managedRealtimeDrainClaimLease     = 2 * time.Minute
	managedRealtimeDrainMaxAttempts    = 5
)

// runManagedRealtimeDrainWorker resumes durable drain rows after boot and
// claims new rows without tying execution to the request that created them.
// The handler also wakes this loop and starts a best-effort immediate pass so
// tests and single-box installs do not wait for the first ticker.
func (s *server) runManagedRealtimeDrainWorker(ctx context.Context) {
	worker, ok := s.store.(state.ManagedRealtimeDrainOperationWorker)
	if !ok {
		return
	}

	runPass := func() { s.runManagedRealtimeDrainPass(ctx, worker) }

	runPass()
	ticker := time.NewTicker(managedRealtimeDrainWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runPass()
		case <-s.realtimeDrainWake:
			runPass()
		}
	}
}

func (s *server) runManagedRealtimeDrainPass(ctx context.Context, worker state.ManagedRealtimeDrainOperationWorker) {
	claims, err := worker.ClaimManagedRealtimeDrainOperations(ctx, managedRealtimeDrainWorkerBatch, managedRealtimeDrainClaimLease)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.log.WarnContext(ctx, "claim managed realtime drain operations", "err", err)
		}
		return
	}
	for _, claim := range claims {
		s.executeManagedRealtimeDrainOperation(ctx, worker, claim)
	}
}

func (s *server) wakeManagedRealtimeDrainWorker() {
	if s.realtimeDrainWake == nil {
		return
	}
	select {
	case s.realtimeDrainWake <- struct{}{}:
	default:
	}
}

func (s *server) executeManagedRealtimeDrainOperation(ctx context.Context, worker state.ManagedRealtimeDrainOperationWorker, claim state.ManagedRealtimeDrainOperationClaim) {
	op := claim.Operation
	response := managedRealtimeDrainResponseForExecution(op)
	retryIDs := make([]string, 0, len(op.ConnectionIDs))
	lastError := ""

	for _, connectionID := range op.ConnectionIDs {
		if op.DryRun {
			response.Results = append(response.Results, api.ManagedRealtimeDrainResult{ID: connectionID, Status: managedRealtimeDrainStatusWouldClose})
			continue
		}

		owner := s.realtimeOwner
		if owner == nil {
			lastError = "managed realtime owner unavailable"
			if op.Attempts >= managedRealtimeDrainMaxAttempts {
				response.Results = append(response.Results, api.ManagedRealtimeDrainResult{ID: connectionID, Status: managedRealtimeDrainStatusFailed})
			} else {
				retryIDs = append(retryIDs, connectionID)
			}
			continue
		}

		err := owner.CloseConnection(ctx, op.EndpointID, connectionID, op.Reason)
		switch {
		case err == nil:
			response.Results = append(response.Results, api.ManagedRealtimeDrainResult{ID: connectionID, Status: managedRealtimeDrainStatusClosed})
		case managedRealtimeConnectionGone(err):
			response.Results = append(response.Results, api.ManagedRealtimeDrainResult{ID: connectionID, Status: managedRealtimeDrainStatusGone})
		default:
			lastError = err.Error()
			if op.Attempts >= managedRealtimeDrainMaxAttempts {
				response.Results = append(response.Results, api.ManagedRealtimeDrainResult{ID: connectionID, Status: managedRealtimeDrainStatusFailed})
			} else {
				retryIDs = append(retryIDs, connectionID)
			}
			s.log.WarnContext(ctx, "drain managed realtime connection", "operation_id", op.ID, "endpoint_id", op.EndpointID, "connection_id", connectionID, "attempt", op.Attempts, "err", err)
		}
	}

	response.Closed, response.Gone, response.Failed = countManagedRealtimeDrainResults(response.Results)
	result, err := json.Marshal(response)
	if err != nil {
		s.log.WarnContext(ctx, "encode managed realtime drain operation result", "operation_id", op.ID, "err", err)
		return
	}

	if len(retryIDs) > 0 {
		nextAttemptAt := time.Now().UTC().Add(managedRealtimeDrainRetryDelay(op.Attempts))
		if err := worker.RetryManagedRealtimeDrainOperation(ctx, op.ID, claim.ClaimToken, retryIDs, result, response.Closed, response.Gone, response.Failed, nextAttemptAt, lastError); err != nil && !errors.Is(err, state.ErrManagedRealtimeDrainOperationNotFound) {
			s.log.WarnContext(ctx, "reschedule managed realtime drain operation", "operation_id", op.ID, "err", err)
		}
		return
	}

	response.Status = string(state.ManagedRealtimeDrainOperationCompleted)
	if response.Gone > 0 || response.Failed > 0 {
		response.Status = string(state.ManagedRealtimeDrainOperationPartial)
	}
	completedAt := api.FormatAlertTime(time.Now().UTC())
	response.CompletedAt = &completedAt
	result, err = json.Marshal(response)
	if err != nil {
		s.log.WarnContext(ctx, "encode completed managed realtime drain operation", "operation_id", op.ID, "err", err)
		return
	}
	if err := worker.FinishManagedRealtimeDrainOperation(ctx, op.ID, claim.ClaimToken, state.ManagedRealtimeDrainOperationStatus(response.Status), result, response.Closed, response.Gone, response.Failed); err != nil {
		if !errors.Is(err, state.ErrManagedRealtimeDrainOperationNotFound) {
			s.log.WarnContext(ctx, "finish managed realtime drain operation", "operation_id", op.ID, "err", err)
		}
		return
	}

	auditKind := "realtime.connections_drained"
	if op.DryRun {
		auditKind = "realtime.connections_drain_previewed"
	}
	accountID := op.AccountID
	s.audit.Emit(ctx, auditKind, &accountID, map[string]any{
		"operation_id": op.ID,
		"endpoint_id":  op.EndpointID,
		"matched":      op.Matched,
		"closed":       response.Closed,
		"gone":         response.Gone,
		"failed":       response.Failed,
		"dry_run":      op.DryRun,
		"partial":      op.Partial,
		"all":          op.All,
		"truncated":    op.Truncated,
		"reason":       op.Reason,
		"status":       response.Status,
	})
}

func managedRealtimeDrainResponseForExecution(operation state.ManagedRealtimeDrainOperation) api.ManagedRealtimeDrainResponse {
	var response api.ManagedRealtimeDrainResponse
	if len(operation.Result) > 0 && string(operation.Result) != "{}" {
		_ = json.Unmarshal(operation.Result, &response)
	}
	response.OperationID = operation.ID
	response.Status = string(operation.Status)
	response.CreatedAt = api.FormatAlertTime(operation.CreatedAt)
	response.Matched = operation.Matched
	response.Limit = operation.Limit
	response.All = operation.All
	response.Truncated = operation.Truncated
	response.DryRun = operation.DryRun
	response.Partial = operation.Partial
	response.NodesQueried = operation.NodesQueried
	response.NodesUnavailable = operation.NodesUnavailable
	if response.Results == nil {
		response.Results = []api.ManagedRealtimeDrainResult{}
	}
	return response
}

func countManagedRealtimeDrainResults(results []api.ManagedRealtimeDrainResult) (closed, gone, failed int) {
	for _, result := range results {
		switch result.Status {
		case managedRealtimeDrainStatusClosed:
			closed++
		case managedRealtimeDrainStatusGone:
			gone++
		case managedRealtimeDrainStatusFailed:
			failed++
		}
	}
	return closed, gone, failed
}

func managedRealtimeDrainRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Second
	case 2:
		return 5 * time.Second
	case 3:
		return 30 * time.Second
	default:
		return 2 * time.Minute
	}
}
