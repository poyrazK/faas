package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrNoRunningInstance means an on-demand profile capture found no RUNNING
// instance to profile. Captures never wake a parked app: a fresh process would
// not show the state the caller is investigating.
var ErrNoRunningInstance = errors.New("sched: app has no running instance")

// ProfileCaptureOutcome is the result of one on-demand capture (ADR-1155).
type ProfileCaptureOutcome struct {
	InstanceID   string
	DeploymentID string
	// PlanMB is the captured instance's memory limit, used when the guest
	// reports the workload was OOM-killed during the window.
	PlanMB int
	Result profileproto.CaptureResult
}

type profileCaptureRouter interface {
	CaptureProfile(ctx context.Context, nodeID, instance string, req profileproto.CaptureRequest) (profileproto.CaptureResult, error)
}

// CaptureAppProfile runs a capture on the requested RUNNING instance or,
// when instanceID is empty, on the app's earliest-started RUNNING instance.
// started, when set, runs once the instance is chosen and before the guest
// is contacted, so the capture row names its instance while it runs.
func (e *Engine) CaptureAppProfile(ctx context.Context, appID, instanceID string, req profileproto.CaptureRequest, started func(instanceID, deploymentID string)) (ProfileCaptureOutcome, error) {
	if e.store == nil || e.vmm == nil {
		return ProfileCaptureOutcome{}, errors.New("sched: CaptureAppProfile requires a store and vmm router")
	}
	router, ok := e.vmm.(profileCaptureRouter)
	if !ok {
		return ProfileCaptureOutcome{}, errors.New("sched: vmm router does not support profile captures")
	}
	rows, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return ProfileCaptureOutcome{}, fmt.Errorf("sched: CaptureAppProfile list instances: %w", err)
	}
	target, ok := pickProfileInstance(rows, instanceID)
	if !ok {
		return ProfileCaptureOutcome{}, ErrNoRunningInstance
	}
	if started != nil {
		started(target.ID, target.DeploymentID)
	}
	result, err := router.CaptureProfile(ctx, target.NodeID, target.ID, req)
	if err != nil {
		return ProfileCaptureOutcome{InstanceID: target.ID, DeploymentID: target.DeploymentID, PlanMB: target.RAMMB}, err
	}
	return ProfileCaptureOutcome{InstanceID: target.ID, DeploymentID: target.DeploymentID, PlanMB: target.RAMMB, Result: result}, nil
}

// QueueMemoryPressureCapture turns a guest memory-pressure report into one
// automatic heap capture on that instance (ADR-1155), so the heap is
// profiled before an OOM kill. It returns queued=false with a reason when
// the capture is skipped: rate limited, already running, not running, or
// the plan lacks profiling.
func (e *Engine) QueueMemoryPressureCapture(ctx context.Context, instanceID string, availableMB, totalMB, anonMB int) (bool, string, error) {
	capture := api.ProfileCapture{Kinds: []string{api.ProfileKindHeap},
		DurationSeconds: int(api.ProfileCaptureAutomaticDuration / time.Second),
		Trigger:         api.ProfileCaptureTriggerMemoryPressure,
		TriggerDetail:   fmt.Sprintf("guest memory available fell to %d MiB of %d MiB (%d MiB anonymous)", availableMB, totalMB, anonMB)}
	return e.queueAutomaticCapture(ctx, instanceID, capture, api.ProfileCaptureAutomaticMinInterval)
}

