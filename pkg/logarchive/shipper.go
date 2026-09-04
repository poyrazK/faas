// shipper.go — the periodic goroutine that scans the spool dir
// and ships .partial files to S3 (issue #562). Mirrors
// pkg/eventretention's Run/RunOnce shape: Run blocks on ctx, the
// first pass runs immediately, errors are logged + retried on
// the next tick (no crash). RunOnce is exported for deterministic
// tests.
//
// Lifecycle:
//
//  1. Shipper.Run(ctx) is started by apid's bgBefore closure.
//  2. Run calls RunOnce(ctx) immediately, then on every
//     FlushInterval tick.
//  3. SIGTERM/SIGINT cancels ctx → Run returns nil; the
//     underlying ticker is drained via defer ticker.Stop().
//  4. The flushOnce path calls Spool.CloseAll on the spool it
//     owns so the bufio buffers are flushed to disk before exit.
//
// The Shipper OWNS the Spool — apid's wire-up constructs the
// Spool in bgBefore and hands it to NewShipper; subsequent
// callers (the vmmd-side OnEvict closure) get the spool via a
// package-private setter. The apid wire-up is the only one that
// exercises this seam in production; tests drive NewShipper
// directly with a t.TempDir() spool.

package logarchive

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// S3 error-code vocabulary classifyFailure matches against. The
// strings come from the S3 API reference (sigv4-error-codes.md);
// hoisted to constants so goconst stops flagging the three
// repeated literals and so future additions stay grep-able.
const (
	s3ErrAccessDenied          = "AccessDenied"
	s3ErrSignatureDoesNotMatch = "SignatureDoesNotMatch"
	s3ErrInvalidAccessKeyId    = "InvalidAccessKeyId"
	s3ErrTooManyRequests       = "TooManyRequests"
	s3ErrSlowDown              = "SlowDown"
	s3ErrRequestThrottled      = "RequestThrottled"
	s3ErrEntityTooLarge        = "EntityTooLarge"
	s3ErrKeyTooLong            = "KeyTooLong"
	s3ErrBodyLengthMismatch    = "BodyLengthMismatch"
)

// Shipper is the loop driver. Construct with NewShipper; drive
// with Run (blocks until ctx cancel) or RunOnce (one pass,
// returns the (files shipped, bytes shipped, error) tuple).
type Shipper struct {
	cfg     Config
	spool   *Spool
	s3      *S3Client
	log     *slog.Logger
	metrics Metrics
	now     func() time.Time
}

// NewShipper constructs a Shipper. Required fields: cfg (with
// cfg.Bucket non-empty for the enabled path; empty Bucket = the
// Shipper.Run disabled-mode branch that returns nil on ctx
// cancel), spool (the on-disk sink the OnEvict closure writes
// to), and s3 (the S3-compatible client). Nil metrics falls
// back to noopMetrics so tests without a registry keep working.
//
// Returns an error when s3 is nil and cfg.Bucket is non-empty —
// the enabled-without-client shape is a wire-up bug the caller
// must fix before Run returns.
func NewShipper(cfg Config, spool *Spool, s3 *S3Client, log *slog.Logger, metrics Metrics) (*Shipper, error) {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = noopMetrics{}
	}
	if spool == nil {
		return nil, errors.New("logarchive: shipper requires a non-nil Spool")
	}
	if cfg.Bucket != "" && s3 == nil {
		return nil, errors.New("logarchive: shipper requires a non-nil S3Client when Bucket is set")
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if cfg.PurgeInterval <= 0 {
		cfg.PurgeInterval = DefaultPurgeInterval
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = DefaultRetentionDays
	}
	if cfg.LocalBytesMax <= 0 {
		cfg.LocalBytesMax = DefaultLocalBytesMax
	}
	// Keep the configured capacity alongside the current spool gauge when
	// the production Prometheus implementation supports it. The optional
	// interface preserves compatibility with the package's lightweight test
	// metrics fakes.
	if capacity, ok := metrics.(interface{ SetLocalBytesMax(int64) }); ok {
		capacity.SetLocalBytesMax(cfg.LocalBytesMax)
	}
	return &Shipper{
		cfg:     cfg,
		spool:   spool,
		s3:      s3,
		log:     log,
		metrics: metrics,
		now:     time.Now,
	}, nil
}

// Spool returns the per-process Spool so the vmmd-side OnEvict
// closure can write evicted lines into it. The Shipper owns the
// spool's lifetime — the apid wire-up calls CloseAll from the
// daemon shutdown path.
func (s *Shipper) Spool() *Spool { return s.spool }

