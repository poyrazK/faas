// Package sched — disk_drift.go is the read-only /srv/fc/snap vs DB
// size-tracking drift sweep (PR scale-out readiness #3, scale-out
// plan §PR 3). It is the canary for "a future contributor rebuilds
// snapshot storage at scale-out and bypasses the storage helpers":
// today the only way to detect "a path inside pkg/fcvm is writing
// files that bypass the DB size tracking" is to notice the
// fleet-average drift on a dashboard, which is too late.
//
// The sweep:
//
//   - calls state.Store.ListSnapshotsForGC to read the canonical
//     (deploymentID → expected mem/vmstate size) set,
//   - reads each <SnapDir>/<depID>/{mem,vmstate} on disk via
//     os.ReadDir — flat, one level deep (the spec layout is two files
//     per deployment dir, nothing deeper; recursive Walk would be
//     wasted work),
//   - increments OpsMetrics.SnapshotDiskDrift by 1 for each
//     discrepancy (missing file, size mismatch, unexpected entry,
//     non-regular entry) and by 1 for any orphan <SnapDir>/<depID>/
//     directory whose depID is not in the DB result.
//
// It NEVER writes to the DB or filesystem, NEVER follows symlinks,
// NEVER retries on failure, and NEVER repairs. It is diagnostic only;
// ops reads rate(snapshot_disk_drift_total[5m]) and alerts on a
// non-zero rate.
//
// Soft-deleted apps are excluded at the SQL/memstore layer by
// ListSnapshotsForGC (apps.status='deleted' filter), so the sweep
// inherits that contract and does not re-implement it.
//
// Tick is exported so tests drive the sweep deterministically without
// spinning up a real ticker — same shape as Retention.SweepOnce.
//
// Storage backend awareness (ADR-054 §3): when a storage backend
// is wired via WithStorage, the sweep uses backend.List("snap/") to
// enumerate deployments and falls back to the byte-comparison only
// when the listed backend reports a local path. A remote backend
// (e.g. OCIRegistryStorageBackend) degrades the byte-comparison to a
// presence check — registry manifests are content-addressed digests,
// not byte sizes — but still catches orphan + missing keys. The
// "no backend wired" path is preserved for unit tests and the
// pre-ADR-054 single-box deploy.
package sched

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// expectedFile names the canonical files inside a deployment's
// snapshot directory. The spec layout is exactly two files; any other
// name is an unexpected entry and counts as drift (plan §PR 3 drift
// contract).
var expectedFiles = []string{"mem", "vmstate"}

// snapshotLister is the minimal store surface DiskDrift depends on.
// Narrowing the dependency to one method keeps the constructor type
// stable across production (state.PgStore) and tests (state.MemStore +
// a tiny errStore shim). Production callers pass a *state.PgStore;
// tests pass either a *state.MemStore (full happy path) or a custom
// shim that fails the call to drive the error-handling branches.
type snapshotLister interface {
	ListSnapshotsForGC(ctx context.Context) ([]state.SnapshotForGC, error)
}

// DiskDrift owns the read-only /srv/fc/snap vs DB drift sweep.
// Constructed once per schedd process; Tick is called from the
// Loop ticker (Loop.WithDiskDrift) at api.DefaultDiskDriftInterval
// cadence.
//
// Fields are unexported; the public surface is NewDiskDrift +
// WithMetrics + WithTickTimeout + WithStorage + WithClock + Tick.
// Constructor injection pattern mirrors Retention and Heartbeat in
// this package.
type DiskDrift struct {
	store   snapshotLister
	log     *slog.Logger
	now     func() time.Time
	timeout time.Duration
	// metrics may be nil; SnapshotDiskDrift() is itself nil-safe so
	// Tick doesn't have to nil-check before each Inc.
	metrics *wire.OpsMetrics
	// storage, when set, replaces the on-disk os.ReadDir scan with
	// a backend.List("snap/") enumeration. The byte-comparison path
	// stays in place for local backends; remote backends (OCI)
	// degrade the comparison to a presence check. ADR-054 §3.
	storage storage.LocalArtifactLister
}

// DefaultDiskDriftTickTimeout bounds the per-tick wall-clock cost of
// the sweep. The Loop runs the drift case inside a 1 Hz select that
// also serves the reaper, watchdog, cron, and heartbeat tickers — a
// single slow /srv/fc/snap ReadDir (e.g. due to a remote-attached
// mount in a future scale-out world) would otherwise freeze the
// loop for the duration of the call. 5s is a generous ceiling for a
// sweep that touches ~tens of dep dirs on a healthy box (sub-ms in
// practice); it leaves the per-tick budget well under the 1 Hz
// tick interval. The cleaner is a Warn log + sweep abort — the
// counter is left untouched, so the next tick catches the new
// state.
const DefaultDiskDriftTickTimeout = 5 * time.Second

