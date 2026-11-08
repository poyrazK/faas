package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	vmmdLogArchiveQueueSize  = 4096
	vmmdLogArchiveQueueBytes = 64 << 20
	// vmmdLogArchiveRetireWait bounds how long a teardown waits for queue
	// room for its retired ring before it writes the lines to the spool
	// itself.
	vmmdLogArchiveRetireWait = 2 * time.Second
)

// vmmdLogArchiveDroppedLines is the §12-style counter of log lines that
// never reached the archive spool: a full eviction queue, a full spool, a
// failed spool write, or a ring retired after the archive closed. Each drop
// also counts in vmmd_log_archive_failures_total{reason}, which
// FaasLogArchiveShipperDegraded alerts on; this counter gives the volume.
const vmmdLogArchiveDroppedLines = "vmmd_log_archive_lines_dropped_total"

func newVMMDLogArchiveDroppedCounter() prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Name: vmmdLogArchiveDroppedLines,
		Help: "Log lines vmmd dropped before they reached the log archive spool (queue full, spool full, spool write failure, or archive closed).",
	})
}

type archivedLine struct {
	instance string
	line     logbuf.Line
	// batch carries a retired ring's retained lines as one queue item, so
	// they are spooled in order after the evictions queued before them.
	batch []logbuf.Line
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
	dropped prometheus.Counter

	maxQueuedBytes int64
	retireWait     time.Duration

	mu          sync.RWMutex
	closed      bool
	queuedBytes int64
	// space is closed by the worker when it frees queue room while a
	// Retire waits for it (waiters > 0); a waiter makes a new one.
	space   chan struct{}
	waiters int
	// spills counts Retire calls writing to the spool themselves; Close
	// waits for them before closing the spool's files.
	spills sync.WaitGroup
}

func newVMMDLogArchiveSink(spool *logarchive.Spool, metrics logarchive.Metrics, log *slog.Logger) *vmmdLogArchiveSink {
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
		dropped:        newVMMDLogArchiveDroppedCounter(),
		maxQueuedBytes: vmmdLogArchiveQueueBytes,
		retireWait:     vmmdLogArchiveRetireWait,
	}
	go s.run()
	return s
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
	if !s.tryEnqueueLocked(archivedLine{instance: instance, line: line}, int64(len(line.Line))) {
		s.metrics.IncFailure(logarchive.FailureReasonQueueFull)
		s.dropped.Inc()
	}
}

// tryEnqueueLocked queues item when the queue has room for it. The send
// cannot block: every sender holds s.mu and only the worker receives.
func (s *vmmdLogArchiveSink) tryEnqueueLocked(item archivedLine, size int64) bool {
	if s.queuedBytes+size > s.maxQueuedBytes || len(s.queue) >= cap(s.queue) {
		return false
	}
	s.queue <- item
	s.queuedBytes += size
	return true
}

// Retire hands over the lines a torn-down VM's ring still held. Lines under
// the ring budget are never evicted, so without this a parked instance had
// nothing in the archive. It runs on the teardown path outside the ring
// lock, so unlike Enqueue it may wait: the batch is one queue item, queued
// after the evictions before it once the worker frees room. A mass teardown
// used to drop whole batches when the queue was full (logs/traces audit
// 2026-10-31 A12). After retireWait the lines go straight to the spool
// instead, possibly ahead of evictions still queued; only lines the spool
// refuses, or that arrive after Close, are dropped and counted.
func (s *vmmdLogArchiveSink) Retire(instance string, lines []logbuf.Line) {
	if s == nil || len(lines) == 0 {
		return
	}
	size := linesBytes(lines)
	item := archivedLine{instance: instance, batch: lines}
	timer := time.NewTimer(s.retireWait)
	defer timer.Stop()
	s.mu.Lock()
	for {
		if s.closed {
			s.mu.Unlock()
			s.dropped.Add(float64(len(lines)))
			return
		}
		if s.tryEnqueueLocked(item, size) {
			s.mu.Unlock()
			return
		}
		if size > s.maxQueuedBytes {
			break // larger than the whole queue: it can never be queued
		}
		if s.space == nil {
			s.space = make(chan struct{})
		}
		space := s.space
		s.waiters++
		s.mu.Unlock()
		timedOut := false
		select {
		case <-space:
		case <-timer.C:
			timedOut = true
		}
		s.mu.Lock()
		s.waiters--
		if timedOut && !s.closed {
			break
		}
	}
	// Close waits for the spill before it closes the spool's files.
	s.spills.Add(1)
	s.mu.Unlock()
	defer s.spills.Done()
	s.spill(instance, lines)
}