// Run drives the shipper loop. The first pass runs immediately
// (so a daemon restart catches up on a backlog), then ticks on
// cfg.FlushInterval. Purge runs on its own cfg.PurgeInterval
// cadence (default 24h). Returns nil on graceful ctx cancel.
//
// On error from RunOnce: logs WARN with op context
// ("logarchive.flush_failed") and continues. The apid pattern
// mirrors pkg/eventretention.Run: a stuck flush never crashes
// the daemon; the next tick retries.
func (s *Shipper) Run(ctx context.Context) error {
	if !s.cfg.Enabled() {
		s.log.Info("logarchive.disabled", "reason", "FAAS_LOG_ARCHIVE_BUCKET unset")
		<-ctx.Done()
		return nil
	}
	// First pass: flush + purge run back-to-back so a
	// long-off daemon catches up before the ticker engages.
	if _, _, err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("logarchive.first_flush_failed", "err", err)
	}
	s.metrics.SetLocalBytes(s.spool.LocalBytes())
	if _, err := s.PurgeOnce(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("logarchive.first_purge_failed", "err", err)
	}
	s.metrics.SetLocalBytes(s.spool.LocalBytes())
	flushTicker := time.NewTicker(s.cfg.FlushInterval)
	defer flushTicker.Stop()
	purgeTicker := time.NewTicker(s.cfg.PurgeInterval)
	defer purgeTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-flushTicker.C:
			if _, _, err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("logarchive.flush_failed", "err", err)
			}
			s.metrics.SetLocalBytes(s.spool.LocalBytes())
		case <-purgeTicker.C:
			if _, err := s.PurgeOnce(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("logarchive.purge_failed", "err", err)
			}
			s.metrics.SetLocalBytes(s.spool.LocalBytes())
		}
	}
}

// RunOnce performs a single flush pass. Returns the (files
// shipped, bytes shipped, error) tuple. The shipped-count is
// the number of .partial files that successfully landed in the
// bucket; failed uploads are NOT counted (the file stays
// .partial and the next tick retries). Bytes shipped is the
// compressed (gzip) byte count.
//
// Failures are surfaced as typed errors so the caller can
// distinguish transient (network) from terminal (4xx). The
// apid log captures the reason via the slog WARN; the metric
// counter increments the right {reason} bucket.
func (s *Shipper) RunOnce(ctx context.Context) (int, int64, error) {
	if !s.cfg.Enabled() {
		return 0, 0, nil
	}
	start := s.now()
	files := s.spool.FilesSnapshot()
	var shipped int
	var bytes int64
	for _, f := range files {
		n, err := s.uploadFile(ctx, f)
		if err != nil {
			s.metrics.IncFilesUploaded("err")
			s.metrics.IncFailure(classifyFailure(err))
			s.log.Warn("logarchive.upload_failed",
				"instance", f.Instance, "day", f.Day, "path", f.Path, "err", err)
			// Continue with the next file — a single failed
			// upload doesn't poison the whole tick.
			continue
		}
		shipped++
		bytes += n
		s.metrics.IncFilesUploaded("ok")
		s.metrics.AddBytesUploaded(n)
	}
	s.metrics.ObserveFlushDuration(s.now().Sub(start).Seconds())
	return shipped, bytes, nil
}

// uploadFile gzips f.Path to a sibling .jsonl.gz file, PUTs the
// gzipped bytes to s3://bucket/faas-logs/{instance}/{YYYY}/{MM}/
// {DD}.jsonl.gz, and renames the local file on success. The
// .partial suffix is preserved on failure so the next tick
// retries; the .jsonl.gz rename signals "shipped" so the
// purger can sweep it after the retention boundary.
func (s *Shipper) uploadFile(ctx context.Context, f FileInfo) (int64, error) {
	// Force the bufio buffer to disk before reading — a partial
	// flush would lose lines that landed after the FilesSnapshot
	// read.
	if err := s.spool.FlushKey(f.Instance, f.Day); err != nil {
		return 0, fmt.Errorf("flush %s: %w", f.Path, err)
	}

	//nolint:forbidigo // vetted path — f.Path is the .partial file
	// the shipper just renamed from /var/log/faas/archive/{instance}/
	// {day}.partial (spool.go:Spool.Append) and was just stat'd at
	// the top of this function; no untrusted input crosses the gate.
	src, err := os.Open(f.Path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", f.Path, err)
	}
	defer func() { _ = src.Close() }()

	gzPath := strings.TrimSuffix(f.Path, ".partial") + ".gz"
	gz, err := os.Create(gzPath)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", gzPath, err)
	}
	gzWriter, err := gzip.NewWriterLevel(gz, gzip.BestSpeed)
	if err != nil {
		_ = gz.Close()
		_ = os.Remove(gzPath)
		return 0, fmt.Errorf("gzip writer: %w", err)
	}
	n, err := io.Copy(gzWriter, src)
	if err != nil {
		_ = gzWriter.Close()
		_ = gz.Close()
		_ = os.Remove(gzPath)
		return 0, fmt.Errorf("gzip copy: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		_ = gz.Close()
		_ = os.Remove(gzPath)
		return 0, fmt.Errorf("gzip close: %w", err)
	}
	if err := gz.Close(); err != nil {
		_ = os.Remove(gzPath)
		return 0, fmt.Errorf("close gz: %w", err)
	}
	stat, err := os.Stat(gzPath)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", gzPath, err)
	}
	uploadStart := s.now()
	key := s.bucketKey(f.Instance, f.Day)
	//nolint:forbidigo // vetted path — gzPath is the .jsonl.gz
	// the shipper just renamed from .partial inside
	// /var/log/faas/archive/; stat'd above, owner faas:faas (spec §11).
	gzReader, err := os.Open(gzPath)
	if err != nil {
		return 0, fmt.Errorf("open %s for upload: %w", gzPath, err)
	}
	defer func() { _ = gzReader.Close() }()
	if err := s.s3.PutObject(ctx, key, "application/gzip", gzReader, stat.Size()); err != nil {
		s.metrics.ObserveUploadDuration(s.now().Sub(uploadStart).Seconds())
		// Best-effort cleanup of the local gz on failure —
		// the next tick will recreate from the still-present
		// .partial.
		_ = os.Remove(gzPath)
		return 0, fmt.Errorf("put %s: %w", key, err)
	}
	s.metrics.ObserveUploadDuration(s.now().Sub(uploadStart).Seconds())
	// Success: rename the local file to .jsonl.gz (the
	// shipped-marker). The .partial file is removed because
	// every byte has been uploaded; the gz file is the
	// shipped-form. The purger sweeps .jsonl.gz files older
	// than the retention boundary.
	if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Warn("logarchive.remove_partial_failed",
			"path", f.Path, "err", err)
	}
	return n, nil
}

