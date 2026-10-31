package apidserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A preview created through the API keeps the production app's tenant-only
// gate and security posture policy: a tenant-only app's preview answered
// requests without tenant attribution.
func TestCreatePreview_KeepsTenantAndSecurityGates(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	if _, err := store.CreateAppIfUnderQuota(ctx, state.App{AccountID: acct.ID, Slug: "tenant-web", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, PlatformTenantRequired: true,
		SecurityPolicy: api.AppSecurityPolicyEnforce}, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, "POST", "/v1/apps/tenant-web/previews", api.CreatePreviewRequest{PRNumber: 7, TTLHours: 12}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create preview = %d: %s", rec.Code, rec.Body)
	}
	preview, err := store.AppBySlug(ctx, "pr-7-tenant-web")
	if err != nil || !preview.PlatformTenantRequired || preview.SecurityPolicy != api.AppSecurityPolicyEnforce {
		t.Fatalf("preview = tenant %t, policy %q (%v); want production's", preview.PlatformTenantRequired, preview.SecurityPolicy, err)
	}
}