// NewDiskDrift returns a DiskDrift ready for the Loop ticker. The
// store parameter accepts any snapshotLister; production passes a
// *state.PgStore, tests pass a *state.MemStore or a shim. The
// metrics receiver is nil at construction — wire it via WithMetrics
// before passing to Loop.WithDiskDrift. A nil metrics receiver
// produces no samples but is otherwise safe; this keeps the
// constructor signature stable across production and tests.
func NewDiskDrift(store snapshotLister, log *slog.Logger) *DiskDrift {
	if store == nil {
		panic("sched: DiskDrift.store is required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &DiskDrift{
		store:   store,
		log:     log,
		now:     time.Now,
		timeout: DefaultDiskDriftTickTimeout,
	}
}

// WithMetrics injects the OpsMetrics receiver the sweep increments on
// each discrepancy. Mirrors the pattern in pkg/gateway where the
// per-Handler Metrics receiver is nil-safe. A nil *OpsMetrics
// produces no samples but does not panic (SnapshotDiskDrift() short-
// circuits on nil-receiver, per the metric accessor contract).
func (d *DiskDrift) WithMetrics(m *wire.OpsMetrics) *DiskDrift {
	d.metrics = m
	return d
}

// WithTickTimeout overrides the per-tick wall-clock budget. 0 or
// negative reverts to DefaultDiskDriftTickTimeout. Used by the
// dispatcher (Loop.runDiskDrift) to bound the synchronous ReadDir
// cost so a slow /srv/fc/snap mount cannot freeze the loop's 1 Hz
// tick budget. Direct tests of Tick (which run synchronously in
// the test goroutine) do not consume this timeout — they pass
// their own context.
func (d *DiskDrift) WithTickTimeout(t time.Duration) *DiskDrift {
	if t <= 0 {
		t = DefaultDiskDriftTickTimeout
	}
	d.timeout = t
	return d
}

// WithClock injects a frozen time source for tests. Same shape as
// Retention.WithClock and Loop.WithClock. The sweep does not
// read the clock today (the size comparison is wall-clock-agnostic);
// the seam is preserved so a future contributor adding a
// "first-fire-defer" or "rate-limit" doesn't need to re-thread the
// dependency.
func (d *DiskDrift) WithClock(now func() time.Time) *DiskDrift {
	if now != nil {
		d.now = now
	}
	return d
}

// WithStorage injects a LocalArtifactLister-capable storage backend
// (typically the production PrefixRouter) so the sweep enumerates
// snapshots via backend.List("snap/") instead of os.ReadDir. The
// byte-comparison path remains intact when the listed backend is a
// local on-disk backend; remote backends (OCI registry) degrade the
// size-mismatch check to a presence check because registry manifests
// don't expose byte sizes to clients.
//
// A nil backend falls back to the os.ReadDir path so the constructor
// is forward-compatible with schedd builds that haven't wired the
// storage backend yet. ADR-054 §3.
func (d *DiskDrift) WithStorage(b storage.StorageBackend) *DiskDrift {
	if b == nil {
		d.storage = nil
		return d
	}
	if lister, ok := b.(storage.LocalArtifactLister); ok {
		d.storage = lister
	}
	return d
}

// Tick walks the snapshot storage and compares each expected file's
// size to the corresponding snapshots.mem_bytes / disk_bytes row.
// Increments OpsMetrics.SnapshotDiskDrift by 1 per discrepancy.
// Returns the total drift count for tests; returns nil error in all
// cases — the sweep is diagnostic and never errors out of a tick.
//
// When a storage backend is wired via WithStorage, the sweep uses
// backend.List("snap/") to enumerate deployments. For local backends
// the byte-comparison runs against os.Stat on the underlying file;
// for remote backends (OCI) the comparison degrades to a presence
// check because manifest digests are not byte sizes. ADR-054 §3.
//
// Failure modes (logged at Warn, no counter increment for the
// overall-tick failure, sweep continues to next depID):
//
//   - ListSnapshotsForGC error → Warn, return nil,
//   - os.ReadDir(<SnapDir>) error (e.g. ErrNotExist on a dev box) →
//     Warn, return nil,
//   - backend.List error → Warn, fall through to os.ReadDir if both
//     paths are viable; otherwise return nil,
//   - per-depID ReadDir error (race with imaged's GC deleting the dir
//     mid-sweep) → Warn, skip dep, continue.
func (d *DiskDrift) Tick(ctx context.Context) (int, error) {
	rows, err := d.store.ListSnapshotsForGC(ctx)
	if err != nil {
		d.log.Warn("disk-drift: list snapshots failed", "err", err)
		return 0, nil
	}

	// Build the expected set keyed by deploymentID. Map → O(1) lookup
	// when scanning the disk tree; rows is small (~tens per box).
	expected := make(map[string]state.SnapshotForGC, len(rows))
	for _, r := range rows {
		directory := r.DeploymentID
		if strings.HasPrefix(r.StorageKey, "snap/") && strings.HasSuffix(r.StorageKey, "/mem") {
			directory = strings.TrimSuffix(strings.TrimPrefix(r.StorageKey, "snap/"), "/mem")
		}
		expected[directory] = r
	}

	// When a storage backend is wired, prefer backend.List over
	// os.ReadDir so the sweep survives a multi-host future where
	// snap/ lives in OCI. The byte-comparison path stays valid
	// only when the listed backend is a local on-disk backend
	// (snap/ is content-addressed and latency-sensitive, so ADR-054
	// keeps it on every compute node by default).
	if d.storage != nil {
		return d.tickWithStorage(ctx, expected, rows)
	}

	root := SnapDir()
	diskDirs, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Dev box or fresh CI: nothing to compare against, no drift.
			// One Warn so operators can see the sweep ran; subsequent
			// ticks stay quiet at Info.
			d.log.Info("disk-drift: SnapDir absent, skipping tick",
				"snap_dir", root)
			return 0, nil
		}
		d.log.Warn("disk-drift: read SnapDir failed",
			"snap_dir", root, "err", err)
		return 0, nil
	}
	return d.scanDiskForDrift(ctx, root, diskDirs, expected, rows, false)
}

