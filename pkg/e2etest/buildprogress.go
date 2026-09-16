package e2etest

// buildprogress.go — wait for a SOURCE deployment by watching progress, not
// a clock.
//
// A source deploy runs a real build in a builder microVM. Its duration is
// dominated by the build, and a build's duration is honest: a cold Railpack
// build pulling the frontend takes minutes, a warm one takes seconds, and a
// wedged one takes forever. No single wall-clock deadline fits all three. Too
// short kills a healthy cold build; too long lets a wedged one burn the whole
// budget and then reports nothing but "deadline reached". Both happened on
// faas-acceptance-1: 2-minute waits failed builds that were still running
// (#2694), and the 16-minute waits that replaced them pinned phase 2 at its
// 60-minute ceiling with nine wedged builds in flight.
//
// A healthy build writes output continuously. A wedged one goes silent. So
// the wait watches three signals — deployment status, build status and the
// size of the build log — and fails as soon as none of them has changed for
// stallWindow, printing the log tail so the report says WHERE it stopped. An
// absolute ceiling remains as a backstop for a build that is still writing
// but pathologically slow.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// DefaultBuildStallWindow is how long a build may go without any observable
// progress before it is reported as wedged.
//
// Sized from measurement, not assumption. The first version assumed buildctl
// output streams to the host log and set 3 minutes; in fact builderd writes
// only its own bracket lines ("allocated builder slot", "build started",
// "dependency cache cold — installing dependencies", ...) and the in-VM run
// between them is silent on the host. In gate run 35137856640 the whole
// enqueue→imaged span was 131–271s per build, which bounds that silent
// stretch at about 4.5 minutes. Five minutes clears the slowest observed
// build with margin and still reports a wedge in a third of the old fixed
// deadline. If builds get slower than this on the acceptance node, that is
// itself the finding — raise it with the number, not by feel.
const DefaultBuildStallWindow = 5 * time.Minute

// DefaultBuildCeiling bounds a build that keeps writing but never finishes.
// It is the platform's own in-guest budget: a build that outlives it would
// have been killed inside the VM anyway.
const DefaultBuildCeiling = api.BuildTimeoutSeconds * time.Second

// buildSignature is everything a poll can observe about a source deployment.
// Two equal signatures mean nothing has happened in between.
type buildSignature struct {
	deployment state.DeploymentStatus
	build      state.BuildStatus
	logBytes   int64
}

// progressTracker decides whether a sequence of observations shows progress.
// It is pure so the stall rule can be tested without Postgres.
type progressTracker struct {
	window  time.Duration
	ceiling time.Duration
	started time.Time
	last    buildSignature
	lastAt  time.Time
}

func newProgressTracker(now time.Time, window, ceiling time.Duration) *progressTracker {
	return &progressTracker{window: window, ceiling: ceiling, started: now, lastAt: now}
}

// errBuildStalled and errBuildCeiling distinguish the two ways a wait can give
// up, so a caller (or a test) can tell "went silent" from "still going".
var (
	errBuildStalled = errors.New("build stalled")
	errBuildCeiling = errors.New("build ceiling reached")
)

// observe records sig at now. It returns errBuildStalled once the signature
// has not changed for the stall window, or errBuildCeiling once the whole
// wait has outlived the ceiling; nil otherwise. Any change at all — a status
// transition or a single extra byte of log — resets the stall clock.
func (p *progressTracker) observe(now time.Time, sig buildSignature) error {
	if sig != p.last {
		p.last = sig
		p.lastAt = now
	}
	if now.Sub(p.started) >= p.ceiling {
		return errBuildCeiling
	}
	if now.Sub(p.lastAt) >= p.window {
		return errBuildStalled
	}
	return nil
}

// silentFor is how long the signature has been unchanged, for reports.
func (p *progressTracker) silentFor(now time.Time) time.Duration { return now.Sub(p.lastAt) }

