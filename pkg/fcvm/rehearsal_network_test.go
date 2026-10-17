// adr: 1349
package fcvm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

type rehearsalPolicyStub struct {
	run string
	err error
}

type rehearsalInputRunner struct {
	fakeRunner
	batches   [][]byte
	failInput bool
}

func (r *rehearsalInputRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	if r.failInput {
		return errors.New("atomic firewall failed")
	}
	r.batches = append(r.batches, append([]byte(nil), input...))
	return r.Run(ctx, argv)
}

func (p *rehearsalPolicyStub) ScenarioRehearsalNetworkRun(context.Context, string) (string, error) {
	return p.run, p.err
}

const rehearsalNetworkTestRun = "abababababababababababababababab"

func TestRehearsalNetworkPolicyPreservesUnownedExecutionAndBuilderModes(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithRehearsalNetworkPolicies(&rehearsalPolicyStub{})
	for _, req := range []WakeRequest{{ExecutionOnly: true}, {ExportDir: "/build-export"}} {
		if err := m.applyRehearsalNetwork(t.Context(), &req); err != nil {
			t.Fatalf("ordinary non-app mode was rejected: %v", err)
		}
	}
	if err := m.applyRehearsalNetwork(t.Context(), &WakeRequest{}); err == nil {
		t.Fatal("ordinary serving boot without an app identity was accepted")
	}
}

func TestRehearsalNetworkAdmissionFailsBeforeAllocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy *rehearsalPolicyStub
		req    WakeRequest
	}{
		{"unavailable", nil, WakeRequest{RehearsalRunID: rehearsalNetworkTestRun}},
		{"store error", &rehearsalPolicyStub{err: errors.New("offline")}, WakeRequest{}},
		{"wrong run", &rehearsalPolicyStub{run: rehearsalNetworkTestRun}, WakeRequest{RehearsalRunID: strings.Repeat("c", 32)}},
		{"execution", &rehearsalPolicyStub{run: rehearsalNetworkTestRun}, WakeRequest{ExecutionOnly: true}},
		{"app task", &rehearsalPolicyStub{run: rehearsalNetworkTestRun}, WakeRequest{AppTaskOnly: true}},
		{"non-atomic runtime", &rehearsalPolicyStub{run: rehearsalNetworkTestRun}, WakeRequest{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, vmm := &fakeRunner{}, &fakeVMM{}
			m := newTestManager(runner, vmm)
			if tc.policy != nil {
				m.WithRehearsalNetworkPolicies(tc.policy)
			}
			tc.req.Instance, tc.req.AppID = "rehearsal-refused", "app"
			if _, err := m.Wake(t.Context(), tc.req); err == nil {
				t.Fatal("unsafe wake accepted")
			}
			if m.LeasedCount() != 0 || m.LiveCount() != 0 || len(runner.commands) != 0 || vmm.bootCount != 0 || len(vmm.restored) != 0 {
				t.Fatal("refused policy reached native effects")
			}
		})
	}
}

func TestRehearsalNetworkLegacyWakeRestoreAndFallbackKeepGuard(t *testing.T) {
	for _, mode := range []string{"cold", "restore", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			runner, vmm := &rehearsalInputRunner{}, &fakeVMM{}
			m := newTestManager(runner, vmm).WithRehearsalNetworkPolicies(&rehearsalPolicyStub{run: rehearsalNetworkTestRun})
			req := WakeRequest{Instance: "rehearsal-" + mode, AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby}
			if mode != "cold" {
				req.Snapshot = usableSnapshot()
			}
			if mode == "fallback" {
				vmm.resumeHookErr = errors.New("resume failed")
			}
			inst, err := m.Wake(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Destroy(context.WithoutCancel(t.Context()), req.Instance) })
			if inst.Net.RehearsalRunID != rehearsalNetworkTestRun {
				t.Fatal("legacy caller lost durable containment")
			}
			found := false
			for _, batch := range runner.batches {
				if strings.Contains(string(batch), "inet gregale_rehearsal forward") {
					found = true
				}
			}
			if !found {
				t.Fatal("guest started without containment hook")
			}
			if _, reusable := m.preparedPolicy(WakeRequest{Plan: api.PlanHobby, RehearsalRunID: rehearsalNetworkTestRun}); reusable {
				t.Fatal("unguarded prepared namespace can be reused")
			}
		})
	}
}

func TestRehearsalNetworkResumeRequiresPolicyAndReinstallsGuard(t *testing.T) {
	runner, vmm := &rehearsalInputRunner{}, &fakeVMM{}
	policy := &rehearsalPolicyStub{run: rehearsalNetworkTestRun}
	m := newTestManager(runner, vmm).WithRehearsalNetworkPolicies(policy)
	inst := &Instance{AppID: "app", Net: netns.Config{RehearsalRunID: rehearsalNetworkTestRun, Netns: "rehearsal", Tap: "tap0", VethPeer: "veth0"}}
	policy.err = errors.New("offline")
	if err := m.resumeWithRehearsalNetwork(t.Context(), inst); err == nil || len(vmm.resumed) != 0 {
		t.Fatal("resume bypassed policy read failure")
	}
	policy.err, policy.run = nil, ""
	if err := m.resumeWithRehearsalNetwork(t.Context(), inst); err == nil || len(vmm.resumed) != 0 {
		t.Fatal("resume bypassed lost enrollment")
	}
	policy.run = rehearsalNetworkTestRun
	if err := m.resumeWithRehearsalNetwork(t.Context(), inst); err != nil {
		t.Fatal(err)
	}
	if len(vmm.resumed) != 1 || len(runner.commands) == 0 {
		t.Fatal("resume did not reinstall guard")
	}
	runner.failInput = true
	if err := m.resumeWithRehearsalNetwork(t.Context(), inst); err == nil || len(vmm.resumed) != 1 {
		t.Fatal("resume ignored firewall failure")
	}
}

func TestRehearsalNetworkHostActionsRequireUncontainedIdentity(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	inst := &Instance{AppID: "app", AccountID: "account"}
	m.live["instance"] = inst
	if app, account, err := m.InstanceExternalActionIdentity(t.Context(), "instance"); err != nil || app != "app" || account != "account" {
		t.Fatalf("ordinary host identity changed: %q %q %v", app, account, err)
	}
	inst.Net.RehearsalRunID = rehearsalNetworkTestRun
	if _, _, err := m.InstanceExternalActionIdentity(t.Context(), "instance"); !errors.Is(err, ErrRehearsalExternalActionDenied) {
		t.Fatalf("vsock action bypassed installed containment: %v", err)
	}
	inst.Net.RehearsalRunID = ""
	policy := &rehearsalPolicyStub{run: rehearsalNetworkTestRun}
	m.WithRehearsalNetworkPolicies(policy)
	if _, _, err := m.InstanceExternalActionIdentity(t.Context(), "instance"); !errors.Is(err, ErrRehearsalExternalActionDenied) {
		t.Fatalf("vsock action bypassed durable containment: %v", err)
	}
	policy.run, policy.err = "", errors.New("offline")
	if _, _, err := m.InstanceExternalActionIdentity(t.Context(), "instance"); err == nil {
		t.Fatal("unavailable policy authority granted external action identity")
	}
}
