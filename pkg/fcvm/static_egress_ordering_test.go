package fcvm

// adr: 20261103235901

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func TestStaticEgressOlderWakeCannotRemoveNewerPin(t *testing.T) {
	previous := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(previous) })
	const account, app = "acct-audit4-race", "app-audit4-race"
	pin := netip.MustParseAddr("203.0.113.93")
	checked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var calls atomic.Int32
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithStaticEgressPolicySource(func(ctx context.Context, _, _ string) (*netip.Addr, error) {
		if calls.Add(1) == 1 {
			// A read begun before the pin commit has a nil snapshot, but its
			// response is delayed until after a newer wake has completed.
			close(checked)
			select {
			case <-release:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &pin, nil
	})
	m.SetHostRenderer(&fakeHostRenderer{})
	t.Cleanup(func() {
		unblock()
		_ = m.Destroy(context.Background(), "audit4-old")
		_ = m.Destroy(context.Background(), "audit4-new")
		_ = ReleaseStaticEgressIP(account, app)
	})
	req := WakeRequest{Instance: "audit4-old", AccountID: account, AppID: app, BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanScale, EgressMbit: 10, Snapshot: usableSnapshot()}
	older := make(chan error, 1)
	go func() { _, err := m.Wake(t.Context(), req); older <- err }()
	select {
	case <-checked:
	case <-time.After(time.Second):
		t.Fatal("older read did not start")
	}
	next := req
	next.Instance = "audit4-new"
	next.StaticEgressIP = pin.String()
	if _, err := m.Wake(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if len(netns.ActiveHostPolicyForRender().StaticEgressRules) != 1 {
		t.Fatal("newer wake did not install its static SNAT")
	}
	unblock()
	select {
	case err := <-older:
		if err != nil && !strings.Contains(err.Error(), "policy changed") {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("older wake stuck")
	}
	m.perAppStaticIPMu.RLock()
	cached := m.perAppStaticIP[app]
	m.perAppStaticIPMu.RUnlock()
	_, reserved := StaticEgressReservationFor(app)
	if cached == nil || !reserved || len(netns.ActiveHostPolicyForRender().StaticEgressRules) == 0 {
		t.Fatalf("older unpinned wake completed after pinned wake and removed current pin: cached=%v reservation=%v SNAT rules=%d", cached, reserved, len(netns.ActiveHostPolicyForRender().StaticEgressRules))
	}
}
