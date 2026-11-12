package livequeries_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

type failingLoginDatabases struct {
	base    *databases
	lookups map[string]int
}

func (d *failingLoginDatabases) Get(_ context.Context, account, id string) (managedpostgres.Database, error) {
	d.lookups[id]++
	db := d.base.db
	db.AccountID, db.ID = account, id
	return db, nil
}
func (d *failingLoginDatabases) LiveHub(db managedpostgres.Database) (string, error) {
	return d.base.LiveHub(db)
}
func (d *failingLoginDatabases) IssueLiveLogin(ctx context.Context, db managedpostgres.Database, slot string) (managedpostgres.LiveLogin, bool, error) {
	if db.ID != d.base.db.ID {
		return managedpostgres.LiveLogin{}, false, managedpostgres.ErrUnavailable
	}
	return d.base.IssueLiveLogin(ctx, db, slot)
}
func (d *failingLoginDatabases) RevokeLiveLogin(ctx context.Context, db managedpostgres.Database, slot string) error {
	return d.base.RevokeLiveLogin(ctx, db, slot)
}

func TestMissingLoginsReachesLaterHealthyDatabase(t *testing.T) {
	h := newHarness(t)
	h.svc.Log = slog.New(slog.DiscardHandler)
	h.db.db.ID = "00000000-0000-0000-0000-000000000021"
	h.db.issuer = func(string) (managedpostgres.LiveLogin, error) {
		return managedpostgres.LiveLogin{CredentialMaterial: managedpostgres.CredentialMaterial{ProviderIdentityID: "gregale_live_test", Username: "gregale_live_test", Password: "fresh", Database: "neondb", TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: "test.invalid", Port: 5432}}}}, nil
	}
	allDatabases := &failingLoginDatabases{base: h.db, lookups: map[string]int{}}
	h.svc.Databases = allDatabases
	store := &missingLogins{stored: map[string]*livekeys.Login{}}
	// The first twenty eligible catalogue rows cannot be issued by this apid.
	// The twenty-first is healthy and has the same queue shape.
	for n := 1; n <= 21; n++ {
		store.refs = append(store.refs, livekeys.LoginRef{AccountID: "acct", DatabaseID: fmt.Sprintf("00000000-0000-0000-0000-%012d", n)})
	}
	for pass := 1; pass <= 3; pass++ {
		issued, err := h.svc.IssueMissingLogins(t.Context(), store)
		t.Logf("pass %d: issued=%d error=%v", pass, issued, err)
		if err != nil {
			t.Fatal(err)
		}
	}
	tailAttempts := allDatabases.lookups[h.db.db.ID]
	// A control pass verifies that the queued tail can really issue a login.
	control := &missingLogins{refs: []livekeys.LoginRef{store.refs[20]}, stored: map[string]*livekeys.Login{}}
	issued, err := h.svc.IssueMissingLogins(t.Context(), control)
	if err != nil || issued != 1 {
		t.Fatalf("healthy tail control: issued=%d error=%v", issued, err)
	}
	t.Logf("healthy tail succeeds in isolation; attempts in full queue=%d", tailAttempts)
	if tailAttempts != 1 || store.stored[h.db.db.ID] == nil {
		t.Fatal("later healthy database was not repaired exactly once")
	}
}
