package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgPrepareAppHealthNotification(ctx context.Context, tx pgx.Tx, claim AppHealthClaim, body []byte, previous *api.AppHealthResponse, a api.AppHealthResponse, entries []api.AppHealthHistoryEntry, now time.Time) (appHealthNotification, error) {
	var state appHealthNotificationState
	if len(body) > 0 {
		if err := json.Unmarshal(body, &state); err != nil {
			return appHealthNotification{}, fmt.Errorf("decode app health notification state: %w", err)
		}
	}
	row, err := sqlc.New().AppHealthNotificationRecipients(ctx, tx, sqlc.AppHealthNotificationRecipientsParams{
		AppID: claim.AppID, AccountID: claim.AccountID, RecipientLimit: api.AppHealthNotificationRecipients + 1})
	if err != nil {
		return appHealthNotification{}, fmt.Errorf("read app health notification recipients: %w", err)
	}
	var recipients map[string]string
	if err := json.Unmarshal(row.Recipients, &recipients); err != nil {
		return appHealthNotification{}, fmt.Errorf("decode app health notification recipients: %w", err)
	}
	return prepareAppHealthNotification(state, previous, a, entries, recipients, row.Slug, now)
}

func pgEnqueueAppHealthNotification(ctx context.Context, tx pgx.Tx, claim AppHealthClaim, n appHealthNotification) error {
	if n.SourceID == "" {
		return nil
	}
	if err := sqlc.New().EnqueueAppHealthNotification(ctx, tx, sqlc.EnqueueAppHealthNotificationParams{
		AccountID: claim.AccountID, AppID: claim.AppID, SourceID: n.SourceID, Payload: n.Payload, RecipientIds: n.Recipients}); err != nil {
		return fmt.Errorf("enqueue app health notification: %w", err)
	}
	return nil
}
