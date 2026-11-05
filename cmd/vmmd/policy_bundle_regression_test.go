package main

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestStaticBundleRemovalRevokesDatabaseGrant(t *testing.T) {
	st := state.NewMemStore()
	ip := netip.MustParseAddr("203.0.113.91")
	const account = "acct-audit4-bundle"
	if err := writeProvisionedStaticEgressIPs(t.Context(), st, []StaticEgressIPEntry{{AccountID: account, AppID: "app-audit4-bundle", IP: ip}}, silentStaticIPLogger()); err != nil {
		t.Fatal(err)
	}
	if yes, err := st.ProvisionedStaticEgressIPExists(t.Context(), account, ip); err != nil || !yes {
		t.Fatalf("fixture: %v %v", yes, err)
	}
	if err := writeProvisionedStaticEgressIPs(t.Context(), st, nil, silentStaticIPLogger()); err != nil {
		t.Fatal(err)
	}
	if yes, err := st.ProvisionedStaticEgressIPExists(t.Context(), account, ip); err != nil || yes {
		t.Fatalf("last bundle entry removed, but its database provisioning grant survives: exists=%v err=%v", yes, err)
	}
}

func TestDisabledStaticBundleRevokesPriorSourceGrant(t *testing.T) {
	st := state.NewMemStore()
	ip := netip.MustParseAddr("203.0.113.93")
	const account = "acct-disabled-bundle"
	if err := writeProvisionedStaticEgressIPs(t.Context(), st, []StaticEgressIPEntry{{AccountID: account, AppID: "app-disabled-bundle", IP: ip}}, silentStaticIPLogger(), "node-disabled"); err != nil {
		t.Fatal(err)
	}
	watchStaticEgressIPBundleReload(t.Context(), &staticEgressIPRecording{}, st, "", silentStaticIPLogger(), nil, "node-disabled")
	if exists, err := st.ProvisionedStaticEgressIPExists(t.Context(), account, ip); err != nil || exists {
		t.Fatalf("disabled bundle retained its source grant: exists=%v err=%v", exists, err)
	}
}

type policyReloadTarget struct {
	events         chan string
	egress, static atomic.Int32
}

func (r *policyReloadTarget) SetEgressOperatorBundle([]netip.Prefix) {
	r.egress.Add(1)
	r.events <- "egress"
}
func (r *policyReloadTarget) SetStaticEgressIPAliases([]StaticEgressIPEntry) {
	r.static.Add(1)
	r.events <- "static"
}

func TestOneSIGHUPReloadsBothPolicyBundles(t *testing.T) {
	dir := t.TempDir()
	egressPath, staticPath := filepath.Join(dir, "egress.toml"), filepath.Join(dir, "static.toml")
	if err := os.WriteFile(egressPath, []byte("cidrs = [\"8.8.8.8/32\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staticPath, []byte("[[entries]]\naccount_id=\"acct-audit4\"\napp_id=\"app-audit4\"\nip=\"203.0.113.92\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := &policyReloadTarget{events: make(chan string, 8)}
	egressHup, staticHup, stop := policyBundleSignals()
	defer stop()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go watchEgressBundleReload(ctx, target, egressPath, silentStaticIPLogger(), egressHup)
	go watchStaticEgressIPBundleReload(ctx, target, state.NewMemStore(), staticPath, silentStaticIPLogger(), staticHup)
	for range 2 {
		select {
		case <-target.events:
		case <-time.After(time.Second):
			t.Fatal("startup did not finish")
		}
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-target.events:
	case <-time.After(time.Second):
		t.Fatal("signal not received")
	}
	select {
	case <-target.events:
	case <-time.After(200 * time.Millisecond):
	}
	if target.egress.Load() != 2 || target.static.Load() != 2 {
		t.Fatalf("one shared SIGHUP was consumed by one watcher: egress loads=%d static loads=%d; both should be 2", target.egress.Load(), target.static.Load())
	}
}
