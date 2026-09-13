package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func TestDaemonConnectionBudgetFitsMultiNodeFleet(t *testing.T) {
	// Control plane: apid, schedd, public gateway/control services. Each
	// compute node: schedd, internal gateway, vmmd, imaged, and builderd.
	control := []string{"apid", "schedd", "gatewayd-public", "meterd", "githubd", "outboundd", "s3-gatewayd"}
	compute := []string{"schedd", "gatewayd-internal", "vmmd", "imaged", "builderd"}
	sum := func(names []string) int32 {
		var total int32
		for _, name := range names {
			total += daemonMaxConnections(name)
		}
		return total
	}
	reservedConnections := postgresCapacityDefault(t, "faas_postgres_superuser_reserved_connections")
	operatorHeadroom := postgresCapacityDefault(t, "faas_postgres_min_operator_headroom")
	declaredControl := postgresCapacityDefault(t, "faas_postgres_control_plane_pool_budget")
	declaredCompute := postgresCapacityDefault(t, "faas_postgres_per_compute_pool_budget")
	standaloneRolloutNodes := postgresCapacityDefault(t, "faas_postgres_rollout_overlap_nodes")
	if got := sum(control); got != declaredControl {
		t.Fatalf("control-plane pool budget = %d, role declares %d", got, declaredControl)
	}
	if got := sum(compute); got != declaredCompute {
		t.Fatalf("per-compute pool budget = %d, role declares %d", got, declaredCompute)
	}
	tests := []struct {
		computeNodes int32
		rolloutNodes int32
		wantMax      int32
	}{
		{computeNodes: 1, rolloutNodes: standaloneRolloutNodes, wantMax: 130},
		{computeNodes: 2, rolloutNodes: standaloneRolloutNodes, wantMax: 160},
		{computeNodes: 10, rolloutNodes: standaloneRolloutNodes, wantMax: 520},
		{computeNodes: 12, rolloutNodes: standaloneRolloutNodes, wantMax: 610},
		// join-fleet converges four nodes by default. These cases cover the
		// boundary where the steady-state 75% margin alone is insufficient.
		{computeNodes: 10, rolloutNodes: 4, wantMax: 540},
		{computeNodes: 11, rolloutNodes: 4, wantMax: 570},
	}
	for _, tt := range tests {
		computeNodes := tt.computeNodes
		steadyTotal := declaredControl + computeNodes*declaredCompute
		rolloutTotal := declaredControl + (computeNodes+tt.rolloutNodes)*declaredCompute
		ordinaryForWarning := (steadyTotal*4 + 2) / 3
		ordinaryForRollout := rolloutTotal + operatorHeadroom
		ordinaryCapacity := max32(ordinaryForWarning, ordinaryForRollout)
		maxConnections := roundUp10(ordinaryCapacity + reservedConnections)
		if maxConnections != tt.wantMax {
			t.Fatalf("%d-node/%d-overlap max_connections = %d, want %d", computeNodes, tt.rolloutNodes, maxConnections, tt.wantMax)
		}
		configuredOrdinary := maxConnections - reservedConnections
		if steadyTotal*4 > configuredOrdinary*3 {
			t.Fatalf("%d-node steady pool budget = %d, want <=75%% of %d ordinary slots", computeNodes, steadyTotal, configuredOrdinary)
		}
		if rolloutTotal+operatorHeadroom > configuredOrdinary {
			t.Fatalf("%d-node rollout pool budget = %d plus %d operator slots, want <=%d", computeNodes, rolloutTotal, operatorHeadroom, configuredOrdinary)
		}
	}
	if got := daemonMaxConnections("faas-schedd"); got != 16 {
		t.Fatalf("schedd pool = %d, want 16 for eleven subscribers plus request headroom", got)
	}
	if got := daemonMaxConnections("faas-gatewayd-internal"); got != 8 {
		t.Fatalf("gatewayd-internal pool = %d, want 8 for six subscribers plus request headroom", got)
	}
	if got := daemonMaxConnections("migration-tool"); got != defaultMaxConnections {
		t.Fatalf("unknown process pool = %d, want default %d", got, defaultMaxConnections)
	}
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func roundUp10(value int32) int32 {
	return ((value + 9) / 10) * 10
}

func postgresCapacityDefault(t *testing.T, key string) int32 {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "postgres_capacity", "defaults", "main.yml"))
	if err != nil {
		t.Fatalf("read postgres_capacity defaults: %v", err)
	}
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*([0-9]+)\s*$`)
	match := re.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("postgres_capacity default %q is missing or non-numeric", key)
	}
	value, err := strconv.ParseInt(string(match[1]), 10, 32)
	if err != nil {
		t.Fatalf("parse postgres_capacity default %q: %v", key, err)
	}
	return int32(value)
}
