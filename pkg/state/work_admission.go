package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func prepareKeyedInvocation(inv Invocation, policy workpolicy.Policy, key string, fairness []string) (Invocation, error) {
	if inv.DeploymentScope != "" && api.ValidateScope(inv.DeploymentScope) != nil {
		return Invocation{}, ErrInvalidArgument
	}
	if err := policy.Validate(); err != nil {
		return Invocation{}, err
	}
	digest, err := workpolicy.DigestKey(key)
	if err != nil {
		return Invocation{}, err
	}
	fair, err := workFairnessDigest(policy, key, fairness)
	if err != nil {
		return Invocation{}, err
	}
	if inv.ID == "" {
		inv.ID = uuid.NewString()
	} else if _, err := uuid.Parse(inv.ID); err != nil {
		return Invocation{}, err
	}
	if inv.AppID == "" || inv.AccountID == "" {
		return Invocation{}, fmt.Errorf("state: keyed invocation requires app and account")
	}
	if inv.State != "" && inv.State != InvocationPending {
		return Invocation{}, fmt.Errorf("state: keyed invocation must start pending")
	}
	inv.WorkPolicyName, inv.WorkKeyDigest = policy.Name, digest[:]
	inv.WorkFairnessDigest, inv.WorkFairnessLimit = fair, policy.MaxRunningPerFairnessKey
	return inv, nil
}

// Admission and claims share lane-first locking. Callers may already hold the
// lane, but must never enter here holding a receipt without acquiring it first.
func lockWorkAdmissionLane(ctx context.Context, tx pgx.Tx, appID, policyName string, digest []byte) (int64, error) {
	q := sqlc.New()
	if err := q.WorkAdmissionEnsureLane(ctx, tx, sqlc.WorkAdmissionEnsureLaneParams{AppID: mustPgUUID(appID), PolicyName: policyName, KeyDigest: digest}); err != nil {
		return 0, err
	}
	return q.KeyedReplayLockLane(ctx, tx, sqlc.KeyedReplayLockLaneParams{AppID: mustPgUUID(appID), PolicyName: policyName, KeyDigest: digest})
}

func enqueueKeyedInvocationTx(ctx context.Context, tx pgx.Tx, inv Invocation, policy workpolicy.Policy) (Invocation, bool, error) {
	sequence, err := lockWorkAdmissionLane(ctx, tx, inv.AppID, policy.Name, inv.WorkKeyDigest)
	if err != nil {
		return Invocation{}, false, err
	}
	q := sqlc.New()
	row, err := q.WorkAdmissionInvocation(ctx, tx, mustPgUUID(inv.ID))
	if err == nil {
		existing, err := invocationFromSQL(row)
		if err != nil {
			return Invocation{}, false, err
		}
		if existing.AppID != inv.AppID || existing.PlatformTenantID != inv.PlatformTenantID || existing.WorkPolicyName != policy.Name ||
			!bytes.Equal(existing.WorkKeyDigest, inv.WorkKeyDigest) || inv.DeploymentScope != "" && existing.DeploymentScope != inv.DeploymentScope ||
			inv.QueueBindingID != "" && canonicalMemUUID(existing.QueueBindingID) != canonicalMemUUID(inv.QueueBindingID) {
			return Invocation{}, false, ErrConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, false, err
	}
	now := time.Now().UTC()
	inv.CreatedAt, inv.DueAt, inv.WorkExpiresAt, inv.WorkSequence = now, policy.AvailableAt(now, inv.DueAt), policy.ExpiresAt(now), sequence
	if policy.PendingUpdates == workpolicy.PendingKeepLatest {
		if err := q.WorkAdmissionSupersede(ctx, tx, sqlc.WorkAdmissionSupersedeParams{AppID: mustPgUUID(inv.AppID), PolicyName: policy.Name, KeyDigest: inv.WorkKeyDigest}); err != nil {
			return Invocation{}, false, err
		}
		if err := q.WorkAdmissionSupersedeBroker(ctx, tx, sqlc.WorkAdmissionSupersedeBrokerParams{AppID: mustPgUUID(inv.AppID), PolicyName: policy.Name, KeyDigest: inv.WorkKeyDigest}); err != nil {
			return Invocation{}, false, err
		}
	}
	if err := q.KeyedReplayAdvanceLane(ctx, tx, sqlc.KeyedReplayAdvanceLaneParams{AppID: mustPgUUID(inv.AppID), PolicyName: policy.Name, KeyDigest: inv.WorkKeyDigest}); err != nil {
		return Invocation{}, false, err
	}
	out, err := enqueueInvocationRow(ctx, tx, inv)
	if err == nil && inv.EnvironmentID != "" {
		out.CreatedAt = inv.CreatedAt.Truncate(time.Microsecond)
	}
	return out, err == nil, err
}

func workCancellationFromSQL(row sqlc.InvocationWorkCancellation) WorkCancellation {
	return WorkCancellation{ID: uuidFromPgtype(row.ID).String(), AppID: uuidFromPgtype(row.AppID).String(), PolicyName: row.PolicyName, KeyDigest: row.KeyDigest, CancelledCount: row.CancelledCount, CreatedAt: timeFromPgtype(row.CreatedAt)}
}

func cancelPendingKeyedInvocationsTx(ctx context.Context, tx pgx.Tx, appID, policy string, digest []byte, id string) (WorkCancellation, error) {
	if _, err := lockWorkAdmissionLane(ctx, tx, appID, policy, digest); err != nil {
		return WorkCancellation{}, err
	}
	q := sqlc.New()
	_, err := q.WorkAdmissionInsertCancellation(ctx, tx, sqlc.WorkAdmissionInsertCancellationParams{ID: mustPgUUID(id), AppID: mustPgUUID(appID), PolicyName: policy, KeyDigest: digest})
	if errors.Is(err, pgx.ErrNoRows) {
		row, err := q.WorkAdmissionCancellation(ctx, tx, mustPgUUID(id))
		if err != nil {
			return WorkCancellation{}, err
		}
		receipt := workCancellationFromSQL(row)
		if receipt.AppID != appID || receipt.PolicyName != policy || !bytes.Equal(receipt.KeyDigest, digest) {
			return WorkCancellation{}, ErrConflict
		}
		return receipt, nil
	}
	if err != nil {
		return WorkCancellation{}, err
	}
	n, err := q.WorkAdmissionCancel(ctx, tx, sqlc.WorkAdmissionCancelParams{AppID: mustPgUUID(appID), PolicyName: policy, KeyDigest: digest})
	if err != nil {
		return WorkCancellation{}, err
	}
	broker, err := q.WorkAdmissionCancelBroker(ctx, tx, sqlc.WorkAdmissionCancelBrokerParams{AppID: mustPgUUID(appID), PolicyName: policy, KeyDigest: digest})
	if err != nil {
		return WorkCancellation{}, err
	}
	row, err := q.WorkAdmissionFinishCancellation(ctx, tx, sqlc.WorkAdmissionFinishCancellationParams{ID: mustPgUUID(id), CancelledCount: n + broker})
	return workCancellationFromSQL(row), err
}
