package internalmail

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) recoveryState(ctx context.Context, db q.DBTX) (q.InternalMailRecoveryState, error) {
	r, err := s.queries.RecoveryState(ctx, db, s.namespace)
	if errors.Is(err, pgx.ErrNoRows) {
		return q.InternalMailRecoveryState{}, nil
	}
	return r, err
}

func (s *PGStore) recoveryStatusTx(ctx context.Context, db q.DBTX) (RecoveryStatus, error) {
	r, err := s.recoveryState(ctx, db)
	if err != nil {
		return RecoveryStatus{}, err
	}
	stats, err := s.queries.RecoveryStatistics(ctx, db, s.namespace)
	return RecoveryStatus{RecoveryID: idString(r.RecoveryID), Revision: r.Revision, Held: r.Held, UpdatedAt: timePointer(r.UpdatedAt), Quarantined: stats.Quarantined, ActiveSubmissions: stats.ActiveSubmissions}, err
}

func (s *PGStore) recoveryStatus(ctx context.Context) (RecoveryStatus, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RecoveryStatus{}, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.queries.ShareRecovery(ctx, tx, s.namespace); err != nil {
		return RecoveryStatus{}, err
	}
	return s.recoveryStatusTx(ctx, tx)
}

// ConfigureRecovery must run before encryption readiness, listener or workers.
// The operator supplies a fresh UUID OUTSIDE the restored database. A database
// cannot reliably detect its own rewind. Reusing the current ID is restart
// safe and never reopens or reholds a released run; older known IDs are refused.
func (s *PGStore) ConfigureRecovery(ctx context.Context, id string, enabled func() bool) error {
	if !canonicalOperatorID(id) {
		return ErrInvalid
	}
	audit := OperatorAuditInput{RequestID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("gregale-mail-recovery:"+s.namespace+":"+id)).String(), Actor: "maild-startup", Reason: "database_restore", Evidence: "restore:" + id}
	_, err := s.operatorMutation(ctx, audit, "recovery_start", id, audit, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.queries.LockRecovery(ctx, tx, s.namespace); err != nil {
			return nil, err
		}
		state, err := s.recoveryState(ctx, tx)
		if err != nil {
			return nil, err
		}
		if idString(state.RecoveryID) == id {
			return s.recoveryStatusTx(ctx, tx)
		}
		seen, err := s.queries.RecoverySeen(ctx, tx, q.RecoverySeenParams{Namespace: s.namespace, RecoveryID: pgID(id)})
		if err != nil {
			return nil, err
		}
		if seen {
			return nil, ErrRecoveryRevision
		}
		if err := s.queries.InsertRecoveryRun(ctx, tx, q.InsertRecoveryRunParams{Namespace: s.namespace, RecoveryID: pgID(id)}); err != nil {
			return nil, err
		}
		if _, err := s.queries.StartRecovery(ctx, tx, q.StartRecoveryParams{Namespace: s.namespace, RecoveryID: pgID(id)}); err != nil {
			return nil, err
		}
		return s.recoveryStatusTx(ctx, tx)
	})
	if err != nil {
		return err
	}
	state, err := s.recoveryState(ctx, s.pool)
	if err != nil {
		return err
	}
	if idString(state.RecoveryID) != id {
		return ErrRecoveryRevision
	}
	return nil
}

func (s *PGStore) checkRecovery(ctx context.Context) error {
	allowed, err := s.queries.RecoveryPermitted(ctx, s.pool, s.namespace)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRecoveryHeld
	}
	return nil
}

func (s *PGStore) expireRecipients(ctx context.Context, params q.ExpireRecipientsParams) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.shareRecovery(ctx, tx, ""); err != nil {
		if errors.Is(err, ErrRecoveryHeld) {
			return nil
		}
		return err
	}
	if err := s.queries.ExpireRecipients(ctx, tx, params); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PGStore) shareRecovery(ctx context.Context, tx pgx.Tx, recipientID string) error {
	if err := s.queries.ShareRecovery(ctx, tx, s.namespace); err != nil {
		return err
	}
	var allowed bool
	var err error
	if recipientID == "" {
		allowed, err = s.queries.RecoveryPermitted(ctx, tx, s.namespace)
	} else {
		result, readErr := s.queries.RecipientRecoveryPermitted(ctx, tx, q.RecipientRecoveryPermittedParams{Namespace: s.namespace, ID: pgID(recipientID)})
		allowed, err = result.Valid && result.Bool, readErr
	}
	if err != nil {
		return storeError(err)
	}
	if !allowed {
		return ErrRecoveryHeld
	}
	return nil
}