// spill writes a retired batch the queue could not take straight to the
// spool from the caller's goroutine. The spool serialises writers.
func (s *vmmdLogArchiveSink) spill(instance string, lines []logbuf.Line) {
	s.metrics.IncFailure(logarchive.FailureReasonQueueFull)
	failed := 0
	var lastErr error
	for _, line := range lines {
		if err := s.writeLine(instance, line); err != nil {
			failed++
			lastErr = err
		}
	}
	s.log.Warn("logarchive.retire_spilled",
		"instance", instance, "lines", len(lines), "dropped", failed, "err", lastErr)
}

func linesBytes(lines []logbuf.Line) int64 {
	var size int64
	for _, line := range lines {
		size += int64(len(line.Line))
	}
	return size
}

func (s *vmmdLogArchiveSink) run() {
	defer close(s.done)
	for item := range s.queue {
		lines := item.batch
		if lines == nil {
			lines = []logbuf.Line{item.line}
		}
		s.release(linesBytes(lines))
		for _, line := range lines {
			if err := s.writeLine(item.instance, line); err != nil {
				s.log.Warn("logarchive.spool_write_failed",
					"instance", item.instance, "seq", line.Seq, "err", err)
			}
		}
	}
}

// release frees the queue room a dequeued item held and wakes a waiting
// Retire.
func (s *vmmdLogArchiveSink) release(size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queuedBytes -= size
	if s.waiters > 0 && s.space != nil {
		close(s.space)
		s.space = nil
	}
}

// writeLine spools one line, counting a refusal as a failure and a drop.
func (s *vmmdLogArchiveSink) writeLine(instance string, line logbuf.Line) error {
	_, err := s.spool.WriteWithLevel(instance, line.Seq, line.Stream, line.WrittenAt, line.Line, line.Level)
	if err == nil {
		return nil
	}
	if errors.Is(err, logarchive.ErrSpoolFull) {
		s.metrics.IncFailure(logarchive.FailureReasonSpoolFull)
	} else {
		s.metrics.IncFailure(logarchive.FailureReasonSpoolWrite)
	}
	s.dropped.Inc()
	return err
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
	spilled := make(chan struct{})
	go func() {
		s.spills.Wait()
		close(spilled)
	}()
	for _, done := range []<-chan struct{}{s.done, spilled} {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.spool.CloseAll()
}

// startVMMDLogArchive wires the compute-side producer and shipper. Archive
// setup is optional: an absent credential envelope leaves vmmd serving live
// logs with archive disabled; a malformed envelope emits a clear startup
// warning, matching apid's best-effort archive policy.
func startVMMDLogArchive(ctx context.Context, log *slog.Logger, ops *wire.OpsMetrics) (*vmmdLogArchiveSink, func()) {
	var reg prometheus.Registerer
	if ops != nil {
		reg = ops.Registry()
	}
	// Every early return below leaves the archive off; the gauge makes that
	// visible (FaasLogArchiveNodeDisabled) instead of one startup log line.
	enabled := false
	defer func() { logarchive.ReportEnabled(reg, "vmmd", enabled) }()
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
	metrics := logarchive.NewMetricsWithPrefix(reg, "vmmd")
	shipper, err := logarchive.NewShipper(cfg, spool, s3, log, metrics)
	if err != nil {
		log.Warn("vmmd: log archive shipper init failed", "err", err)
		return nil, nil
	}
	sink := newVMMDLogArchiveSink(spool, metrics, log)
	if reg != nil {
		reg.MustRegister(sink.dropped)
	}
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
	enabled = true
	return sink, cleanup
}
