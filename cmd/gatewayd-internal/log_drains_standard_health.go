package main

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logdrain"
	"github.com/onebox-faas/faas/pkg/state"
)

type appLogDrainStandardHealthObserver struct {
	mu    sync.Mutex
	spec  state.AppLogDrain
	event state.ApplicationStandardLogHealthEvent
	loss  string
}

func (m *appLogDrainManager) newStandardLogHealthObserver(spec state.AppLogDrain) *appLogDrainStandardHealthObserver {
	if spec.StandardBinding == nil {
		return nil
	}
	if _, ok := m.store.(state.ApplicationStandardLogHealthStore); !ok {
		return nil
	}
	b := *spec.StandardBinding
	spec.StandardBinding = &b
	spec.AuthHeaderSealed = append([]byte(nil), spec.AuthHeaderSealed...)
	return &appLogDrainStandardHealthObserver{spec: spec, event: state.ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "unknown", Reason: "idle"}}
}

func (o *appLogDrainStandardHealthObserver) delivered(r logdrain.Record) {
	if o == nil || r.InstanceID == "" || r.Sequence == 0 || r.Sequence > uint64(api.ApplicationStandardMaxLogSequence) {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.event.Status == "healthy" || o.loss != "" {
		return
	}
	o.setLocked(state.ApplicationStandardLogHealthEvent{Status: "healthy", Reason: "delivered", SourceInstanceID: r.InstanceID, Sequence: int64(r.Sequence)})
}

func (o *appLogDrainStandardHealthObserver) failed(reason string, loss bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if loss && o.loss == "" {
		o.loss = reason
	}
	if o.loss != "" {
		reason = o.loss
	}
	o.setLocked(state.ApplicationStandardLogHealthEvent{Status: "degraded", Reason: reason})
}

func (o *appLogDrainStandardHealthObserver) rejected(logdrain.Record) {
	// The source cursor is retained for retry after a durable enqueue refusal.
	o.failed("queue_fault", false)
}

func (o *appLogDrainStandardHealthObserver) setLocked(e state.ApplicationStandardLogHealthEvent) {
	e.EventRevision = o.event.EventRevision
	if e == o.event || o.event.EventRevision == api.ApplicationStandardMaxLogHealthEvent {
		return
	}
	e.EventRevision++
	if e.EventRevision == api.ApplicationStandardMaxLogHealthEvent {
		e = state.ApplicationStandardLogHealthEvent{EventRevision: e.EventRevision, Status: "degraded", Reason: "reporter_exhausted"}
	}
	o.event = e
}

func (o *appLogDrainStandardHealthObserver) flush(ctx context.Context, s state.ApplicationStandardLogHealthStore, session state.ApplicationStandardLogConsumerSession) error {
	o.mu.Lock()
	e := o.event
	o.mu.Unlock()
	_, err := s.RecordApplicationStandardLogHealth(ctx, session, o.spec, e)
	if errors.Is(err, state.ErrApplicationStandardLogDeliveryStale) && e.Status == "healthy" {
		// An erased source must not prevent reporting a future current source.
		o.mu.Lock()
		if o.event == e {
			o.setLocked(state.ApplicationStandardLogHealthEvent{Status: "unknown", Reason: "idle"})
		}
		o.mu.Unlock()
	}
	return err
}

func (m *appLogDrainManager) flushStandardLogHealth(ctx context.Context) {
	s, ok := m.store.(state.ApplicationStandardLogHealthStore)
	i, hasInventory := m.store.(state.ApplicationStandardLogInventoryStore)
	if !ok || !hasInventory {
		return
	}
	budget, cancel := context.WithTimeout(ctx, api.ApplicationStandardLogReceiptTimeout)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.prepareStandardLogConsumerLocked(budget, i) {
		return
	}
	workers := m.standardLogHealthWorkersLocked()
	for n := 0; n < len(workers) && budget.Err() == nil; n++ {
		m.standardHealthCursor %= len(workers)
		w := workers[m.standardHealthCursor]
		m.standardHealthCursor++
		err := w.standardHealth.flush(budget, s, m.standardSession)
		if errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
			m.standardFenced = true
			return
		}
		if err != nil && !errors.Is(err, state.ErrApplicationStandardLogDeliveryStale) && !errors.Is(err, state.ErrApplicationStandardLogHealthStale) && !errors.Is(err, state.ErrApplicationStandardReviewBusy) {
			m.log.WarnContext(ctx, "persist standard logging health", slog.String("drain_id", w.spec.ID), slog.String("code", "storage_unavailable"))
		}
	}
}

func (m *appLogDrainManager) standardLogHealthWorkersLocked() []*appLogDrainWorker {
	workers := []*appLogDrainWorker{}
	for _, w := range m.workers {
		if w.stopping || w.standardHealth == nil || w.done == nil {
			continue
		}
		select {
		case <-w.done:
			continue
		default:
		}
		workers = append(workers, w)
	}
	slices.SortFunc(workers, func(a, b *appLogDrainWorker) int { return strings.Compare(a.spec.ID, b.spec.ID) })
	return workers
}
