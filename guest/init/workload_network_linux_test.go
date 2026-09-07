//go:build linux

package main

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildWorkloadEndpointEnv(t *testing.T) {
	roster := workloadRoster{
		Main: workloadSpec{Name: "main", Port: 8080},
		Sidecars: []workloadSpec{
			{Name: "metrics-agent", Port: 9090},
			{Name: "migrate", Type: "init"},
		},
	}

	got, err := buildWorkloadEndpointEnv(roster, api.AppManifest{Port: 8080})
	if err != nil {
		t.Fatalf("buildWorkloadEndpointEnv: %v", err)
	}
	want := map[string]string{
		"FAAS_WORKLOAD_MAIN_HOST":              "127.0.0.1",
		"FAAS_WORKLOAD_MAIN_PORT":              "8080",
		"FAAS_WORKLOAD_MAIN_ADDR":              "127.0.0.1:8080",
		"FAAS_WORKLOAD_MAIN_PROTOCOL":          "tcp",
		"FAAS_WORKLOAD_METRICS_AGENT_HOST":     "127.0.0.1",
		"FAAS_WORKLOAD_METRICS_AGENT_PORT":     "9090",
		"FAAS_WORKLOAD_METRICS_AGENT_ADDR":     "127.0.0.1:9090",
		"FAAS_WORKLOAD_METRICS_AGENT_PROTOCOL": "tcp",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("endpoint env = %#v, want %#v", got, want)
	}
}

func TestBuildWorkloadEndpointEnvExposesNamedTCPAndUDPPorts(t *testing.T) {
	got, err := buildWorkloadEndpointEnv(workloadRoster{
		Main: workloadSpec{Ports: []api.WorkloadPort{
			{Name: "http", Port: 8080, Protocol: api.WorkloadPortTCP},
			{Name: "dns", Port: 53, Protocol: api.WorkloadPortUDP},
		}},
	}, api.AppManifest{})
	if err != nil {
		t.Fatalf("buildWorkloadEndpointEnv: %v", err)
	}
	checks := map[string]string{
		"FAAS_WORKLOAD_MAIN_PORT":          "8080",
		"FAAS_WORKLOAD_MAIN_PROTOCOL":      "tcp",
		"FAAS_WORKLOAD_MAIN_HTTP_ADDR":     "127.0.0.1:8080",
		"FAAS_WORKLOAD_MAIN_HTTP_PROTOCOL": "tcp",
		"FAAS_WORKLOAD_MAIN_DNS_ADDR":      "127.0.0.1:53",
		"FAAS_WORKLOAD_MAIN_DNS_PROTOCOL":  "udp",
	}
	for key, want := range checks {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
}

func TestBuildWorkloadEndpointEnvAllowsTCPAndUDPSamePort(t *testing.T) {
	_, err := buildWorkloadEndpointEnv(workloadRoster{
		Main: workloadSpec{Ports: []api.WorkloadPort{
			{Name: "http", Port: 8080, Protocol: api.WorkloadPortTCP},
			{Name: "udp", Port: 8080, Protocol: api.WorkloadPortUDP},
		}},
	}, api.AppManifest{})
	if err != nil {
		t.Fatalf("same numeric TCP/UDP port should be allowed: %v", err)
	}
}

func TestBuildWorkloadEndpointEnvKeepsEffectivePortAsLegacyBase(t *testing.T) {
	got, err := buildWorkloadEndpointEnv(workloadRoster{}, api.AppManifest{
		Ports: []api.WorkloadPort{
			{Name: "dns", Port: 53, Protocol: api.WorkloadPortUDP},
			{Name: "http", Port: 8080, Protocol: api.WorkloadPortTCP},
		},
	})
	if err != nil {
		t.Fatalf("buildWorkloadEndpointEnv: %v", err)
	}
	if got["FAAS_WORKLOAD_MAIN_PORT"] != "8080" || got["FAAS_WORKLOAD_MAIN_PROTOCOL"] != "tcp" {
		t.Fatalf("legacy main endpoint = %v, want tcp/8080", got)
	}
	if got["FAAS_WORKLOAD_MAIN_DNS_PROTOCOL"] != "udp" {
		t.Fatalf("named UDP endpoint missing: %v", got)
	}
}

func TestBuildWorkloadEndpointEnvUsesManifestPort(t *testing.T) {
	got, err := buildWorkloadEndpointEnv(workloadRoster{}, api.AppManifest{Port: 3000})
	if err != nil {
		t.Fatalf("buildWorkloadEndpointEnv: %v", err)
	}
	if got["FAAS_WORKLOAD_MAIN_ADDR"] != "127.0.0.1:3000" {
		t.Fatalf("main endpoint = %q, want 127.0.0.1:3000", got["FAAS_WORKLOAD_MAIN_ADDR"])
	}
}

func TestBuildWorkloadEndpointEnvRejectsPortCollision(t *testing.T) {
	_, err := buildWorkloadEndpointEnv(workloadRoster{
		Main:     workloadSpec{Name: "main", Port: 8080},
		Sidecars: []workloadSpec{{Name: "metrics", Port: 8080}},
	}, api.AppManifest{})
	if err == nil {
		t.Fatal("expected duplicate port error")
	}
}

func TestBuildWorkloadEndpointEnvRejectsInvalidPort(t *testing.T) {
	_, err := buildWorkloadEndpointEnv(workloadRoster{
		Main: workloadSpec{Name: "main", Port: 65536},
	}, api.AppManifest{})
	if err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestStampWorkloadEndpointEnvSortsAndAppends(t *testing.T) {
	base := []string{"PATH=/usr/bin", "FAAS_WORKLOAD_MAIN_PORT=1"}
	got := stampWorkloadEndpointEnv(base, map[string]string{
		"FAAS_WORKLOAD_MAIN_PORT": "8080",
		"FAAS_WORKLOAD_MAIN_HOST": "127.0.0.1",
	})
	want := []string{
		"PATH=/usr/bin",
		"FAAS_WORKLOAD_MAIN_PORT=1",
		"FAAS_WORKLOAD_MAIN_HOST=127.0.0.1",
		"FAAS_WORKLOAD_MAIN_PORT=8080",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stamped env = %#v, want %#v", got, want)
	}
}
