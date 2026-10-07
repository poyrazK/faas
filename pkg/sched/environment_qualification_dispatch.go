package sched

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type EnvironmentQualificationVisitor func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error

type EnvironmentQualificationGraphVisitor func(context.Context, map[string]state.Instance) error

type EnvironmentQualificationDispatchPage struct {
	Examined   int
	Claimed    int
	Executed   int
	Skipped    int
	NextCursor string
}

type EnvironmentQualificationGraphDispatchPage struct {
	Examined   int
	Claimed    int
	Executed   int
	Skipped    int
	NextCursor string
}

// Dispatch consumes one bounded durable page, independently of notifications.
// Claim rechecks the graph and node owner; runtime rechecks before native effects.
// Each visit is lease-bounded and its original VM is retired before advancing.
// Executed means the visitor and retirement returned successfully, not that the
// graph is qualified. Smoke, snapshot and activation authority remain separate.
// Production does not invoke this consumer until those evidence gates exist.
func (e *Engine) DispatchEnvironmentWorkloadQualifications(ctx context.Context, nodeID, workerID, afterRequestID string, limit int, visit EnvironmentQualificationVisitor) (page EnvironmentQualificationDispatchPage, result error) {
	store, ok := e.store.(state.EnvironmentGitOpsQualificationDispatchStore)
	if !ok || visit == nil || !e.qualificationDispatchArgumentsValid(nodeID, workerID, afterRequestID, limit) {
		return page, state.ErrInvalidArgument
	}
	if !e.qualificationDispatchCapabilitiesAvailable() {
		return page, fmt.Errorf("qualification dispatch requires attempt-aware execution and retirement: %w", state.ErrConflict)
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	ids, err := store.ListEnvironmentWorkloadQualificationsForDispatch(ctx, nodeID, afterRequestID, limit)
	if err != nil {
		return page, err
	}
	page.NextCursor = afterRequestID
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return page, errors.Join(result, err)
		}
		page.Examined++
		page.NextCursor = id
		claimed, err := store.ClaimEnvironmentWorkloadQualificationForNode(ctx, id, nodeID, workerID, api.EnvironmentGitOpsQualificationLeaseDuration)
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			page.Skipped++
			continue
		}
		if err == nil {
			page.Claimed++
			err = e.WithEnvironmentWorkloadQualificationRuntime(ctx, claimed, func(ctx context.Context, ins state.Instance) error {
				return visit(ctx, claimed, ins)
			})
			if err == nil {
				page.Executed++
			}
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("qualification dispatch request %s: %w", id, err))
		}
	}
	if len(ids) < limit {
		page.NextCursor = ""
	}
	return page, result
}

// Dispatch service-binding graphs as a single durable cohort. Each graph gets
// all member attempts atomically before the private dependency-first execution
// window starts; successful return still does not qualify or activate it.
func (e *Engine) DispatchEnvironmentWorkloadQualificationGraphs(ctx context.Context, nodeID, workerID, afterGraphID string, limit int, visit EnvironmentQualificationGraphVisitor) (page EnvironmentQualificationGraphDispatchPage, result error) {
	store, ok := e.store.(state.EnvironmentGitOpsQualificationGraphDispatchStore)
	if !ok || visit == nil || !e.qualificationDispatchArgumentsValid(nodeID, workerID, afterGraphID, limit) {
		return page, state.ErrInvalidArgument
	}
	if !e.qualificationDispatchCapabilitiesAvailable() || e.environmentQualificationServiceURL == nil {
		return page, fmt.Errorf("qualification graph dispatch requires private binding and attempt-aware retirement adapters: %w", state.ErrConflict)
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	ids, err := store.ListEnvironmentWorkloadQualificationGraphsForDispatch(ctx, nodeID, afterGraphID, limit)
	if err != nil {
		return page, err
	}
	page.NextCursor = afterGraphID
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return page, errors.Join(result, err)
		}
		page.Examined++
		page.NextCursor = id
		claimed, err := store.ClaimEnvironmentWorkloadQualificationGraphForNode(ctx, id, nodeID, workerID, api.EnvironmentGitOpsQualificationLeaseDuration)
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			page.Skipped++
			continue
		}
		if err == nil {
			page.Claimed++
			graphCtx := context.WithValue(ctx, qualificationGraphDispatchNodeContextKey{}, nodeID)
			err = e.WithEnvironmentQualificationGraphRuntimes(graphCtx, claimed, visit)
			if err == nil {
				page.Executed++
			}
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("qualification graph dispatch %s: %w", id, err))
		}
	}
	if len(ids) < limit {
		page.NextCursor = ""
	}
	return page, result
}

func (e *Engine) qualificationDispatchCapabilitiesAvailable() bool {
	_, claim := e.store.(state.EnvironmentGitOpsQualificationStore)
	_, admission := e.store.(state.EnvironmentGitOpsQualificationInstanceStore)
	_, runtime := e.store.(state.EnvironmentGitOpsQualificationRuntimeStore)
	_, execution := e.store.(state.EnvironmentQualificationExecutionStore)
	_, native := e.vmm.(EnvironmentQualificationVMM)
	return claim && admission && runtime && execution && native
}

func (e *Engine) qualificationDispatchArgumentsValid(nodeID, workerID, cursor string, limit int) bool {
	node, err := uuid.Parse(nodeID)
	if err != nil || node == uuid.Nil || workerID == "" || workerID != strings.TrimSpace(workerID) ||
		len(workerID) > api.EnvironmentGitOpsQualificationWorkerIDMaxBytes || limit < 1 || limit > api.EnvironmentGitOpsQualificationDispatchBatchMax {
		return false
	}
	if cursor != "" {
		if id, err := uuid.Parse(cursor); err != nil || id == uuid.Nil {
			return false
		}
	}
	owner := e.ownerNodeID
	if owner == "" {
		owner = e.defaultLocalNodeID
	}
	ownerID, err := uuid.Parse(owner)
	return err == nil && ownerID == node
}
