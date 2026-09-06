package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreDeveloperQuotaIsSeparateFromDeployedApps(t *testing.T) {
	m := NewMemStore()
	acct, err := m.CreateAccount(context.Background(), "developer-quota@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanFree)

	dev := App{AccountID: acct.ID, Slug: "dev-one", Status: AppActive, PreviewOfSlug: "project"}
	if _, err := m.CreateAppIfUnderQuota(context.Background(), dev, limits); err != nil {
		t.Fatalf("create developer app: %v", err)
	}
	prod := App{AccountID: acct.ID, Slug: "production", Status: AppActive}
	if _, err := m.CreateAppIfUnderQuota(context.Background(), prod, limits); err != nil {
		t.Fatalf("developer app consumed deployed-app quota: %v", err)
	}

	if got, _ := m.CountDeployedApps(context.Background(), acct.ID); got != 1 {
		t.Fatalf("CountDeployedApps = %d, want 1", got)
	}
	if got, _ := m.CountDeveloperApps(context.Background(), acct.ID); got != 1 {
		t.Fatalf("CountDeveloperApps = %d, want 1", got)
	}

	secondDev := App{AccountID: acct.ID, Slug: "dev-two", Status: AppActive, PreviewOfSlug: "another-project"}
	_, err = m.CreateAppIfUnderQuota(context.Background(), secondDev, limits)
	var quotaErr *QuotaError
	if !errors.As(err, &quotaErr) || quotaErr.Kind != QuotaErrorKindDeveloperApps || quotaErr.Limit != limits.DeveloperApps || quotaErr.Observed != 1 {
		t.Fatalf("second developer app error = %v, want developer quota error", err)
	}
}
