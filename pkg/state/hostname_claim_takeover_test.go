package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClaimTakeoverTokenBindsKindAccountAndName(t *testing.T) {
	base := ClaimTakeoverToken(ClaimKindCustomDomain, "acct-1", "shop.example.com")
	if len(base) != 32 {
		t.Fatalf("token %q has %d characters, want 32", base, len(base))
	}
	if again := ClaimTakeoverToken(ClaimKindCustomDomain, "acct-1", " Shop.Example.com "); again != base {
		t.Fatalf("token is not stable across case and spacing: %q vs %q", again, base)
	}
	for name, other := range map[string]string{
		"account":  ClaimTakeoverToken(ClaimKindCustomDomain, "acct-2", "shop.example.com"),
		"hostname": ClaimTakeoverToken(ClaimKindCustomDomain, "acct-1", "api.example.com"),
		"kind":     ClaimTakeoverToken(ClaimKindTenantHostname, "acct-1", "shop.example.com"),
	} {
		if other == base {
			t.Errorf("a different %s yields the same token", name)
		}
	}
}

func claimTakeoverFixture(t *testing.T) (*MemStore, App, App, App) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	holder, _ := m.CreateAccount(ctx, "claim-holder@example.test", api.PlanScale)
	claimant, _ := m.CreateAccount(ctx, "claim-claimant@example.test", api.PlanScale)
	holderApp, _ := m.CreateApp(ctx, App{AccountID: holder.ID, Slug: "claim-holder"})
	holderOther, _ := m.CreateApp(ctx, App{AccountID: holder.ID, Slug: "claim-holder-two"})
	claimantApp, _ := m.CreateApp(ctx, App{AccountID: claimant.ID, Slug: "claim-claimant"})
	return m, holderApp, holderOther, claimantApp
}

func TestMemStoreTakeOverCustomDomainClaim(t *testing.T) {
	ctx := context.Background()
	m, holderApp, holderOther, claimantApp := claimTakeoverFixture(t)
	const domain = "squatted.example.test"
	if _, err := m.CreateCustomDomainIfUnderQuota(ctx, domain, holderApp.ID, "holder-token", 10, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateCustomDomainIfUnderQuota(ctx, domain, claimantApp.ID, "plain", 10, 10); !errors.Is(err, ErrConflict) {
		t.Fatalf("plain create over a live claim = %v, want ErrConflict", err)
	}
	if _, err := m.TakeOverCustomDomainClaim(ctx, domain, holderOther.ID, "", "own", 10, 10); !errors.Is(err, ErrConflict) {
		t.Fatalf("takeover by the holder's own account = %v, want ErrConflict", err)
	}
	if _, err := m.TakeOverCustomDomainClaim(ctx, domain, claimantApp.ID, "", "proof", 0, 10); !errors.Is(err, ErrCustomDomainQuotaExceeded) {
		t.Fatalf("takeover past the claimant's quota = %v, want the quota error", err)
	}
	got, err := m.TakeOverCustomDomainClaim(ctx, domain, claimantApp.ID, "", "proof", 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.AppID != claimantApp.ID || got.ChallengeToken != "proof" || got.Verified() || !got.VerificationExpiresAt.After(time.Now()) {
		t.Fatalf("taken-over row = %+v; want a fresh pending claim for the claimant", got)
	}
	if matched, err := m.MarkDomainVerifiedIfChallenge(ctx, domain, "holder-token"); err != nil || matched {
		t.Fatalf("the old holder's proof matched=%v err=%v; want it void", matched, err)
	}
	if matched, err := m.MarkDomainVerifiedIfChallenge(ctx, domain, "proof"); err != nil || !matched {
		t.Fatalf("the claimant's proof matched=%v err=%v; want verification", matched, err)
	}
	if _, err := m.TakeOverCustomDomainClaim(ctx, domain, holderApp.ID, "", "back", 10, 10); !errors.Is(err, ErrConflict) {
		t.Fatalf("takeover of a verified domain = %v, want ErrConflict", err)
	}
}

func TestMemStoreTenantHostnamePendingCapAndTakeover(t *testing.T) {
	ctx := context.Background()
	m, holderApp, _, claimantApp := claimTakeoverFixture(t)
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10, TenantHostnamesPendingPerSurface: 2}
	surface := func(app App, name string) TenantSurface {
		s, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{AccountID: app.AccountID, AppID: app.ID, Name: name}, lim)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	holder, holderTwo, claimant := surface(holderApp, "holder"), surface(holderApp, "holder-two"), surface(claimantApp, "claimant")
	add := func(s TenantSurface, host string) error {
		_, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{SurfaceID: s.ID, Hostname: host, ChallengeToken: "tok-" + host}, lim)
		return err
	}
	for _, host := range []string{"a.squat.test", "b.squat.test"} {
		if err := add(holder, host); err != nil {
			t.Fatal(err)
		}
	}
	var qe *TenantHostnameQuotaError
	if err := add(holder, "c.squat.test"); !errors.As(err, &qe) || !qe.Pending || qe.Limit != 2 || qe.Observed != 2 {
		t.Fatalf("third pending hostname = %v, want the pending cap (limit 2, observed 2)", err)
	}
	if ok, err := m.MarkTenantHostnameVerifiedIfChallenge(ctx, "a.squat.test", "tok-a.squat.test"); err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	if err := add(holder, "c.squat.test"); err != nil {
		t.Fatalf("a verified hostname frees a pending slot: %v", err)
	}

	takeOver := func(s TenantSurface, host string) (TenantHostname, error) {
		return m.TakeOverTenantHostnameClaim(ctx, CreateTenantHostnameParams{SurfaceID: s.ID, Hostname: host, ChallengeToken: "proof"}, lim)
	}
	if _, err := takeOver(claimant, "a.squat.test"); !errors.Is(err, ErrConflict) {
		t.Fatalf("takeover of a verified hostname = %v, want ErrConflict", err)
	}
	if _, err := takeOver(holderTwo, "b.squat.test"); !errors.Is(err, ErrConflict) {
		t.Fatalf("takeover from the claimant's own account = %v, want ErrConflict", err)
	}
	got, err := takeOver(claimant, "b.squat.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.SurfaceID != claimant.ID || got.ChallengeToken != "proof" || got.Verified() {
		t.Fatalf("taken-over hostname = %+v; want a pending row on the claimant's surface", got)
	}
	if hosts, _ := m.ListTenantHostnamesForSurface(ctx, holder.ID); len(hosts) != 2 {
		t.Fatalf("holder surface keeps %d hostnames, want 2 (a verified, c pending)", len(hosts))
	}
}

