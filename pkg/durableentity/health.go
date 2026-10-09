// adr: 847
package durableentity

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// HealthSample contains observations only, never payloads or ownership authority.
type HealthSample struct {
	AlarmPending, AlarmExhausted    bool
	AlarmAt                         *time.Time
	OutboxPending, OutboxUnknownAge int
	OutboxExhausted                 bool
	OldestOutboxAt                  *time.Time
}

type HealthPage struct {
	Samples    []HealthSample
	NextCursor string
	Failed     int
}

func (m *Manager) CheckHealthDiscovery(ctx context.Context) error {
	_, err := m.entityPrefixes(ctx, "", api.DurableEntityHealthScanPageSize)
	return err
}

// ScanHealth reads one bounded page. It does not dispatch, acquire ownership,
// repair indexes or mutate state. Lists and completed rotations are observations.
func (m *Manager) ScanHealth(ctx context.Context, cursor string, allowed func(ID) bool) (HealthPage, error) {
	if allowed == nil {
		return HealthPage{}, ErrInvalid
	}
	page, err := m.entityPrefixes(ctx, cursor, api.DurableEntityHealthScanPageSize)
	if err != nil {
		return HealthPage{}, err
	}
	out := HealthPage{NextCursor: page.NextCursor}
	for _, prefix := range page.Prefixes {
		if err := ctx.Err(); err != nil {
			return HealthPage{}, err
		}
		sample, include, err := m.healthAtPrefix(ctx, prefix, allowed)
		if err != nil {
			out.Failed++
		} else if include {
			out.Samples = append(out.Samples, sample)
		}
	}
	return out, nil
}

func (m *Manager) healthAtPrefix(ctx context.Context, prefix string, allowed func(ID) bool) (HealthSample, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityHealthReadTimeout)
	defer cancel()
	base, err := m.manifestAtPrefix(ctx, prefix)
	if err != nil {
		return HealthSample{}, false, err
	}
	if !allowed(base.ID) {
		return HealthSample{}, false, nil
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return HealthSample{}, false, err
	}
	return healthSample(base, state), true, nil
}

func healthSample(base manifest, state snapshot) HealthSample {
	out := HealthSample{AlarmPending: state.AlarmAt != nil, AlarmAt: copyTime(state.AlarmAt), OutboxPending: len(state.Outbox), AlarmExhausted: base.AlarmDelivery != nil && base.AlarmDelivery.Attempts == api.MaxDurableEntityAlarmAttempts, OutboxExhausted: base.OutboxDelivery != nil && base.OutboxDelivery.Attempts == api.MaxDurableEntityOutboxAttempts}
	for _, message := range state.Outbox {
		if message.CommittedAt == nil {
			out.OutboxUnknownAge++
			continue
		}
		if out.OldestOutboxAt == nil || message.CommittedAt.Before(*out.OldestOutboxAt) {
			out.OldestOutboxAt = copyTime(message.CommittedAt)
		}
	}
	return out
}
