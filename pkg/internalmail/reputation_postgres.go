package internalmail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) lockReputation(ctx context.Context, tx pgx.Tx) error {
	// Excludes every BeginSubmission, including the first hold insertion, and
	// serializes independent feedback/release writers without recipient deadlocks.
	return s.queries.LockDeliveryScope(ctx, tx, q.LockDeliveryScopeParams{Namespace: s.namespace, ScopeID: pgID(operatorAfterID(""))})
}

func (s *PGStore) recordReputation(ctx context.Context, tx pgx.Tx, report Report) error {
	n, err := s.queries.RecordReputationFeedback(ctx, tx, q.RecordReputationFeedbackParams{Namespace: s.namespace, RecipientID: pgID(report.RecipientID), Kind: report.Kind, EventID: report.EventID})
	if err != nil || n == 0 {
		return err
	}
	return s.refreshReputationTx(ctx, tx)
}

func (s *PGStore) refreshReputation(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.lockReputation(ctx, tx); err != nil {
		return err
	}
	if err := s.refreshReputationTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PGStore) refreshReputationTx(ctx context.Context, tx pgx.Tx) error {
	clock, err := s.queries.RelayDatabaseTime(ctx, tx)
	if err != nil {
		return err
	}
	candidates, err := s.queries.ReputationCandidates(ctx, tx, q.ReputationCandidatesParams{Namespace: s.namespace, WindowEnd: clock, WindowSeconds: int32(api.InternalMailReputationWindow / time.Second)})
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		scope := ReputationScope{Kind: candidate.Scope, Key: candidate.ScopeKey}
		window, err := s.reputationWindow(ctx, tx, scope, clock.Time)
		if err != nil {
			return err
		}
		reason := window.reason()
		if reason == "" {
			continue
		}
		r, err := s.queries.SetReputationHold(ctx, tx, q.SetReputationHoldParams{Namespace: s.namespace, Scope: scope.Kind, ScopeKey: scope.Key, Reason: reason,
			Samples: window.Samples, Bounces: window.Bounces, Complaints: window.Complaints, WindowStart: sqlTime(&window.Start), WindowEnd: sqlTime(&window.End),
			TriggerRecipientID: candidate.RecipientID, TriggerEventID: candidate.EventID.String})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		} // An existing hold never ages away or loses its original evidence.
		if err != nil {
			return err
		}
		if err := s.reputationTransition(ctx, tx, r, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *PGStore) reputationWindow(ctx context.Context, db q.DBTX, scope ReputationScope, now time.Time) (ReputationWindow, error) {
	r, err := s.queries.ReputationWindow(ctx, db, q.ReputationWindowParams{Namespace: s.namespace, Scope: scope.Kind, ScopeKey: scope.Key,
		WindowEnd: sqlTime(&now), WindowSeconds: int32(api.InternalMailReputationWindow / time.Second)})
	return (ReputationWindow{Start: now.Add(-api.InternalMailReputationWindow), End: now, Samples: r.Samples, Bounces: r.Bounces, Complaints: r.Complaints}).withRates(), err
}

func reputationHoldRow(r q.InternalMailReputationHold) ReputationStatus {
	evidence := &ReputationEvidence{ReputationWindow: (ReputationWindow{Start: r.WindowStart.Time, End: r.WindowEnd.Time, Samples: r.Samples, Bounces: r.Bounces, Complaints: r.Complaints}).withRates(), RecipientID: idString(r.TriggerRecipientID), EventID: r.TriggerEventID}
	return ReputationStatus{ReputationScope: ReputationScope{Kind: r.Scope, Key: r.ScopeKey}, Held: r.Held, Revision: r.Revision, Reason: r.Reason, Evidence: evidence, UpdatedAt: timePointer(r.UpdatedAt)}
}

func (s *PGStore) reputationTransition(ctx context.Context, tx pgx.Tx, r q.InternalMailReputationHold, requestID string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(reputationHoldRow(r).Evidence)
	if err != nil {
		return err
	}
	return s.queries.InsertReputationEvent(ctx, tx, q.InsertReputationEventParams{Namespace: s.namespace, ID: pgID(id.String()), Scope: r.Scope, ScopeKey: r.ScopeKey,
		Revision: r.Revision, Held: r.Held, Reason: r.Reason, Evidence: evidence, RequestID: pgID(requestID)})
}

