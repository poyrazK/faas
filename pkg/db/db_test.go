package db

import "testing"

func TestDaemonConnectionBudgetFitsThreeNodeFleet(t *testing.T) {
	// Control plane: apid, schedd, both gateways/control services. Each of two
	// compute nodes: schedd, internal gateway, vmmd, imaged, and builderd.
	control := []string{"apid", "schedd", "gatewayd-public", "meterd", "githubd", "outboundd", "s3-gatewayd"}
	compute := []string{"schedd", "gatewayd-internal", "vmmd", "imaged", "builderd"}
	sum := func(names []string) int32 {
		var total int32
		for _, name := range names {
			total += daemonMaxConnections(name)
		}
		return total
	}
	total := sum(control) + 2*sum(compute)
	if total > 90 {
		t.Fatalf("three-node daemon pool budget = %d, want <=90 for ordinary/admin headroom", total)
	}
	if got := daemonMaxConnections("faas-schedd"); got != 12 {
		t.Fatalf("prefixed application name profile = %d, want 12", got)
	}
	if got := daemonMaxConnections("migration-tool"); got != defaultMaxConnections {
		t.Fatalf("unknown process pool = %d, want default %d", got, defaultMaxConnections)
	}
}
