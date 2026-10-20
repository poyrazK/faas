package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgTenantSurfaceSeed stands up a Pro account + app with a unique
// slug/email so multiple tests in the same schema don't trip the
// apps.slug UNIQUE.
func pgTenantSurfaceSeed(t *testing.T, s *state.PgStore, ctx context.Context, suffix string) (string, string) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "tenant-surf-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount(%s): %v", suffix, err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "tenant-surf-" + suffix, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp(%s): %v", suffix, err)
	}
	return acct.ID, app.ID
}

func pgTenantSurfaceParams(acct, app, name string) state.CreateTenantSurfaceParams {
	return state.CreateTenantSurfaceParams{
		AccountID: acct,
		AppID:     app,
		Name:      name,
		CertKind:  state.CertKindPerHostSAN,
	}
}

func TestPgStore_TenantSurface_CreateAndGet(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "rt")

	surf, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "na"), api.Limits{
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
		TenantSurfacesAllowed:     true,
	})
	if err != nil {
		t.Fatalf("CreateTenantSurfaceIfUnderQuota: %v", err)
	}
	if surf.Status != state.SurfaceStatusPending || surf.CertState != state.CertStateNone {
		t.Fatalf("initial state = %+v", surf)
	}
	got, err := s.GetTenantSurfaceByID(ctx, surf.ID)
	if err != nil || got.ID != surf.ID {
		t.Fatalf("GetTenantSurfaceByID = %+v, %v", got, err)
	}
	gotByName, err := s.GetTenantSurfaceByName(ctx, acct, "na")
	if err != nil || gotByName.ID != surf.ID {
		t.Fatalf("GetTenantSurfaceByName = %+v, %v", gotByName, err)
	}
	if _, err := s.GetTenantSurfaceByID(ctx, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("GetTenantSurfaceByID missing = %v", err)
	}
	if _, err := s.GetTenantSurfaceByName(ctx, acct, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("GetTenantSurfaceByName missing = %v", err)
	}
}

func TestPgStore_TenantSurface_PlanGate(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "gate")
	if _, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "blocked"), api.Limits{
		TenantSurfacesAllowed: false,
	}); !errors.Is(err, state.ErrTenantSurfacesNotAllowed) {
		t.Fatalf("plan gate off = %v", err)
	}
}

func TestPgStore_TenantSurface_AccountMissing(t *testing.T) {
	s, ctx := pgStore(t)
	_, app := pgTenantSurfaceSeed(t, s, ctx, "noacct")
	if _, err := s.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: uuid.NewString(), AppID: app, Name: "x",
	}, api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  1,
		TenantHostnamesPerSurface: 1,
	}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing account = %v", err)
	}
}

func TestPgStore_TenantSurface_PerAccountQuota(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "quota")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  2,
		TenantHostnamesPerSurface: 10,
	}
	if _, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "s1"), lim); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "s2"), lim); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "s3"), lim)
	var qe *state.TenantSurfaceQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *TenantSurfaceQuotaError, got %T: %v", err, err)
	}
	if qe.Limit != 2 || qe.Observed != 2 {
		t.Fatalf("quota fields = %+v", qe)
	}
}

func TestPgStore_TenantSurface_NameConflictAndReuseAfterDelete(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "name")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	first, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "reuse"), lim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "reuse"), lim); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("dup name = %v", err)
	}
	if err := s.DeleteTenantSurface(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "reuse"), lim); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}
}

func TestPgStore_TenantSurface_ListCountAndAppLookup(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "list")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	names := []string{"alpha", "bravo", "charlie"}
	for _, n := range names {
		if _, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, n), lim); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListTenantSurfacesForAccount(ctx, acct)
	if err != nil || len(list) != 3 {
		t.Fatalf("ListTenantSurfacesForAccount = %+v, %v", list, err)
	}
	if list[0].Name != "alpha" || list[2].Name != "charlie" {
		t.Fatalf("sort = %+v", list)
	}
	appList, err := s.ListTenantSurfacesForApp(ctx, app)
	if err != nil || len(appList) != 3 {
		t.Fatalf("ListTenantSurfacesForApp = %+v, %v", appList, err)
	}
	if n, err := s.CountTenantSurfacesForAccount(ctx, acct); err != nil || n != 3 {
		t.Fatalf("CountTenantSurfacesForAccount = %d, %v", n, err)
	}
}