// QueueSlowRequestCapture turns a gateway report of a request in flight for
// too long into one automatic stacks+CPU capture of the serving instance
// (ADR-1155): the stacks show where the request is waiting, the CPU where any
// time goes.
func (e *Engine) QueueSlowRequestCapture(ctx context.Context, instanceID, requestID, traceID, route string, inflight time.Duration) (bool, string, error) {
	if route == "" {
		route = "a request"
	}
	capture := api.ProfileCapture{Kinds: []string{api.ProfileKindStacks, api.ProfileKindCPU},
		DurationSeconds: int(api.ProfileCaptureSlowRequestDuration / time.Second),
		Trigger:         api.ProfileCaptureTriggerSlowRequest,
		TriggerDetail:   fmt.Sprintf("%s in flight for %s", truncateRunes(route, 160), inflight.Round(time.Second)),
		RequestID:       truncateRunes(requestID, 128), RequestTraceID: truncateRunes(traceID, 128)}
	return e.queueAutomaticCapture(ctx, instanceID, capture, api.ProfileCaptureSlowRequestMinInterval)
}

// QueueCPUSaturationCapture turns an instance the stats poller saw at its
// CPU quota for ProfileCPUSaturationAfter into one automatic CPU capture of
// that instance (ADR-1155), so the profile shows what used the CPU.
func (e *Engine) QueueCPUSaturationCapture(ctx context.Context, instanceID string, usedMillicores, quotaMillicores int, sustained time.Duration) (bool, string, error) {
	if quotaMillicores <= 0 {
		return false, "instance has no CPU quota", nil
	}
	capture := api.ProfileCapture{Kinds: []string{api.ProfileKindCPU},
		DurationSeconds: int(api.ProfileCaptureCPUSaturationDuration / time.Second),
		Trigger:         api.ProfileCaptureTriggerCPUSaturation,
		TriggerDetail: fmt.Sprintf("CPU use at %d%% of the %dm limit for %s",
			usedMillicores*100/quotaMillicores, quotaMillicores, sustained.Round(time.Second))}
	return e.queueAutomaticCapture(ctx, instanceID, capture, api.ProfileCaptureCPUSaturationMinInterval)
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// queueAutomaticCapture queues a platform-triggered capture of a RUNNING
// instance whose plan includes profiling, at most once per minInterval per
// app and trigger, and notifies the capture drain.
func (e *Engine) queueAutomaticCapture(ctx context.Context, instanceID string, capture api.ProfileCapture, minInterval time.Duration) (bool, string, error) {
	queue, ok := e.store.(state.ProfileCaptureQueue)
	if !ok {
		return false, "profile capture storage is unavailable", nil
	}
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		return false, "", err
	}
	if ins.State != string(state.StateRunning) {
		return false, "instance is not running", nil
	}
	app, err := e.store.AppByID(ctx, ins.AppID)
	if err != nil {
		return false, "", err
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return false, "", err
	}
	if limits, ok := api.LimitsFor(acct.Plan); !ok || !limits.Profiling.Enabled {
		return false, "plan does not include profiling", nil
	}
	now := time.Now().UTC()
	capture.ID, capture.AppID, capture.RequestedInstanceID = uuid.NewString(), app.ID, ins.ID
	capture.CreatedAt, capture.ExpiresAt = now, now.Add(api.ProfileCaptureRetention)
	switch err := queue.QueueAutomaticProfileCapture(ctx, app.AccountID, app.ID, capture, minInterval); {
	case errors.Is(err, state.ErrProfileCaptureRecent):
		return false, "an automatic capture ran for this app within the minimum interval", nil
	case errors.Is(err, state.ErrProfileCaptureActive):
		return false, "a capture is already queued or running for this app", nil
	case errors.Is(err, state.ErrProfileCaptureQuota):
		return false, "the account's hourly capture quota is used", nil
	case err != nil:
		return false, "", err
	}
	if e.notif != nil {
		payload, _ := json.Marshal(map[string]string{"capture_id": capture.ID})
		if err := e.notif.Notify(ctx, db.NotifyProfileCapture, string(payload)); err != nil {
			// The queued row is the source of truth; the safety tick claims it.
			e.log.Warn("sched: profile_capture: notify failed", "capture_id", capture.ID, "err", err)
		}
	}
	return true, "", nil
}

