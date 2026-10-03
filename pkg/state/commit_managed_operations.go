// adr: 430
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// WorkID selects the durable work identity without treating a managed operation
// as an invocation. Legacy receipts keep their original invocation identifier.
func (r CommitReceipt) WorkID() string {
	if r.OperationID != "" {
		return r.OperationID
	}
	return r.InvocationID
}

func validCommitOperationPolicy(policy ExclusiveWorkPolicy, app string, allowTenant bool) bool {
	scope := "account"
	if allowTenant {
		scope = "platform_tenant"
	}
	return !policy.Retired && policy.Policy.Scope == scope && policy.Policy.EnvironmentID == "" &&
		policy.Policy.Contention == "queue" && slices.Contains(policy.Policy.MemberAppIDs, app)
}

func (s *PgStore) setCommitSourceEnabled(ctx context.Context, account, id string, enabled bool) (CommitSource, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitSource{}, fmt.Errorf("state: begin Commit source update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner := &exclusivePostgresTx{ctx: ctx, db: tx, q: sqlc.New()}
	if err := owner.lockAccount(account); err != nil {
		return CommitSource{}, exclusiveSQLError(err)
	}
	row, err := owner.q.CommitSourceForManagedAdmission(ctx, tx, sqlc.CommitSourceForManagedAdmissionParams{AccountID: account, SourceID: id})
	if err != nil {
		return CommitSource{}, exclusiveSQLError(err)
	}
	if enabled && row.OperationPolicy != "" {
		policy, err := owner.policy(account, row.OperationPolicy)
		if err != nil {
			return CommitSource{}, exclusiveSQLError(err)
		}
		if !validCommitOperationPolicy(policy, row.CAppID, row.AllowTenantSelection) || (row.PlatformTenantRequired && !row.AllowTenantSelection) {
			return CommitSource{}, ErrInvalidArgument
		}
	}
	if err := owner.q.SetCommitSourceEnabled(ctx, tx, sqlc.SetCommitSourceEnabledParams{AccountID: account, SourceID: id, Enabled: enabled}); err != nil {
		return CommitSource{}, fmt.Errorf("state: update Commit source: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CommitSource{}, fmt.Errorf("state: commit source update: %w", err)
	}
	return s.CommitSourceByID(ctx, account, id)
}

// CreateManagedCommitSource binds one immutable application/policy destination.
// The account lock serializes this validation with policy changes and retirement.
func (s *PgStore) CreateManagedCommitSource(ctx context.Context, src CommitSource) (CommitSource, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return src, fmt.Errorf("state: begin managed Commit source: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner := &exclusivePostgresTx{ctx: ctx, db: tx, q: sqlc.New()}
	if err := owner.lockAccount(src.AccountID); err != nil {
		return src, exclusiveSQLError(err)
	}
	policy, err := owner.policy(src.AccountID, src.OperationPolicy)
	if err != nil {
		return src, exclusiveSQLError(err)
	}
	if !validCommitOperationPolicy(policy, src.AppID, src.AllowTenantSelection) {
		return src, ErrInvalidArgument
	}
	if _, err := owner.appScope(src.AccountID, src.AppID); err != nil {
		return src, exclusiveSQLError(err)
	}
	row, err := owner.q.CommitManagedSource(ctx, tx, sqlc.CommitManagedSourceParams{
		AccountID: src.AccountID, AppID: src.AppID, Name: src.Name, OperationPolicy: src.OperationPolicy, ContractVersion: int32(src.ContractVersion), AllowTenantSelection: src.AllowTenantSelection,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return src, ErrConflict
	}
	if err != nil {
		return src, fmt.Errorf("state: bind managed Commit source: %w", err)
	}
	src.ID, src.Enabled = row.ID, row.Enabled
	if err := tx.Commit(ctx); err != nil {
		return src, fmt.Errorf("state: commit managed Commit source: %w", err)
	}
	return src, nil
}

// AcceptCommitOperation couples the receipt to the same transaction as managed
// Operations admission. Replay precedes pause, policy, current-version and quota
// checks, so an accepted source/event never reopens after configuration changes.
func (s *PgStore) AcceptCommitOperation(ctx context.Context, account, source, event, kind string, data json.RawMessage, inv Invocation, routing ...*api.CommitRouting) (CommitReceipt, error) {
	if len(routing) > 1 {
		return CommitReceipt{}, ErrInvalidArgument
	}
	var selected *api.CommitRouting
	if len(routing) == 1 {
		selected = routing[0]
	}
	routingJSON, err := commitwork.NormalizeRouting(selected)
	if err != nil {
		return CommitReceipt{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitReceipt{}, fmt.Errorf("state: begin Commit operation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner := &exclusivePostgresTx{ctx: ctx, db: tx, q: sqlc.New()}
	if err := owner.lockAccount(account); err != nil {
		return CommitReceipt{}, exclusiveSQLError(err)
	}
	src, err := owner.q.CommitSourceForManagedAdmission(ctx, tx, sqlc.CommitSourceForManagedAdmissionParams{AccountID: account, SourceID: source})
	if err != nil {
		return CommitReceipt{}, exclusiveSQLError(err)
	}
	prior, err := owner.q.CommitManagedReceiptReplay(ctx, tx, sqlc.CommitManagedReceiptReplayParams{
		AccountID: account, SourceID: source, EventID: event, EventType: kind, Payload: data, Routing: routingJSON,
	})
	if err == nil {
		if !prior.Matches.Valid || !prior.Matches.Bool {
			return CommitReceipt{}, ErrConflict
		}
		r := CommitReceipt{ID: prior.ID, SourceID: prior.SourceID, EventID: prior.EventID,
			InvocationID: prior.InvocationID, OperationID: prior.OperationID, AcceptedAt: prior.AcceptedAt.Time}
		r.OperationURL = "/v1/operations/" + r.WorkID()
		return r, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CommitReceipt{}, fmt.Errorf("state: recover Commit operation receipt: %w", err)
	}
	if !src.Enabled || src.OperationPolicy == "" || (src.PlatformTenantRequired && !src.AllowTenantSelection) ||
		inv.AccountID != account || inv.AppID != src.CAppID {
		return CommitReceipt{}, ErrInvalidArgument
	}
	policy, err := owner.policy(account, src.OperationPolicy)
	if err != nil {
		return CommitReceipt{}, exclusiveSQLError(err)
	}
	if !validCommitOperationPolicy(policy, src.CAppID, src.AllowTenantSelection) {
		return CommitReceipt{}, ErrInvalidArgument
	}
	if (src.ContractVersion == 1 && selected != nil) || (src.ContractVersion == 2 && selected == nil) {
		return CommitReceipt{}, ErrInvalidArgument
	}
	var tenantID string
	if selected != nil {
		var normalized api.CommitRouting
		if err := json.Unmarshal(routingJSON, &normalized); err != nil {
			return CommitReceipt{}, ErrInvalidArgument
		}
		selected = &normalized
		tenantID = selected.PlatformTenantID
	}
	if src.AllowTenantSelection != (tenantID != "") {
		return CommitReceipt{}, ErrInvalidArgument
	}
	if inv.PlatformTenantID != "" {
		id, err := uuid.Parse(inv.PlatformTenantID)
		if err != nil || id.String() != tenantID {
			return CommitReceipt{}, ErrInvalidArgument
		}
	}
	if tenantID != "" {
		if err := owner.tenantScope(account, tenantID); err != nil {
			return CommitReceipt{}, err
		}
		if _, err := owner.q.CommitTenantAppScope(ctx, tx, sqlc.CommitTenantAppScopeParams{AccountID: account, AppID: src.CAppID, TenantID: tenantID}); err != nil {
			return CommitReceipt{}, exclusiveSQLError(err)
		}
	}
	request, err := json.Marshal(api.InvokeRequest{Method: inv.Method, Path: inv.Path, Payload: inv.Payload, Headers: inv.Headers})
	if err != nil {
		return CommitReceipt{}, fmt.Errorf("state: encode Commit operation request: %w", err)
	}
	key, err := json.Marshal("gregale.commit." + source)
	if err != nil {
		return CommitReceipt{}, fmt.Errorf("state: encode Commit operation key: %w", err)
	}
	idempotencyKey := event
	if selected != nil {
		key = selected.Key
		idempotencyKey = source + "/" + event
	}
	operation, joined, err := admitExclusiveTransaction(owner, ExclusiveAdmission{
		AccountID: account, AppID: src.CAppID, PlatformTenantID: tenantID, PolicyName: src.OperationPolicy,
		Key: key, Request: request, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return CommitReceipt{}, exclusiveSQLError(err)
	}
	if joined {
		return CommitReceipt{}, fmt.Errorf("state: Commit queue policy unexpectedly joined an operation")
	}
	row, err := owner.q.CommitManagedReceipt(ctx, tx, sqlc.CommitManagedReceiptParams{
		ID: uuid.NewString(), AccountID: account, SourceID: source, EventID: event, EventType: kind, Payload: data,
		OperationID: operation.ID, OperationState: commitManagedState(operation.State), Routing: routingJSON,
		CompletedAt: exclusivePGTime(operation.CompletedAt),
	})
	if err != nil {
		return CommitReceipt{}, fmt.Errorf("state: insert managed Commit receipt: %w", err)
	}
	receipt := CommitReceipt{ID: row.ID, SourceID: row.SourceID, EventID: row.EventID,
		OperationID: row.OperationID, AcceptedAt: row.AcceptedAt.Time, OperationURL: "/v1/operations/" + row.OperationID}
	if err := tx.Commit(ctx); err != nil {
		return CommitReceipt{}, fmt.Errorf("state: commit managed Commit admission: %w", err)
	}
	return receipt, nil
}

func commitManagedState(state string) string {
	switch state {
	case "pending":
		return "accepted"
	case "running", "completed", "cancelled", "failed":
		return state
	case "expired":
		return "failed"
	default:
		return "unknown"
	}
}
