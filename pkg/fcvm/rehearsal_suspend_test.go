// adr: 20261017201015
package fcvm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

type suspendWakePolicy struct {
	recorderWakePolicy
	receipt, state, session, failStep string
	pauseMS                           int64
	paused                            chan struct{}
	onStep                            func(string) error
	running                           bool
	invocation                        string
}

func (p *suspendWakePolicy) AdvanceScenarioSuspendResume(_ context.Context, id rehearsalrecorder.Identity, session, receipt, expected, next string, pause, resume int64) (rehearsalresume.Spec, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed || id != p.identity || receipt != p.receipt || p.state != expected || p.failStep == next || p.session != "" && p.session != session {
		return rehearsalresume.Spec{}, errors.New("original suspend/resume receipt unavailable")
	}
	if p.onStep != nil {
		if err := p.onStep(next); err != nil {
			return rehearsalresume.Spec{}, err
		}
	}
	p.session, p.state = session, next
	if next == "paused" && p.paused != nil {
		close(p.paused)
	}
	spec := rehearsalresume.Spec{Version: 1, Checkpoint: "after_setup", Workloads: []string{"primary"}, PauseMS: p.pauseMS, WithinMS: 1000}
	if p.running {
		spec.Checkpoint = "managed_job"
		spec.WorkerCheckpoint = "after-commit"
	}
	return spec, nil
}

func (p *suspendWakePolicy) ScenarioSuspendResume(_ context.Context, id string) (rehearsalresume.Receipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id != p.receipt {
		return rehearsalresume.Receipt{}, errors.New("receipt differs")
	}
	return rehearsalresume.Receipt{ID: id, InstanceID: p.identity.InstanceID, InvocationID: p.invocation, InvocationAttempt: 1, State: p.state}, nil
}
func (p *suspendWakePolicy) FailScenarioSuspendResume(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id != p.receipt {
		return errors.New("receipt differs")
	}
	p.state = "failed"
	return nil
}

type suspendReadyVMM struct {
	*freezingVMM
	readyErr   error
	readyCalls int
}

func (v *suspendReadyVMM) WaitRehearsalResumeReady(context.Context, Lease, string, int) error {
	v.readyCalls++
	return v.readyErr
}

func TestSuspendResumeKeepsOriginalOwnerAndRequiresEveryPhysicalReceipt(t *testing.T) {
	for _, mode := range []string{"complete", "fence_failed", "pause_failed", "paused_receipt_failed", "containment_failed", "resume_failed", "readiness_failed", "resume_receipt_failed", "lost_owner"} {
		t.Run(mode, func(t *testing.T) {
			p := &suspendWakePolicy{receipt: uuid.NewString(), state: "claimed", pauseMS: 1}
			v := &suspendReadyVMM{freezingVMM: &freezingVMM{fakeVMM: &fakeVMM{}}}
			runner := &rehearsalInputRunner{}
			m := newTestManager(runner, v).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
			req := WakeRequest{Instance: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), DeploymentID: uuid.NewString(), BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby}
			if _, err := m.Wake(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Destroy(context.WithoutCancel(t.Context()), req.Instance) })
			original, err := m.InstanceRehearsalRecorder(req.Instance)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "fence_failed" {
				p.failStep = "fenced"
			}
			if mode == "paused_receipt_failed" {
				p.failStep = "paused"
			}
			if mode == "resume_receipt_failed" {
				p.failStep = "resumed"
			}
			if mode == "pause_failed" {
				v.pauseErr = errors.New("pause failed")
			}
			if mode == "readiness_failed" {
				v.readyErr = errors.New("not ready")
			}
			if mode == "resume_failed" {
				v.resumeHookErr = errors.New("resume hook failed")
			}
			if mode == "containment_failed" {
				runner.failInput = true
			}
			if mode == "lost_owner" {
				m.mu.Lock()
				delete(m.rehearsalRecorders, req.Instance)
				m.mu.Unlock()
				t.Cleanup(original.Stop)
			}
			p.onStep = func(next string) error {
				frozen, _ := m.FrozenState(req.Instance)
				if next == "paused" && !frozen {
					return errors.New("pause receipt preceded physical pause")
				}
				if next == "resumed" && (frozen || v.readyCalls != 1) {
					return errors.New("resume receipt preceded readiness")
				}
				return nil
			}
			err = m.RehearseSuspendResume(t.Context(), req.Instance, p.receipt)
			if (err == nil) != (mode == "complete") {
				t.Fatalf("mode %s: %v", mode, err)
			}
			if mode == "complete" {
				again, e := m.InstanceRehearsalRecorder(req.Instance)
				if e != nil || again != original || p.state != "resumed" || p.starts != 1 || len(v.resumes()) != 1 || len(v.resumeHookCalls) != 1 {
					t.Fatal("resume did not preserve its original VM and collector")
				}
				if m.LiveCount() != 1 || m.LeasedCount() != 1 {
					t.Fatal("resident VM lost its lease")
				}
			}
		})
	}
}

func TestSuspendResumeDestroyCancelsAndJoinsPausedOwner(t *testing.T) {
	p := &suspendWakePolicy{receipt: uuid.NewString(), state: "claimed", pauseMS: 30000, paused: make(chan struct{})}
	v := &suspendReadyVMM{freezingVMM: &freezingVMM{fakeVMM: &fakeVMM{}}}
	m := newTestManager(&rehearsalInputRunner{}, v).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
	req := WakeRequest{Instance: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), DeploymentID: uuid.NewString(), BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.RehearseSuspendResume(t.Context(), req.Instance, p.receipt) }()
	select {
	case <-p.paused:
	case <-time.After(3 * time.Second):
		t.Fatal("pause not observed")
	}
	if err := m.RehearseSuspendResume(t.Context(), req.Instance, p.receipt); err == nil {
		t.Fatal("concurrent pause adopted an active owner")
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("destroyed owner acknowledged resume")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("destroy did not join lifecycle owner")
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || len(v.resumes()) != 0 {
		t.Fatal("destroyed pause resumed or leaked its lease")
	}
}

func TestSuspendResumeJoinsDelayedOriginalRelayDetach(t *testing.T) {
	p := &suspendWakePolicy{receipt: uuid.NewString(), state: "claimed", pauseMS: 1}
	v := &suspendReadyVMM{freezingVMM: &freezingVMM{fakeVMM: &fakeVMM{}}}
	m := newTestManager(&rehearsalInputRunner{}, v).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
	req := WakeRequest{Instance: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), DeploymentID: uuid.NewString(), BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.WithoutCancel(t.Context()), req.Instance) })
	owner, err := m.InstanceRehearsalRecorder(req.Instance)
	if err != nil {
		t.Fatal(err)
	}
	var connection *rehearsalrecorder.Connection
	detached := make(chan struct{})
	connection, err = owner.Attach(func() { time.AfterFunc(10*time.Millisecond, func() { connection.Detach(false); close(detached) }) })
	if err != nil {
		t.Fatal(err)
	}
	if err = m.RehearseSuspendResume(t.Context(), req.Instance, p.receipt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-detached:
	default:
		t.Fatal("pause acknowledged before the original relay detached")
	}
	if _, err = owner.Begin(t.Context()); err != nil {
		t.Fatal("resumed original collector did not reopen admission")
	}
}
