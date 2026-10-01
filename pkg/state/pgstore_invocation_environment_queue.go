package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentQueueInvocationStore = (*PgStore)(nil)

func readInvocationEnvironmentQueueAdmissionDB(ctx context.Context, db sqlc.DBTX, id string) (InvocationEnvironmentQueueAdmission, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		return InvocationEnvironmentQueueAdmission{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadInvocationEnvironmentQueueAdmission(ctx, db, mustPgUUID(id))
	if err != nil {
		return InvocationEnvironmentQueueAdmission{}, mapErr(err)
	}
	return InvocationEnvironmentQueueAdmission{InvocationID: pgUUIDString(row.InvocationID), EnvironmentID: pgUUIDString(row.EnvironmentID),
		AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), ConsumerID: pgUUIDString(row.ConsumerID), RuntimeSetID: pgUUIDString(row.RuntimeSetID),
		DeploymentID: pgUUIDString(row.DeploymentID), WorkloadSpecID: pgUUIDString(row.WorkloadSpecID), PinHash: row.PinHash, SettingsHash: row.SettingsHash,
		DefinitionHash: row.DefinitionHash, QueueName: row.QueueName, AdmittedAt: row.AdmittedAt.Time.UTC()}, nil
}

func (s *PgStore) InvocationEnvironmentQueueAdmission(ctx context.Context, id string) (InvocationEnvironmentQueueAdmission, error) {
	return readInvocationEnvironmentQueueAdmissionDB(ctx, s.pool, id)
}

func (s *PgStore) EnqueueProjectEnvironmentQueueInvocation(ctx context.Context, accountID, projectID, deploymentID, name string, inv Invocation) (Invocation, error) {
	set, err := s.ProjectEnvironmentQueueConsumersForDeployment(ctx, accountID, projectID, deploymentID)
	if err != nil {
		return Invocation{}, err
	}
	inv, err = prepareEnvironmentQueueInvocation(ctx, s, set, name, inv)
	if err != nil {
		return Invocation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invocation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err := q.LockEnvironmentQueueProducer(ctx, tx, sqlc.LockEnvironmentQueueProducerParams{
		EnvironmentID: mustPgUUID(set.EnvironmentID), AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), AppID: mustPgUUID(set.AppID),
	}); err != nil {
		return Invocation{}, mapErr(err)
	}
	current, err := projectEnvironmentQueueConsumersDB(ctx, tx, accountID, projectID, deploymentID, false, true)
	if err != nil {
		return Invocation{}, err
	}
	consumer, err := queueConsumerByName(current, name)
	if err != nil {
		return Invocation{}, err
	}
	if inv.ID == "" {
		inv.ID = uuid.NewString()
	}
	owner := queueAdmissionForInvocation(set, consumer, inv)
	if _, err := validateQueueAdmission(owner, inv, current); err != nil {
		return Invocation{}, err
	}
	plan, err := q.ReadEnvironmentQueueProducerPlan(ctx, tx, mustPgUUID(accountID))
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	depth, err := q.CountEnvironmentQueueProducerDepth(ctx, tx, sqlc.CountEnvironmentQueueProducerDepthParams{EnvironmentID: mustPgUUID(current.EnvironmentID), AppID: mustPgUUID(current.AppID)})
	if err != nil {
		return Invocation{}, err
	}
	if err := environmentQueueProducerCapacity(api.Plan(plan), depth); err != nil {
		return Invocation{}, err
	}
	out, err := enqueueInvocationRow(ctx, tx, inv)
	if err != nil {
		return Invocation{}, err
	}
	count, err := sqlc.New().CreateInvocationEnvironmentQueueAdmission(ctx, tx, sqlc.CreateInvocationEnvironmentQueueAdmissionParams{
		InvocationID: mustPgUUID(inv.ID), EnvironmentID: mustPgUUID(owner.EnvironmentID), AccountID: mustPgUUID(owner.AccountID), AppID: mustPgUUID(owner.AppID),
		ConsumerID: mustPgUUID(owner.ConsumerID), RuntimeSetID: mustPgUUID(owner.RuntimeSetID), DeploymentID: mustPgUUID(owner.DeploymentID),
		WorkloadSpecID: mustPgUUID(owner.WorkloadSpecID), PinHash: owner.PinHash, SettingsHash: owner.SettingsHash, DefinitionHash: owner.DefinitionHash, QueueName: owner.QueueName,
		AdmittedAt: pgtype.Timestamptz{Time: owner.AdmittedAt, Valid: true},
	})
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	if count != 1 {
		return Invocation{}, ErrInvocationEnvironmentWorkIsolation
	}
	valid, err := sqlc.New().ValidateInvocationEnvironmentQueuePin(ctx, tx, sqlc.ValidateInvocationEnvironmentQueuePinParams{InvocationID: mustPgUUID(inv.ID), RequireLive: true})
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	if !valid {
		return Invocation{}, ErrInvocationEnvironmentWorkIsolation
	}
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, err
	}
	out.EnvironmentID, out.CreatedAt = inv.EnvironmentID, inv.CreatedAt
	return out, nil
}

// Caller holds the environment ownership lock. Retired pins remain valid for
// idle deletion; claim and delivery require a live, unexpired selected pin.
func validateInvocationQueueClaimDB(ctx context.Context, db sqlc.DBTX, id string, requireLive bool) (bool, error) {
	owner, err := readInvocationEnvironmentQueueAdmissionDB(ctx, db, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	q := sqlc.New()
	row, err := q.ReadEnvironmentQueueInvocation(ctx, db, mustPgUUID(id))
	if err != nil {
		return true, mapErr(err)
	}
	if !queueOwnerMatchesEnvelope(owner, invocationFromSQLC(row)) {
		return true, ErrInvocationEnvironmentWorkIsolation
	}
	projectID, err := q.ReadEnvironmentQueueAdmissionProject(ctx, db, mustPgUUID(owner.EnvironmentID))
	if err != nil {
		return true, mapErr(err)
	}
	set, err := projectEnvironmentQueueConsumersDB(ctx, db, owner.AccountID, pgUUIDString(projectID), owner.DeploymentID, false, requireLive)
	if err != nil {
		return true, ErrInvocationEnvironmentWorkIsolation
	}
	if _, err := validateQueueAdmission(owner, invocationFromSQLC(row), set); err != nil {
		return true, err
	}
	valid, err := q.ValidateInvocationEnvironmentQueuePin(ctx, db, sqlc.ValidateInvocationEnvironmentQueuePinParams{InvocationID: mustPgUUID(id), RequireLive: requireLive})
	if err != nil {
		return true, mapErr(err)
	}
	if !valid {
		return true, ErrInvocationEnvironmentWorkIsolation
	}
	return true, nil
}

func queueClaimCapacityDB(ctx context.Context, db sqlc.DBTX, id string) error {
	capacity, err := sqlc.New().EnvironmentQueueClaimCapacity(ctx, db, mustPgUUID(id))
	if errors.Is(mapErr(err), ErrNotFound) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	if capacity.Active >= int64(capacity.MaxConcurrency) {
		return ErrQuotaExceeded
	}
	return nil
}
