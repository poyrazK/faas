package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventConsumerProcessingHealthStore interface {
	GetEventConsumerProcessingHealth(context.Context, string, string, string, time.Duration) (api.EventConsumerProcessingHealth, error)
}

func processingHealthBase(accountApp, sub string, now time.Time, window time.Duration) api.EventConsumerProcessingHealth {
	return api.EventConsumerProcessingHealth{AppID: canonicalMemUUID(accountApp), SubscriptionID: canonicalMemUUID(sub), ObservedAt: now, WindowStart: now.Add(-window), Coverage: "retained_deferred_invocations", Complete: true, NearExpirySeconds: int(api.EventProcessingNearExpiry / time.Second), RenewalFailureCoverage: "not_recorded"}
}
func validProcessingHealthWindow(window time.Duration) bool {
	return window >= time.Second && window <= api.EventConsumerHealthMaxWindow && window%time.Second == 0
}
func (s *PgStore) GetEventConsumerProcessingHealth(ctx context.Context, account, app, sub string, window time.Duration) (api.EventConsumerProcessingHealth, error) {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventConsumerProcessingHealth{}, err
	}
	if !validProcessingHealthWindow(window) {
		return api.EventConsumerProcessingHealth{}, ErrInvalidArgument
	}
	row, err := sqlc.New().EventConsumerProcessingHealth(ctx, s.pool, sqlc.EventConsumerProcessingHealthParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), LifetimeSeconds: int32(api.EventAcknowledgmentLifetime / time.Second), WindowSeconds: int32(window / time.Second), NearExpirySeconds: float64(api.EventProcessingNearExpiry / time.Second)})
	if err != nil {
		return api.EventConsumerProcessingHealth{}, mapErr(err)
	}
	out := processingHealthBase(app, sub, timeFromPgtype(row.ObservedAt), window)
	out.Waiting, out.Delivering, out.Processing, out.Expired = row.Waiting, row.Delivering, row.Processing, row.Expired
	out.Completed, out.DeadLettered, out.Failed, out.Other = row.Completed, row.DeadLettered, row.Failed, row.Other
	out.NearExpiry = row.NearExpiry
	out.OldestProcessingOfferedAt = timestamptzToTimePtr(row.OldestProcessingOfferedAt)
	out.AcknowledgmentSamples = row.AcknowledgmentSamples
	out.AcknowledgmentLatencyMaxSeconds = row.AcknowledgmentLatencyMaxSeconds
	setProcessingAge(&out)
	if err := s.setAcknowledgmentMetricHealth(ctx, account, app, sub, window, &out); err != nil {
		return api.EventConsumerProcessingHealth{}, err
	}
	return out, nil
}
func setProcessingAge(out *api.EventConsumerProcessingHealth) {
	if at := out.OldestProcessingOfferedAt; at != nil && !at.After(out.ObservedAt) {
		out.OldestProcessingAgeSeconds = out.ObservedAt.Sub(*at).Seconds()
	}
}
func (m *MemStore) GetEventConsumerProcessingHealth(ctx context.Context, account, app, sub string, window time.Duration) (api.EventConsumerProcessingHealth, error) {
	if err := ctx.Err(); err != nil {
		return api.EventConsumerProcessingHealth{}, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventConsumerProcessingHealth{}, err
	}
	if !validProcessingHealthWindow(window) {
		return api.EventConsumerProcessingHealth{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	target, ok := m.eventSubscriptionAppLocked(app)
	if !ok || target.Status == AppDeleted || !sameMemUUID(target.AccountID, account) {
		return api.EventConsumerProcessingHealth{}, ErrNotFound
	}
	out := processingHealthBase(app, sub, time.Now().UTC(), window)
	for id, inv := range m.invocations {
		if !sameMemUUID(inv.AccountID, account) || !sameMemUUID(inv.AppID, app) {
			continue
		}
		seconds, err := EventAcknowledgmentSeconds(inv)
		if err != nil || seconds == 0 {
			continue
		}
		var headers map[string]string
		if json.Unmarshal(inv.Headers, &headers) != nil || !sameMemUUID(headers["x-gregale-event-subscription-id"], sub) {
			continue
		}
		ack := m.eventAcknowledgments[id]
		viewAck := ack
		if ack.claim.Attempt != inv.Attempts || ack.claim.ReplayGeneration != inv.ReplayGeneration {
			viewAck = eventAcknowledgment{}
		}
		status := acknowledgmentStatus(inv, viewAck, out.ObservedAt)
		switch status.ProcessingState {
		case "waiting":
			out.Waiting++
		case "delivering":
			out.Delivering++
		case "processing":
			out.Processing++
			risk := time.Duration(ack.seconds) * time.Second / 3
			if risk > api.EventProcessingNearExpiry {
				risk = api.EventProcessingNearExpiry
			}
			if inv.LeaseExpiresAt != nil && !inv.LeaseExpiresAt.After(out.ObservedAt.Add(risk)) {
				out.NearExpiry++
			}
			offered := ack.deadline.Add(-api.EventAcknowledgmentLifetime)
			if out.OldestProcessingOfferedAt == nil || offered.Before(*out.OldestProcessingOfferedAt) {
				out.OldestProcessingOfferedAt = &offered
			}
		case "expired":
			if inv.State == InvocationDispatching {
				out.Expired++
			} else {
				out.Other++
			}
		case "completed":
			out.Completed++
		case "dead_letter":
			out.DeadLettered++
		case "failed":
			out.Failed++
		default:
			out.Other++
		}
		if inv.State == InvocationCompleted && ack.phase == "completed" && ack.claim.Attempt == inv.Attempts && ack.claim.ReplayGeneration == inv.ReplayGeneration && inv.CompletedAt != nil && !inv.CompletedAt.Before(out.WindowStart) && !inv.CompletedAt.After(out.ObservedAt) && !inv.CompletedAt.Before(ack.deadline.Add(-api.EventAcknowledgmentLifetime)) {
			out.AcknowledgmentSamples++
			latency := inv.CompletedAt.Sub(ack.deadline.Add(-api.EventAcknowledgmentLifetime)).Seconds()
			if latency > out.AcknowledgmentLatencyMaxSeconds {
				out.AcknowledgmentLatencyMaxSeconds = latency
			}
		}
	}
	setProcessingAge(&out)
	m.setAcknowledgmentMetricHealthLocked(account, app, sub, window, &out)
	return out, nil
}
