package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func operationUUID(id string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, ErrNotFound
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func operationPGDefinition(row sqlc.GetCustomerOperationDefinitionRow) (OperationDefinition, error) {
	def := OperationDefinition{OperationDefinitionResponse: api.OperationDefinitionResponse{ID: row.ID, AppID: row.AppID, Scope: row.Scope,
		Revision: row.Revision, DeploymentID: row.DeploymentID, ReleaseID: row.ReleaseID, CreatedAt: row.CreatedAt.Time}, AccountID: row.AccountID}
	if err := json.Unmarshal(row.Spec, &def.Spec); err != nil {
		return OperationDefinition{}, fmt.Errorf("state: decode operation definition: %w", err)
	}
	return def, nil
}

func operationPGRecord(raw []byte) (Operation, error) {
	var op Operation
	if err := json.Unmarshal(raw, &op); err != nil {
		return Operation{}, fmt.Errorf("state: decode operation: %w", err)
	}
	return op, nil
}

func (s *PgStore) OperationDefinitionByID(ctx context.Context, accountID, id string) (OperationDefinition, error) {
	a, err := operationUUID(accountID)
	if err != nil {
		return OperationDefinition{}, err
	}
	i, err := operationUUID(id)
	if err != nil {
		return OperationDefinition{}, err
	}
	row, err := sqlc.New().GetCustomerOperationDefinition(ctx, s.pool, sqlc.GetCustomerOperationDefinitionParams{ID: i, AccountID: a})
	if err != nil {
		return OperationDefinition{}, fmt.Errorf("state: read operation definition: %w", mapErr(err))
	}
	return operationPGDefinition(row)
}

func (s *PgStore) OperationDefinitionForDeployment(ctx context.Context, accountID, appID, deploymentID, name string) (OperationDefinition, error) {
	a, err := operationUUID(accountID)
	if err != nil {
		return OperationDefinition{}, err
	}
	app, err := operationUUID(appID)
	if err != nil {
		return OperationDefinition{}, err
	}
	dep, err := operationUUID(deploymentID)
	if err != nil {
		return OperationDefinition{}, err
	}
	row, err := sqlc.New().GetCustomerOperationDefinitionForDeployment(ctx, s.pool, sqlc.GetCustomerOperationDefinitionForDeploymentParams{AccountID: a, AppID: app, DeploymentID: dep, Name: name})
	if err != nil {
		return OperationDefinition{}, fmt.Errorf("state: read deployment operation definition: %w", mapErr(err))
	}
	return operationPGDefinition(sqlc.GetCustomerOperationDefinitionRow(row))
}

func (s *PgStore) PutOperationDefinition(ctx context.Context, def OperationDefinition) (OperationDefinition, error) {
	account, err := operationUUID(def.AccountID)
	if err != nil {
		return OperationDefinition{}, err
	}
	app, err := operationUUID(def.AppID)
	if err != nil {
		return OperationDefinition{}, err
	}
	deployment, err := operationUUID(def.DeploymentID)
	if err != nil {
		return OperationDefinition{}, err
	}
	if def.ReleaseID != "" {
		_, dep, err := s.ResolveProjectRelease(ctx, def.AppID, def.Scope, def.ReleaseID)
		if err != nil || dep != def.DeploymentID {
			return OperationDefinition{}, ErrConflict
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperationDefinition{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	plan, err := q.LockCustomerOperationAccount(ctx, tx, account)
	if err != nil {
		return OperationDefinition{}, mapErr(err)
	}
	limits := api.MustLimitsFor(api.Plan(plan))
	contract, err := operations.Compile(def.Spec, limits.Operations)
	if err != nil {
		return OperationDefinition{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	def.Spec, def.Revision = contract.Spec, contract.Revision
	scope, err := q.CustomerOperationDeploymentScope(ctx, tx, sqlc.CustomerOperationDeploymentScopeParams{DeploymentID: deployment, AppID: app, AccountID: account})
	if err != nil {
		return OperationDefinition{}, mapErr(err)
	}
	if scope != def.Scope {
		return OperationDefinition{}, ErrNotFound
	}
	if def.Spec.CompletionWebhookID != "" {
		hook, err := operationUUID(def.Spec.CompletionWebhookID)
		if err != nil {
			return OperationDefinition{}, err
		}
		enabled, err := q.CustomerOperationCompletionWebhook(ctx, tx, sqlc.CustomerOperationCompletionWebhookParams{ID: hook, AccountID: account, AppID: app})
		if err != nil {
			return OperationDefinition{}, mapErr(err)
		}
		if !enabled {
			return OperationDefinition{}, ErrNotFound
		}
	}

	existing, err := q.GetCustomerOperationDefinitionForDeployment(ctx, tx, sqlc.GetCustomerOperationDefinitionForDeploymentParams{AppID: app, AccountID: account, DeploymentID: deployment, Name: def.Spec.Name})
	if err == nil {
		if existing.Revision != def.Revision || existing.ReleaseID != def.ReleaseID {
			return OperationDefinition{}, ErrConflict
		}
		return operationPGDefinition(sqlc.GetCustomerOperationDefinitionRow(existing))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperationDefinition{}, err
	}
	count, err := q.CountCustomerOperationDefinitionNames(ctx, tx, sqlc.CountCustomerOperationDefinitionNamesParams{AppID: app, Scope: def.Scope})
	if err != nil {
		return OperationDefinition{}, err
	}
	exists, err := q.CustomerOperationDefinitionNameExists(ctx, tx, sqlc.CustomerOperationDefinitionNameExistsParams{AppID: app, Scope: def.Scope, Name: def.Spec.Name})
	if err != nil {
		return OperationDefinition{}, err
	}
	if !exists && count >= int64(limits.Operations.DefinitionsPerApp) {
		return OperationDefinition{}, NewOperationLimitError("definitions_per_app", int64(limits.Operations.DefinitionsPerApp), int64(count)+1)
	}
	if def.ID == "" {
		def.ID = newOperationID()
	}
	id, err := operationUUID(def.ID)
	if err != nil {
		return OperationDefinition{}, err
	}
	spec, err := json.Marshal(def.Spec)
	if err != nil {
		return OperationDefinition{}, err
	}
	row, err := q.InsertCustomerOperationDefinition(ctx, tx, sqlc.InsertCustomerOperationDefinitionParams{ID: id, AccountID: account, AppID: app, Scope: def.Scope, Name: def.Spec.Name, Revision: def.Revision, DeploymentID: deployment, ReleaseID: def.ReleaseID, Spec: spec})
	if err != nil {
		return OperationDefinition{}, fmt.Errorf("state: insert operation definition: %w", mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationDefinition{}, err
	}
	return operationPGDefinition(sqlc.GetCustomerOperationDefinitionRow(row))
}

func (s *PgStore) AdmitOperation(ctx context.Context, admission OperationAdmission) (Operation, bool, error) {
	account, err := operationUUID(admission.AccountID)
	if err != nil {
		return Operation{}, false, err
	}
	definition, err := operationUUID(admission.DefinitionID)
	if err != nil {
		return Operation{}, false, err
	}
	tenant, err := operationUUID(admission.PlatformTenantID)
	if err != nil {
		return Operation{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	plan, err := q.LockCustomerOperationAccount(ctx, tx, account)
	if err != nil {
		return Operation{}, false, mapErr(err)
	}
	row, err := q.GetCustomerOperationDefinition(ctx, tx, sqlc.GetCustomerOperationDefinitionParams{ID: definition, AccountID: account})
	if err != nil {
		return Operation{}, false, mapErr(err)
	}
	def, err := operationPGDefinition(row)
	if err != nil {
		return Operation{}, false, err
	}
	app, _ := operationUUID(def.AppID)
	deployment, _ := operationUUID(def.DeploymentID)
	if _, err := q.CustomerOperationDeploymentScope(ctx, tx, sqlc.CustomerOperationDeploymentScopeParams{DeploymentID: deployment, AppID: app, AccountID: account}); err != nil {
		return Operation{}, false, mapErr(err)
	}

	status, err := q.LockCustomerOperationTenant(ctx, tx, sqlc.LockCustomerOperationTenantParams{TenantID: tenant, AccountID: account})
	if err != nil {
		return Operation{}, false, mapErr(err)
	}
	limits := api.MustLimitsFor(api.Plan(plan))
	now := time.Now().UTC()
	op, inv, key, fingerprint, err := prepareOperationAdmission(def, admission, limits, now)
	if err != nil {
		return Operation{}, false, err
	}
	receipt, err := q.GetCustomerOperationIdempotency(ctx, tx, sqlc.GetCustomerOperationIdempotencyParams{ScopeDigest: key, AccountID: account})
	if err == nil && receipt.ExpiresAt.Time.After(now) {
		if receipt.Fingerprint != fingerprint {
			return Operation{}, false, ErrOperationInputConflict
		}
		id, _ := operationUUID(receipt.OperationID)
		raw, err := q.GetCustomerOperation(ctx, tx, sqlc.GetCustomerOperationParams{ID: id, AccountID: account, TenantID: admission.PlatformTenantID})
		if errors.Is(err, pgx.ErrNoRows) {
			return Operation{}, false, ErrOperationExpired
		}
		if err != nil {
			return Operation{}, false, err
		}
		original, err := operationPGRecord(raw)
		if err != nil {
			return Operation{}, false, err
		}
		if !original.ExpiresAt.After(now) {
			return Operation{}, false, ErrOperationExpired
		}
		return original, false, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, false, err
	}
	if !limits.Operations.Allowed {
		return Operation{}, false, NewOperationLimitError("plan_admission", 0, 1)
	}
	if status != string(PlatformTenantActive) {
		return Operation{}, false, ErrPlatformTenantSuspended
	}
	if admission.ReleaseID != "" {
		_, deployment, err := s.ResolveProjectRelease(ctx, def.AppID, def.Scope, admission.ReleaseID)
		if err != nil {
			return Operation{}, false, err
		}
		if deployment != def.DeploymentID || (def.ReleaseID != "" && def.ReleaseID != admission.ReleaseID) {
			return Operation{}, false, ErrConflict
		}
	}
	deploymentStatus, err := q.LockCustomerOperationDeployment(ctx, tx, sqlc.LockCustomerOperationDeploymentParams{DeploymentID: deployment, AppID: app, AccountID: account})
	if err != nil {
		return Operation{}, false, mapErr(err)
	}
	if deploymentStatus != string(DeployLive) {
		return Operation{}, false, ErrConflict
	}
	if err := validateNewOperationInput(def, admission.Input, limits); err != nil {
		return Operation{}, false, err
	}
	pending, err := q.CountPendingCustomerOperations(ctx, tx, account)
	if err != nil {
		return Operation{}, false, err
	}
	if pending >= int64(limits.Operations.PendingPerAccount) {
		return Operation{}, false, NewOperationLimitError("pending_per_account", int64(limits.Operations.PendingPerAccount), int64(pending)+1)
	}
	if _, err := enqueueInvocationRow(ctx, tx, inv); err != nil {
		return Operation{}, false, err
	}
	if err := insertOperationPG(ctx, tx, q, op, key, fingerprint, limits.Operations); err != nil {
		return Operation{}, false, err
	}
	if err := operationPinsTx(ctx, tx, op); err != nil {
		return Operation{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, false, fmt.Errorf("state: commit operation admission: %w", err)
	}
	return op, true, nil
}

func insertOperationPG(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, op Operation, key, fingerprint string, limits api.OperationPlanLimits) error {
	id, _ := operationUUID(op.ID)
	account, _ := operationUUID(op.AccountID)
	app, _ := operationUUID(op.AppID)
	tenant, _ := operationUUID(op.PlatformTenantID)
	definition, _ := operationUUID(op.DefinitionID)
	invocation, _ := operationUUID(op.CurrentInvocationID)
	record, err := json.Marshal(op)
	if err != nil {
		return err
	}
	if err := q.SetCustomerOperationExecutionIdentity(ctx, tx, sqlc.SetCustomerOperationExecutionIdentityParams{InvocationID: invocation, OperationID: id}); err != nil {
		return err
	}
	if err := q.InsertCustomerOperation(ctx, tx, sqlc.InsertCustomerOperationParams{ID: id, AccountID: account, AppID: app, TenantID: tenant, DefinitionID: definition, InvocationID: invocation, State: string(op.State), Record: record, ExpiresAt: pgtype.Timestamptz{Time: op.ExpiresAt, Valid: true}, CreatedAt: pgtype.Timestamptz{Time: op.CreatedAt, Valid: true}}); err != nil {
		return fmt.Errorf("state: insert operation: %w", err)
	}
	if err := q.InsertCustomerOperationExecution(ctx, tx, sqlc.InsertCustomerOperationExecutionParams{OperationID: id, Generation: int32(op.Generation), InvocationID: invocation}); err != nil {
		return err
	}
	if err := q.InsertCustomerOperationEvent(ctx, tx, sqlc.InsertCustomerOperationEventParams{OperationID: id, Sequence: 1, EventType: "accepted", ExecutionID: invocation, Data: initialOperationEvent(op).Data, CreatedAt: pgtype.Timestamptz{Time: op.CreatedAt, Valid: true}}); err != nil {
		return err
	}
	return q.PutCustomerOperationIdempotency(ctx, tx, sqlc.PutCustomerOperationIdempotencyParams{ScopeDigest: key, AccountID: account, AppID: app, OperationID: id, Fingerprint: fingerprint, ExpiresAt: pgtype.Timestamptz{Time: op.CreatedAt.Add(time.Duration(limits.IdempotencyRetentionSeconds) * time.Second), Valid: true}})
}

func (s *PgStore) OperationByID(ctx context.Context, accountID, tenantID, id string) (Operation, error) {
	a, err := operationUUID(accountID)
	if err != nil {
		return Operation{}, err
	}
	i, err := operationUUID(id)
	if err != nil {
		return Operation{}, err
	}
	raw, err := sqlc.New().GetCustomerOperation(ctx, s.pool, sqlc.GetCustomerOperationParams{ID: i, AccountID: a, TenantID: tenantID})
	if err != nil {
		return Operation{}, fmt.Errorf("state: read operation: %w", mapErr(err))
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return Operation{}, err
	}
	if !op.ExpiresAt.After(time.Now().UTC()) {
		return Operation{}, ErrOperationExpired
	}
	if id := op.CompletionDelivery.DeliveryID; id != "" {
		delivery, err := s.AppWebhookDeliveryByID(ctx, id)
		if errors.Is(err, ErrNotFound) {
			op.CompletionDelivery.State = "delivery_expired"
		} else if err != nil {
			return Operation{}, err
		} else {
			op.CompletionDelivery = operationDeliveryProjection(delivery)
		}
	}
	return op, nil
}

func (s *PgStore) OperationEvents(ctx context.Context, accountID, tenantID, id string, after int64, limit int) (api.OperationEventsResponse, error) {
	if after < 0 || limit < 1 || limit > api.OperationEventsPageMax {
		return api.OperationEventsResponse{}, ErrInvalidArgument
	}
	op, err := s.OperationByID(ctx, accountID, tenantID, id)
	if err != nil {
		return api.OperationEventsResponse{}, err
	}
	page := api.OperationEventsResponse{Events: []api.OperationEvent{}, LatestSequence: op.LatestSequence}
	if after > op.LatestSequence || !op.EventExpiresAt.After(time.Now().UTC()) {
		page.ResyncRequired = true
		return page, nil
	}
	i, _ := operationUUID(id)
	rows, err := sqlc.New().ReadCustomerOperationEvents(ctx, s.pool, sqlc.ReadCustomerOperationEventsParams{OperationID: i, AfterSequence: after, LatestSequence: op.LatestSequence, PageLimit: int32(limit)})
	if err != nil {
		return api.OperationEventsResponse{}, fmt.Errorf("state: read operation events: %w", err)
	}
	for _, row := range rows {
		page.Events = append(page.Events, api.OperationEvent{OperationID: row.OperationID, Sequence: row.Sequence, Type: row.EventType, ExecutionID: row.ExecutionID, Attempt: int(row.Attempt), Data: row.Data, CreatedAt: row.CreatedAt.Time})
	}
	return page, nil
}

var _ OperationStore = (*PgStore)(nil)
