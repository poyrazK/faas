package sched

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSidecarSpecsFromDeployment_UsesDeclarationOrder(t *testing.T) {
	falseValue := false
	raw, err := json.Marshal(api.Sidecars{
		{Name: "metrics", Image: "ghcr.io/org/metrics@sha256:01", Type: api.SidecarTypeSidecar, Port: 9090},
		{Name: "migrate", Type: api.SidecarTypeInit, Essential: &falseValue, RamMB: 64},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The store reader sorts by sidecar_name. The scheduler must instead
	// preserve the JSON declaration order because it determines drive slots.
	layers := []state.DeploymentSidecarLayer{
		{SidecarName: "migrate", StorageKey: "apps/a/d-migrate.ext4"},
		{SidecarName: "metrics", StorageKey: "apps/a/d-metrics.ext4"},
	}
	got, err := sidecarSpecsFromDeployment(raw, layers)
	if err != nil {
		t.Fatalf("sidecarSpecsFromDeployment: %v", err)
	}
	want := []fcvm.WorkloadSpec{
		{
			Name: "metrics", Type: "sidecar", Image: "ghcr.io/org/metrics@sha256:01", StorageKey: "apps/a/d-metrics.ext4",
			DriveID: fcvm.DriveSidecarPrefix + "0", Port: 9090, Essential: true,
		},
		{
			Name: "migrate", Type: "init", StorageKey: "apps/a/d-migrate.ext4",
			DriveID: fcvm.DriveSidecarPrefix + "1", RamMB: 64, Essential: false,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workload specs = %#v, want %#v", got, want)
	}
}

func TestSidecarSpecsFromDeployment_RejectsLayerSetMismatch(t *testing.T) {
	raw := json.RawMessage(`[{"name":"metrics","type":"sidecar"}]`)

	if _, err := sidecarSpecsFromDeployment(raw, nil); err == nil || !strings.Contains(err.Error(), "no built layer") {
		t.Fatalf("missing layer error = %v, want no built layer", err)
	}

	if _, err := sidecarSpecsFromDeployment(raw, []state.DeploymentSidecarLayer{
		{SidecarName: "metrics", StorageKey: "apps/a/metrics.ext4"},
		{SidecarName: "orphan", StorageKey: "apps/a/orphan.ext4"},
	}); err == nil || !strings.Contains(err.Error(), "not referenced") {
		t.Fatalf("orphan layer error = %v, want not referenced", err)
	}
}

func TestSidecarSpecsFromDeployment_PreservesSealedEnv(t *testing.T) {
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := secretbox.SealBytes(ident.Recipient(), "sidecar_env", []byte("secret"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(api.Sidecars{{
		Name: "metrics", Image: "ghcr.io/org/metrics@sha256:01", Type: api.SidecarTypeSidecar,
		Env: map[string]string{"TOKEN": base64.StdEncoding.EncodeToString(ciphertext)},
	}})
	if err != nil {
		t.Fatal(err)
	}

	got, err := sidecarSpecsFromDeployment(raw, []state.DeploymentSidecarLayer{{
		SidecarName: "metrics", StorageKey: "apps/a/d-metrics.ext4",
	}})
	if err != nil {
		t.Fatalf("sidecarSpecsFromDeployment: %v", err)
	}
	if len(got) != 1 || len(got[0].SealedEnv) != 1 {
		t.Fatalf("sealed env = %#v, want one entry", got)
	}
	if got[0].SealedEnv[0].Key != "TOKEN" || !reflect.DeepEqual(got[0].SealedEnv[0].Ciphertext, ciphertext) {
		t.Errorf("sealed env = %#v, want original ciphertext", got[0].SealedEnv)
	}
}

func TestSidecarSpecsFromDeployment_RejectsUnsafeOrDuplicateDeclarations(t *testing.T) {
	layers := []state.DeploymentSidecarLayer{
		{SidecarName: "metrics", StorageKey: "apps/a/metrics.ext4"},
	}
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "unsafe name", raw: `[{"name":"../metrics","type":"sidecar"}]`, want: "invalid sidecar name"},
		{name: "duplicate name", raw: `[{"name":"metrics","type":"sidecar"},{"name":"metrics","type":"init"}]`, want: "duplicate sidecar name"},
		{name: "duplicate type", raw: `[{"name":"metrics","type":"sidecar"},{"name":"logs","type":"sidecar"}]`, want: "duplicate sidecar type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := sidecarSpecsFromDeployment(json.RawMessage(tt.raw), layers); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
