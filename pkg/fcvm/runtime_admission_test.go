// adr: 386 — single-use native grants bind actual boot identity and cancellation.

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func admittedFixture(t *testing.T, m *Manager) AdmittedWakeRequest {
	t.Helper()
	m.WithRuntimeAdmissionNodeID(uuid.NewString())
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	b := runtimeadmission.Binding{ProtocolVersion: runtimeadmission.ProtocolVersion, Token: uuid.NewString(), InstanceID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), AccountID: uuid.NewString(), NodeID: identity.NodeID, Incarnation: identity.Incarnation, DesiredRevision: 9, EffectiveHash: strings.Repeat("a", 64), CapturedInputHash: strings.Repeat("b", 64), PayloadHash: strings.Repeat("c", 64), EgressRevision: 7, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	if err := m.UpdateAppEgressPolicy(t.Context(), b.AppID, 7, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}, []uint16{5432}); err != nil {
		t.Fatal(err)
	}
	req := WakeRequest{Instance: b.InstanceID, AppID: b.AppID, DeploymentID: b.DeploymentID, AccountID: b.AccountID, BaseKey: "/b.ext4", LayerKey: "/l.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanPro, EgressAllowlist: []string{"8.8.8.0/24"}, EgressPorts: []uint16{5432}}
	hash, err := NativeWakeInputHash(req)
	if err != nil {
		t.Fatal(err)
	}
	return AdmittedWakeRequest{Request: req, Binding: b, NativeInputHash: hash}
}

func TestNativeRuntimeAdmissionReceiptAndReplay(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	req := admittedFixture(t, m)
	inst, receipt, err := m.WakeAdmitted(t.Context(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Check(req.Binding, time.Now()); err != nil {
		t.Fatal(err)
	}
	if receipt.NativeInputHash != req.NativeInputHash || receipt.HostIP != inst.Lease.HostIP.String() || receipt.Netns != inst.Net.Netns || receipt.LeaseUID != int32(inst.Lease.UID) || m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("receipt did not describe the actual native instance")
	}
	if err := m.Destroy(t.Context(), req.Request.Instance); err != nil {
		t.Fatal(err)
	}
	for _, differentToken := range []bool{false, true} {
		if differentToken {
			req.Binding.Token = uuid.NewString()
		}
		if _, _, err := m.WakeAdmitted(t.Context(), req, nil); !errors.Is(err, runtimeadmission.ErrReplay) {
			t.Fatalf("destroyed instance replay err=%v", err)
		}
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || vmm.bootCount != 1 {
		t.Fatal("replay allocated native resources")
	}
}

func TestNativeRuntimeAdmissionRefusesBeforeAllocation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Manager, *AdmittedWakeRequest)
	}{
		{"old incarnation", func(_ *Manager, r *AdmittedWakeRequest) { r.Binding.Incarnation = uuid.NewString() }},
		{"wrong node", func(_ *Manager, r *AdmittedWakeRequest) { r.Binding.NodeID = uuid.NewString() }},
		{"expired", func(_ *Manager, r *AdmittedWakeRequest) {
			r.Binding.IssuedAtUnixNano = time.Now().Add(-time.Minute).UnixNano()
			r.Binding.ExpiresAtUnixNano = time.Now().Add(-time.Second).UnixNano()
		}},
		{"native projection changed", func(_ *Manager, r *AdmittedWakeRequest) { r.Request.LayerKey = "different" }},
		{"request identity", func(_ *Manager, r *AdmittedWakeRequest) { r.Binding.AppID = uuid.NewString() }},
		{"missing installed policy", func(m *Manager, r *AdmittedWakeRequest) { m.appEgressPolicies = map[string]appEgressPolicy{} }},
		{"old egress revision", func(_ *Manager, r *AdmittedWakeRequest) { r.Binding.EgressRevision = 6 }},
		{"conflicting CIDRs", func(m *Manager, r *AdmittedWakeRequest) {
			p := m.appEgressPolicies[r.Binding.AppID]
			p.allowlist = nil
			m.appEgressPolicies[r.Binding.AppID] = p
		}},
		{"conflicting ports", func(m *Manager, r *AdmittedWakeRequest) {
			p := m.appEgressPolicies[r.Binding.AppID]
			p.ports = nil
			m.appEgressPolicies[r.Binding.AppID] = p
		}},
		{"unrepresented plan ports", func(_ *Manager, r *AdmittedWakeRequest) {
			r.Request.Plan = api.PlanFree
			r.NativeInputHash, _ = NativeWakeInputHash(r.Request)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			run, vmm := &fakeRunner{}, &fakeVMM{}
			m := newTestManager(run, vmm)
			req := admittedFixture(t, m)
			test.mutate(m, &req)
			commands := len(run.commands)
			inst, receipt, err := m.WakeAdmitted(t.Context(), req, nil)
			if err == nil || inst != nil || receipt.Binding.Token != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 || vmm.bootCount != 0 || len(run.commands) != commands {
				t.Fatalf("refused request allocated resources: err=%v live=%d leased=%d", err, m.LiveCount(), m.LeasedCount())
			}
		})
	}
}

