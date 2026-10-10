// adr: 933
package durableentity

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// AcknowledgeOutbox removes an accepted head through the authoritative manifest
// CAS. Business version, state, alarm reservation and receipts are preserved.
// A lost acknowledgement is resolved by rediscovery and deduplicating acceptance.
func (m *Manager) AcknowledgeOutbox(ctx context.Context, claim Claim, messageID, token string) error {
	unlock, err := m.lock(ctx, claim.ID.prefix())
	if err != nil {
		return err
	}
	defer unlock()
	base, _, err := m.owned(ctx, claim)
	if err != nil {
		return err
	}
	if !matchingOutboxReservation(base, messageID, token) {
		return ErrOutboxObsolete
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return m.restoreFailure(ctx, claim, base, err)
	}
	state.Outbox = state.Outbox[1:]
	body, err := json.Marshal(state)
	if err != nil || len(body) > api.MaxDurableEntitySnapshotBytes {
		return ErrLimit
	}
	key := fmt.Sprintf("%ssnapshots/%d/%s.json", claim.ID.prefix(), base.Generation, uuid.NewString())
	if _, err := m.store.Put(ctx, key, body, ""); err != nil {
		return writeFailure("upload outbox acknowledgement snapshot", err)
	}
	latest, etag, err := m.owned(ctx, claim)
	if err != nil {
		return err
	}
	if latest.Version != base.Version || latest.SnapshotKey != base.SnapshotKey || latest.Generation != base.Generation || !matchingOutboxReservation(latest, messageID, token) {
		return ErrConflict
	}
	latest.SnapshotKey, latest.SnapshotHash, latest.OutboxDelivery = key, digest(body), nil
	if latest.StorageUsage != nil {
		usage := *latest.StorageUsage
		usage.SnapshotBytes = int64(len(body))
		latest.StorageUsage = &usage
	}
	// Removal must remain possible when a cap was lowered below retained bytes.
	// No journal writes or new receipt counts accompany this smaller snapshot.
	if err := m.putManifest(ctx, latest, etag); err != nil {
		return err
	}
	m.publishOutboxHint(ctx, latest, state)
	return nil
}

func matchingOutboxReservation(value manifest, messageID, token string) bool {
	d := value.OutboxDelivery
	return token != "" && d != nil && d.MessageID == messageID && d.Token == token
}
