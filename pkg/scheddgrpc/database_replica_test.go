// adr: 1027 amendment 2026-11-11 — read replicas: WakeDatabase names the
// standby node pgproxyd sends read sessions to while the standby serves
// them, and nothing otherwise.
package scheddgrpc

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/dbsched"
)

func TestDatabaseRuntimeNamesTheServingReplica(t *testing.T) {
	serving := dbsched.VM{DatabaseID: testDBID, NodeID: "db-a", NodeTarget: "unix:///db-a", State: dbsched.StateRunning,
		Replicated: true, StandbyNode: "db-b", StandbySynced: true, ReplicaTarget: "unix:///db-b"}
	rt := databaseRuntime(serving)
	if rt.GetReplicaVmmdTarget() != "unix:///db-b" || rt.GetStandbyNodeId() != "db-b" || rt.GetVmmdTarget() != "unix:///db-a" {
		t.Fatalf("serving standby: runtime = %+v", rt)
	}
	notServing := serving
	notServing.ReplicaTarget = ""
	if rt := databaseRuntime(notServing); rt.GetReplicaVmmdTarget() != "" {
		t.Fatalf("standby not serving reads: replica target %q, want none", rt.GetReplicaVmmdTarget())
	}
}
