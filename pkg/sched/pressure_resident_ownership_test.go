// adr: 432
package sched

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Ownership may move through the cheap path only when every instance is
// parked/stopped/failed. Cover every state, placement and retained policy
// value so a resident VM on a peer cannot escape the owner guard.
func TestPressureRebalancePreservesResidentOwnership(t *testing.T) {
	for _, policy := range []string{"skip_live", "migrate_after_1", "migrate_after_2"} {
		for _, current := range state.States {
			for _, onPeer := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/peer=%t", policy, current, onPeer), func(t *testing.T) {
					store, ctx, owner, peer := pressureTestOwners(t)
					app := seedPressureApp(t, store, ctx, api.PlanHobby, 128, owner.ID)
					nodeID := owner.ID
					if onPeer {
						nodeID = peer.ID
					}
					if _, err := store.CreateInstanceWithMode(ctx, app.ID, "", string(current), 128, nodeID, "", string(state.InstanceModeNormal)); err != nil {
						t.Fatal(err)
					}
					notif := &fakeNotifier{}
					e := newPressureEngine(t, store, owner.ID, notif, policy)
					if err := e.RebalancePressuredApps(ctx, app.ID); err != nil {
						t.Fatal(err)
					}
					got, err := store.AppByID(ctx, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					movable := current == state.StateParked || current == state.StateStopped || current == state.StateFailed
					wantOwner, wantNotifies := owner.ID, 0
					if movable {
						wantOwner, wantNotifies = peer.ID, 1
					}
					if got.NodeID != wantOwner || countPressureRebalancedNotifies(notif) != wantNotifies {
						t.Fatalf("state=%s: owner=%s notifications=%d; want owner=%s notifications=%d", current, got.NodeID, countPressureRebalancedNotifies(notif), wantOwner, wantNotifies)
					}
				})
			}
		}
	}
}

type pressureInstancesUnavailableStore struct{ state.Store }

func (s pressureInstancesUnavailableStore) ListInstancesForApp(context.Context, string) ([]state.Instance, error) {
	return nil, errors.New("instance inventory unavailable")
}

func TestPressureRebalanceInventoryErrorPreservesOwner(t *testing.T) {
	store, ctx, owner, _ := pressureTestOwners(t)
	app := seedPressureApp(t, store, ctx, api.PlanHobby, 128, owner.ID)
	notif := &fakeNotifier{}
	e := newPressureEngine(t, store, owner.ID, notif, "skip_live")
	e.store = pressureInstancesUnavailableStore{Store: store}
	if err := e.RebalancePressuredApps(ctx, app.ID); err == nil {
		t.Fatal("missing inventory must return an error")
	}
	got, err := store.AppByID(ctx, app.ID)
	if err != nil || got.NodeID != owner.ID || countPressureRebalancedNotifies(notif) != 0 {
		t.Fatalf("inventory error transferred ownership: app=%+v err=%v", got, err)
	}
}

func TestPressureRebalanceSerializesWithAdmission(t *testing.T) {
	store, ctx, owner, _ := pressureTestOwners(t)
	app := seedPressureApp(t, store, ctx, api.PlanHobby, 128, owner.ID)
	e := newPressureEngine(t, store, owner.ID, &fakeNotifier{}, "skip_live")
	release := e.lockApp(app.ID)
	defer func() {
		if release != nil {
			release()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- e.RebalancePressuredApps(ctx, app.ID) }()
	select {
	case err := <-done:
		t.Fatalf("rebalance escaped the admission lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := store.CreateInstanceWithMode(ctx, app.ID, "", string(state.StateWaking), 128, owner.ID, "", string(state.InstanceModeNormal)); err != nil {
		t.Fatal(err)
	}
	release()
	release = nil
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rebalance did not finish after admission unlocked")
	}
	got, err := store.AppByID(ctx, app.ID)
	if err != nil || got.NodeID != owner.ID {
		t.Fatalf("rebalance lost the newly admitted instance: app=%+v err=%v", got, err)
	}
}
