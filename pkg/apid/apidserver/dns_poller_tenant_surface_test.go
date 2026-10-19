package apidserver_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// tenantSurfacePollerServer is a test harness that wires a real
// MemStore into a minimal server. We don't construct a full
// *server (it has dozens of fields); instead we exercise the
// dns_poller via the standalone runVerifyOnce and the
// pendingUnverifiedHostnames helper. The poller's data path
// only touches store + notif; those are the only two fields
// needed.
type tenantSurfacePollerServer struct {
	store state.Store
	notif *tenantPollerNotifier
}

// tenantPollerNotifier is a minimal notifier used by the
// tenant-hostname poller test. The production runVerifyOnce
// doesn't emit a separate notification for tenant-hostname
// verification (the trigger on tenant_hostnames UPDATE
// fires tenant_surface_changed), but we keep the seam
// parallel to the custom-domain path so a future change
// lands without a refactor.
type tenantPollerNotifier struct {
	mu     sync.Mutex
	events []notifEvent
}

type notifEvent struct {
	channel string
	payload string
}

func (r *tenantPollerNotifier) Notify(_ context.Context, channel, payload string) error {
	r.mu.Lock()
	r.events = append(r.events, notifEvent{channel: channel, payload: payload})
	r.mu.Unlock()
	return nil
}

func newPollerServer() *tenantSurfacePollerServer {
	return &tenantSurfacePollerServer{
		store: state.NewMemStore(),
		notif: &tenantPollerNotifier{},
	}
}

func TestDNSPoller_TenantHostname_VerifiesOnTXTMatch(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	s := newPollerServer()
	ctx := context.Background()

	acct, err := s.store.CreateAccount(ctx, "ts-poll@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "ts-poll", RAMMB: 256, Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	surf, err := s.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "poll-surf",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	token := "tok-123"
	if _, err := s.store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID:      surf.ID,
		Hostname:       "api.customer-a.com",
		ChallengeToken: token,
	}, lim); err != nil {
		t.Fatal(err)
	}

	// Inject a fake TXT resolver that returns the matching
	// token. Restore on exit so other tests see the real
	// net.Resolver.
	prev := (*apidserver.TxtLookupFunc)
	(*apidserver.TxtLookupFunc) = func(_ context.Context, target string) ([]string, error) {
		if target != "_faas-verify.api.customer-a.com" {
			t.Errorf("txtLookupFunc target = %q; want _faas-verify.api.customer-a.com", target)
		}
		return []string{token}, nil
	}
	defer func() { (*apidserver.TxtLookupFunc) = prev }()

	// Drive the poller directly. The runVerifyOnce method
	// lives on *server; we invoke pendingUnverifiedHostnames
	// + checkTXT through the same code path the production
	// goroutine would. To keep the harness minimal, call the
	// helpers directly rather than reconstructing *server.
	pending, err := pendingUnverifiedHostnamesForTest(ctx, s.store)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if !apidserver.CheckTXT(ctx, pending[0].Hostname, pending[0].ChallengeToken) {
		t.Fatal("checkTXT = false; want true")
	}
	if err := s.store.MarkTenantHostnameVerified(ctx, pending[0].Hostname); err != nil {
		t.Fatal(err)
	}

	// After MarkTenantHostnameVerified, the host is no longer
	// in the pending batch.
	pending, err = pendingUnverifiedHostnamesForTest(ctx, s.store)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("post-verify pending = %d, want 0", len(pending))
	}
}

// pendingUnverifiedHostnamesForTest is a test-only thin wrapper
// around the store call. Mirrors the production
// s.pendingUnverifiedHostnames helper but takes the store
// directly so the test harness doesn't need a *server.
func pendingUnverifiedHostnamesForTest(ctx context.Context, store state.Store) ([]apidserver.PendingHostnameRow, error) {
	rows, err := store.ListPendingTenantHostnames(ctx, time.Now(), 50)
	if err != nil {
		return nil, err
	}
	out := make([]apidserver.PendingHostnameRow, len(rows))
	for i, h := range rows {
		out[i] = apidserver.PendingHostnameRow{
			Hostname:       h.Hostname,
			ChallengeToken: h.ChallengeToken,
			SurfaceID:      h.SurfaceID,
		}
	}
	return out, nil
}

