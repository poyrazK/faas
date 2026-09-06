// Package imaged — daemon loop. The Loop is the M8 readiness glue: it owns
// the LISTEN subscriber (notifications arrive as db.Notification), the
// nightly GC tick (spec §4.6: keep current+previous per app, fleet budget
// pressure evicts biggest accounts first), and the one-shot FC-version
// sweep (spec §4.4: "on FC upgrade, mark all snapshots stale", ADR-005).
//
// All filesystem + state mutation goes through Handler. The Loop only
// orchestrates when each subsystem acts.
package imaged

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Loop is the imaged M8 daemon loop. cmd/imaged constructs it after wiring
// the Handler's collaborators (store, notifier, OCI puller, builder).
type Loop struct {
	handler     *Handler
	store       state.Store
	pool        *pgxpool.Pool
	log         *slog.Logger
	now         func() time.Time
	lvUsedPct   func(ctx context.Context) (float64, error)
	gcEvery     time.Duration // default 24h; tests shrink to ms
	detectFC    func(ctx context.Context) (string, error)
	appsRoot    string
	storageRoot string
	gcMu        sync.Mutex

	// Injected channels so tests never block on time.Sleep. Defaults are
	// built in NewLoop and can be overridden by WithGCChannel/WithFCSweepCh.
	gcCh <-chan time.Time
	fcCh <-chan struct{}
}

// LoopConfig bundles the dependencies NewLoop needs. Kept as a struct so
// tests can build it once with stub collaborators instead of threading six
// positional args through.
type LoopConfig struct {
	Handler     *Handler
	Store       state.Store
	Pool        *pgxpool.Pool
	Log         *slog.Logger
	Now         func() time.Time
	LvUsedPct   func(ctx context.Context) (float64, error)
	DetectFC    func(ctx context.Context) (string, error)
	AppsRoot    string
	StorageRoot string
	GCEvery     time.Duration
}

// NewLoop returns a Loop wired with sane defaults. The caller (cmd/imaged)
// supplies real collaborators; tests build a LoopConfig with fakes.
func NewLoop(cfg LoopConfig) *Loop {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if cfg.GCEvery == 0 {
		cfg.GCEvery = 24 * time.Hour
	}
	return &Loop{
		handler:     cfg.Handler,
		store:       cfg.Store,
		pool:        cfg.Pool,
		log:         cfg.Log,
		now:         cfg.Now,
		lvUsedPct:   cfg.LvUsedPct,
		detectFC:    cfg.DetectFC,
		appsRoot:    cfg.AppsRoot,
		storageRoot: cfg.StorageRoot,
		gcEvery:     cfg.GCEvery,
	}
}

// WithGCChannel swaps the GC tick channel. Used by tests to drive a
// deterministic tick boundary without sleeping.
func (l *Loop) WithGCChannel(ch <-chan time.Time) *Loop {
	if ch != nil {
		l.gcCh = ch
	}
	return l
}

// WithFCSweepCh swaps the one-shot FC sweep channel. Tests send on this
// channel to fire the sweep manually.
func (l *Loop) WithFCSweepCh(ch <-chan struct{}) *Loop {
	if ch != nil {
		l.fcCh = ch
	}
	return l
}

