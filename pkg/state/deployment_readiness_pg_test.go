// adr: 570
package state

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestDeploymentReadinessInputBoundBeforeDatabaseAccess(t *testing.T) {
	store := &PgStore{}
	for _, tc := range []struct {
		name  string
		ids   []string
		fails bool
	}{
		{name: "empty"},
		{name: "invalid identity", ids: []string{"invalid"}, fails: true},
		{name: "batch overflow", ids: make([]string, api.TrafficReadinessBatchSize+1), fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.DeploymentReadinessConfigs(t.Context(), tc.ids)
			if (err != nil) != tc.fails || len(got) != 0 {
				t.Fatalf("bounded config read=%+v err=%v", got, err)
			}
		})
	}
}

func TestDeploymentReadinessPostgresProjectsOnlyGateConfiguration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "readiness-projection@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "readiness-projection", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:projection", Status: DeployPending,
		OverrideReadinessProbe: json.RawMessage(`{"path":"/readyz"}`),
		Sidecars:               json.RawMessage(`[{"name":"proxy","type":"sidecar","primary_ingress":true,"image":"private/image","env":{"TOKEN":"fixture-sidecar-secret"},"readiness_probe":{"exec":{"command":["fixture-probe-secret"]}}}]`)})
	if err != nil {
		t.Fatal(err)
	}
	missing := uuid.NewString()
	configs, err := store.DeploymentReadinessConfigs(t.Context(), []string{deployment.ID, deployment.ID, missing})
	if err != nil {
		t.Fatal(err)
	}
	config := configs[deployment.ID]
	if len(configs) != 1 || config.AppID != app.ID || config.DeploymentID != deployment.ID || !bytes.Equal(config.OverrideReadinessProbe, deployment.OverrideReadinessProbe) {
		t.Fatalf("readiness configuration identity or cardinality differs: %+v", configs)
	}
	var sidecars []map[string]json.RawMessage
	if err := json.Unmarshal(config.Sidecars, &sidecars); err != nil {
		t.Fatal(err)
	}
	if len(sidecars) != 1 || len(sidecars[0]) != 4 || string(sidecars[0]["name"]) != `"proxy"` || string(sidecars[0]["readiness_probe"]) != `{}` ||
		bytes.Contains(config.Sidecars, []byte("secret")) || bytes.Contains(config.Sidecars, []byte("image")) || bytes.Contains(config.Sidecars, []byte("env")) {
		t.Fatalf("unrelated sidecar settings reached readiness reader: %s", config.Sidecars)
	}
	// A test-only tombstone isolates deleted-deployment behavior; production
	// readiness refresh never writes customer intent or lifecycle state.
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET deleted_at = now() WHERE id = $1", deployment.ID); err != nil {
		t.Fatal(err)
	}
	configs, err = store.DeploymentReadinessConfigs(t.Context(), []string{deployment.ID})
	if err != nil || len(configs) != 0 {
		t.Fatalf("deleted readiness config returned: %+v err=%v", configs, err)
	}
}
