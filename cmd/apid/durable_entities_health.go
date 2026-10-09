// adr: 847
package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type durableEntityHealthRotation struct {
	cursor                                                                string
	failed                                                                bool
	entities, alarms, outbox, alarmExhausted, outboxExhausted, unknownAge int
	oldestAlarm, oldestOutbox                                             *time.Time
}

func earlierHealthTime(old, value *time.Time) *time.Time {
	if value != nil && (old == nil || value.Before(*old)) {
		at := *value
		return &at
	}
	return old
}

func (r *durableEntityHealthRotation) add(page durableentity.HealthPage) {
	r.failed = r.failed || page.Failed > 0
	r.cursor = page.NextCursor
	for _, sample := range page.Samples {
		r.entities++
		if sample.AlarmPending {
			r.alarms++
		}
		if sample.AlarmExhausted {
			r.alarmExhausted++
		}
		if sample.OutboxExhausted {
			r.outboxExhausted++
		}
		r.outbox += sample.OutboxPending
		r.unknownAge += sample.OutboxUnknownAge
		r.oldestAlarm = earlierHealthTime(r.oldestAlarm, sample.AlarmAt)
		r.oldestOutbox = earlierHealthTime(r.oldestOutbox, sample.OldestOutboxAt)
	}
}

func (s *server) runDurableEntityHealth(ctx context.Context) {
	if s.durableEntities == nil || !s.durableEntityHealthEnabled {
		return
	}
	if m := s.durableEntityMetrics; m != nil {
		m.health.enabled.WithLabelValues().Set(1)
	}
	rotation := durableEntityHealthRotation{}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.pollDurableEntityHealth(ctx, &rotation)
		timer.Reset(api.DurableEntityHealthPollInterval)
	}
}

func (s *server) pollDurableEntityHealth(ctx context.Context, rotation *durableEntityHealthRotation) {
	if m := s.durableEntityMetrics; m != nil {
		m.health.enabled.WithLabelValues().Set(1)
	}
	scanCtx, cancel := context.WithTimeout(ctx, api.DurableEntityHealthScanTimeout)
	defer cancel()
	page, err := s.durableEntities.ScanHealth(scanCtx, rotation.cursor, func(id durableentity.ID) bool { return s.durableEntityApps[id.AppID] })
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		if m := s.durableEntityMetrics; m != nil {
			m.health.pollSuccess.WithLabelValues().Set(0)
			m.health.scans.WithLabelValues("failed").Inc()
		}
		return
	}
	rotation.add(page)
	if m := s.durableEntityMetrics; m != nil {
		outcome := "success"
		success := 1.0
		if rotation.failed {
			success = 0
			outcome = "partial"
		}
		m.health.pollSuccess.WithLabelValues().Set(success)
		m.health.scans.WithLabelValues(outcome).Inc()
		if rotation.cursor == "" && !rotation.failed {
			m.health.publish(*rotation, time.Now())
		}
	}
	if rotation.cursor == "" {
		*rotation = durableEntityHealthRotation{}
	}
}