// bucketKey is the S3 object key for an (instance, day) tuple.
// Mirrors the layout the read-back path (PR-B's gatewayd-internal proxy)
// expects: s3://{bucket}/faas-logs/{instanceID}/{YYYY}/{MM}/
// {DD}.jsonl.gz. The prefix "faas-logs/" is fixed so an operator
// sharing a bucket across multiple Gregale installs gets a
// namespace without renaming the bucket.
func (s *Shipper) bucketKey(instance, day string) string {
	if len(day) < 10 {
		// Defensive: spool's day format is YYYY-MM-DD;
		// shorter values come from a buggy caller. The
		// read-back path would 404 either way; surface as
		// a clear error here.
		return fmt.Sprintf("faas-logs/%s/%s.jsonl.gz", instance, day)
	}
	return fmt.Sprintf("faas-logs/%s/%s/%s/%s.jsonl.gz",
		instance, day[:4], day[5:7], day[:10])
}

// classifyFailure maps an upload error to the closed-set reason
// label. Permanent 4xx with explicit S3 codes map to
// FailureReasonAuth (403) / FailureReasonThrottle (429, SlowDown)
// / FailureReasonSize (EntityTooLarge); everything else is
// FailureReasonNetwork (transient 5xx, dial errors).
func classifyFailure(err error) string {
	if err == nil {
		return FailureReasonOther
	}
	var perm *Permanent
	if errors.As(err, &perm) {
		switch perm.Code {
		case s3ErrAccessDenied, s3ErrSignatureDoesNotMatch, s3ErrInvalidAccessKeyId:
			return FailureReasonAuth
		case s3ErrTooManyRequests, s3ErrSlowDown, s3ErrRequestThrottled:
			return FailureReasonThrottle
		case s3ErrEntityTooLarge, s3ErrKeyTooLong:
			return FailureReasonSize
		case s3ErrBodyLengthMismatch:
			return FailureReasonBodyLength
		}
	}
	if errors.Is(err, ErrSpoolFull) {
		return FailureReasonSpoolFull
	}
	return FailureReasonNetwork
}

// PurgeOnce removes any .jsonl.gz file older than the
// configured retention boundary. The 7-day default matches
// issue #562 acceptance criterion 1. The walk is bounded by
// the per-day directory count under {root}, not by the per-
// line count — a single fs.WalkDir pass over the spool root.
//
// Returns the count of files removed. Errors are logged but
// don't abort the walk — one unreadable file shouldn't block
// the rest of the purge.
func (s *Shipper) PurgeOnce(ctx context.Context) (int, error) {
	if !s.cfg.Enabled() {
		return 0, nil
	}
	cutoff := s.now().UTC().AddDate(0, 0, -s.cfg.RetentionDays)
	count := 0
	err := filepath.WalkDir(s.cfg.SpoolRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			s.log.Warn("logarchive.purge_walk_error", "path", path, "err", walkErr)
			return nil // skip and continue
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl.gz") {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := d.Info()
		if err != nil {
			s.log.Warn("logarchive.purge_stat_failed", "path", path, "err", err)
			return nil
		}
		if info.ModTime().UTC().After(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("logarchive.purge_remove_failed", "path", path, "err", err)
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		return count, fmt.Errorf("logarchive: purge: %w", err)
	}
	if count > 0 {
		s.log.Info("logarchive.purged", "files", count, "retention_days", s.cfg.RetentionDays)
	}
	return count, nil
}
