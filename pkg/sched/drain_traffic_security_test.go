// adr: 570
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

type drainSecurityStore struct {
	mu     sync.Mutex
	states map[trafficrevocation.Scope]trafficrevocation.State
	err    error
}

func (s *drainSecurityStore) Read(_ context.Context, scopes []trafficrevocation.Scope) (map[trafficrevocation.Scope]trafficrevocation.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make(map[trafficrevocation.Scope]trafficrevocation.State, len(scopes))
	for _, scope := range scopes {
		states[scope] = s.states[scope]
	}
	return states, s.err
}

func (s *drainSecurityStore) change(scope trafficrevocation.Scope, after trafficrevocation.State, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[scope], s.err = after, err
}

func newSecurityDrain(t *testing.T) (*Drain, state.Store, *fakeVMM, *drainSecurityStore, *trafficrevocation.Registry, state.Invocation, string) {
	t.Helper()
	d, store, vmm, _, _ := newDrainHarness(t, api.PlanPro, true)
	inv := seedDrainInvocation(t, store, state.InvocationAsyncInvoke)
	app, err := store.AppByID(t.Context(), inv.AppID)
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.LiveDeployment(t.Context(), inv.AppID)
	if err != nil {
		t.Fatal(err)
	}
	security := &drainSecurityStore{states: make(map[trafficrevocation.Scope]trafficrevocation.State)}
	registry := trafficrevocation.New(security)
	t.Cleanup(registry.Close)
	WithDrainTrafficRevocations(registry)(d)
	return d, store, vmm, security, registry, inv, dep.ID
}

func assertSecurityDrainRefused(t *testing.T, store state.Store, inv state.Invocation, registry *trafficrevocation.Registry) {
	t.Helper()
	got, err := store.InvocationByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.InvocationPending || got.Attempts != 1 || !strings.Contains(got.LastError, "traffic security ") || len(got.Result) != 0 || !got.DueAt.After(inv.DueAt) {
		t.Fatalf("revoked delivery lost retry outcome: %#v", got)
	}
	if requests, scopes := registry.Tracked(); requests != 0 || scopes != 0 {
		t.Fatalf("delivery cleanup leaked security ownership: %d/%d", requests, scopes)
	}
}

func TestDrainSecurityRefusesBeforeWarmOrColdWake(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, kind := range []string{"account", "app", "deployment", "outage"} {
			t.Run(kind+map[bool]string{false: "/cold", true: "/warm"}[warm], func(t *testing.T) {
				d, store, vmm, security, registry, inv, dep := newSecurityDrain(t)
				inv.Headers, _ = json.Marshal(map[string]string{api.RevisionHeader: dep})
				inv.ID = ""
				var err error
				inv, err = store.EnqueueInvocation(t.Context(), inv)
				if err != nil {
					t.Fatal(err)
				}
				if warm {
					instances, err := store.ListInstancesForApp(t.Context(), inv.AppID)
					if err != nil {
						t.Fatal(err)
					}
					if err := store.UpdateInstanceState(t.Context(), instances[0].ID, string(state.StateRunning)); err != nil {
						t.Fatal(err)
					}
				}
				// A cached allow must never substitute for a fresh security read.
				d.isAccountActive(t.Context(), inv.AppID)
				scope := trafficrevocation.Scope{Kind: kind, ID: inv.AccountID}
				if kind == "app" {
					scope.ID = inv.AppID
				}
				if kind == "deployment" {
					scope.ID = dep
				}
				if kind == "outage" {
					security.change(scope, trafficrevocation.State{}, errors.New("offline"))
				} else {
					security.change(scope, trafficrevocation.State{Revision: 1, Revoked: true}, nil)
				}
				vmm.mu.Lock()
				before := vmm.coldBoots + vmm.restores
				vmm.mu.Unlock()
				d.dispatchOne(t.Context(), inv)
				if d.gateway.(*drainSynth).calls.Load() != 0 {
					t.Fatal("refused delivery reached gateway")
				}
				vmm.mu.Lock()
				after := vmm.coldBoots + vmm.restores
				vmm.mu.Unlock()
				if after != before {
					t.Fatal("refused delivery woke a VM")
				}
				assertSecurityDrainRefused(t, store, inv, registry)
			})
		}
	}
}

