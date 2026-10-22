package state_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReleaseSecretsStandaloneDeploys(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, store state.Store, _ state.RuntimeReleaseStore) {
		ctx := t.Context()
		account, err := store.CreateAccount(ctx, "standalone-secrets@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "standalone-secrets", Type: state.AppTypeFunction, Runtime: "node22"})
		if err != nil {
			t.Fatal(err)
		}
		if app.ProjectID != "" {
			t.Fatal("fixture must remain outside a project")
		}
		sealed := []byte("sealed-standalone-value")
		if err := store.UpsertAppSecret(ctx, account.ID, app.ID, "TOKEN", sealed); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppEnv(ctx, account.ID, app.ID, "MODE", "standalone"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatalf("standalone deploy requires no project release owner: %v", err)
			}
			snapshot, err := store.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, deployment.ID)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.ReleaseSecretsPinned || len(snapshot.Secrets) != 1 || !bytes.Equal(snapshot.Secrets[0].Ciphertext, sealed) {
				t.Fatal("standalone secret inputs were replaced by project release pins")
			}
			if len(snapshot.Values) != 1 || snapshot.Values[0].Key != "MODE" || snapshot.Values[0].Value != "standalone" {
				t.Fatal("standalone environment inputs changed", snapshot.Values)
			}
			if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: uuid.NewString(), Kind: state.DeploymentKindTarball}); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("missing app was admitted", err)
		}
	})
}
