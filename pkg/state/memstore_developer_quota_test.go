package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreDeveloperQuotaIsSeparateFromDeployedApps(t *testing.T) {
	m := state.NewMemStore()
	acct, err := m.CreateAccount(context.Background(), "developer-quota@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanFree)

	dev := state.App{AccountID: acct.ID, Slug: "dev-one", Status: state.AppActive, PreviewOfSlug: "project"}
	if _, err := m.CreateAppIfUnderQuota(context.Background(), dev, limits); err != nil {
		t.Fatalf("create developer app: %v", err)
	}
	prod := state.App{AccountID: acct.ID, Slug: "production", Status: state.AppActive}
	if _, err := m.CreateAppIfUnderQuota(context.Background(), prod, limits); err != nil {
		t.Fatalf("developer app consumed deployed-app quota: %v", err)
	}

	if got, _ := m.CountDeployedApps(context.Background(), acct.ID); got != 1 {
		t.Fatalf("CountDeployedApps = %d, want 1", got)
	}
	if got, _ := m.CountDeveloperApps(context.Background(), acct.ID); got != 1 {
		t.Fatalf("CountDeveloperApps = %d, want 1", got)
	}

	secondDev := state.App{AccountID: acct.ID, Slug: "dev-two", Status: state.AppActive, PreviewOfSlug: "another-project"}
	_, err = m.CreateAppIfUnderQuota(context.Background(), secondDev, limits)
	var quotaErr *state.QuotaError
	if !errors.As(err, &quotaErr) || quotaErr.Kind != state.QuotaErrorKindDeveloperApps || quotaErr.Limit != limits.DeveloperApps || quotaErr.Observed != 1 {
		t.Fatalf("second developer app error = %v, want developer quota error", err)
	}
}