func TestPgStore_TenantSurface_StatusAndCert(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "lifecycle")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	surf, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "lc"), lim)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTenantSurfaceStatus(ctx, surf.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{
		SurfaceID: surf.ID,
		CertState: state.CertStateIssued,
		NotAfter:  time.Now().Add(90 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTenantSurfaceByID(ctx, surf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active() {
		t.Fatal("active() = false after status update")
	}
	if got.CertState != state.CertStateIssued || !got.CertValid(time.Now()) {
		t.Fatalf("cert update = %+v", got)
	}
	if err := s.UpdateTenantSurfaceStatus(ctx, uuid.NewString(), state.SurfaceStatusActive); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("status missing = %v", err)
	}
	if err := s.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{SurfaceID: uuid.NewString()}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cert missing = %v", err)
	}
	if err := s.UpdateTenantSurfaceStatus(ctx, surf.ID, state.SurfaceStatusDeleted); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListTenantSurfacesForAccount(ctx, acct)
	if len(list) != 0 {
		t.Fatalf("soft-deleted surface still listed: %+v", list)
	}
}

// Every surface UPDATE notifies the cert remint path, so a cert write that
// changes neither the state nor the expiry must not write; a renewal that
// keeps cert_state=issued must still record its new expiry.
func TestPgStore_TenantSurface_CertWriteOnlyOnStateOrExpiryChange(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "certguard")
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10}
	surf, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "cg"), lim)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(10 * 24 * time.Hour).Truncate(time.Second)
	write := func(st state.CertState, notAfter time.Time, lastError string) state.TenantSurface {
		t.Helper()
		if err := s.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{SurfaceID: surf.ID, CertState: st, NotAfter: notAfter, LastError: lastError}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetTenantSurfaceByID(ctx, surf.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	issued := write(state.CertStateIssued, expiry, "")
	if same := write(state.CertStateIssued, expiry, ""); !same.UpdatedAt.Equal(issued.UpdatedAt) {
		t.Fatal("an unchanged issued write updated the row")
	}
	renewed := write(state.CertStateIssued, expiry.Add(80*24*time.Hour), "")
	if !renewed.CertNotAfter.Equal(expiry.Add(80*24*time.Hour)) || renewed.UpdatedAt.Equal(issued.UpdatedAt) {
		t.Fatalf("renewal kept cert_not_after %v; want the new expiry written", renewed.CertNotAfter)
	}
	failed := write(state.CertStateFailed, time.Time{}, "order 1 failed")
	if again := write(state.CertStateFailed, time.Time{}, "order 2 failed"); !again.UpdatedAt.Equal(failed.UpdatedAt) || again.CertLastError != "order 1 failed" {
		t.Fatalf("a new error text alone rewrote the row: %+v", again)
	}
}

func TestPgStore_TenantHostname_PerSurfaceQuota(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "hquota")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 2,
	}
	surf, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "hq"), lim)
	if err != nil {
		t.Fatal(err)
	}
	// Per-surface cap counts VERIFIED hostnames only (see
	// limits.go:449-450 doc, PR-A review finding K). The two
	// hostnames below are both verified before the third
	// insert attempts to land — that's how the cap trips.
	for _, h := range []string{"a.example", "b.example"} {
		if _, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
			SurfaceID: surf.ID, Hostname: h, ChallengeToken: "tok",
		}, lim); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkTenantHostnameVerified(ctx, "a.example"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkTenantHostnameVerified(ctx, "b.example"); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "c.example", ChallengeToken: "tok",
	}, lim)
	var qe *state.TenantHostnameQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *TenantHostnameQuotaError, got %T: %v", err, err)
	}
	if qe.Limit != 2 || qe.SurfaceID != surf.ID {
		t.Fatalf("quota fields = %+v", qe)
	}
}

