package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
)

func (s *PgStore) ChangeHostedTaskRehearsal(ctx context.Context, account, id string, action HostedTaskRehearsalAction, in HostedTaskRehearsalChange) (HostedTaskRehearsalResult, error) {
	var zero HostedTaskRehearsalResult
	if !validHostedTaskRehearsalChange(account, id, action, in) {
		return zero, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.HostedTaskBindingProbeTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.HostedTaskBindingCleanupTimeout)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := toolpublicationdb.New()
	acct := mustPgUUID(account)
	if _, err := q.LockToolPublicationAccount(ctx, tx, acct); err != nil {
		return zero, mapErr(err)
	}
	owner := HostedTaskSourceOwner{KeyID: in.OwnerKeyID}
	if err := lockHostedTaskBindingOwner(ctx, tx, account, owner); err != nil {
		return zero, err
	}

	// Reuse the read-only native freshness checks under the same transaction.
	// These checks retain source, qualification, authority and runtime locks
	// through the plan decision or claim and never start a probe.
	binding, err := changeHostedTaskBindingsPG(ctx, tx, account, id, "get", HostedTaskBindingChange{})
	if err != nil {
		return zero, err
	}
	row, err := q.LockHostedTaskRehearsalPlan(ctx, tx, toolpublicationdb.LockHostedTaskRehearsalPlanParams{AccountID: acct, ID: id})
	exists := err == nil
	if !exists && !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	var record hostedTaskRehearsalRecord
	if exists && (json.Unmarshal(row.Payload, &record) != nil || tasktoolrehearsal.ValidateRecord(record, account, id) != nil ||
		!row.ExpiresAt.Valid || !record.Plan.ExpiresAt.Equal(row.ExpiresAt.Time)) {
		return zero, ErrInvalidArgument
	}
	approvalFresh, err := hostedTaskRehearsalApprovalFreshPG(ctx, tx, account, record.ApprovalKeyID)
	if err != nil {
		return zero, err
	}
	if err := lockHostedTaskBindingOwner(ctx, tx, account, owner); err != nil {
		return zero, err
	}
	now, err := hostedTaskBindingTime(ctx, tx)
	if err != nil {
		return zero, err
	}
	record, acquired, err := applyHostedTaskRehearsal(record, exists, binding.Public, approvalFresh, action, in, now)
	if err != nil {
		// The owner has observed this qualification. Preserve a terminal
		// refresh even when the requested plan operation conflicts.
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return zero, commitErr
			}
		}
		return zero, err
	}
	return saveHostedTaskRehearsalPG(ctx, tx, account, id, record, binding, in.OwnerKeyID, acquired)
}

func hostedTaskRehearsalApprovalFreshPG(ctx context.Context, tx pgx.Tx, account, keyID string) (bool, error) {
	if keyID == "" {
		return true, nil
	}
	err := lockHostedTaskBindingOwner(ctx, tx, account, HostedTaskSourceOwner{KeyID: keyID})
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func saveHostedTaskRehearsalPG(ctx context.Context, tx pgx.Tx, account, id string, r hostedTaskRehearsalRecord, binding HostedTaskBindingRecord, owner string, acquired bool) (HostedTaskRehearsalResult, error) {
	var zero HostedTaskRehearsalResult
	if tasktoolrehearsal.ValidateRecord(r, account, id) != nil {
		return zero, ErrConflict
	}
	body, err := json.Marshal(r)
	if err != nil {
		return zero, err
	}
	requireFresh := r.Plan.Status == tasktoolrehearsal.Planned || r.Plan.Status == tasktoolrehearsal.Approved || acquired
	keys := []pgtype.UUID{mustPgUUID(owner)}
	if requireFresh {
		for _, key := range []string{r.ApprovalKeyID, binding.RequesterKeyID, binding.OwnerKeyID} {
			if key != "" {
				keys = append(keys, mustPgUUID(key))
			}
		}
	}
	n, err := toolpublicationdb.New().SaveHostedTaskRehearsalPlan(ctx, tx, toolpublicationdb.SaveHostedTaskRehearsalPlanParams{
		AccountID: mustPgUUID(account), ID: id, Payload: body, ExpiresAt: pgtype.Timestamptz{Time: r.Plan.ExpiresAt, Valid: true},
		RequireFresh: requireFresh, AuthorityKeys: keys,
	})
	if err != nil {
		return zero, err
	}
	if n != 1 {
		return zero, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return HostedTaskRehearsalResult{Plan: r.Plan, ClaimAcquired: acquired}, nil
}
