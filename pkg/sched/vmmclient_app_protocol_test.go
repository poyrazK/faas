package sched

import "testing"

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
