// adr: 20261116213000
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func postgresRolesVisible() bool {
	for _, command := range main.VisibleCliCommands() {
		if command.Name != "postgres" {
			continue
		}
		for _, sub := range command.Subcommands {
			if sub.Name == "roles" {
				return true
			}
		}
	}
	return false
}

// Custom roles are dark: the CLI hides `postgres roles` from discovery and
// dispatch until FAAS_POSTGRES_CUSTOM_ROLES_ENABLED is set.
func TestPostgresRolesCLIHiddenUntilEnabled(t *testing.T) {
	t.Setenv("FAAS_POSTGRES_CUSTOM_ROLES_ENABLED", "")
	if postgresRolesVisible() {
		t.Fatal("hidden postgres roles surfaced")
	}
	if code := main.CmdPostgres([]string{"roles", "list", "windmill-db"}); code == 0 {
		t.Fatal("hidden postgres roles dispatched")
	}
	t.Setenv("FAAS_POSTGRES_CUSTOM_ROLES_ENABLED", "1")
	if !postgresRolesVisible() {
		t.Fatal("enabled postgres roles hidden")
	}
}

func TestPostgresRolesCLIGrantKeepsTheDeclaration(t *testing.T) {
	var put api.PutManagedPostgresCustomRoleRequest
	var paths []string
	databaseID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresCustomRoleList{Items: []api.ManagedPostgresCustomRole{{
				DatabaseID: databaseID, Name: "windmill_admin", BypassRLS: true, GrantedTo: []string{"migration"}, MemberOf: []string{"windmill_user"}, State: "ready"}}})
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(&put); err != nil {
				t.Errorf("decode: %v", err)
			}
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresCustomRole{DatabaseID: databaseID, Name: "windmill_admin", BypassRLS: put.BypassRLS,
				GrantedTo: put.GrantedTo, MemberOf: put.MemberOf, State: "ready"})
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	t.Setenv("FAAS_POSTGRES_CUSTOM_ROLES_ENABLED", "1")
	var out bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = old })

	if code := main.CmdPostgres([]string{"roles", "grant", databaseID, "windmill_admin", "--to", "read_write"}); code != 0 {
		t.Fatalf("grant exit = %d", code)
	}
	if !put.BypassRLS || len(put.MemberOf) != 1 || strings.Join(put.GrantedTo, ",") != "migration,read_write" {
		t.Fatalf("grant changed the declaration: %+v", put)
	}
	if !strings.Contains(out.String(), "SET ROLE windmill_admin") {
		t.Fatalf("output: %q", out.String())
	}
	if got := strings.Join(paths, " | "); !strings.Contains(got, "PUT /v1/postgres/databases/"+databaseID+"/roles/windmill_admin") {
		t.Fatalf("paths: %s", got)
	}
}
