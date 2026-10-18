package productemail_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func TestProductEmailFeedbackMemory(t *testing.T)   { feedbackContract(t, false) }
func TestProductEmailFeedbackPostgres(t *testing.T) { feedbackContract(t, true) }
func feedbackContract(t *testing.T, postgres bool) {
	repo, k := ownerRepository(t, postgres)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ctx := t.Context()
	sub := ownerSubscription(t, repo, k, now)
	scope := productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID, TenantID: k.TenantID}
	queue := func(s productemail.Subscription) productemail.Delivery {
		t.Helper()
		if e := repo.Enqueue(ctx, s, []api.ProductNotification{item(now, "work_failed")}, now); e != nil {
			t.Fatal(e)
		}
		jobs, e := repo.Claim(ctx, now)
		if e != nil || len(jobs) != 1 {
			t.Fatal(jobs, e)
		}
		return jobs[0]
	}
	// A signed provider event may beat the provider's HTTP acceptance response.
	d := queue(sub)
	if err := repo.Finish(ctx, d, "delivered", "", now, now); !errors.Is(err, productemail.ErrConflict) {
		t.Fatal("ordinary send forged delivery evidence", err)
	}
	provider := uuid.NewString()
	f := productemail.Feedback{ProviderID: provider, Kind: "bounced", OccurredAt: now, HardBounce: true}
	if err := repo.Feedback(ctx, f, now); err != nil {
		t.Fatal(err)
	}
	fresh, err := repo.Get(ctx, k)
	if err != nil || fresh.PauseReason != "" {
		t.Fatal("unmatched event paused recipient", fresh.PauseReason, err)
	}
	if err = repo.Accept(ctx, d, provider, now); err != nil {
		t.Fatal(err)
	}
	fresh, err = repo.Get(ctx, k)
	if err != nil || fresh.PauseReason != "hard_bounce" {
		t.Fatal("early event lost", fresh.PauseReason, err)
	}
	page, err := repo.OwnerPage(ctx, scope, "bounced", "", now)
	if err != nil || len(page.Deliveries) != 1 || page.Counts.Bounced != 1 || page.Deliveries[0].ReportedAt == nil || page.Deliveries[0].RetryEligible {
		t.Fatal(page, err)
	}
	page.Mode = "resend"
	if err = productemail.ValidateOwnerPage(page, k.TenantID, "bounced", ""); err != nil {
		t.Fatal(err)
	}
	// Delivery events arriving after a bounce cannot revive a message or consent.
	if err = repo.Feedback(ctx, productemail.Feedback{ProviderID: provider, Kind: "delivered", OccurredAt: now.Add(time.Second)}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	f.Kind, f.HardBounce, f.OccurredAt = "complained", false, now.Add(2*time.Second)
	var wg sync.WaitGroup
	errorsCh := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errorsCh <- repo.Feedback(ctx, f, now.Add(3*time.Second)) })
	}
	wg.Wait()
	close(errorsCh)
	for e := range errorsCh {
		if e != nil {
			t.Fatal(e)
		}
	}
	rows, err := repo.Recent(ctx, k)
	if err != nil || len(rows) != 1 || rows[0].Status != "complained" || rows[0].Attempts != 1 || rows[0].ReportedAt == nil || !rows[0].ReportedAt.Equal(f.OccurredAt) {
		t.Fatal(rows, err)
	}
	if err = repo.Finish(ctx, d, "provider_accepted", "", now, now); !errors.Is(err, productemail.ErrConflict) {
		t.Fatal("stale send erased feedback", err)
	}
	// Preference changes cannot clear a complaint, even across off/on saves.
	sub.Work, sub.Help = false, false
	sub, err = repo.Put(ctx, sub, sub.Version, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	sub.Work = true
	sub.ExpiresAt = now.Add(time.Hour)
	sub.Grant = []byte("encrypted-renewal")
	sub, err = repo.Put(ctx, sub, sub.Version, now.Add(5*time.Second))
	if err != nil || sub.PauseReason != "complaint" {
		t.Fatal("preferences cleared suppression", sub.PauseReason, err)
	}
	if err = repo.Enqueue(ctx, sub, []api.ProductNotification{item(now.Add(time.Minute), "work_succeeded")}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if rows, err = repo.Recent(ctx, k); err != nil || len(rows) != 1 {
		t.Fatal("paused subscription queued mail", rows, err)
	}
	// Another personal subscription, even in the same customer workspace, remains independent.
	other := k
	other.ActorID = uuid.NewString()
	otherSub := ownerSubscription(t, repo, other, now)
	second := queue(otherSub)
	if err = repo.Accept(ctx, second, provider, now); !errors.Is(err, productemail.ErrConflict) {
		t.Fatal("provider receipt rebound to another person", err)
	}
	otherProvider := uuid.NewString()
	if err = repo.Accept(ctx, second, otherProvider, now); err != nil {
		t.Fatal(err)
	}
	if err = repo.Feedback(ctx, productemail.Feedback{ProviderID: otherProvider, Kind: "bounced", OccurredAt: now}, now); err != nil {
		t.Fatal(err)
	}
	otherCurrent, err := repo.Get(ctx, other)
	if err != nil || otherCurrent.PauseReason != "" {
		t.Fatal("nonpermanent bounce paused recipient", otherCurrent.PauseReason, err)
	}
	// A claimed job is stopped by the final ready check after another mail bounces.
	leased := queue(otherCurrent)
	if err = repo.Enqueue(ctx, otherCurrent, []api.ProductNotification{item(now, "work_succeeded")}, now); err != nil {
		t.Fatal(err)
	}
	if err = repo.Feedback(ctx, productemail.Feedback{ProviderID: otherProvider, Kind: "bounced", HardBounce: true, OccurredAt: now}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Ready(ctx, leased, now); !errors.Is(err, productemail.ErrSuppressed) {
		t.Fatal("paused recipient still sendable", err)
	}
	if err = repo.Finish(ctx, leased, "cancelled", "recipient_suppressed", now, now); err != nil {
		t.Fatal(err)
	}
	otherRows, err := repo.Recent(ctx, other)
	if err != nil || len(otherRows) != 3 {
		t.Fatal(otherRows, err)
	}
	cancelled := 0
	for _, row := range otherRows {
		if row.Status == "cancelled" && row.Reason == "recipient_suppressed" {
			cancelled++
		}
	}
	if cancelled != 2 {
		t.Fatal("queued or leased notification escaped suppression", otherRows)
	}
}

func TestProductEmailPendingFeedbackExpires(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			repo, k := ownerRepository(t, postgres)
			now := time.Now().UTC().Truncate(time.Microsecond)
			sub := ownerSubscription(t, repo, k, now)
			provider := uuid.NewString()
			// Leave the target outside one bounded cleanup batch. Receipt
			// matching must enforce expiry even before those rows are pruned.
			for range api.ProductControlEmailPruneSize {
				if err := repo.Feedback(t.Context(), productemail.Feedback{ProviderID: uuid.NewString(), Kind: "delivered", OccurredAt: now}, now); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.Feedback(t.Context(), productemail.Feedback{ProviderID: provider, Kind: "complained", OccurredAt: now}, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := repo.Enqueue(t.Context(), sub, []api.ProductNotification{item(now, "work_failed")}, now); err != nil {
				t.Fatal(err)
			}
			jobs, err := repo.Claim(t.Context(), now)
			if err != nil || len(jobs) != 1 {
				t.Fatal(jobs, err)
			}
			if err = repo.Accept(t.Context(), jobs[0], provider, now.Add(api.ProductControlEmailPendingFeedbackTTL+2*time.Second)); err != nil {
				t.Fatal(err)
			}
			_, d, err := repo.OwnerGet(t.Context(), productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID, TenantID: k.TenantID}, jobs[0].ID, now.Add(api.ProductControlEmailPendingFeedbackTTL+time.Second))
			if err != nil || d.Status != "provider_accepted" {
				t.Fatal("expired unmatched event applied", d.Status, err)
			}
		})
	}
}
