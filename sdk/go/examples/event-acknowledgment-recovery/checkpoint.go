package recovery

import (
	"context"
	"database/sql"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

// RecoveryAlertHost belongs to the application. Hold one fenced writer across
// load/watch/final-save. SaveCheckpoint must atomically commit the whole snapshot
// and reject stale writers; reconcile uncertain saves against current storage.
// Deliver must deduplicate by account/app, incident ID and sequence. The ack
// ledger is durable and nondestructive until its checkpoint receipt is saved.
type RecoveryAlertHost interface {
	SaveCheckpoint(context.Context, faas.EventAcknowledgmentIntentCheckpoint) error
	Acknowledgments(context.Context, faas.EventAcknowledgmentIntentCheckpoint) ([]faas.EventAcknowledgmentIntentAcknowledgment, error)
	Deliver(context.Context, string, string, faas.EventAcknowledgmentIntentNotification) error
	RecordAcknowledgment(context.Context, faas.EventAcknowledgmentIntentAcknowledgment) error
}

// WatchRecoveryAlerts resumes a checkpoint loaded by the current fenced owner.
// The host bounds each storage/transport call and retains ownership until final
// returned state is reconciled/persisted with an independent shutdown context.
func WatchRecoveryAlerts(ctx context.Context, db *sql.DB, accountID, appID string, resume *faas.EventAcknowledgmentIntentCheckpoint, host RecoveryAlertHost) (faas.EventAcknowledgmentIntentCheckpointWatchResult, error) {
	if host == nil {
		return faas.EventAcknowledgmentIntentCheckpointWatchResult{}, faas.ErrInvalidEventConsumerRequest
	}
	return faas.WatchEventAcknowledgmentIntentHealthWithCheckpoint(ctx, db, accountID, appID,
		faas.EventAcknowledgmentIntentCheckpointWatchOptions{
			EventAcknowledgmentIntentWatchOptions: faas.EventAcknowledgmentIntentWatchOptions{
				DueThreshold: 100, StalledThreshold: 1, ParkedThreshold: 1, Sustain: 30 * time.Second,
			},
			Resume: resume, PollAcknowledgments: host.Acknowledgments,
			OnProgress: func(ctx context.Context, progress faas.EventAcknowledgmentIntentCheckpointWatchResult) error {
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
					if err := host.RecordAcknowledgment(ctx, faas.EventAcknowledgmentIntentAcknowledgment{IncidentID: notification.IncidentID, Sequence: notification.Sequence}); err != nil {
						return err
					}
				}
				return nil
			},
		})
}
