package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrNoRunningInstance means an on-demand profile capture found no RUNNING
// instance to profile. Captures never wake a parked app: a fresh process would
// not show the state the caller is investigating.
var ErrNoRunningInstance = errors.New("sched: app has no running instance")

// ProfileCaptureOutcome is the result of one on-demand capture (ADR-967).
type ProfileCaptureOutcome struct {
	InstanceID   string
	DeploymentID string
	Result       profileproto.CaptureResult
}

type profileCaptureRouter interface {
	CaptureProfile(ctx context.Context, nodeID, instance string, req profileproto.CaptureRequest) (profileproto.CaptureResult, error)
}

// CaptureAppProfile runs a capture on the requested RUNNING instance or,
// when instanceID is empty, on the app's earliest-started RUNNING instance.
func (e *Engine) CaptureAppProfile(ctx context.Context, appID, instanceID string, req profileproto.CaptureRequest) (ProfileCaptureOutcome, error) {
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
	result, err := router.CaptureProfile(ctx, target.NodeID, target.ID, req)
	if err != nil {
		return ProfileCaptureOutcome{}, err
	}
	return ProfileCaptureOutcome{InstanceID: target.ID, DeploymentID: target.DeploymentID, Result: result}, nil
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
	resp, err := c.cli.CaptureProfile(ctx, &vmmdpb.CaptureProfileRequest{Instance: instance, CaptureId: req.CaptureID, Kinds: req.Kinds, DurationMs: req.DurationMillis})
	if err != nil {
		return profileproto.CaptureResult{}, err
	}
	out := profileproto.CaptureResult{Processes: int(resp.GetProcesses()), Dropped: int(resp.GetDropped()), Reason: resp.GetReason()}
	for _, p := range resp.GetProfiles() {
		out.Profiles = append(out.Profiles, profileproto.CapturedProfile{Kind: p.GetKind(), ProcessID: p.GetProcessId(), Profile: p.GetProfile(), FromUnixNano: p.GetFromUnixNano(), UntilUnixNano: p.GetUntilUnixNano()})
	}
	if err := out.Validate(); err != nil {
		return profileproto.CaptureResult{}, fmt.Errorf("vmm client: %w", err)
	}
	return out, nil
}
