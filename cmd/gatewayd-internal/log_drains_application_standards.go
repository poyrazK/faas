package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logdrain"
	"github.com/onebox-faas/faas/pkg/state"
)

// A worker retains at most one pending receipt for its loaded projection. It
// retains source identity and sequence, never the delivered log or credentials.
type appLogDrainStandardObserver struct {
	mu       sync.Mutex
	spec     state.AppLogDrain
	source   string
	sequence uint64
	done     bool
	busy     bool
}

func (m *appLogDrainManager) newStandardLogObserver(spec state.AppLogDrain) *appLogDrainStandardObserver {
	if spec.StandardBinding == nil {
		return nil
	}
	if _, ok := m.store.(state.ApplicationStandardLogDeliveryStore); !ok {
		return nil
	}
	b := *spec.StandardBinding
	spec.StandardBinding = &b
	spec.AuthHeaderSealed = append([]byte(nil), spec.AuthHeaderSealed...)
	return &appLogDrainStandardObserver{spec: spec}
}

func (o *appLogDrainStandardObserver) delivered(r logdrain.Record) {
	if o == nil || r.InstanceID == "" || r.Sequence == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return
	}
	o.source, o.sequence = r.InstanceID, r.Sequence
}

func (m *appLogDrainManager) flushStandardLogDeliveries(ctx context.Context) {
	store, ok := m.store.(state.ApplicationStandardLogDeliveryStore)
	if !ok {
		return
	}
	m.mu.Lock()
	observers := make([]*appLogDrainStandardObserver, 0, len(m.workers))
	for _, w := range m.workers {
		if !w.stopping && w.standardObserver != nil {
			observers = append(observers, w.standardObserver)
		}
	}
	m.mu.Unlock()
	budget, cancel := context.WithTimeout(ctx, api.ApplicationStandardLogReceiptTimeout)
	defer cancel()
	for _, o := range observers {
		if budget.Err() != nil {
			return
		}
		o.flush(budget, store, m.log)
	}
}

func (o *appLogDrainStandardObserver) flush(ctx context.Context, store state.ApplicationStandardLogDeliveryStore, log *slog.Logger) {
	o.mu.Lock()
	if o.done || o.busy || o.sequence == 0 {
		o.mu.Unlock()
		return
	}
	o.busy = true
	source, sequence := o.source, o.sequence
	o.mu.Unlock()
	_, err := store.RecordApplicationStandardLogDelivery(ctx, o.spec, source, sequence)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.busy = false
	if err == nil {
		o.done = true
		return
	}
	if errors.Is(err, state.ErrApplicationStandardLogDeliveryStale) || errors.Is(err, state.ErrInvalidArgument) {
		// A queued record's source can be erased before delivery. Reject that
		// candidate without suppressing later delivery from a current source.
		if o.source == source && o.sequence == sequence {
			o.source, o.sequence = "", 0
		}
		return
	}
	// Do not expose storage errors: they may contain internal configuration.
	log.WarnContext(ctx, "persist standard log delivery receipt", slog.String("drain_id", o.spec.ID), slog.String("code", "storage_unavailable"))
}

func sameAppLogDrainStandardBinding(a, b *state.ApplicationStandardLogDrainBinding) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// A replacement must wait for both writers to stop before reopening the same
// durable queue. Reconcile retains the retiring worker without blocking.
func (m *appLogDrainManager) retireLogDrainWorkerLocked(w *appLogDrainWorker) bool {
	if !w.stopping {
		w.stopping = true
		w.cancel()
		m.setActiveLocked(w.spec, -1)
	}
	if w.done == nil {
		return true
	}
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (m *appLogDrainManager) runLogDrainWorker(ctx context.Context, w *appLogDrainWorker, sender *logdrain.Sender) {
	w.done = make(chan struct{})
	var joined sync.WaitGroup
	joined.Add(2)
	go func() { defer joined.Done(); sender.Run(ctx) }()
	go func() { defer joined.Done(); m.streamWorker(ctx, w.spec, sender) }()
	go func() { joined.Wait(); close(w.done) }()
}
