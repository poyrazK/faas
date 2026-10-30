package state_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// openAPIDocQuotaContract captures twelve deployments' documents at once
// against an allowance of three: the count and the save are one step, so
// exactly three land. Replacing a captured document is not a new one.
func openAPIDocQuotaContract(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "openapi-docs-"+uuid.NewString()[:8]+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "docs-" + uuid.NewString()[:8], Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	const limit, callers = 3, 12
	doc := []byte(`{"openapi":"3.1.0","info":{"title":"docs"}}`)
	capture := func(deploymentID string) error {
		return store.UpsertDeploymentOpenAPIDocIfUnderQuota(ctx, deploymentID, account.ID, app.ID, doc,
			state.OpenAPIDocSourceManualUpload, false, limit)
	}
	deployments := make([]string, callers)
	for i := range deployments {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:test"})
		if err != nil {
			t.Fatal(err)
		}
		deployments[i] = d.ID
	}
	var (
		mu       sync.Mutex
		captured []string
		wg       sync.WaitGroup
	)
	errs := make([]error, callers)
	for i, id := range deployments {
		wg.Go(func() {
			errs[i] = capture(id)
			if errs[i] == nil {
				mu.Lock()
				captured = append(captured, id)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(captured) != limit {
		t.Fatalf("%d concurrent captures landed against an allowance of %d: %v", len(captured), limit, errs)
	}
	for i, err := range errs {
		var quota *state.OpenAPIDocQuotaError
		if err != nil && (!errors.As(err, &quota) || quota.Limit != limit || quota.Observed != limit) {
			t.Fatalf("capture %d: %v, want the quota error at %d", i, err, limit)
		}
	}
	if err := capture(captured[0]); err != nil {
		t.Fatalf("replacing a captured document at the allowance: %v", err)
	}
	if err := capture(uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("capture for an unknown deployment: %v, want ErrNotFound", err)
	}
}

func TestMemOpenAPIDocQuotaHoldsUnderConcurrentCaptures(t *testing.T) {
	openAPIDocQuotaContract(t, state.NewMemStore())
}
