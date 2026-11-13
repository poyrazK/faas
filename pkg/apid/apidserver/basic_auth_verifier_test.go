package apidserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/basicverifier"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261110210000 slice 3h — setting basic auth stores the verify-only
// form keyed by the sealed blob, so edges can check it without opening it.
func TestPublicAuthBasic_StoresAVerifier(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	e := apidserver.Setup(t, api.PlanPro)
	withEdgeRecipients(t, fleet.Recipient(), true, domain.Recipient(), "")
	app := apidserver.SeedAppForAudit(t, e, "verifier-basic-app")
	rec := e.Do(t, http.MethodPatch, "/v1/apps/"+app.Slug, api.UpdateAppRequest{PublicAuth: &api.PublicAuthBlock{
		Mode: api.AppPublicAuthModeBasic, BasicUser: "editor", BasicPass: "verifier-pass",
	}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", rec.Code, rec.Body.String())
	}
	ctx := context.Background()
	row, err := (*e.Store()).AppBySlug(ctx, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	var verifiers state.BasicAuthVerifierStore = *e.Store()
	v, err := verifiers.AppBasicAuthVerifier(ctx, basicverifier.Key(row.PublicAuthBasicSealed))
	if err != nil {
		t.Fatalf("no verifier for the sealed credential: %v", err)
	}
	for pass, want := range map[string]bool{"verifier-pass": true, "wrong": false} {
		if got, err := basicverifier.Verify(ctx, v, "editor", pass); err != nil || got != want {
			t.Errorf("Verify(%q) = %v, %v; want %v", pass, got, err, want)
		}
	}
}