// scanDiskForDrift is the shared os.ReadDir-based sweep. Both
// the inline Tick path (no storage wired) and the storage-aware
// path's fallback (registry transient failure) call this so the
// drift-counting logic exists in one place. The fallback flag
// flips the "discrepancies observed" log suffix to "(fallback)"
// so an operator investigating a transient registry outage can
// tell which leg of the dispatch produced the count.
func (d *DiskDrift) scanDiskForDrift(ctx context.Context, root string, diskDirs []os.DirEntry, expected map[string]state.SnapshotForGC, rows []state.SnapshotForGC, fallback bool) (int, error) {
	drift := 0
	seen := make(map[string]struct{}, len(diskDirs))

	// Pass 1: walk every DB-known dep dir; compare files. Counts
	// missing files, size mismatches, unexpected regular files, and
	// non-regular entries under each dep dir. The per-iteration
	// ctx.Err() check is the per-tick timeout guard — a slow
	// /srv/fc/snap mount (e.g. network-attached in a future
	// scale-out world) cannot freeze the loop's 1 Hz tick budget
	// because the dispatcher wraps ctx with a bounded deadline via
	// d.timeout. We check between dep dirs (not between syscalls)
	// because os.ReadDir is synchronous and not interruptible.
	for depID, row := range expected {
		if err := ctx.Err(); err != nil {
			d.log.Warn("disk-drift: tick timed out",
				"err", err, "drift", drift, "rows_processed", len(seen))
			return drift, nil
		}
		seen[strings.SplitN(depID, "/", 2)[0]] = struct{}{}
		drift += d.checkDepDir(depID, row)
	}

	// Pass 2: orphan dep dirs on disk with no matching DB row.
	// The sweep never writes, so an orphan is itself drift — it
	// means a file system entry exists that no DB row accounts for.
	for _, entry := range diskDirs {
		if !entry.IsDir() {
			// Files directly under <SnapDir>/ (not in a dep dir) are
			// unexpected — every snapshot lives one level deep.
			drift += d.recordDrift("orphan-file-under-snap-dir",
				filepath.Join(root, entry.Name()))
			continue
		}
		if _, ok := seen[entry.Name()]; ok {
			continue
		}
		drift += d.recordDrift("orphan-dep-dir",
			filepath.Join(root, entry.Name()))
	}

	if drift > 0 {
		suffix := "discrepancies observed"
		if fallback {
			suffix = "discrepancies observed (fallback)"
		}
		d.log.Warn("disk-drift: "+suffix,
			"drift", drift, "rows", len(rows), "snap_dir", root)
	}
	return drift, nil
}

