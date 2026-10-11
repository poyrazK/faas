package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
)

func (m *MemStore) eventExecutionRecoveryCandidatesLocked(ctx context.Context, account, app string, req api.EventRecoveryRequest, now time.Time) ([]memEventRecoveryItem, error) {
	if !m.eventRecoveryAppLocked(account, app) {
		return nil, ErrNotFound
	}
	items := []memEventRecoveryItem{}
	for _, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !sameMemUUID(eventRoutingAccount(work), account) {
			continue
		}
		var event eventcontract.Envelope
		if json.Unmarshal(work.Payload, &event) != nil {
			continue
		}
		if req.EventSource != "" && req.EventSource != event.Source || req.EventType != "" && req.EventType != event.Type {
			continue
		}
		recipients := map[string]PublishedEventRecipient{}
		for _, r := range work.RecipientSnapshot {
			recipients[r.ID] = r
		}
		for id, r := range work.routingRecipients {
			recipients[id] = r.Recipient
		}
		for _, r := range recipients {
			if !sameMemUUID(r.AppID, app) || len(r.Workflow) > 0 || r.ObjectNotification != nil || req.SubscriptionID != "" && req.SubscriptionID != r.ID || eventRecipientRoutingStateLocked(work, r.ID) != PublishedEventRecipientEnqueued {
				continue
			}
			root := PublishedEventInvocationID(event.AccountID, event.Source, event.ID, r.ID)
			var latest *Invocation
			for _, inv := range m.invocations {
				if !sameMemUUID(inv.AccountID, account) || !sameMemUUID(inv.AppID, app) || inv.ID != root && inv.ReplayRootInvocationID != root && inv.ReplayedFromInvocationID != root {
					continue
				}
				if latest == nil || executionRecoveryLatest(inv, *latest) {
					copy := inv
					latest = &copy
				}
			}
			if latest == nil || latest.State != InvocationFailed && latest.State != InvocationDeadLetter || req.Outcome != "" && req.Outcome != string(latest.State) || latest.EnvironmentID != "" || InvocationHasOperation(*latest) {
				continue
			}
			if _, _, linked := m.operationForInvocationLocked(latest.ID); linked {
				continue
			}
			failedAt := latest.CreatedAt
			if latest.CompletedAt != nil {
				failedAt = *latest.CompletedAt
			}
			if failedAt.After(now.Add(-time.Duration(req.MinAgeSeconds)*time.Second)) || m.plainReplayChildren[latest.ID].ChildID != "" || m.keyedReplayChildren[latest.ID] != "" {
				continue
			}
			if latest.WorkExpiresAt != nil && !latest.WorkExpiresAt.After(now) || latest.StartDeadlineAt != nil && !latest.StartDeadlineAt.After(now) {
				continue
			}
			dlq := ""
			if latest.State == InvocationDeadLetter {
				for _, ev := range m.deadLetterEventsLocked(app) {
					if ev.Source == "invocation" && ev.SourceID == latest.ID && ev.ReplayedAt == nil {
						dlq = ev.ID
						break
					}
				}
				if dlq == "" {
					continue
				}
			} else if !plainReplayAllowed(*latest) && !keyedReplayAllowed(*latest) {
				continue
			} else if latest.WorkExpiresAt != nil && !latest.WorkExpiresAt.After(now) || latest.StartDeadlineAt != nil && !latest.StartDeadlineAt.After(now) {
				continue
			}
			encoded, err := json.Marshal(executionRecoveryIdentityFor(*latest, dlq))
			if err != nil {
				return nil, err
			}
			items = append(items, memEventRecoveryItem{OutboxID: work.ID, ExpectedProgress: encoded, EventRecoveryItem: api.EventRecoveryItem{InvocationID: latest.ID, EventSource: event.Source, EventID: event.ID, EventType: event.Type, SubscriptionID: r.ID, FailedAt: failedAt, FailureCode: string(latest.State), Retryable: true, State: "pending"}})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].OutboxID != items[j].OutboxID {
			return items[i].OutboxID < items[j].OutboxID
		}
		return items[i].SubscriptionID < items[j].SubscriptionID
	})
	if len(items) > api.EventRecoveryRecipientsMax+1 {
		items = items[:api.EventRecoveryRecipientsMax+1]
	}
	for i := range items {
		items[i].Position = int64(i + 1)
	}
	return items, nil
}
func (m *MemStore) processEventExecutionRecoveryLocked(ctx context.Context, job *memEventRecoveryJob, item *memEventRecoveryItem, now time.Time) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if !m.eventRecoveryAppLocked(job.AccountID, job.Job.AppID) {
		return "skipped", "target_unavailable", nil
	}
	retained := false
	for _, work := range m.eventFanout {
		if work.ID == item.OutboxID && sameMemUUID(eventRoutingAccount(work), job.AccountID) {
			retained = true
			break
		}
	}
	if !retained {
		return "skipped", "receipt_expired", nil
	}
	var identity executionRecoveryIdentity
	if err := json.Unmarshal(item.ExpectedProgress, &identity); err != nil {
		return "", "", err
	}
	parent, ok := m.invocations[identity.InvocationID]
	if !ok || !sameMemUUID(parent.AccountID, job.AccountID) || !sameMemUUID(parent.AppID, job.Job.AppID) || !identity.matches(parent) || m.plainReplayChildren[parent.ID].ChildID != "" || m.keyedReplayChildren[parent.ID] != "" {
		return "skipped", "changed", nil
	}
	if parent.WorkExpiresAt != nil && !parent.WorkExpiresAt.After(now) || parent.StartDeadlineAt != nil && !parent.StartDeadlineAt.After(now) {
		return "skipped", "expired", nil
	}
	plain, keyed := executionRecoveryOptions(parent, now)
	var err error
	var replay Invocation
	switch {
	case parent.State == InvocationDeadLetter:
		_, err = m.replayDeadLetterEventLocked(job.AccountID, job.Job.AppID, identity.DeadLetterID)
		replay = m.invocations[parent.ID]
	case parent.WorkPolicyName != "":
		replay, err = m.replayKeyedInvocationLocked(job.AccountID, parent.ID, keyed)
	default:
		replay, err = m.replayPlainInvocationLocked(job.AccountID, parent.ID, plain)
	}
	if err == nil {
		item.ReplayInvocationID = replay.ID
		generation := replay.ReplayGeneration
		item.ReplayGeneration = &generation
		item.ReplayCreatedAt = replay.CreatedAt
	}
	return executionRecoveryError(err)
}

// In-place dead-letter replay starts a new execution generation without changing
// CreatedAt. Compare its replay time before breaking ties by row identity.
func executionRecoveryLatest(a, b Invocation) bool {
	at, bt := a.CreatedAt, b.CreatedAt
	if a.LastReplayedAt != nil {
		at = *a.LastReplayedAt
	}
	if b.LastReplayedAt != nil {
		bt = *b.LastReplayedAt
	}
	if !at.Equal(bt) {
		return at.After(bt)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	if a.ReplayGeneration != b.ReplayGeneration {
		return a.ReplayGeneration > b.ReplayGeneration
	}
	return a.ID > b.ID
}
