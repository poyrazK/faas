// adr: 20261117215858
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// queryFixture serves one database bound to app alpha and records queries.
type queryFixture struct {
	*operationFixture
	qmu     sync.Mutex
	queries []api.ManagedPostgresQueryRequest
	ids     []string
	// large makes the column catalog query come back truncated.
	large bool
}

func newQueryFixture(t *testing.T, capabilities []string) (*management, *queryFixture) {
	t.Helper()
	f := &queryFixture{operationFixture: &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}}
	backend := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(backend.Close)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, agentKey: true, Capabilities: capabilities, Toolsets: []string{ToolsetAll}})
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

func (f *queryFixture) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/apps/alpha/resource-inventory":
		_ = json.NewEncoder(w).Encode(api.AppResourceInventoryResponse{SchemaVersion: 1, AppID: f.base.app.ID, Scope: api.DefaultEnvScope, Kind: "databases", Limit: 100, Offset: 0, Total: 1, GeneratedAt: time.Now(),
			Items: []api.AppResourceInventoryItem{{ID: "db-1", AppID: f.base.app.ID, Scope: api.DefaultEnvScope, Name: "orders", Region: "test", State: "ready", PostgresMajor: 17, Binding: &api.AppResourceInventoryBinding{ID: "b-1", DatabaseID: "db-1", AppID: f.base.app.ID, Scope: api.DefaultEnvScope, Access: "read_write", State: "ready", EnvironmentKey: "DATABASE_URL"}}}})
	case strings.HasPrefix(r.URL.Path, "/v1/postgres/databases/") && strings.HasSuffix(r.URL.Path, "/query"):
		var req api.ManagedPostgresQueryRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/postgres/databases/"), "/query")
		f.qmu.Lock()
		f.queries = append(f.queries, req)
		f.ids = append(f.ids, id)
		f.qmu.Unlock()
		s := func(v string) *string { return &v }
		out := api.ManagedPostgresQueryResponse{DatabaseID: id, Columns: []api.ManagedPostgresQueryColumn{{Name: "id", Type: "int4"}}, Rows: [][]*string{{s("1")}}, RowCount: 1}
		switch {
		case strings.Contains(req.SQL, "count(a.attnum)"):
			out.Rows = [][]*string{{s("orders"), s("table"), s("2")}, {s("users"), s("table"), s("9")}}
		case strings.Contains(req.SQL, "pg_attribute"):
			out.Rows = [][]*string{{s("orders"), s("table"), s("id"), s("integer"), s("t"), s("t")}, {s("orders"), s("table"), s("note"), s("text"), s("f"), s("f")}}
			out.Truncated = f.large && !strings.Contains(req.SQL, "c.relname IN")
		case strings.Contains(req.SQL, "pg_indexes"):
			out.Rows = [][]*string{{s("orders"), s("orders_pkey"), s("CREATE UNIQUE INDEX orders_pkey ON public.orders USING btree (id)")}}
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		f.serveHTTP(w, r)
	}
}

func TestQueryPostgresNeedsConsentAndABoundDatabase(t *testing.T) {
	m, _ := newQueryFixture(t, nil)
	if m.toolNames["gregale_query_postgres"] {
		t.Fatal("query tool listed without data:read")
	}
	m, f := newQueryFixture(t, []string{api.ScopeDataRead})
	if !m.toolNames["gregale_query_postgres"] || !m.toolNames["gregale_get_postgres_schema"] {
		t.Fatal("query tools missing with data:read")
	}
	out, err := m.queryPostgres(t.Context(), postgresQueryArgs{App: "alpha", Database: "orders", SQL: "select id from orders", MaxRows: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out.(api.ManagedPostgresQueryResponse).RowCount != 1 || f.ids[0] != "db-1" || f.queries[0].MaxRows != 5 {
		t.Fatalf("out=%+v ids=%v", out, f.ids)
	}
	if _, err := m.queryPostgres(t.Context(), postgresQueryArgs{App: "alpha", Database: "someone-elses-db", SQL: "select 1"}); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("unbound database err = %v", err)
	}
	if len(f.ids) != 1 {
		t.Fatal("unbound database was queried")
	}
}

func TestPostgresSchemaGroupsColumnsAndIndexes(t *testing.T) {
	m, _ := newQueryFixture(t, []string{api.ScopeDataRead})
	out, err := m.getPostgresSchema(t.Context(), postgresSchemaArgs{App: "alpha", Database: "db-1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	for _, want := range []string{`"name":"orders"`, `"primary_key":true`, `"orders_pkey"`, `"schema":"public"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("schema %s lacks %s", raw, want)
		}
	}
	if _, err := m.getPostgresSchema(t.Context(), postgresSchemaArgs{App: "alpha", Database: "db-1", Schema: "public'; drop"}); err == nil {
		t.Fatal("schema name injection accepted")
	}
}

// A 257-table schema exceeded the MCP result bound, so get_postgres_schema
// failed with response_too_large. A large schema is listed by name with
// column counts, and `tables` describes the named ones in full.
func TestPostgresSchemaListsALargeSchemaByName(t *testing.T) {
	m, f := newQueryFixture(t, []string{api.ScopeDataRead})
	f.large = true
	out, err := m.getPostgresSchema(t.Context(), postgresSchemaArgs{App: "alpha", Database: "db-1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	for _, want := range []string{`"details_omitted":true`, `"name":"users"`, `"column_count":9`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("listing %s lacks %s", raw, want)
		}
	}
	if strings.Contains(string(raw), `"columns"`) {
		t.Fatalf("listing carries column detail: %s", raw)
	}
	out, err = m.getPostgresSchema(t.Context(), postgresSchemaArgs{App: "alpha", Database: "db-1", Tables: []string{"orders"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(out)
	if !strings.Contains(string(raw), `"primary_key":true`) || strings.Contains(string(raw), "details_omitted") {
		t.Fatalf("named tables = %s", raw)
	}
	if last := f.queries[len(f.queries)-1].SQL; !strings.Contains(last, "tablename IN ('orders')") {
		t.Fatalf("index query not filtered: %s", last)
	}
	for _, bad := range [][]string{{"orders'; drop"}, make([]string, 21)} {
		if _, err := m.getPostgresSchema(t.Context(), postgresSchemaArgs{App: "alpha", Database: "db-1", Tables: bad}); err == nil {
			t.Fatalf("accepted tables %q", bad)
		}
	}
}