// linkOOMProfileCapture links an OOM kill to the newest heap capture started
// on the killed instance (ADR-1155), so the capture reads as "the heap before
// the kill". Best effort: the kill is handled either way.
func (e *Engine) linkOOMProfileCapture(ctx context.Context, appID, instanceID string, killedAt time.Time, peakMB, planMB int) {
	queue, ok := e.store.(state.ProfileCaptureQueue)
	if !ok || appID == "" {
		return
	}
	oom := state.ProfileCaptureOOM{KilledAt: killedAt, PeakMB: peakMB, PlanMB: planMB}
	id, err := queue.StampProfileCaptureOOM(ctx, appID, instanceID, oom, killedAt.Add(-api.ProfileCaptureOOMLinkWindow))
	switch {
	case errors.Is(err, state.ErrNotFound):
	case err != nil:
		e.log.Warn("sched: workload_oom: link profile capture", "instance", instanceID, "err", err)
	default:
		e.log.Info("sched: workload_oom: linked heap capture", "instance", instanceID, "capture_id", id)
	}
}

func pickProfileInstance(rows []state.Instance, instanceID string) (state.Instance, bool) {
	var running []state.Instance
	for _, ins := range rows {
		if ins.State != string(state.StateRunning) || ins.NodeID == "" {
			continue
		}
		if instanceID != "" && ins.ID != instanceID {
			continue
		}
		running = append(running, ins)
	}
	if len(running) == 0 {
		return state.Instance{}, false
	}
	sort.Slice(running, func(i, j int) bool {
		if !running[i].StartedAt.Equal(running[j].StartedAt) {
			return running[i].StartedAt.Before(running[j].StartedAt)
		}
		return running[i].ID < running[j].ID
	})
	return running[0], true
}

// CaptureProfile routes an on-demand capture to the instance's vmmd.
func (r *VMMRouter) CaptureProfile(ctx context.Context, nodeID, instance string, req profileproto.CaptureRequest) (profileproto.CaptureResult, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return profileproto.CaptureResult{}, err
	}
	capturer, ok := cli.(interface {
		CaptureProfile(context.Context, string, profileproto.CaptureRequest) (profileproto.CaptureResult, error)
	})
	if !ok {
		return profileproto.CaptureResult{}, fmt.Errorf("vmm router: profile capture unsupported by node %q", nodeID)
	}
	return capturer.CaptureProfile(ctx, instance, req)
}

// CaptureProfile calls vmmd's CaptureProfile. gRPC status codes are returned
// unchanged so schedd can relay NotFound/FailedPrecondition to apid.
func (c *VMMClient) CaptureProfile(ctx context.Context, instance string, req profileproto.CaptureRequest) (profileproto.CaptureResult, error) {
	resp, err := c.cli.CaptureProfile(ctx, &vmmdpb.CaptureProfileRequest{Instance: instance, CaptureId: req.CaptureID, Kinds: req.Kinds, DurationMs: req.DurationMillis, CheckpointMs: req.CheckpointMillis})
	if err != nil {
		return profileproto.CaptureResult{}, err
	}
	out := profileproto.CaptureResult{Processes: int(resp.GetProcesses()), Dropped: int(resp.GetDropped()), Reason: resp.GetReason(), Partial: resp.GetPartial(),
		OOMKilled: resp.GetOomKilled(), OOMPeakMB: int(resp.GetOomPeakMb())}
	for _, p := range resp.GetProfiles() {
		out.Profiles = append(out.Profiles, profileproto.CapturedProfile{Kind: p.GetKind(), ProcessID: p.GetProcessId(), Profile: p.GetProfile(), FromUnixNano: p.GetFromUnixNano(), UntilUnixNano: p.GetUntilUnixNano()})
	}
	if err := out.Validate(); err != nil {
		return profileproto.CaptureResult{}, fmt.Errorf("vmm client: %w", err)
	}
	return out, nil
}
