package state

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreOpenAPIContractPolicyDefaultsAndUpdates(t *testing.T) {
	store := NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "openapi-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), App{AccountID: acct.ID, Slug: "policy-app"})
	if err != nil {
		t.Fatal(err)
	}
	if app.OpenAPIContractPolicy != api.OpenAPIContractPolicyObserve {
		t.Fatalf("default policy = %q, want observe", app.OpenAPIContractPolicy)
	}
	warn := api.OpenAPIContractPolicyWarn
	updated, err := store.UpdateApp(context.Background(), app.ID, UpdateAppParams{
		OpenAPIContractPolicy: &warn, SetOpenAPIContractPolicy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.OpenAPIContractPolicy != warn {
		t.Fatalf("updated policy = %q, want warn", updated.OpenAPIContractPolicy)
	}
}
