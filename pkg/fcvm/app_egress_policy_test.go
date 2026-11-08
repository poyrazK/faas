// adr: 435 — order live egress projection and wake publication.
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

type policyNetworkRunner struct {
	fakeRunner
	inputMu       sync.Mutex
	inputs        []string
	failInput     bool
	failPortsOnly bool
	entered       chan struct{}
	release       chan struct{}
	blockOnce     sync.Once
}

func (r *policyNetworkRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	r.inputMu.Lock()
	r.inputs = append(r.inputs, string(input))
	fail := r.failInput && (!r.failPortsOnly || strings.Contains(string(input), "flush set"))
	r.inputMu.Unlock()
	if r.entered != nil {
		var blocked bool
		r.blockOnce.Do(func() { close(r.entered); blocked = true })
		if blocked {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-r.release:
			}
		}
	}
	if fail {
		return errors.New("injected nft port failure")
	}
	return nil
}

func (r *policyNetworkRunner) inputCount() int {
	r.inputMu.Lock()
	defer r.inputMu.Unlock()
	return len(r.inputs)
}

func (r *policyNetworkRunner) RunCapture(_ context.Context, _ []string) ([]byte, error) {
	return []byte("iifname \"tap0\" ip daddr { 8.8.8.0/24 } tcp dport != 25 accept # handle 100\n"), nil
}

func seedPolicyInstance(m *Manager, id, app string, plan api.Plan) {
	nc := netns.NewConfig(id, "fc-"+id, "vh1", "vp1", netip.MustParseAddr("10.100.0.2"))
	nc.EgressPorts = api.TenantEgressBasePorts()
	m.live[id] = &Instance{AppID: app, Plan: plan, Lease: Lease{Instance: id, Plan: plan}, Net: nc}
}

func TestAppEgressPolicyRejectsStaleConflictingAndLegacyWrites(t *testing.T) {
	run := &policyNetworkRunner{}
	m := newTestManager(run, &fakeVMM{})
	seedPolicyInstance(m, "live", "app", api.PlanPro)
	prefixes := []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 2, prefixes, []uint16{5432}); err != nil {
		t.Fatal(err)
	}
	inputs := run.inputCount()
	run.mu.Lock()
	commands := len(run.commands)
	run.mu.Unlock()
	for _, tc := range []struct {
		name   string
		update func() error
	}{
		{"stale", func() error { return m.UpdateAppEgressPolicy(t.Context(), "app", 1, nil, nil) }},
		{"conflicting ports", func() error { return m.UpdateAppEgressPolicy(t.Context(), "app", 2, prefixes, nil) }},
		{"conflicting CIDRs", func() error { return m.UpdateAppEgressPolicy(t.Context(), "app", 2, nil, []uint16{5432}) }},
		{"legacy CIDRs", func() error { return m.UpdateEgressAllowlist(t.Context(), "app", nil) }},
		{"legacy ports", func() error { return m.UpdateEgressPorts(t.Context(), "app", nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.update(); err == nil {
				t.Fatal("unexpected success")
			}
		})
	}
	if run.inputCount() != inputs {
		t.Fatal("rejected update changed nft ports")
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.commands) != commands {
		t.Fatal("rejected update issued network commands")
	}
}

func TestAppEgressPolicyCanonicalRetryAndDefensiveCopies(t *testing.T) {
	run := &policyNetworkRunner{}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(run)
	seedPolicyInstance(m, "live", "app", api.PlanPro)
	prefixes := []netip.Prefix{netip.MustParsePrefix("8.8.8.1/24"), netip.MustParsePrefix("1.1.1.0/24"), netip.MustParsePrefix("8.8.8.0/24")}
	ports := []uint16{6379, 80, 5432, 6379}
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 3, prefixes, ports); err != nil {
		t.Fatal(err)
	}
	prefixes[0] = netip.MustParsePrefix("9.9.9.0/24")
	ports[0] = 8883
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 3, []netip.Prefix{netip.MustParsePrefix("1.1.1.0/24"), netip.MustParsePrefix("8.8.8.0/24")}, []uint16{5432, 6379}); err != nil {
		t.Fatalf("canonical retry: %v", err)
	}
	if run.inputCount() != 2 {
		t.Fatalf("canonical retry issued %d physical policy writes", run.inputCount())
	}
}

