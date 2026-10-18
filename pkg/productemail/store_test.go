package productemail_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

func emailKey() productemail.Key {
	return productemail.Key{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: uuid.NewString(), AuthAppID: uuid.NewString(), OrganizationID: uuid.NewString(), ActorID: uuid.NewString()}
}
func item(at time.Time, kind string) api.ProductNotification {
	id := uuid.NewString()
	prefix := "work/"
	help := ""
	if kind == "help_replied" {
		prefix = "help/"
		help = "workspace"
	}
	hash := sha256.Sum256([]byte(id))
	return api.ProductNotification{ID: prefix + id, ReferenceID: id, Kind: kind, HelpKind: help, OccurredAt: at, Revision: hex.EncodeToString(hash[:])}
}
func TestProductEmailMemory(t *testing.T) { contract(t, productemail.NewMemory(), emailKey()) }
func TestProductEmailPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "mail-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "mail-customer", "Mail customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	k := emailKey()
	k.AccountID, k.AppID, k.TenantID = a.ID, app.ID, tenant.ID
	contract(t, productemail.NewPostgres(pool), k)
	// Exercise the platform's actual send-time source lookup against Postgres,
	// including a changed workspace mapping, without involving a mail provider.
	auth, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "auth-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutProductCatalog(t.Context(), a.ID, app.ID, api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10}}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetProductCustomer(t.Context(), a.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: k.OrganizationID})
	if err != nil {
		t.Fatal(err)
	}
	scope := state.ProductNotificationScope{ProductPlanScope: state.ProductPlanScope{AccountID: a.ID, AppID: app.ID, TenantID: tenant.ID, AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: k.OrganizationID, Role: "owner"}}
	h, err := s.CreateProductHelp(t.Context(), state.ProductHelpScope(scope), api.ProductHelpCreate{ID: uuid.NewString(), Kind: "workspace", Message: "Private customer text"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateProductHelp(t.Context(), a.ID, app.ID, h.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "Private owner text"})
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.GetProductNotification(t.Context(), scope, "help/"+h.Request.ID)
	if err != nil || n.Kind != "help_resolved" || len(n.Revision) != 64 {
		t.Fatal(n, err)
	}
	scope.OrganizationID = uuid.NewString()
	if _, err = s.GetProductNotification(t.Context(), scope, n.ID); !errors.Is(err, state.ErrProductAccess) {
		t.Fatal("send-time mapping check", err)
	}
}
func contract(t *testing.T, s productemail.Store, k productemail.Key) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	sub, err := s.Get(ctx, k)
	if err != nil || sub.Version != 0 {
		t.Fatal(sub, err)
	}
	sub = productemail.Subscription{Key: k, AppSlug: "exports", AuthSlug: "auth", Work: true, Grant: []byte("encrypted-fixture"), ExpiresAt: now.Add(time.Hour)}
	sub, err = s.Put(ctx, sub, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Version != 1 || !sub.WorkSince.Equal(now) {
		t.Fatal(sub)
	}
	if _, err = s.Put(ctx, sub, 0, now); !errors.Is(err, productemail.ErrConflict) {
		t.Fatal("stale preference write", err)
	}
	sub.Grant[0] = 'X'
	fresh, err := s.Get(ctx, k)
	if err != nil || string(fresh.Grant) != "encrypted-fixture" {
		t.Fatal("grant aliased", err)
	}
	sub = fresh
	old, new, help := item(now.Add(-time.Second), "work_succeeded"), item(now.Add(time.Second), "work_failed"), item(now.Add(time.Second), "help_replied")
	for range 2 {
		if err = s.Enqueue(ctx, sub, []api.ProductNotification{old, new, help}, now.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := s.Recent(ctx, k)
	if err != nil || len(recent) != 1 || recent[0].Status != "queued" || recent[0].NotificationID != new.ID {
		t.Fatal("opt-in baseline/category/dedup", recent, err)
	}
	other := k
	other.ActorID = uuid.NewString()
	if rows, e := s.Recent(ctx, other); e != nil || len(rows) != 0 {
		t.Fatal("cross-person history", rows, e)
	}
	jobs, err := s.Claim(ctx, now.Add(2*time.Second))
	if err != nil || len(jobs) != 1 || jobs[0].Attempts != 1 {
		t.Fatal(jobs, err)
	}
	job := jobs[0]
	if jobs, e := s.Claim(ctx, now.Add(3*time.Second)); e != nil || len(jobs) != 0 {
		t.Fatal("duplicate active lease", jobs, e)
	}
	if _, err = s.Ready(ctx, job, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	// A crashed worker loses its lease; its completion cannot overwrite a retry.
	jobs, err = s.Claim(ctx, now.Add(api.ProductControlEmailLease+3*time.Second))
	if err != nil || len(jobs) != 1 || jobs[0].Attempts != 2 || jobs[0].ID != job.ID {
		t.Fatal(jobs, err)
	}
	if err = s.Finish(ctx, job, "provider_accepted", "", now, now); !errors.Is(err, productemail.ErrConflict) {
		t.Fatal("stale attempt completed", err)
	}
	job = jobs[0]
	if err = s.Finish(ctx, job, "captured", "", now, now); err != nil {
		t.Fatal(err)
	}
	if jobs, e := s.Claim(ctx, now.Add(time.Hour)); e != nil || len(jobs) != 0 {
		t.Fatal("terminal retried", jobs, e)
	}
	// Consent changes cancel pending messages, without rewriting completed ones.
	next := item(now.Add(time.Second), "work_succeeded")
	if err = s.Enqueue(ctx, sub, []api.ProductNotification{next}, now); err != nil {
		t.Fatal(err)
	}
	sub.Work = false
	sub, err = s.Put(ctx, sub, 1, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.Recent(ctx, k)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	states := map[string]bool{}
	for _, r := range rows {
		states[r.Status] = true
	}
	if !states["captured"] || !states["cancelled"] {
		t.Fatal(states)
	}
	if len(sub.Grant) != 0 || !sub.ExpiresAt.IsZero() {
		t.Fatal("disabled grant retained")
	}
	// Concurrent preference updates have exactly one winner.
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() { _, e := s.Put(ctx, sub, sub.Version, now.Add(5*time.Second)); results <- e })
	}
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else if !errors.Is(e, productemail.ErrConflict) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatal("CAS winners", wins)
	}
}

func TestProductEmailRetryBoundsAndExpiredConsent(t *testing.T) {
	s := productemail.NewMemory()
	now := time.Now().UTC()
	sub := productemail.Subscription{Key: emailKey(), AppSlug: "export", AuthSlug: "auth", Help: true, Grant: []byte("encrypted"), ExpiresAt: now.Add(time.Hour)}
	sub, err := s.Put(t.Context(), sub, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	n := item(now.Add(time.Second), "help_replied")
	if err = s.Enqueue(t.Context(), sub, []api.ProductNotification{n}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	at := now.Add(2 * time.Second)
	for i := 1; i <= api.ProductControlEmailMaxAttempts; i++ {
		jobs, e := s.Claim(t.Context(), at)
		if e != nil || len(jobs) != 1 || jobs[0].Attempts != i {
			t.Fatal(i, jobs, e)
		}
		at = at.Add(api.ProductControlEmailLease + time.Second)
	}
	if jobs, e := s.Claim(t.Context(), at); e != nil || len(jobs) != 0 {
		t.Fatal(jobs, e)
	}
	rows, _ := s.Recent(t.Context(), sub.Key)
	if rows[0].Status != "failed" || rows[0].Reason != "retry_exhausted" {
		t.Fatal(rows)
	}
	renew := sub
	renew.ExpiresAt = now.Add(3 * time.Hour)
	renew, err = s.Put(t.Context(), renew, 1, now.Add(2*time.Hour))
	if err != nil || !renew.HelpSince.Equal(now.Add(2*time.Hour)) {
		t.Fatal("renewal backfilled expired consent", renew, err)
	}
}
