package state

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreAppRetryPolicyRoundTripAndClear(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "retry-policy@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	policy := json.RawMessage(`{"max_attempts":4,"base_seconds":2,"max_seconds":30}`)
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "retry-policy", RetryPolicyJSON: policy})
	if err != nil {
		t.Fatal(err)
	}
	if got := app.RetryPolicy().MaxAttempts; got != 4 {
		t.Fatalf("max attempts = %d, want 4", got)
	}
	clear := []byte(`{}`)
	app, err = store.UpdateApp(ctx, app.ID, UpdateAppParams{RetryPolicyJSON: &clear, SetRetryPolicy: true})
	if err != nil {
		t.Fatal(err)
	}
	if !app.RetryPolicy().Zero() {
		t.Fatalf("cleared policy = %+v, want zero", app.RetryPolicy())
	}
}