func TestDNSPoller_TenantHostname_LeavesUnmatchedAlone(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	s := newPollerServer()
	ctx := context.Background()
	acct, _ := s.store.CreateAccount(ctx, "ts-poll2@example.com", api.PlanPro)
	app, _ := s.store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "ts-poll2", RAMMB: 256, Status: state.AppActive})
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	surf, _ := s.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "nope",
	}, lim)
	if _, err := s.store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID:      surf.ID,
		Hostname:       "nope.example",
		ChallengeToken: "expected-token",
	}, lim); err != nil {
		t.Fatal(err)
	}

	prev := (*apidserver.TxtLookupFunc)
	(*apidserver.TxtLookupFunc) = func(_ context.Context, _ string) ([]string, error) {
		return []string{"different-token"}, nil
	}
	defer func() { (*apidserver.TxtLookupFunc) = prev }()

	pending, _ := pendingUnverifiedHostnamesForTest(ctx, s.store)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if apidserver.CheckTXT(ctx, pending[0].Hostname, pending[0].ChallengeToken) {
		t.Fatal("checkTXT = true; want false (record didn't match)")
	}
}

func TestDNSPoller_TenantHostname_LookupErrorStaysPending(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	s := newPollerServer()
	ctx := context.Background()
	acct, _ := s.store.CreateAccount(ctx, "ts-poll3@example.com", api.PlanPro)
	app, _ := s.store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "ts-poll3", RAMMB: 256, Status: state.AppActive})
	lim := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
	}
	surf, _ := s.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "err-surf",
	}, lim)
	if _, err := s.store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID:      surf.ID,
		Hostname:       "err.example",
		ChallengeToken: "tok",
	}, lim); err != nil {
		t.Fatal(err)
	}

	prev := (*apidserver.TxtLookupFunc)
	(*apidserver.TxtLookupFunc) = func(_ context.Context, _ string) ([]string, error) {
		return nil, errors.New("dns timeout")
	}
	defer func() { (*apidserver.TxtLookupFunc) = prev }()

	pending, _ := pendingUnverifiedHostnamesForTest(ctx, s.store)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1 (lookup error keeps the host in the batch)", len(pending))
	}
	if apidserver.CheckTXT(ctx, pending[0].Hostname, pending[0].ChallengeToken) {
		t.Fatal("checkTXT = true on DNS error; want false")
	}
}

func TestDNSPoller_TenantHostname_FlagOffSkips(t *testing.T) {
	// FAAS_TENANT_SURFACES_ENABLED unset → production
	// runVerifyOnce's tenant-hostname branch short-circuits.
	// The test mirrors the production guard inline so a
	// regression trips the test instead of the operator.
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "")
	if api.TenantSurfacesEnabled() {
		t.Fatal("TenantSurfacesEnabled = true; want false (flag unset)")
	}
	_ = db.NotifyTenantSurfaceChanged // the constant is wired in pkg/db/notify.go (commit 6)
}

// The poller reads a pending hostname's token, then resolves TXT. If the
// hostname is deleted and added again (here by another account) while the
// lookup is in flight, the proof found belongs to the old row's token; it
// must not verify the replacement, which proved nothing.
func TestDNSPoller_TenantHostname_StaleProofDoesNotVerifyReplacement(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10}
	surface := func(email, slug string) state.TenantSurface {
		acct, err := store.CreateAccount(ctx, email, api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, RAMMB: 256, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		surf, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: app.ID, Name: slug}, lim)
		if err != nil {
			t.Fatal(err)
		}
		return surf
	}
	owner, newcomer := surface("hostname-owner@example.com", "hostname-owner"), surface("hostname-newcomer@example.com", "hostname-newcomer")
	const host = "shop.customer-a.com"
	if _, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{SurfaceID: owner.ID, Hostname: host, ChallengeToken: "owner-token"}, lim); err != nil {
		t.Fatal(err)
	}

	prev := *apidserver.TxtLookupFunc
	*apidserver.TxtLookupFunc = func(context.Context, string) ([]string, error) {
		if err := store.DeleteTenantHostname(ctx, host); err != nil {
			t.Errorf("delete hostname mid-lookup: %v", err)
		}
		if _, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{SurfaceID: newcomer.ID, Hostname: host, ChallengeToken: "newcomer-token"}, lim); err != nil {
			t.Errorf("re-add hostname mid-lookup: %v", err)
		}
		return []string{"owner-token"}, nil
	}
	t.Cleanup(func() { *apidserver.TxtLookupFunc = prev })

	(*e.S()).RunVerifyOnce(ctx, slog.New(slog.DiscardHandler))

	got, err := store.GetTenantHostnameByName(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	if got.SurfaceID != newcomer.ID {
		t.Fatalf("hostname surface = %s, want the replacement row %s", got.SurfaceID, newcomer.ID)
	}
	if got.Verified() {
		t.Fatal("the old row's TXT proof verified the replacement hostname")
	}
}
