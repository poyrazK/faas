package builderd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Build log byte cap (ADR-20261102182856). Config.BuildLogMaxBytes bounds the
// build output builderd stores in deployment_logs for one deployment, as it
// bounds the node-local log file. The line that would cross the cap is
// dropped and replaced by one marker row (stream
// state.DeploymentLogStreamSystem) so readers see the output was cut; every
// later line is dropped too and is not fanned out to live subscribers.
//
// The running count lives in memory per deployment and is re-derived from
// the database (state.Store.DeploymentLogUsage) on a deployment's first line
// in this process and after buildLogUsageReloadAfter without a line. A
// builderd restart mid-build, or a build requeued to another builder node,
// therefore continues from what is stored, marker included, instead of
// starting a fresh budget.

// buildLogUsageReloadAfter is how long a deployment's count is trusted
// without a line. A gap this long re-reads the count before the next line, so
// another builder's writes in the meantime are counted, and idle entries are
// dropped from memory.
const buildLogUsageReloadAfter = time.Minute

type buildLogUsageReader interface {
	DeploymentLogUsage(ctx context.Context, deploymentID string) (state.DeploymentLogUsage, error)
}

type buildLogVerdict int

const (
	// buildLogKeep stores the line.
	buildLogKeep buildLogVerdict = iota
	// buildLogTruncate drops the line and stores the truncation marker.
	buildLogTruncate
	// buildLogDrop drops the line; the marker is already stored.
	buildLogDrop
)

type buildLogUsage struct {
	bytes     int64
	truncated bool
	seen      time.Time
}

// buildLogBudget tracks each deployment's stored build log bytes. The zero
// value is ready to use.
type buildLogBudget struct {
	mu      sync.Mutex
	entries map[string]buildLogUsage
}

// admit decides whether line still fits deploymentID's cap of maxBytes and
// counts it if it does. The mutex is held across the occasional usage read so
// two lines of one build cannot both start from the same stored count. A
// failed read keeps the line (error returned for logging) and caches nothing,
// so the next line reads again.
func (b *buildLogBudget) admit(ctx context.Context, store buildLogUsageReader, deploymentID, line string, maxBytes int64, now time.Time) (buildLogVerdict, error) {
	maxBytes = effectiveBuildLogMaxBytes(maxBytes)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = map[string]buildLogUsage{}
	}
	for id, entry := range b.entries {
		if now.Sub(entry.seen) >= buildLogUsageReloadAfter {
			delete(b.entries, id)
		}
	}
	usage, ok := b.entries[deploymentID]
	if !ok {
		stored, err := store.DeploymentLogUsage(ctx, deploymentID)
		if err != nil {
			return buildLogKeep, fmt.Errorf("read build log usage: %w", err)
		}
		usage = buildLogUsage{bytes: stored.Bytes, truncated: stored.Truncated}
	}
	usage.seen = now
	verdict := buildLogKeep
	switch {
	case usage.truncated:
		verdict = buildLogDrop
	case usage.bytes+int64(len(line)) > maxBytes:
		verdict = buildLogTruncate
		usage.truncated = true
	default:
		usage.bytes += int64(len(line))
	}
	b.entries[deploymentID] = usage
	return verdict, nil
}

// buildLogTruncatedMarker is the row stored in place of the first line past
// the cap.
func buildLogTruncatedMarker(maxBytes int64) string {
	maxBytes = effectiveBuildLogMaxBytes(maxBytes)
	size := fmt.Sprintf("%d bytes", maxBytes)
	if maxBytes%(1<<20) == 0 {
		size = fmt.Sprintf("%d MiB", maxBytes>>20)
	}
	return fmt.Sprintf("[build log truncated: this build's log reached its %s limit; later output was not stored]\n", size)
}

// persistBuildLog stores line in deployment_logs within the deployment's cap
// and returns what live subscribers should see: the line, the truncation
// marker, or "" once the log is cut.
func (b *Builderd) persistBuildLog(ctx context.Context, buildID, deploymentID, line string) string {
	verdict, err := b.logBudget.admit(ctx, b.store, deploymentID, line, b.cfg.BuildLogMaxBytes, time.Now())
	if err != nil {
		b.log.Warn("builderd: build log cap unknown; storing the line", "build", buildID, "deployment", deploymentID, "err", err)
	}
	stream := "build"
	switch verdict {
	case buildLogDrop:
		return ""
	case buildLogTruncate:
		line, stream = buildLogTruncatedMarker(b.cfg.BuildLogMaxBytes), state.DeploymentLogStreamSystem
		b.log.Warn("builderd: build log reached its cap; later lines are not stored",
			"build", buildID, "deployment", deploymentID, "max_bytes", effectiveBuildLogMaxBytes(b.cfg.BuildLogMaxBytes))
	}
	if _, err := b.store.AppendDeploymentLog(ctx, deploymentID, stream, line); err != nil {
		b.log.Warn("builderd: persist build log", "build", buildID, "deployment", deploymentID, "err", err)
	}
	return line
}