// Run blocks until ctx is cancelled. It owns three event sources: the LISTEN
// subscriber, the GC tick, the one-shot FC sweep, and ctx.Done. Mirrors
// pkg/sched/loop.go::Run.
//
// PR-B: builderd owns the build-queue durability surface now (its
// in-process worker polls with FOR UPDATE SKIP LOCKED). imaged no
// longer re-emits db.NotifyBuildQueued on a reap tick; only the
// deployment-side signals (NotifyDeploymentChanged,
// NotifySnapshotBoot, NotifySnapshotWritten, NotifyAppChanged) drive
// imaged's handlers.
//
// Tests can drive the loop without a pool by passing nil Pool; in that
// mode the LISTEN subscriber is skipped and the loop is purely ticker
// driven.
func (l *Loop) Run(ctx context.Context) error {
	// Serialize recovery with notification handling: replay cannot race an
	// imaged conversion in this process, and deployment status deduplicates it.
	buildTicker := time.NewTicker(2 * time.Second)
	defer buildTicker.Stop()

	if l.gcCh == nil {
		t := time.NewTicker(l.gcEvery)
		defer t.Stop()
		l.gcCh = t.C
	}
	if l.fcCh == nil {
		// One-shot FC sweep at startup. The channel yields exactly one
		// struct{} after construction.
		once := make(chan struct{}, 1)
		once <- struct{}{}
		l.fcCh = once
	}

	var notif <-chan db.Notification
	if l.pool != nil {
		var err error
		// F-11: switched from Subscribe to SubscribeWithReconnect — the
		// outer channel stays open across Postgres restarts so the daemon
		// keeps reacting to deploys/rolls/deletes instead of going silent
		// (the silent-LISTEN-close bug). The wrapper owns its own cancel
		// via its deferred goroutine; ctx cancel propagates.
		//
		// PR-B: NotifyBuildQueued is no longer here — builderd owns the
		// build-queue durability surface (in-process worker + SKIP
		// LOCKED) and is the single consumer of the build_queued
		// channel. imaged reacts to NotifyDeploymentChanged +
		// NotifySnapshotBoot for the deploy pipeline.
		notif, err = db.SubscribeWithReconnect(ctx, l.pool, []string{
			db.NotifyDeploymentChanged,
			db.NotifySnapshotBoot,
			db.NotifySnapshotWritten,
			db.NotifyAppChanged,
			// Issue #472 / ADR-054: cosign trusted-publisher CRUD
			// (apid → imaged refresh) + imaged-side audit emits
			// (imaged → apid-side pkg/audit writer). Both are
			// routed via pg_notify so the audit write surface
			// stays single-sourced in apid.
			db.NotifyTrustedSignerChanged,
			db.NotifyAuditEvent,
		}, l.log)
		if err != nil {
			return err
		}
	}

	l.recoverBuildHandoffs(ctx)
	// A daemon that restarts more often than gcEvery would otherwise never
	// reclaim anything because every restart resets the ticker. Run one sweep
	// after recovery so cleanup makes progress on frequently updated nodes.
	go l.runGCTick(ctx, l.now())

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			// F-11: outer channel never closes on conn drop (the wrapper
			// resubscribes internally). The only `ok==false` path is ctx
			// cancel. Leave the safety branch for paranoia.
			if !ok {
				return nil
			}
			l.handler.HandleNotification(ctx, n)
		case <-buildTicker.C:
			l.recoverBuildHandoffs(ctx)
		case <-l.gcCh:
			l.runGCTick(ctx, l.now())
		case <-l.fcCh:
			// F-08: drain fcCh only on a successful sweep. A failed
			// detectFC leaves the buffered value on the channel so the
			// next select iteration retries the detect.
			if l.runFCSweep(ctx) {
				l.fcCh = nil
			}
		}
	}
}