func TestAppEgressPolicyPartialFailureRetainsNewIntentAndRetries(t *testing.T) {
	run := &policyNetworkRunner{failInput: true, failPortsOnly: true}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(run)
	seedPolicyInstance(m, "live", "app", api.PlanPro)
	// Start with an installed allowlist so the CIDR write uses the live
	// handle patch and only the subsequent port transaction fails.
	m.live["live"].Net.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.1.1.0/24")}
	m.live["live"].AllowlistHandleV4 = 7
	prefixes := []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 4, prefixes, []uint16{5432}); err == nil {
		t.Fatal("port failure acknowledged")
	}
	if !slices.Equal(m.live["live"].Net.EgressAllowlist, prefixes) {
		t.Fatal("test did not reach partial application")
	}
	if !slices.Equal(m.live["live"].Net.EgressPorts, api.TenantEgressBasePorts()) {
		t.Fatal("failed ports cached as applied")
	}
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 3, nil, nil); err == nil {
		t.Fatal("older intent replaced partially applied revision")
	}
	run.inputMu.Lock()
	run.failInput = false
	run.inputMu.Unlock()
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 4, prefixes, []uint16{5432}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !slices.Equal(m.live["live"].Net.EgressPorts, []uint16{80, 443, 5432}) {
		t.Fatal("retry did not converge ports")
	}
}

func TestAppEgressPolicyOrdersDelayedPhysicalWrites(t *testing.T) {
	run := &policyNetworkRunner{entered: make(chan struct{}), release: make(chan struct{})}
	m := newTestManager(run, &fakeVMM{})
	seedPolicyInstance(m, "live", "app", api.PlanPro)
	first := make(chan error, 1)
	go func() { first <- m.UpdateAppEgressPolicy(t.Context(), "app", 1, nil, []uint16{5432}) }()
	select {
	case <-run.entered:
	case <-time.After(time.Second):
		t.Fatal("first update did not reach physical write")
	}
	second := make(chan error, 1)
	go func() { second <- m.UpdateAppEgressPolicy(t.Context(), "app", 2, nil, []uint16{6379}) }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.UpdateAppEgressPolicy(ctx, "app", 3, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting cancellation: %v", err)
	}
	// One app's slow native operation does not occupy the gate of another app.
	if err := m.UpdateAppEgressPolicy(t.Context(), "other", 1, nil, nil); err != nil {
		t.Fatal(err)
	}
	if run.inputCount() != 1 {
		t.Fatal("new revision wrote while old revision was still in flight")
	}
	close(run.release)
	for _, done := range []<-chan error{first, second} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("ordered update stuck")
		}
	}
	if !slices.Equal(m.live["live"].Net.EgressPorts, []uint16{80, 443, 6379}) {
		t.Fatal("late physical update left older ports")
	}
}

func TestAppEgressPolicyRefusesSilentPlanTruncation(t *testing.T) {
	run := &policyNetworkRunner{}
	m := newTestManager(run, &fakeVMM{})
	seedPolicyInstance(m, "live", "app", api.PlanHobby)
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 1, nil, []uint16{5432}); err == nil {
		t.Fatal("plan silently truncated projection and acknowledged")
	}
	if run.inputCount() != 0 || m.appEgressPolicies["app"].revision != 0 {
		t.Fatal("rejected plan changed desired or physical state")
	}
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 1, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAppEgressPolicyWakeUsesAcceptedProjection(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 2, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}, []uint16{5432}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "wake") })
	inst, err := m.Wake(t.Context(), WakeRequest{Instance: "wake", AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro,
		EgressAllowlist: []string{"1.1.1.0/24"}, EgressPorts: []uint16{6379}, Snapshot: usableSnapshot()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(inst.Net.EgressAllowlist, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}) || !slices.Equal(inst.Net.EgressPorts, []uint16{80, 443, 5432}) {
		t.Fatalf("published stale wake projection: %+v", inst.Net)
	}
	if !run.ran("add element ip faas egress_ports { 80,443,5432 }") {
		t.Fatal("correct cache did not reach network setup")
	}
}

// Found on n2 (ADR-20261106105015 check): the node cached revision 1 (no
// allowlist) and an app task woke after the app's egress changed to
// revision 2; the cache replaced the request's newer allowlist and ports.
func TestAppEgressPolicyWakePrefersNewerRequestRevision(t *testing.T) {
	cached := []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}
	for _, tc := range []struct {
		name          string
		revision      int64
		wantAllowlist []netip.Prefix
		wantPorts     []uint16
	}{
		{"newer request wins", 3, []netip.Prefix{netip.MustParsePrefix("198.18.0.254/32")}, []uint16{80, 443, 6379}},
		{"same revision keeps cache", 2, cached, []uint16{80, 443, 5432}},
		{"older request keeps cache", 1, cached, []uint16{80, 443, 5432}},
		{"unknown revision keeps cache", 0, cached, []uint16{80, 443, 5432}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &fakeRunner{}
			m := newTestManager(run, &fakeVMM{})
			if err := m.UpdateAppEgressPolicy(t.Context(), "app", 2, cached, []uint16{5432}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Destroy(context.Background(), "wake") })
			inst, err := m.Wake(t.Context(), WakeRequest{Instance: "wake", AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro,
				EgressAllowlist: []string{"198.18.0.254/32"}, EgressPorts: []uint16{6379}, EgressRevision: tc.revision, Snapshot: usableSnapshot()})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(inst.Net.EgressAllowlist, tc.wantAllowlist) || !slices.Equal(inst.Net.EgressPorts, tc.wantPorts) {
				t.Fatalf("wake egress = %v ports %v, want %v ports %v", inst.Net.EgressAllowlist, inst.Net.EgressPorts, tc.wantAllowlist, tc.wantPorts)
			}
			if got := m.appEgressPolicies["app"].revision; got != 2 {
				t.Fatalf("wake changed the cached revision to %d; schedd's push owns it", got)
			}
		})
	}
}

