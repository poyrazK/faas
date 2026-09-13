package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func TestDaemonConnectionBudgetFitsTwoNodePublicBetaFleet(t *testing.T) {
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
	maxConnections := postgresCapacityDefault(t, "faas_postgres_max_connections")
	reservedConnections := postgresCapacityDefault(t, "faas_postgres_superuser_reserved_connections")
	ordinaryCapacity := maxConnections - reservedConnections
	operatorHeadroom := postgresCapacityDefault(t, "faas_postgres_min_operator_headroom")
	steadyTotal := sum(control) + 2*sum(compute)
	rolloutTotal := sum(control) + 3*sum(compute)
	if steadyTotal*4 > ordinaryCapacity*3 {
		t.Fatalf("two-compute steady pool budget = %d, want <=75%% of %d ordinary slots", steadyTotal, ordinaryCapacity)
	}
	if rolloutTotal+operatorHeadroom > ordinaryCapacity {
		t.Fatalf("rollout pool budget = %d plus %d operator slots, want <=%d", rolloutTotal, operatorHeadroom, ordinaryCapacity)
	}
	declaredSteady := postgresCapacityDefault(t, "faas_postgres_two_compute_pool_budget")
	declaredRollout := postgresCapacityDefault(t, "faas_postgres_rollout_overlap_pool_budget")
	if steadyTotal != declaredSteady || rolloutTotal != declaredRollout {
		t.Fatalf("capacity role contract drifted: computed steady/rollout=%d/%d, declared=%d/%d", steadyTotal, rolloutTotal, declaredSteady, declaredRollout)
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
