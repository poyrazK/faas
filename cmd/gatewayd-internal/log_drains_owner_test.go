package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type logDrainOwnerTestStore struct {
	logDrainHealthTestStore
	rows     []state.AppLogDrain
	nodes    map[string]string // name -> id
	appNodes map[string]string // app id -> node id
	appErr   error
}

func (s *logDrainOwnerTestStore) ListEnabledAppLogDrains(context.Context) ([]state.AppLogDrain, error) {
	return s.rows, nil
}

func (s *logDrainOwnerTestStore) ComputeNodeByName(_ context.Context, name string) (state.ComputeNode, error) {
	id, ok := s.nodes[name]
	if !ok {
		return state.ComputeNode{}, state.ErrNotFound
	}
	return state.ComputeNode{ID: id}, nil
}

func (s *logDrainOwnerTestStore) AppByID(_ context.Context, id string) (state.App, error) {
	if s.appErr != nil {
		return state.App{}, s.appErr
	}
	return state.App{ID: id, NodeID: s.appNodes[id]}, nil
}

// Production-us hunt #9: with two compute nodes every drained log line
// reached the customer twice, because each gatewayd-internal reconciled every
// enabled drain. Only the gateway on the app's owner node may deliver it.
func TestAppLogDrainOwnershipDeliversFromOwnerNodeOnly(t *testing.T) {
	store := &logDrainOwnerTestStore{
		nodes:    map[string]string{"fsn-2.faas": "node-2", "fsn-3.faas": "node-3"},
		appNodes: map[string]string{"app-a": "node-2", "app-b": "node-3"},
	}
	ctx := context.Background()
	cases := []struct {
		node, app string
		want      bool
	}{
		{"fsn-2.faas", "app-a", true},
		{"fsn-3.faas", "app-a", false},
		{"fsn-2.faas", "app-b", false},
		{"fsn-3.faas", "app-b", true},
	}
	owned := map[string]int{}
	for _, tc := range cases {
		got, err := newAppLogDrainOwnership(store, tc.node)(ctx, state.AppLogDrain{ID: "d-" + tc.app, AppID: tc.app})
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.node, tc.app, err)
		}
		if got != tc.want {
			t.Errorf("%s owns %s = %v, want %v", tc.node, tc.app, got, tc.want)
		}
		if got {
			owned[tc.app]++
		}
	}
	for app, n := range owned {
		if n != 1 {
			t.Errorf("%s has %d delivering gateways, want exactly 1", app, n)
		}
	}
}

func TestAppLogDrainReconcileSkipsForeignDrainsAndKeepsRunningOnLookupError(t *testing.T) {
	store := &logDrainOwnerTestStore{
		logDrainHealthTestStore: logDrainHealthTestStore{drains: map[string]state.AppLogDrain{}},
		rows:                    []state.AppLogDrain{{ID: "drain-foreign", AppID: "app-foreign"}},
	}
	manager := newAppLogDrainManager(store, nil, nil, nil, nil)
	manager.owns = func(context.Context, state.AppLogDrain) (bool, error) { return false, nil }
	manager.reconcile(context.Background())
	if len(manager.workers) != 0 {
		t.Fatalf("workers = %d, want 0: a foreign drain must not start", len(manager.workers))
	}

	// A running worker survives an ownership lookup failure rather than
	// flapping, and a lookup failure never starts a new deliverer.
	cancelled := false
	manager.workers["drain-foreign"] = &appLogDrainWorker{spec: store.rows[0], cancel: func() { cancelled = true }}
	manager.owns = func(context.Context, state.AppLogDrain) (bool, error) { return false, errors.New("pg down") }
	manager.reconcile(context.Background())
	if cancelled || len(manager.workers) != 1 {
		t.Fatalf("lookup error stopped the running worker (cancelled=%v workers=%d)", cancelled, len(manager.workers))
	}
}

// The log-drains kill switch (ADR-20261023115741): apid hides the drain
// routes, and gatewayd-internal stops delivering an off account's drains,
// running ones included, while other accounts keep theirs.
func TestAppLogDrainReconcileStopsDrainsOfAccountsWithLogDrainsOff(t *testing.T) {
	on := state.AppLogDrain{ID: "drain-on", AppID: "app-on", AccountID: "acct-on"}
	offRunning := state.AppLogDrain{ID: "drain-off-running", AppID: "app-off", AccountID: "acct-off"}
	offNew := state.AppLogDrain{ID: "drain-off-new", AppID: "app-off", AccountID: "acct-off"}
	store := &logDrainOwnerTestStore{
		logDrainHealthTestStore: logDrainHealthTestStore{drains: map[string]state.AppLogDrain{}},
		rows:                    []state.AppLogDrain{on, offRunning, offNew},
	}
	manager := newAppLogDrainManager(store, nil, nil, nil, nil)
	var asked []string
	manager.capabilityAllowed = func(capability, accountID string) bool {
		asked = append(asked, capability)
		return accountID != "acct-off"
	}
	onCancelled, offCancelled := false, false
	manager.workers[on.ID] = &appLogDrainWorker{spec: on, cancel: func() { onCancelled = true }}
	manager.workers[offRunning.ID] = &appLogDrainWorker{spec: offRunning, cancel: func() { offCancelled = true }}

	manager.reconcile(context.Background())

	if onCancelled || manager.workers[on.ID] == nil {
		t.Fatalf("the released account's drain stopped (cancelled=%v)", onCancelled)
	}
	if !offCancelled || manager.workers[offRunning.ID] != nil {
		t.Fatalf("the off account's running drain kept delivering (cancelled=%v)", offCancelled)
	}
	if manager.workers[offNew.ID] != nil {
		t.Fatal("a drain of an account with log-drains off started")
	}
	for _, capability := range asked {
		if capability != capabilityLogDrains {
			t.Fatalf("reconcile decided %q, want %q", capability, capabilityLogDrains)
		}
	}
}
