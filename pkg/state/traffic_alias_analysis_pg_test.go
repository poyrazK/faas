//go:build !no_pg

// adr: 375
package state

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgTrafficAliasPublicationRollbackAndNamespace(t *testing.T) {
	for _, mode := range []string{"new", "retarget", "new-url", "different-domain", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			_, pool, account, app := trafficHostPGFixture(t)
			domain := "apps.example.test"
			if mode == "disabled" {
				domain = ""
			}
			store := NewPgStore(pool, WithTrafficAppsDomain(domain))
			testTrafficAliasPublication(t, store, store, app, mode, func(pattern string) {
				in := globalTrafficRule(account, app, pattern, 520)
				encoded, err := json.Marshal(in.Action)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES('00000000-0000-0000-0000-000000000375',$1,$2,$3,'/',true,'route',$4::jsonb)`, account.ID, app.ID, pattern, encoded); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestPgTrafficAliasMetadataMatchesRuntimeStatuses(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	for _, status := range []DeploymentStatus{DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting, DeployLive, DeploySuperseded, DeployFailed, DeployCancelled} {
		t.Run(string(status), func(t *testing.T) {
			deployment, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: status})
			if err != nil {
				t.Fatal(err)
			}
			name := string(status)
			if _, err := pool.Exec(t.Context(), `INSERT INTO deployment_aliases(app_id,name,deployment_id) VALUES($1,$2,$3)`, app.ID, name, deployment.ID); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			view, err := readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(account.ID), ".apps.example.test")
			if err != nil {
				t.Fatal(err)
			}
			label, _ := hostidentity.DeploymentAliasLabel(app.ID, name)
			host := label + ".apps.example.test"
			found := false
			for _, candidate := range view.AliasHosts {
				found = found || candidate == host
			}
			if found != deployment.DeploymentAliasActive() {
				t.Fatalf("alias eligibility differs from runtime: status=%s found=%v", status, found)
			}
			if status == DeploySuperseded {
				row, err := sqlc.New().ReadTrafficHostAnalysis(t.Context(), tx, sqlc.ReadTrafficHostAnalysisParams{AccountID: uuidToPgtype(account.ID), AppsSuffix: ".apps.example.test", MaxInputs: 1, MaxBytes: 1})
				if err != nil || len(row.Data) != 0 {
					t.Fatalf("alias metadata escaped scalar bounds: %+v err=%v", row, err)
				}
			}
		})
	}
	name := strings.Repeat("a", 40)
	deployment, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO deployment_aliases(app_id,name,deployment_id) VALUES($1,$2,$3)`, app.ID, name, deployment.ID); err != nil {
		t.Fatal(err)
	}
	label, _ := hostidentity.DeploymentAliasLabel(app.ID, name)
	if _, err := store.DeploymentAliasByHostLabel(t.Context(), label); err != nil {
		t.Fatalf("legacy SQL label lookup: %v", err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	view, err := readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(account.ID), ".apps.example.test")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range view.AliasHosts {
		found = found || candidate == label+".apps.example.test"
	}
	if !found {
		t.Fatal("legacy SQL label omitted from analysis")
	}
}
