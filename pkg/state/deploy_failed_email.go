package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// DeployFailedEmailCooldownStore is the durable per-app gate for I1. It is
// intentionally separate from Store so existing narrow test doubles do not
// need a new method; production PgStore and MemStore both implement it.
type DeployFailedEmailCooldownStore interface {
	// ClaimDeployFailedEmail atomically claims the next failure-email slot for
	// appID. It returns false when the app was emailed less than an hour ago.
	ClaimDeployFailedEmail(ctx context.Context, appID string, at time.Time) (bool, error)
}

// ClaimDeployFailedEmail updates the app's cooldown anchor only when the
// previous stamp is outside the one-hour window. The UPDATE ... RETURNING
// shape is the cross-replica serialization primitive; no read-then-write
// race can send duplicate notifications.
func (s *PgStore) ClaimDeployFailedEmail(ctx context.Context, appID string, at time.Time) (bool, error) {
	if appID == "" {
		return false, ErrNotFound
	}
	var claimed string
	err := s.pool.QueryRow(ctx, `
		UPDATE apps
		   SET last_deploy_failed_email_at = $2
		 WHERE id = $1
		   AND status <> 'deleted'
		   AND (last_deploy_failed_email_at IS NULL
		        OR last_deploy_failed_email_at < $2 - interval '1 hour')
		 RETURNING id`, appID, at.UTC()).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return claimed != "", nil
}