// runGCTick is the F1 GC body. Always runs the per-app "current +
// previous" cleanup. When lv-fc usage is at or above the alarm threshold,
// also walks biggest accounts first until pressure is relieved.
func (l *Loop) runGCTick(ctx context.Context, now time.Time) {
	l.gcMu.Lock()
	defer l.gcMu.Unlock()

	pct, err := l.lvUsedPct(ctx)
	pctKnown := err == nil && !math.IsNaN(pct) && !math.IsInf(pct, 0)
	pressure := pctKnown && pct >= api.SnapshotBudgetAlarmPct
	pctForLog := pct
	if !pctKnown {
		pctForLog = 0
	}
	l.log.Info("imaged: gc tick",
		"now", now.Format(time.RFC3339),
		"lv_fc_pct", pctForLog, "lv_fc_pct_known", pctKnown, "pressure", pressure)

	rows, err := l.store.ListSnapshotsForGC(ctx)
	if err != nil {
		l.log.Warn("imaged: gc list", "err", err)
		return
	}

	// Step A: per-app per-tier floor. Always runs.
	// Issue #470 / PR C / ADR-074: replaced
	// perAppKeepCurrentPrevious with perAppKeepTierFloor so
	// warm-enabled apps keep 2 warm + 2 init and warm-disabled
	// apps drop all warm rows + keep 2 init only. The legacy
	// function is preserved for the property-test suite
	// (pkg/imaged/gc_test.go keeps both call sites) and for
	// pre-#470 fleet replay.
	stale := perAppKeepTierFloor(rows)
	if len(stale) > 0 {
		if err := l.deleteSnapshotsAndFiles(ctx, stale); err != nil {
			l.log.Warn("imaged: per-app gc", "err", err)
		}
	}
	l.removeLocalSnapshotOrphans(ctx, now)
	if !pressure {
		return
	}

	// Step B: fleet budget pressure. Re-probe lv-fc after each delete;
	// exit when below threshold or no further evictions possible.
	for {
		pct, err = l.lvUsedPct(ctx)
		if err != nil || math.IsNaN(pct) || pct < api.SnapshotBudgetAlarmPct {
			return
		}
		rows, err = l.store.ListSnapshotsForGC(ctx)
		if err != nil {
			return
		}
		evicted := evictOldestFromHeaviestAccount(rows)
		if len(evicted) == 0 {
			l.log.Warn("imaged: pressure gc no candidates", "lv_fc_pct", pct)
			return
		}
		if err := l.deleteSnapshotsAndFiles(ctx, evicted); err != nil {
			l.log.Warn("imaged: pressure gc", "err", err)
			return
		}
	}
}

const localSnapshotOrphanGrace = time.Hour

// removeLocalSnapshotOrphans reclaims the pre-OCI /srv/fc/snap layout after
// its database rows have disappeared. Every snapshot row, including a retained
// stale row, protects its deployment directory. The age guard avoids racing a
// newly-created directory before snapshot publication commits its row.
func (l *Loop) removeLocalSnapshotOrphans(ctx context.Context, now time.Time) {
	if strings.TrimSpace(l.storageRoot) == "" {
		return
	}
	knownIDs, err := l.store.ListSnapshotDeploymentIDs(ctx)
	if err != nil {
		l.log.Warn("imaged: local snapshot orphan list", "err", err)
		return
	}
	known := make(map[string]struct{}, len(knownIDs))
	for _, deploymentID := range knownIDs {
		known[deploymentID] = struct{}{}
	}
	snapRoot := filepath.Join(filepath.Clean(l.storageRoot), "snap")
	entries, err := os.ReadDir(snapRoot)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		l.log.Warn("imaged: local snapshot orphan scan", "root", snapRoot, "err", err)
		return
	}
	cutoff := now.Add(-localSnapshotOrphanGrace)
	removed := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return
		}
		if !entry.IsDir() {
			continue
		}
		deploymentID := entry.Name()
		if _, err := uuid.Parse(deploymentID); err != nil {
			continue
		}
		if _, ok := known[deploymentID]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(snapRoot, deploymentID)
		info, err = os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			l.log.Warn("imaged: local snapshot orphan remove", "deployment", deploymentID, "err", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		l.log.Info("imaged: local snapshot orphans removed", "count", removed, "root", snapRoot)
	}
}

