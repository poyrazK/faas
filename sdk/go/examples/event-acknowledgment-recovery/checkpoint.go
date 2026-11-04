package recovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"
)

// RecoveryAlertHost belongs to the application. Hold one fenced writer across
// load/watch/final-save. SaveCheckpoint must atomically commit the whole snapshot
// and reject stale writers; reconcile uncertain saves against current storage.
// Deliver must deduplicate by account/app, incident ID and sequence. The ack
// ledger is durable and nondestructive until its checkpoint receipt is saved.
type RecoveryAlertHost interface {
	SaveCheckpoint(context.Context, preview.EventAcknowledgmentIntentCheckpoint) error
	Acknowledgments(context.Context, preview.EventAcknowledgmentIntentCheckpoint) ([]preview.EventAcknowledgmentIntentAcknowledgment, error)
	Deliver(context.Context, string, string, preview.EventAcknowledgmentIntentNotification) error
	RecordAcknowledgment(context.Context, preview.EventAcknowledgmentIntentAcknowledgment) error
}

// WatchRecoveryAlerts resumes a checkpoint loaded by the current fenced owner.
// The host bounds each storage/transport call and retains ownership until final
// returned state is reconciled/persisted with an independent shutdown context.
func WatchRecoveryAlerts(ctx context.Context, db *sql.DB, accountID, appID string, resume *preview.EventAcknowledgmentIntentCheckpoint, host RecoveryAlertHost) (preview.EventAcknowledgmentIntentCheckpointWatchResult, error) {
	if host == nil {
		return preview.EventAcknowledgmentIntentCheckpointWatchResult{}, preview.ErrInvalidEventConsumerRequest
	}
	return preview.WatchEventAcknowledgmentIntentHealthWithCheckpoint(ctx, db, accountID, appID,
		preview.EventAcknowledgmentIntentCheckpointWatchOptions{
			EventAcknowledgmentIntentWatchOptions: preview.EventAcknowledgmentIntentWatchOptions{
				DueThreshold: 100, StalledThreshold: 1, ParkedThreshold: 1, Sustain: 30 * time.Second,
			},
			Resume: resume, PollAcknowledgments: host.Acknowledgments,
			OnProgress: func(ctx context.Context, progress preview.EventAcknowledgmentIntentCheckpointWatchResult) error {
				checkpoint := progress.Checkpoint
				if err := host.SaveCheckpoint(ctx, checkpoint); err != nil {
					return err // A failed save can have committed; no notification follows.
				}
				for _, notification := range checkpoint.Pending {
					if err := ctx.Err(); err != nil {
						return err
					}
					if err := host.Deliver(ctx, checkpoint.AccountID, checkpoint.AppID, notification); err != nil {
						return err
					}
					if err := ctx.Err(); err != nil {
						return err
					}
					if err := host.RecordAcknowledgment(ctx, preview.EventAcknowledgmentIntentAcknowledgment{IncidentID: notification.IncidentID, Sequence: notification.Sequence}); err != nil {
						return err
					}
				}
				return nil
			},
		})
}
