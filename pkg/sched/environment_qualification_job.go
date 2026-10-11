package sched

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// runEnvironmentQualificationJob executes one reviewed command through the
// private VMMD path and accepts only its matching clean exit receipt. Guest
// output manifests and task tokens are deliberately discarded.
func (e *Engine) runEnvironmentQualificationJob(ctx context.Context, claimed state.EnvironmentWorkloadQualificationRequest,
	frame state.EnvironmentQualificationExecution, accountID string, plan api.Plan, env map[string]string, sealedEnv []fcvm.SealedEnvEntry,
	beforeStart func(context.Context, state.Instance) error) (state.EnvironmentQualificationJobSmokeEvidence, error) {
	var zero state.EnvironmentQualificationJobSmokeEvidence
	policy, _, err := state.EnvironmentQualificationJobSmokePolicyFor(claimed)
	if err != nil {
		return zero, err
	}
	if e.jobVmmClient == nil || e.jobExitWaiter == nil || beforeStart == nil || accountID == "" || !plan.Valid() || frame.InstanceID != claimed.ReservedInstanceID ||
		frame.RequestID != claimed.ID || frame.GraphID != claimed.GraphID || frame.Attempt != claimed.Attempt || frame.Resource != claimed.Resource ||
		frame.AppID != claimed.AppID || frame.DeploymentID != claimed.DeploymentID || frame.NodeID == "" || frame.Artifact != claimed.Artifact ||
		frame.RAMMB <= 0 || frame.Attempt > int64(1<<31-1) {
		return zero, errors.Join(state.ErrConflict, state.ErrEnvironmentWorkloadPreparationUnavailable)
	}
	leaseToken := uuid.NewString()
	spec := JobVmmSpec{QualificationExecution: &frame, AccountID: accountID, RunID: frame.RequestID, TaskIndex: int(frame.Attempt),
		InstanceID: frame.InstanceID, ImageRef: policy.ImageKey, Command: append([]string(nil), policy.Command...), RAMMB: frame.RAMMB,
		Env: env, SealedEnvEntries: append([]fcvm.SealedEnvEntry(nil), sealedEnv...), StartHeld: true,
		TaskTimeoutSec: policy.TimeoutSeconds, LeaseToken: leaseToken, NodeID: frame.NodeID, Plan: plan,
		KernelKey: KernelKey(e.fcVer), BaseKey: BaseKey(""), VcpuCount: 1}
	boot, err := e.jobVmmClient.JobColdBoot(ctx, spec)
	if err != nil {
		return zero, fmt.Errorf("qualification job cold boot: %w", err)
	}
	hostIP, hostIPErr := netip.ParseAddr(boot.HostIP)
	if boot.InstanceID != frame.InstanceID || boot.NodeID != frame.NodeID || !boot.StartHeld || boot.Netns == "" || boot.GuestUID <= 0 ||
		hostIPErr != nil || !hostIP.Is4() || !hostIP.IsPrivate() || hostIP.String() != boot.HostIP {
		return zero, state.ErrConflict
	}
	if err := beforeStart(ctx, state.Instance{ID: boot.InstanceID, AppID: frame.AppID, DeploymentID: frame.DeploymentID,
		NodeID: boot.NodeID, WakeID: frame.WakeID, State: string(state.StateRunning), Netns: boot.Netns, HostIP: boot.HostIP, GuestUID: boot.GuestUID}); err != nil {
		return zero, fmt.Errorf("qualification job pre-start validation: %w", err)
	}
	exit, err := e.jobExitWaiter.WaitJobExit(ctx, JobExitSpec{AccountID: accountID, RunID: frame.RequestID,
		TaskIndex: int(frame.Attempt), InstanceID: frame.InstanceID, NodeID: frame.NodeID,
		LeaseToken: leaseToken, Deadline: fcvm.EffectiveDestroyWait(policy.TimeoutSeconds)})
	if err != nil {
		return zero, fmt.Errorf("qualification job exit wait: %w", err)
	}
	if exit.LeaseToken != leaseToken {
		return zero, state.ErrConflict
	}
	evidence, err := state.NewEnvironmentQualificationJobSmokeEvidence(claimed, frame.InstanceID, exit.ExitCode, exit.ErrorClass, exit.Signal)
	if err != nil {
		return zero, err
	}
	return evidence, evidence.ValidateFor(claimed, frame.InstanceID)
}
