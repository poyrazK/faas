package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	vmmdLogArchiveQueueSize  = 4096
	vmmdLogArchiveQueueBytes = 64 << 20
)

type archivedLine struct {
	instance string
	line     logbuf.Line
}

// vmmdLogArchiveSink is the non-blocking bridge between logbuf and the
// on-disk spool. Ring eviction happens under the ring mutex, so Enqueue only
// performs a bounded channel send. Disk I/O and error logging stay on the
// worker goroutine.
type vmmdLogArchiveSink struct {
	spool   *logarchive.Spool
	metrics logarchive.Metrics
	log     *slog.Logger
	queue   chan archivedLine
	done    chan struct{}

	mu          sync.RWMutex
	closed      bool
	queuedBytes int64
	catalog     state.RuntimeLogArchiveCatalog
	nodeID      string
	bindings    map[string]state.RuntimeLogArchiveBinding
}

func newVMMDLogArchiveSink(spool *logarchive.Spool, metrics logarchive.Metrics, log *slog.Logger) *vmmdLogArchiveSink {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = logarchive.NewMetrics(nil)
	}
	s := &vmmdLogArchiveSink{
		spool:   spool,
		metrics: metrics,
		log:     log,
		queue:   make(chan archivedLine, vmmdLogArchiveQueueSize),
		done:    make(chan struct{}),
	}
	go s.run()
	return s
}

// Capture runs before a VM wake, outside the ring mutex. A failed or mismatched
// lookup clears the cached identity so no stale binding can accept evictions.
func (s *vmmdLogArchiveSink) Capture(ctx context.Context, wr fcvm.WakeRequest) error {
	s.mu.Lock()
	delete(s.bindings, wr.Instance)
	s.mu.Unlock()
	b, err := s.catalog.CaptureRuntimeLogArchiveBinding(ctx, wr.Instance, s.nodeID)
	if err != nil {
		return err
	}
	if b.AppID != wr.AppID || b.AccountID != wr.AccountID || b.DeploymentID != wr.DeploymentID {
		return state.ErrInvalidArgument
	}
	s.mu.Lock()
	s.bindings[wr.Instance] = b
	s.mu.Unlock()
	return nil
}

func (s *vmmdLogArchiveSink) bootstrap(ctx context.Context, store state.Store) {
	instances, err := store.ListInstancesOnNodeID(ctx, s.nodeID)
	if err != nil {
		s.log.Warn("logarchive.binding_bootstrap_failed", "err", err)
		return
	}
	for _, i := range instances {
		b, err := s.catalog.CaptureRuntimeLogArchiveBinding(ctx, i.ID, s.nodeID)
		if err != nil {
			continue
		}
		s.mu.Lock()
		s.bindings[i.ID] = b
		s.mu.Unlock()
	}
}

func (s *vmmdLogArchiveSink) Forget(instance string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bindings, instance)
}

// Enqueue is intentionally non-blocking because it runs from Ring's
// eviction callback while the per-instance ring mutex is held.
func (s *vmmdLogArchiveSink) Enqueue(instance string, line logbuf.Line) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || (s.bindings != nil && s.bindings[instance].InstanceID == "") {
		return
	}
	lineBytes := int64(len(line.Line))
	if lineBytes > vmmdLogArchiveQueueBytes || s.queuedBytes+lineBytes > vmmdLogArchiveQueueBytes {
		s.metrics.IncFailure(logarchive.FailureReasonQueueFull)
		return
	}
	select {
	case s.queue <- archivedLine{instance: instance, line: line}:
		s.queuedBytes += lineBytes
	default:
		s.metrics.IncFailure(logarchive.FailureReasonQueueFull)
	}
}

func (s *vmmdLogArchiveSink) run() {
	defer close(s.done)
	for item := range s.queue {
		s.mu.Lock()
		s.queuedBytes -= int64(len(item.line.Line))
		s.mu.Unlock()
		if _, err := s.spool.WriteWithLevel(item.instance, item.line.Seq, item.line.Stream, item.line.WrittenAt, item.line.Line, item.line.Level); err != nil {
			if errors.Is(err, logarchive.ErrSpoolFull) {
				s.metrics.IncFailure(logarchive.FailureReasonSpoolFull)
			} else {
				s.metrics.IncFailure(logarchive.FailureReasonSpoolWrite)
			}
			s.log.Warn("logarchive.spool_write_failed",
				"instance", item.instance, "seq", item.line.Seq, "err", err)
		}
	}
}

