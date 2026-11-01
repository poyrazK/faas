package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
)

func computeGatewayRegistry(t *testing.T, gatewayCredential bool, bytesPerSecond int64) *objectstorage.Registry {
	t.Helper()
	backend := objectstorage.BackendConfig{ID: "ovh", Driver: "s3", Region: "us-east-1", S3Region: "us-east-va", Namespace: "project", Endpoint: "https://s3.example.test", PathStyle: true,
		AccessKeyEnv: "ADMIN_KEY", SecretKeyEnv: "ADMIN_SECRET", UsageReportsPath: "/var/spool/faas/s3-gatewayd/ovh-usage.json",
		Usage: objectstorage.UsageConfig{Driver: "ovh", ApplicationKeyEnv: "OVH_APP_KEY", ApplicationSecretEnv: "OVH_APP_SECRET", ConsumerKeyEnv: "OVH_CONSUMER_KEY", RequestLogBucket: "logs"}}
	if gatewayCredential {
		backend.GatewayAccessKeyEnv, backend.GatewaySecretKeyEnv = "GATEWAY_KEY", "GATEWAY_SECRET"
	}
	body, err := json.Marshal(objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "ovh"},
		Transfer: objectstorage.ObjectTransferConfig{MaxBytesPerSecond: bytesPerSecond}, Backends: []objectstorage.BackendConfig{backend}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "object-storage.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := objectstorage.LoadGateway(func(name string) string {
		switch name {
		case "FAAS_OBJECT_STORAGE_CONFIG":
			return path
		case "ADMIN_KEY", "ADMIN_SECRET", "GATEWAY_KEY", "GATEWAY_SECRET":
			return "synthetic"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// adr: 20261031122757 — a gateway on a compute node holds the storage seal
// domain and a gateway credential, never the fleet identity or the
// bucket-admin credential, and keeps to its port share.
func TestComputeGatewayHoldsOnlyWhatTheNodeMay(t *testing.T) {
	storage, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	domain := []*age.X25519Identity{storage}
	for _, tc := range []struct {
		name     string
		storage  []*age.X25519Identity
		fleet    string
		registry *objectstorage.Registry
		want     string
	}{
		{name: "allowed", storage: domain, registry: computeGatewayRegistry(t, true, 37_500_000)},
		{name: "no storage domain", registry: computeGatewayRegistry(t, true, 37_500_000), want: "storage seal domain"},
		{name: "fleet identity", storage: domain, fleet: "/run/credentials/faas_fleet_age_identity", registry: computeGatewayRegistry(t, true, 37_500_000), want: "fleet or host identity"},
		{name: "no port share", storage: domain, registry: computeGatewayRegistry(t, true, 0), want: "max_bytes_per_second"},
		{name: "admin credential", storage: domain, registry: computeGatewayRegistry(t, false, 37_500_000), want: "names no gateway credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkComputeGateway(func(name string) string {
				if name == "FAAS_HOST_AGE_IDENTITY_PATH" {
					return tc.fleet
				}
				return ""
			}, tc.storage, tc.registry)
			if (err == nil) != (tc.want == "") || (err != nil && !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A compute node's gateway runs no provider usage export, so it never needs
// the provider account keys the control plane's gateway holds.
func TestComputeGatewayRunsNoUsageExports(t *testing.T) {
	registry := computeGatewayRegistry(t, true, 37_500_000)
	getenv := func(string) string { return "" }
	store := state.NewMemStore()
	if jobs, err := usageExportJobsForRole(role.RoleComputeOnly, registry, store, getenv); err != nil || len(jobs) != 0 {
		t.Fatalf("compute jobs = %d, %v; want none", len(jobs), err)
	}
	if _, err := usageExportJobsForRole(role.RoleControlPlane, registry, store, getenv); err == nil || !strings.Contains(err.Error(), "incomplete ovh usage credentials") {
		t.Fatalf("control-plane err = %v, want the missing provider account keys refused", err)
	}
}

func TestCheckListeners(t *testing.T) {
	for _, tc := range []struct {
		name    string
		role    role.Role
		data    string
		control string
		wantErr string
	}{
		{name: "control plane on loopback", role: role.RoleControlPlane, data: "127.0.0.1:8084", control: "127.0.0.1:9096"},
		{name: "compute control on every address", role: role.RoleComputeOnly, data: "127.0.0.1:8084", control: "0.0.0.0:9096"},
		{name: "control plane control off loopback", role: role.RoleControlPlane, data: "127.0.0.1:8084", control: "0.0.0.0:9096", wantErr: "control listener"},
		{name: "single box control off loopback", role: role.RoleSingleBox, data: "127.0.0.1:8084", control: "10.0.0.1:9096", wantErr: "control listener"},
		{name: "compute data off loopback", role: role.RoleComputeOnly, data: "0.0.0.0:8084", control: "0.0.0.0:9096", wantErr: "data listener"},
		{name: "control plane data off loopback", role: role.RoleControlPlane, data: "0.0.0.0:8084", control: "127.0.0.1:9096", wantErr: "data listener"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkListeners(tc.role, tc.data, tc.control)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("checkListeners: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("checkListeners err = %v, want one naming the %s", err, tc.wantErr)
			}
		})
	}
}
