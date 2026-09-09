package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/logdrain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const appLogDrainReconcileInterval = 30 * time.Second
const appLogDrainSecretSealLabel = "APP_LOG_DRAIN_AUTH_HEADER"

type appLogDrainStore interface {
	ListEnabledAppLogDrains(context.Context) ([]state.AppLogDrain, error)
}

type appLogDrainManager struct {
	store    appLogDrainStore
	resolver logStreamerResolver
	unseal   func([]byte) (string, error)
	metrics  *gateway.Metrics
	log      *slog.Logger

	mu      sync.Mutex
	workers map[string]*appLogDrainWorker
	active  map[string]int
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
	}
}

func (m *appLogDrainManager) Run(ctx context.Context) {
	m.reconcile(ctx)
	ticker := time.NewTicker(appLogDrainReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-ticker.C:
			m.reconcile(ctx)
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
		worker, err := m.startWorkerLocked(ctx, spec)
		if err != nil {
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
	sender, err := logdrain.New(logdrain.Config{
		Kind:       logdrain.Kind(spec.Kind),
		TargetURL:  spec.TargetURL,
		AuthHeader: authHeader,
		OnDropped:  func(logdrain.Record) { m.metrics.IncLogDrainDropped(spec.AppID) },
		OnDelivered: func(logdrain.Record) {
			m.metrics.ObserveLogDrainDelivered(spec.AppID, string(spec.Kind))
		},
		OnFailed: func(logdrain.Record, error) {
			m.metrics.ObserveLogDrainFailed(spec.AppID, string(spec.Kind))
		},
	})
	if err != nil {
		return nil, err
	}
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
				backoff = time.Second
				for {
					frame, recvErr := stream.Recv()
					if recvErr != nil {
						if errors.Is(recvErr, io.EOF) || ctx.Err() == nil {
							m.log.DebugContext(ctx, "customer log drain stream ended", slog.String("drain_id", spec.ID), slog.String("app_id", spec.AppID), slog.String("err", recvErr.Error()))
						}
						break
					}
					if frame.IsGap || frame.InstanceID == "" || frame.Seq <= lastSeq[frame.InstanceID] {
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
