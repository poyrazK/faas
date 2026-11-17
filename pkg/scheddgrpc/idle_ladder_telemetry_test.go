// adr: 20261107122928 — the capacity stream carries vmmd's idle ladder level
// into schedd's telemetry, where measured admission charges level 2 with the
// frozen floor.
package scheddgrpc

import (
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
)

func TestNodeTelemetryFromProtoCarriesIdleLadderLevel(t *testing.T) {
	got := nodeTelemetryFromProto([]*scheddpb.InstanceTelemetry{
		{InstanceId: "vm-a", QuietPaused: true, IdleLadderLevel: 2},
		{InstanceId: "vm-b"},
	})
	if len(got) != 2 || got[0].IdleLadderLevel != 2 || !got[0].QuietPaused || got[1].IdleLadderLevel != 0 {
		t.Fatalf("telemetry = %+v, want vm-a at level 2 and vm-b at none", got)
	}
}
