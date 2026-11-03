// adr: 20261022121642 — data-api hooks prints the migration and turns
// publication of recorded changes on and off.
package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataAPIHooksSQLRendersTheSchema(t *testing.T) {
	sql, err := main.DataAPIHooksSQL("catalog")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CREATE TABLE catalog.gregale_change_events", "ENABLE ROW LEVEL SECURITY", "CREATE FUNCTION gregale_hooks.capture()", "SECURITY DEFINER",
		"CREATE FUNCTION catalog.gregale_change_claim", "CREATE FUNCTION catalog.gregale_change_settle", "SELECT gregale_hooks.enable('catalog.orders')"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q", want)
		}
	}
	// The gateway routes only functions commented '@gregale:rpc'.
	if strings.Contains(sql, "{{schema}}") || strings.Contains(sql, "COMMENT ON FUNCTION") {
		t.Fatal("unrendered placeholder or an exposed relay function")
	}
	for _, bad := range []string{"", "API", "api;drop", "1api"} {
		if _, err := main.DataAPIHooksSQL(bad); err == nil {
			t.Fatalf("accepted schema %q", bad)
		}
	}
}

// hooksAPI fakes the app, secret and restart routes.
type hooksAPI struct {
	mu      sync.Mutex
	secrets []api.AppSecretResponse
	writes  []string
	bodies  map[string]string
}

func newHooksClient(t *testing.T, fake *hooksAPI, base string) *api.Client {
	t.Helper()
	fake.bodies = map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodGet {
			fake.writes = append(fake.writes, r.Method+" "+r.URL.Path)
			fake.bodies[r.URL.Path] = string(body)
		}
		switch {
		case r.URL.Path == "/v1/apps/shop-data" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "8c0a63d2-5a7e-4b8f-9a55-2f8a3c1d9e01", Slug: "shop-data"})
		case r.URL.Path == "/v1/apps/shop-data/secrets":
			_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Secrets: fake.secrets})
		case strings.HasPrefix(r.URL.Path, "/v1/apps/shop-data/secrets/"):
			key := strings.TrimPrefix(r.URL.Path, "/v1/apps/shop-data/secrets/")
			if r.Method == http.MethodPut {
				fake.secrets = append(fake.secrets, api.AppSecretResponse{Key: key, Scope: "default"})
			} else {
				fake.secrets = nil
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "restart"):
			_, _ = w.Write([]byte(`{"wake_id":"wake-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := api.NewClient(base, "test-token")
	client.HTTPClient().Transport = hooksTransport(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})
	return client
}

type hooksTransport func(*http.Request) (*http.Response, error)

func (f hooksTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDataAPIHooksEnableStoresTheOriginAndReportsTheSource(t *testing.T) {
	fake := &hooksAPI{}
	client := newHooksClient(t, fake, "https://api.gregale.example")
	ctx := context.Background()
	receipt, err := main.EnableDataAPIHooks(ctx, client, "shop-data", api.DefaultEnvScope, true)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Enabled || !receipt.Restarted || receipt.Source != "app.8c0a63d2-5a7e-4b8f-9a55-2f8a3c1d9e01" {
		t.Fatalf("receipt %+v", receipt)
	}
	if got := strings.Join(fake.writes, "|"); got != "PUT /v1/apps/shop-data/secrets/DATA_API_CHANGE_EVENTS_URL|POST /v1/apps/shop-data/restart" {
		t.Fatalf("writes %s", got)
	}
	if !strings.Contains(fake.bodies["/v1/apps/shop-data/secrets/DATA_API_CHANGE_EVENTS_URL"], "https://api.gregale.example/") {
		t.Fatalf("stored %s", fake.bodies["/v1/apps/shop-data/secrets/DATA_API_CHANGE_EVENTS_URL"])
	}
	status, err := main.DataAPIHooksStatus(ctx, client, "shop-data", api.DefaultEnvScope)
	if err != nil || !status.Enabled {
		t.Fatalf("status %+v %v", status, err)
	}
	fake.writes = nil
	off, err := main.DisableDataAPIHooks(ctx, client, "shop-data", api.DefaultEnvScope, false)
	if err != nil || off.Enabled || off.Restarted || len(fake.writes) != 1 || !strings.HasPrefix(fake.writes[0], "DELETE /v1/apps/shop-data/secrets/DATA_API_CHANGE_EVENTS_URL") {
		t.Fatalf("disable %+v %v %v", off, err, fake.writes)
	}
}

func TestDataAPIHooksNeedAnHTTPSPlatform(t *testing.T) {
	fake := &hooksAPI{}
	client := newHooksClient(t, fake, "http://localhost:8080")
	if _, err := main.EnableDataAPIHooks(context.Background(), client, "shop-data", api.DefaultEnvScope, true); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("err %v", err)
	}
	if len(fake.writes) != 0 {
		t.Fatalf("wrote %v", fake.writes)
	}
}

func TestDataAPIHooksUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"publish"}, {"enable"}, {"enable", "Bad Name"}, {"status", "shop-data", "--no-restart"}, {"sql", "extra"}} {
		_, _, restore := main.SwapIO(t)
		code := main.CmdDataAPIHooks(args)
		restore()
		if code == 0 {
			t.Fatalf("%v accepted", args)
		}
	}
}