// Close stops accepting new evictions, drains the bounded queue, and flushes
// the spool. A shutdown deadline prevents a damaged filesystem from holding
// vmmd teardown indefinitely; the process can still exit with the remaining
// queue unflushed after the deadline is reported to the caller.
func (s *vmmdLogArchiveSink) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
	s.mu.Unlock()
	select {
	case <-s.done:
		return s.spool.CloseAll()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startVMMDLogArchive wires the compute-side producer and shipper. Archive
// setup is optional: an absent credential envelope leaves vmmd serving live
// logs with archive disabled; a malformed envelope emits a clear startup
// warning, matching apid's best-effort archive policy.
func startVMMDLogArchive(ctx context.Context, log *slog.Logger, ops *wire.OpsMetrics, store state.Store, nodeID string, backend storage.StorageBackend) (*vmmdLogArchiveSink, func()) {
	cfg, err := logarchive.ConfigFromEnv(os.Getenv, log)
	if err != nil {
		log.Warn("vmmd: log archive config failed", "err", err)
		return nil, nil
	}
	credsPath := envOr(logarchive.EnvCredentialsPath, logarchive.DefaultCredentialsPath)
	creds, err := logarchive.ReadCredentials(credsPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Warn("vmmd: log archive credentials unavailable", "path", credsPath, "err", err)
		}
	} else {
		cfg = cfg.WithCredentials(creds)
	}
	// Keep vmmd's local producer spool separate from apid on single-box
	// installs. An explicit vmmd override wins; otherwise a shared override
	// remains supported for operators that intentionally co-locate spools.
	if root := os.Getenv(logarchive.EnvVMMDSpoolRoot); root != "" {
		cfg.SpoolRoot = root
	} else if os.Getenv(logarchive.EnvSpoolRoot) == "" {
		cfg.SpoolRoot = logarchive.DefaultVMMDSpoolRoot
	}
	if !cfg.Enabled() {
		log.Info("vmmd: log archive disabled", "reason", "archive bucket not configured")
		return nil, nil
	}
	catalog, ok := store.(state.RuntimeLogArchiveCatalog)
	locker, lockOK := store.(state.RuntimeLogArchiveLocker)
	if !ok || !lockOK || nodeID == "" {
		log.Warn("vmmd: log archive requires durable ownership store")
		return nil, nil
	}
	s3, err := logarchive.ClientForConfig(cfg, backend)
	if err != nil {
		log.Warn("vmmd: log archive client init failed", "err", err)
		return nil, nil
	}
	spool := logarchive.NewSpool(cfg.SpoolRoot, cfg.LocalBytesMax)
	var reg prometheus.Registerer
	if ops != nil {
		reg = ops.Registry()
	}
	metrics := logarchive.NewMetricsWithPrefix(reg, "vmmd")
	shipper, err := logarchive.NewShipper(cfg, spool, s3, log, metrics)
	if err != nil {
		log.Warn("vmmd: log archive shipper init failed", "err", err)
		return nil, nil
	}
	shipper.WithCatalog(catalog, locker)
	sink := newVMMDLogArchiveSink(spool, metrics, log)
	sink.mu.Lock()
	sink.catalog, sink.nodeID, sink.bindings = catalog, nodeID, make(map[string]state.RuntimeLogArchiveBinding)
	sink.mu.Unlock()
	sink.bootstrap(ctx, store)
	archiveCtx, archiveCancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := shipper.Run(archiveCtx); err != nil && archiveCtx.Err() == nil {
			log.Warn("vmmd: log archive stopped", "err", err)
		}
	}()
	shutdownBase := context.WithoutCancel(ctx)
	cleanup := func() {
		archiveCancel()
		shutdownCtx, cancel := context.WithTimeout(shutdownBase, 5*time.Second)
		defer cancel()
		select {
		case <-done:
		case <-shutdownCtx.Done():
			log.Warn("vmmd: log archive shipper shutdown timed out", "err", shutdownCtx.Err())
		}
		if err := sink.Close(shutdownCtx); err != nil {
			log.Warn("vmmd: log archive spool shutdown failed", "err", err)
		}
	}
	log.Info("vmmd: log archive enabled", "bucket", cfg.Bucket, "spool_root", cfg.SpoolRoot, "interval", cfg.FlushInterval)
	return sink, cleanup
}
