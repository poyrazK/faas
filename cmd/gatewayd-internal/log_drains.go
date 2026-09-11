package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/logdrain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const appLogDrainReconcileInterval = 30 * time.Second
const appLogDrainHealthFlushInterval = 15 * time.Second
const appLogDrainSecretSealLabel = "APP_LOG_DRAIN_AUTH_HEADER"

const (
	appLogDrainHealthUnknown  = "unknown"
	appLogDrainHealthHealthy  = "healthy"
	appLogDrainHealthDegraded = "degraded"
	appLogDrainHealthInactive = "inactive"
)

type appLogDrainStore interface {
	ListEnabledAppLogDrains(context.Context) ([]state.AppLogDrain, error)
}

type appLogDrainHealthStore interface {
	UpsertAppLogDrainHealth(context.Context, state.AppLogDrainHealth) error
}

type appLogDrainLookupStore interface {
	AppLogDrainByID(context.Context, string) (state.AppLogDrain, error)
}

type appLogDrainManager struct {
	store    appLogDrainStore
	resolver logStreamerResolver
	unseal   func([]byte) (string, error)
	metrics  *gateway.Metrics
	log      *slog.Logger

	mu       sync.Mutex
	workers  map[string]*appLogDrainWorker
	active   map[string]int
	healthMu sync.Mutex
	health   map[string]state.AppLogDrainHealth
}

type appLogDrainWorker struct {
	spec   state.AppLogDrain
	cancel context.CancelFunc
}

func newAppLogDrainManager(store appLogDrainStore, resolver logStreamerResolver, unseal func([]byte) (string, error), metrics *gateway.Metrics, log *slog.Logger) *appLogDrainManager {
	if log == nil {
		log = slog.Default()
	}
	return &appLogDrainManager{
		store: store, resolver: resolver, unseal: unseal, metrics: metrics, log: log,
		workers: make(map[string]*appLogDrainWorker),
		active:  make(map[string]int),
		health:  make(map[string]state.AppLogDrainHealth),
	}
}

func (m *appLogDrainManager) Run(ctx context.Context) {
	m.reconcile(ctx)
	m.flushHealth(ctx)
	ticker := time.NewTicker(appLogDrainHealthFlushInterval)
	defer ticker.Stop()
	reconcileTicker := time.NewTicker(appLogDrainReconcileInterval)
	defer reconcileTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			m.flushHealth(flushCtx)
			cancel()
			return
		case <-reconcileTicker.C:
			m.reconcile(ctx)
		case <-ticker.C:
			m.flushHealth(ctx)
		}
	}
}

