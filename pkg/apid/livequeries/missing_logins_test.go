package livequeries_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// missingLogins is an in-memory MissingLogins: databases with a live key and
// no login, and the logins it stored.
type missingLogins struct {
	refs   []livekeys.LoginRef
	stored map[string]*livekeys.Login
}

func (m *missingLogins) DatabasesWithoutLogin(context.Context, int) ([]livekeys.LoginRef, error) {
	var out []livekeys.LoginRef
	for _, ref := range m.refs {
		if m.stored[ref.DatabaseID] == nil {
			out = append(out, ref)
		}
	}
	return out, nil
}

func (m *missingLogins) IssueMissingLogin(ctx context.Context, _, databaseID string, issue func(context.Context) (*livekeys.Login, error)) (bool, error) {
	if m.stored[databaseID] != nil {
		return false, nil
	}
	login, err := issue(ctx)
	if err != nil || login == nil {
		return false, err
	}
	m.stored[databaseID] = login
	return true, nil
}

// The operator re-seal drops legacy live logins; apid issues each database
// left with a live key and no login a new one from the provider, sealed bound
// to that database, and leaves one it cannot look up for the next pass.
func TestIssueMissingLoginsBindsANewLoginToItsDatabase(t *testing.T) {
	h := newHarness(t)
	h.svc.Log = slog.New(slog.DiscardHandler)
	material := managedpostgres.CredentialMaterial{ProviderIdentityID: "gregale_live_ab", Username: "gregale_live_ab", Password: "fresh",
		Database: "neondb", TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: "ep-1.neon.tech", Port: 5432}}}
	h.db.issuer = func(string) (managedpostgres.LiveLogin, error) {
		return managedpostgres.LiveLogin{CredentialMaterial: material}, nil
	}
	store := &missingLogins{
		refs:   []livekeys.LoginRef{{AccountID: "acct", DatabaseID: "db-1"}, {AccountID: "acct", DatabaseID: "db-gone"}},
		stored: map[string]*livekeys.Login{},
	}
	issued, err := h.svc.IssueMissingLogins(t.Context(), store)
	if err != nil || issued != 1 {
		t.Fatalf("issued %d, %v; want one login", issued, err)
	}
	login := store.stored["db-1"]
	if login == nil || login.Slot != "gregale_live_db_1" || login.Recipient != h.identity.Recipient().String() {
		t.Fatalf("stored login %+v", login)
	}
	namespace, plain, err := secretbox.OpenBytes(h.identity, login.Sealed)
	if err != nil || namespace != livekeys.LoginSealNamespaceFor("db-1") || !strings.Contains(string(plain), ":fresh@ep-1.neon.tech") {
		t.Fatalf("the new login opens to %q under %q (%v); want the provider's login bound to db-1", plain, namespace, err)
	}
	if store.stored["db-gone"] != nil {
		t.Fatal("a database that could not be looked up was given a login")
	}
	if len(h.audits) != 1 || h.audits[0] != "managed_postgres.live.login_issued" {
		t.Fatalf("audits %v", h.audits)
	}
	if again, err := h.svc.IssueMissingLogins(t.Context(), store); err != nil || again != 0 {
		t.Fatalf("a second pass issued %d, %v; db-1 has its login", again, err)
	}
}
