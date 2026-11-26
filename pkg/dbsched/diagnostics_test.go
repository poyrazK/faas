package dbsched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

type diagnosticsVMM struct {
	VMM
	probes int
	legacy bool
}

func (v *diagnosticsVMM) Control(ctx context.Context, node Node, id string, req dbguest.Request) (dbguest.Response, error) {
	if req.Op != dbguest.OpDiagnostics {
		return v.VMM.Control(ctx, node, id, req)
	}
	v.probes++
	if req.TableHealthOptions != nil && !v.legacy {
		return dbguest.Response{State: dbguest.StateReady, TableHealth: &dbguest.TableHealthSample{
			SampledAt: time.Now(), Tables: []dbguest.TableHealthRow{{Schema: "public", Name: "orders"}},
		}}, nil
	}
	return dbguest.Response{State: dbguest.StateReady, Activity: &dbguest.ActivitySample{SampledAt: time.Now(), ConnectionLimit: 100}}, nil
}

func TestDiagnosticsPreservesIdleDeadlineAndKeepsParkedDatabaseAsleep(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := t.Context()
	parked, running := provision(t, s), provision(t, s)
	current, err := s.Wake(ctx, running.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	s.vmm = &diagnosticsVMM{VMM: vmm}
	probe := s.vmm.(*diagnosticsVMM)
	result, err := s.Diagnose(ctx, parked.DatabaseID, "gregale")
	stillParked, readErr := store.Get(ctx, parked.DatabaseID)
	if err != nil || readErr != nil || result.State != dbguest.DiagnosticsParked || probe.probes != 0 || stillParked.State != StateParked {
		t.Fatalf("parked probe returned %+v, %v, state %s, read error %v", result, err, stillParked.State, readErr)
	}
	lastActive := current.LastActiveAt.Add(-time.Minute)
	if err := store.Touch(ctx, running.DatabaseID, lastActive); err != nil {
		t.Fatal(err)
	}
	result, err = s.Diagnose(ctx, running.DatabaseID, "gregale")
	after, readErr := store.Get(ctx, running.DatabaseID)
	if err != nil || readErr != nil || result.State != dbguest.DiagnosticsAvailable || result.Sample == nil ||
		probe.probes != 1 || !after.LastActiveAt.Equal(lastActive) {
		t.Fatalf("running probe returned %+v, %v; activity time %v, read error %v", result, err, after.LastActiveAt, readErr)
	}
}

func TestTableHealthUsesOnlyRunningPrimary(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := t.Context()
	parked, running := provision(t, s), provision(t, s)
	current, err := s.Wake(ctx, running.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	probe := &diagnosticsVMM{VMM: vmm}
	s.vmm = probe
	options := dbguest.TableHealthOptions{Sort: "dead-rows", Limit: 1}
	result, err := s.TableHealth(ctx, parked.DatabaseID, "gregale", options)
	if err != nil || result.State != dbguest.DiagnosticsParked || result.Sample != nil || probe.probes != 0 {
		t.Fatalf("parked probe returned %+v, %v after %d probes", result, err, probe.probes)
	}

	lastActive := current.LastActiveAt.Add(-time.Minute)
	if err := store.Touch(ctx, running.DatabaseID, lastActive); err != nil {
		t.Fatal(err)
	}
	result, err = s.TableHealth(ctx, running.DatabaseID, "gregale", options)
	after, readErr := store.Get(ctx, running.DatabaseID)
	if err != nil || readErr != nil || result.State != dbguest.DiagnosticsAvailable || result.Sample == nil ||
		len(result.Sample.Tables) != 1 || result.Sample.Tables[0].Name != "orders" ||
		probe.probes != 1 || !after.LastActiveAt.Equal(lastActive) {
		t.Fatalf("running probe returned %+v, %v; activity time %v, read error %v", result, err, after.LastActiveAt, readErr)
	}

	probe.legacy = true
	result, err = s.TableHealth(ctx, running.DatabaseID, "gregale", options)
	if err != nil || result.State != dbguest.DiagnosticsUnavailable || result.Sample != nil {
		t.Fatalf("legacy activity probe returned %+v, %v", result, err)
	}
}
