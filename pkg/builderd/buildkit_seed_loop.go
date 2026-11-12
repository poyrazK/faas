package builderd

// adr: 20261030122204

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// BuildKitSeeder is a VM driver that can make the node's BuildKit seed
// (ADR-20261030122204). CreateBuildKitSeed boots the seed run as buildID when
// the node has no seed for the current builder base and the disk has room. It
// reports whether it made one.
type BuildKitSeeder interface {
	CreateBuildKitSeed(ctx context.Context, buildID, snapshotter string) (bool, error)
}

// RunBuildKitSeedLoop keeps the node's BuildKit seed current until ctx ends.
// It does nothing unless Config.BuildKitSeed is on and the VM driver can make
// seeds.
func (b *Builderd) RunBuildKitSeedLoop(ctx context.Context) {
	if !b.cfg.BuildKitSeed {
		return
	}
	if _, ok := b.vm.(BuildKitSeeder); !ok {
		return
	}
	t := time.NewTicker(api.BuildKitSeedCheckEvery)
	defer t.Stop()
	var retryAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if time.Now().Before(retryAt) {
			continue
		}
		if err := b.createBuildKitSeed(ctx); err != nil {
			retryAt = time.Now().Add(api.BuildKitSeedRetryAfter)
			b.log.Warn("builderd: BuildKit seed run failed; builds run without a seed", "retry_after", api.BuildKitSeedRetryAfter, "err", err)
		}
	}
}

// createBuildKitSeed runs one seed check in the builder slot, only when no
// build holds it, so the seed run never takes a slot from a build. Drain
// waits for the run and stops its VM as it does a build's.
func (b *Builderd) createBuildKitSeed(ctx context.Context) error {
	seeder, ok := b.vm.(BuildKitSeeder)
	if !ok || b.claimsPaused() {
		return nil
	}
	release, ok := b.acquireIdleSlot()
	if !ok {
		return nil
	}
	defer release()
	if err := b.beginProcess(); err != nil {
		return nil
	}
	defer b.endProcess()
	buildID := "seed-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if !b.registerActiveVM(buildID, BuildHandle{BuildID: buildID}) {
		return nil
	}
	defer b.unregisterActiveVM(buildID)
	started := time.Now()
	made, err := seeder.CreateBuildKitSeed(ctx, buildID, b.cfg.BuildKitSnapshotter)
	if err != nil {
		return err
	}
	if made {
		b.log.Info("builderd: BuildKit seed created", "build", buildID, "seconds", time.Since(started).Seconds())
	}
	return nil
}