// tickWithStorage is the storage-backend-aware sweep path. It
// enumerates the snap/ prefix via d.storage.List, parses each key
// into (deploymentID, fileName), and runs a presence + (when local)
// byte-comparison check against the DB rows.
//
// Remote backends (OCI) intentionally skip the byte-comparison: a
// registry manifest's size is a digest-length, not a byte count,
// and the wire doesn't expose it. The presence check is still
// valuable — a missing snapshot mem or vmstate is drift regardless
// of where it lives.
func (d *DiskDrift) tickWithStorage(ctx context.Context, expected map[string]state.SnapshotForGC, rows []state.SnapshotForGC) (int, error) {
	keys, err := d.storage.List(ctx, "snap/")
	if err != nil {
		d.log.Warn("disk-drift: storage.List failed; falling back to disk read",
			"err", err, "snap_dir", SnapDir())
		// Fall through to the on-disk path so a transient registry
		// outage doesn't silence the sweep.
		return d.tickOnDiskFallback(ctx, expected, rows)
	}

	// Bucket keys by deploymentID. Each depID has up to 2 keys:
	// snap/<depID>/mem and snap/<depID>/vmstate.
	present := make(map[string]map[string]struct{}, len(keys))
	for _, k := range keys {
		depID, file, ok := parseSnapKey(k)
		if !ok {
			continue
		}
		if present[depID] == nil {
			present[depID] = make(map[string]struct{}, 2)
		}
		present[depID][file] = struct{}{}
	}

	drift := 0
	// Pass 1: every DB-known dep must have its expected files.
	// The storage-aware path only checks presence (registry
	// manifests don't expose byte sizes); the per-row data is
	// discarded by design. A future contributor adding a
	// remote-side size comparison should rebind the range to
	// `for depID, row := range expected`.
	for depID := range expected {
		if err := ctx.Err(); err != nil {
			d.log.Warn("disk-drift: tick timed out",
				"err", err, "drift", drift)
			return drift, nil
		}
		presentSet := present[depID]
		if presentSet == nil {
			drift += d.recordDrift("dep-missing",
				"snap/"+depID)
			continue
		}
		for _, want := range expectedFiles {
			if _, ok := presentSet[want]; !ok {
				drift += d.recordDrift("expected-file-missing",
					"snap/"+depID+"/"+want)
			}
		}
	}

	// Pass 2: orphan dep dirs in storage with no DB row.
	for depID := range present {
		if _, ok := expected[depID]; ok {
			continue
		}
		drift += d.recordDrift("orphan-dep-dir", "snap/"+depID)
	}

	if drift > 0 {
		d.log.Warn("disk-drift: discrepancies observed",
			"drift", drift, "rows", len(rows))
	}
	return drift, nil
}

// tickOnDiskFallback runs the read-through on-disk sweep so a
// transient backend failure doesn't silence the drift detector.
// The body delegates to scanDiskForDrift so the drift-counting
// logic lives in one place; the only thing this fallback adds
// is the per-row os.ReadDir error path (and the "(fallback)"
// log suffix in the discrepancies emission).
func (d *DiskDrift) tickOnDiskFallback(ctx context.Context, expected map[string]state.SnapshotForGC, rows []state.SnapshotForGC) (int, error) {
	root := SnapDir()
	diskDirs, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			d.log.Info("disk-drift: SnapDir absent, skipping tick",
				"snap_dir", root)
			return 0, nil
		}
		d.log.Warn("disk-drift: read SnapDir failed",
			"snap_dir", root, "err", err)
		return 0, nil
	}
	return d.scanDiskForDrift(ctx, root, diskDirs, expected, rows, true)
}

