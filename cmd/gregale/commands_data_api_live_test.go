// adr: 20261016151947 — data-api live sets up what Data API live reads need.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// liveAPI fakes the routes data-api live reads and writes.
type liveAPI struct {
	mu        sync.Mutex
	live      bool
	hidden    bool
	reporting bool
	endpoints []api.ManagedRealtimeEndpointResponse
	writes    []string
	created   api.CreateManagedRealtimeEndpointRequest
}

func newLiveAPI(t *testing.T, l *liveAPI) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if r.Method != http.MethodGet {
			l.writes = append(l.writes, r.Method+" "+r.URL.Path)
		}
		switch {
		case r.URL.Path == "/v1/apps/shop-data":
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "shop-data", URL: "https://shop-data.gregale.app", CanonicalURL: "https://data.shop.example"})
		case r.URL.Path == "/v1/apps/shop-data/bindings":
			_ = json.NewEncoder(w).Encode(api.AppBindingInventory{App: "shop-data", Scope: "default", Bindings: []api.AppBindingInventoryItem{
				{Type: api.BindingTypePostgres, Name: "shop", Binding: "DATABASE_URL", Scope: "default", Access: "data_api"},
				{Type: api.BindingTypePostgres, Name: "shop", Binding: "MIGRATION_DATABASE_URL", Scope: "default", Access: "migration"},
			}})
		case r.URL.Path == "/v1/postgres/databases":
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-1", Name: "shop"}}})
		case r.URL.Path == "/v1/postgres/databases/db-1/live" && l.hidden:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not found","status":404,"code":"not_found"}`))
		case r.URL.Path == "/v1/postgres/databases/db-1/live":
			if r.Method == http.MethodPost {
				l.live = true
				_ = json.NewEncoder(w).Encode(api.ManagedPostgresLiveQueries{DatabaseID: "db-1", Enabled: true, Secret: "glsk1_shown_once"})
				return
			}
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresLiveQueries{DatabaseID: "db-1", Enabled: l.live})
		case r.URL.Path == "/v1/apps/shop-data/secrets":
			list := api.AppSecretListResponse{}
			if l.reporting {
				list.Secrets = []api.AppSecretResponse{{Key: dataAPIUsageURLKey, Scope: "default"}}
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.URL.Path == "/v1/apps/shop-data/secrets/"+dataAPIUsageURLKey:
			l.reporting = true
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1/apps/shop-data/realtime/endpoints" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &l.created)
			endpoint := api.ManagedRealtimeEndpointResponse{ID: "rt-1", AuthMode: l.created.AuthMode}
			l.endpoints = append(l.endpoints, endpoint)
			_ = json.NewEncoder(w).Encode(endpoint)
		case r.URL.Path == "/v1/apps/shop-data/realtime/endpoints":
			_ = json.NewEncoder(w).Encode(l.endpoints)
		case strings.Contains(r.URL.Path, "restart"):
			_, _ = w.Write([]byte(`{"wake_id":"wake-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_API", server.URL)
}

func TestDataAPILiveEnableSetsUpOnlyWhatIsMissing(t *testing.T) {
	fake := &liveAPI{}
	newLiveAPI(t, fake)
	client, ctx := httpsClient(t), context.Background()
	app, err := client.GetApp(ctx, "shop-data")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := readDataAPILive(ctx, client, app, "default")
	if err != nil || receipt.Database != "shop" || receipt.DatabaseID != "db-1" || receipt.Ready {
		t.Fatalf("before: %+v (%v)", receipt, err)
	}
	if err := enableDataAPILive(ctx, client, app, &receipt); err != nil {
		t.Fatal(err)
	}
	if strings.Join(receipt.Changed, ",") != "live_queries,usage_reporting,realtime_endpoint" || !receipt.Ready ||
		receipt.URL != "wss://data.shop.example/__gregale/realtime/rt-1" {
		t.Fatalf("after: %+v", receipt)
	}
	want := "POST /v1/postgres/databases/db-1/live|PUT /v1/apps/shop-data/secrets/DATA_API_USAGE_URL|POST /v1/apps/shop-data/realtime/endpoints"
	if got := strings.Join(fake.writes, "|"); got != want {
		t.Fatalf("writes %s", got)
	}
	// The endpoint carries live frames only; its callback goes to the app's
	// platform host with a token nobody needs to keep.
	if c := fake.created; c.AuthMode != api.RealtimeAuthModeNone || c.CallbackURL != "https://shop-data.gregale.app" ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(c.CallbackAuthToken) || c.Enabled == nil || !*c.Enabled {
		t.Fatalf("endpoint request %+v", c)
	}
	encoded, _ := json.Marshal(receipt)
	if strings.Contains(string(encoded), "glsk1_") {
		t.Fatalf("the live secret leaked: %s", encoded)
	}

	fake.writes = nil
	again, err := readDataAPILive(ctx, client, app, "default")
	if err != nil || !again.Ready {
		t.Fatalf("second read: %+v (%v)", again, err)
	}
	if err := enableDataAPILive(ctx, client, app, &again); err != nil || len(again.Changed) != 0 || len(fake.writes) != 0 {
		t.Fatalf("a second enable changed %v, wrote %v (%v)", again.Changed, fake.writes, err)
	}
}

func TestDataAPILiveStatusExplainsWhatIsMissing(t *testing.T) {
	resetJSONOut(t)
	fake := &liveAPI{live: true, reporting: true, endpoints: []api.ManagedRealtimeEndpointResponse{{ID: "rt-oidc", AuthMode: api.RealtimeAuthModeOIDCJWT}}}
	newLiveAPI(t, fake)
	stdout, stderr, restore := swapIO(t)
	code := cmdDataAPILive([]string{"status", "shop-data"})
	warnings := stderr()
	restore()
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout.String(), warnings)
	}
	for _, want := range []string{"live queries      on", "realtime endpoint rt-oidc (auth oidc_jwt)", "Run gregale data-api live enable shop-data"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, stdout.String())
		}
	}
	if !strings.Contains(warnings, "--auth-mode none") {
		t.Fatalf("no warning about the endpoint's auth mode: %s", warnings)
	}
	if len(fake.writes) != 0 {
		t.Fatalf("status wrote %v", fake.writes)
	}

	fake.hidden = true
	_, stderr, restore = swapIO(t)
	code = cmdDataAPILive([]string{"status", "shop-data"})
	refusal := stderr()
	restore()
	if code == 0 || !strings.Contains(refusal, "not available to this account yet") {
		t.Fatalf("outside the rollout: exit %d, %s", code, refusal)
	}
	if code := cmdDataAPILive([]string{"status", "shop-data", "--no-restart"}); code != 1 {
		t.Fatal("status accepted --no-restart")
	}
}
