package internalmail

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) operatorMessages(ctx context.Context, o OperatorList) (OperatorPage[OperatorMessage], error) {
	if err := o.validate(true); err != nil {
		return OperatorPage[OperatorMessage]{}, err
	}
	rows, err := s.queries.OperatorMessages(ctx, s.pool, q.OperatorMessagesParams{Namespace: s.namespace, AfterID: pgID(operatorAfterID(o.After)), Status: o.Status, PageLimit: int32(o.Limit + 1)})
	items := make([]OperatorMessage, 0, len(rows))
	for _, r := range rows {
		m := OperatorMessage{ID: idString(r.ID), DomainID: idString(r.DomainID), ProducerID: r.ProducerID, SenderAddress: r.SenderAddress, CreatedAt: r.CreatedAt.Time, RecipientCount: r.RecipientCount, MessageTiming: MessageTiming{SendAt: timePointer(r.SendAt), ExpiresAt: timePointer(r.ExpiresAt)}}
		if r.PayloadPrunedAt.Valid {
			m.PayloadPrunedAt = &r.PayloadPrunedAt.Time
		}
		items = append(items, m)
	}
	return operatorPage(items, o.Limit, func(m OperatorMessage) string { return m.ID }), err
}

func (s *PGStore) operatorMessage(ctx context.Context, id string) (OperatorMessage, error) {
	r, err := s.queries.OperatorMessage(ctx, s.pool, q.OperatorMessageParams{Namespace: s.namespace, ID: pgID(id)})
	if err != nil {
		return OperatorMessage{}, storeError(err)
	}
	return s.operatorMessageDetails(ctx, r)
}

func (s *PGStore) operatorMessageDetails(ctx context.Context, r q.OperatorMessageRow) (OperatorMessage, error) {
	m := OperatorMessage{ID: idString(r.ID), DomainID: idString(r.DomainID), ProducerID: r.ProducerID, SenderAddress: r.SenderAddress, CreatedAt: r.CreatedAt.Time, Recipients: []OperatorRecipient{}, MessageTiming: MessageTiming{SendAt: timePointer(r.SendAt), ExpiresAt: timePointer(r.ExpiresAt)}}
	if r.PayloadPrunedAt.Valid {
		m.PayloadPrunedAt = &r.PayloadPrunedAt.Time
	}
	rows, err := s.queries.OperatorRecipients(ctx, s.pool, q.OperatorRecipientsParams{Namespace: s.namespace, MessageID: r.ID})
	for _, r := range rows {
		v := OperatorRecipient{Recipient: Recipient{ID: idString(r.ID), MessageID: idString(r.MessageID), Address: r.Address, Status: r.Status, Attempts: r.Attempts, Code: r.Code, UpdatedAt: r.UpdatedAt.Time, SigningSelector: r.SigningSelector}, NextAttemptAt: r.NextAttemptAt.Time}
		if r.LeaseUntil.Valid {
			v.LeaseUntil = &r.LeaseUntil.Time
		}
		m.Recipients = append(m.Recipients, v)
	}
	m.RecipientCount = int64(len(rows))
	return m, err
}

func (s *PGStore) operatorEvents(ctx context.Context, id string, o OperatorList) (OperatorPage[Event], error) {
	if err := o.validate(false); err != nil {
		return OperatorPage[Event]{}, err
	}
	if _, err := s.queries.OperatorMessage(ctx, s.pool, q.OperatorMessageParams{Namespace: s.namespace, ID: pgID(id)}); err != nil {
		return OperatorPage[Event]{}, storeError(err)
	}
	rows, err := s.queries.OperatorEvents(ctx, s.pool, q.OperatorEventsParams{Namespace: s.namespace, MessageID: pgID(id), AfterID: o.After, PageLimit: int32(o.Limit + 1)})
	items := make([]Event, 0, len(rows))
	for _, r := range rows {
		items = append(items, Event{ID: r.ID, RecipientID: idString(r.RecipientID), Kind: r.Kind, Code: r.Code, CreatedAt: r.CreatedAt.Time})
	}
	return operatorPage(items, o.Limit, func(e Event) string { return e.ID }), err
}

func (s *PGStore) operatorDomains(ctx context.Context, o OperatorList) (OperatorPage[Domain], error) {
	if err := o.validate(true); err != nil {
		return OperatorPage[Domain]{}, err
	}
	rows, err := s.queries.OperatorDomains(ctx, s.pool, q.OperatorDomainsParams{Namespace: s.namespace, AfterID: pgID(operatorAfterID(o.After)), PageLimit: int32(o.Limit + 1)})
	items := make([]Domain, 0, len(rows))
	for _, r := range rows {
		items = append(items, domainPublicRow(q.OperatorDomainRow(r)))
	}
	return operatorPage(items, o.Limit, func(d Domain) string { return d.ID }), err
}

func (s *PGStore) operatorDomain(ctx context.Context, id string) (Domain, error) {
	r, err := s.queries.OperatorDomain(ctx, s.pool, q.OperatorDomainParams{Namespace: s.namespace, ID: pgID(id)})
	return domainPublicRow(r), storeError(err)
}

