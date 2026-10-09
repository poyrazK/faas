// adr: 843
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

type outboxSweepCursor struct{ index, entities string }

func (s *server) runDurableEntityOutbox(ctx context.Context) {
	if s.durableEntities == nil || !s.durableEntityOutboxEnabled {
		return
	}
	cursor := outboxSweepCursor{}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		next, err := s.sweepDurableEntityOutbox(ctx, cursor)
		cursor = next
		if err != nil && ctx.Err() == nil {
			// Neither provider/SQL errors nor customer payloads enter logs.
			s.log.Warn("durable entity outbox discovery deferred")
		}
		timer.Reset(api.DurableEntityOutboxPollInterval)
	}
}

func scanDurableEntityOutboxPage(ctx context.Context, cursor string, scan func(context.Context, string) (durableentity.OutboxPage, error)) (durableentity.OutboxPage, error) {
	scanCtx, cancel := context.WithTimeout(ctx, api.DurableEntityOutboxScanTimeout)
	defer cancel()
	return scan(scanCtx, cursor)
}

func (s *server) sweepDurableEntityOutbox(ctx context.Context, cursor outboxSweepCursor) (outboxSweepCursor, error) {
	indexed, indexErr := scanDurableEntityOutboxPage(ctx, cursor.index, s.durableEntities.ScanIndexedDueOutbox)
	reconciled, reconcileErr := scanDurableEntityOutboxPage(ctx, cursor.entities, s.durableEntities.ScanDueOutbox)
	next := outboxSweepCursor{index: indexed.NextCursor, entities: reconciled.NextCursor}
	if failed := indexed.Failed + reconciled.Failed; failed > 0 {
		s.log.Warn("durable entity outbox state unavailable", "entities", failed)
	}
	seen := map[durableentity.ID]bool{}
	for _, work := range append(indexed.Work, reconciled.Work...) {
		if ctx.Err() != nil {
			return next, ctx.Err()
		}
		if seen[work.Entity] || !s.durableEntityApps[work.Entity.AppID] {
			continue
		}
		seen[work.Entity] = true
		if err := s.deliverDurableEntityOutbox(ctx, work); err != nil && ctx.Err() == nil {
			if !errors.Is(err, durableentity.ErrOutboxObsolete) && !errors.Is(err, durableentity.ErrOutboxBackoff) && !errors.Is(err, durableentity.ErrOutboxExhausted) && !errors.Is(err, durableentity.ErrBusy) && !errors.Is(err, durableentity.ErrConflict) {
				s.log.Warn("durable entity outbox acceptance deferred")
			}
		}
	}
	return next, errors.Join(indexErr, reconcileErr)
}

func (s *server) deliverDurableEntityOutbox(ctx context.Context, work durableentity.OutboxWork) (err error) {
	if s.durableEntities == nil || !s.durableEntityOutboxEnabled || !s.durableEntityApps[work.Entity.AppID] {
		return durableentity.ErrInvalid
	}
	started := time.Now()
	defer s.durableEntityMetrics.observeDuration("outbox", started)
	defer func() { s.durableEntityMetrics.observeResult("outbox", durableentity.Result{}, err) }()
	callCtx, cancel := context.WithTimeout(ctx, api.DurableEntityInvokeTimeout)
	defer cancel()
	if status, err := s.durableEntities.InspectOutbox(callCtx, work.Entity); err == nil && s.durableEntityMetrics != nil {
		s.durableEntityMetrics.outboxPending.WithLabelValues().Observe(float64(status.Pending))
	}
	// Established admission holds do not burn the relay retry budget. Recheck
	// again under ownership before transport acceptance to cover changes here.
	if _, _, err := s.durableEntityOutboxAdmission(callCtx, work.Entity); err != nil {
		return err
	}
	return s.durableEntities.RelayOutbox(callCtx, work.Entity, work.MessageID, s.durableEntityOwner, func(ctx context.Context, message durableentity.OutboxMessage) error {
		return s.acceptDurableEntityOutbox(ctx, work.Entity, message)
	})
}

func (s *server) acceptDurableEntityOutbox(ctx context.Context, entity durableentity.ID, message durableentity.OutboxMessage) error {
	acct, app, err := s.durableEntityOutboxAdmission(ctx, entity)
	if err != nil {
		return err
	}
	store, ok := s.store.(state.EntityOutboxDeliveryStore)
	if !ok {
		return durableentity.ErrUnsupported
	}
	// The existing dispatcher supplies signing, destination/network controls,
	// receiver retries and delivery inspection. The stable ledger ID also
	// appears in its receiver headers; the body retains verified entity scope.
	body, err := json.Marshal(struct {
		Entity       durableentity.ID `json:"entity"`
		MessageID    string           `json:"message_id"`
		StateVersion uint64           `json:"state_version"`
		Ordinal      int              `json:"ordinal"`
		Data         json.RawMessage  `json:"data"`
	}{entity, message.ID, message.Version, message.Ordinal, message.Intent.Payload})
	if err != nil {
		return durableentity.ErrInvalid
	}
	accepted, err := store.AcceptEntityOutboxDelivery(ctx, state.AppWebhookDelivery{
		ID: message.ID, WebhookID: message.Intent.WebhookID, AccountID: acct.ID, AppID: app.ID,
		Event: state.AppWebhookEvent(message.Intent.EventType), Payload: body,
	})
	if err != nil {
		return err
	}
	if accepted != message.ID {
		return durableentity.ErrCorrupt
	}
	return nil
}

func (s *server) durableEntityOutboxAdmission(ctx context.Context, entity durableentity.ID) (state.Account, state.App, error) {
	acct, app, request, err := s.durableEntityBackgroundSelection(ctx, entity)
	if err != nil {
		if errors.Is(err, durableentity.ErrAlarmObsolete) {
			err = durableentity.ErrOutboxObsolete
		}
		return acct, app, err
	}
	if acct.AbuseHeld() || acct.Plan.WebhookPerApp() <= 0 || !s.durableEntityApps[entity.AppID] || !s.durableEntityOutboxEnabled {
		return acct, app, durableentity.ErrInvalid
	}
	id, _, problem := s.durableEntityIdentity((&http.Request{}).WithContext(ctx), acct, app, request)
	if problem != nil {
		return acct, app, problem
	}
	if id != entity {
		return acct, app, durableentity.ErrOutboxObsolete
	}
	return acct, app, nil
}
