package state

import (
	"context"
	"fmt"
)

// JobKindImageRun marks a platform-managed runner job for image runs
// (ADR-1161). apid creates, touches and evicts these rows; customer job
// listings and the Job quota exclude them.
const JobKindImageRun = "image_run"

// ImageRunEnsure is the get-or-create request for one runner job.
type ImageRunEnsure struct {
	AccountID      string
	Name           string
	ImageRef       string
	RAMMB          int
	TaskTimeoutSec int
	// PullAlways re-queues a ready image so a moved tag is pulled again. A
	// failed image, or a ready one without recorded image config, is always
	// re-queued.
	PullAlways bool
	// MaxCached is the plan's cap on live runner jobs per account.
	MaxCached int
}

// ImageRunEnsureResult reports the runner job and any runner jobs evicted
// to make room for it.
type ImageRunEnsureResult struct {
	Job      Job
	Created  bool
	Requeued bool
	Evicted  []Job
}

// ImageRunCacheFullError reports that every cached image is in use, so no
// runner job could be evicted for a new image.
type ImageRunCacheFullError struct {
	Limit    int
	Observed int
}

func (e *ImageRunCacheFullError) Error() string {
	return fmt.Sprintf("image run cache full: %d of %d cached images are in use", e.Observed, e.Limit)
}

// ImageRunJobStore is apid's seam for runner jobs. JobEnsureImageRun runs
// under the account lock: it returns the live runner job for the name,
// re-queuing its materialization when needed, or creates one after evicting
// the least recently used idle runner jobs above MaxCached. A live job of a
// different kind holding the name yields ErrConflict.
type ImageRunJobStore interface {
	JobEnsureImageRun(ctx context.Context, in ImageRunEnsure) (ImageRunEnsureResult, error)
}

// JobImageConfigRecorder records the source image's ENTRYPOINT and CMD under
// the same claim fence as JobPublishImageMaterialization. imaged calls it
// before publishing, so a ready runner job always carries the config of the
// artifact it points at.
type JobImageConfigRecorder interface {
	JobRecordImageConfig(ctx context.Context, id, sourceRef, owner string, attempt int, entrypoint, cmd []string) error
}

// imageRunNeedsRequeue reports whether an existing runner job must pull
// its image again before it can start a run.
func imageRunNeedsRequeue(job Job, pullAlways bool) bool {
	switch job.ImageMaterializationStatus {
	case "failed":
		return true
	case "ready":
		return pullAlways || !job.ImageConfigRecorded
	default:
		return false
	}
}

// nonNilArgs keeps an empty argv distinguishable from "not recorded":
// PostgreSQL stores a nil slice as NULL and an empty one as '{}'.
func nonNilArgs(args []string) []string {
	return append([]string{}, args...)
}