// runFCSweep is the F2 startup body. Returns true when the sweep ran
// to completion (whether or not anything was marked stale — an
// empty-stale-marker is still a successful run). Run() uses the return
// value to decide whether to drain fcCh; on failure the channel stays
// open so the next tick retries the detect call (F-08). Errors are
// logged, never returned — FC detection failure must not block imaged
// startup (a degraded box still serves traffic).
//
// ADR-127 §D1 (Layer 6) extends this body with an F3 step:
// MarkAppProtocolSnapshotsStale, the app-protocol dimension of the
// F2/F3 stale-mark split. F3 runs at the tail of runFCSweep — after
// the F2 mark-stale + retention-eviction — and operates on the
// wire-protocol-capable close-set {http2, grpc} (ADR-126 §Decision
// 6). F3 returns the F2 result on partial failure so F2's success
// is preserved even if F3 hits a transient error.
func (l *Loop) runFCSweep(ctx context.Context) bool {
	if l.detectFC == nil {
		l.log.Warn("imaged: fc sweep skipped: no detectFC wired")
		// F3 (Layer 6) is independent of FC detection — run it
		// regardless of the F2 detect call so an FAAS_BASE_IMAGE_VERSION
		// bump gets swept on a box with no firecracker binary on PATH
		// (e.g. a remote storage-only node). Detect is the F2 trigger,
		// not the sweep body.
		l.runAppProtocolSweep(ctx)
		return false
	}
	ver, err := l.detectFC(ctx)
	if err != nil {
		// F-08: do not drain fcCh on error — next tick retries the
		// detect. A permanently-broken `firecracker` binary on PATH
		// produces repeated Warn logs; the daemon stays up so the
		// operator notices and fixes the path.
		l.log.Warn("imaged: fc detect", "err", err)
		// F3 still runs — see above.
		l.runAppProtocolSweep(ctx)
		return false
	}
	n, err := l.handler.MarkFCSnapshotsStale(ctx, ver)
	if err != nil {
		l.log.Warn("imaged: fc sweep mark", "err", err)
		// F3 still runs.
		l.runAppProtocolSweep(ctx)
		return false
	}
	// Reclaim expired stale rows and their storage artifacts together. Deleting
	// rows first loses the deployment and app keys needed to find the files.
	expired, err := l.store.ListSnapshotsStaleOlderThan(ctx, api.SnapshotStaleRetention)
	if err != nil {
		l.log.Warn("imaged: fc sweep list expired", "err", err)
		// mark-stale succeeded; partial eviction still counts as progress.
		// F3 still runs.
		l.runAppProtocolSweep(ctx)
		return true
	}
	if err := l.deleteSnapshotsAndFiles(ctx, snapshotTargets(expired)); err != nil {
		l.log.Warn("imaged: fc sweep evict", "err", err)
		l.runAppProtocolSweep(ctx)
		return true
	}
	evicted := len(expired)
	// F3 (Layer 6): app-protocol stale-mark sweep. ADR-127 §D1 —
	// flips every non-stale snapshot whose deployment's
	// app.app_protocol ∈ {http2, grpc}. Operates on the base-image
	// version stamp, not the FC version; F3 returns success/failure
	// is logged but does NOT roll back F2's success.
	apN, apErr := l.handler.MarkAppProtocolSnapshotsStale(ctx)
	if apErr != nil {
		l.log.Warn("imaged: app_protocol sweep mark", "err", apErr)
	} else if apN > 0 {
		l.log.Info("imaged: app_protocol sweep marked stale",
			"base_image_version", fcvm.FAAS_BASE_IMAGE_VERSION,
			"marked_stale", apN)
	}
	l.log.Info("imaged: fc sweep done",
		"fc_version", ver, "marked_stale", n, "evicted", evicted,
		"app_protocol_marked_stale", apN)
	return true
}

func snapshotTargets(rows []state.SnapshotForGC) []deleteTarget {
	targets := make([]deleteTarget, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, targetForSnapshot(row))
	}
	return targets
}

// runAppProtocolSweep is the F3 standalone entry point — runs
// without F2 context. Used when F2 is skipped (no firecracker
// binary on PATH) or when F2 fails before its tail. Errors are
// logged, never returned; F3 is best-effort.
func (l *Loop) runAppProtocolSweep(ctx context.Context) {
	apN, apErr := l.handler.MarkAppProtocolSnapshotsStale(ctx)
	if apErr != nil {
		l.log.Warn("imaged: app_protocol sweep mark (standalone)", "err", apErr)
		return
	}
	if apN > 0 {
		l.log.Info("imaged: app_protocol sweep marked stale (standalone)",
			"base_image_version", fcvm.FAAS_BASE_IMAGE_VERSION,
			"marked_stale", apN)
	}
}

