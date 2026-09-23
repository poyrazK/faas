package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	vmmdLogArchiveQueueSize        = 4096
	vmmdLogArchiveQueueBytes       = 64 << 20
	vmmdLogArchiveIdentityCacheMax = 4096
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

	identityStore  state.Store
	identityNodeID string
	identityMu     sync.Mutex
	identityCache  map[string]api.PlatformIdentity
}

func newVMMDLogArchiveSink(spool *logarchive.Spool, metrics logarchive.Metrics, log *slog.Logger) *vmmdLogArchiveSink {
	return newVMMDLogArchiveSinkWithIdentity(spool, metrics, log, nil, "")
}

func newVMMDLogArchiveSinkWithIdentity(spool *logarchive.Spool, metrics logarchive.Metrics, log *slog.Logger, store state.Store, nodeID string) *vmmdLogArchiveSink {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = logarchive.NewMetrics(nil)
	}
	s := &vmmdLogArchiveSink{
		spool:          spool,
		metrics:        metrics,
		log:            log,
		queue:          make(chan archivedLine, vmmdLogArchiveQueueSize),
		done:           make(chan struct{}),
		identityStore:  store,
		identityNodeID: nodeID,
		identityCache:  make(map[string]api.PlatformIdentity),
	}
	go s.run()
	return s
}

func (s *vmmdLogArchiveSink) resolveIdentity(instance string) api.PlatformIdentity {
	identity := api.PlatformIdentity{InstanceID: instance, NodeID: s.identityNodeID}
	if s.identityStore == nil {
		return identity
	}
	s.identityMu.Lock()
	if cached, ok := s.identityCache[instance]; ok {
		s.identityMu.Unlock()
		return cached
	}
	s.identityMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	instanceRow, err := s.identityStore.InstanceByID(ctx, instance)
	if err != nil {
		return identity
	}
	identity.AppID = instanceRow.AppID
	identity.DeploymentID = instanceRow.DeploymentID
	if instanceRow.NodeID != "" {
		identity.NodeID = instanceRow.NodeID
	}
	if app, appErr := s.identityStore.AppByID(ctx, instanceRow.AppID); appErr == nil {
		identity.TenantID = app.AccountID
	}
	if identity.DeploymentID != "" {
		if deployment, deploymentErr := s.identityStore.DeploymentByID(ctx, identity.DeploymentID); deploymentErr == nil &&
			(deployment.AppID == "" || deployment.AppID == identity.AppID) {
			identity.CommitSHA = deployment.CommitSHA
			identity.DeploymentTag = deployment.Tag
			identity.ImageDigest = deployment.ImageDigest
			if !deployment.CreatedAt.IsZero() {
				identity.DeploymentCreatedAt = deployment.CreatedAt.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	if identity.NodeID != "" {
		if node, nodeErr := s.identityStore.ComputeNodeByID(ctx, identity.NodeID); nodeErr == nil && node.Region != nil {
			identity.Region = *node.Region
		}
	}
	s.identityMu.Lock()
	if len(s.identityCache) >= vmmdLogArchiveIdentityCacheMax {
		// Instance ids are not reused, so a daemon that runs indefinitely
		// must bound this best-effort lookup cache. A clear only causes a
		// later eviction to re-read state; it never changes the persisted
		// snapshot already written for earlier lines.
		s.identityCache = make(map[string]api.PlatformIdentity)
	}
	s.identityCache[instance] = identity
	s.identityMu.Unlock()
	return identity
}

// Enqueue is intentionally non-blocking because it runs from Ring's
// eviction callback while the per-instance ring mutex is held.
func (s *vmmdLogArchiveSink) Enqueue(instance string, line logbuf.Line) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
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
		var writeErr error
		if s.identityStore == nil && s.identityNodeID == "" {
			_, writeErr = s.spool.WriteWithLevel(item.instance, item.line.Seq, item.line.Stream, item.line.WrittenAt, item.line.Line, item.line.Level)
		} else {
			identity := s.resolveIdentity(item.instance)
			_, writeErr = s.spool.WriteWithIdentity(item.instance, item.line.Seq, item.line.Stream, item.line.WrittenAt, item.line.Line, item.line.Level, identity)
		}
		if writeErr != nil {
			if errors.Is(writeErr, logarchive.ErrSpoolFull) {
				s.metrics.IncFailure(logarchive.FailureReasonSpoolFull)
			} else {
				s.metrics.IncFailure(logarchive.FailureReasonSpoolWrite)
			}
			s.log.Warn("logarchive.spool_write_failed",
				"instance", item.instance, "seq", item.line.Seq, "err", writeErr)
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
func startVMMDLogArchive(ctx context.Context, store state.Store, nodeID string, log *slog.Logger, ops *wire.OpsMetrics) (*vmmdLogArchiveSink, func()) {
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
		return nil, nil
	}
	cfg = cfg.WithCredentials(creds)
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
	s3, err := logarchive.NewS3Client(cfg.Endpoint, cfg.Region, cfg.Bucket, cfg.KeyID, cfg.Secret, cfg.AuthMode)
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
	sink := newVMMDLogArchiveSinkWithIdentity(spool, metrics, log, store, nodeID)
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
