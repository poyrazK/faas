package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func newRecoveryNotificationHealth() *api.EventRecoveryNotificationsHealth {
	return &api.EventRecoveryNotificationsHealth{Coverage: "bounded_retained_notification_jobs", CountsComplete: true, JobLimit: api.EventRecoveryNotificationHealthJobsMax, OverdueGraceSeconds: int64(api.EventRecoveryNotificationOverdueGrace / time.Second), Admission: api.EventRecoveryNotificationHealthCounts{CountsComplete: true}, Execution: api.EventRecoveryNotificationHealthCounts{CountsComplete: true}, Jobs: []api.EventRecoveryNotificationJobHealth{}}
}

func appendRecoveryNotificationHealth(out *api.EventRecoveryNotificationsHealth, report api.EventRecoveryNotifications, now time.Time) {
	out.ObservedJobs++
	for _, notice := range report.Notifications {
		if notice.CaptureStatus == "pending" || notice.CaptureStatus == "not_applicable" {
			continue
		}
		counts := &out.Admission
		if notice.Kind == "execution" {
			counts = &out.Execution
		}
		job := api.EventRecoveryNotificationJobHealth{JobID: report.JobID, Kind: notice.Kind, Event: notice.Event, CaptureStatus: notice.CaptureStatus, AcknowledgementStatus: notice.AcknowledgementStatus, EvidenceSource: notice.EvidenceSource, CapturedAt: cloneEventReceiptTime(notice.CapturedAt), Dead: notice.DeadCount > 0, Unknown: notice.CaptureStatus == "unknown" || notice.AcknowledgementStatus == "unknown" || !notice.CountsComplete, NoReceivers: notice.AcknowledgementStatus == "no_receivers"}
		unacknowledged := notice.PendingCount + notice.InFlightCount + notice.FailedCount + notice.DeadCount + notice.AwaitingRelayCount
		if unacknowledged > 0 && notice.CapturedAt != nil {
			age := max(0, now.Sub(*notice.CapturedAt).Seconds())
			job.UnacknowledgedAgeSeconds = &age
			job.Overdue = now.Sub(*notice.CapturedAt) >= api.EventRecoveryNotificationOverdueGrace
		}
		if job.Overdue {
			counts.OverdueJobs++
		}
		if job.Dead {
			counts.DeadJobs++
		}
		if job.Unknown {
			counts.UnknownJobs++
			counts.CountsComplete = false
		}
		if job.NoReceivers {
			counts.NoReceiversJobs++
		}
		if (job.Overdue || job.Dead || job.Unknown || job.NoReceivers) && len(out.Jobs) < api.EventRecoveryNotificationHealthSampleMax {
			out.Jobs = append(out.Jobs, job)
		}
	}
}
func truncateRecoveryNotificationHealth(out *api.EventRecoveryNotificationsHealth) {
	out.CountsComplete = false
	out.Admission.CountsComplete = false
	out.Execution.CountsComplete = false
}
func observeRecoveryNotificationHealth(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app string, now time.Time) (*api.EventRecoveryNotificationsHealth, error) {
	out := newRecoveryNotificationHealth()
	ids, err := q.EventRecoveryNotificationHealthJobs(ctx, db, sqlc.EventRecoveryNotificationHealthJobsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), NowAt: pgtypeFromTime(now), ReceiverLimit: api.EventRecoveryNotificationReceiversMax, JobLimit: api.EventRecoveryNotificationHealthJobsMax + 1})
	if err != nil {
		return nil, err
	}
	if len(ids) > api.EventRecoveryNotificationHealthJobsMax {
		truncateRecoveryNotificationHealth(out)
		ids = ids[:api.EventRecoveryNotificationHealthJobsMax]
	}
	for _, id := range ids {
		report, err := getEventRecoveryNotifications(ctx, q, db, account, uuidString(id), now)
		if err != nil {
			return nil, err
		}
		appendRecoveryNotificationHealth(out, report, now)
	}
	return out, ctx.Err()
}

// Only a complete saved selection with retained successful deliveries can be
// excluded. Missing history and empty selections remain observable candidates.
func (m *MemStore) recoveryNotificationHealthCandidateLocked(entry *memEventRecoveryJob) bool {
	admission := 0
	for _, event := range recoveryNotificationEvents[:3] {
		if _, ok := entry.NotificationReceipts[event]; ok {
			admission++
		}
	}
	if admission != 1 {
		return true
	}
	if entry.Job.ExecutionFinishedAt != nil {
		if _, ok := entry.NotificationReceipts[recoveryNotificationEvents[3]]; !ok {
			return true
		}
	}
	if validateRecoveryNotificationReceipts(entry.Job.ID, entry.NotificationReceipts) != nil {
		return true
	}
	for event, receipt := range entry.NotificationReceipts {
		if len(receipt.RecipientWebhookIDs) == 0 || len(receipt.RecipientWebhookIDs) > api.EventRecoveryNotificationReceiversMax {
			return true
		}
		for _, id := range receipt.RecipientWebhookIDs {
			delivery, ok := m.appWebhookDeliveries[entry.NotificationDeliveryIDs[event][id]]
			if !ok || !sameMemUUID(delivery.AccountID, entry.AccountID) || !sameMemUUID(delivery.AppID, entry.Job.AppID) || !sameMemUUID(delivery.WebhookID, id) || string(delivery.Event) != event || string(delivery.Status) != "succeeded" {
				return true
			}
		}
	}
	return false
}
func (m *MemStore) recoveryNotificationHealthLocked(ctx context.Context, account, app string, now time.Time) (*api.EventRecoveryNotificationsHealth, error) {
	out := newRecoveryNotificationHealth()
	entries := []*memEventRecoveryJob{}
	for _, entry := range m.eventRecoveryJobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !sameMemUUID(entry.AccountID, account) || !sameMemUUID(entry.Job.AppID, app) || entry.Job.CompletedAt == nil || entry.Job.CompletedAt.After(now) || (entry.Job.State != "completed" && entry.Job.State != "cancelled") || !m.recoveryNotificationHealthCandidateLocked(entry) {
			continue
		}
		entries = append(entries, entry)
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Job.CompletedAt.Equal(*entries[j].Job.CompletedAt) {
				return entries[i].Job.ID < entries[j].Job.ID
			}
			return entries[i].Job.CompletedAt.Before(*entries[j].Job.CompletedAt)
		})
		if len(entries) > api.EventRecoveryNotificationHealthJobsMax+1 {
			entries = entries[:api.EventRecoveryNotificationHealthJobsMax+1]
		}
	}
	if len(entries) > api.EventRecoveryNotificationHealthJobsMax {
		truncateRecoveryNotificationHealth(out)
		entries = entries[:api.EventRecoveryNotificationHealthJobsMax]
	}
	for _, entry := range entries {
		report, err := m.recoveryNotificationReportLocked(ctx, entry, now)
		if err != nil {
			return nil, err
		}
		appendRecoveryNotificationHealth(out, report, now)
	}
	return out, ctx.Err()
}