func TestNativeRuntimeAdmissionRestartChangesAuthority(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	req := admittedFixture(t, m)
	i, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		t.Fatal(err)
	}
	m.WithRuntimeAdmissionNodeID(uuid.NewString())
	same, _ := m.RuntimeAdmissionIdentity()
	if same != i {
		t.Fatal("identity changed within a live manager")
	}
	restarted := newTestManager(&fakeRunner{}, &fakeVMM{}).WithRuntimeAdmissionNodeID(i.NodeID)
	fresh, err := restarted.RuntimeAdmissionIdentity()
	if err != nil || fresh.Incarnation == i.Incarnation {
		t.Fatal("restarted manager retained old authority")
	}
	if _, _, err := restarted.WakeAdmitted(t.Context(), req, nil); !errors.Is(err, runtimeadmission.ErrStale) {
		t.Fatalf("old grant accepted after restart: %v", err)
	}
}

type admittedBlockingVMM struct {
	fakeVMM
	entered, release chan struct{}
}

func (v *admittedBlockingVMM) BootColdBoot(ctx context.Context, l Lease, spec ColdBootSpec) error {
	close(v.entered)
	<-v.release
	// Deliberately return success even after cancellation, exercising the
	// manager's last receipt gate rather than a cooperative boot fake.
	return v.fakeVMM.BootColdBoot(ctx, l, spec)
}

func TestNativeRuntimeAdmissionDestroyJoinsFlightAndLeavesNoReceipt(t *testing.T) {
	for _, stop := range []string{"destroy", "signal"} {
		t.Run(stop, func(t *testing.T) {
			vmm := &admittedBlockingVMM{entered: make(chan struct{}), release: make(chan struct{})}
			m := newTestManager(&fakeRunner{}, vmm)
			req := admittedFixture(t, m)
			bootDone := make(chan error, 1)
			go func() {
				_, receipt, err := m.WakeAdmitted(t.Context(), req, nil)
				if receipt.Binding.Token != "" {
					bootDone <- errors.New("cancelled boot received a receipt")
					return
				}
				bootDone <- err
			}()
			select {
			case <-vmm.entered:
			case <-time.After(time.Second):
				t.Fatal("boot did not start")
			}
			stopped := make(chan error, 1)
			go func() {
				if stop == "signal" {
					_, _, err := m.SignalAndKill(t.Context(), req.Request.Instance, 0, 0)
					stopped <- err
				} else {
					stopped <- m.Destroy(t.Context(), req.Request.Instance)
				}
			}()
			m.mu.Lock()
			flight := m.runtimeAdmissionFlights[req.Request.Instance]
			m.mu.Unlock()
			if flight == nil {
				t.Fatal("flight missing")
			}
			select {
			case <-flight.ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("stop did not cancel flight")
			}
			select {
			case err := <-stopped:
				t.Fatalf("stop finished during boot: %v", err)
			default:
			}
			close(vmm.release)
			select {
			case err := <-bootDone:
				if err == nil {
					t.Fatal("cancelled boot succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled boot stuck")
			}
			select {
			case err := <-stopped:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("stop stuck")
			}
			if m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatal("cancelled flight leaked native residency")
			}
		})
	}
}

func TestNativeRuntimeAdmissionExpiryDuringBoot(t *testing.T) {
	vmm := &admittedBlockingVMM{entered: make(chan struct{}), release: make(chan struct{})}
	m := newTestManager(&fakeRunner{}, vmm)
	req := admittedFixture(t, m)
	req.Binding.ExpiresAtUnixNano = time.Now().Add(100 * time.Millisecond).UnixNano()
	done := make(chan error, 1)
	go func() {
		_, receipt, err := m.WakeAdmitted(t.Context(), req, nil)
		if receipt.Binding.Token != "" {
			done <- errors.New("expired boot received a receipt")
			return
		}
		done <- err
	}()
	select {
	case <-vmm.entered:
	case <-time.After(time.Second):
		t.Fatal("boot did not start")
	}
	<-time.After(time.Until(time.Unix(0, req.Binding.ExpiresAtUnixNano)))
	close(vmm.release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expired boot succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("expired flight stuck")
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("expired flight leaked native residency")
	}
}

func TestNativeRuntimeAdmissionFailedBootCannotReplay(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{bootErr: errors.New("injected boot failure")})
	req := admittedFixture(t, m)
	if inst, receipt, err := m.WakeAdmitted(t.Context(), req, nil); err == nil || inst != nil || receipt.Binding.Token != "" {
		t.Fatal("failed boot received authority")
	}
	if _, _, err := m.WakeAdmitted(t.Context(), req, nil); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatalf("failed boot replay err=%v", err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("failed boot leaked residency")
	}
}

func TestNativeRuntimeAdmissionReplayWindowIsBoundedAndPruned(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	req := admittedFixture(t, m)
	m.runtimeAdmissionTokens = map[string]time.Time{}
	for i := 0; i < api.ApplicationStandardRuntimeAdmissionReplayLimit; i++ {
		m.runtimeAdmissionTokens[fmt.Sprint(i)] = time.Now().Add(time.Minute)
	}
	if _, _, err := m.WakeAdmitted(t.Context(), req, nil); !errors.Is(err, runtimeadmission.ErrCapacity) {
		t.Fatalf("unbounded replay window err=%v", err)
	}
	for token := range m.runtimeAdmissionTokens {
		m.runtimeAdmissionTokens[token] = time.Now().Add(-time.Second)
	}
	if _, _, err := m.WakeAdmitted(t.Context(), req, nil); err != nil {
		t.Fatalf("expired replay entries retained: %v", err)
	}
	if len(m.runtimeAdmissionTokens) != 1 || len(m.runtimeAdmissionInstances) != 1 {
		t.Fatal("replay window did not prune expired entries")
	}
	if err := m.Destroy(t.Context(), req.Binding.InstanceID); err != nil {
		t.Fatal(err)
	}
}
