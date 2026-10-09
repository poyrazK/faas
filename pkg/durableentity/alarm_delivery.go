// adr: 712
package durableentity

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrAlarmBackoff   = errors.New("durable entity alarm retry is not due")
	ErrAlarmExhausted = errors.New("durable entity alarm attempt budget exhausted")
)

// Reservations are manifest authority, not advisory queue metadata. A crash or
// uncertain reservation consumes an attempt if its CAS actually committed.
type alarmDelivery struct {
	At            time.Time `json:"scheduled_at"`
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
}

func validAlarmDelivery(value manifest) bool {
	delivery := value.AlarmDelivery
	return delivery == nil || value.Schema >= 4 && value.Version > 0 &&
		validAlarm(&delivery.At) && validAlarm(&delivery.NextAttemptAt) &&
		delivery.Attempts > 0 && delivery.Attempts <= api.MaxDurableEntityAlarmAttempts && !delivery.NextAttemptAt.Before(delivery.At)
}

func alarmRetryDelay(attempt int) time.Duration {
	delay := api.DurableEntityAlarmRetryBase
	for i := 1; i < attempt && delay < api.DurableEntityAlarmRetryMax; i++ {
		delay = min(delay*2, api.DurableEntityAlarmRetryMax)
	}
	return delay
}

func (m *Manager) reserveAlarmAttempt(ctx context.Context, claim Claim, alarm Alarm) error {
	value, etag, err := m.owned(ctx, claim)
	if err != nil {
		return err
	}
	if value.Version != alarm.Version {
		return ErrAlarmObsolete
	}
	delivery := value.AlarmDelivery
	if delivery == nil {
		delivery = &alarmDelivery{At: alarm.At.UTC()}
	}
	if delivery.Attempts >= api.MaxDurableEntityAlarmAttempts {
		return ErrAlarmExhausted
	}
	if m.now().Before(delivery.NextAttemptAt) {
		return ErrAlarmBackoff
	}
	delivery.Attempts++
	delivery.NextAttemptAt = m.now().UTC().Add(alarmRetryDelay(delivery.Attempts))
	if !validAlarm(&delivery.NextAttemptAt) {
		return ErrLimit
	}
	value.AlarmDelivery = delivery
	if err := m.putManifest(ctx, value, etag); err != nil {
		return err
	}
	// This hint schedules recovery if the process dies during guest execution.
	_ = m.putAlarmHint(ctx, alarm, delivery)
	return nil
}

// AlarmStatus is a private operator view. Exhausted alarms retain their business
// deadline and state, with no stored provider error or customer payload.
type AlarmStatus struct {
	Alarm         *Alarm     `json:"alarm,omitempty"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	Exhausted     bool       `json:"exhausted"`
}

func (m *Manager) InspectAlarm(ctx context.Context, id ID) (AlarmStatus, error) {
	value, _, err := m.readManifest(ctx, id)
	if err != nil {
		return AlarmStatus{}, err
	}
	state, err := m.readSnapshot(ctx, value)
	if err != nil {
		return AlarmStatus{}, err
	}
	status := AlarmStatus{}
	if state.AlarmAt != nil {
		status.Alarm = &Alarm{Entity: id, Version: state.Version, At: *state.AlarmAt}
	}
	if value.AlarmDelivery != nil {
		status.Attempts = value.AlarmDelivery.Attempts
		status.NextAttemptAt = copyTime(&value.AlarmDelivery.NextAttemptAt)
		status.Exhausted = status.Attempts >= api.MaxDurableEntityAlarmAttempts
	}
	return status, nil
}
