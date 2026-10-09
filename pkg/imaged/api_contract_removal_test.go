package imaged

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestAPIContractDarkRemoval(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
	for _, test := range []struct {
		name    string
		percent int
		schema  string
		blocked bool
	}{
		{"dark_removal", 0, "string", false},
		{"unapproved_weighted_removal", 100, "string", true},
		{"dark_unrelated_break", 0, "integer", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "contract-"+test.name+"@test.invalid", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "contract", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			serving, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "prod", Kind: state.DeploymentKindImage, Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true})
			if err != nil {
				t.Fatal(err)
			}
			if err = store.SetDeploymentRootfs(ctx, serving.ID, "/test", "test", 1024); err != nil {
				t.Fatal(err)
			}
			if err = store.UpdateDeploymentStatus(ctx, serving.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			baseline := []byte(`{"openapi":"3.0.3","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"string"}}}}}}}}}`)
			candidate := []byte(`{"openapi":"3.0.3","paths":{"/new":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"` + test.schema + `"}}}}}}}}}`)
			snapshot, _, err := openapidiff.SnapshotFromDocument(serving.ID, app.ID, "prod", baseline, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.UpdateDeploymentOpenAPISnapshot(ctx, snapshot); err != nil {
				t.Fatal(err)
			}
			if err = store.UpsertAppOpenAPIDoc(ctx, app.ID, account.ID, candidate, 1, "3.0.3"); err != nil {
				t.Fatal(err)
			}
			handler := &Handler{store: store}
			err = handler.checkAPIContract(ctx, state.Deployment{ID: "candidate", AppID: app.ID, Scope: "prod", TrafficPercent: test.percent, TrafficPercentExplicit: true})
			if (err != nil) != test.blocked {
				t.Fatalf("blocked=%v: %v", test.blocked, err)
			}
		})
	}
}
