package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func (s *PgStore) ControlWorkflowRecoveryPolicy(ctx context.Context, account, app string, r api.ControlWorkflowRecoveryPolicyRequest) (api.WorkflowRecoveryPolicyResponse, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	if err := r.Validate(); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	actor := recoveryAuditActor(ctx, "policy_"+r.Operation)
	paused := pgtype.Timestamptz{}
	if r.ExpectedPausedAt != nil {
		paused = pgtypeFromTime(*r.ExpectedPausedAt)
	}
	row, err := sqlc.New().WorkflowRecoveryPolicyControl(ctx, s.pool, sqlc.WorkflowRecoveryPolicyControlParams{ControlID: mustPgUUID(uuid.NewString()), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ExpectedVersion: r.ExpectedVersion, ExpectedUpdatedAt: pgtypeFromTime(r.ExpectedUpdatedAt), ExpectedPausedAt: paused, Operation: r.Operation, Reason: r.Reason, NowAt: pgtypeFromTime(time.Now().UTC()), ActorKind: actor.Kind, ActorID: actor.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		p, readErr := s.GetWorkflowRecoveryPolicy(ctx, account, app)
		if readErr != nil {
			return p, readErr
		}
		if recoveryControlMatches(p, r) && (!p.Policy.Enabled || !api.WorkflowRecoveryPolicyAutomaticMode(p.Policy.Mode)) {
			return p, ErrEventRecoveryQuery
		}
		return api.WorkflowRecoveryPolicyResponse{}, ErrWorkflowRecoveryPolicyConflict
	}
	if err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, fmt.Errorf("control recovery policy: %w", err)
	}
	return pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification)
}
func (s *PgStore) GetWorkflowRecoveryAutomaticQueue(ctx context.Context, account, app string) (api.WorkflowRecoveryAutomaticQueue, error) {
	out := api.WorkflowRecoveryAutomaticQueue{ObservedAt: time.Now().UTC(), Entries: []api.WorkflowRecoveryAutomaticQueueEntry{}}
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("begin automatic queue snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, _, err = pgWorkflowRecoveryAutomaticQueue(ctx, sqlc.New(), tx, account, app, out.ObservedAt, false)
	if err != nil {
		return out, err
	}
	if err = pgRecoveryQueueEvidence(ctx, sqlc.New(), tx, account, app, &out); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit automatic queue snapshot: %w", err)
	}
	return out, nil
}

func pgWorkflowRecoveryAutomaticQueue(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app string, now time.Time, allPending bool) (api.WorkflowRecoveryAutomaticQueue, *time.Time, error) {
	out := api.WorkflowRecoveryAutomaticQueue{ObservedAt: now, Entries: []api.WorkflowRecoveryAutomaticQueueEntry{}}
	if _, err := q.WorkflowRecoveryAutomaticQueueApp(ctx, tx, sqlc.WorkflowRecoveryAutomaticQueueAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); errors.Is(err, pgx.ErrNoRows) {
		return out, nil, ErrNotFound
	} else if err != nil {
		return out, nil, fmt.Errorf("scope automatic queue: %w", err)
	}
	row, err := q.WorkflowRecoveryPolicyGet(ctx, tx, sqlc.WorkflowRecoveryPolicyGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if errors.Is(err, pgx.ErrNoRows) {
		out.Policy = defaultWorkflowRecoveryPolicy(canonicalMemUUID(app))
		return out, nil, nil
	}
	if err != nil {
		return out, nil, fmt.Errorf("read queue policy: %w", err)
	}
	out.Policy, err = pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification)
	if err != nil {
		return out, nil, err
	}
	raw, err := q.WorkflowRecoveryAutomaticQueue(ctx, tx, sqlc.WorkflowRecoveryAutomaticQueueParams{AppID: mustPgUUID(app), PolicyVersion: row.Version, SinceAt: pgtypeFromTime(out.ObservedAt.Add(-time.Hour)), MaxEntries: api.EventRecoveryHistoryPageMax, AllPending: allPending})
	if err != nil {
		return out, nil, fmt.Errorf("read automatic queue: %w", err)
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, nil, fmt.Errorf("decode automatic queue: %w", err)
	}
	var last struct {
		At *time.Time `json:"last_execution_at"`
	}
	if err = json.Unmarshal(raw, &last); err != nil {
		return out, nil, fmt.Errorf("decode queue cooldown: %w", err)
	}
	recoveryQueueFinish(&out, last.At)
	incident, err := q.WorkflowRecoveryHealthIncidentGet(ctx, tx, mustPgUUID(app))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, nil, fmt.Errorf("read recovery health incident: %w", err)
	}
	if err == nil {
		if err = json.Unmarshal(incident, &out.HealthIncident); err != nil {
			return out, nil, fmt.Errorf("decode recovery health incident: %w", err)
		}
	}
	return out, last.At, nil
}
