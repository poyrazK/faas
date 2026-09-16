package builderd

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/buildexport"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	DefaultBuildExportMaxBytes      int64 = 8 << 30
	DefaultBuildExportMaxAge              = 24 * time.Hour
	DefaultBuildExportOrphanMinAge        = time.Hour
	DefaultBuildExportSweepInterval       = 5 * time.Minute
)

// BuildExportGCConfig is the node-local completed-export retention contract.
// MaxAge is the hard recovery window for a durable handoff; OrphanMinAge is
// the shorter floor before an unreferenced legacy directory can be reclaimed
// under byte pressure.
type BuildExportGCConfig struct {
	Root         string
	MaxBytes     int64
	MaxAge       time.Duration
	OrphanMinAge time.Duration
}

// SweepBuildExports executes one complete inventory/cleanup pass.
func SweepBuildExports(ctx context.Context, store state.Store, cfg BuildExportGCConfig, now time.Time) (buildexport.SweepResult, error) {
	return buildexport.Sweep(ctx, buildexport.SweepOptions{
		Root: cfg.Root, MaxBytes: cfg.MaxBytes, MaxAge: cfg.MaxAge,
		OrphanMinAge: cfg.OrphanMinAge, Now: now,
		Resolve: func(ctx context.Context, buildID, artifact string) (buildexport.ReferenceState, error) {
			if _, err := uuid.Parse(buildID); err != nil {
				return buildexport.ReferenceUnknown, nil //nolint:nilerr // malformed directory names are unowned artifacts, not resolver failures.
			}
			build, err := store.BuildByID(ctx, buildID)
			if errors.Is(err, state.ErrNotFound) {
				return buildexport.ReferenceUnknown, nil
			}
			if err != nil {
				return buildexport.ReferenceUnknown, err
			}
			dep, err := store.DeploymentByID(ctx, build.DeploymentID)
			if errors.Is(err, state.ErrNotFound) {
				return buildexport.ReferenceReleased, nil
			}
			if err != nil {
				return buildexport.ReferenceUnknown, err
			}
			if build.Status == state.BuildQueued || build.Status == state.BuildRunning ||
				(dep.RootfsPath == artifact && !dep.Status.IsTerminal()) {
				return buildexport.ReferenceActive, nil
			}
			return buildexport.ReferenceReleased, nil
		},
	})
}

// BuildExportSweepLoop runs a startup pass immediately and then periodically.
// Metrics describe the post-sweep tree, so a restarted daemon exposes disk
// pressure before waiting for its first interval.
func BuildExportSweepLoop(ctx context.Context, store state.Store, ops *wire.OpsMetrics, cfg BuildExportGCConfig, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultBuildExportSweepInterval
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultBuildExportMaxBytes
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = DefaultBuildExportMaxAge
	}
	if cfg.OrphanMinAge <= 0 {
		cfg.OrphanMinAge = DefaultBuildExportOrphanMinAge
	}
	if log == nil {
		log = slog.Default()
	}
	run := func() {
		result, err := SweepBuildExports(ctx, store, cfg, time.Now())
		if err != nil {
			ops.ObserveBuildExportCleanupErrors(1)
			log.Warn("builderd: build export sweep", "err", err)
			return
		}
		for reason, count := range result.RemovedByReason {
			ops.ObserveBuildExportCleanup(reason, count)
		}
		ops.ObserveBuildExportCleanupErrors(result.Errors)
		ops.SetBuildExportBytes(result.CurrentBytes)
		if result.Removed > 0 || result.Errors > 0 {
			log.Info("builderd: build export sweep complete", "removed", result.Removed,
				"reclaimed_bytes", result.ReclaimedBytes, "current_bytes", result.CurrentBytes,
				"skipped_active", result.SkippedActive, "errors", result.Errors)
		}
	}

	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