// A hostname left on a deleted surface routes nothing; proof releases it
// whatever its state or account.
func TestMemStoreTenantHostnameOnDeletedSurfaceIsReleasedOnProof(t *testing.T) {
	ctx := context.Background()
	m, holderApp, _, claimantApp := claimTakeoverFixture(t)
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10}
	holder, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{AccountID: holderApp.AccountID, AppID: holderApp.ID, Name: "gone"}, lim)
	if err != nil {
		t.Fatal(err)
	}
	claimant, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{AccountID: claimantApp.AccountID, AppID: claimantApp.ID, Name: "new"}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{SurfaceID: holder.ID, Hostname: "left.example.test", ChallengeToken: "tok"}, lim); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.MarkTenantHostnameVerifiedIfChallenge(ctx, "left.example.test", "tok"); err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	if err := m.DeleteTenantSurface(ctx, holder.ID); err != nil {
		t.Fatal(err)
	}
	got, err := m.TakeOverTenantHostnameClaim(ctx, CreateTenantHostnameParams{SurfaceID: claimant.ID, Hostname: "left.example.test", ChallengeToken: "proof"}, lim)
	if err != nil || got.SurfaceID != claimant.ID {
		t.Fatalf("takeover from a deleted surface = %+v, %v; want the row released", got, err)
	}
}

// A TOTP step is accepted once per account, and only moving forward.
func TestMemStoreAcceptTOTPStep(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, _ := m.CreateAccount(ctx, "totp-step@example.test", api.PlanPro)
	other, _ := m.CreateAccount(ctx, "totp-step-other@example.test", api.PlanPro)
	for _, tc := range []struct {
		account string
		step    int64
		want    bool
	}{
		{acct.ID, 100, true},
		{acct.ID, 100, false},
		{acct.ID, 99, false},
		{other.ID, 100, true},
		{acct.ID, 101, true},
		{"missing", 200, false},
	} {
		if got, err := m.AcceptTOTPStep(ctx, tc.account, tc.step); err != nil || got != tc.want {
			t.Errorf("AcceptTOTPStep(%s, %d) = %v, %v; want %v", tc.account, tc.step, got, err, tc.want)
		}
	}
}
