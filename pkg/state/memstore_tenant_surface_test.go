package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// tenantSurfaceFixture — account + app + permissive limits for the
// CreateTenantSurfaceIfUnderQuota / CreateTenantHostnameIfUnderQuota
// happy paths. Tests that need a tight quota construct limits locally.
func tenantSurfaceFixture(t *testing.T) (*MemStore, context.Context, Account, App, api.Limits) {
	t.Helper()
	m, ctx, acct, app, _ := memCoverageFixture(t)
	return m, ctx, acct, app, api.Limits{
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
		TenantSurfacesAllowed:     true,
	}
}

func TestMemStoreTenantSurfaceCreateHappy(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	surf, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "na-customers",
	}, lim)
	if err != nil {
		t.Fatalf("CreateTenantSurfaceIfUnderQuota: %v", err)
	}
	if surf.Status != SurfaceStatusPending || surf.CertState != CertStateNone {
		t.Fatalf("initial state = %+v", surf)
	}
	if surf.CertKind != CertKindPerHostSAN {
		t.Fatalf("default cert_kind = %q", surf.CertKind)
	}
}

func TestMemStoreTenantSurfaceCreateDefaultsCertKind(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	if lim.TenantSurfacesAllowed != true {
		t.Fatalf("fixture gate off")
	}
	// CreateTenantSurfaceParams.CertKind empty → store fills per_host_san.
	surf, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "default-cert",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if surf.CertKind != CertKindPerHostSAN {
		t.Fatalf("default cert_kind = %q", surf.CertKind)
	}
}

func TestMemStoreTenantSurfaceCreatePlanGateOff(t *testing.T) {
	m, ctx, acct, app, _ := tenantSurfaceFixture(t)
	lim := api.Limits{TenantSurfacesAllowed: false}
	if _, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "free-plan-blocked",
	}, lim); !errors.Is(err, ErrTenantSurfacesNotAllowed) {
		t.Fatalf("plan gate off = %v", err)
	}
}

func TestMemStoreTenantSurfaceCreateAccountMissing(t *testing.T) {
	m, ctx, _, app, lim := tenantSurfaceFixture(t)
	if _, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: "missing-account", AppID: app.ID, Name: "no-acct",
	}, lim); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account = %v", err)
	}
}

func TestMemStoreTenantSurfaceQuotaEnforced(t *testing.T) {
	m, ctx, acct, app, _ := tenantSurfaceFixture(t)
	lim := api.Limits{
		TenantSurfacesPerAccount:  2,
		TenantHostnamesPerSurface: 10,
		TenantSurfacesAllowed:     true,
	}
	for i := 0; i < 2; i++ {
		if _, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
			AccountID: acct.ID, AppID: app.ID, Name: "surf-" + uuid.NewString()[:8],
		}, lim); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	_, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "third",
	}, lim)
	var qe *TenantSurfaceQuotaError
	if !errors.As(err, &qe) || qe.Limit != 2 || qe.Observed != 2 {
		t.Fatalf("quota err = %v (%T)", err, err)
	}
}

func TestMemStoreTenantSurfaceNameConflict(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	if _, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "dup",
	}, lim); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "dup",
	}, lim); !errors.Is(err, ErrConflict) {
		t.Fatalf("dup name = %v", err)
	}
}

func TestMemStoreTenantSurfaceNameReuseAfterDelete(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	first, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "recyclable",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteTenantSurface(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "recyclable",
	}, lim); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}
}