// WaitForSourceDeployment waits for a source deployment to reach live,
// watching progress rather than a clock. It returns the deployment and its
// build row as last observed.
//
// It returns as soon as the deployment is live or failed, or the build is
// failed or cancelled; a build that has produced no observable progress for
// stallWindow, or that outlives ceiling, is reported with the build log tail
// so the failure names where it stopped. Pass DefaultBuildStallWindow and
// DefaultBuildCeiling unless a test has a reason not to.
func WaitForSourceDeployment(ctx context.Context, t T, pool *pgxpool.Pool, deploymentID string, stallWindow, ceiling time.Duration) (state.Deployment, state.Build, error) {
	t.Helper()
	notif, cancel, err := db.Subscribe(ctx, pool, []string{db.NotifyDeploymentChanged})
	if err != nil {
		return state.Deployment{}, state.Build{}, fmt.Errorf("subscribe deployment_changed: %w", err)
	}
	defer cancel()

	store := state.NewPgStore(pool)
	tracker := newProgressTracker(time.Now(), stallWindow, ceiling)
	// Builds are minutes-scale; a one-second poll is plenty and keeps the log
	// stat cheap. The notify still wakes the loop early on a status change.
	poll := time.NewTicker(time.Second)
	defer poll.Stop()

	var (
		dep   state.Deployment
		build state.Build
	)
	for {
		dep, err = store.DeploymentByID(ctx, deploymentID)
		if err != nil {
			return dep, build, fmt.Errorf("read deployment: %w", err)
		}
		// The build row appears once apid has enqueued the build; before that
		// its absence is itself a (non-)signal the stall clock covers.
		if b, berr := store.BuildByDeployment(ctx, deploymentID); berr == nil {
			build = b
		}

		switch dep.Status {
		case state.DeployLive:
			return dep, build, nil
		case state.DeployFailed:
			return dep, build, fmt.Errorf("deployment %s failed: %s%s", deploymentID, dep.Error, buildLogTail(dep, build))
		}
		switch build.Status {
		case state.BuildFailed, state.BuildCancelled:
			return dep, build, fmt.Errorf("build %s %s (failure_class=%q) while deployment %s was %s%s",
				build.ID, build.Status, build.FailureClass, deploymentID, dep.Status, buildLogTail(dep, build))
		}

		now := time.Now()
		sig := buildSignature{deployment: dep.Status, build: build.Status, logBytes: buildLogSize(dep, build)}
		switch perr := tracker.observe(now, sig); {
		case errors.Is(perr, errBuildStalled):
			return dep, build, fmt.Errorf("%w: no progress for %s (deployment=%s build=%s log=%d bytes)%s",
				errBuildStalled, tracker.silentFor(now).Round(time.Second), dep.Status, build.Status, sig.logBytes, buildLogTail(dep, build))
		case errors.Is(perr, errBuildCeiling):
			return dep, build, fmt.Errorf("%w: still not live after %s (deployment=%s build=%s log=%d bytes)%s",
				errBuildCeiling, ceiling, dep.Status, build.Status, sig.logBytes, buildLogTail(dep, build))
		}

		select {
		case <-ctx.Done():
			return dep, build, ctx.Err()
		case n := <-notif:
			var p struct {
				To string `json:"to"`
			}
			_ = json.Unmarshal([]byte(n.Payload), &p)
			// Our deployment changed: re-read now rather than at the next tick.
			if p.To == deploymentID {
				continue
			}
		case <-poll.C:
		}
	}
}

// buildLogPath is where builderd streams the build log on this host.
//
// It is the DEPLOYMENT row's LogPath: builderd's appendLogBounded resolves
// the build to its deployment and appends every line there. The build row
// has a LogPath column too, but builderd never fills it during the build, so
// watching it reads 0 bytes for the whole build. That is exactly what the
// first version of this wait did — every one of 16 builds in gate run
// 35137856640 was reported as "build stalled ... log=0 bytes" while the
// enqueue→imaged timestamps show they completed in 131–271s. The signal was
// blind, not the builds.
func buildLogPath(dep state.Deployment, build state.Build) string {
	if dep.LogPath != "" {
		return dep.LogPath
	}
	return build.LogPath
}

// buildLogSize is the progress signal: bytes written to the build log so far.
// A missing or unreadable log counts as zero, which is "no progress" — the
// stall clock then does the right thing without a separate error path.
func buildLogSize(dep state.Deployment, build state.Build) int64 {
	path := buildLogPath(dep, build)
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// buildLogTail renders the last 4 KiB of the build log for a failure report,
// or an explanation of why it could not.
func buildLogTail(dep state.Deployment, build state.Build) string {
	path := buildLogPath(dep, build)
	if path == "" {
		return "\n(no build log path recorded yet)"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("\n(build log %s unreadable: %v)", path, err)
	}
	if len(data) == 0 {
		return fmt.Sprintf("\n(build log %s is empty)", path)
	}
	const keep = 4096
	if len(data) > keep {
		data = data[len(data)-keep:]
	}
	return fmt.Sprintf("\nbuild log tail (%s, last %d bytes):\n%s", path, len(data), data)
}
