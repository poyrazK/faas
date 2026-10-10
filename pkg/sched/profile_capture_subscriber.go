// pkg/sched/profile_capture_subscriber.go — schedd's on-demand profile
// capture consumer (ADR-967).
//
// Producer: apid's POST /v1/apps/{slug}/profiles/captures inserts a queued
// profile_captures row and emits db.NotifyProfileCapture. apid never calls
// schedd or vmmd (CLAUDE.md ownership).
//
// Consumer: the Loop's notify arm, a 30s safety tick and the startup sweep
// call drainProfileCaptures. It fails interrupted captures, deletes expired
// ones, then claims queued rows while a worker slot is free. Each capture
// runs in its own goroutine because a window lasts up to a minute and must
// never stall the scheduler loop.

package sched

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const profileCaptureSafetyTick = 30 * time.Second

// profileCaptureSlotsFor lazily allocates the worker slots. Only the loop
// goroutine calls it, so no synchronization is needed for the allocation.
func (l *Loop) profileCaptureSlotsFor() chan struct{} {
	if l.profileCaptureSlots == nil {
		l.profileCaptureSlots = make(chan struct{}, api.ProfileCaptureMaxConcurrentPerScheduler)
	}
	return l.profileCaptureSlots
}

func (l *Loop) drainProfileCaptures(ctx context.Context) {
	queue, ok := l.engine.Store().(state.ProfileCaptureQueue)
	if !ok {
		return
	}
	if n, err := queue.ExpireProfileCaptures(ctx, time.Now()); err != nil {
		l.log.Warn("sched: profile_capture: expiry failed", "err", err)
	} else if n > 0 {
		l.log.Info("sched: profile_capture: expired or failed stale captures", "count", n)
	}
	slots := l.profileCaptureSlotsFor()
	for {
		select {
		case slots <- struct{}{}:
		default:
			return // all workers busy; the next notify or tick resumes
		}
		claimed, err := queue.ClaimProfileCapture(ctx, time.Now())
		if err != nil {
			<-slots
			if !errors.Is(err, state.ErrNotFound) {
				l.log.Warn("sched: profile_capture: claim failed", "err", err)
			}
			return
		}
		go func() {
			defer func() { <-slots }()
			l.runProfileCapture(ctx, queue, claimed)
		}()
	}
}

func (l *Loop) runProfileCapture(ctx context.Context, queue state.ProfileCaptureQueue, c state.ClaimedProfileCapture) {
	req := profileproto.CaptureRequest{CaptureID: strings.ReplaceAll(c.ID, "-", ""), Kinds: c.Capture.Kinds,
		DurationMillis: int64(c.Capture.DurationSeconds) * 1000}
	callCtx, cancel := context.WithTimeout(ctx, req.Duration()+api.ProfileCaptureGrace+30*time.Second)
	out, err := l.engine.CaptureAppProfile(callCtx, c.AppID, c.Capture.RequestedInstanceID, req)
	cancel()
	result, blobs := profileCaptureOutcome(out, err)
	if ferr := queue.FinishProfileCapture(context.WithoutCancel(ctx), c.ID, result, blobs, time.Now()); ferr != nil {
		l.log.Warn("sched: profile_capture: finish failed", "capture_id", c.ID, "err", ferr)
		return
	}
	l.log.Info("sched: profile_capture: finished", "capture_id", c.ID, "app_id", c.AppID, "status", result.Status,
		"instance_id", result.InstanceID, "profiles", len(blobs), "processes", result.Processes)
}

// profileCaptureOutcome maps a capture to its terminal record. A capture
// that reached the guest but found no instrumented process is still ready:
// its reason explains the empty result.
func profileCaptureOutcome(out ProfileCaptureOutcome, err error) (api.ProfileCapture, []state.ProfileCaptureBlob) {
	if err != nil {
		return api.ProfileCapture{Status: api.ProfileCaptureFailed, InstanceID: out.InstanceID, DeploymentID: out.DeploymentID, Reason: profileCaptureFailureReason(err)}, nil
	}
	result := api.ProfileCapture{Status: api.ProfileCaptureReady, InstanceID: out.InstanceID, DeploymentID: out.DeploymentID,
		Processes: out.Result.Processes, Dropped: out.Result.Dropped, Reason: out.Result.Reason, Profiles: []api.ProfileCaptureProfile{}}
	blobs := make([]state.ProfileCaptureBlob, 0, len(out.Result.Profiles))
	for _, p := range out.Result.Profiles {
		blobs = append(blobs, state.ProfileCaptureBlob{Kind: p.Kind, ProcessID: p.ProcessID, Profile: p.Profile})
		result.Profiles = append(result.Profiles, api.ProfileCaptureProfile{Kind: p.Kind, ProcessID: p.ProcessID, Bytes: len(p.Profile)})
	}
	return result, blobs
}

func profileCaptureFailureReason(err error) string {
	if errors.Is(err, ErrNoRunningInstance) {
		return "the app has no running instance; send it a request and retry the capture"
	}
	switch status.Code(err) {
	case codes.NotFound:
		return "the instance stopped before the capture started"
	case codes.FailedPrecondition, codes.InvalidArgument:
		if msg := status.Convert(err).Message(); msg != "" && len(msg) <= 256 {
			return msg
		}
		return "the instance cannot run a profile capture now"
	case codes.DeadlineExceeded:
		return "the capture timed out"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "the capture timed out"
	}
	return "the capture could not reach the instance's host"
}
