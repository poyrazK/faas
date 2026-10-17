// adr: 1351
package fcvm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

type recorderWakePolicy struct {
	mu       sync.Mutex
	err      error
	identity rehearsalrecorder.Identity
	starts   int
	failed   bool
}

func (*recorderWakePolicy) ScenarioRehearsalNetworkRun(context.Context, string) (string, error) {
	return rehearsalNetworkTestRun, nil
}
func (*recorderWakePolicy) ScenarioRehearsalSimulationRun(context.Context, string) (string, error) {
	return rehearsalNetworkTestRun, nil
}
func (p *recorderWakePolicy) StartScenarioRecorder(_ context.Context, id rehearsalrecorder.Identity) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts++
	p.identity = id
	return uuid.NewString(), p.err
}
func (*recorderWakePolicy) BeginScenarioRecorderRequest(context.Context, rehearsalrecorder.Identity, string) (string, error) {
	return uuid.NewString(), nil
}
func (p *recorderWakePolicy) SettleScenarioRecorder(_ context.Context, _ rehearsalrecorder.Identity, _ string, failed, _ bool) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = p.failed || failed
	if p.failed {
		return "failed", nil
	}
	return "recording", nil
}

func TestRehearsalRecorderMustPersistBeforeAnyBootEffects(t *testing.T) {
	p := &recorderWakePolicy{err: errors.New("recorder storage offline")}
	runner, vmm := &rehearsalInputRunner{}, &fakeVMM{}
	m := newTestManager(runner, vmm).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
	req := WakeRequest{Instance: "refused-recorder", AppID: "app", AccountID: "account", DeploymentID: "deployment", Plan: api.PlanHobby}
	if _, err := m.Wake(t.Context(), req); err == nil {
		t.Fatal("guest booted without a durable recorder")
	}
	if vmm.bootCount != 0 || len(vmm.restored) != 0 || len(runner.commands) != 0 || m.LeasedCount() != 0 {
		t.Fatal("recorder refusal reached boot effects")
	}
}

func TestRehearsalRecorderBindsExecutionAcrossRestoreResumeAndRetirement(t *testing.T) {
	for _, mode := range []string{"cold", "restore", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			p := &recorderWakePolicy{}
			vmm := &freezingVMM{fakeVMM: &fakeVMM{}}
			m := newTestManager(&rehearsalInputRunner{}, vmm).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
			req := WakeRequest{Instance: "recorded-" + mode, AppID: "app", AccountID: "account", DeploymentID: "deployment", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby}
			if mode != "cold" {
				req.Snapshot = usableSnapshot()
			}
			if mode == "fallback" {
				vmm.resumeHookErr = errors.New("restore failed")
			}
			if _, err := m.Wake(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Destroy(context.WithoutCancel(t.Context()), req.Instance) })
			session, err := m.InstanceRehearsalRecorder(req.Instance)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Freeze(t.Context(), req.Instance, FreezeOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := m.Thaw(t.Context(), req.Instance); err != nil {
				t.Fatal(err)
			}
			again, err := m.InstanceRehearsalRecorder(req.Instance)
			if err != nil || again != session {
				t.Fatal("resume replaced the recorder owner")
			}
			p.mu.Lock()
			identity, starts := p.identity, p.starts
			p.mu.Unlock()
			if starts != 1 || identity.AccountID != req.AccountID || identity.AppID != req.AppID || identity.InstanceID != req.Instance || identity.DeploymentID != req.DeploymentID || identity.RunID != rehearsalNetworkTestRun {
				t.Fatalf("recorder execution binding lost: %+v starts=%d", identity, starts)
			}
			if err := m.Destroy(t.Context(), req.Instance); err != nil {
				t.Fatal(err)
			}
			select {
			case <-session.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("retired execution left recorder goroutine running")
			}
			p.mu.Lock()
			failed := p.failed
			p.mu.Unlock()
			if !failed {
				t.Fatal("retirement before acknowledgement became successful completion")
			}
		})
	}
}
