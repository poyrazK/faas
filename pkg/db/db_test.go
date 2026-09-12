package db

import "testing"

func TestDaemonConnectionBudgetFitsPublicBetaFleet(t *testing.T) {
	// Control plane: apid, schedd, both gateways/control services. Each
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
	activeTotal := sum(control) + sum(compute)
	if activeTotal > 90 {
		t.Fatalf("active public-beta daemon pool budget = %d, want <=90 for ordinary/admin headroom", activeTotal)
	}
	const ordinaryConnectionLimit int32 = 97
	threeNodeTotal := sum(control) + 2*sum(compute)
	if threeNodeTotal > ordinaryConnectionLimit {
		t.Fatalf("three-node daemon pool budget = %d, want <=%d ordinary PostgreSQL slots", threeNodeTotal, ordinaryConnectionLimit)
	}
	if got := daemonMaxConnections("faas-schedd"); got != 12 {
		t.Fatalf("prefixed application name profile = %d, want 12", got)
	}
	if got := daemonMaxConnections("faas-gatewayd-internal"); got != 8 {
		t.Fatalf("gatewayd-internal pool = %d, want 8 for six subscribers plus request headroom", got)
	}
	if got := daemonMaxConnections("migration-tool"); got != defaultMaxConnections {
		t.Fatalf("unknown process pool = %d, want default %d", got, defaultMaxConnections)
	}
}
