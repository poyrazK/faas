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

// Custom roles are beta (ADR-20261125232048): `postgres roles` is in the
// CLI's discovery without any switch.
func TestPostgresRolesCLIVisible(t *testing.T) {
	if !postgresRolesVisible() {
		t.Fatal("postgres roles hidden")
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

// Amendment 2: grant without --default leaves default_for out (the server
// keeps it); --default and create --default-for send it.
func TestPostgresRolesCLIDefaultFor(t *testing.T) {
	var bodies []map[string]any
	databaseID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresCustomRoleList{Items: []api.ManagedPostgresCustomRole{{
				DatabaseID: databaseID, Name: "windmill_admin", BypassRLS: true, GrantedTo: []string{"migration"}, State: "ready"}}})
		case http.MethodPut:
			body := map[string]any{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			bodies = append(bodies, body)
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresCustomRole{DatabaseID: databaseID, Name: "windmill_admin", State: "ready",
				GrantedTo: []string{"migration", "read_write"}, DefaultFor: []string{"read_write"}})
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	var out bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = old })

	if code := main.CmdPostgres([]string{"roles", "grant", databaseID, "windmill_admin", "--to", "read_write"}); code != 0 {
		t.Fatalf("grant exit = %d", code)
	}
	if _, sent := bodies[0]["default_for"]; sent {
		t.Fatalf("plain grant sent default_for: %v", bodies[0])
	}
	if code := main.CmdPostgres([]string{"roles", "grant", databaseID, "windmill_admin", "--to", "read_write", "--default"}); code != 0 {
		t.Fatalf("grant --default exit = %d", code)
	}
	if got, _ := json.Marshal(bodies[1]["default_for"]); string(got) != `["read_write"]` {
		t.Fatalf("grant --default sent default_for %s", got)
	}
	if !strings.Contains(out.String(), "start every session as windmill_admin") {
		t.Fatalf("output: %q", out.String())
	}
	if code := main.CmdPostgres([]string{"roles", "create", databaseID, "windmill_admin", "--bypass-rls", "--default-for", "read_write"}); code != 0 {
		t.Fatalf("create --default-for exit = %d", code)
	}
	if got, _ := json.Marshal(bodies[2]["default_for"]); string(got) != `["read_write"]` {
		t.Fatalf("create --default-for sent %s", got)
	}
	// Refused locally: not granted, or migration.
	for _, args := range [][]string{
		{"roles", "create", databaseID, "windmill_admin", "--grant-to", "migration", "--default-for", "read_write"},
		{"roles", "create", databaseID, "windmill_admin", "--default-for", "migration"},
	} {
		if code := main.CmdPostgres(args); code == 0 {
			t.Fatalf("%v accepted", args)
		}
	}
	if len(bodies) != 3 {
		t.Fatalf("refused commands reached the API: %d PUTs", len(bodies))
	}
}
