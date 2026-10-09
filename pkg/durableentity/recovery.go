// adr: 846
package durableentity

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRecoveryObsolete = errors.New("durable entity recovery observation is stale or work is not exhausted")

// recoveryRevision is an opaque comparison value, never ownership authority.
func recoveryRevision(value manifest) string { return digest([]byte(value.Revision)) }

// Recovery selects exactly one exhausted item observed by Inspect.
type Recovery struct {
	Version   uint64
	Revision  string
	MessageID string
	AlarmAt   *time.Time
}

// RetryExhausted changes only retry metadata on an existing unowned entity.
// The manifest CAS fences acquisition, business commits and other recovery.
// It never creates state, invokes guest code or changes transport acceptance.
func (m *Manager) RetryExhausted(ctx context.Context, id ID, request Recovery) error {
	if request.Version == 0 || !validRecoveryRevision(request.Revision) || (request.MessageID == "") == (request.AlarmAt == nil) || request.MessageID != "" && !validUUID(request.MessageID) || request.AlarmAt != nil && !validAlarm(request.AlarmAt) {
		return ErrInvalid
	}
	base, etag, err := m.readManifest(ctx, id)
	if err != nil {
		return err
	}
	if base.Version != request.Version || recoveryRevision(base) != request.Revision {
		return ErrRecoveryObsolete
	}
	if base.OwnerID != "" && m.now().Before(base.ExpiresAt) {
		return ErrBusy
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return err
	}
	if err := resetExhaustedDelivery(&base, state, request); err != nil {
		return err
	}
	if err := m.putManifest(ctx, base, etag); err != nil {
		return err
	}
	if request.MessageID != "" {
		m.publishOutboxHint(ctx, base, state)
	} else {
		_ = m.putAlarmHint(ctx, Alarm{Entity: id, Version: state.Version, At: *state.AlarmAt}, nil)
	}
	return nil
}

func validRecoveryRevision(value string) bool {
	if len(value) != len(digest(nil)) {
		return false
	}
	for _, c := range value {
		decimal := c >= '0' && c <= '9'
		hexLetter := c >= 'a' && c <= 'f'
		if !decimal && !hexLetter {
			return false
		}
	}
	return true
}

func resetExhaustedDelivery(base *manifest, state snapshot, request Recovery) error {
	if request.MessageID != "" {
		if len(state.Outbox) == 0 || state.Outbox[0].ID != request.MessageID || base.OutboxDelivery == nil || base.OutboxDelivery.Attempts != api.MaxDurableEntityOutboxAttempts {
			return ErrRecoveryObsolete
		}
		base.OutboxDelivery = nil
	} else {
		if state.AlarmAt == nil || !state.AlarmAt.Equal(*request.AlarmAt) || base.AlarmDelivery == nil || base.AlarmDelivery.Attempts != api.MaxDurableEntityAlarmAttempts {
			return ErrRecoveryObsolete
		}
		base.AlarmDelivery = nil
	}
	return nil
}
