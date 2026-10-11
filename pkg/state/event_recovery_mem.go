package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memEventRecoveryItem struct {
	ReplayCreatedAt time.Time
	api.EventRecoveryItem
	OutboxID         int64
	ExpectedProgress []byte
	RoutingOrder     int64
	SnapshotPosition int
}
type memEventRecoveryJob struct {
	NotificationRetryReceipts     map[string][]byte
	NotificationReceipts          map[string]recoveryNotificationReceipt
	NotificationDeliveryIDs       map[string]map[string]string
	ExecutionNotificationCaptured bool
	NextExecutionNotificationAt   time.Time

	CapacityScope          string
	CapacityWaitStartedAt  *time.Time
	CapacityWaitObservedAt *time.Time
	NotificationCaptured   bool
	LastProgressAt         *time.Time
	WaitReason             string
	History                []api.EventRecoveryHistoryEntry
	AccountID              string
	Job                    api.EventRecoveryJob
	Items                  []memEventRecoveryItem
	NextAttemptAt          time.Time
	WindowStartedAt        time.Time
	WindowCount            int
}

func (m *MemStore) eventRecoveryAppLocked(accountID, appID string) bool {
	app, ok := m.eventSubscriptionAppLocked(appID)
	return ok && sameMemUUID(app.AccountID, accountID) && app.Status != AppDeleted
}
func (m *MemStore) eventRecoveryCandidatesLocked(ctx context.Context, accountID, appID string, req api.EventRecoveryRequest, now time.Time) ([]memEventRecoveryItem, error) {
	if req.ParentJobID != "" {
		return m.eventRecoveryRetryCandidatesLocked(ctx, accountID, appID, req, now)
	}
	if req.Mode == "execution" {
		return m.eventExecutionRecoveryCandidatesLocked(ctx, accountID, appID, req, now)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.eventRecoveryAppLocked(accountID, appID) {
		return nil, ErrNotFound
	}
	items := []memEventRecoveryItem{}
	before := now.Add(-time.Duration(req.MinAgeSeconds) * time.Second)
	for _, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var event publishedEventIdentity
		if json.Unmarshal(work.Payload, &event) != nil {
			continue
		}
		if req.EventSource != "" && event.Source != req.EventSource || req.EventType != "" && event.Type != req.EventType {
			continue
		}
		for recipientIndex, recipient := range work.RecipientSnapshot {
			if !sameMemUUID(recipient.AccountID, accountID) || !sameMemUUID(recipient.AppID, appID) || len(recipient.Workflow) != 0 || req.SubscriptionID != "" && recipient.ID != req.SubscriptionID {
				continue
			}
			if EventDeliveryExpired(recipient, work.CreatedAt, PublishedEventRecipientProgress{}, now) {
				continue
			}
			progress, ok := work.RecipientProgress[recipient.ID]
			if !ok || progress.State != PublishedEventRecipientFailed || !req.IncludeNonRetryable && !progress.Retryable {
				continue
			}
			failedAt := progress.UpdatedAt
			if failedAt.IsZero() {
				failedAt = work.CreatedAt
			}
			code := progress.FailureCode
			if code == "" {
				code = EventFanoutFailureCodeUnknown
			}
			if failedAt.After(before) || req.FailureCode != "" && code != req.FailureCode {
				continue
			}
			encoded, err := memEventRecoveryIdentity(progress)
			if err != nil {
				return nil, err
			}
			routingOrder := work.ID
			if recipient.Work != nil && recipient.Work.RoutingOrder != 0 {
				routingOrder = recipient.Work.RoutingOrder
			}
			items = append(items, memEventRecoveryItem{RoutingOrder: routingOrder, SnapshotPosition: recipientIndex, OutboxID: work.ID, ExpectedProgress: encoded, EventRecoveryItem: api.EventRecoveryItem{EventSource: event.Source, EventID: event.ID, EventType: event.Type, SubscriptionID: recipient.ID, FailedAt: failedAt, FailureCode: eventHistoryText(code, api.EventRoutingHistoryCodeMaxBytes), Retryable: progress.Retryable, State: "pending"}})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.RoutingOrder != b.RoutingOrder {
			return a.RoutingOrder < b.RoutingOrder
		}
		if a.OutboxID != b.OutboxID {
			return a.OutboxID < b.OutboxID
		}
		return a.SnapshotPosition < b.SnapshotPosition
	})
	if len(items) > api.EventRecoveryRecipientsMax+1 {
		items = items[:api.EventRecoveryRecipientsMax+1]
	}
	for i := range items {
		items[i].Position = int64(i + 1)
	}
	return items, nil
}
func (m *MemStore) PreviewEventRecovery(ctx context.Context, accountID, appID string, req api.EventRecoveryRequest) (api.EventRecoveryPreview, error) {
	if err := normalizeEventRecovery(accountID, appID, &req); err != nil {
		return api.EventRecoveryPreview{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	items, err := m.eventRecoveryCandidatesLocked(ctx, accountID, appID, req, now)
	if err != nil {
		return api.EventRecoveryPreview{}, err
	}
	out := api.EventRecoveryPreview{ObservedAt: now, Coverage: eventRecoveryCoverage(req), MatchedCount: int64(len(items)), ExceedsJobLimit: len(items) > api.EventRecoveryRecipientsMax, Sample: []api.EventRecoveryItem{}}
	for _, item := range items[:min(len(items), api.EventRecoveryPreviewLimit)] {
		out.Sample = append(out.Sample, item.EventRecoveryItem)
	}
	return out, nil
}
func (m *MemStore) CreateEventRecovery(ctx context.Context, accountID, appID string, req api.EventRecoveryRequest) (api.EventRecoveryJob, error) {
	if req.ParentJobID != "" && req.RequestID == "" {
		return api.EventRecoveryJob{}, fmt.Errorf("%w: request_id is required for a child recovery", ErrEventRecoveryQuery)
	}
	if err := normalizeEventRecovery(accountID, appID, &req); err != nil {
		return api.EventRecoveryJob{}, err
	}
	ctx, err := WithEventRecoveryReason(ctx, req.Reason)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	req.Reason = ""
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryJob{}, err
	}
	if !m.eventRecoveryAppLocked(accountID, appID) {
		return api.EventRecoveryJob{}, ErrNotFound
	}
	if req.RequestID != "" {
		for _, existing := range m.eventRecoveryJobs {
			if sameMemUUID(existing.AccountID, accountID) && existing.Job.Selection.RequestID == req.RequestID {
				if !sameMemUUID(existing.Job.AppID, appID) || existing.Job.Selection != req {
					return api.EventRecoveryJob{}, ErrEventRecoveryRequestConflict
				}
				return m.eventRecoveryObservedResponseLocked(existing, now), nil
			}
		}
	}
	if req.ParentJobID != "" {
		if _, err := m.eventRecoveryRetryParentLocked(accountID, appID, req.ParentJobID); err != nil {
			return api.EventRecoveryJob{}, err
		}
	}
	active := 0
	for _, job := range m.eventRecoveryJobs {
		if sameMemUUID(job.AccountID, accountID) && eventRecoveryActive(job.Job.State) {
			active++
		}
	}
	if active >= api.EventRecoveryActiveJobsMax {
		return api.EventRecoveryJob{}, ErrEventRecoveryQuota
	}
	items, err := m.eventRecoveryCandidatesLocked(ctx, accountID, appID, req, now)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	if len(items) > api.EventRecoveryRecipientsMax {
		return api.EventRecoveryJob{}, ErrEventRecoverySelection
	}
	job := &memEventRecoveryJob{AccountID: canonicalMemUUID(accountID), Items: items, NextAttemptAt: now, Job: api.EventRecoveryJob{RatePerSecond: req.RatePerSecond, ID: uuid.NewString(), AppID: canonicalMemUUID(appID), Coverage: eventRecoveryCoverage(req), Selection: req, State: "running", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(api.EventRecoveryJobLifetime)}}
	if m.eventRecoveryJobs == nil {
		m.eventRecoveryJobs = map[string]*memEventRecoveryJob{}
	}
	m.eventRecoveryJobs[job.Job.ID] = job
	_ = memEventRecoveryResponse(job)
	memRecoveryHistory(ctx, job, "created", "", 0, now)
	if job.Job.State == "completed" {
		m.enqueueRecoveryNotificationLocked(job, "completed")
	}
	return m.eventRecoveryObservedResponseLocked(job, time.Now().UTC()), nil
}
func memEventRecoveryResponse(job *memEventRecoveryJob) api.EventRecoveryJob {
	out := job.Job
	out.SelectedCount = int64(len(job.Items))
	out.PendingCount, out.QueuedCount, out.SkippedCount, out.CancelledCount = 0, 0, 0, 0
	for _, item := range job.Items {
		switch item.State {
		case "pending":
			out.PendingCount++
		case "queued":
			out.QueuedCount++
		case "skipped":
			out.SkippedCount++
		case "cancelled":
			out.CancelledCount++
		}
	}
	if out.PendingCount == 0 && out.State == "running" {
		job.Job.State = "completed"
		t := job.Job.UpdatedAt
		job.Job.CompletedAt = &t
		out.State = "completed"
		out.CompletedAt = &t
	}
	if out.PausedAt != nil {
		t := *out.PausedAt
		out.PausedAt = &t
	}
	out.ExecutionFinishedAt = cloneEventReceiptTime(out.ExecutionFinishedAt)
	if out.CompletedAt != nil {
		t := *out.CompletedAt
		out.CompletedAt = &t
	}
	return out
}
func (m *MemStore) eventRecoveryJobLocked(accountID, jobID string) (*memEventRecoveryJob, error) {
	if err := eventRecoveryIDs(accountID, jobID); err != nil {
		return nil, err
	}
	job := m.eventRecoveryJobs[canonicalMemUUID(jobID)]
	if job == nil || !sameMemUUID(job.AccountID, accountID) {
		return nil, ErrNotFound
	}
	app, ok := m.eventSubscriptionAppLocked(job.Job.AppID)
	if !ok || !sameMemUUID(app.AccountID, accountID) {
		return nil, ErrNotFound
	}
	return job, nil
}
func (m *MemStore) GetEventRecovery(ctx context.Context, accountID, jobID string) (api.EventRecoveryJob, error) {
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryJob{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, err := m.eventRecoveryJobLocked(accountID, jobID)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	return m.eventRecoveryObservedResponseLocked(job, time.Now().UTC()), nil
}
func (m *MemStore) memCancelEventRecovery(ctx context.Context, job *memEventRecoveryJob, now time.Time, reason string) {
	if !eventRecoveryActive(job.Job.State) {
		return
	}
	previous := job.Job.State
	for i := range job.Items {
		if job.Items[i].State == "pending" {
			job.Items[i].State = "cancelled"
			job.Items[i].Reason = reason
		}
	}
	job.Job.State = "cancelled"
	job.Job.PausedAt = nil
	job.Job.UpdatedAt = now
	job.Job.CompletedAt = &now
	action := "cancelled"
	if reason == "expired" {
		action = "expired"
	}
	memRecoveryHistory(ctx, job, action, previous, job.Job.RatePerSecond, now)
	m.enqueueRecoveryNotificationLocked(job, action)
}
func (m *MemStore) CancelEventRecovery(ctx context.Context, accountID, jobID string) (api.EventRecoveryJob, error) {
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryJob{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, err := m.eventRecoveryJobLocked(accountID, jobID)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	m.memCancelEventRecovery(ctx, job, time.Now().UTC(), "cancelled")
	return m.eventRecoveryObservedResponseLocked(job, time.Now().UTC()), nil
}
func (m *MemStore) ListEventRecoveryItems(ctx context.Context, accountID, jobID string, after int64, limit int) (api.EventRecoveryItems, error) {
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryItems{}, err
	}
	if after < 0 || limit < 1 || limit > api.EventRecoveryItemsPageMax {
		return api.EventRecoveryItems{}, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, err := m.eventRecoveryJobLocked(accountID, jobID)
	if err != nil {
		return api.EventRecoveryItems{}, err
	}
	out := api.EventRecoveryItems{JobID: job.Job.ID, Items: []api.EventRecoveryItem{}}
	now := time.Now().UTC()
	attempts := map[recoveryAttemptKey]InvocationAttempt{}
	if job.Job.Selection.Mode == "execution" {
		attempts = m.recoveryAttemptIndexLocked(job.AccountID, job.Job.AppID, now)
	}
	for _, item := range job.Items {
		if item.Position <= after {
			continue
		}
		if len(out.Items) == limit {
			out.NextAfter = out.Items[len(out.Items)-1].Position
			break
		}
		out.Items = append(out.Items, m.eventRecoveryObservedItemWithAttemptsLocked(job, item, now, attempts))
	}
	return out, nil
}
func (m *MemStore) ProcessNextEventRecovery(ctx context.Context, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var job *memEventRecoveryJob
	for _, candidate := range m.eventRecoveryJobs {
		if !eventRecoveryActive(candidate.Job.State) || candidate.Job.ExpiresAt.After(now) && (candidate.Job.State != "running" || candidate.NextAttemptAt.After(now) || candidate.WindowStartedAt.Add(time.Second).After(now) && candidate.WindowCount >= candidate.Job.RatePerSecond) {
			continue
		}
		if job == nil || memRecoveryDueAt(candidate, now).Before(memRecoveryDueAt(job, now)) || memRecoveryDueAt(candidate, now).Equal(memRecoveryDueAt(job, now)) && candidate.Job.ID < job.Job.ID {
			job = candidate
		}
	}
	if job == nil {
		return false, nil
	}
	if !job.Job.ExpiresAt.After(now) {
		m.memCancelEventRecovery(ctx, job, now, "expired")
		return true, nil
	}
	for i := range job.Items {
		item := &job.Items[i]
		if item.State != "pending" {
			continue
		}
		if job.Job.Selection.Mode == "execution" {
			state, reason, err := m.processEventExecutionRecoveryLocked(ctx, job, item, now)
			if err != nil {
				return false, err
			}
			if state == "pending" {
				if job.WaitReason != "capacity" || job.CapacityWaitStartedAt == nil {
					t := now
					job.CapacityWaitStartedAt = &t
				}
				t := now
				job.CapacityWaitObservedAt = &t
				job.CapacityScope = reason
				reason = ""
			}
			item.State, item.Reason = state, reason
		} else {
			var work *PublishedEventWork
			for _, candidate := range m.eventFanout {
				if candidate.ID == item.OutboxID {
					work = candidate
					break
				}
			}
			switch {
			case work == nil:
				item.State, item.Reason = "skipped", "receipt_expired"
			case !m.eventRecoveryAppLocked(job.AccountID, job.Job.AppID):
				item.State, item.Reason = "skipped", "target_unavailable"
			default:
				progress := work.RecipientProgress[item.SubscriptionID]
				encoded, err := memEventRecoveryIdentity(progress)
				if err != nil {
					return false, err
				}
				if !bytes.Equal(encoded, item.ExpectedProgress) || eventRecipientRoutingStateLocked(work, item.SubscriptionID) != PublishedEventRecipientFailed || work.RecipientClaims && work.routingRecipients[item.SubscriptionID] == nil {
					item.State, item.Reason = "skipped", "changed"
					break
				}
				if !work.RecipientClaims && !work.Delivered && work.ClaimToken != "" {
					job.WaitReason = "legacy_claim"
					job.NextAttemptAt = now.Add(time.Second)
					job.Job.UpdatedAt = now
					return true, nil
				}
				expired := false
				for _, recipient := range work.RecipientSnapshot {
					if recipient.ID == item.SubscriptionID && EventDeliveryExpired(recipient, work.CreatedAt, PublishedEventRecipientProgress{}, now) {
						expired = true
						break
					}
				}
				if expired {
					item.State, item.Reason = "skipped", "expired"
					break
				}
				progress.DeliveryAgeOverride = false
				replay := progress
				replay.State = PublishedEventRecipientPending
				replay.UpdatedAt = now
				m.appendEventFanoutAttemptLocked(work, item.SubscriptionID, EventFanoutAttemptActionReplay, replay)
				progress.State = PublishedEventRecipientPending
				progress.FailureCode = ""
				progress.Retryable = false
				progress.LastError = ""
				progress.CapacityScope = ""
				progress.NextAttemptAt = nil
				progress.UpdatedAt = now
				work.RecipientProgress[item.SubscriptionID] = progress
				if work.RecipientClaims {
					resetEventRecipientForReplay(work, item.SubscriptionID, now)
				}
				work.Delivered = false
				work.DeliveredAt = time.Time{}
				work.AvailableAt = now
				// Independent recipient replay must preserve another consumer's lease.
				if !work.RecipientClaims {
					work.ClaimToken = ""
					work.LeaseUntil = time.Time{}
				}
				item.State = "queued"
			}
		}
		if item.State == "queued" && item.ReplayInvocationID != "" {
			m.captureRecoveryInvocationResultLocked(m.invocations[item.ReplayInvocationID], now)
		}
		job.Job.UpdatedAt = now
		if !job.WindowStartedAt.Add(time.Second).After(now) {
			job.WindowStartedAt = now
			job.WindowCount = 0
		}
		job.WindowCount++
		job.NextAttemptAt = now
		if job.WindowCount >= job.Job.RatePerSecond {
			job.NextAttemptAt = job.WindowStartedAt.Add(time.Second)
		}
		if item.State == "pending" {
			job.WaitReason = "capacity"
			job.NextAttemptAt = now.Add(EventDeliveryCapacityRetryDelay)
		}
		if item.State != "pending" {
			t := now
			job.LastProgressAt = &t
			job.WaitReason = ""
			job.CapacityScope = ""
			job.CapacityWaitStartedAt = nil
			job.CapacityWaitObservedAt = nil
		}
		memEventRecoveryResponse(job)
		if job.Job.State == "completed" {
			m.enqueueRecoveryNotificationLocked(job, "completed")
		}
		return true, nil
	}
	memEventRecoveryResponse(job)
	if job.Job.State == "completed" {
		m.enqueueRecoveryNotificationLocked(job, "completed")
	}
	return true, nil
}
func (m *MemStore) PruneEventRecoveries(ctx context.Context, now time.Time, limit int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if limit < 1 || limit > api.EventRecoveryItemsPageMax {
		return 0, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	for id, job := range m.eventRecoveryJobs {
		if removed >= int64(limit) {
			break
		}
		if job.Job.CompletedAt != nil && job.Job.CompletedAt.Before(now.Add(-api.EventRecoveryJobRetention)) {
			for key := range m.eventRecoveryExecutionResults {
				if key.JobID == id {
					delete(m.eventRecoveryExecutionResults, key)
				}
			}
			delete(m.eventRecoveryJobs, id)
			removed++
		}
	}
	return removed, nil
}

func memEventRecoveryIdentity(progress PublishedEventRecipientProgress) ([]byte, error) {
	return json.Marshal(PublishedEventRecipientProgress{State: progress.State, Attempts: progress.Attempts, UpdatedAt: progress.UpdatedAt, FailureCode: progress.FailureCode, Retryable: progress.Retryable})
}

// eventRecoveryReceiptHoldsLocked observes immutable opt-ins and pending items.
// The expiry check releases holds even if a worker has not finalized the job.
// An empty account selects all accounts for the global receipt pruner.
func (m *MemStore) eventRecoveryReceiptHoldsLocked(account string, now time.Time) map[int64]struct{} {
	holds := map[int64]struct{}{}
	for _, job := range m.eventRecoveryJobs {
		if account != "" && !sameMemUUID(job.AccountID, account) || !job.Job.Selection.ProtectReceipts || !eventRecoveryActive(job.Job.State) || !job.Job.ExpiresAt.After(now) {
			continue
		}
		for _, item := range job.Items {
			if item.State == "pending" {
				holds[item.OutboxID] = struct{}{}
			}
		}
	}
	return holds
}
