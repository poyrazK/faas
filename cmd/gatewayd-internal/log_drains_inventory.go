package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The OS lock protects all durable queues through worker shutdown, independently
// of Postgres availability or session fencing. A restart cannot share their cursors.
func (m *appLogDrainManager) acquireLogSpoolLease() error {
	if err := os.MkdirAll(m.spoolRoot, 0o750); err != nil {
		return err
	}
	l := flock.New(filepath.Join(m.spoolRoot, ".manager.lock"))
	locked, err := l.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("log drain spool already has a manager")
	}
	m.spoolLease = l
	return nil
}

func (m *appLogDrainManager) stopAllLogWorkersJoined() {
	m.shutdownMu.Lock()
	defer m.shutdownMu.Unlock()
	m.mu.Lock()
	m.stopping = true
	joined := make([]chan struct{}, 0, len(m.workers))
	for id, w := range m.workers {
		m.retireLogDrainWorkerLocked(w)
		if w.done != nil {
			joined = append(joined, w.done)
		}
		delete(m.workers, id)
	}
	m.mu.Unlock()
	for _, done := range joined {
		<-done
	}
	m.closeStandardLogConsumerJoined()
}

func (m *appLogDrainManager) closeStandardLogConsumerJoined() {
	s, ok := m.store.(state.ApplicationStandardLogConsumerClosureStore)
	if !ok || m.spoolLease == nil {
		return
	}
	m.mu.Lock()
	session := m.standardSession
	m.mu.Unlock()
	if session.Generation == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.ApplicationStandardLogReceiptTimeout)
	defer cancel()
	if _, err := s.CloseApplicationStandardLogConsumer(ctx, session); err != nil && !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		m.log.WarnContext(ctx, "persist standard logging shutdown", slog.String("code", "storage_unavailable"))
	}
}

func (m *appLogDrainManager) loadLogConsumerSnapshot(ctx context.Context) (state.ApplicationStandardLogConsumerSnapshot, error) {
	if s, ok := m.store.(state.ApplicationStandardLogInventoryStore); ok {
		return s.LoadApplicationStandardLogConsumerSnapshot(ctx)
	}
	d, err := m.store.ListEnabledAppLogDrains(ctx)
	return state.ApplicationStandardLogConsumerSnapshot{Drains: d}, err
}

func (m *appLogDrainManager) prepareStandardLogConsumerLocked(ctx context.Context, s state.ApplicationStandardLogInventoryStore) bool {
	if m.stopping || m.spoolLease == nil || m.standardNode == nil || m.standardNode.name == "" || m.standardFenced {
		return false
	}
	if m.standardSession.Generation == 0 {
		if m.standardSessionID == "" {
			m.standardSessionID = uuid.NewString()
		}
		nodeID, err := m.standardNode.Get(ctx)
		if err != nil {
			return false
		}
		session, err := s.RegisterApplicationStandardLogConsumer(ctx, nodeID, m.standardSessionID)
		if err != nil {
			return false
		}
		m.standardSession = session
	}
	err := s.CheckApplicationStandardLogConsumer(ctx, m.standardSession)
	if errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		m.standardFenced = true
		m.log.WarnContext(ctx, "standard logging consumer fenced", slog.String("code", "consumer_session_changed"))
	}
	return err == nil
}

func (m *appLogDrainManager) standardLogInventoryLoadedLocked(i state.ApplicationStandardLogInventory) bool {
	loaded := []state.ApplicationStandardLogInventoryDrain{}
	for _, w := range m.workers {
		id, err := uuid.Parse(w.spec.AppID)
		if err != nil || id.String() != i.AppID {
			continue
		}
		if w.stopping || w.done == nil {
			return false
		}
		select {
		case <-w.done:
			return false
		default:
		}
		drainID, err := uuid.Parse(w.spec.ID)
		if err != nil {
			return false
		}
		loaded = append(loaded, state.ApplicationStandardLogInventoryDrain{DrainID: drainID.String(), ConfigHash: state.ApplicationStandardLogDrainConfigHash(w.spec)})
	}
	slices.SortFunc(loaded, func(a, b state.ApplicationStandardLogInventoryDrain) int {
		return strings.Compare(a.DrainID, b.DrainID)
	})
	return slices.Equal(loaded, i.Drains)
}

func (m *appLogDrainManager) flushStandardLogInventories(ctx context.Context) {
	s, ok := m.store.(state.ApplicationStandardLogInventoryStore)
	if !ok {
		return
	}
	budget, cancel := context.WithTimeout(ctx, api.ApplicationStandardLogReceiptTimeout)
	defer cancel()
	// Keep loaded workers stable throughout the bounded storage pass.
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.prepareStandardLogConsumerLocked(budget, s) {
		return
	}
	for n := 0; n < len(m.standardInventories) && budget.Err() == nil; n++ {
		m.standardInventoryCursor %= len(m.standardInventories)
		i := m.standardInventories[m.standardInventoryCursor]
		m.standardInventoryCursor++
		if !m.standardLogInventoryLoadedLocked(i) {
			continue
		}
		_, err := s.RecordApplicationStandardLogInventory(budget, m.standardSession, i)
		if errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
			m.standardFenced = true
			return
		}
		if err != nil && !errors.Is(err, state.ErrApplicationStandardLogDeliveryStale) && !errors.Is(err, state.ErrApplicationStandardReviewBusy) {
			m.log.WarnContext(ctx, "persist standard logging inventory", slog.String("app_id", i.AppID), slog.String("code", "storage_unavailable"))
		}
	}
}

func (m *appLogDrainManager) standardLogConsumerFenced() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.standardFenced
}
