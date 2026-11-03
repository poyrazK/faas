package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A preview is torn down with its pull request or lease, so a custom domain
// on it stopped serving then and kept its domain slot until the purge.
func TestCreateDomain_RefusesPreviewApp(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "domain-web", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	preview, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "pr-3-domain-web", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: "domain-web", PreviewPrNumber: 3,
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, "POST", "/v1/domains", api.CreateCustomDomainRequest{Domain: "preview.example.com", AppID: preview.ID}, nil)
	if rec.Code != 409 {
		t.Fatalf("domain on a preview = %d: %s, want 409", rec.Code, rec.Body)
	}
	if rec := e.Do(t, "POST", "/v1/domains", api.CreateCustomDomainRequest{Domain: "shop.example.com", AppID: "domain-web"}, nil); rec.Code != 202 {
		t.Fatalf("domain on the production app = %d: %s, want 202", rec.Code, rec.Body)
	}
}
