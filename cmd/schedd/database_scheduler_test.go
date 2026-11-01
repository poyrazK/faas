// adr: 1027
package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/dbsched"
)

func TestDatabaseNodeConfigValidation(t *testing.T) {
	node, err := DatabaseNodeConfig{ID: "db-1", TargetURL: "tcp://10.0.2.10:7443", AdmissionCeilingMB: 48000, VolumeCapacityBytes: 3 << 40}.node()
	if err != nil || node.Lifecycle != dbsched.NodeActive {
		t.Fatalf("node = %+v, %v", node, err)
	}
	for name, cfg := range map[string]DatabaseNodeConfig{
		"no id":          {TargetURL: "tcp://x:1", AdmissionCeilingMB: 1, VolumeCapacityBytes: 1},
		"no target":      {ID: "db-1", AdmissionCeilingMB: 1, VolumeCapacityBytes: 1},
		"no ceiling":     {ID: "db-1", TargetURL: "tcp://x:1", VolumeCapacityBytes: 1},
		"no capacity":    {ID: "db-1", TargetURL: "tcp://x:1", AdmissionCeilingMB: 1},
		"bad lifecycle":  {ID: "db-1", TargetURL: "tcp://x:1", AdmissionCeilingMB: 1, VolumeCapacityBytes: 1, Lifecycle: "zombie"},
		"bad boot slots": {ID: "db-1", TargetURL: "tcp://x:1", AdmissionCeilingMB: 1, VolumeCapacityBytes: 1, BootSlots: 257},
		"bad major":      {ID: "db-1", TargetURL: "tcp://x:1", AdmissionCeilingMB: 1, VolumeCapacityBytes: 1, PostgresMajors: []int{17, 13}},
	} {
		if _, err := cfg.node(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadConfigParsesDatabaseScheduler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedd.toml")
	body := `[database_scheduler]
idle_timeout = "7m"
wake_queue_wait = "20s"
evict_idle_after = "-1s"
evacuate_draining = true
drill_every = "-1s"
[[database_scheduler.nodes]]
id = "db-1"
target_url = "tcp://10.0.2.10:7443"
admission_ceiling_mb = 48000
volume_capacity_bytes = 3000000000000
boot_slots = 8
postgres_majors = [16, 17]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	ds := cfg.DatabaseScheduler
	if ds == nil || ds.IdleTimeout.Minutes() != 7 || len(ds.Nodes) != 1 || ds.Nodes[0].AdmissionCeilingMB != 48000 || ds.Nodes[0].VolumeCapacityBytes != 3000000000000 ||
		ds.WakeQueueWait.Seconds() != 20 || ds.EvictIdleAfter >= 0 || ds.Nodes[0].BootSlots != 8 || !ds.EvacuateDraining ||
		len(ds.Nodes[0].PostgresMajors) != 2 || ds.DrillEvery >= 0 {
		t.Fatalf("database_scheduler = %+v", ds)
	}
}

func TestEnableDatabaseSchedulerDisabledByDefault(t *testing.T) {
	databases, stop, err := enableDatabaseScheduler(context.Background(), nil, nil, nil, nil, nil, slog.Default())
	if err != nil || databases != nil {
		t.Fatalf("disabled scheduler = %v, %v", databases, err)
	}
	stop()
}

// adr: 1027 — database nodes are not compute_nodes rows; their vmmd leaves
// carry <id>.faas and only configured nodes are accepted.
func TestDatabaseNodeVerifierAcceptsConfiguredNodesOnly(t *testing.T) {
	verifier := databaseNodeVerifier(&DatabaseSchedulerConfig{Nodes: []DatabaseNodeConfig{{ID: "db-1"}, {ID: "db-2.faas"}}})
	for _, cn := range []string{"db-1.faas", "db-2.faas"} {
		if err := verifier.LookupCN(cn); err != nil {
			t.Errorf("LookupCN(%q) = %v", cn, err)
		}
	}
	for _, cn := range []string{"db-3.faas", "fsn-2.faas", "vmmd.faas", "db-1"} {
		if err := verifier.LookupCN(cn); err == nil {
			t.Errorf("LookupCN(%q) accepted", cn)
		}
	}
}
