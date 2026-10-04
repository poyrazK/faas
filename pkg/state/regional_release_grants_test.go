package state

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func grantFixture(t *testing.T, app App, cell string) RegionalReleaseGrant {
	t.Helper()
	_, hash, err := api.GenerateRegionalReleaseToken()
	if err != nil {
		t.Fatal(err)
	}
	return RegionalReleaseGrant{ID: uuid.NewString(), CellID: cell, AppID: app.ID, AccountID: app.AccountID, OrgID: app.OrgID, GlobalAppID: uuid.NewString(), Hash: hash, ExpiresAt: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)}
}
func runRegionalGrantContract(t *testing.T, s Store, grants RegionalReleaseGrantStore, cell string, acct Account) {
	app := releaseTestApp(t, s, acct)
	g := grantFixture(t, app, cell)
	first, err := grants.PutRegionalReleaseGrant(t.Context(), g)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := grants.PutRegionalReleaseGrant(t.Context(), g)
	if err != nil || !first.matches(retry) || !first.CreatedAt.Equal(retry.CreatedAt) {
		t.Fatalf("replay: %+v %v", retry, err)
	}
	for _, change := range []func(*RegionalReleaseGrant){func(g *RegionalReleaseGrant) { g.GlobalAppID = uuid.NewString() }, func(g *RegionalReleaseGrant) { g.Hash = api.HashAPIKey("other") }, func(g *RegionalReleaseGrant) { g.ExpiresAt = g.ExpiresAt.Add(time.Second) }} {
		other := g
		change(&other)
		if _, err := grants.PutRegionalReleaseGrant(t.Context(), other); !errors.Is(err, ErrConflict) {
			t.Fatalf("immutable input: %v", err)
		}
	}
	owner, key, err := grants.AuthenticateRegionalReleaseGrant(t.Context(), g.Hash)
	if err != nil || owner.ID != acct.ID || key.AppID != app.ID || key.RegionalReleaseGrantID != g.ID || key.RegionalReleaseGlobalAppID != g.GlobalAppID || len(key.Scopes) != 1 || key.Scopes[0] != api.ScopeRegionalImageRelease {
		t.Fatalf("principal: %+v %v", key, err)
	}
	if _, err := grants.GetRegionalReleaseGrant(t.Context(), uuid.NewString(), cell, app.ID, g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign owner", err)
	}
	if _, err := grants.GetRegionalReleaseGrant(t.Context(), acct.ID, uuid.NewString(), app.ID, g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign cell", err)
	}
	revoked, err := grants.RevokeRegionalReleaseGrant(t.Context(), acct.ID, cell, app.ID, g.ID)
	if err != nil || revoked.Status != "revoked" || revoked.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v", revoked, err)
	}
	again, err := grants.PutRegionalReleaseGrant(t.Context(), g)
	if err != nil || again.Status != "revoked" || !again.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Fatalf("revived by retry: %+v %v", again, err)
	}
	if _, _, err := grants.AuthenticateRegionalReleaseGrant(t.Context(), g.Hash); !errors.Is(err, ErrAPIKeyRevoked) {
		t.Fatal("revoked auth", err)
	}
	intent, dep := releaseTestIntent(cell, app)
	intent.GlobalAppID, intent.Generation, intent.GrantID = g.GlobalAppID, 1, g.ID
	releases := s.(RegionalImageReleaseStore)
	if _, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrAPIKeyRevoked) {
		t.Fatal("revoked acceptance", err)
	}
	if _, err := releases.RegionalImageReleaseReceipt(t.Context(), cell, app.ID, intent.ReleaseID); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked grant left receipt", err)
	}
	// Registration canonicalizes sub-microsecond timestamp precision in both stores.
	nano := grantFixture(t, app, cell)
	nano.ExpiresAt = nano.ExpiresAt.Add(123 * time.Nanosecond)
	normalized, err := grants.PutRegionalReleaseGrant(t.Context(), nano)
	if err != nil {
		t.Fatal(err)
	}
	again, err = grants.PutRegionalReleaseGrant(t.Context(), nano)
	if err != nil || !again.ExpiresAt.Equal(normalized.ExpiresAt) {
		t.Fatal("timestamp replay", err)
	}
}
func runRegionalGrantBound(t *testing.T, s Store, grants RegionalReleaseGrantStore, cell string, acct Account) {
	app := releaseTestApp(t, s, acct)
	const requests = 40
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		g := grantFixture(t, app, cell)
		wg.Go(func() { _, err := grants.PutRegionalReleaseGrant(t.Context(), g); errs <- err })
	}
	wg.Wait()
	close(errs)
	accepted := 0
	for err := range errs {
		if err == nil {
			accepted++
			continue
		}
		var bound *RegionalReleaseGrantLimitError
		if !errors.As(err, &bound) || bound.Limit != api.RegionalReleaseGrantsPerAppMax || bound.Observed != api.RegionalReleaseGrantsPerAppMax+1 {
			t.Fatalf("bound result: %v %+v", err, bound)
		}
	}
	if accepted != api.RegionalReleaseGrantsPerAppMax {
		t.Fatalf("accepted %d", accepted)
	}
}
func TestRegionalReleaseGrantMemoryContract(t *testing.T) {
	s := NewMemStore()
	acct, err := s.CreateAccount(t.Context(), "grant@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runRegionalGrantContract(t, s, s, uuid.NewString(), acct)
}
func TestRegionalReleaseGrantMemoryBound(t *testing.T) {
	s := NewMemStore()
	acct, err := s.CreateAccount(t.Context(), "bound@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runRegionalGrantBound(t, s, s, uuid.NewString(), acct)
}
func TestRegionalReleaseTokenFormat(t *testing.T) {
	token, hash, err := api.GenerateRegionalReleaseToken()
	if err != nil || len(hash) != 32 || !api.ValidRegionalReleaseTokenFormat(token) || api.ValidAPIKeyFormat(token) || api.ValidDeployTokenFormat(token) {
		t.Fatalf("token format: %v", err)
	}
	for _, bad := range []string{token + "a", api.RegionalReleaseTokenPrefix + strings.Repeat("G", 64), api.RegionalReleaseTokenPrefix + strings.Repeat("A", 64)} {
		if api.ValidRegionalReleaseTokenFormat(bad) {
			t.Fatal("invalid token accepted")
		}
	}
	if api.IsValidScope(api.ScopeRegionalImageRelease) {
		t.Fatal("general API-key issuance can mint delegated scope")
	}
}
