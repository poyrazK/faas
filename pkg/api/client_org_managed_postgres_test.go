// adr: 202610171906

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrgPostgresClientKeepsTenantInPath(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	c := NewClient(server.URL, "token")
	ctx := context.Background()
	calls := []struct {
		path string
		call func() error
	}{
		{"/v1/orgs/my%20company/postgres/databases", func() error { _, err := c.ListOrgManagedPostgresDatabases(ctx, "my company"); return err }},
		{"/v1/orgs/my%20company/postgres/databases/database%2Fid/bindings", func() error {
			_, err := c.CreateOrgManagedPostgresBinding(ctx, "my company", "database/id", CreateManagedPostgresBindingRequest{})
			return err
		}},
		{"/v1/orgs/my%20company/postgres/usage", func() error { _, err := c.GetOrgManagedPostgresUsage(ctx, "my company"); return err }},
	}
	for i, tc := range calls {
		if err := tc.call(); err != nil {
			t.Fatal(err)
		}
		if paths[i] != tc.path {
			t.Fatalf("request escaped organization path: %s; want %s", paths[i], tc.path)
		}
	}
}
