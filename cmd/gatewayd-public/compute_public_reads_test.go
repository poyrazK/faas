// adr: 20261031122757
// compute_public_reads_test.go pins what a compute edge holds to serve public
// bucket reads itself (step 7): the gateway credential for every backend,
// never the bucket-admin one, and its part of the node's port share.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func computeEdgeRegistry(t *testing.T, gatewayCredential bool) *objectstorage.Registry {
	t.Helper()
	backend := objectstorage.BackendConfig{ID: "ovh", Driver: "s3", Region: "us-east-1", S3Region: "us-east-va", Namespace: "project", Endpoint: "https://s3.example.test", PathStyle: true,
		AccessKeyEnv: "ADMIN_KEY", SecretKeyEnv: "ADMIN_SECRET"}
	if gatewayCredential {
		backend.GatewayAccessKeyEnv, backend.GatewaySecretKeyEnv = "GATEWAY_KEY", "GATEWAY_SECRET"
	}
	body, err := json.Marshal(objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "ovh"}, Backends: []objectstorage.BackendConfig{backend}})
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

func TestComputePublicReadBudget(t *testing.T) {
	share := strconv.FormatInt(12_375_000, 10)
	for _, tc := range []struct {
		name     string
		registry *objectstorage.Registry
		budget   string
		want     int64
		wantErr  string
	}{
		{name: "gateway credential and a share", registry: computeEdgeRegistry(t, true), budget: share, want: 12_375_000},
		{name: "bucket-admin credential only", registry: computeEdgeRegistry(t, false), budget: share, wantErr: "names no gateway credential"},
		{name: "no share", registry: computeEdgeRegistry(t, true), wantErr: publicReadBudgetEnv},
		{name: "below the floor", registry: computeEdgeRegistry(t, true), budget: strconv.FormatInt(api.MinObjectGatewayTransferBytesPerSecond-1, 10), wantErr: publicReadBudgetEnv},
		{name: "not a number", registry: computeEdgeRegistry(t, true), budget: "30%", wantErr: publicReadBudgetEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := computePublicReadBudget(func(name string) string {
				if name == publicReadBudgetEnv {
					return tc.budget
				}
				return ""
			}, tc.registry)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("computePublicReadBudget = %d, %v; want an error naming %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("computePublicReadBudget = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}
