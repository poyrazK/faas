// Billing review R8-2: vmmd's cumulative internal-egress reading rides the
// capacity stream into schedd's telemetry and out on ListInstanceStats, unset
// when vmmd has none.
package scheddgrpc

import (
	"testing"

	"google.golang.org/protobuf/types/known/wrapperspb"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/sched/instancestats"
)

func TestNodeTelemetryFromProtoCarriesInternalEgress(t *testing.T) {
	got := nodeTelemetryFromProto([]*scheddpb.InstanceTelemetry{
		{InstanceId: "vm-a", InternalTxBytes: wrapperspb.Int64(4096)},
		{InstanceId: "vm-b"},
	})
	if len(got) != 2 || got[0].InternalTxBytes == nil || *got[0].InternalTxBytes != 4096 || got[1].InternalTxBytes != nil {
		t.Fatalf("telemetry = %+v, want vm-a at 4096 internal bytes and vm-b unset", got)
	}
}

func TestInstanceStatsRowProtoCarriesInternalEgress(t *testing.T) {
	row := instanceStatsRowProto(instancestats.InstanceStat{InstanceID: "vm-a", InternalTXBytes: 4096, InternalTXValid: true})
	if v := row.GetInternalTxBytes(); v == nil || v.GetValue() != 4096 {
		t.Fatalf("internal_tx_bytes = %v, want 4096", v)
	}
	if v := instanceStatsRowProto(instancestats.InstanceStat{InstanceID: "vm-b", InternalTXBytes: 4096}).GetInternalTxBytes(); v != nil {
		t.Fatalf("internal_tx_bytes without a reading = %v, want unset", v)
	}
}