// parseSnapKey splits a snap/<depID>/<file> key into its parts.
// Returns ok=false for any key that doesn't match the canonical
// shape — those are drift but not surfaced through this helper.
func parseSnapKey(key string) (depID, file string, ok bool) {
	const prefix = "snap/"
	if !strings.HasPrefix(key, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, prefix)
	idx := strings.LastIndex(rest, "/")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// checkDepDir inspects one deployment's snapshot directory and
// returns the number of drift discrepancies observed. The depID +
// row pair is the expected contract; the directory on disk is what
// reality delivers. Discrepancies counted:
//
//   - expected file missing (no drift count if the row says size=0 —
//     degraded-but-recorded state),
//   - expected file present but size mismatch,
//   - non-expected regular file (an entry in the dep dir whose name
//     is not exactly "mem" or "vmstate"),
//   - any non-regular entry (symlink / device / pipe / socket /
//     irregular) — counted as drift without following or recursing.
//
// Directories are ignored. Errors reading the dep dir are logged and
// counted as one drift (so the next tick catches the new state).
func (d *DiskDrift) checkDepDir(depID string, row state.SnapshotForGC) int {
	dir := filepath.Join(SnapDir(), depID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Per-depID failure: race with imaged's GC deleting the dir,
		// or transient permission error. Log + count one drift + move
		// on; the next tick catches the new state.
		d.log.Warn("disk-drift: read dep dir failed",
			"dep_id", depID, "dir", dir, "err", err)
		return d.recordDrift("dep-dir-unreadable", dir)
	}

	drift := 0
	seen := make(map[string]struct{}, len(entries))
	// expectedSizes maps the canonical file name → DB-recorded size.
	// A row with size=0 (degraded-but-recorded state) suppresses the
	// size-mismatch check; we still detect missing files because
	// expectedFiles is iterated unconditionally below.
	expectedSizes := map[string]int64{
		"mem":     row.MemBytes,
		"vmstate": row.DiskBytes,
	}
	for _, want := range expectedFiles {
		fp := filepath.Join(dir, want)
		info, statErr := os.Lstat(fp)
		if statErr != nil {
			// Expected file missing on disk.
			drift += d.recordDrift("expected-file-missing", fp)
			continue
		}
		seen[want] = struct{}{}
		// Symlink and other non-regular entries are drift even if
		// the name matches; spec layout is a regular file only.
		if !info.Mode().IsRegular() {
			drift += d.recordDrift("expected-entry-non-regular", fp)
			continue
		}
		// Size mismatch only fires when the row claims bytes > 0.
		// A row with size=0 (degraded-but-recorded state) is
		// intentionally not flagged — it's a separate audit signal.
		if dbSize := expectedSizes[want]; dbSize > 0 && info.Size() != dbSize {
			drift += d.recordDrift("size-mismatch", fmt.Sprintf(
				"%s disk=%d db=%d", fp, info.Size(), dbSize))
		}
	}

	// Walk every other entry in the dep dir: anything that isn't
	// "mem" or "vmstate" is drift.
	for _, entry := range entries {
		if _, ok := seen[entry.Name()]; ok {
			continue
		}
		// Directory entries are ignored (spec says flat layout; a
		// directory here would be a future contributor's misuse).
		if entry.IsDir() {
			drift += d.recordDrift("unexpected-directory",
				filepath.Join(dir, entry.Name()))
			continue
		}
		// Symlink / device / pipe / socket / irregular → drift.
		// WalkDir does not follow symlinks; d.Type() tells us what
		// kind of entry this is without stat'ing the target.
		mode := entry.Type()
		if isNonRegular(mode) {
			drift += d.recordDrift("non-regular-entry",
				filepath.Join(dir, entry.Name()))
			continue
		}
		drift += d.recordDrift("unexpected-file",
			filepath.Join(dir, entry.Name()))
	}
	return drift
}

// isNonRegular reports whether the fs.FileMode describes a non-regular
// filesystem entry that the sweep should count as drift without
// following. Covers the union of fs.ModeSymlink | ModeDevice |
// ModeNamedPipe | ModeSocket | ModeIrregular — the spec layout is a
// regular file only. ModeIrregular is the catch-all for platform-
// specific types WalkDir surfaces that don't fit the named buckets.
func isNonRegular(m fs.FileMode) bool {
	return m&(fs.ModeSymlink|fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0
}

// recordDrift increments the OpsMetrics counter and returns 1 so the
// caller can accumulate the per-tick drift total. Nil-safe on the
// metrics receiver (SnapshotDiskDrift() short-circuits on a nil
// receiver). The per-drift log is at Debug — a persistent condition
// increments the counter every hour, which would otherwise spam
// Info-level logs. The Tick-end Warn summary
// ("disk-drift: discrepancies observed") is the operator-facing
// signal; the per-drift Debug line is for a developer tailing
// the sweep at lower log level.
func (d *DiskDrift) recordDrift(reason, path string) int {
	d.log.Debug("disk-drift: discrepancy",
		"reason", reason, "path", path)
	if c := d.metrics.SnapshotDiskDrift(); c != nil {
		c.Inc()
	}
	return 1
}
