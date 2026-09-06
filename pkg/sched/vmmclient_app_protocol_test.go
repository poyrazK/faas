package sched

import "testing"

func TestAppSpecToProtoCarriesAppProtocol(t *testing.T) {
	got := (AppSpec{AppProtocol: "grpc"}).toProto().GetAppProtocol()
	if got != "grpc" {
		t.Fatalf("app_protocol = %q, want grpc", got)
	}
}
