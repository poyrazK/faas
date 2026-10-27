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

// ErrProjectEnvironmentCloneTargetDeploymentEnded means a deployment the clone
// created for its target failed or was retired, so the clone cannot publish.
var ErrProjectEnvironmentCloneTargetDeploymentEnded = fmt.Errorf("clone target deployment ended: %w", ErrConflict)

// ErrProjectEnvironmentCloneTargetsDraining means a clone's cancelled target
// deployments still have instances; compensation retries once schedd has
// drained them.
var ErrProjectEnvironmentCloneTargetsDraining = fmt.Errorf("clone target deployments are still draining: %w", ErrConflict)

// ErrProjectEnvironmentCloneTargetDatabasesRemaining means compensation still
// has restored target databases to delete before it removes the target.
var ErrProjectEnvironmentCloneTargetDatabasesRemaining = fmt.Errorf("clone target databases are still being deleted: %w", ErrConflict)

// ProjectEnvironmentCloneDirectCompensationStore removes the target of a clone
// that will not publish, when everything the clone made can be deleted
// directly (ADR-20261024123122, ADR-20261024224230). Retirement cancels the
// target deployments it created and reports how many of their instances are
// still active. Once none is, withdrawal soft-deletes the target bindings the
// clone prepared, with their stage secrets, and returns the restored target
// databases still to delete. Finishing deletes the target environment and
// marks the operation compensated in one transaction, once no restored
// database remains.
type ProjectEnvironmentCloneDirectCompensationStore interface {
	RetireProjectEnvironmentCloneTargetsForLease(context.Context, ProjectEnvironmentCloneLease) (int64, error)
	WithdrawProjectEnvironmentCloneDatabaseBindingsForLease(context.Context, ProjectEnvironmentCloneLease) ([]string, error)
	FinishProjectEnvironmentCloneDirectCompensationForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneOperation, error)
}

// ProjectEnvironmentCloneIsDirectlyCompensable reports whether an operation's
// inventory holds nothing that needs data-checkpoint retirement: no
// checkpoint, object copy or unmanaged PostgreSQL resource. Managed databases
// restored at the capture point are deleted directly.
func ProjectEnvironmentCloneIsDirectlyCompensable(op ProjectEnvironmentCloneOperation) bool {
	for _, resource := range op.Resources {
		switch {
		case strings.HasPrefix(resource.Kind, "data_checkpoint"), resource.Kind == "postgres", resource.Kind == "object_storage":
			return false
		}
	}
	return true
}

// Failure codes of a clone the coordinator ended itself because it could not
// finish (ADR-20261024123122, ADR-20261024224230). Only such a clone is
// compensated directly; any other compensating clone may hold checkpoint
// artifacts that need their own retirement proofs.
const (
	CloneFailureTargetDeployment = "target_deployment_failed"
	CloneFailureTargetDatabase   = "target_database_failed"
	CloneFailureSourceChanged    = "source_changed_during_capture"
	CloneFailureDeadline         = "clone_deadline_exceeded"
)

// ProjectEnvironmentCloneFailedDirectly reports whether the coordinator ended
// this directly compensable clone itself.
func ProjectEnvironmentCloneFailedDirectly(op ProjectEnvironmentCloneOperation) bool {
	switch op.ErrorCode {
	case CloneFailureTargetDeployment, CloneFailureTargetDatabase, CloneFailureSourceChanged, CloneFailureDeadline:
		return ProjectEnvironmentCloneIsDirectlyCompensable(op)
	}
	return false
}

// ProjectEnvironmentCloneHasManagedDatabases reports whether the operation
// restores managed databases for its target.
func ProjectEnvironmentCloneHasManagedDatabases(op ProjectEnvironmentCloneOperation) bool {
	for _, resource := range op.Resources {
		if resource.Kind == "managed_postgres" {
			return true
		}
	}
	return false
}

func (s *PgStore) directCloneCompensationTx(ctx context.Context, l ProjectEnvironmentCloneLease, work func(context.Context, pgx.Tx, ProjectEnvironmentCloneOperation) error) error {
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
	if op.Status != CloneOperationCompensating || !ProjectEnvironmentCloneFailedDirectly(op) {
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

func (s *PgStore) RetireProjectEnvironmentCloneTargetsForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (int64, error) {
	var active int64
	err := s.directCloneCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
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

// WithdrawProjectEnvironmentCloneDatabaseBindingsForLease withdraws the target
// bindings once no target instance can use their credentials. The credentials
// themselves are roles in the clone's own target databases, which the caller
// deletes next; the returned IDs are those not yet deleted.
func (s *PgStore) WithdrawProjectEnvironmentCloneDatabaseBindingsForLease(ctx context.Context, l ProjectEnvironmentCloneLease) ([]string, error) {
	var databases []string
	err := s.directCloneCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
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
		target := sqlc.WithdrawProjectEnvironmentCloneTargetBindingSecretsParams{OperationID: mustPgUUID(op.ID), Environment: op.TargetEnvironment}
		if err := q.WithdrawProjectEnvironmentCloneTargetBindingSecrets(ctx, tx, target); err != nil {
			return mapErr(err)
		}
		if err := q.WithdrawProjectEnvironmentCloneTargetBindings(ctx, tx, sqlc.WithdrawProjectEnvironmentCloneTargetBindingsParams(target)); err != nil {
			return mapErr(err)
		}
		databases, err = q.ReadProjectEnvironmentCloneLiveTargetDatabases(ctx, tx, sqlc.ReadProjectEnvironmentCloneLiveTargetDatabasesParams{
			OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID)})
		return mapErr(err)
	})
	return databases, err
}

func (s *PgStore) FinishProjectEnvironmentCloneDirectCompensationForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneOperation, error) {
	var finished ProjectEnvironmentCloneOperation
	err := s.directCloneCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
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
		databases, err := q.ReadProjectEnvironmentCloneLiveTargetDatabases(ctx, tx, sqlc.ReadProjectEnvironmentCloneLiveTargetDatabasesParams{
			OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID)})
		if err != nil {
			return mapErr(err)
		}
		if len(databases) != 0 {
			return ErrProjectEnvironmentCloneTargetDatabasesRemaining
		}
		projectSlug, err := q.ReadProjectEnvironmentCloneProjectSlug(ctx, tx, sqlc.ReadProjectEnvironmentCloneProjectSlugParams{
			ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID)})
		if err != nil {
			return mapErr(err)
		}
		// A clone that failed before materialization has no target environment,
		// so it has no invocations or rows to remove either.
		if err := cleanupEnvironmentInvocationsDB(ctx, tx, op.AccountID, op.ProjectID, op.TargetEnvironment); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := q.DeleteProjectEnvironmentCloneTargetPolicies(ctx, tx, sqlc.DeleteProjectEnvironmentCloneTargetPoliciesParams{
			ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment}); err != nil {
			return mapErr(err)
		}
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

var _ ProjectEnvironmentCloneDirectCompensationStore = (*PgStore)(nil)
