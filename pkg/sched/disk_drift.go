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
// ListSnapshotsForGC also supplies rows that imaged is about to reclaim.
// This sweep omits deleted apps and failed/cancelled deployments from the
// expected set so their remaining directories are reported as drift until GC
// removes them.
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

	"github.com/google/uuid"
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
	store snapshotLister
	log   *slog.Logger
	// snapDir is injected for hermetic sweeps. Production leaves it empty and
	// resolves the process default through SnapDir(); tests must not mutate a
	// package-global path while other scheduler tests run in parallel.
	snapDir string
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
	// snapshotIndexer upgrades OCI repositories created before the durable
	// registry-side index existed. It is nil for ordinary local storage.
	snapshotIndexer storage.SnapshotRepositoryIndexer
}

// WithSnapDir sets the on-disk snapshot root used by this sweep. It is
// intended for tests and local diagnostics; production normally relies on the
// canonical /srv/fc/snap default returned by SnapDir().
func (d *DiskDrift) WithSnapDir(root string) *DiskDrift {
	d.snapDir = strings.TrimSpace(root)
	return d
}

func (d *DiskDrift) snapshotRoot() string {
	if d.snapDir != "" {
		return d.snapDir
	}
	return SnapDir()
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
	if indexer, ok := b.(storage.SnapshotRepositoryIndexer); ok {
		d.snapshotIndexer = indexer
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
		if r.AppStatus == state.AppDeleted ||
			r.DeploymentStatus == state.DeployFailed ||
			r.DeploymentStatus == state.DeployCancelled {
			continue
		}
		directory := r.DeploymentID
		if parsedDirectory, part, ok := parseSnapKey(r.StorageKey); ok && part == "mem" {
			directory = parsedDirectory
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

	root := d.snapshotRoot()
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
	type diskPart struct {
		path string
		info fs.FileInfo
	}
	present := make(map[string]map[string]diskPart, len(expected))
	terminalDirectories := make(map[string]struct{}, len(expected))
	unreadableRoots := make(map[string]struct{})
	invalidRoots := make(map[string]struct{})
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, readErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if readErr != nil {
			drift += d.recordDrift("snapshot-entry-unreadable", path)
			if rel, relErr := filepath.Rel(root, path); relErr == nil {
				unreadableRoots[strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]] = struct{}{}
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			drift += d.recordDrift("snapshot-path-invalid", path)
			return nil //nolint:nilerr // one malformed entry must not abort the bounded inventory
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			objectID, terminal, valid := parseSnapshotDirectory(rel)
			if !valid {
				invalidRoots[strings.SplitN(rel, "/", 2)[0]] = struct{}{}
				drift += d.recordDrift("malformed-snapshot-directory", path)
				return filepath.SkipDir
			}
			if terminal {
				terminalDirectories[objectID] = struct{}{}
				if present[objectID] == nil {
					present[objectID] = make(map[string]diskPart, 2)
				}
			}
			return nil
		}
		objectID, part, valid := parseSnapKey("snap/" + rel)
		if !valid {
			drift += d.recordDrift("malformed-snapshot-key", path)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			drift += d.recordDrift("snapshot-entry-unreadable", path)
			return nil //nolint:nilerr // record the unreadable entry and continue with siblings
		}
		if present[objectID] == nil {
			present[objectID] = make(map[string]diskPart, 2)
		}
		present[objectID][part] = diskPart{path: path, info: info}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, context.DeadlineExceeded) {
		d.log.Warn("disk-drift: recursive snapshot read failed", "snap_dir", root, "err", walkErr)
	}
	if err := ctx.Err(); err != nil {
		d.log.Warn("disk-drift: tick timed out", "err", err, "drift", drift, "objects_processed", len(present))
		return drift, nil
	}

	for objectID, row := range expected {
		if _, unreadable := unreadableRoots[strings.SplitN(objectID, "/", 2)[0]]; unreadable {
			continue
		}
		parts, exists := present[objectID]
		if !exists {
			drift += d.recordDrift("snapshot-object-missing", filepath.Join(root, objectID))
			continue
		}
		expectedSizes := map[string]int64{"mem": row.MemBytes, "vmstate": row.DiskBytes}
		for _, part := range expectedFiles {
			disk, ok := parts[part]
			if !ok {
				drift += d.recordDrift("expected-file-missing", filepath.Join(root, objectID, part))
				continue
			}
			if !disk.info.Mode().IsRegular() {
				drift += d.recordDrift("expected-entry-non-regular", disk.path)
				continue
			}
			if want := expectedSizes[part]; want > 0 && disk.info.Size() != want {
				drift += d.recordDrift("size-mismatch", fmt.Sprintf("%s disk=%d db=%d", disk.path, disk.info.Size(), want))
			}
		}
	}
	for objectID := range present {
		if _, ok := expected[objectID]; ok {
			continue
		}
		reason := "orphan-snapshot-object"
		if _, capture := terminalDirectories[objectID]; capture {
			reason = "orphan-snapshot-capture"
		}
		drift += d.recordDrift(reason, filepath.Join(root, objectID))
	}
	for _, entry := range diskDirs {
		if !entry.IsDir() {
			continue
		}
		_, hasObject := unreadableRoots[entry.Name()]
		if _, invalid := invalidRoots[entry.Name()]; invalid {
			hasObject = true
		}
		for objectID := range present {
			if objectID == entry.Name() || strings.HasPrefix(objectID, entry.Name()+"/") {
				hasObject = true
				break
			}
		}
		if !hasObject {
			drift += d.recordDrift("orphan-dep-dir", filepath.Join(root, entry.Name()))
		}
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

// parseSnapshotDirectory validates each directory prefix without following
// links. The boolean terminal result identifies immutable capture roots;
// legacy deployment and warm directories become concrete objects when their
// mem/vmstate files are encountered.
func parseSnapshotDirectory(directory string) (objectID string, terminal, valid bool) {
	parts := strings.Split(directory, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", false, false
	}
	switch {
	case len(parts) == 1:
		return parts[0], false, true
	case len(parts) == 2 && (parts[1] == "warm" || parts[1] == "captures"):
		return directory, false, true
	case len(parts) == 3 && parts[1] == "captures" && canonicalSnapshotCaptureID(parts[2]):
		return directory, true, true
	case len(parts) == 3 && parts[1] == "warm" && parts[2] == "captures":
		return directory, false, true
	case len(parts) == 4 && parts[1] == "warm" && parts[2] == "captures" && canonicalSnapshotCaptureID(parts[3]):
		return directory, true, true
	default:
		return "", false, false
	}
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
	if d.snapshotIndexer != nil {
		deploymentSet := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			if row.AppStatus == state.AppDeleted || row.DeploymentStatus == state.DeployFailed || row.DeploymentStatus == state.DeployCancelled {
				continue
			}
			deploymentSet[row.DeploymentID] = struct{}{}
		}
		deploymentIDs := make([]string, 0, len(deploymentSet))
		for deploymentID := range deploymentSet {
			deploymentIDs = append(deploymentIDs, deploymentID)
		}
		if err := d.snapshotIndexer.ReconcileSnapshotRepositoryIndex(ctx, deploymentIDs); err != nil {
			d.log.Warn("disk-drift: snapshot repository index reconciliation failed; falling back to disk read",
				"err", err, "snap_dir", d.snapshotRoot())
			return d.tickOnDiskFallback(ctx, expected, rows)
		}
	}
	keys, err := d.storage.List(ctx, "snap/")
	if errors.Is(err, storage.ErrIncompleteEnumeration) {
		// Repositories written before the durable OCI snapshot index can be
		// discovered from the authoritative DB rows without a registry catalog.
		// Exact lists seed the backend index, after which the global retry also
		// recovers orphan detection for future sweeps.
		for depID := range expected {
			if _, seedErr := d.storage.List(ctx, "snap/"+depID+"/"); seedErr != nil {
				err = fmt.Errorf("seed snapshot repository %s: %w", depID, seedErr)
				break
			}
		}
		if err != nil && errors.Is(err, storage.ErrIncompleteEnumeration) {
			keys, err = d.storage.List(ctx, "snap/")
		}
	}
	if err != nil {
		d.log.Warn("disk-drift: storage.List failed; falling back to disk read",
			"err", err, "snap_dir", d.snapshotRoot())
		// Fall through to the on-disk path so a transient registry
		// outage doesn't silence the sweep.
		return d.tickOnDiskFallback(ctx, expected, rows)
	}

	// Bucket keys by deploymentID. Each depID has up to 2 keys:
	// snap/<depID>/mem and snap/<depID>/vmstate.
	drift := 0
	present := make(map[string]map[string]struct{}, len(keys))
	for _, k := range keys {
		objectID, file, ok := parseSnapKey(k)
		if !ok {
			drift += d.recordDrift("malformed-snapshot-key", k)
			continue
		}
		if present[objectID] == nil {
			present[objectID] = make(map[string]struct{}, 2)
		}
		present[objectID][file] = struct{}{}
	}

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
	root := d.snapshotRoot()
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

// parseSnapKey returns the canonical on-disk object directory and part for
// legacy, warm-tier, and immutable capture keys. Capture IDs are deliberately
// strict UUIDs so a malformed directory is surfaced as drift instead of being
// mistaken for a deployment namespace.
func parseSnapKey(key string) (objectID, part string, ok bool) {
	parts := strings.Split(key, "/")
	if len(parts) < 3 || parts[0] != "snap" || parts[1] == "" {
		return "", "", false
	}
	part = parts[len(parts)-1]
	if part != "mem" && part != "vmstate" {
		return "", "", false
	}
	switch {
	case len(parts) == 3:
		return parts[1], part, true
	case len(parts) == 4 && parts[2] == "warm":
		return strings.Join(parts[1:3], "/"), part, true
	case len(parts) == 5 && parts[2] == "captures" && canonicalSnapshotCaptureID(parts[3]):
		return strings.Join(parts[1:4], "/"), part, true
	case len(parts) == 6 && parts[2] == "warm" && parts[3] == "captures" && canonicalSnapshotCaptureID(parts[4]):
		return strings.Join(parts[1:5], "/"), part, true
	default:
		return "", "", false
	}
}

func canonicalSnapshotCaptureID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
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
