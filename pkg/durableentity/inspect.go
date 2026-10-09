// adr: 845
package durableentity

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// Inspection observes one authenticated manifest/snapshot without exposing data,
// receipts, ownership or object-storage metadata.
type Inspection struct {
	Version          uint64
	RecoveryRevision string
	Alarm            AlarmStatus
	Outbox           OutboxStatus
}

// Inspect performs no writes, index repair, claim acquisition or guest dispatch.
// Reclamation races are retryable; missing/corrupt committed state fails closed.
func (m *Manager) Inspect(ctx context.Context, id ID) (Inspection, error) {
	base, _, err := m.readManifest(ctx, id)
	if err != nil {
		return Inspection{}, err
	}
	state, err := m.readSnapshot(ctx, base)
	if errors.Is(err, ErrNotFound) {
		latest, _, readErr := m.readManifest(ctx, id)
		if readErr != nil {
			return Inspection{}, readErr
		}
		if latest.SnapshotKey != base.SnapshotKey || latest.Generation != base.Generation {
			return Inspection{}, ErrConflict
		}
	}
	if err != nil {
		return Inspection{}, err
	}
	out := Inspection{Version: state.Version, RecoveryRevision: recoveryRevision(base), Outbox: OutboxStatus{Version: state.Version, Pending: len(state.Outbox)}}
	if state.AlarmAt != nil {
		out.Alarm.Alarm = &Alarm{Entity: id, Version: state.Version, At: *copyTime(state.AlarmAt)}
	}
	if d := base.AlarmDelivery; d != nil {
		out.Alarm.Attempts, out.Alarm.NextAttemptAt = d.Attempts, copyTime(&d.NextAttemptAt)
		out.Alarm.Exhausted = d.Attempts == api.MaxDurableEntityAlarmAttempts
	}
	if len(state.Outbox) > 0 {
		out.Outbox.HeadID = state.Outbox[0].ID
	}
	if d := base.OutboxDelivery; d != nil {
		out.Outbox.Attempts, out.Outbox.NextAttemptAt = d.Attempts, copyTime(&d.NextAttemptAt)
		out.Outbox.Exhausted = d.Attempts == api.MaxDurableEntityOutboxAttempts
	}
	return out, nil
}
