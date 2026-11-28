package main

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type grantRecorder struct {
	mu      sync.Mutex
	last    []StaticEgressIPEntry
	repairs int
}

func (r *grantRecorder) SetStaticEgressIPAliases(entries []StaticEgressIPEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = append([]StaticEgressIPEntry(nil), entries...)
}

func (r *grantRecorder) ReconcileStaticEgressPolicies(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.repairs++
	return nil
}

func (r *grantRecorder) snapshot() ([]StaticEgressIPEntry, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]StaticEgressIPEntry(nil), r.last...), r.repairs
}

func TestStaticEgressGrantSetKeepsEachSourcesPart(t *testing.T) {
	rec := &grantRecorder{}
	set := newStaticEgressGrantSet(rec)
	bundleIP, inventoryIP := netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("203.0.113.11")
	set.SetStaticEgressIPAliases([]StaticEgressIPEntry{{AccountID: "a", AppID: "app", IP: bundleIP}})
	set.setInventory([]state.StaticEgressIPGrant{{AccountID: "b", IP: inventoryIP}})
	// The legacy no-bundle loop clears only the bundle part.
	set.SetStaticEgressIPAliases(nil)
	got, _ := rec.snapshot()
	if len(got) != 1 || got[0].IP != inventoryIP || got[0].AccountID != "b" {
		t.Fatalf("bundle clear stripped inventory grants: %+v", got)
	}
	set.SetStaticEgressIPAliases([]StaticEgressIPEntry{{AccountID: "a", AppID: "app", IP: bundleIP}})
	set.setInventory(nil)
	if got, _ := rec.snapshot(); len(got) != 1 || got[0].IP != bundleIP {
		t.Fatalf("inventory clear stripped bundle grants: %+v", got)
	}
}

type flakyInventory struct {
	mu     sync.Mutex
	grants []state.StaticEgressIPGrant
	err    error
	reads  int
}

func (f *flakyInventory) AssignedStaticEgressAddressesForNode(_ context.Context, nodeID string) ([]state.StaticEgressIPGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if nodeID != "node-1" {
		return nil, errors.New("wrong node")
	}
	return append([]state.StaticEgressIPGrant(nil), f.grants...), f.err
}

func (f *flakyInventory) set(grants []state.StaticEgressIPGrant, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants, f.err = grants, err
}

func TestWatchStaticEgressInventoryAppliesChangesAndKeepsLastGoodOnError(t *testing.T) {
	rec := &grantRecorder{}
	set := newStaticEgressGrantSet(rec)
	ip := netip.MustParseAddr("203.0.113.20")
	source := &flakyInventory{grants: []state.StaticEgressIPGrant{{AccountID: "acct", IP: ip}}}
	wake := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go watchStaticEgressInventory(ctx, set, source, "node-1", wake, silentStaticIPLogger())

	waitFor(t, func() bool { got, repairs := rec.snapshot(); return len(got) == 1 && got[0].IP == ip && repairs == 1 })
	source.set(nil, errors.New("db down"))
	wake <- struct{}{}
	waitFor(t, func() bool { source.mu.Lock(); defer source.mu.Unlock(); return source.reads >= 2 })
	if got, _ := rec.snapshot(); len(got) != 1 || got[0].IP != ip {
		t.Fatalf("a failed read stripped the live grant: %+v", got)
	}
	source.set(nil, nil)
	wake <- struct{}{}
	waitFor(t, func() bool { got, repairs := rec.snapshot(); return len(got) == 0 && repairs == 2 })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type intentStore struct {
	app         state.App
	plan        api.Plan
	provisioned bool
}

func (s intentStore) AppByID(context.Context, string) (state.App, error) { return s.app, nil }
func (s intentStore) AccountByID(context.Context, string) (state.Account, error) {
	return state.Account{ID: s.app.AccountID, Plan: s.plan}, nil
}
func (s intentStore) ProvisionedStaticEgressIPExists(context.Context, string, netip.Addr) (bool, error) {
	return s.provisioned, nil
}

func TestStaticEgressIntentFollowsPlanAndProvisioning(t *testing.T) {
	ip := netip.MustParseAddr("203.0.113.30")
	app := state.App{ID: "app", AccountID: "acct", StaticEgressIP: &ip}
	if got, err := staticEgressIntent(t.Context(), intentStore{app: app, plan: api.PlanScale, provisioned: true}, "acct", "app"); err != nil || got == nil || *got != ip {
		t.Fatalf("scale pin = %v, %v", got, err)
	}
	if got, err := staticEgressIntent(t.Context(), intentStore{app: app, plan: api.PlanPro, provisioned: true}, "acct", "app"); err != nil || got != nil {
		t.Fatalf("downgraded pin = %v, %v; want inactive", got, err)
	}
	if got, err := staticEgressIntent(t.Context(), intentStore{app: app, plan: api.PlanScale}, "acct", "app"); !errors.Is(err, fcvm.ErrStaticEgressUnprovisioned) || got == nil {
		t.Fatalf("unprovisioned pin = %v, %v", got, err)
	}
	if _, err := staticEgressIntent(t.Context(), intentStore{app: app, plan: api.PlanScale}, "other", "app"); err == nil {
		t.Fatal("account mismatch accepted")
	}
}
