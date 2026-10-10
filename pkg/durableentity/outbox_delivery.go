// adr: 933
package durableentity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrOutboxObsolete  = errors.New("durable entity outbox head changed")
	ErrOutboxBackoff   = errors.New("durable entity outbox retry is not due")
	ErrOutboxExhausted = errors.New("durable entity outbox attempt budget exhausted")
)

// Only the FIFO head has a reservation. This bounds manifest metadata and
// keeps a full snapshot drainable without adding retry bytes to that snapshot.
// An exhausted head remains a private dead letter, blocking this entity only.
type outboxDelivery struct {
	MessageID     string    `json:"message_id"`
	Token         string    `json:"attempt_token"`
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
}

func validOutboxDelivery(value manifest) bool {
	d := value.OutboxDelivery
	return d == nil || value.Schema >= 6 && value.Version > 0 && validUUID(d.MessageID) && validUUID(d.Token) &&
		d.Attempts > 0 && d.Attempts <= api.MaxDurableEntityOutboxAttempts && validAlarm(&d.NextAttemptAt)
}

func outboxRetryDelay(attempt int) time.Duration {
	delay := api.DurableEntityOutboxRetryBase
	for i := 1; i < attempt && delay < api.DurableEntityOutboxRetryMax; i++ {
		delay = min(delay*2, api.DurableEntityOutboxRetryMax)
	}
	return delay
}

// OutboxReservation is private dispatch authority under an unexpired entity
// Claim. Token fences acknowledgements from earlier attempts, including replay.
type OutboxReservation struct {
	Message OutboxMessage
	Token   string `json:"-"`
}

// ReserveOutbox durably consumes an attempt before handing work to a relay.
// Uncertain writes never authorize acceptance. A committed reservation survives
// process death, ownership changes and ordinary business transitions.
func (m *Manager) ReserveOutbox(ctx context.Context, claim Claim, messageID string) (OutboxReservation, error) {
	base, etag, err := m.owned(ctx, claim)
	if err != nil {
		return OutboxReservation{}, err
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return OutboxReservation{}, m.restoreFailure(ctx, claim, base, err)
	}
	if len(state.Outbox) == 0 || state.Outbox[0].ID != messageID {
		return OutboxReservation{}, ErrOutboxObsolete
	}
	attempts := 0
	if d := base.OutboxDelivery; d != nil {
		if d.Attempts >= api.MaxDurableEntityOutboxAttempts {
			return OutboxReservation{}, ErrOutboxExhausted
		}
		if m.now().Before(d.NextAttemptAt) {
			return OutboxReservation{}, ErrOutboxBackoff
		}
		attempts = d.Attempts
	}
	d := &outboxDelivery{MessageID: messageID, Token: uuid.NewString(), Attempts: attempts + 1,
		NextAttemptAt: m.now().UTC().Add(outboxRetryDelay(attempts + 1))}
	if !validAlarm(&d.NextAttemptAt) {
		return OutboxReservation{}, ErrLimit
	}
	base.OutboxDelivery = d
	if !m.now().Before(base.ExpiresAt) {
		return OutboxReservation{}, ErrStaleOwner
	}
	if err := m.putManifest(ctx, base, etag); err != nil {
		return OutboxReservation{}, err
	}
	m.publishOutboxHint(ctx, base, state)
	return OutboxReservation{Message: state.Outbox[0], Token: d.Token}, nil
}

// RelayOutbox accepts one head message into a durable deduplicating transport,
// then acknowledges it. accept must deduplicate by Message.ID and must return
// nil only after durable acceptance; it must never directly send to a receiver.
// External delivery remains at least once. No guest code runs in this path.
func (m *Manager) RelayOutbox(ctx context.Context, id ID, messageID, owner string, accept func(context.Context, OutboxMessage) error) error {
	if !id.valid() || !validUUID(messageID) || !validIdentity(owner) || accept == nil {
		return ErrInvalid
	}
	unlock, err := m.lock(ctx, id.prefix()+"invocations/")
	if err != nil {
		return err
	}
	defer unlock()
	claim, err := m.Acquire(ctx, id, owner)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DurableEntityReleaseTimeout)
		defer cancel()
		_ = m.Release(cleanup, claim)
	}()
	reservation, err := m.ReserveOutbox(ctx, claim, messageID)
	if err != nil {
		return err
	}
	current, _, err := m.owned(ctx, claim)
	if err != nil {
		return err
	}
	if !matchingOutboxReservation(current, messageID, reservation.Token) {
		return ErrOutboxObsolete
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := accept(ctx, reservation.Message); err != nil {
		return err
	}
	return m.AcknowledgeOutbox(ctx, claim, messageID, reservation.Token)
}

// RetryOutbox re-arms an exhausted FIFO head without changing its identity or
// payload. A previously accepted delivery is therefore never enqueued twice.
// This is trusted operator authority, not an unauthenticated customer endpoint.
func (m *Manager) RetryOutbox(ctx context.Context, claim Claim, messageID string) error {
	base, etag, err := m.owned(ctx, claim)
	if err != nil {
		return err
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return m.restoreFailure(ctx, claim, base, err)
	}
	if len(state.Outbox) == 0 || state.Outbox[0].ID != messageID {
		return ErrOutboxObsolete
	}
	if base.OutboxDelivery == nil || base.OutboxDelivery.Attempts != api.MaxDurableEntityOutboxAttempts {
		return ErrInvalid
	}
	base.OutboxDelivery = nil
	if !m.now().Before(base.ExpiresAt) {
		return ErrStaleOwner
	}
	if err := m.putManifest(ctx, base, etag); err != nil {
		return err
	}
	m.publishOutboxHint(ctx, base, state)
	return nil
}

// OutboxStatus omits payloads and tokens. Counts describe this exact entity,
// not a fleet backlog. Acceptance and receiver delivery are separate stages.
type OutboxStatus struct {
	Version       uint64     `json:"state_version"`
	Pending       int        `json:"pending"`
	HeadID        string     `json:"head_id,omitempty"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	Exhausted     bool       `json:"exhausted"`
}

func (m *Manager) InspectOutbox(ctx context.Context, id ID) (OutboxStatus, error) {
	base, _, err := m.readManifest(ctx, id)
	if err != nil {
		return OutboxStatus{}, err
	}
	state, err := m.readSnapshot(ctx, base)
	if errors.Is(err, ErrNotFound) {
		latest, _, readErr := m.readManifest(ctx, id)
		if readErr != nil {
			return OutboxStatus{}, readErr
		}
		if latest.SnapshotKey != base.SnapshotKey {
			return OutboxStatus{}, ErrConflict
		}
	}
	if err != nil {
		return OutboxStatus{}, err
	}
	status := OutboxStatus{Version: state.Version, Pending: len(state.Outbox)}
	if len(state.Outbox) > 0 {
		status.HeadID = state.Outbox[0].ID
	}
	if d := base.OutboxDelivery; d != nil {
		status.Attempts, status.NextAttemptAt = d.Attempts, copyTime(&d.NextAttemptAt)
		status.Exhausted = d.Attempts == api.MaxDurableEntityOutboxAttempts
	}
	return status, nil
}
