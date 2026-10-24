package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrProjectEnvironmentCloneTargetsDraining means a data-less clone's cancelled
// target deployments still have instances; compensation retries once schedd
// has drained them.
var ErrProjectEnvironmentCloneTargetsDraining = fmt.Errorf("clone target deployments are still draining: %w", ErrConflict)

// ProjectEnvironmentCloneDatalessCompensationStore removes the target of a
// clone that captured no managed data and will not publish
// (ADR-20261024123122). Retirement cancels the target deployments it created
// and reports how many of their instances are still active; finishing
// deletes the target environment and marks the operation compensated in one
// transaction, once nothing of the target runs.
type ProjectEnvironmentCloneDatalessCompensationStore interface {
	RetireDatalessProjectEnvironmentCloneTargetsForLease(context.Context, ProjectEnvironmentCloneLease) (int64, error)
	FinishDatalessProjectEnvironmentCloneCompensationForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneOperation, error)
}

// ProjectEnvironmentCloneIsDataless reports whether an operation's inventory
// has no managed data or data checkpoint, so removing its target environment
// and deployments leaves nothing behind.
func ProjectEnvironmentCloneIsDataless(op ProjectEnvironmentCloneOperation) bool {
	for _, resource := range op.Resources {
		switch {
		case strings.HasPrefix(resource.Kind, "data_checkpoint"),
			resource.Kind == "managed_postgres", resource.Kind == "postgres", resource.Kind == "object_storage":
			return false
		}
	}
	return true
}

func (s *PgStore) datalessCloneCompensationTx(ctx context.Context, l ProjectEnvironmentCloneLease, work func(context.Context, pgx.Tx, ProjectEnvironmentCloneOperation) error) error {
	if !validCloneLeaseIdentity(l) || l.Operation.Status != CloneOperationCompensating {
		return ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return err
	}
	if op.Status != CloneOperationCompensating || !ProjectEnvironmentCloneIsDataless(op) {
		return ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	if err := work(ctx, tx, op); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) RetireDatalessProjectEnvironmentCloneTargetsForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (int64, error) {
	var active int64
	err := s.datalessCloneCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		q := sqlc.New()
		if _, err := q.CancelProjectEnvironmentCloneTargetDeployments(ctx, tx, sqlc.CancelProjectEnvironmentCloneTargetDeploymentsParams{
			OperationID: mustPgUUID(op.ID), Environment: op.TargetEnvironment}); err != nil {
			return mapErr(err)
		}
		retirement, err := q.ReadProjectEnvironmentCloneTargetRetirement(ctx, tx, mustPgUUID(op.ID))
		if err != nil {
			return mapErr(err)
		}
		if retirement.Unretired != 0 {
			return ErrConflict
		}
		active = retirement.ActiveInstances
		return nil
	})
	return active, err
}

func (s *PgStore) FinishDatalessProjectEnvironmentCloneCompensationForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneOperation, error) {
	var finished ProjectEnvironmentCloneOperation
	err := s.datalessCloneCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		q := sqlc.New()
		retirement, err := q.ReadProjectEnvironmentCloneTargetRetirement(ctx, tx, mustPgUUID(op.ID))
		if err != nil {
			return mapErr(err)
		}
		if retirement.Unretired != 0 {
			return ErrConflict
		}
		if retirement.ActiveInstances != 0 {
			return ErrProjectEnvironmentCloneTargetsDraining
		}
		projectSlug, err := q.ReadProjectEnvironmentCloneProjectSlug(ctx, tx, sqlc.ReadProjectEnvironmentCloneProjectSlugParams{
			ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID)})
		if err != nil {
			return mapErr(err)
		}
		if err := cleanupEnvironmentInvocationsDB(ctx, tx, op.AccountID, op.ProjectID, op.TargetEnvironment); err != nil {
			return err
		}
		if err := q.DeleteProjectEnvironmentCloneTargetPolicies(ctx, tx, sqlc.DeleteProjectEnvironmentCloneTargetPoliciesParams{
			ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment}); err != nil {
			return mapErr(err)
		}
		// Materialization may not have created the environment before the
		// clone failed; there is then nothing more to remove.
		if err := deleteProjectEnvironmentRowsTx(ctx, tx, op.AccountID, op.ProjectID, projectSlug, op.TargetEnvironment); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		resources := make([]ProjectEnvironmentCloneResource, len(op.Resources))
		for i, resource := range op.Resources {
			resource.Status = "compensated"
			resources[i] = resource
		}
		if err := validateCloneResourceTransition(op, CloneOperationCompensated, resources, op.ErrorCode); err != nil {
			return err
		}
		raw, err := json.Marshal(resources)
		if err != nil {
			return err
		}
		count, err := q.AdvanceProjectEnvironmentCloneOperationStatus(ctx, tx, sqlc.AdvanceProjectEnvironmentCloneOperationStatusParams{
			OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID),
			ExpectedStatus: CloneOperationCompensating, NextStatus: CloneOperationCompensated, ExpectedRevision: op.Revision, Resources: raw, ErrorCode: op.ErrorCode})
		if err != nil {
			return mapErr(err)
		}
		if count != 1 {
			return ErrConflict
		}
		finished, err = cloneWorkerOperationDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
		return err
	})
	return finished, err
}

var _ ProjectEnvironmentCloneDatalessCompensationStore = (*PgStore)(nil)
