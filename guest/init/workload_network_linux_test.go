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
		"FAAS_WORKLOAD_MAIN_HOST":          "127.0.0.1",
		"FAAS_WORKLOAD_MAIN_PORT":          "8080",
		"FAAS_WORKLOAD_MAIN_ADDR":          "127.0.0.1:8080",
		"FAAS_WORKLOAD_METRICS_AGENT_HOST": "127.0.0.1",
		"FAAS_WORKLOAD_METRICS_AGENT_PORT": "9090",
		"FAAS_WORKLOAD_METRICS_AGENT_ADDR": "127.0.0.1:9090",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("endpoint env = %#v, want %#v", got, want)
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