func TestMemStoreTenantSurfaceGetAndList(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	names := []string{"alpha", "bravo", "charlie"}
	for _, n := range names {
		if _, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
			AccountID: acct.ID, AppID: app.ID, Name: n,
		}, lim); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.ListTenantSurfacesForAccount(ctx, acct.ID)
	if err != nil || len(got) != 3 {
		t.Fatalf("list = %+v, %v", got, err)
	}
	// The memstore impl sorts by CreatedAt then ID (see
	// memstore_tenant_surface.go::ListTenantSurfacesForAccount) so
	// the test cannot pin got[0]/got[2] to alphabetical names — the
	// three rows are created in sub-microsecond intervals and the
	// UUID tie-break is non-deterministic. Assert the set instead:
	// all three names must be present, and the IDs must be unique.
	seen := make(map[string]bool, 3)
	ids := make(map[string]bool, 3)
	for _, s := range got {
		seen[s.Name] = true
		ids[s.ID] = true
	}
	for _, want := range names {
		if !seen[want] {
			t.Fatalf("missing name %q in list = %+v", want, got)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 unique IDs, got %d: %+v", len(ids), ids)
	}
	appSurfaces, err := m.ListTenantSurfacesForApp(ctx, app.ID)
	if err != nil || len(appSurfaces) != 3 {
		t.Fatalf("app list = %+v, %v", appSurfaces, err)
	}
	if n, err := m.CountTenantSurfacesForAccount(ctx, acct.ID); err != nil || n != 3 {
		t.Fatalf("count = %d, %v", n, err)
	}
	if _, err := m.GetTenantSurfaceByID(ctx, got[1].ID); err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if _, err := m.GetTenantSurfaceByName(ctx, acct.ID, "bravo"); err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if _, err := m.GetTenantSurfaceByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id = %v", err)
	}
	if _, err := m.GetTenantSurfaceByName(ctx, acct.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing name = %v", err)
	}
}

func TestMemStoreTenantSurfaceStatusAndCert(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	surf, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "lifecycle",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateTenantSurfaceStatus(ctx, surf.ID, SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetTenantSurfaceByID(ctx, surf.ID)
	if !got.Active() {
		t.Fatalf("active() = false after Activate")
	}
	notAfter := time.Now().Add(90 * 24 * time.Hour)
	if err := m.UpdateTenantSurfaceCert(ctx, UpdateSurfaceCertParams{
		SurfaceID: surf.ID,
		CertState: CertStateIssued,
		NotAfter:  notAfter,
		LastError: "",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = m.GetTenantSurfaceByID(ctx, surf.ID)
	if got.CertState != CertStateIssued || !got.CertValid(time.Now()) {
		t.Fatalf("cert update = %+v", got)
	}
	if err := m.UpdateTenantSurfaceStatus(ctx, "missing", SurfaceStatusActive); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status missing = %v", err)
	}
	if err := m.UpdateTenantSurfaceCert(ctx, UpdateSurfaceCertParams{SurfaceID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cert missing = %v", err)
	}
}

func TestMemStoreTenantHostnameQuota(t *testing.T) {
	m, ctx, acct, app, _ := tenantSurfaceFixture(t)
	lim := api.Limits{
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 2,
		TenantSurfacesAllowed:     true,
	}
	surf, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "two-host",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	// Per-surface cap counts VERIFIED hostnames only (see
	// limits.go:449-450 doc, PR-A review finding K). The two
	// hostnames below are both verified before the third
	// insert attempts to land — that's how the cap trips.
	for _, h := range []string{"a.example", "b.example"} {
		if _, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
			SurfaceID: surf.ID, Hostname: h, ChallengeToken: "tok",
		}, lim); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.MarkTenantHostnameVerified(ctx, "a.example"); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkTenantHostnameVerified(ctx, "b.example"); err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "c.example", ChallengeToken: "tok",
	}, lim)
	var qe *TenantHostnameQuotaError
	if !errors.As(err, &qe) || qe.Limit != 2 || qe.SurfaceID != surf.ID {
		t.Fatalf("hostname quota = %v (%T)", err, err)
	}
}

// TestMemStoreTenantHostnameQuotaUnverifiedAllowed pins the
// PR-A review finding K fix: the per-surface cap counts VERIFIED
// hostnames only. A customer who adds 250 unverified hostnames
// (typos, DNS never resolved) is below the cap and can keep
// retrying verification — the previous implementation counted
// unverified rows and locked the customer out.
func TestMemStoreTenantHostnameQuotaUnverifiedAllowed(t *testing.T) {
	m, ctx, acct, app, _ := tenantSurfaceFixture(t)
	lim := api.Limits{
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 2,
		TenantSurfacesAllowed:     true,
	}
	surf, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "unverified-cap",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	// Add 5 unverified hostnames — well above the cap of 2.
	// All inserts succeed because the cap counts verified only.
	for _, h := range []string{"u1.example", "u2.example", "u3.example", "u4.example", "u5.example"} {
		if _, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
			SurfaceID: surf.ID, Hostname: h, ChallengeToken: "tok",
		}, lim); err != nil {
			t.Fatalf("insert %s: %v", h, err)
		}
	}
	// Verify two of them. The next insert attempt must now trip
	// the quota (verified-count == 2 == limit).
	if err := m.MarkTenantHostnameVerified(ctx, "u1.example"); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkTenantHostnameVerified(ctx, "u2.example"); err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "u6.example", ChallengeToken: "tok",
	}, lim)
	var qe *TenantHostnameQuotaError
	if !errors.As(err, &qe) || qe.Limit != 2 {
		t.Fatalf("post-verify quota = %v (%T); want TenantHostnameQuotaError(Limit=2)", err, err)
	}
	if qe.Observed != 2 {
		t.Errorf("observed = %d, want 2", qe.Observed)
	}
}

