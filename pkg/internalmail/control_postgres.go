package internalmail

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) dispatchPermittedFor(ctx context.Context, id string) (bool, error) {
	domainID, err := s.queries.RecipientDeliveryDomain(ctx, s.pool, q.RecipientDeliveryDomainParams{Namespace: s.namespace, ID: pgID(id)})
	if err != nil {
		return false, storeError(err)
	}
	return s.queries.DispatchPermitted(ctx, s.pool, q.DispatchPermittedParams{Namespace: s.namespace, Column2: domainID})
}
func (s *PGStore) recipientStopped(ctx context.Context, id string) (bool, error) {
	return s.queries.RecipientStopped(ctx, s.pool, q.RecipientStoppedParams{Namespace: s.namespace, ID: pgID(id)})
}

func controlStatusRow(scope DeliveryScope, r q.DeliveryControlStatusRow) DeliveryControlStatus {
	held := r.Mode != "running" || r.NamespaceMode != "running"
	return DeliveryControlStatus{DeliveryScope: scope, Mode: r.Mode, Revision: r.Revision, RequestID: idString(r.RequestID), UpdatedAt: timePointer(r.UpdatedAt), NamespaceMode: r.NamespaceMode, Held: held,
		Drained: held && r.ActiveClaims == 0 && r.ActiveSubmissions == 0 && r.Uncertain == 0, Queued: r.Queued, ActiveClaims: r.ActiveClaims, ExpiredClaims: r.ExpiredClaims, ActiveSubmissions: r.ActiveSubmissions, Uncertain: r.Uncertain, RelayOwned: r.RelayOwned, Cancelled: r.Cancelled, Expired: r.Expired}
}
func (s *PGStore) validateDeliveryScope(ctx context.Context, db q.DBTX, scope DeliveryScope) error {
	if scope.Validate() != nil {
		return ErrInvalid
	}
	if scope.Kind == "domain" {
		_, err := s.queries.OperatorDomain(ctx, db, q.OperatorDomainParams{Namespace: s.namespace, ID: pgID(scope.DomainID)})
		return storeError(err)
	}
	return nil
}
func (s *PGStore) deliveryControlStatus(ctx context.Context, scope DeliveryScope) (DeliveryControlStatus, error) {
	if err := s.validateDeliveryScope(ctx, s.pool, scope); err != nil {
		return DeliveryControlStatus{}, err
	}
	r, err := s.queries.DeliveryControlStatus(ctx, s.pool, q.DeliveryControlStatusParams{Namespace: s.namespace, ScopeID: pgID(scope.id())})
	return controlStatusRow(scope, r), err
}

func (s *PGStore) changeDeliveryControl(ctx context.Context, mode string, input DeliveryControlRequest, enabled func() bool) (OperationRecord, error) {
	action := map[string]string{"running": "delivery_resume", "paused": "delivery_pause", "draining": "delivery_drain"}[mode]
	return s.operatorMutation(ctx, input.OperatorAuditInput, action, input.resource(), input, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.queries.LockDeliveryScope(ctx, tx, q.LockDeliveryScopeParams{Namespace: s.namespace, ScopeID: pgID(input.id())}); err != nil {
			return nil, err
		}
		if err := s.validateDeliveryScope(ctx, tx, input.DeliveryScope); err != nil {
			return nil, err
		}
		previous, err := s.queries.DeliveryControl(ctx, tx, q.DeliveryControlParams{Namespace: s.namespace, ScopeID: pgID(input.id())})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if previous.Revision != *input.ExpectedRevision {
			return nil, ErrControlRevision
		}
		if err := s.queries.SetDeliveryControl(ctx, tx, q.SetDeliveryControlParams{Namespace: s.namespace, ScopeID: pgID(input.id()), DomainID: pgID(input.DomainID), Mode: mode, RequestID: pgID(input.RequestID)}); err != nil {
			return nil, err
		}
		r, err := s.queries.DeliveryControlStatus(ctx, tx, q.DeliveryControlStatusParams{Namespace: s.namespace, ScopeID: pgID(input.id())})
		return controlStatusRow(input.DeliveryScope, r), err
	})
}

func (s *PGStore) lockCancellation(ctx context.Context, tx pgx.Tx, id string, message bool) ([]q.LockMessageCancellationRow, error) {
	if !message {
		r, err := s.queries.LockRecipientCancellation(ctx, tx, q.LockRecipientCancellationParams{Namespace: s.namespace, ID: pgID(id)})
		return []q.LockMessageCancellationRow{{ID: r.ID, Status: r.Status}}, storeError(err)
	}
	if _, err := s.queries.CancellationMessage(ctx, tx, q.CancellationMessageParams{Namespace: s.namespace, ID: pgID(id)}); err != nil {
		return nil, storeError(err)
	}
	rows, err := s.queries.LockMessageCancellation(ctx, tx, q.LockMessageCancellationParams{Namespace: s.namespace, MessageID: pgID(id), Limit: api.InternalMailMaxRecipients + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.InternalMailMaxRecipients {
		return nil, ErrQuota
	}
	if len(rows) == 0 {
		return nil, ErrConflict
	}
	return rows, nil
}
func (s *PGStore) cancelMail(ctx context.Context, id string, message bool, input OperatorAuditInput, enabled func() bool) (OperationRecord, error) {
	action := "recipient_cancel"
	if message {
		action = "message_cancel"
	}
	return s.operatorMutation(ctx, input, action, id, input, enabled, func(tx pgx.Tx) (any, error) {
		rows, err := s.lockCancellation(ctx, tx, id, message)
		if err != nil {
			return nil, err
		}
		result := CancellationResult{ResourceID: id, Cancelled: []string{}, AlreadyCancelled: []string{}, Protected: []CancellationRecipient{}}
		for _, r := range rows {
			switch r.Status {
			case "cancelled":
				result.AlreadyCancelled = append(result.AlreadyCancelled, idString(r.ID))
			case "queued", "deferred", "claimed":
				n, err := s.queries.CancelRecipient(ctx, tx, q.CancelRecipientParams{Namespace: s.namespace, ID: r.ID})
				if err != nil {
					return nil, err
				}
				if n != 1 {
					return nil, ErrConflict
				}
				if err := s.event(ctx, tx, "cancel:"+input.RequestID+":"+idString(r.ID), idString(r.ID), "cancelled", "operator_cancelled"); err != nil {
					return nil, err
				}
				result.Cancelled = append(result.Cancelled, idString(r.ID))
			default:
				result.Protected = append(result.Protected, CancellationRecipient{ID: idString(r.ID), Status: r.Status})
			}
		}
		result.Complete = len(result.Protected) == 0
		return result, nil
	})
}

func (s *PGStore) shareSubmissionControls(ctx context.Context, tx pgx.Tx, id string) error {
	domainID, err := s.queries.RecipientDeliveryDomain(ctx, tx, q.RecipientDeliveryDomainParams{Namespace: s.namespace, ID: pgID(id)})
	if err != nil {
		return storeError(err)
	}
	for _, scope := range []pgtype.UUID{pgID(operatorAfterID("")), domainID} {
		if err := s.queries.ShareDeliveryScope(ctx, tx, q.ShareDeliveryScopeParams{Namespace: s.namespace, ScopeID: scope}); err != nil {
			return err
		}
	}
	allowed, err := s.queries.DispatchPermitted(ctx, tx, q.DispatchPermittedParams{Namespace: s.namespace, Column2: domainID})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrDeliveryPaused
	}
	return nil
}
