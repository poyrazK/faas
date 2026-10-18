package productemail_test

import (
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

func ownerRepository(t *testing.T, postgres bool) (productemail.Store, productemail.Key) {
	t.Helper()
	k := emailKey()
	if !postgres {
		return productemail.NewMemory(), k
	}
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "email-owner-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "email-owner", "Email customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	k.AccountID, k.AppID, k.TenantID = a.ID, app.ID, tenant.ID
	return productemail.NewPostgres(pool), k
}
func ownerSubscription(t *testing.T, repo productemail.Store, k productemail.Key, now time.Time) productemail.Subscription {
	t.Helper()
	sub, err := repo.Put(t.Context(), productemail.Subscription{Key: k, AppSlug: "exports", AuthSlug: "auth", Work: true, Help: true, Grant: []byte("encrypted-fixture"), ExpiresAt: now.Add(time.Hour)}, 0, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return sub
}
func TestProductEmailOwnerMemory(t *testing.T)   { ownerContract(t, false) }
func TestProductEmailOwnerPostgres(t *testing.T) { ownerContract(t, true) }
func ownerContract(t *testing.T, postgres bool) {
	repo, k := ownerRepository(t, postgres)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	sub := ownerSubscription(t, repo, k, now)
	scope := productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID, TenantID: k.TenantID}
	n := item(now.Add(-time.Minute), "work_succeeded")
	if err := repo.Enqueue(ctx, sub, []api.ProductNotification{n}, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	page, err := repo.OwnerPage(ctx, scope, "all", "", now)
	if err != nil || len(page.Deliveries) != 1 || page.Counts.Queued != 1 || page.OldestPendingAt == nil {
		t.Fatal(page, err)
	}
	page.Mode = "local"
	if err = productemail.ValidateOwnerPage(page, k.TenantID, "all", ""); err != nil {
		t.Fatal("invalid queue projection", err)
	}
	id := page.Deliveries[0].ID
	jobs, err := repo.Claim(ctx, now)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	d := jobs[0]
	if _, err = repo.Retry(ctx, scope, d, now); !errors.Is(err, productemail.ErrConflict) {
		t.Fatal("active lease retried", err)
	}
	if err = repo.Finish(ctx, d, "failed", "provider_rejected", now, now); err != nil {
		t.Fatal(err)
	}
	page, err = repo.OwnerPage(ctx, scope, "failed", "", now)
	if err != nil || !page.Deliveries[0].RetryEligible || page.Counts.Failed != 1 || page.OldestPendingAt != nil {
		t.Fatal(page, err)
	}
	var wg sync.WaitGroup
	results := make(chan api.ProductEmailRetryReceipt, 8)
	errorsCh := make(chan error, 8)
	for range 8 {
		wg.Go(func() { r, e := repo.Retry(ctx, scope, d, now); results <- r; errorsCh <- e })
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for e := range errorsCh {
		if e != nil {
			t.Fatal("retry receipt recovery", e)
		}
	}
	var receipt api.ProductEmailRetryReceipt
	for r := range results {
		if receipt.DeliveryID != "" && r != receipt {
			t.Fatal("different retry receipts", r, receipt)
		}
		receipt = r
	}
	if receipt.FailedAttempt != 1 || receipt.DeliveryID != id || receipt.TenantID != k.TenantID {
		t.Fatal(receipt)
	}
	_, fresh, err := repo.OwnerGet(ctx, scope, id, now)
	if err != nil || fresh.Status != "queued" || fresh.Attempts != 1 || !fresh.Deadline.Equal(d.Deadline) {
		t.Fatal("retry changed budget/deadline", fresh, err)
	}
	jobs, err = repo.Claim(ctx, now)
	if err != nil || len(jobs) != 1 || jobs[0].Attempts != 2 || jobs[0].ID != id {
		t.Fatal(jobs, err)
	}
	if err = repo.Finish(ctx, jobs[0], "provider_accepted", "", now, now); err != nil {
		t.Fatal(err)
	}
	// A lost response recovers the original receipt even after the send finished.
	got, err := repo.Retry(ctx, scope, d, now.Add(time.Second))
	if err != nil || got != receipt {
		t.Fatal("repeated recovery resubmitted", got, err)
	}
	if _, err = repo.Retry(ctx, scope, jobs[0], now); !errors.Is(err, productemail.ErrConflict) {
		t.Fatal("accepted message retried", err)
	}
	if e := repo.Finish(ctx, d, "failed", "provider_rejected", now, now); !errors.Is(e, productemail.ErrConflict) {
		t.Fatal("stale worker overwrote accepted attempt", e)
	}
	for _, field := range []string{"account", "app", "tenant"} {
		foreign := scope
		switch field {
		case "account":
			foreign.AccountID = uuid.NewString()
		case "app":
			foreign.AppID = uuid.NewString()
		case "tenant":
			foreign.TenantID = uuid.NewString()
		}
		if _, _, e := repo.OwnerGet(ctx, foreign, id, now); !errors.Is(e, productemail.ErrNotFound) {
			t.Fatal("cross-scope lookup", field, e)
		}
		if _, e := repo.RetryReceipt(ctx, foreign, id, 1, now); !errors.Is(e, productemail.ErrNotFound) {
			t.Fatal("cross-scope receipt", field, e)
		}
		if p, e := repo.OwnerPage(ctx, foreign, "all", "", now); e != nil || len(p.Deliveries) != 0 || p.Counts.ProviderAccepted != 0 {
			t.Fatal("cross-scope totals", field, p, e)
		}
	}
	if _, e := repo.RetryReceipt(ctx, scope, id, 1, now.Add(api.ProductControlNotificationWindow)); !errors.Is(e, productemail.ErrNotFound) {
		t.Fatal("expired retry receipt visible", e)
	}
	// A new failure cannot revive expired consent or extend its original window.
	n = item(now, "help_replied")
	if err = repo.Enqueue(ctx, sub, []api.ProductNotification{n}, now); err != nil {
		t.Fatal(err)
	}
	jobs, err = repo.Claim(ctx, now)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	d = jobs[0]
	if err = repo.Finish(ctx, d, "failed", "provider_rejected", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Retry(ctx, scope, d, d.Deadline); !errors.Is(err, productemail.ErrConflict) {
		t.Fatal("extended deadline", err)
	}
	sub.Work, sub.Help = false, false
	if _, err = repo.Put(ctx, sub, sub.Version, now); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Retry(ctx, scope, d, now); !errors.Is(err, productemail.ErrConflict) {
		t.Fatal("revived opt-out", err)
	}
}
func TestProductEmailOwnerPagination(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			repo, k := ownerRepository(t, postgres)
			now := time.Now().UTC().Truncate(time.Microsecond)
			sub := ownerSubscription(t, repo, k, now)
			scope := productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID}
			items := []api.ProductNotification{}
			for range api.ProductControlEmailOwnerPageSize + 4 {
				items = append(items, item(now.Add(-time.Minute), "work_failed"))
			}
			if err := repo.Enqueue(t.Context(), sub, items, now.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			first, err := repo.OwnerPage(t.Context(), scope, "all", "", now)
			if err != nil || len(first.Deliveries) != api.ProductControlEmailOwnerPageSize || first.NextCursor == "" || first.Counts.Queued != int64(len(items)) {
				t.Fatal(first, err)
			}
			second, err := repo.OwnerPage(t.Context(), scope, "all", first.NextCursor, now)
			if err != nil || len(second.Deliveries) != 4 || second.NextCursor != "" {
				t.Fatal(second, err)
			}
			seen := map[string]bool{}
			for _, p := range []api.ProductEmailOwnerPage{first, second} {
				for _, d := range p.Deliveries {
					if seen[d.ID] {
						t.Fatal("duplicate tied-time page", d.ID)
					}
					seen[d.ID] = true
				}
			}
			if len(seen) != len(items) {
				t.Fatal("missing page entries", len(seen))
			}
			filtered, err := repo.OwnerPage(t.Context(), scope, "failed", "", now)
			if err != nil || len(filtered.Deliveries) != 0 || filtered.Counts.Queued != int64(len(items)) {
				t.Fatal("partial-page/status totals", filtered, err)
			}
			for _, status := range []string{"failed", "bad"} {
				if _, err = repo.OwnerPage(t.Context(), scope, status, first.NextCursor, now); !errors.Is(err, productemail.ErrInvalid) {
					t.Fatal("cursor changed status", err)
				}
			}
			scope.TenantID = k.TenantID
			if _, err = repo.OwnerPage(t.Context(), scope, "all", first.NextCursor, now); !errors.Is(err, productemail.ErrInvalid) {
				t.Fatal("cursor changed customer", err)
			}
		})
	}
}

func TestProductEmailOwnerRetryKeepsAttemptBudget(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			repo, k := ownerRepository(t, postgres)
			now := time.Now().UTC().Truncate(time.Microsecond)
			sub := ownerSubscription(t, repo, k, now)
			scope := productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID, TenantID: k.TenantID}
			if err := repo.Enqueue(t.Context(), sub, []api.ProductNotification{item(now, "work_failed")}, now); err != nil {
				t.Fatal(err)
			}
			var originalID string
			for attempt := 1; attempt <= api.ProductControlEmailMaxAttempts; attempt++ {
				jobs, err := repo.Claim(t.Context(), now)
				if err != nil || len(jobs) != 1 || jobs[0].Attempts != attempt {
					t.Fatal(attempt, jobs, err)
				}
				d := jobs[0]
				if originalID == "" {
					originalID = d.ID
				}
				if d.ID != originalID || !d.Deadline.Equal(now.Add(api.ProductControlEmailRetryWindow)) {
					t.Fatal("recovery replaced delivery or deadline", d)
				}
				if err = repo.Finish(t.Context(), d, "failed", "provider_rejected", now, now); err != nil {
					t.Fatal(err)
				}
				_, err = repo.Retry(t.Context(), scope, d, now)
				if attempt < api.ProductControlEmailMaxAttempts && err != nil {
					t.Fatal(err)
				}
				if attempt == api.ProductControlEmailMaxAttempts && err == nil {
					t.Fatal("owner reset attempt budget")
				}
			}
			if jobs, err := repo.Claim(t.Context(), now.Add(time.Second)); err != nil || len(jobs) != 0 {
				t.Fatal("seventh attempt claimed", jobs, err)
			}
		})
	}
}
