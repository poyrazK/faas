// adr: 057 — readiness probe configuration is forwarded to the VM runtime.
package sched

import (
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestAppSpecToProtoCarriesAppProtocol(t *testing.T) {
	got := (AppSpec{AppProtocol: "grpc"}).toProto().GetAppProtocol()
	if got != "grpc" {
		t.Fatalf("app_protocol = %q, want grpc", got)
	}
}

func TestAppSpecToProtoCarriesGRPCHealthcheck(t *testing.T) {
	got := (AppSpec{HealthcheckGRPC: true, HealthcheckGRPCService: "catalog.v1.Catalog"}).toProto()
	if !got.GetHealthcheckGrpc() {
		t.Fatal("healthcheck_grpc was not forwarded to vmmd")
	}
	if got.GetHealthcheckGrpcService() != "catalog.v1.Catalog" {
		t.Fatalf("healthcheck_grpc_service = %q, want catalog.v1.Catalog", got.GetHealthcheckGrpcService())
	}
}

// adr: 385 — structured application outcomes cross the VM execution protocol.
func TestAppTaskResponseCarriesStructuredOutcome(t *testing.T) {
	result := appTaskResultFromResponse(&vmmdpb.ExecuteAppTaskResponse{
		TaskId: "task-1", Status: string(apptaskproto.StatusSucceeded), OutcomeCode: "invalid_record",
	})
	if result.Status != apptaskproto.StatusSucceeded || result.OutcomeCode != "invalid_record" {
		t.Fatalf("app task result = %+v", result)
	}
}
