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
	var retry buildKitSeedRetry
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		if !retry.due(now) {
			continue
		}
		made, err := b.createBuildKitSeed(ctx)
		switch {
		case err != nil:
			wait := retry.failed(now)
			b.log.Warn("builderd: BuildKit seed run failed; builds run without a seed", "retry_after", wait, "err", err)
		case made:
			retry = buildKitSeedRetry{}
		}
	}
}

// buildKitSeedRetry spaces seed runs after failures: api.BuildKitSeedRetryMin
// after the first, doubling up to api.BuildKitSeedRetryMax. A seed made
// resets it.
type buildKitSeedRetry struct {
	at   time.Time
	wait time.Duration
}

func (r *buildKitSeedRetry) due(now time.Time) bool { return !now.Before(r.at) }

// failed records a failed run at now and returns the wait before the next.
func (r *buildKitSeedRetry) failed(now time.Time) time.Duration {
	switch {
	case r.wait <= 0:
		r.wait = api.BuildKitSeedRetryMin
	case 2*r.wait > api.BuildKitSeedRetryMax:
		r.wait = api.BuildKitSeedRetryMax
	default:
		r.wait *= 2
	}
	r.at = now.Add(r.wait)
	return r.wait
}

// createBuildKitSeed runs one seed check in the builder slot, only when no
// build holds it, so the seed run never takes a slot from a build. Drain
// waits for the run and stops its VM as it does a build's. It reports
// whether it made a seed.
func (b *Builderd) createBuildKitSeed(ctx context.Context) (bool, error) {
	seeder, ok := b.vm.(BuildKitSeeder)
	if !ok || b.claimsPaused() {
		return false, nil
	}
	release, ok := b.acquireIdleSlot()
	if !ok {
		return false, nil
	}
	defer release()
	if err := b.beginProcess(); err != nil {
		return false, nil
	}
	defer b.endProcess()
	buildID := "seed-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if !b.registerActiveVM(buildID, BuildHandle{BuildID: buildID}) {
		return false, nil
	}
	defer b.unregisterActiveVM(buildID)
	started := time.Now()
	made, err := seeder.CreateBuildKitSeed(ctx, buildID, b.cfg.BuildKitSnapshotter)
	if err != nil {
		return false, err
	}
	if made {
		b.log.Info("builderd: BuildKit seed created", "build", buildID, "seconds", time.Since(started).Seconds())
	}
	return made, nil
}