func TestMemStoreTenantHostnameParentSurfaceMissing(t *testing.T) {
	m, ctx, _, _, lim := tenantSurfaceFixture(t)
	if _, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: "missing-surface", Hostname: "x.example", ChallengeToken: "tok",
	}, lim); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing parent = %v", err)
	}
}

func TestMemStoreTenantHostnameAlreadyClaimed(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	s1, _ := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "first",
	}, lim)
	s2, _ := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "second",
	}, lim)
	if _, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: s1.ID, Hostname: "shared.example", ChallengeToken: "tok",
	}, lim); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: s2.ID, Hostname: "shared.example", ChallengeToken: "tok",
	}, lim); !errors.Is(err, ErrConflict) {
		t.Fatalf("dup hostname across surfaces = %v", err)
	}
}

func TestMemStoreTenantHostnameVerifiedAndList(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	surf, _ := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "verify-list",
	}, lim)
	h1, _ := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "v1.example", ChallengeToken: "tok1",
	}, lim)
	h2, _ := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "v2.example", ChallengeToken: "tok2",
	}, lim)
	if err := m.MarkTenantHostnameVerified(ctx, h1.Hostname); err != nil {
		t.Fatal(err)
	}
	all, _ := m.ListTenantHostnamesForSurface(ctx, surf.ID)
	if len(all) != 2 {
		t.Fatalf("list all = %d", len(all))
	}
	verified, _ := m.ListVerifiedTenantHostnamesForSurface(ctx, surf.ID)
	if len(verified) != 1 || verified[0].ID != h1.ID {
		t.Fatalf("verified = %+v", verified)
	}
	if err := m.MarkTenantHostnameCheckFailed(ctx, h2.Hostname, "dns timeout"); err != nil {
		t.Fatal(err)
	}
	if n, _ := m.CountTenantHostnamesForSurface(ctx, surf.ID); n != 2 {
		t.Fatalf("count = %d", n)
	}
	// MarkTenantHostnameVerified is idempotent on VerifiedAt semantics:
	// a second call refreshes LastCheckAt but keeps VerifiedAt set.
	if err := m.MarkTenantHostnameVerified(ctx, h1.Hostname); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkTenantHostnameVerified(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("verify missing = %v", err)
	}
	if err := m.MarkTenantHostnameCheckFailed(ctx, "missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fail missing = %v", err)
	}
}

func TestMemStoreListTenantSurfaceHostnames(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)

	active, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "global-active",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "global-deleted",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}

	// CreateTenantHostnameIfUnderQuota indexes each row by both its original
	// and lowercase names. The global listing must deduplicate those aliases
	// while excluding hostnames belonging to the deleted surface.
	if _, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: active.ID, Hostname: "Zed.Example", ChallengeToken: "tok-z",
	}, lim); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: deleted.ID, Hostname: "gone.example", ChallengeToken: "tok-gone",
	}, lim); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteTenantSurface(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}

	got, err := m.ListTenantSurfaceHostnames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "Zed.Example" {
		t.Fatalf("global hostnames = %#v, want [Zed.Example]", got)
	}
}

func TestMemStoreTenantHostnameDeleteAndResolve(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	surf, _ := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "delete-resolve",
	}, lim)
	h, _ := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "del.example", ChallengeToken: "tok",
	}, lim)
	got, err := m.TenantSurfaceByHostname(ctx, h.Hostname)
	if err != nil || got.ID != surf.ID {
		t.Fatalf("resolve = %+v, %v", got, err)
	}
	if err := m.DeleteTenantHostname(ctx, h.Hostname); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TenantSurfaceByHostname(ctx, h.Hostname); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve after delete = %v", err)
	}
	if err := m.DeleteTenantHostname(ctx, h.Hostname); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete = %v", err)
	}
}

