package state

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Expiry admission is independent of this repair queue. Lost worker time cannot
// extend an approval. Queueing preserves the last installed/observed revision.
func (m *MemStore) queueExpiredStandardExceptionsLocked(now time.Time) {
	keys := []string{}
	for k, e := range m.applicationStandardEnrollments {
		if (e.State == "persisted" || e.State == "observed") && e.ExceptionExpiresAt != nil && !now.Before(*e.ExceptionExpiresAt) && e.DesiredRevision < api.ApplicationStandardMaxVersion {
			for _, app := range m.apps {
				if sameStandardUUID(app.ID, e.AppID) && app.Status != AppDeleted {
					keys = append(keys, k)
					break
				}
			}
		}
	}
	slices.Sort(keys)
	if len(keys) > api.ApplicationStandardWorkerPassLimit {
		keys = keys[:api.ApplicationStandardWorkerPassLimit]
	}
	for _, key := range keys {
		before := m.applicationStandardEnrollments[key]
		m.standardExceptionPendingLocked(key, before, now)
		data, _ := json.Marshal(map[string]any{"org_id": canonicalStandardUUID(before.OrgID), "app_id": canonicalStandardUUID(before.AppID), "previous_revision": before.DesiredRevision, "desired_revision": before.DesiredRevision + 1, "exception_expires_at": before.ExceptionExpiresAt})
		m.appendAuditLogLocked(AuditLog{ID: uuid.New(), Kind: "application_standard.exception_expiry_queued", ReceivedAt: now, Data: data})
	}
}

func (s *PgStore) queueExpiredStandardExceptions(ctx context.Context) error {
	_, err := sqlc.New().QueueExpiredApplicationStandardExceptions(ctx, s.pool, sqlc.QueueExpiredApplicationStandardExceptionsParams{MaxRevision: api.ApplicationStandardMaxVersion, PassLimit: api.ApplicationStandardWorkerPassLimit})
	return err
}
