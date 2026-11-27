package dbsched

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbquerycontrol"
)

func TestQueryControlDispatchIsOneShotAndFencedByPrimary(t *testing.T) {
	ctx := t.Context()
	store := storeFactories(t)["pg"](t).(*PgStore)
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive})
	s := New(store, newFakeVMM(), Config{}, nil)
	parked := provision(t, s)
	active, err := s.Wake(ctx, parked.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	owner := uuid.NewString()
	target := dbquerycontrol.Target{ID: uuid.NewString(), DatabaseID: active.DatabaseID, NodeID: active.NodeID,
		PrimaryEpochMicros: active.StateChangedAt.UnixMicro(), BackendPID: 73, ApplicationName: "orders"}
	if commands, err := s.ExchangeQueryControls(ctx, owner, []dbquerycontrol.Target{target}, nil); err != nil || len(commands) != 0 {
		t.Fatalf("publish: %+v, %v", commands, err)
	}
	targets, err := store.queryTargets(ctx, target.DatabaseID)
	if err != nil || len(targets) != 1 || targets[0] != target {
		t.Fatalf("read published targets: %+v, %v", targets, err)
	}
	sample := &dbguest.SessionSample{SampledAt: time.Now(), Sessions: []dbguest.SessionRow{{PID: 73, State: "active"}}}
	attachQueryTargets(sample, active, targets)
	if sample.Sessions[0].TargetID != target.ID || sample.Sessions[0].ApplicationName != "orders" {
		t.Fatalf("attach target: %+v", sample)
	}
	if err := store.requestQueryCancel(ctx, target.DatabaseID, target.ID); err != nil {
		t.Fatal(err)
	}
	if commands, err := s.ExchangeQueryControls(ctx, uuid.NewString(), nil, nil); err != nil || len(commands) != 0 {
		t.Fatalf("another owner received a command: %+v, %v", commands, err)
	}
	commands, err := s.ExchangeQueryControls(ctx, owner, []dbquerycontrol.Target{target}, nil)
	if err != nil || len(commands) != 1 || commands[0].Target != target || !commands[0].ExpiresAt.After(time.Now()) {
		t.Fatalf("dispatch: %+v, %v", commands, err)
	}
	commands, err = s.ExchangeQueryControls(ctx, owner, nil, []QueryControlResult{{TargetID: target.ID, Outcome: dbquerycontrol.SignalSent}})
	if err != nil || len(commands) != 0 {
		t.Fatalf("ack and repeat: %+v, %v", commands, err)
	}
	if outcome, err := s.CancelQuery(ctx, target.DatabaseID, target.ID); err != nil || outcome != dbquerycontrol.SignalSent {
		t.Fatalf("receipt: %s, %v", outcome, err)
	}

	target.ID = uuid.NewString()
	if _, err := s.ExchangeQueryControls(ctx, owner, []dbquerycontrol.Target{target}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.requestQueryCancel(ctx, target.DatabaseID, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "UPDATE database_vms SET state_changed_at=state_changed_at+interval '1 second' WHERE database_id=$1",
		mustUUID(target.DatabaseID)); err != nil {
		t.Fatal(err)
	}
	if commands, err := s.ExchangeQueryControls(ctx, owner, []dbquerycontrol.Target{target}, nil); err != nil || len(commands) != 0 {
		t.Fatalf("stale generation was dispatched: %+v, %v", commands, err)
	}
	if outcome, err := s.CancelQuery(ctx, target.DatabaseID, target.ID); err != nil || outcome != dbquerycontrol.Stale {
		t.Fatalf("stale receipt: %s, %v", outcome, err)
	}
}
