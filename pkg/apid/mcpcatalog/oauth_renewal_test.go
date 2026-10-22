package mcpcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type renewalSaveFailureStore struct {
	state.Store
	state.MCPCatalogObserverStore
	state.MCPCatalogOAuthRenewalStore
	fail bool
}

func (s *renewalSaveFailureStore) SaveMCPCatalogObserverOAuth(ctx context.Context, job state.MCPCatalogObserverJob, status api.MCPCatalogObserverOAuthStatus) error {
	if s.fail {
		s.fail = false
		return state.ErrConflict
	}
	return s.MCPCatalogObserverStore.SaveMCPCatalogObserverOAuth(ctx, job, status)
}
func TestRenewalRecoveryMemory(t *testing.T) { testRenewalRecovery(t, state.NewMemStore()) }
func TestRenewalRecoveryPostgres(t *testing.T) {
	testRenewalRecovery(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testRenewalRecovery(t *testing.T, store state.Store) {
	t.Helper()
	for _, scenario := range []string{"saved_result", "lost_response", "deleted_app", "rejected", "malformed_success"} {
		unknown := scenario == "lost_response" || scenario == "malformed_success"
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, uuid.NewString()+"@renewal.test", api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "renewal-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
			if err != nil {
				t.Fatal(err)
			}
			identity, _ := age.GenerateX25519Identity()
			sealed, _ := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"REFRESH": "ORIGINAL_PRIVATE"})
			if err = store.UpsertAppSecret(ctx, acct.ID, app.ID, "REFRESH", sealed); err != nil {
				t.Fatal(err)
			}
			monitors := store.(state.MCPCatalogMonitorStore)
			observers := store.(state.MCPCatalogObserverStore)
			config := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH"}}}
			if _, err = monitors.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, config); err != nil {
				t.Fatal(err)
			}
			due, err := observers.ListDueMCPCatalogObservers(ctx, 4)
			if err != nil {
				t.Fatal(err)
			}
			var job state.MCPCatalogObserverJob
			for _, v := range due {
				if v.AppID == app.ID {
					job = v
				}
			}
			job, err = observers.ClaimMCPCatalogObserver(ctx, job)
			if err != nil {
				t.Fatal(err)
			}
			wrapped := &renewalSaveFailureStore{Store: store, MCPCatalogObserverStore: observers, MCPCatalogOAuthRenewalStore: store.(state.MCPCatalogOAuthRenewalStore), fail: !unknown && scenario != "rejected"}
			calls := 0
			runner := ObserverRunner{Store: wrapped, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }, Client: &http.Client{Transport: oauthRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				if scenario == "lost_response" {
					return nil, errors.New("PRIVATE_TRANSPORT_ERROR")
				}
				if scenario == "rejected" && calls == 1 {
					return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{"error":"temporarily_unavailable"}`))}, nil
				}
				if scenario == "malformed_success" {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"RECOVERED_PRIVATE_ACCESS","refresh_token":"ROTATED_PRIVATE","token_type":"Bearer","expires_in":0}`))}, nil
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"RECOVERED_PRIVATE_ACCESS","refresh_token":"ROTATED_PRIVATE","token_type":"Bearer","expires_in":3600}`))}, nil
			})}}
			if _, err = runner.oauthToken(ctx, job); err == nil {
				t.Fatal("failed exchange/install unexpectedly succeeded")
			}
			root, err := monitors.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			view, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
			job.Snapshot = view
			public, _ := json.Marshal(api.CatalogObserverStatus(view, time.Now()))
			if strings.Contains(string(public), "PRIVATE") || strings.Contains(string(public), "private_") {
				t.Fatal("renewal journal leaked private fields")
			}
			if view.ObserverStatus.Renewal == nil {
				t.Fatal("renewal intent missing")
			}
			if scenario == "deleted_app" {
				if err = store.DeleteApp(ctx, app.ID); err != nil {
					t.Fatal(err)
				}
				records, e := store.(state.MCPOAuthRetirementStore).ListMCPOAuthRetirements(ctx, acct.ID, 0)
				if e != nil || len(records.Items) != 1 || records.Items[0].Reason != "app_deleted" {
					t.Fatal("saved renewal was lost on deletion", e)
				}
			} else if !unknown {
				if scenario != "rejected" && (view.ObserverStatus.Renewal.State != "saved" || view.ObserverStatus.Renewal.Result == nil) {
					t.Fatal("result lost before installation")
				}
				// A harmless restart changes revision and lease, preserving the saved grant.
				config.Revision = uuid.NewString()
				if _, err = monitors.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, config); err != nil {
					t.Fatal(err)
				}
				due, err = observers.ListDueMCPCatalogObservers(ctx, 4)
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range due {
					if v.AppID == app.ID {
						job = v
					}
				}
				job, err = observers.ClaimMCPCatalogObserver(ctx, job)
				if err != nil {
					t.Fatal(err)
				}
				token, e := runner.oauthToken(ctx, job)
				if e != nil || token != "RECOVERED_PRIVATE_ACCESS" {
					t.Fatal("saved renewal could not recover", e)
				}
				root, _ = monitors.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
				view, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
				if view.ObserverStatus.Renewal.State != "installed" || view.ObserverStatus.Renewal.Result != nil {
					t.Fatal("installed journal retained duplicate sealed result")
				}
			} else if _, err = runner.oauthToken(ctx, job); err == nil {
				t.Fatal("unknown exchange retried")
			}
			expectedCalls := 1
			if scenario == "rejected" {
				expectedCalls = 2
			}
			if calls != expectedCalls {
				t.Fatal("recovery repeated provider exchange", calls)
			}
		})
	}
}
