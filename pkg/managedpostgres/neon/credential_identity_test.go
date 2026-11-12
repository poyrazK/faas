// adr: 461 — verified branch backfills preserve recoverable credential retirement.
package neon

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestCredentialRevocationAfterDataPinBackfill(t *testing.T) {
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly, managedpostgres.CredentialMigration} {
		t.Run(string(access), func(t *testing.T) {
			f := newCredentialFixture(t)
			deleted := map[string]int{}
			var deletedMu sync.Mutex
			p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/connection_uri"):
					if r.URL.Query().Get("branch_id") != "br-original-123" {
						t.Error("revocation followed the default branch")
					}
					writeResponse(t, w, 200, map[string]any{"uri": "postgres://" + ownerLogin + ":test@test.invalid/" + f.config.Database + "?sslmode=require"})
				case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v2/projects/quiet-river-12345678/branches/br-original-123/roles/"):
					deletedMu.Lock()
					deleted[strings.TrimPrefix(r.URL.Path, "/api/v2/projects/quiet-river-12345678/branches/br-original-123/roles/")]++
					deletedMu.Unlock()
					w.WriteHeader(http.StatusNotFound)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			p.databaseName = f.config.Database
			p.roles = f.manager
			old := managedpostgres.CredentialRequest{ProviderResourceID: "quiet-river-12345678", IdentityKey: "old-generation", IdempotencyKey: "credentials-old", Access: access}
			f.runtime = p.credentialRole(old)
			if err := f.manager.Ensure(t.Context(), f.material, f.runtime); err != nil {
				t.Fatal(err)
			}
			existing := f.connect(t, f.runtime.name)
			// An API administrator from an earlier preview can coexist with
			// the SQL login. Both historical naming schemes must be retired.
			executeSQL(t, f.admin, "CREATE ROLE "+roleIdentifier(f.runtime.legacy)+" LOGIN CREATEROLE")
			legacy := f.connect(t, f.runtime.legacy)
			pinned := old
			pinned.ProviderResourceID += "/br-original-123"
			replacement := pinned
			replacement.IdentityKey = "new-generation"
			next := p.credentialRole(replacement)
			f.extraRoles = append(f.extraRoles, next.name)
			if err := f.manager.Ensure(t.Context(), f.material, next); err != nil {
				t.Fatal(err)
			}
			serving := f.connect(t, next.name)
			for range 2 {
				if err := p.RevokeCredentials(t.Context(), pinned); err != nil {
					t.Fatal("revocation/replay", err)
				}
			}
			var login bool
			if err := f.admin.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname IN ($1,$2) AND rolcanlogin)`, f.runtime.name, f.runtime.legacy).Scan(&login); err != nil {
				t.Fatal(err)
			}
			if login {
				t.Fatal("backfill left an original credential able to log in")
			}
			for _, conn := range []*pgx.Conn{existing, legacy} {
				if _, err := conn.Exec(t.Context(), "SELECT 1"); err == nil {
					t.Fatal("original credential session survived")
				}
			}
			if _, err := serving.Exec(t.Context(), "SELECT 1"); err != nil {
				t.Fatal("new generation was retired", err)
			}
			deletedMu.Lock()
			defer deletedMu.Unlock()
			for _, name := range []string{f.runtime.legacy, p.credentialRole(pinned).legacy} {
				if deleted[name] != 2 {
					t.Fatalf("API alias was not retired on replay: calls=%d", deleted[name])
				}
			}
		})
	}
}

func TestCredentialBackfillRevocationRetriesLegacyAPIFailure(t *testing.T) {
	var failed atomic.Bool
	var legacy string
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/connection_uri"):
			if r.URL.Query().Get("branch_id") != "br-original-123" {
				t.Error("unqualified owner selector")
			}
			writeResponse(t, w, 200, map[string]any{"uri": "postgres://" + ownerLogin + ":test@test.invalid/gregale?sslmode=require"})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v2/projects/quiet-river-12345678/branches/br-original-123/roles/"):
			if strings.HasSuffix(r.URL.Path, "/"+legacy) && !failed.Swap(true) {
				writeResponse(t, w, http.StatusServiceUnavailable, map[string]any{"message": "retry"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	request := managedpostgres.CredentialRequest{ProviderResourceID: "quiet-river-12345678/br-original-123", IdentityKey: "binding", IdempotencyKey: "credentials-binding", Access: managedpostgres.CredentialReadWrite}
	alias := request
	alias.ProviderResourceID = "quiet-river-12345678"
	legacy = p.credentialRole(alias).legacy
	if err := p.RevokeCredentials(t.Context(), request); !errors.Is(err, managedpostgres.ErrUnavailable) || !failed.Load() {
		t.Fatalf("legacy API failure was hidden: %v", err)
	}
	if err := p.RevokeCredentials(t.Context(), request); err != nil {
		t.Fatal("legacy API retirement retry", err)
	}
}