func (m *appLogDrainManager) reconcile(ctx context.Context) {
	rows, err := m.store.ListEnabledAppLogDrains(ctx)
	if err != nil {
		m.log.WarnContext(ctx, "list enabled app log drains", slog.String("err", err.Error()))
		return
	}
	desired := make(map[string]state.AppLogDrain, len(rows))
	for _, row := range rows {
		desired[row.ID] = row
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for id, worker := range m.workers {
		spec, ok := desired[id]
		if ok && sameAppLogDrainSpec(worker.spec, spec) {
			continue
		}
		worker.cancel()
		m.setActiveLocked(worker.spec, -1)
		delete(m.workers, id)
	}
	for _, spec := range desired {
		if _, exists := m.workers[spec.ID]; exists {
			continue
		}
		m.ensureHealth(spec)
		worker, err := m.startWorkerLocked(ctx, spec)
		if err != nil {
			m.markHealthStartFailure(spec, err)
			m.log.WarnContext(ctx, "start app log drain", slog.String("drain_id", spec.ID), slog.String("app_id", spec.AppID), slog.String("err", err.Error()))
			continue
		}
		m.workers[spec.ID] = worker
	}
}

func (m *appLogDrainManager) startWorkerLocked(parent context.Context, spec state.AppLogDrain) (*appLogDrainWorker, error) {
	authHeader := ""
	if len(spec.AuthHeaderSealed) > 0 {
		if m.unseal == nil {
			return nil, errors.New("host age identities are unavailable")
		}
		var err error
		authHeader, err = m.unseal(spec.AuthHeaderSealed)
		if err != nil {
			return nil, err
		}
	}
	m.ensureHealth(spec)
	sender, err := logdrain.New(logdrain.Config{
		Kind:       logdrain.Kind(spec.Kind),
		TargetURL:  spec.TargetURL,
		AuthHeader: authHeader,
		OnDropped: func(logdrain.Record) {
			m.metrics.IncLogDrainDropped(spec.AppID, string(spec.Kind))
			m.updateHealth(spec.ID, func(health *state.AppLogDrainHealth) {
				health.DroppedTotal++
				if health.Active {
					health.Status = appLogDrainHealthDegraded
				}
				health.LastError = "delivery queue dropped records"
			})
		},
		OnDelivered: func(logdrain.Record) {
			m.metrics.ObserveLogDrainDelivered(spec.AppID, string(spec.Kind))
			at := time.Now().UTC()
			m.metrics.SetLogDrainLastSuccess(spec.AppID, string(spec.Kind), at)
			m.updateHealth(spec.ID, func(health *state.AppLogDrainHealth) {
				health.DeliveredTotal++
				health.LastSuccessAt = at
				// A later 2xx clears a transient endpoint failure, but it
				// cannot repair records already lost to a full queue or a
				// source-ring gap. Keep those durable loss signals degraded.
				if health.Active && health.DroppedTotal == 0 && health.GapsTotal == 0 {
					health.Status = appLogDrainHealthHealthy
					health.LastError = ""
				}
			})
		},
		OnFailed: func(_ logdrain.Record, err error) {
			m.metrics.ObserveLogDrainFailed(spec.AppID, string(spec.Kind))
			at := time.Now().UTC()
			m.metrics.SetLogDrainLastFailure(spec.AppID, string(spec.Kind), at)
			m.updateHealth(spec.ID, func(health *state.AppLogDrainHealth) {
				health.FailedTotal++
				health.LastFailureAt = at
				if health.Active {
					health.Status = appLogDrainHealthDegraded
				}
				health.LastError = appLogDrainErrorSummary(err)
			})
			m.log.Warn("customer log drain delivery failed",
				slog.String("drain_id", spec.ID),
				slog.String("app_id", spec.AppID),
				slog.String("kind", string(spec.Kind)),
				slog.String("err", err.Error()),
			)
		},
		OnQueueDepth: func(depth, capacity int) {
			m.metrics.SetLogDrainQueue(spec.AppID, string(spec.Kind), depth, capacity)
			m.updateHealth(spec.ID, func(health *state.AppLogDrainHealth) {
				health.QueueDepth = depth
				health.QueueCapacity = capacity
			})
		},
		OnRetry: func(_ logdrain.Record, _ int) {
			m.metrics.ObserveLogDrainRetry(spec.AppID, string(spec.Kind))
			m.updateHealth(spec.ID, func(health *state.AppLogDrainHealth) { health.RetriesTotal++ })
		},
		OnDeliveredLatency: func(_ logdrain.Record, latency time.Duration) {
			m.metrics.ObserveLogDrainDeliveryLatency(spec.AppID, string(spec.Kind), latency)
		},
	})
	if err != nil {
		return nil, err
	}
	m.metrics.InitializeLogDrain(spec.AppID, string(spec.Kind))
	workerCtx, cancel := context.WithCancel(parent)
	worker := &appLogDrainWorker{spec: spec, cancel: cancel}
	m.setActiveLocked(spec, 1)
	go sender.Run(workerCtx)
	go m.streamWorker(workerCtx, spec, sender)
	return worker, nil
}

func (m *appLogDrainManager) streamWorker(ctx context.Context, spec state.AppLogDrain, sender *logdrain.Sender) {
	lastSeq := make(map[string]int64)
	backoff := time.Second
	streamEstablished := false
	for {
		if ctx.Err() != nil {
			return
		}
		streamer, err := m.resolver.ScheddForApp(ctx, spec.AppID)
		if err == nil && streamer == nil {
			err = errors.New("log stream resolver returned nil streamer")
		}
		if err == nil {
			stream, streamErr := streamer.StreamAppLogs(ctx, spec.AppID, 0, time.Time{}, "", "", "")
			if streamErr == nil {
				if streamEstablished {
					m.metrics.ObserveLogDrainStreamReconnect(spec.AppID, string(spec.Kind))
					m.updateHealth(spec.ID, func(health *state.AppLogDrainHealth) { health.StreamReconnectsTotal++ })
				}
				streamEstablished = true
				backoff = time.Second
				for {
					frame, recvErr := stream.Recv()
					if recvErr != nil {
						if errors.Is(recvErr, io.EOF) || ctx.Err() == nil {
							m.log.DebugContext(ctx, "customer log drain stream ended", slog.String("drain_id", spec.ID), slog.String("app_id", spec.AppID), slog.String("err", recvErr.Error()))
						}
						break
					}
					if frame.IsGap {
						m.metrics.IncLogDrainGap(spec.AppID, string(spec.Kind))
						m.updateHealth(spec.ID, func(health *state.AppLogDrainHealth) {
							health.GapsTotal++
							if health.Active {
								health.Status = appLogDrainHealthDegraded
							}
							health.LastError = "source log gap observed"
						})
						continue
					}
					if frame.InstanceID == "" || frame.Seq <= lastSeq[frame.InstanceID] {
						continue
					}
					lastSeq[frame.InstanceID] = frame.Seq
					sender.Enqueue(logdrain.Record{
						AppID: spec.AppID, AccountID: spec.AccountID, InstanceID: frame.InstanceID,
						Sequence: uint64(frame.Seq), Stream: frame.Stream, Line: frame.Line, WrittenAt: frame.WrittenAt,
					})
				}
			} else {
				err = streamErr
			}
		}
		if err != nil && ctx.Err() == nil {
			m.log.WarnContext(ctx, "customer log drain stream unavailable", slog.String("drain_id", spec.ID), slog.String("app_id", spec.AppID), slog.String("err", err.Error()))
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (m *appLogDrainManager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, worker := range m.workers {
		worker.cancel()
		m.setActiveLocked(worker.spec, -1)
		delete(m.workers, id)
	}
}

func (m *appLogDrainManager) setActiveLocked(spec state.AppLogDrain, delta int) {
	key := spec.AppID + "\x00" + string(spec.Kind)
	m.active[key] += delta
	if m.active[key] <= 0 {
		delete(m.active, key)
	}
	m.metrics.SetLogDrainActive(spec.AppID, string(spec.Kind), m.active[key] > 0)
	active := m.active[key] > 0
	m.updateHealth(spec.ID, func(health *state.AppLogDrainHealth) {
		health.Active = active
		if !active {
			health.Status = appLogDrainHealthInactive
		}
	})
}

func (m *appLogDrainManager) ensureHealth(spec state.AppLogDrain) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	health, ok := m.health[spec.ID]
	if !ok {
		health = state.AppLogDrainHealth{DrainID: spec.ID, Status: appLogDrainHealthUnknown}
	}
	health.Active = true
	if health.Status == appLogDrainHealthInactive {
		health.Status = appLogDrainHealthUnknown
	}
	health.UpdatedAt = time.Now().UTC()
	m.health[spec.ID] = health
}

func (m *appLogDrainManager) markHealthStartFailure(spec state.AppLogDrain, err error) {
	m.updateHealth(spec.ID, func(health *state.AppLogDrainHealth) {
		health.Active = false
		health.Status = appLogDrainHealthDegraded
		health.LastError = appLogDrainErrorSummary(err)
	})
}

func (m *appLogDrainManager) updateHealth(drainID string, update func(*state.AppLogDrainHealth)) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	health := m.health[drainID]
	if health.DrainID == "" {
		health.DrainID = drainID
		health.Status = appLogDrainHealthUnknown
	}
	update(&health)
	health.UpdatedAt = time.Now().UTC()
	m.health[drainID] = health
}

func (m *appLogDrainManager) flushHealth(ctx context.Context) {
	store, ok := m.store.(appLogDrainHealthStore)
	if !ok {
		return
	}
	m.healthMu.Lock()
	snapshots := make([]state.AppLogDrainHealth, 0, len(m.health))
	for _, health := range m.health {
		snapshots = append(snapshots, health)
	}
	m.healthMu.Unlock()
	for _, health := range snapshots {
		if err := store.UpsertAppLogDrainHealth(ctx, health); err != nil {
			if lookup, ok := m.store.(appLogDrainLookupStore); ok {
				_, lookupErr := lookup.AppLogDrainByID(ctx, health.DrainID)
				if errors.Is(lookupErr, state.ErrNotFound) {
					m.forgetHealth(health.DrainID)
					continue
				}
			}
			m.log.WarnContext(ctx, "persist app log drain health", slog.String("drain_id", health.DrainID), slog.String("err", err.Error()))
		}
	}
}

func (m *appLogDrainManager) forgetHealth(drainID string) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	delete(m.health, drainID)
}

func appLogDrainErrorSummary(err error) string {
	if err == nil {
		return "delivery failed"
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "endpoint returned status"):
		return "endpoint returned an unsuccessful HTTP status"
	case strings.Contains(message, "post:"):
		return "endpoint request failed"
	case strings.Contains(message, "build request"):
		return "endpoint request could not be built"
	default:
		return "delivery failed"
	}
}

func sameAppLogDrainSpec(a, b state.AppLogDrain) bool {
	return a.ID == b.ID && a.AppID == b.AppID && a.AccountID == b.AccountID && a.Kind == b.Kind && a.TargetURL == b.TargetURL && a.Enabled == b.Enabled && bytes.Equal(a.AuthHeaderSealed, b.AuthHeaderSealed)
}

func newAppLogDrainUnsealer(hostKeyDir string) (func([]byte) (string, error), error) {
	if hostKeyDir == "" {
		return nil, nil
	}
	identities, err := secretbox.LoadHostKeys(hostKeyDir)
	if err != nil {
		return nil, err
	}
	return func(sealed []byte) (string, error) {
		namespace, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
		if err != nil {
			return "", err
		}
		if namespace != appLogDrainSecretSealLabel {
			return "", errors.New("sealed log drain credential namespace mismatch")
		}
		if len(plaintext) > 4096 {
			return "", errors.New("sealed log drain credential exceeds size limit")
		}
		return string(plaintext), nil
	}, nil
}