func TestPgStore_TenantHostname_AlreadyClaimedAcrossSurfaces(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "claim")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	s1, _ := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "s1"), lim)
	s2, _ := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "s2"), lim)
	if _, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: s1.ID, Hostname: "shared.example", ChallengeToken: "tok",
	}, lim); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: s2.ID, Hostname: "shared.example", ChallengeToken: "tok",
	}, lim); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cross-surface duplicate hostname = %v", err)
	}
}

func TestPgStore_TenantHostname_ParentMissing(t *testing.T) {
	s, ctx := pgStore(t)
	if _, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: uuid.NewString(), Hostname: "x.example", ChallengeToken: "tok",
	}, api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  1,
		TenantHostnamesPerSurface: 1,
	}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing parent = %v", err)
	}
}

func TestPgStore_TenantHostname_VerifyAndList(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "verify")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	surf, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "vsurf"), lim)
	if err != nil {
		t.Fatal(err)
	}
	h1, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "v1.example", ChallengeToken: "tok1",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "v2.example", ChallengeToken: "tok2",
	}, lim); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkTenantHostnameVerified(ctx, h1.Hostname); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkTenantHostnameCheckFailed(ctx, "v2.example", "dns timeout"); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListTenantHostnamesForSurface(ctx, surf.ID)
	if len(all) != 2 || all[0].Hostname != "v1.example" || all[1].Hostname != "v2.example" {
		t.Fatalf("ListTenantHostnamesForSurface = %+v", all)
	}
	verified, _ := s.ListVerifiedTenantHostnamesForSurface(ctx, surf.ID)
	if len(verified) != 1 || verified[0].Hostname != h1.Hostname {
		t.Fatalf("ListVerifiedTenantHostnamesForSurface = %+v", verified)
	}
	if n, _ := s.CountTenantHostnamesForSurface(ctx, surf.ID); n != 2 {
		t.Fatalf("count = %d", n)
	}
	if err := s.MarkTenantHostnameVerified(ctx, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("verify missing = %v", err)
	}
}

// Verification binds the proved TXT token: a stale token, an already
// verified row and a missing row all report no match.
func TestPgStore_TenantHostname_VerifyIfChallenge(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "verifycas")
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10}
	surf, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "casurf"), lim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "cas.example", ChallengeToken: "current",
	}, lim); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host, token string
		want        bool
	}{
		{"cas.example", "stale", false},
		{"missing.example", "current", false},
		{"cas.example", "current", true},
		{"cas.example", "current", false},
	} {
		got, err := s.MarkTenantHostnameVerifiedIfChallenge(ctx, tc.host, tc.token)
		if err != nil || got != tc.want {
			t.Fatalf("verify %s with %q = %v, %v; want %v", tc.host, tc.token, got, err, tc.want)
		}
	}
	h, err := s.GetTenantHostnameByName(ctx, "cas.example")
	if err != nil || !h.Verified() {
		t.Fatalf("hostname after verification = %+v, %v", h, err)
	}
}

