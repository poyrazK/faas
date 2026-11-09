// adr: 20261031193635
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
)

type branchEgressStub struct {
	policy    state.EnvironmentBranchEgressPolicy
	err       error
	endpoints []state.EnvironmentBranchDatabaseEndpoint
	reads     int
}

func (s *branchEgressStub) EnvironmentBranchEgress(context.Context, string) (state.EnvironmentBranchEgressPolicy, error) {
	s.reads++
	return s.policy, s.err
}

func (s *branchEgressStub) EnvironmentBranchDatabaseEndpoints(context.Context, string) ([]state.EnvironmentBranchDatabaseEndpoint, error) {
	return s.endpoints, nil
}

func containedBranchStub() *branchEgressStub {
	return &branchEgressStub{
		policy:    state.EnvironmentBranchEgressPolicy{Mode: state.EnvironmentBranchEgressContained, EnvironmentID: "env", Scope: "pr-1"},
		endpoints: []state.EnvironmentBranchDatabaseEndpoint{{Host: "EP-Branch.eu.neon.tech", Port: 5432}},
	}
}

func branchWakeRequest(instance string) WakeRequest {
	return WakeRequest{
		Instance: instance, AppID: "app-branch", AccountID: "acct", DeploymentID: "dep-branch",
		BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 512,
		Plan: api.PlanPro, EgressMbit: 100, Snapshot: usableSnapshot(),
	}
}

func TestContainedBranchWakeInstallsTheProfileAndDeniesHostActions(t *testing.T) {
	run := &rehearsalInputRunner{}
	stub := containedBranchStub()
	m := newTestManager(run, &fakeVMM{}).WithEnvironmentBranchEgress(stub)
	inst, err := m.Wake(t.Context(), branchWakeRequest("branch-a"))
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "branch-a") })
	if !inst.Net.EnvironmentBranchContained {
		t.Fatal("the contained branch instance has no contained profile")
	}
	var installed bool
	for _, batch := range run.batches {
		if strings.Contains(string(batch), "add chain inet "+netns.EnvironmentBranchTable+" forward") {
			installed = true
		}
	}
	if !installed {
		t.Fatal("the contained profile was not installed in an atomic transaction")
	}
	if _, _, err := m.InstanceExternalActionIdentity(t.Context(), "branch-a"); !errors.Is(err, ErrEnvironmentBranchExternalActionDenied) {
		t.Fatalf("external action identity = %v, want denial", err)
	}
	if _, _, err := m.WorkloadTokenIdentity(t.Context(), "branch-a"); !errors.Is(err, ErrEnvironmentBranchExternalActionDenied) {
		t.Fatalf("workload token identity = %v, want denial", err)
	}

	// Only the branch's own database name enters branch_db.
	source := inst.Net.HostIP
	owner, ok := m.ResolveServiceCaller(source)
	if !ok {
		t.Fatal("the branch instance owns no source address")
	}
	addrs := []netip.Addr{netip.MustParseAddr("203.0.113.7")}
	before := len(run.batches)
	if err := m.AllowResolvedEgressForName(t.Context(), source, owner, "ep-parent.eu.neon.tech", addrs, time.Minute, 5432); err != nil {
		t.Fatal(err)
	}
	for _, batch := range run.batches[before:] {
		if strings.Contains(string(batch), netns.EnvironmentBranchDatabaseSet) {
			t.Fatalf("the parent's database entered branch_db:\n%s", batch)
		}
	}
	before = len(run.batches)
	if err := m.AllowResolvedEgressForName(t.Context(), source, owner, "ep-branch.eu.neon.tech.", addrs, time.Minute, 5432); err != nil {
		t.Fatal(err)
	}
	var added bool
	for _, batch := range run.batches[before:] {
		if strings.Contains(string(batch), "add element inet gregale_branch branch_db { 203.0.113.7 . 5432 timeout") {
			added = true
		}
	}
	if !added {
		t.Fatal("the branch database did not enter branch_db")
	}
	before = len(run.batches)
	if err := m.AllowResolvedEgressForName(t.Context(), source, owner, "ep-branch.eu.neon.tech", addrs, time.Minute, 5432); err != nil || len(run.batches) != before {
		t.Fatalf("a repeat within half the TTL re-ran nft (runs %d, err %v)", len(run.batches)-before, err)
	}
	// An older resolver sends no name: nothing enters branch_db.
	if err := m.AllowResolvedEgress(t.Context(), source, owner, []netip.Addr{netip.MustParseAddr("203.0.113.9")}, time.Minute, 5432); err != nil {
		t.Fatal(err)
	}
	for _, batch := range run.batches[before:] {
		if strings.Contains(string(batch), "203.0.113.9 . 5432 timeout") && strings.Contains(string(batch), netns.EnvironmentBranchDatabaseSet) {
			t.Fatal("a nameless answer entered branch_db")
		}
	}
}

