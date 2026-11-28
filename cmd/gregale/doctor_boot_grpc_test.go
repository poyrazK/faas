package main_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// serveGRPC starts a plaintext gRPC server that the fake Docker publishes as
// the container port. With withHealth, it registers the standard health
// service and hands it to setup.
func serveGRPC(t *testing.T, docker fakeBootDocker, withHealth bool, setup func(*health.Server)) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	if withHealth {
		hs := health.NewServer()
		healthpb.RegisterHealthServer(srv, hs)
		if setup != nil {
			setup(hs)
		}
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(srv.Stop)
	docker.write(t, "addr", l.Addr().String())
}

func TestDoctorBootGRPCHealth(t *testing.T) {
	for _, tc := range []struct {
		name       string
		withHealth bool
		setup      func(*health.Server)
		args       []string
		check      string
		status     string
		code       string
		detail     string
	}{
		{name: "whole server serving", withHealth: true, check: "boot-grpc-health", status: "ok", detail: "the whole server → SERVING"},
		{name: "named service serving", withHealth: true, args: []string{"--healthcheck-grpc-service", "shop.Orders"},
			setup: func(hs *health.Server) { hs.SetServingStatus("shop.Orders", healthpb.HealthCheckResponse_SERVING) },
			check: "boot-grpc-health", status: "ok", detail: `service "shop.Orders" → SERVING`},
		{name: "serving after a while", withHealth: true,
			setup: func(hs *health.Server) {
				hs.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
				time.AfterFunc(50*time.Millisecond, func() { hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING) })
			},
			check: "boot-grpc-health", status: "ok"},
		{name: "never serving", withHealth: true,
			setup: func(hs *health.Server) { hs.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING) },
			check: "boot-grpc-health", status: "error", code: "boot_grpc_health_failed"},
		{name: "unknown service", withHealth: true, args: []string{"--healthcheck-grpc-service", "shop.Orders"},
			check: "boot-grpc-health", status: "error", code: "boot_grpc_service_unknown"},
		{name: "no health service", check: "boot-grpc-health", status: "error", code: "boot_grpc_health_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker := newFakeBootDocker(t)
			serveGRPC(t, docker, tc.withHealth, tc.setup)
			_, report, _ := runDoctorBoot(t, bootInspector("3000/tcp"), append([]string{"--healthcheck-grpc", "--json"}, tc.args...)...)
			c := bootCheck(t, report, tc.check)
			if c.Status != tc.status || c.Code != tc.code || !strings.Contains(c.Detail, tc.detail) {
				t.Fatalf("%s = %+v", tc.check, c)
			}
			for _, c := range report.Checks {
				if c.Name == "boot-http" {
					t.Errorf("a gRPC app got an HTTP check: %+v", c)
				}
			}
			if !docker.removed(t) {
				t.Error("container not removed")
			}
		})
	}
}

func TestDoctorBootGRPCNothingListening(t *testing.T) {
	docker := newFakeBootDocker(t)
	docker.write(t, "addr", closedAddr(t))
	_, report, _ := runDoctorBoot(t, bootInspector("3000/tcp"), "--healthcheck-grpc", "--json")
	if c := bootCheck(t, report, "boot-listener"); c.Status != "error" || !strings.Contains(c.Hint, "Nothing answered plaintext gRPC") {
		t.Fatalf("boot-listener = %+v", c)
	}
}

func TestDoctorBootGRPCFlagValidation(t *testing.T) {
	newFakeBootDocker(t)
	for _, args := range [][]string{
		{"--healthcheck-grpc-service", "shop.Orders"},
		{"--healthcheck-grpc", "--healthcheck-path", "/healthz"},
		{"--healthcheck-grpc", "--execution-mode", "worker"},
	} {
		if code, _, _ := runDoctorBoot(t, bootInspector("3000/tcp"), args...); code == 0 {
			t.Errorf("%q accepted", args)
		}
	}
}

// An app that binds late in the window is still seen: reconnects keep the
// poll's pace instead of gRPC's growing backoff (1 s, 2.6 s, ...).
func TestDoctorBootGRPCLateListener(t *testing.T) {
	docker := newFakeBootDocker(t)
	main.SetDoctorBootTimings(t.Cleanup, 2500*time.Millisecond, 50*time.Millisecond, 20*time.Millisecond)
	addr := closedAddr(t)
	docker.write(t, "addr", addr)
	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, health.NewServer())
	t.Cleanup(srv.Stop)
	time.AfterFunc(1200*time.Millisecond, func() {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		_ = srv.Serve(l)
	})
	_, report, _ := runDoctorBoot(t, bootInspector("3000/tcp"), "--healthcheck-grpc", "--json")
	if c := bootCheck(t, report, "boot-grpc-health"); c.Status != "ok" {
		t.Fatalf("boot-grpc-health = %+v", c)
	}
}

func TestDoctorBootGRPCExitedFixNamesGRPC(t *testing.T) {
	docker := newFakeBootDocker(t)
	docker.write(t, "state", "false 1 false")
	_, report, _ := runDoctorBoot(t, bootInspector("3000/tcp"), "--healthcheck-grpc", "--json")
	if c := bootCheck(t, report, "boot-start"); c.Status != "error" || !strings.Contains(c.Fix, "serve plaintext gRPC") {
		t.Fatalf("boot-start = %+v", c)
	}
}
