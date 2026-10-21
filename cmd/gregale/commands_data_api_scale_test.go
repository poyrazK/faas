// adr: 20261021180534 — engine pool and server-key limits as configure
// settings, and their refusals in diagnostics.
package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// capacityAPI records secret writes and lists the given secrets.
type capacityAPI struct {
	mu      sync.Mutex
	present []api.AppSecretResponse
	writes  []string
}

func newCapacityAPI(t *testing.T, present ...string) *capacityAPI {
	t.Helper()
	c := &capacityAPI{}
	for _, key := range present {
		c.present = append(c.present, api.AppSecretResponse{Key: key, Scope: "default"})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/shop-data/secrets":
			_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Secrets: c.present})
		case strings.HasPrefix(r.URL.Path, "/v1/apps/shop-data/secrets/"):
			key := strings.TrimPrefix(r.URL.Path, "/v1/apps/shop-data/secrets/")
			var value struct {
				Value string `json:"value"`
			}
			_ = json.Unmarshal(body, &value)
			c.writes = append(c.writes, r.Method+" "+key+"="+value.Value)
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "restart"):
			c.writes = append(c.writes, "restart")
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
	return c
}

func configureCapacity(t *testing.T, c *capacityAPI, args ...string) (int, string, []string) {
	t.Helper()
	main.ResetJSONOut(t)
	c.mu.Lock()
	c.writes = nil
	c.mu.Unlock()
	_, stderr, restore := main.SwapIO(t)
	code := main.CmdDataAPIConfigure(append([]string{"shop-data"}, args...))
	errors := stderr()
	restore()
	c.mu.Lock()
	defer c.mu.Unlock()
	return code, errors, append([]string(nil), c.writes...)
}

func TestDataAPIConfigureCapacitySettings(t *testing.T) {
	c := newCapacityAPI(t)
	for _, tc := range []struct {
		args  []string
		write string
	}{
		{[]string{"--pool", "6"}, "PUT DATA_API_POOL=6|restart"},
		{[]string{"--pool", "default", "--no-restart"}, "DELETE DATA_API_POOL="},
		{[]string{"--server-key-concurrency", "3", "--no-restart"}, "PUT DATA_API_SERVER_KEY_CONCURRENCY=3"},
		{[]string{"--server-key-rate-limit", "50,burst=100", "--no-restart"}, "DELETE DATA_API_SERVER_KEY_RATE_LIMIT_PER_SECOND=|DELETE DATA_API_SERVER_KEY_RATE_LIMIT_BURST=|PUT DATA_API_SERVER_KEY_RATE_LIMIT_PER_SECOND=50|PUT DATA_API_SERVER_KEY_RATE_LIMIT_BURST=100"},
		{[]string{"--server-key-rate-limit", "20", "--no-restart"}, "DELETE DATA_API_SERVER_KEY_RATE_LIMIT_PER_SECOND=|DELETE DATA_API_SERVER_KEY_RATE_LIMIT_BURST=|PUT DATA_API_SERVER_KEY_RATE_LIMIT_PER_SECOND=20"},
		{[]string{"--server-key-rate-limit", "off", "--no-restart"}, "DELETE DATA_API_SERVER_KEY_RATE_LIMIT_PER_SECOND=|DELETE DATA_API_SERVER_KEY_RATE_LIMIT_BURST="},
	} {
		code, errors, writes := configureCapacity(t, c, tc.args...)
		if code != 0 || strings.Join(writes, "|") != tc.write {
			t.Fatalf("%v: exit %d %s, writes %v", tc.args, code, errors, writes)
		}
	}
	for _, bad := range [][]string{
		{"--pool", "0"}, {"--pool", "9"}, {"--pool", "02"}, {"--server-key-concurrency", "9"},
		{"--server-key-rate-limit", "0"}, {"--server-key-rate-limit", "5,burst=0"}, {"--server-key-rate-limit", "5,rate=2"}, {"--server-key-rate-limit", "1001"},
	} {
		if code, _, writes := configureCapacity(t, c, bad...); code == 0 || len(writes) != 0 {
			t.Fatalf("%v accepted: writes %v", bad, writes)
		}
	}
}

func TestDataAPIConfigurePoolGuardsTheRefreshReaderAndWarnsAboutUserLimits(t *testing.T) {
	c := newCapacityAPI(t, "DATA_API_COORDINATED_REFRESH", "DATA_API_USER_CONCURRENCY")
	code, errors, writes := configureCapacity(t, c, "--pool", "1", "--no-restart")
	if code == 0 || len(writes) != 0 || !strings.Contains(errors, "coordinated refresh") {
		t.Fatalf("a one-connection pool was accepted beside coordinated refresh: %d %s %v", code, errors, writes)
	}
	code, errors, writes = configureCapacity(t, c, "--pool", "4", "--no-restart")
	if code != 0 || strings.Join(writes, "|") != "PUT DATA_API_POOL=4" || !strings.Contains(errors, "DATA_API_USER_CONCURRENCY") {
		t.Fatalf("pool with user limits: %d %s %v", code, errors, writes)
	}
}

func TestDataAPIServerKeyRefusalsAreDiagnosed(t *testing.T) {
	c := main.NewDataAPIDiagnosticsCollector("", "")
	concurrency := dataAPIDiagnosticFixture(t, 1, "rest", "rejected", 429, 1, "data_api_concurrency_limited")
	c.Observe(strings.TrimSuffix(concurrency, "}") + `,"concurrency_limit":{"reason":"server_key","max_concurrent":3,"active":3,"cooling_down":0,"retry_after_seconds":1}}`)
	rate := dataAPIDiagnosticFixture(t, 2, "rest", "rejected", 429, 1, "data_api_rate_limited")
	c.Observe(strings.TrimSuffix(rate, "}") + `,"rate_limit":{"reason":"server_key","per_second":5,"burst":10,"retry_after_seconds":1}}`)
	c.Finish()
	report := *c.Report()
	if report.Partial || report.Failures.ConcurrencyLimited != 1 || report.Failures.RateLimited != 1 {
		t.Fatalf("server-key refusals were misclassified: %+v", report)
	}
	inconsistent := main.NewDataAPIDiagnosticsCollector("", "")
	inconsistent.Observe(strings.TrimSuffix(concurrency, "}") + `,"concurrency_limit":{"reason":"server_key","max_concurrent":3,"active":2,"cooling_down":1,"retry_after_seconds":1}}`)
	inconsistent.Finish()
	if !(*inconsistent.Report()).Partial {
		t.Fatal("a server-key refusal with free slots was accepted")
	}
}