func TestUncontainedBranchWakeIsCachedAndUnchanged(t *testing.T) {
	run := &rehearsalInputRunner{}
	stub := &branchEgressStub{policy: state.EnvironmentBranchEgressPolicy{Mode: state.EnvironmentBranchEgressLive, Scope: "pr-1"}}
	m := newTestManager(run, &fakeVMM{}).WithEnvironmentBranchEgress(stub)
	for _, instance := range []string{"live-a", "live-b"} {
		inst, err := m.Wake(t.Context(), branchWakeRequest(instance))
		if err != nil {
			t.Fatalf("Wake %s: %v", instance, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
		if inst.Net.EnvironmentBranchContained {
			t.Fatal("a live branch instance got the contained profile")
		}
		if _, _, err := m.InstanceExternalActionIdentity(t.Context(), instance); err != nil {
			t.Fatalf("a live branch instance lost its external actions: %v", err)
		}
	}
	if stub.reads != 1 {
		t.Fatalf("policy reads = %d, want 1 (an uncontained deployment is cached)", stub.reads)
	}
}

func TestBranchContainmentAdmissionFailsBeforeAllocation(t *testing.T) {
	contained := func() *branchEgressStub { return containedBranchStub() }
	for _, tc := range []struct {
		name   string
		stub   *branchEgressStub
		mutate func(*WakeRequest)
		runner Runner
	}{
		{"requested without authority", nil, func(r *WakeRequest) { r.EnvironmentBranchContained = true }, &rehearsalInputRunner{}},
		{"store error", &branchEgressStub{err: errors.New("offline")}, nil, &rehearsalInputRunner{}},
		{"branch deleted", &branchEgressStub{err: state.ErrEnvironmentBranchGone}, nil, &rehearsalInputRunner{}},
		{"requested but live", &branchEgressStub{policy: state.EnvironmentBranchEgressPolicy{Mode: state.EnvironmentBranchEgressLive}}, func(r *WakeRequest) { r.EnvironmentBranchContained = true }, &rehearsalInputRunner{}},
		{"requested without deployment", contained(), func(r *WakeRequest) { r.EnvironmentBranchContained = true; r.DeploymentID = "" }, &rehearsalInputRunner{}},
		{"static egress", contained(), func(r *WakeRequest) { r.StaticEgressIP = "198.51.100.4" }, &rehearsalInputRunner{}},
		{"non-atomic firewall", contained(), nil, &fakeRunner{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := &fakeVMM{}
			m := newTestManager(tc.runner, vmm)
			if tc.stub != nil {
				m.WithEnvironmentBranchEgress(tc.stub)
			}
			req := branchWakeRequest("branch-refused")
			if tc.mutate != nil {
				tc.mutate(&req)
			}
			if _, err := m.Wake(t.Context(), req); err == nil {
				t.Fatal("unsafe wake accepted")
			}
			if m.LeasedCount() != 0 || m.LiveCount() != 0 || vmm.bootCount != 0 || len(vmm.restored) != 0 {
				t.Fatal("a refused containment reached native effects")
			}
		})
	}
}

func TestResidentContainedBranchRechecksBeforeResume(t *testing.T) {
	run := &rehearsalInputRunner{}
	stub := containedBranchStub()
	m := newTestManager(run, &fakeVMM{}).WithEnvironmentBranchEgress(stub)
	inst, err := m.Wake(t.Context(), branchWakeRequest("branch-resume"))
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "branch-resume") })
	before := len(run.batches)
	if err := m.ensureRehearsalNetwork(t.Context(), inst); err != nil {
		t.Fatalf("resume recheck: %v", err)
	}
	if len(run.batches) != before+1 || !strings.Contains(string(run.batches[before]), "flush chain inet gregale_branch forward") {
		t.Fatal("resume did not reinstall the contained profile atomically")
	}
	stub.err = state.ErrEnvironmentBranchGone
	if err := m.ensureRehearsalNetwork(t.Context(), inst); err == nil {
		t.Fatal("a deleted branch's resident instance resumed")
	}
}