// deleteSnapshotsAndFiles is the shared cleanup helper. Takes the tuples
// produced by perAppKeepCurrentPrevious / evictOldestFromHeaviestAccount;
// each tuple carries the snap row id (for MarkOldSnapshotsStale /
// DeleteSnapshotsByID) and the deployment id (for the storage key
// under sched.SnapshotMemKey / sched.SnapshotVMStateKey). Marks the rows
// stale first so schedd's per-row freshness check refuses them in the
// brief mark→delete window, bulk-deletes the rows, then drops the
// on-disk artifacts via the Storage backend. F-05 fixes the prior
// snapshot-id/deployment-id namespace mismatch that prevented any
// filesystem cleanup from running.
//
// Issue #96 (ADR-025 axis 2) reframes cleanup as storage.Delete calls;
// the LocalStorageBackend swallows ErrNotFound so a transient race with
// schedd's restore path can't turn into an error. A future remote
// backend without LocalArtifactLister compatibility will need its own
// GC; we log + skip in that case (a remote registry has its own
// lifecycle).
//
// Issue #470 / PR C / ADR-074: the tuple's Tier field drives the
// storage key — warm-tier targets delete WarmSnapMemKey +
// WarmSnapVMStateKey (under /snap/<dep>/warm/), init-tier targets
// delete SnapMemKey + SnapVMStateKey (under /snap/<dep>/). The
// per-app ext4 layer (drive1, sched.AppLayerKey) is shared across
// tiers for a given (app, deployment) pair and is always removed
// when both tiers' rows have been evicted — but here we delete on
// every row to keep the layer discard idempotent against partial
// progress (an init row is always paired with its warm sibling
// once a deployment is replaced).
func (l *Loop) deleteSnapshotsAndFiles(ctx context.Context, ts []deleteTarget) error {
	if len(ts) == 0 {
		return nil
	}
	ids := make([]string, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	if _, err := l.store.MarkOldSnapshotsStale(ctx, ids); err != nil {
		return err
	}
	if _, err := l.store.DeleteSnapshotsByID(ctx, ids); err != nil {
		return err
	}
	be, err := l.handler.storageFor()
	if err != nil {
		return fmt.Errorf("imaged: gc storageFor: %w", err)
	}
	var legacyLocal *storage.LocalStorageBackend
	if strings.TrimSpace(l.storageRoot) != "" {
		legacyLocal, err = storage.NewLocalStorageBackend(l.storageRoot)
		if err != nil {
			l.log.Warn("imaged: gc legacy storage root", "root", l.storageRoot, "err", err)
		}
	}
	for _, t := range ts {
		// snap blobs: pick the keys by tier. The storage backend
		// swallows missing keys so a transient race with restore
		// is harmless. The legacy init-only path was the
		// unconditional default before #470; the tier-aware path
		// preserves that for init-tier targets and switches to
		// the warm keys when the row was a warm-tier snapshot.
		snap := state.Snapshot{DeploymentID: t.DeploymentID, StorageKey: t.StorageKey, Tier: t.Tier}
		memKey := t.StorageKey
		if memKey == "" {
			memKey = state.SnapMemKey(t.DeploymentID)
			if t.Tier == state.SnapshotTierWarm {
				memKey = state.WarmSnapMemKey(t.DeploymentID)
			}
		}
		vmstateKey := state.SnapshotVMStateKey(snap)
		if err := be.Delete(ctx, memKey); err != nil {
			l.log.Warn("imaged: gc remove snap mem", "deployment", t.DeploymentID, "tier", t.Tier, "err", err)
		}
		if err := be.Delete(ctx, vmstateKey); err != nil {
			l.log.Warn("imaged: gc remove snap vmstate", "deployment", t.DeploymentID, "tier", t.Tier, "err", err)
		}
		if legacyLocal != nil {
			l.deleteLegacyLocalSnapshot(ctx, legacyLocal, t.DeploymentID, memKey, vmstateKey)
		}
		// Per-app ext4 (drive1) — derive the key the same way buildImageLayer
		// writes it. B1.1 (issue #195): AppSlug is now on SnapshotForGC;
		// an empty AppSlug here is an invariant violation (the projection
		// JOIN broke) — log loudly and skip the ext4 delete rather than
		// silently paying 2 SQL round-trips per row to re-resolve it.
		if t.AppSlug == "" {
			l.log.Warn("imaged: gc evict without slug, skipping ext4 delete",
				"snapshot", t.ID, "deployment", t.DeploymentID)
			continue
		}
		if err := be.Delete(ctx, sched.AppLayerKey(t.AppSlug, t.DeploymentID)); err != nil {
			l.log.Warn("imaged: gc remove ext4", "deployment", t.DeploymentID, "err", err)
		}
	}
	// Best-effort: if the backend supports LocalArtifactLister (it does
	// in production via LocalStorageBackend + PrefixRouter) the next
	// nightly tick will see the smaller set without our help. We log a
	// debug-level hint when the backend isn't capable of list so the
	// remote-driver future is observable in the daemon's metrics.
	if _, ok := be.(storage.LocalArtifactLister); !ok {
		l.log.Warn("imaged: gc backend cannot list; rely on remote driver to reclaim space",
			"backend", fmt.Sprintf("%T", be))
	}
	return nil
}

// deleteLegacyLocalSnapshot removes snapshot files left in FAAS_STORAGE_ROOT
// before snap/ was routed to shared OCI storage. It is intentionally limited
// to snapshot keys selected by database GC; app layers retain their existing
// router-owned lifecycle.
func (l *Loop) deleteLegacyLocalSnapshot(ctx context.Context, local *storage.LocalStorageBackend, deploymentID string, keys ...string) {
	for _, key := range keys {
		if !strings.HasPrefix(key, "snap/") {
			continue
		}
		if err := local.Delete(ctx, key); err != nil {
			l.log.Warn("imaged: gc remove legacy local snapshot", "deployment", deploymentID, "key", key, "err", err)
			continue
		}
		removeEmptySnapshotParents(local.Root(), key)
	}
}

// removeEmptySnapshotParents prunes capture/tier/deployment directories after
// their files are gone. os.Remove only succeeds for an empty directory, so a
// surviving snapshot in the same deployment remains protected.
func removeEmptySnapshotParents(root, key string) {
	snapRoot := filepath.Join(filepath.Clean(root), "snap")
	full := filepath.Join(filepath.Clean(root), filepath.FromSlash(key))
	for dir := filepath.Dir(full); dir != snapRoot && strings.HasPrefix(dir, snapRoot+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			return
		}
	}
}

// recoverBuildHandoffs consumes successful builds whose image stage has not
// started. Provenance and rootfs are committed with success, so an imaged
// restart or a lost snapshot_boot notification cannot strand this handoff.
func (l *Loop) recoverBuildHandoffs(ctx context.Context) {
	if l.handler == nil || l.store == nil {
		return
	}
	work, err := l.store.ListBuildsAwaitingImage(ctx, l.handler.nodeName, 16)
	if err != nil {
		l.log.Warn("imaged: recover build handoffs", "err", err)
		return
	}
	for _, item := range work {
		if err := l.handler.handleSnapshotBoot(ctx, snapshotBootPayload{AppID: item.AppID, DeploymentID: item.DeploymentID, NodeID: item.NodeID}); err != nil {
			l.log.Warn("imaged: recovered build handoff failed", "deployment", item.DeploymentID, "err", err)
		}
	}
}
