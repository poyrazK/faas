package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) PreviewEventSubscriptionRateLimit(ctx context.Context, account, app, sub string, req api.EventSubscriptionRatePreviewRequest) (api.EventSubscriptionRatePreview, error) {
	var zero api.EventSubscriptionRatePreview
	if req.Validate() != nil || validSubscriptionControlIDs(account, app, sub) != nil {
		return zero, ErrInvalidArgument
	}
	enabled := *req.Enabled
	req.Enabled = &enabled
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return zero, ErrNotFound
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	st := m.subscriptionRatePreviewStateLocked(account, app, sub, now)
	pending := make([]subscriptionRatePreviewPending, 0, api.EventSubscriptionRatePreviewSampleMax+1)
	for _, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if work.Delivered || !work.SnapshotCaptured || !sameMemUUID(eventRoutingAccount(work), account) {
			continue
		}
		for _, recipient := range work.RecipientSnapshot {
			if !sameMemUUID(recipient.AppID, app) || !sameMemUUID(recipient.ID, sub) || len(recipient.Workflow) != 0 || eventRecipientRoutingStateLocked(work, recipient.ID) != "pending" {
				continue
			}
			var identity struct {
				Source string `json:"source"`
				ID     string `json:"id"`
			}
			if err := json.Unmarshal(work.Payload, &identity); err != nil {
				return zero, err
			}
			progress := work.RecipientProgress[recipient.ID]
			at := work.AvailableAt
			if progress.NextAttemptAt != nil {
				at = *progress.NextAttemptAt
			}
			leased := work.LeaseUntil.After(now)
			if row := work.routingRecipients[recipient.ID]; row != nil {
				if work.RecipientClaims || row.LeaseUntil.After(now) {
					leased = row.LeaseUntil.After(now)
				}
				at = row.AvailableAt
			}
			pending = appendRatePreviewPending(pending, subscriptionRatePreviewPending{source: identity.Source, id: identity.ID, accepted: work.CreatedAt, available: at, deadline: EventDeliveryDeadline(recipient, work.CreatedAt, progress), outbox: work.ID, leased: leased})
		}
	}
	return buildSubscriptionRatePreview(app, sub, req, st, pending, now), nil
}

func appendRatePreviewPending(rows []subscriptionRatePreviewPending, item subscriptionRatePreviewPending) []subscriptionRatePreviewPending {
	index := sort.Search(len(rows), func(i int) bool {
		return rows[i].accepted.After(item.accepted) || rows[i].accepted.Equal(item.accepted) && rows[i].outbox >= item.outbox
	})
	bound := api.EventSubscriptionRatePreviewSampleMax + 1
	if index >= bound {
		return rows
	}
	if len(rows) < bound {
		rows = append(rows, subscriptionRatePreviewPending{})
	}
	copy(rows[index+1:], rows[index:len(rows)-1])
	rows[index] = item
	return rows
}

func (m *MemStore) subscriptionRatePreviewStateLocked(account, app, sub string, now time.Time) subscriptionRatePreviewState {
	c := m.eventSubscriptionControls[canonicalMemUUID(sub)]
	var st subscriptionRatePreviewState
	st.current, st.credit, _ = subscriptionRateMem(c, now)
	st.circuit = "closed"
	configured := 0
	if c != nil {
		st.paused = c.Paused
		st.rate = c.Rate
		st.count = c.WindowCount
		st.window = c.WindowAt
		configured = c.MaxPendingDeliveries
		st.clockInvalid = c.RateEnabled && c.RateRefilledAt.After(now) || c.Rate > 0 && c.WindowAt.After(now)
	}
	if circuit := m.eventCircuitBreakers[canonicalMemUUID(sub)]; circuit != nil {
		st.circuit = circuit.Runtime.State
	}
	consumer, application, acct := m.eventDeliveryCountsLocked(eventDeliverySlot{account, app, canonicalMemUUID(sub)}, nil)
	st.capacity = subscriptionRatePreviewCapacity(m.eventDeliveryLimitsLocked(account), configured, [3]int64{consumer, application, acct})
	return st
}