// Unverified hostnames are capped per surface, and another account's
// unverified hostname yields to a takeover (ADR-20261020090000).
func TestPgStore_TenantHostname_PendingCapAndTakeover(t *testing.T) {
	s, ctx := pgStore(t)
	holderAcct, holderApp := pgTenantSurfaceSeed(t, s, ctx, "takeover-holder")
	claimantAcct, claimantApp := pgTenantSurfaceSeed(t, s, ctx, "takeover-claimant")
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10, TenantHostnamesPendingPerSurface: 2}
	surface := func(acct, app, name string) state.TenantSurface {
		surf, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, name), lim)
		if err != nil {
			t.Fatal(err)
		}
		return surf
	}
	holder, holderTwo, claimant := surface(holderAcct, holderApp, "holder"), surface(holderAcct, holderApp, "holder-two"), surface(claimantAcct, claimantApp, "claimant")
	add := func(surf state.TenantSurface, host string) error {
		_, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{SurfaceID: surf.ID, Hostname: host, ChallengeToken: "tok-" + host}, lim)
		return err
	}
	for _, host := range []string{"a.pg-squat.example", "b.pg-squat.example"} {
		if err := add(holder, host); err != nil {
			t.Fatal(err)
		}
	}
	var qe *state.TenantHostnameQuotaError
	if err := add(holder, "c.pg-squat.example"); !errors.As(err, &qe) || !qe.Pending || qe.Limit != 2 || qe.Observed != 2 {
		t.Fatalf("third pending hostname = %v, want the pending cap", err)
	}
	if ok, err := s.MarkTenantHostnameVerifiedIfChallenge(ctx, "a.pg-squat.example", "tok-a.pg-squat.example"); err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	takeOver := func(surf state.TenantSurface, host string) (state.TenantHostname, error) {
		return s.TakeOverTenantHostnameClaim(ctx, state.CreateTenantHostnameParams{SurfaceID: surf.ID, Hostname: host, ChallengeToken: "proof"}, lim)
	}
	if _, err := takeOver(claimant, "a.pg-squat.example"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("takeover of a verified hostname = %v, want ErrConflict", err)
	}
	if _, err := takeOver(holderTwo, "b.pg-squat.example"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("takeover from the claimant's own account = %v, want ErrConflict", err)
	}
	before, err := s.GetTenantHostnameByName(ctx, "b.pg-squat.example")
	if err != nil {
		t.Fatal(err)
	}
	got, err := takeOver(claimant, "b.pg-squat.example")
	if err != nil {
		t.Fatal(err)
	}
	if got.SurfaceID != claimant.ID || got.ChallengeToken != "proof" || got.Verified() || got.ID == before.ID {
		t.Fatalf("taken-over hostname = %+v (was %s); want a new pending row on the claimant's surface", got, before.ID)
	}
	if hosts, _ := s.ListTenantHostnamesForSurface(ctx, holder.ID); len(hosts) != 1 {
		t.Fatalf("holder surface keeps %d hostnames, want only the verified one", len(hosts))
	}
	// A verified hostname left on a deleted surface routes nothing and is
	// released on proof too.
	if err := s.UpdateTenantSurfaceStatus(ctx, holder.ID, state.SurfaceStatusDeleted); err != nil {
		t.Fatal(err)
	}
	if got, err := takeOver(claimant, "a.pg-squat.example"); err != nil || got.SurfaceID != claimant.ID {
		t.Fatalf("takeover from a deleted surface = %+v, %v", got, err)
	}
}

func TestPgStore_TenantHostname_DeleteAndResolveByHostname(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "resolve")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	surf, err := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "rsurf"), lim)
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "del.example", ChallengeToken: "tok",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.TenantSurfaceByHostname(ctx, h.Hostname)
	if err != nil || got.ID != surf.ID {
		t.Fatalf("TenantSurfaceByHostname = %+v, %v", got, err)
	}
	if err := s.DeleteTenantHostname(ctx, h.Hostname); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TenantSurfaceByHostname(ctx, h.Hostname); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("resolve after delete = %v", err)
	}
	if err := s.DeleteTenantHostname(ctx, h.Hostname); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("double delete = %v", err)
	}
}

func TestPgStore_TenantHostname_ResolveHidesSoftDeletedSurface(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "hidden")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	surf, _ := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "hsurf"), lim)
	h, _ := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "hidden.example", ChallengeToken: "tok",
	}, lim)
	if err := s.DeleteTenantSurface(ctx, surf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TenantSurfaceByHostname(ctx, h.Hostname); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("soft-deleted surface still resolves = %v", err)
	}
}

func TestPgStore_TenantHostname_ListPending(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgTenantSurfaceSeed(t, s, ctx, "pending")
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	surf, _ := s.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(acct, app, "psurf"), lim)
	for _, h := range []string{"p1.example", "p2.example", "p3.example"} {
		if _, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
			SurfaceID: surf.ID, Hostname: h, ChallengeToken: "tok",
		}, lim); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.ListPendingTenantHostnames(ctx, time.Now().Add(time.Hour), 50)
	if err != nil || len(pending) != 3 {
		t.Fatalf("pending (all) = %+v, %v", pending, err)
	}
	if err := s.MarkTenantHostnameVerified(ctx, "p1.example"); err != nil {
		t.Fatal(err)
	}
	pending, err = s.ListPendingTenantHostnames(ctx, time.Now().Add(time.Hour), 1)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending (limit=1) = %+v, %v", pending, err)
	}
}
