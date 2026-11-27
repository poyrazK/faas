package dbsched

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

type autovacuumVMM struct {
	*fakeVMM
	configured map[string]dbguest.AutovacuumProfile
	probes     int
}

func (f *autovacuumVMM) Control(ctx context.Context, node Node, id string, req dbguest.Request) (dbguest.Response, error) {
	if req.Op != dbguest.OpAutovacuum {
		return f.fakeVMM.Control(ctx, node, id, req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes++
	state := dbguest.StateReady
	if f.standbyOn[id] == node.ID {
		state = dbguest.StateStandby
	} else if !f.running[id] || f.primaryOn[id] != node.ID {
		return dbguest.Response{Error: dbguest.ErrCodeNotReady}, nil
	}
	key := node.ID + "/" + id
	if req.AutovacuumProfile != "" {
		f.configured[key] = req.AutovacuumProfile
	}
	profile := f.configured[key]
	if profile == "" {
		profile = dbguest.AutovacuumDefault
	}
	settings, _ := profile.Settings()
	return dbguest.Response{State: state, Autovacuum: &settings}, nil
}

func TestAutovacuumParkedWakeAndFailover(t *testing.T) {
	ctx := t.Context()
	store := storeFactories(t)["pg"](t).(*PgStore)
	primary := Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 30 << 30, Lifecycle: NodeActive}
	standby := Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive}
	seedNodes(t, store, primary, standby)
	vmm := &autovacuumVMM{fakeVMM: newFakeVMM(), configured: make(map[string]dbguest.AutovacuumProfile)}
	s := New(store, vmm, Config{StandbySyncTimeout: time.Second}, nil)
	t.Cleanup(s.standbyWG.Wait)
	id := uuid.NewString()
	if _, err := s.Provision(ctx, ProvisionRequest{
		DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, Replicated: true, AlwaysOn: true,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := s.Autovacuum(ctx, id, dbguest.AutovacuumHighChurn)
	if err != nil || report.Profile != dbguest.AutovacuumHighChurn || report.State != dbguest.DiagnosticsParked || report.Effective != nil || vmm.probes != 0 {
		t.Fatalf("set while parked: %+v, %v, probes %d", report, err, vmm.probes)
	}
	if err := s.reconcileAutovacuumBatch(ctx, store); err != nil || vmm.probes != 0 {
		t.Fatalf("worker contacted parked database: %v, probes %d", err, vmm.probes)
	}
	if _, err := s.Wake(ctx, id); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	report, err = s.Autovacuum(ctx, id, "")
	want, _ := dbguest.AutovacuumHighChurn.Settings()
	if err != nil || report.State != dbguest.AutovacuumApplied || report.Effective == nil || *report.Effective != want ||
		report.ReplicaEffective == nil || *report.ReplicaEffective != want {
		t.Fatalf("after wake: %+v, %v", report, err)
	}
	vm, err := store.Get(ctx, id)
	if err != nil || vm.NodeID != primary.ID || vm.StandbyNode != standby.ID || !vm.StandbySynced {
		t.Fatalf("before failover: %+v, %v", vm, err)
	}
	if err := s.failoverLocked(ctx, id, primary, "test"); err != nil {
		t.Fatal(err)
	}
	report, err = s.Autovacuum(ctx, id, "")
	if err != nil || report.State != dbguest.AutovacuumApplied || report.Effective == nil || *report.Effective != want {
		t.Fatalf("after failover: %+v, %v", report, err)
	}
	report, err = s.Autovacuum(ctx, id, dbguest.AutovacuumDefault)
	defaults, _ := dbguest.AutovacuumDefault.Settings()
	if err != nil || report.State != dbguest.AutovacuumApplied || report.Effective == nil || *report.Effective != defaults {
		t.Fatalf("reset on running primary: %+v, %v", report, err)
	}
	policy, err := store.autovacuumPolicy(ctx, id)
	if err != nil || policy.profile != dbguest.AutovacuumDefault || policy.revision != 2 {
		t.Fatalf("saved policy: %+v, %v", policy, err)
	}
}