func TestAppEgressPolicyNewerWakeRequestStaysWithinPlan(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 1, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "wake") })
	_, err := m.Wake(t.Context(), WakeRequest{Instance: "wake", AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanHobby,
		EgressPorts: []uint16{5432}, EgressRevision: 2, Snapshot: usableSnapshot()})
	if err == nil {
		t.Fatal("a newer request with ports beyond the plan replaced the plan-checked cache")
	}
}

func TestAppEgressPolicyConcurrentWakeIsIncludedBeforeUpdateAck(t *testing.T) {
	run := &policyNetworkRunner{entered: make(chan struct{}), release: make(chan struct{})}
	m := newTestManager(run, &fakeVMM{})
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "wake") })
	woke := make(chan error, 1)
	go func() {
		_, err := m.Wake(t.Context(), WakeRequest{Instance: "wake", AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro, Snapshot: usableSnapshot()})
		woke <- err
	}()
	select {
	case <-run.entered:
	case <-time.After(time.Second):
		t.Fatal("wake did not reach network setup")
	}
	applied := make(chan error, 1)
	go func() { applied <- m.UpdateAppEgressPolicy(t.Context(), "app", 1, nil, []uint16{5432}) }()
	close(run.release)
	for _, done := range []<-chan error{woke, applied} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("wake/update stuck")
		}
	}
	m.mu.Lock()
	ports := append([]uint16{}, m.live["wake"].Net.EgressPorts...)
	m.mu.Unlock()
	if !slices.Equal(ports, []uint16{80, 443, 5432}) {
		t.Fatalf("update acknowledged without new instance: %v", ports)
	}
	run.inputMu.Lock()
	defer run.inputMu.Unlock()
	var patched bool
	for _, input := range run.inputs {
		patched = patched || strings.Contains(input, "flush set ip faas egress_ports") && strings.Contains(input, "80,443,5432")
	}
	if !patched {
		t.Fatalf("new live netns did not receive patch: %v", run.inputs)
	}
}

func TestAppEgressPolicyOperatorRefreshKeepsLatestTenantIntent(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 1, []netip.Prefix{netip.MustParsePrefix("1.1.1.0/24")}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateAppEgressPolicy(t.Context(), "app", 2, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}, nil); err != nil {
		t.Fatal(err)
	}
	m.SetEgressOperatorBundle([]netip.Prefix{netip.MustParsePrefix("9.9.9.0/24")})
	if got := m.perAppAllowlistSnapshot("app"); !slices.Equal(got, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}) {
		t.Fatalf("operator refresh replaced tenant intent: %v", got)
	}
}

func TestAppEgressPolicyGatePreservesConcurrentWakes(t *testing.T) {
	run := &policyNetworkRunner{entered: make(chan struct{}), release: make(chan struct{})}
	m := newTestManager(run, &fakeVMM{})
	t.Cleanup(func() {
		for _, id := range []string{"first", "second"} {
			_ = m.Destroy(context.Background(), id)
		}
	})
	first := make(chan error, 1)
	wake := func(id string) error {
		_, err := m.Wake(t.Context(), WakeRequest{Instance: id, AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro, Snapshot: usableSnapshot()})
		return err
	}
	go func() { first <- wake("first") }()
	select {
	case <-run.entered:
	case <-time.After(time.Second):
		t.Fatal("first wake did not reach network setup")
	}
	second := make(chan error, 1)
	go func() { second <- wake("second") }()
	// The first wake still holds its shared gate; the second must make progress.
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(run.release)
		<-first
		t.Fatal("policy gate serialized ordinary wakes")
	}
	close(run.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestAppEgressPolicyInvalidTupleHasNoEffects(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	for _, tc := range []struct {
		name     string
		revision int64
		prefixes []netip.Prefix
		ports    []uint16
	}{
		{"revision", 0, nil, nil},
		{"invalid prefix", 1, []netip.Prefix{{}}, nil},
		{"default route", 1, []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, nil},
		{"zero port", 1, nil, []uint16{0}},
		{"forbidden port", 1, nil, []uint16{25}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := m.UpdateAppEgressPolicy(t.Context(), "app", tc.revision, tc.prefixes, tc.ports); err == nil {
				t.Fatal("invalid tuple accepted")
			}
			if len(m.appEgressPolicies) != 0 {
				t.Fatal("invalid tuple changed accepted intent")
			}
		})
	}
}
