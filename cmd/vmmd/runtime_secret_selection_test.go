// adr: 462 — release-only managed migration credential delivery.

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type migrationDeliveryStore struct {
	deployment state.Deployment
	rows       []state.AppSecret
}

func (s migrationDeliveryStore) DeploymentByID(context.Context, string) (state.Deployment, error) {
	return s.deployment, nil
}
func (s migrationDeliveryStore) ListAppSecretsInScope(context.Context, string, string, string) ([]state.AppSecret, error) {
	return s.rows, nil
}

func TestRuntimeSecretRefreshExcludesMigrationBindings(t *testing.T) {
	s := migrationDeliveryStore{deployment: state.Deployment{ID: "dep", AppID: "app", Scope: "default"}, rows: []state.AppSecret{
		{AccountID: "acct", AppID: "app", Scope: "default", Key: "DATABASE_URL", Ciphertext: []byte("runtime"), DeliveryVersion: 1},
		{AccountID: "acct", AppID: "app", Scope: "default", Key: "SCHEMA_DSN", Ciphertext: []byte("ddl"), DeliveryVersion: 1, ManagedPostgresBindingID: "binding", ManagedPostgresAccess: "migration"},
	}}
	selection, err := selectRuntimeSecretRowsForWorkload(context.Background(), s, "dep", "app", "acct", "")
	if err != nil || len(selection.Rows) != 1 || selection.Rows[0].Key != "DATABASE_URL" {
		t.Fatalf("runtime selection = %+v, %v", selection, err)
	}
	// Rotating a credential outside the workload's audience changes neither
	// the serving projection nor the revision acknowledgement fence.
	s.rows[1].DeliveryVersion++
	s.rows[1].Ciphertext = []byte("rotated ddl")
	rotated, err := selectRuntimeSecretRowsForWorkload(context.Background(), s, "dep", "app", "acct", "")
	if err != nil || rotated.Revision != selection.Revision {
		t.Fatalf("DDL rotation changed serving projection: %v", err)
	}
	s.deployment.OverrideEnvSecrets = json.RawMessage(`{"SCHEMA_DSN":"secret:SCHEMA_DSN"}`)
	if _, err := selectRuntimeSecretRowsForWorkload(context.Background(), s, "dep", "app", "acct", ""); err == nil {
		t.Fatal("explicit main grant bypassed release restriction")
	}
	s.deployment.Sidecars = json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"SCHEMA_DSN":"secret:SCHEMA_DSN"}}]`)
	if _, err := selectRuntimeSecretRowsForWorkload(context.Background(), s, "dep", "app", "acct", "worker"); err == nil {
		t.Fatal("sidecar grant bypassed release restriction")
	}
}
