package state

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// memSidecarsFixture creates an account + app for sidecar round-trip
// tests. The deployment is created separately by the test so the test
// can pin the Sidecars raw bytes verbatim.
func memSidecarsFixture(t *testing.T) (*MemStore, context.Context, App) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "sidecars-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "sidecars-" + uuid.NewString(), RAMMB: 512, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, app
}

// TestMemStore_Deployment_Sidecars_RoundTrip pins the contract that
// Deployment.Sidecars (json.RawMessage) survives a CreateDeployment ↔
// DeploymentByID round-trip byte-for-byte. PR-A ships the contract;
// PR-B reads from this field, so any byte-level drift on the JSON
// encoding (whitespace, key ordering, type coercion) is a regression
// the moment PR-B lands.
//
// Two-sidecar payload preserves the canonical customer shape from issue
// #463. Cardinality validation belongs to the API and database layers;
// MemStore preserves valid JSON bytes without imposing a second policy.
func TestMemStore_Deployment_Sidecars_RoundTrip(t *testing.T) {
	m, ctx, app := memSidecarsFixture(t)
	raw := json.RawMessage(`[
		{"name":"migrator","image":"ghcr.io/me/migrator@sha256:0000000000000000000000000000000000000000000000000000000000000001","type":"init","cmd":["--to","head"]},
		{"name":"scraper","image":"ghcr.io/me/scraper@sha256:0000000000000000000000000000000000000000000000000000000000000002","type":"sidecar"}
	]`)

	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:sidecars",
		Sidecars:    raw,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	got, err := m.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if !bytes.Equal(got.Sidecars, raw) {
		t.Errorf("Sidecars round-trip drifted\n  got:  %s\n  want: %s", got.Sidecars, raw)
	}
}

// TestMemStore_Deployment_Sidecars_DefaultEmpty pins the contract that
// a deployment created without an explicit Sidecars field reads back
// as a nil json.RawMessage (the byte-equivalent of "no sidecars
// shape"). The handler normalises nil → '[]'::jsonb before the
// INSERT (see cmd/apid/handlers_deployments.go passthrough), so
// MemStore sees the raw bytes verbatim either way.
func TestMemStore_Deployment_Sidecars_DefaultEmpty(t *testing.T) {
	m, ctx, app := memSidecarsFixture(t)

	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:no-sidecars",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	got, err := m.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if len(got.Sidecars) != 0 {
		t.Errorf("Sidecars default = %s; want nil", got.Sidecars)
	}
}

// TestMemStore_Deployment_Sidecars_VariousJSONPayloads pins that
// arbitrary, well-formed JSONB payloads survive the round-trip. The
// schema defines no per-sidecar shape (the api-side `Sidecar.Validate`
// is the load-bearing gate per ADR-068 §Decision 2); the state layer
// just carries bytes. This test asserts that the bytes pass through
// unimpeded.
func TestMemStore_Deployment_Sidecars_VariousJSONPayloads(t *testing.T) {
	m, ctx, app := memSidecarsFixture(t)

	cases := []struct {
		name    string
		payload string
	}{
		{"empty-array", `[]`},
		{"one-init", `[{"name":"only","image":"x@sha256:01","type":"init"}]`},
		{"max-five-helpers", `[{"name":"init","image":"x@sha256:01","type":"init"},{"name":"a","image":"x@sha256:02","type":"sidecar"},{"name":"b","image":"x@sha256:03","type":"sidecar"},{"name":"c","image":"x@sha256:04","type":"sidecar"},{"name":"d","image":"x@sha256:05","type":"sidecar"}]`},
		{"whitespace-but-valid", `  [{"name":"ws","image":"x@sha256:01","type":"init"}]  `},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(tc.payload)
			dep, err := m.CreateDeployment(ctx, Deployment{
				AppID:       app.ID,
				ImageDigest: "sha256:case-" + tc.name,
				Sidecars:    raw,
			})
			if err != nil {
				t.Fatalf("CreateDeployment(%s): %v", tc.name, err)
			}
			got, err := m.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatalf("DeploymentByID(%s): %v", tc.name, err)
			}
			if !bytes.Equal(got.Sidecars, raw) {
				t.Errorf("Sidecars round-trip(%s) drifted\n  got:  %s\n  want: %s", tc.name, got.Sidecars, raw)
			}
		})
	}
}
