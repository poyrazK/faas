// adr: 167
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainCanonicalURLFollowsRuntimeTenantRouting(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			// Durable configuration takes precedence over the process environment.
			t.Setenv("FAAS_TENANT_SURFACES_ENABLED", strconv.FormatBool(!enabled))
			e := apidserver.Setup(t, api.PlanPro)
			manager := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
			if err := manager.Apply("tenant_surfaces_enabled", json.RawMessage(strconv.FormatBool(enabled))); err != nil {
				t.Fatal(err)
			}
			(*e.S()).WithRuntimeConfigManager(manager)
			store, ctx := *e.Store(), t.Context()
			const slug = "canonical-origin"
			const host = "canonical.example.com"
			appID := apidserver.MustSeedApp(t, e, slug)
			domainRecoveryVerified(t, store, host, appID, "proof")
			selected := e.Do(t, http.MethodPost, "/v1/domains/"+host+"/default", nil, nil)
			if selected.Code != http.StatusOK {
				t.Fatalf("initial default: %d %s", selected.Code, selected.Body)
			}
			other, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "canonical-target", Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			limits := api.MustLimitsFor(api.PlanPro)
			surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: other.AccountID, AppID: other.ID, Name: "canonical-override"}, limits)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.CreateTenantHostnameIfUnderQuota(state.WithPublicTenantHostnameClaim(ctx), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: host, ChallengeToken: "tenant-proof"}, limits)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.MarkTenantHostnameVerified(ctx, host); err != nil {
				t.Fatal(err)
			}
			if err = store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
				t.Fatal(err)
			}
			for _, routingEnabled := range []bool{enabled, !enabled} {
				if err = manager.Apply("tenant_surfaces_enabled", json.RawMessage(strconv.FormatBool(routingEnabled))); err != nil {
					t.Fatal(err)
				}
				selected = e.Do(t, http.MethodPost, "/v1/domains/"+host+"/default", nil, nil)
				wantStatus := http.StatusOK
				if routingEnabled {
					wantStatus = http.StatusConflict
				}
				if selected.Code != wantStatus {
					t.Fatalf("shadowed default, routing=%v: %d %s", routingEnabled, selected.Code, selected.Body)
				}
				response := e.Do(t, http.MethodGet, "/v1/apps/"+slug, nil, nil)
				if response.Code != http.StatusOK {
					t.Fatalf("get app: %d %s", response.Code, response.Body)
				}
				var app api.AppResponse
				if err = json.Unmarshal(response.Body.Bytes(), &app); err != nil {
					t.Fatal(err)
				}
				wantURL, wantDomain := "https://"+host, host
				if routingEnabled {
					wantURL, wantDomain = app.URL, ""
				}
				if app.CanonicalURL != wantURL || app.DefaultDomain != wantDomain {
					t.Fatalf("single app routing=%v: canonical=%q default=%q; want %q %q", routingEnabled, app.CanonicalURL, app.DefaultDomain, wantURL, wantDomain)
				}
				response = e.Do(t, http.MethodGet, "/v1/apps", nil, nil)
				if response.Code != http.StatusOK {
					t.Fatalf("list apps: %d %s", response.Code, response.Body)
				}
				var apps []api.AppResponse
				if err = json.Unmarshal(response.Body.Bytes(), &apps); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, listed := range apps {
					if listed.ID == appID {
						found = true
						if listed.CanonicalURL != wantURL || listed.DefaultDomain != wantDomain {
							t.Fatalf("batch app routing=%v: %+v", routingEnabled, listed)
						}
					}
				}
				if !found {
					t.Fatal("canonical app missing from list")
				}
				response = e.Do(t, http.MethodGet, "/v1/domains/"+host, nil, nil)
				if response.Code != http.StatusOK {
					t.Fatalf("get domain: %d %s", response.Code, response.Body)
				}
				var domain api.CustomDomainResponse
				if err = json.Unmarshal(response.Body.Bytes(), &domain); err != nil {
					t.Fatal(err)
				}
				if domain.Default == routingEnabled {
					t.Fatalf("domain default flag disagrees with routing=%v: %+v", routingEnabled, domain)
				}
			}
		})
	}
}