func TestMemStoreTenantHostnameSoftDeletedSurfaceHidden(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	surf, _ := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "soft-hidden",
	}, lim)
	h, _ := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
		SurfaceID: surf.ID, Hostname: "hidden.example", ChallengeToken: "tok",
	}, lim)
	if err := m.DeleteTenantSurface(ctx, surf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TenantSurfaceByHostname(ctx, h.Hostname); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soft-deleted surface still resolvable = %v", err)
	}
}

func TestMemStoreListPendingTenantHostnames(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	surf, _ := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "poller",
	}, lim)
	for _, h := range []string{"p1.example", "p2.example", "p3.example"} {
		if _, err := m.CreateTenantHostnameIfUnderQuota(ctx, CreateTenantHostnameParams{
			SurfaceID: surf.ID, Hostname: h, ChallengeToken: "tok",
		}, lim); err != nil {
			t.Fatal(err)
		}
	}
	// All three have LastCheckAt zero → eligible.
	pending, err := m.ListPendingTenantHostnames(ctx, time.Now().Add(time.Hour), 50)
	if err != nil || len(pending) != 3 {
		t.Fatalf("pending (all) = %+v, %v", pending, err)
	}
	if err := m.MarkTenantHostnameVerified(ctx, "p1.example"); err != nil {
		t.Fatal(err)
	}
	// Limit + the verified one is filtered out.
	pending, err = m.ListPendingTenantHostnames(ctx, time.Now().Add(time.Hour), 1)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending (limit=1) = %+v, %v", pending, err)
	}
}

// TestMemStoreListTenantSurfacesNearingExpiry — PR-D commit 3
// renewer hot-path. Pins the predicate semantics:
// status=active AND cert_state=issued AND
// cert_not_after < cutoff; soft-deleted, non-issued, and
// out-of-window rows are filtered out.
func TestMemStoreListTenantSurfacesNearingExpiry(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	cutoff := time.Now().Add(30 * 24 * time.Hour)
	mkSurf := func(name string, status SurfaceStatus, certState CertState, notAfter time.Time) TenantSurface {
		s, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
			AccountID: acct.ID, AppID: app.ID, Name: name,
		}, lim)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.UpdateTenantSurfaceStatus(ctx, s.ID, status); err != nil {
			t.Fatal(err)
		}
		if err := m.UpdateTenantSurfaceCert(ctx, UpdateSurfaceCertParams{
			SurfaceID: s.ID, CertState: certState, NotAfter: notAfter,
		}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	due := mkSurf("due", SurfaceStatusActive, CertStateIssued, time.Now().Add(5*24*time.Hour))
	// The other three are outside the predicate.
	_ = mkSurf("not_active", SurfaceStatusSuspended, CertStateIssued, time.Now().Add(5*24*time.Hour))
	_ = mkSurf("failed", SurfaceStatusActive, CertStateFailed, time.Now().Add(5*24*time.Hour))
	_ = mkSurf("fresh", SurfaceStatusActive, CertStateIssued, time.Now().Add(90*24*time.Hour))
	got, err := m.ListTenantSurfacesNearingExpiry(ctx, cutoff, 1000, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d surfaces, want 1 (only 'due' matches)", len(got))
	}
	if got[0].ID != due.ID {
		t.Errorf("got surface %s, want %s", got[0].ID, due.ID)
	}
}

// TestMemStoreTouchTenantSurfaceForRenewal — PR-D commit 3
// renewer hot-path. Asserts the bump + ErrNotFound on a
// missing row.
func TestMemStoreTouchTenantSurfaceForRenewal(t *testing.T) {
	m, ctx, acct, app, lim := tenantSurfaceFixture(t)
	s, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "touch",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	before := s.UpdatedAt
	if err := m.TouchTenantSurfaceForRenewal(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetTenantSurfaceByID(ctx, s.ID)
	if !got.UpdatedAt.After(before) {
		t.Errorf("UpdatedAt = %v, want > %v (renewer touch should bump)", got.UpdatedAt, before)
	}
	if err := m.TouchTenantSurfaceForRenewal(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing = %v, want ErrNotFound", err)
	}
}