func (s *PGStore) reputationStatus(ctx context.Context, scope ReputationScope) (ReputationStatus, error) {
	if scope.Validate() != nil {
		return ReputationStatus{}, ErrInvalid
	}
	if scope.Kind == "domain" {
		if _, err := s.queries.OperatorDomain(ctx, s.pool, q.OperatorDomainParams{Namespace: s.namespace, ID: pgID(scope.Key)}); err != nil {
			return ReputationStatus{}, storeError(err)
		}
	}
	r, err := s.queries.ReputationHold(ctx, s.pool, q.ReputationHoldParams{Namespace: s.namespace, Scope: scope.Kind, ScopeKey: scope.Key})
	status := ReputationStatus{ReputationScope: scope}
	if err == nil {
		status = reputationHoldRow(r)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ReputationStatus{}, err
	}
	clock, err := s.queries.RelayDatabaseTime(ctx, s.pool)
	if err != nil {
		return ReputationStatus{}, err
	}
	status.Window, err = s.reputationWindow(ctx, s.pool, scope, clock.Time)
	return status, err
}

func (s *PGStore) reputationHolds(ctx context.Context, list OperatorList) (OperatorPage[ReputationStatus], error) {
	if list.Limit < 1 || list.Limit > api.InternalMailMaxOperatorPageSize || list.Status != "" || len(list.After) > api.InternalMailMaxOperatorCursorBytes {
		return OperatorPage[ReputationStatus]{}, ErrInvalid
	}
	if list.After != "" {
		kind, key, ok := strings.Cut(list.After, ":")
		if !ok || (ReputationScope{Kind: kind, Key: key}).Validate() != nil {
			return OperatorPage[ReputationStatus]{}, ErrInvalid
		}
	}
	rows, err := s.queries.ReputationHolds(ctx, s.pool, q.ReputationHoldsParams{Namespace: s.namespace, AfterKey: list.After, PageLimit: int32(list.Limit + 1)})
	if err != nil {
		return OperatorPage[ReputationStatus]{}, err
	}
	clock, err := s.queries.RelayDatabaseTime(ctx, s.pool)
	if err != nil {
		return OperatorPage[ReputationStatus]{}, err
	}
	items := make([]ReputationStatus, 0, len(rows))
	for _, r := range rows {
		status := reputationHoldRow(r)
		status.Window, err = s.reputationWindow(ctx, s.pool, status.ReputationScope, clock.Time)
		if err != nil {
			return OperatorPage[ReputationStatus]{}, err
		}
		items = append(items, status)
	}
	return operatorPage(items, list.Limit, func(v ReputationStatus) string { return v.cursor() }), nil
}

func (s *PGStore) reputationEvents(ctx context.Context, list OperatorList) (OperatorPage[ReputationEvent], error) {
	if err := list.validate(true); err != nil {
		return OperatorPage[ReputationEvent]{}, err
	}
	rows, err := s.queries.ReputationEvents(ctx, s.pool, q.ReputationEventsParams{Namespace: s.namespace, AfterID: pgID(operatorAfterID(list.After)), PageLimit: int32(list.Limit + 1)})
	if err != nil {
		return OperatorPage[ReputationEvent]{}, err
	}
	items := make([]ReputationEvent, 0, len(rows))
	for _, r := range rows {
		e := ReputationEvent{ID: idString(r.ID), ReputationScope: ReputationScope{Kind: r.Scope, Key: r.ScopeKey}, Revision: r.Revision, Held: r.Held, Reason: r.Reason, RequestID: idString(r.RequestID), CreatedAt: r.CreatedAt.Time}
		if err := json.Unmarshal(r.Evidence, &e.Evidence); err != nil {
			return OperatorPage[ReputationEvent]{}, err
		}
		items = append(items, e)
	}
	return operatorPage(items, list.Limit, func(v ReputationEvent) string { return v.ID }), nil
}

func (s *PGStore) releaseReputation(ctx context.Context, input ReputationReleaseRequest, enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input.OperatorAuditInput, "reputation_release", input.cursor(), input, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.lockReputation(ctx, tx); err != nil {
			return nil, err
		}
		r, err := s.queries.ReleaseReputationHold(ctx, tx, q.ReleaseReputationHoldParams{Namespace: s.namespace, Scope: input.Kind, ScopeKey: input.Key, Revision: *input.ExpectedRevision})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReputationRevision
		}
		if err != nil {
			return nil, err
		}
		if err := s.reputationTransition(ctx, tx, r, input.RequestID); err != nil {
			return nil, err
		}
		clock, err := s.queries.RelayDatabaseTime(ctx, tx)
		if err != nil {
			return nil, err
		}
		status := reputationHoldRow(r)
		status.Window, err = s.reputationWindow(ctx, tx, status.ReputationScope, clock.Time)
		return status, err
	})
}

func (s *PGStore) checkReputationDispatch(ctx context.Context, db q.DBTX, id string) error {
	allowed, err := s.queries.ReputationDispatchPermitted(ctx, db, q.ReputationDispatchPermittedParams{Namespace: s.namespace, ID: pgID(id)})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrReputationHeld
	}
	return nil
}
