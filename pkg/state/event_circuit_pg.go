package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventCircuitBreakerStore interface {
	GetEventCircuitBreaker(context.Context, string, string, string) (api.EventCircuitBreakerResponse, error)
	SetEventCircuitBreaker(context.Context, string, string, string, *api.EventCircuitBreakerPolicy) (api.EventCircuitBreakerResponse, error)
	ResetEventCircuitBreaker(context.Context, string, string, string) (api.EventCircuitBreakerResponse, error)
	AcquireEventCircuitPermit(context.Context, *PublishedEventRecipientWork, time.Time) (string, time.Time, error)
}

func decodeCircuit(row sqlc.EventSubscriptionCircuitBreaker) (*eventCircuitRecord, error) {
	r := &eventCircuitRecord{SubscriptionID: uuidString(row.SubscriptionID), AccountID: uuidString(row.AccountID), AppID: uuidString(row.AppID)}
	if err := json.Unmarshal(row.Policy, &r.Policy); err != nil {
		return nil, err
	}
	if err := r.Policy.Validate(); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(row.StateData, &r.Runtime); err != nil {
		return nil, err
	}
	switch r.Runtime.State {
	case "closed", "open", "half_open", "draining":
	default:
		return nil, ErrInvalidArgument
	}
	return r, nil
}
func readCircuit(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app, sub string) (*eventCircuitRecord, error) {
	row, err := q.EventCircuitGet(ctx, db, sqlc.EventCircuitGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeCircuit(row)
}
func saveCircuit(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, r *eventCircuitRecord, now time.Time) error {
	policy, err := json.Marshal(r.Policy)
	if err != nil {
		return err
	}
	runtime, err := json.Marshal(r.Runtime)
	if err != nil {
		return err
	}
	n, err := q.EventCircuitSave(ctx, db, sqlc.EventCircuitSaveParams{AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), SubscriptionID: mustPgUUID(r.SubscriptionID), Policy: policy, StateData: runtime, NowAt: pgtypeFromTime(now)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}
func circuitManualPaused(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app, sub string) (bool, error) {
	c, err := q.EventSubscriptionControlGet(ctx, db, sqlc.EventSubscriptionControlGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return c.Paused, err
}
func (s *PgStore) GetEventCircuitBreaker(ctx context.Context, account, app, sub string) (api.EventCircuitBreakerResponse, error) {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventCircuitBreakerResponse{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return api.EventCircuitBreakerResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventSubscriptionControlTarget(ctx, tx, sqlc.EventSubscriptionControlTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return api.EventCircuitBreakerResponse{}, mapErr(err)
	}
	r, err := readCircuit(ctx, q, tx, account, app, sub)
	if err != nil {
		return api.EventCircuitBreakerResponse{}, err
	}
	paused, err := circuitManualPaused(ctx, q, tx, account, app, sub)
	if err != nil {
		return api.EventCircuitBreakerResponse{}, err
	}
	out := circuitResponse(sub, r, paused, time.Now().UTC())
	return out, tx.Commit(ctx)
}
func (s *PgStore) SetEventCircuitBreaker(ctx context.Context, account, app, sub string, p *api.EventCircuitBreakerPolicy) (api.EventCircuitBreakerResponse, error) {
	return s.changeEventCircuit(ctx, account, app, sub, p, false)
}
func (s *PgStore) ResetEventCircuitBreaker(ctx context.Context, account, app, sub string) (api.EventCircuitBreakerResponse, error) {
	return s.changeEventCircuit(ctx, account, app, sub, nil, true)
}
func (s *PgStore) changeEventCircuit(ctx context.Context, account, app, sub string, p *api.EventCircuitBreakerPolicy, reset bool) (api.EventCircuitBreakerResponse, error) {
	out := api.EventCircuitBreakerResponse{}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if p != nil {
		if err := p.Validate(); err != nil {
			return out, ErrInvalidArgument
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventSubscriptionControlTarget(ctx, tx, sqlc.EventSubscriptionControlTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return out, mapErr(err)
	}
	if err = q.EventSubscriptionControlLock(ctx, tx, canonicalMemUUID(sub)); err != nil {
		return out, err
	}
	r, err := readCircuit(ctx, q, tx, account, app, sub)
	if err != nil {
		return out, err
	}
	now := time.Now().UTC()
	if reset {
		if r == nil {
			return out, ErrNotFound
		}
		r.Runtime = newEventCircuitRuntime(now, "manual_reset")
	} else if p == nil {
		if err = q.EventCircuitDelete(ctx, tx, sqlc.EventCircuitDeleteParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
			return out, err
		}
		r = nil
	} else {
		r = &eventCircuitRecord{AccountID: account, AppID: app, SubscriptionID: sub, Policy: *p, Runtime: newEventCircuitRuntime(now, "configured")}
		if err = q.EventCircuitEnsureControl(ctx, tx, sqlc.EventCircuitEnsureControlParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
			return out, err
		}
	}
	if r != nil {
		if err = saveCircuit(ctx, q, tx, r, now); err != nil {
			return out, err
		}
	}
	paused, err := circuitManualPaused(ctx, q, tx, account, app, sub)
	if err != nil {
		return out, err
	}
	out = circuitResponse(sub, r, paused, now)
	return out, tx.Commit(ctx)
}
func loadCircuitObservation(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, r *eventCircuitRecord, now time.Time) (eventCircuitObservation, error) {
	out := eventCircuitObservation{}
	if (r.Runtime.State == "closed" || r.Runtime.State == "draining") && now.Sub(r.Runtime.EvaluatedAt) >= api.EventCircuitEvaluationInterval {
		row, err := q.EventCircuitWindow(ctx, db, sqlc.EventCircuitWindowParams{AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), SubscriptionID: canonicalMemUUID(r.SubscriptionID), SinceAt: pgtypeFromTime(circuitSince(r, now)), NowAt: pgtypeFromTime(now)})
		if err != nil {
			return out, err
		}
		out.Successes, out.Failures, out.Incomplete, out.Sampled = row.Successes, row.Failures, row.Incomplete, true
		r.Runtime.EvaluatedAt = now
	}
	if r.Runtime.State == "half_open" && r.Runtime.ProbeToken != "" {
		row, err := q.EventCircuitProbeOutcome(ctx, db, sqlc.EventCircuitProbeOutcomeParams{AccountID: mustPgUUID(r.AccountID), OutboxID: r.Runtime.ProbeOutboxID, SubscriptionID: canonicalMemUUID(r.SubscriptionID)})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		if len(row) > 0 {
			var progress PublishedEventRecipientProgress
			if err := json.Unmarshal(row, &progress); err != nil {
				return out, err
			}
			out.Probe = &progress
		}
	}
	return out, nil
}
func (s *PgStore) AcquireEventCircuitPermit(ctx context.Context, work *PublishedEventRecipientWork, now time.Time) (string, time.Time, error) {
	if !circuitWorkEligible(work) {
		return "", time.Time{}, nil
	}
	account, app, sub := work.Recipient.AccountID, work.Recipient.AppID, work.Recipient.ID
	q := sqlc.New()
	r, err := readCircuit(ctx, q, s.pool, account, app, sub)
	if err != nil || r == nil {
		return "", time.Time{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = q.EventSubscriptionControlLock(ctx, tx, canonicalMemUUID(sub)); err != nil {
		return "", time.Time{}, err
	}
	available, err := q.EventCircuitAppAvailable(ctx, tx, sqlc.EventCircuitAppAvailableParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil || !available {
		return "", time.Time{}, err
	}
	r, err = readCircuit(ctx, q, tx, account, app, sub)
	if err != nil || r == nil {
		return "", time.Time{}, err
	}
	paused, err := circuitManualPaused(ctx, q, tx, account, app, sub)
	if err != nil {
		return "", time.Time{}, err
	}
	if paused {
		return "subscription_paused", now.Add(api.EventSubscriptionControlRetryDelay), tx.Commit(ctx)
	}
	observation, err := loadCircuitObservation(ctx, q, tx, r, now)
	if err != nil {
		return "", time.Time{}, err
	}
	circuitAdvance(r, observation, now)
	reason, next := circuitWaitingReason(r, now)
	if reason == "" {
		circuitReserve(r, work, now)
	}
	if err = saveCircuit(ctx, q, tx, r, now); err != nil {
		return "", time.Time{}, err
	}
	return reason, next, tx.Commit(ctx)
}
func circuitWorkEligible(work *PublishedEventRecipientWork) bool {
	if work == nil || len(work.Recipient.Workflow) != 0 || work.Recipient.ObjectNotification != nil {
		return false
	}
	return validSubscriptionControlIDs(work.Recipient.AccountID, work.Recipient.AppID, work.Recipient.ID) == nil
}
func observeCircuitTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, id int64, app, sub string, p PublishedEventRecipientProgress) error {
	// Non-UUID workflow/object recipient identities do not have application circuits.
	if !isEventSubscriptionUUID(sub) {
		return nil
	}
	row, err := q.EventCircuitGetForApp(ctx, tx, sqlc.EventCircuitGetForAppParams{AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = q.EventSubscriptionControlLock(ctx, tx, canonicalMemUUID(sub)); err != nil {
		return err
	}
	r, err := readCircuit(ctx, q, tx, uuidString(row.AccountID), app, sub)
	if err != nil || r == nil {
		return err
	}
	now := p.UpdatedAt
	observation, err := loadCircuitObservation(ctx, q, tx, r, now)
	if err != nil {
		return err
	}
	if r.Runtime.State == "half_open" && r.Runtime.ProbeOutboxID == id {
		observation.Probe = &p
	}
	if r.Runtime.State == "draining" && circuitOutcomeFailed(p) {
		observation.Failures = max(1, observation.Failures)
	}
	circuitAdvance(r, observation, now)
	return saveCircuit(ctx, q, tx, r, now)
}
func circuitOutcomeFailed(p PublishedEventRecipientProgress) bool {
	return p.FailureCode != EventFanoutFailureCodeDeliveryExpired && (p.State == PublishedEventRecipientFailed || p.State == PublishedEventRecipientPending && p.LastError != "" && p.CapacityScope == "" && p.DeliveryControlReason == "")
}

var _ EventCircuitBreakerStore = (*PgStore)(nil)
var _ EventCircuitBreakerStore = (*MemStore)(nil)
