// adr: 1027 — the database scheduler's metrics: failovers, standby drops
// and moves as they happen, and the catalog (states, standbys, node load)
// as the idle sweep observes it.
package dbsched

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func withMetrics(s *Scheduler) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	s.cfg.Metrics = NewMetrics(reg, "schedd")
	return reg
}

func TestMetricsCountStandbyDropsAndFailovers(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b", "db-c")
	withMetrics(s)
	ctx := context.Background()
	vm := wakeReplicated(t, s)

	vmm.mu.Lock()
	vmm.unsynced[vm.DatabaseID] = true
	vmm.mu.Unlock()
	if dropped, err := s.CheckStandbys(ctx); err != nil || dropped != 1 {
		t.Fatalf("check = %d, %v", dropped, err)
	}
	if got := testutil.ToFloat64(s.cfg.Metrics.standbyDrops.WithLabelValues(dropLagging)); got != 1 {
		t.Fatalf("lagging drops = %v, want 1", got)
	}
	vmm.mu.Lock()
	vmm.unsynced[vm.DatabaseID] = false
	vmm.mu.Unlock()
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()

	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///db-a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeUnavailable})
	if n, err := s.SettleLostNodes(ctx); err != nil || n != 1 {
		t.Fatalf("settle = %d, %v", n, err)
	}
	if got := testutil.ToFloat64(s.cfg.Metrics.failovers.WithLabelValues(failoverLostNode, "ok")); got != 1 {
		t.Fatalf("lost-node failovers = %v, want 1", got)
	}
	// A second sweep finds it already failed over: nothing to count.
	if _, err := s.SettleLostNodes(ctx); err != nil {
		t.Fatal(err)
	}
	if got := testutil.CollectAndCount(s.cfg.Metrics.failovers); got != 1 {
		t.Fatalf("failover series = %d after a repeated sweep, want 1", got)
	}
}

func TestMetricsCountAFailedPromotion(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b", "db-c")
	withMetrics(s)
	ctx := context.Background()
	wakeReplicated(t, s)
	vmm.mu.Lock()
	vmm.controlErr = errors.New("promotion refused")
	vmm.mu.Unlock()
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///db-a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeUnavailable})
	if _, err := s.SettleLostNodes(ctx); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(s.cfg.Metrics.failovers.WithLabelValues(failoverLostNode, "failed")); got != 1 {
		t.Fatalf("failed failovers = %v, want 1", got)
	}
}

func TestMetricsCountMoves(t *testing.T) {
	s, _, vmm := newMoveScheduler(t)
	withMetrics(s)
	ctx := context.Background()
	vm := provisionOn(t, s, "db-a")
	if _, err := s.Move(ctx, vm.DatabaseID, "db-b"); err != nil {
		t.Fatal(err)
	}
	vmm.copyErr = errors.New("import digest mismatch")
	if _, err := s.Move(ctx, vm.DatabaseID, "db-a"); err == nil {
		t.Fatal("a failed move reported success")
	}
	for result, want := range map[string]float64{"ok": 1, "failed": 1} {
		if got := testutil.ToFloat64(s.cfg.Metrics.moves.WithLabelValues(result)); got != want {
			t.Errorf("moves{result=%q} = %v, want %v", result, got, want)
		}
	}
}

func TestObserveReportsTheCatalog(t *testing.T) {
	s, store, _ := newStandbyScheduler(t, "db-a", "db-b")
	reg := withMetrics(s)
	ctx := context.Background()
	// Before the first observation nothing is reported, rather than zeros.
	if n, err := testutil.GatherAndCount(reg, "schedd_database_vms"); err != nil || n != 0 {
		t.Fatalf("catalog series before an observation = %d, %v", n, err)
	}
	replicated := wakeReplicated(t, s)
	plain := provisionOn(t, s, "db-a")
	if err := s.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	billable := float64(billableMB(replicated.MemMiB))
	want := `
# HELP schedd_database_node_memory_mb Memory admitted on a database node: active database VMs and running standbys, with per-VM overhead.
# TYPE schedd_database_node_memory_mb gauge
schedd_database_node_memory_mb{node="db-a"} ` + ftoa(billable) + `
schedd_database_node_memory_mb{node="db-b"} ` + ftoa(billable) + `
# HELP schedd_database_node_volume_bytes Volume committed on a database node: its databases, standbys and incoming moves.
# TYPE schedd_database_node_volume_bytes gauge
schedd_database_node_volume_bytes{node="db-a"} ` + ftoa(float64(replicated.VolumeBytes+plain.VolumeBytes)) + `
schedd_database_node_volume_bytes{node="db-b"} ` + ftoa(float64(replicated.VolumeBytes)) + `
# HELP schedd_database_standbys Replicated (production-class) databases by state (running, parked) and standby status: synced (promotable), unsynced, or none placed.
# TYPE schedd_database_standbys gauge
schedd_database_standbys{state="parked",status="none"} 0
schedd_database_standbys{state="parked",status="synced"} 0
schedd_database_standbys{state="parked",status="unsynced"} 0
schedd_database_standbys{state="running",status="none"} 0
schedd_database_standbys{state="running",status="synced"} 1
schedd_database_standbys{state="running",status="unsynced"} 0
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "schedd_database_node_memory_mb", "schedd_database_node_volume_bytes", "schedd_database_standbys"); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, reg, "schedd_database_vms", "state", "running"); got != 1 {
		t.Fatalf("running databases = %v, want 1", got)
	}
	if got := gaugeValue(t, reg, "schedd_database_vms", "state", "parked"); got != 1 {
		t.Fatalf("parked databases = %v, want 1", got)
	}

	// A node leaving the configuration leaves no stale series; a lost one
	// is reported as such.
	seedNodes(t, store, Node{ID: "db-b", TargetURL: "unix:///db-b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeUnavailable})
	if err := s.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, reg, "schedd_database_node_lifecycle", "lifecycle", "unavailable"); got != 1 {
		t.Fatalf("db-b lifecycle unavailable = %v, want 1", got)
	}
	if n, _ := testutil.GatherAndCount(reg, "schedd_database_node_lifecycle"); n != 2 {
		t.Fatalf("lifecycle series = %d, want one per node", n)
	}
}

// gaugeValue reads the gauge of name whose label key equals value.
func gaugeValue(t *testing.T, reg *prometheus.Registry, name, key, value string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == key && l.GetValue() == value {
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	t.Fatalf("no %s{%s=%q}", name, key, value)
	return 0
}

func ftoa(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Loads counts what placement and admission count: a standby's volume on
// its node, and memory only for active VMs.
func TestStoreLoads(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s,
				Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive},
			)
			vm := newVM(2<<30, 384)
			vm.NodeID, vm.Replicated = "db-a", true
			vm, err := s.Place(ctx, vm)
			if err != nil || vm.StandbyNode != "db-b" {
				t.Fatalf("place = %+v, %v", vm, err)
			}
			if err := s.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
				t.Fatal(err)
			}
			loads, err := s.Loads(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if loads["db-a"] != (NodeLoad{VolumeBytes: 2 << 30}) || loads["db-b"] != (NodeLoad{VolumeBytes: 2 << 30}) {
				t.Fatalf("parked loads = %+v", loads)
			}
			if _, err := s.AdmitWake(ctx, vm.DatabaseID, false); err != nil {
				t.Fatal(err)
			}
			loads, err = s.Loads(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if want := (NodeLoad{VolumeBytes: 2 << 30, ActiveMB: billableMB(384)}); loads["db-a"] != want {
				t.Fatalf("db-a while waking = %+v, want %+v", loads["db-a"], want)
			}
		})
	}
}