func TestDrainSecurityCancelsWaiterWhileSharedWakeRemainsOwned(t *testing.T) {
	for _, kind := range []string{"account", "app", "missed release", "outage"} {
		t.Run(kind, func(t *testing.T) {
			d, store, vmm, security, registry, inv, _ := newSecurityDrain(t)
			vmm.bootStarted, vmm.bootRelease = make(chan struct{}, 1), make(chan struct{})
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(vmm.bootRelease) }) })
			finished := make(chan struct{})
			go func() { defer close(finished); d.dispatchOne(t.Context(), inv) }()
			select {
			case <-vmm.bootStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("wake did not start")
			}
			// Dispatch resolves a blank legacy scope to default. Join that
			// actual leader so its independent lifetime is observed.
			call, leader, err := d.engine.wakeCoord.EnterScoped(inv.AppID, state.DefaultEnvScope, WakeFanout{})
			if err != nil || leader {
				t.Fatalf("missing shared wake owner: %v leader=%v", err, leader)
			}
			defer d.engine.wakeCoord.Release(inv.AppID, call)
			scope := trafficrevocation.Scope{Kind: "account", ID: inv.AccountID}
			if kind == "app" {
				scope = trafficrevocation.Scope{Kind: "app", ID: inv.AppID}
			}
			if kind == "outage" {
				security.change(scope, trafficrevocation.State{}, errors.New("offline"))
			} else {
				security.change(scope, trafficrevocation.State{Revision: 1, Revoked: true}, nil)
				if kind == "missed release" {
					security.change(scope, trafficrevocation.State{Revision: 2}, nil)
				}
			}
			_ = registry.Refresh(t.Context())
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("revoked waiter kept waiting for shared boot")
			}
			assertSecurityDrainRefused(t, store, inv, registry)
			if d.gateway.(*drainSynth).calls.Load() != 0 {
				t.Fatal("revoked waiter forwarded")
			}
			select {
			case <-call.done:
				t.Fatal("delivery cancellation ended the shared wake owner")
			default:
			}
			release.Do(func() { close(vmm.bootRelease) })
			waitCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			out := call.Await(waitCtx)
			if out.Err != nil || out.Instance == nil {
				t.Fatalf("shared wake did not finish independently: %#v", out)
			}
		})
	}
}

type securityDrainSynth struct {
	invoke func(context.Context, state.Invocation) (state.Invocation, error)
}

func (*securityDrainSynth) SynthesizeRequest(context.Context, string, string, string) error {
	return nil
}
func (s *securityDrainSynth) Invoke(ctx context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	return s.invoke(ctx, inv)
}

func TestDrainSecurityRetainsOwnershipThroughGatewayCleanup(t *testing.T) {
	for _, kind := range []string{"account", "app", "deployment", "missed release", "outage"} {
		t.Run(kind, func(t *testing.T) {
			d, store, _, security, registry, inv, dep := newSecurityDrain(t)
			started, canceled, cleanup, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(cleanup) }) })
			d.gateway = &securityDrainSynth{invoke: func(ctx context.Context, row state.Invocation) (state.Invocation, error) {
				baseline, ok := trafficrevocation.HandoffSnapshot(ctx)
				if !ok || len(baseline) != 3 {
					return row, errors.New("missing exact security handoff")
				}
				close(started)
				<-ctx.Done()
				close(canceled)
				<-cleanup
				row.Result = json.RawMessage(`{"late":true}`)
				return row, nil
			}}
			go func() { defer close(finished); d.dispatchOne(t.Context(), inv) }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("forwarding did not start")
			}
			scope := trafficrevocation.Scope{Kind: "account", ID: inv.AccountID}
			if kind == "app" {
				scope = trafficrevocation.Scope{Kind: "app", ID: inv.AppID}
			}
			if kind == "deployment" {
				scope = trafficrevocation.Scope{Kind: "deployment", ID: dep}
			}
			if kind == "outage" {
				security.change(scope, trafficrevocation.State{}, errors.New("offline"))
			} else {
				security.change(scope, trafficrevocation.State{Revision: 2}, nil)
			}
			_ = registry.Refresh(t.Context())
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("forwarding was not canceled")
			}
			if requests, scopes := registry.Tracked(); requests == 0 || scopes != 3 {
				t.Fatalf("cleanup lost owner early: %d/%d", requests, scopes)
			}
			row, err := store.InvocationByID(t.Context(), inv.ID)
			if err != nil || row.State != state.InvocationDispatching {
				t.Fatalf("cleanup published outcome early: %#v %v", row, err)
			}
			release.Do(func() { close(cleanup) })
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("cleanup did not finish")
			}
			assertSecurityDrainRefused(t, store, inv, registry)
		})
	}
}

func TestDrainSecurityFreshReadRejectsLateSuccessWithoutNotification(t *testing.T) {
	for _, kind := range []string{"missed release", "outage", "valid"} {
		t.Run(kind, func(t *testing.T) {
			d, store, _, security, registry, inv, _ := newSecurityDrain(t)
			d.gateway = &securityDrainSynth{invoke: func(ctx context.Context, row state.Invocation) (state.Invocation, error) {
				baseline, ok := trafficrevocation.HandoffSnapshot(ctx)
				if !ok || len(baseline) != 3 {
					return row, errors.New("missing handoff")
				}
				if kind == "missed release" {
					security.change(trafficrevocation.Scope{Kind: "account", ID: inv.AccountID}, trafficrevocation.State{Revision: 2}, nil)
				}
				if kind == "outage" {
					security.change(trafficrevocation.Scope{}, trafficrevocation.State{}, errors.New("offline"))
				}
				row.Result = json.RawMessage(`{"ok":true}`)
				return row, nil
			}}
			d.dispatchOne(t.Context(), inv)
			if kind != "valid" {
				assertSecurityDrainRefused(t, store, inv, registry)
				return
			}
			row, err := store.InvocationByID(t.Context(), inv.ID)
			if err != nil || row.State != state.InvocationCompleted || string(row.Result) != `{"ok":true}` {
				t.Fatalf("valid delivery: %#v %v", row, err)
			}
			if requests, scopes := registry.Tracked(); requests != 0 || scopes != 0 {
				t.Fatalf("completed owner leaked: %d/%d", requests, scopes)
			}
		})
	}
}