func (s *PGStore) operatorSuppressions(ctx context.Context, o OperatorList) (OperatorPage[Suppression], error) {
	if err := o.validate(false); err != nil {
		return OperatorPage[Suppression]{}, err
	}
	rows, err := s.queries.OperatorSuppressions(ctx, s.pool, q.OperatorSuppressionsParams{Namespace: s.namespace, AfterID: o.After, PageLimit: int32(o.Limit + 1)})
	items := make([]Suppression, 0, len(rows))
	for _, r := range rows {
		items = append(items, Suppression{Address: r.Address, Reason: r.Reason, CreatedAt: r.CreatedAt.Time})
	}
	return operatorPage(items, o.Limit, func(v Suppression) string { return v.Address }), err
}

func (s *PGStore) operatorAudit(ctx context.Context, o OperatorList) (OperatorPage[OperationRecord], error) {
	if err := o.validate(true); err != nil {
		return OperatorPage[OperationRecord]{}, err
	}
	rows, err := s.queries.OperatorAudit(ctx, s.pool, q.OperatorAuditParams{Namespace: s.namespace, AfterID: pgID(operatorAfterID(o.After)), PageLimit: int32(o.Limit + 1)})
	items := make([]OperationRecord, 0, len(rows))
	for _, r := range rows {
		items = append(items, OperationRecord{ID: idString(r.ID), Action: r.Action, Resource: r.Resource, Actor: r.Actor, Reason: r.Reason, Evidence: r.Evidence, Result: r.Result, CreatedAt: r.CreatedAt.Time})
	}
	return operatorPage(items, o.Limit, func(v OperationRecord) string { return v.ID }), err
}

func (s *PGStore) operatorReconcile(ctx context.Context, id string, input ReconcileRequest, enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input.OperatorAuditInput, "reconcile", id, input, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.queries.RecoverReportedSubmission(ctx, tx, q.RecoverReportedSubmissionParams{Namespace: s.namespace, ID: pgID(id)}); err != nil {
			return nil, err
		}
		r, err := s.queries.LockRecipient(ctx, tx, q.LockRecipientParams{Namespace: s.namespace, ID: pgID(id)})
		if err != nil {
			return nil, storeError(err)
		}
		if r.Status != "uncertain" {
			return nil, ErrConflict
		}
		status, kind, code := "submitted", "submitted", "confirmed_relay_accepted"
		if input.Outcome == "retry" {
			status, kind, code = "queued", "retry", "confirmed_not_accepted"
		}
		if err := s.queries.ReportStatus(ctx, tx, q.ReportStatusParams{Namespace: s.namespace, ID: r.ID, Status: status, Code: code}); err != nil {
			return nil, err
		}
		if err := s.event(ctx, tx, "operator:"+input.RequestID, id, kind, code); err != nil {
			return nil, storeError(err)
		}
		return map[string]string{"status": status}, nil
	})
}

func (s *PGStore) operatorRetention(ctx context.Context, input OperatorAuditInput, policy RetentionPolicy, prepare func(Message) ([]byte, error), enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input, "retention_apply", "payloads", input, enabled, func(tx pgx.Tx) (any, error) {
		return s.prunePayloads(ctx, tx, policy, prepare, enabled)
	})
}

func (s *PGStore) operatorMutation(ctx context.Context, input OperatorAuditInput, action, resource string, request any, enabled func() bool, perform func(pgx.Tx) (any, error)) (OperationRecord, error) {
	if enabled == nil || !enabled() {
		return OperationRecord{}, ErrDisabled
	}
	if input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	raw, err := json.Marshal(struct {
		Action, Resource string
		Request          any
	}{action, resource, request})
	if err != nil {
		return OperationRecord{}, err
	}
	hash := sha256.Sum256(raw)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperationRecord{}, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.queries.LockOperation(ctx, tx, q.LockOperationParams{Namespace: s.namespace, RequestID: input.RequestID}); err != nil {
		return OperationRecord{}, err
	}
	previous, err := s.queries.Operation(ctx, tx, q.OperationParams{Namespace: s.namespace, ID: pgID(input.RequestID)})
	if err == nil {
		if subtle.ConstantTimeCompare(previous.RequestHash, hash[:]) != 1 {
			return OperationRecord{}, ErrConflict
		}
		record := operationRow(previous)
		record.replayed = true
		return record, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperationRecord{}, err
	}
	result, err := perform(tx)
	if err != nil {
		return OperationRecord{}, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return OperationRecord{}, err
	}
	record, err := s.queries.InsertOperation(ctx, tx, q.InsertOperationParams{Namespace: s.namespace, ID: pgID(input.RequestID), Action: action, Resource: resource, Actor: input.Actor, Reason: input.Reason, Evidence: input.Evidence, RequestHash: hash[:], Result: body})
	if err != nil {
		return OperationRecord{}, err
	}
	if !enabled() {
		return OperationRecord{}, ErrDisabled
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationRecord{}, err
	}
	return operationRow(record), nil
}

func operationRow(r q.InternalMailOperation) OperationRecord {
	return OperationRecord{ID: idString(r.ID), Action: r.Action, Resource: r.Resource, Actor: r.Actor, Reason: r.Reason, Evidence: r.Evidence, Result: r.Result, CreatedAt: r.CreatedAt.Time}
}

func rollbackOperator(ctx context.Context, tx pgx.Tx) {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.InternalMailOperatorRollbackTimeout)
	defer cancel()
	_ = tx.Rollback(rollbackCtx)
}
