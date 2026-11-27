// adr: 20261125231222
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresActivityNeedsConsentAndABoundDatabase(t *testing.T) {
	newFixture := func(capabilities []string) (*management, *[]string) {
		t.Helper()
		var requested []string
		base := &queryFixture{operationFixture: &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}}
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/activity") {
				requested = append(requested, r.URL.Path+"?"+r.URL.RawQuery)
				id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/postgres/databases/"), "/activity")
				_ = json.NewEncoder(w).Encode(api.ManagedPostgresActivity{DatabaseID: id, DatabaseName: "orders", Since: time.Now().Add(-time.Hour), Until: time.Now(), Starts: 1,
					Causes: []api.ManagedPostgresActivityCause{}, ComputeStarts: []api.ManagedPostgresActivityComputeStart{}, Hints: []api.ManagedPostgresActivityHint{}})
				return
			}
			base.serve(w, r)
		}))
		t.Cleanup(backend.Close)
		_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, agentKey: true, Capabilities: capabilities, Toolsets: []string{ToolsetAll}})
		if err != nil {
			t.Fatal(err)
		}
		return m, &requested
	}
	if m, _ := newFixture(nil); m.toolNames["gregale_get_postgres_activity"] {
		t.Fatal("activity tool listed without postgres:read")
	}
	m, requested := newFixture([]string{api.ScopeManagedPostgresRead})
	if !m.toolNames["gregale_get_postgres_activity"] {
		t.Fatal("activity tool missing with postgres:read")
	}
	out, err := m.getPostgresActivity(t.Context(), postgresActivityArgs{App: "alpha", Database: "orders", Since: "7d"})
	if err != nil {
		t.Fatal(err)
	}
	if out.(api.ManagedPostgresActivity).DatabaseID != "db-1" || len(*requested) != 1 || (*requested)[0] != "/v1/postgres/databases/db-1/activity?since=7d" {
		t.Fatalf("out=%+v requested=%v", out, *requested)
	}
	if _, err := m.getPostgresActivity(t.Context(), postgresActivityArgs{App: "alpha", Database: "someone-elses-db"}); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("unbound database err = %v", err)
	}
	if len(*requested) != 1 {
		t.Fatal("an unbound database's activity was read")
	}
}