func TestDrainSecurityHandoffSnapshotIsDefensive(t *testing.T) {
	d, _, _, _, _, inv, _ := newSecurityDrain(t)
	_, _, _, traffic, err := d.prepareInvocationTraffic(t.Context(), inv)
	if err != nil {
		t.Fatal(err)
	}
	defer traffic.close()
	ctx, err := traffic.handoff(traffic.ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := trafficrevocation.HandoffSnapshot(ctx)
	after, _ := trafficrevocation.HandoffSnapshot(ctx)
	for scope := range before {
		before[scope] = trafficrevocation.State{Revision: 10}
	}
	current, _ := trafficrevocation.HandoffSnapshot(ctx)
	if !maps.Equal(current, after) {
		t.Fatal("caller mutation rebased the immutable handoff")
	}
}

func TestDrainSecurityRechecksAfterWakeBeforeForwarding(t *testing.T) {
	for _, kind := range []string{"account", "app", "deployment", "outage"} {
		t.Run(kind, func(t *testing.T) {
			d, store, vmm, security, registry, inv, dep := newSecurityDrain(t)
			vmm.coldBootHook = func() {
				scope := trafficrevocation.Scope{Kind: "account", ID: inv.AccountID}
				if kind == "app" {
					scope = trafficrevocation.Scope{Kind: "app", ID: inv.AppID}
				}
				if kind == "deployment" {
					scope = trafficrevocation.Scope{Kind: "deployment", ID: dep}
				}
				if kind == "outage" {
					security.change(scope, trafficrevocation.State{}, errors.New("offline"))
				} else {
					after := trafficrevocation.State{Revision: 2}
					if kind == "deployment" {
						after.Revoked = true
					}
					security.change(scope, after, nil)
				}
			}
			d.dispatchOne(t.Context(), inv)
			if d.gateway.(*drainSynth).calls.Load() != 0 {
				t.Fatal("wake result forwarded without an exact generation recheck")
			}
			assertSecurityDrainRefused(t, store, inv, registry)
		})
	}
}

func TestDrainSecurityRequiresConfiguredGateway(t *testing.T) {
	d, store, _, _, registry, inv, _ := newSecurityDrain(t)
	d.gateway = nil
	d.dispatchOne(t.Context(), inv)
	assertSecurityDrainRefused(t, store, inv, registry)
}

func TestDrainSecurityCancelsPinnedWakeAndPersistsClaimRefusal(t *testing.T) {
	for _, kind := range []string{"account", "app", "deployment", "outage"} {
		t.Run(kind, func(t *testing.T) {
			d, store, vmm, security, registry, inv, dep := newSecurityDrain(t)
			inv.ID = ""
			inv.Headers, _ = json.Marshal(map[string]string{api.RevisionHeader: dep})
			var err error
			inv, err = store.EnqueueInvocation(t.Context(), inv)
			if err != nil {
				t.Fatal(err)
			}
			vmm.bootStarted, vmm.bootRelease = make(chan struct{}, 1), make(chan struct{})
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(vmm.bootRelease) }) })
			finished := make(chan struct{})
			go func() { defer close(finished); d.dispatchOne(t.Context(), inv) }()
			select {
			case <-vmm.bootStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("pinned wake did not start")
			}
			scope := trafficrevocation.Scope{Kind: "account", ID: inv.AccountID}
			if kind == "app" {
				scope = trafficrevocation.Scope{Kind: "app", ID: inv.AppID}
			}
			if kind == "deployment" {
				scope = trafficrevocation.Scope{Kind: "deployment", ID: dep}
			}
			if kind == "outage" {
				security.change(scope, trafficrevocation.State{}, errors.New("offline"))
			} else {
				security.change(scope, trafficrevocation.State{Revision: 1, Revoked: true}, nil)
			}
			_ = registry.Refresh(t.Context())
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("pinned delivery did not release its canceled wake")
			}
			if d.gateway.(*drainSynth).calls.Load() != 0 {
				t.Fatal("revoked pinned wake forwarded")
			}
			assertSecurityDrainRefused(t, store, inv, registry)
		})
	}
}
