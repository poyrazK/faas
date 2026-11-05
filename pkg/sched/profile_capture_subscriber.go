// pkg/sched/profile_capture_subscriber.go — schedd's on-demand profile
// capture consumer (ADR-1155).
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
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const profileCaptureSafetyTick = 30 * time.Second

// profileCaptureFinishAttempts bounds retries of a transient store error when
// a capture finishes: the window's profiles exist only in this goroutine.
const profileCaptureFinishAttempts = 3

const profileCaptureUnstoredReason = "the capture's profiles could not be stored; start the capture again"

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
	claimed := time.Now()
	req := profileCaptureRequest(c)
	callCtx, cancel := context.WithTimeout(ctx, req.Duration()+api.ProfileCaptureGrace+30*time.Second)
	out, err := l.engine.CaptureAppProfile(callCtx, c.AppID, c.Capture.RequestedInstanceID, req, func(instanceID, deploymentID string) {
		// Recording the instance up front lets an OOM kill during the window
		// find this capture (ADR-1155).
		if serr := queue.StartProfileCapture(context.WithoutCancel(ctx), c.ID, instanceID, deploymentID); serr != nil {
			l.log.Warn("sched: profile_capture: record instance failed", "capture_id", c.ID, "err", serr)
		}
	})
	cancel()
	if err == nil && out.Result.OOMKilled {
		// The guest saw the workload OOM-killed mid-window (ADR-1155). Apps
		// without a workload cgroup restart in place and never send a
		// workload-OOM report, so the reply is the only signal. Linking
		// before the finish lets the capture's one notification name the kill.
		killedAt := time.Now().UTC()
		oom := state.ProfileCaptureOOM{KilledAt: killedAt, PeakMB: out.Result.OOMPeakMB, PlanMB: out.PlanMB}
		if _, serr := queue.StampProfileCaptureOOM(context.WithoutCancel(ctx), c.AppID, out.InstanceID, oom, killedAt.Add(-api.ProfileCaptureOOMLinkWindow)); serr != nil {
			l.log.Warn("sched: profile_capture: link OOM kill failed", "capture_id", c.ID, "err", serr)
		}
	}
	result, blobs := profileCaptureOutcome(out, err)
	ferr := finishProfileCapture(context.WithoutCancel(ctx), queue, c.ID, result, blobs, time.Sleep)
	l.engine.profileCaptureMetrics.CaptureFinished(c.Capture.Trigger, profileCaptureResultLabel(err, ferr), claimed.Sub(c.Capture.CreatedAt), time.Since(claimed))
	if ferr != nil {
		l.log.Warn("sched: profile_capture: finish failed", "capture_id", c.ID, "err", ferr)
		return
	}
	l.log.Info("sched: profile_capture: finished", "capture_id", c.ID, "app_id", c.AppID, "status", result.Status,
		"instance_id", result.InstanceID, "profiles", len(blobs), "processes", result.Processes)
}

// profileCaptureRequest builds the guest request. Automatic memory-pressure
// captures ask for heap checkpoints because an OOM kill may end the window.
func profileCaptureRequest(c state.ClaimedProfileCapture) profileproto.CaptureRequest {
	req := profileproto.CaptureRequest{CaptureID: strings.ReplaceAll(c.ID, "-", ""), Kinds: c.Capture.Kinds,
		DurationMillis: int64(c.Capture.DurationSeconds) * 1000}
	if c.Capture.Trigger == api.ProfileCaptureTriggerMemoryPressure && req.WantsKind(api.ProfileKindHeap) && req.Duration() > api.ProfileCaptureCheckpointInterval {
		req.CheckpointMillis = api.ProfileCaptureCheckpointInterval.Milliseconds()
	}
	return req
}

// profileCaptureOutcome maps a capture to its terminal record. A capture
// that reached the guest but found no instrumented process is still ready:
// its reason explains the empty result.
func profileCaptureOutcome(out ProfileCaptureOutcome, err error) (api.ProfileCapture, []state.ProfileCaptureBlob) {
	if err != nil {
		return api.ProfileCapture{Status: api.ProfileCaptureFailed, InstanceID: out.InstanceID, DeploymentID: out.DeploymentID, Reason: profileCaptureFailureReason(err)}, nil
	}
	result := api.ProfileCapture{Status: api.ProfileCaptureReady, InstanceID: out.InstanceID, DeploymentID: out.DeploymentID,
		Processes: out.Result.Processes, Dropped: out.Result.Dropped, Reason: out.Result.Reason, Partial: out.Result.Partial, Profiles: []api.ProfileCaptureProfile{}}
	blobs := make([]state.ProfileCaptureBlob, 0, len(out.Result.Profiles))
	for _, p := range out.Result.Profiles {
		blob := state.ProfileCaptureBlob{Kind: p.Kind, ProcessID: p.ProcessID, Profile: p.Profile}
		if !state.ProfileCaptureBlobFits(blob) {
			// A profile the store cannot hold (a guest-reported process id
			// too long, say) is dropped; the rest of the capture is kept.
			result.Dropped++
			continue
		}
		blobs = append(blobs, blob)
		result.Profiles = append(result.Profiles, api.ProfileCaptureProfile{Kind: p.Kind, ProcessID: p.ProcessID, Bytes: len(p.Profile)})
	}
	return result, blobs
}

type profileCaptureFinisher interface {
	FinishProfileCapture(ctx context.Context, id string, result api.ProfileCapture, blobs []state.ProfileCaptureBlob, now time.Time) error
}

// finishProfileCapture stores a capture's terminal record, retrying a
// transient store error. A ready capture whose profiles still cannot be
// stored finishes failed with a reason instead of expiring later as
// interrupted. ErrNotFound (the row expired or was deleted) is final.
func finishProfileCapture(ctx context.Context, queue profileCaptureFinisher, id string, result api.ProfileCapture, blobs []state.ProfileCaptureBlob, wait func(time.Duration)) error {
	var err error
	for attempt := range profileCaptureFinishAttempts {
		if attempt > 0 {
			wait(time.Duration(attempt) * time.Second)
		}
		if err = queue.FinishProfileCapture(ctx, id, result, blobs, time.Now()); err == nil || errors.Is(err, state.ErrNotFound) {
			return err
		}
	}
	if result.Status != api.ProfileCaptureReady {
		return err
	}
	failed := api.ProfileCapture{Status: api.ProfileCaptureFailed, InstanceID: result.InstanceID, DeploymentID: result.DeploymentID, Reason: profileCaptureUnstoredReason}
	if ferr := queue.FinishProfileCapture(ctx, id, failed, nil, time.Now()); ferr != nil {
		return errors.Join(err, ferr)
	}
	return fmt.Errorf("profiles not stored, capture marked failed: %w", err)
}

// profileCaptureResultLabel classifies a finished capture for metrics: ready,
// why the capture failed, or why its result was not stored.
func profileCaptureResultLabel(captureErr, finishErr error) string {
	switch {
	case errors.Is(finishErr, state.ErrNotFound):
		return "gone" // deleted or expired while it ran
	case finishErr != nil:
		return "not_stored"
	case captureErr == nil:
		return "ready"
	case errors.Is(captureErr, ErrNoRunningInstance):
		return "no_running_instance"
	}
	switch status.Code(captureErr) {
	case codes.NotFound:
		return "instance_stopped"
	case codes.FailedPrecondition, codes.InvalidArgument:
		return "guest_refused"
	case codes.DeadlineExceeded:
		return "timeout"
	}
	if errors.Is(captureErr, context.DeadlineExceeded) {
		return "timeout"
	}
	return "unreachable"
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