func (s *PGStore) recoveryPreview(ctx context.Context, list OperatorList) (RecoveryPreview, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RecoveryPreview{}, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.queries.ShareRecovery(ctx, tx, s.namespace); err != nil {
		return RecoveryPreview{}, err
	}
	status, err := s.recoveryStatusTx(ctx, tx)
	if err != nil {
		return RecoveryPreview{}, err
	}
	rows, err := s.queries.RecoveryRecipients(ctx, tx, q.RecoveryRecipientsParams{Namespace: s.namespace, AfterID: pgID(operatorAfterID(list.After)), PageLimit: int32(list.Limit + 1)})
	items := make([]RecoveryRecipient, 0, len(rows))
	for _, r := range rows {
		items = append(items, RecoveryRecipient{ID: idString(r.ID), MessageID: idString(r.MessageID), Status: r.Status, LeaseUntil: timePointer(r.LeaseUntil)})
	}
	return RecoveryPreview{status, operatorPage(items, list.Limit, func(r RecoveryRecipient) string { return r.ID })}, err
}

func (s *PGStore) lockRecoveryRequest(ctx context.Context, tx pgx.Tx, id string, revision int64) (q.InternalMailRecoveryState, error) {
	if err := s.queries.LockRecovery(ctx, tx, s.namespace); err != nil {
		return q.InternalMailRecoveryState{}, err
	}
	state, err := s.recoveryState(ctx, tx)
	if err != nil {
		return state, err
	}
	if idString(state.RecoveryID) != id || state.Revision != revision {
		return state, ErrRecoveryRevision
	}
	return state, nil
}

func (s *PGStore) reviewRecovery(ctx context.Context, id string, input RecoveryReviewRequest, enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input.OperatorAuditInput, "recovery_review", id, input, enabled, func(tx pgx.Tx) (any, error) {
		state, err := s.lockRecoveryRequest(ctx, tx, input.RecoveryID, *input.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		r, err := s.queries.LockRecipient(ctx, tx, q.LockRecipientParams{Namespace: s.namespace, ID: pgID(id)})
		if err != nil {
			return nil, storeError(err)
		}
		if r.RecoveryID == state.RecoveryID || r.Status != input.ExpectedStatus || !recoveryPending(r.Status) {
			return nil, ErrConflict
		}
		// A handoff committed before the hold keeps custody until its lease
		// expires or its real worker settles. Recovery never steals that lease.
		active, err := s.queries.RecoveryRecipientActive(ctx, tx, q.RecoveryRecipientActiveParams{Namespace: s.namespace, ID: r.ID})
		if err != nil {
			return nil, err
		}
		if active || input.Outcome == "retry" && r.Status == "submitted" {
			return nil, ErrConflict
		}
		status, kind, code := "submitted", "submitted", "recovery_confirmed_relay_accepted"
		if input.Outcome == "retry" {
			status, kind, code = "queued", "retry", "recovery_confirmed_not_accepted"
		}
		if _, err := s.queries.ReviewRecoveryRecipient(ctx, tx, q.ReviewRecoveryRecipientParams{Namespace: s.namespace, ID: r.ID, RecoveryID: state.RecoveryID, Status: status, Code: code}); err != nil {
			return nil, err
		}
		if err := s.event(ctx, tx, "recovery:"+input.RequestID, id, kind, code); err != nil {
			return nil, err
		}
		return map[string]string{"recipient_id": id, "status": status, "recovery_id": input.RecoveryID}, nil
	})
}

func (s *PGStore) releaseRecovery(ctx context.Context, input RecoveryReleaseRequest, codec *KeyringCodec, enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input.OperatorAuditInput, "recovery_release", input.RecoveryID, input, enabled, func(tx pgx.Tx) (any, error) {
		state, err := s.lockRecoveryRequest(ctx, tx, input.RecoveryID, *input.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		if !state.Held {
			return nil, ErrConflict
		}
		if codec == nil || codec.namespace != s.namespace {
			return nil, ErrInvalid
		}
		if err := s.shareEncryption(ctx, tx); err != nil {
			return nil, err
		}
		keys, err := s.encryptionStatusTx(ctx, tx, codec)
		if err != nil {
			return nil, err
		}
		for _, key := range keys.Keys {
			if key.required() && (!key.Configured || key.RetiredAt != nil) {
				return nil, ErrEncryptionKeyUnavailable
			}
		}
		if _, err := s.queries.ReleaseRecovery(ctx, tx, s.namespace); err != nil {
			return nil, err
		}
		return s.recoveryStatusTx(ctx, tx)
	})
}
