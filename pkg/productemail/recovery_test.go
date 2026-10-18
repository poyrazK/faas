package productemail_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func recoveryRequest(sub productemail.Subscription) api.ProductEmailRecoveryRequest {
	yes := true
	return api.ProductEmailRecoveryRequest{ID: uuid.NewString(), ExpectedVersion: sub.Version, ExpectedGeneration: sub.Generation, WorkOutcomes: &yes, HelpReplies: &yes}
}
func TestProductEmailRecoveryContract(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			repo, k := ownerRepository(t, pg)
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Microsecond)
			sub := ownerSubscription(t, repo, k, now)
			queue := func(s productemail.Subscription, at time.Time) productemail.Delivery {
				t.Helper()
				if err := repo.Enqueue(ctx, s, []api.ProductNotification{item(at, "work_failed")}, at); err != nil {
					t.Fatal(err)
				}
				jobs, err := repo.Claim(ctx, at)
				if err != nil || len(jobs) != 1 {
					t.Fatal(jobs, err)
				}
				return jobs[0]
			}
			old := queue(sub, now)
			old.RecipientSealed = []byte("encrypted-old-address-evidence")
			provider := uuid.NewString()
			if err := repo.Accept(ctx, old, provider, now); err != nil {
				t.Fatal(err)
			}
			bounce := productemail.Feedback{ProviderID: provider, Kind: "bounced", HardBounce: true, OccurredAt: now}
			if err := repo.Feedback(ctx, bounce, now); err != nil {
				t.Fatal(err)
			}
			sub, err := repo.Get(ctx, k)
			if err != nil || productemail.RecoveryBlocked(sub) != "verified_replacement_required" {
				t.Fatal(sub, err)
			}
			sub.PauseRecipient[0] = 'X'
			fresh, _ := repo.Get(ctx, k)
			if string(fresh.PauseRecipient) != "encrypted-old-address-evidence" {
				t.Fatal("mutable pause evidence")
			}
			sub = fresh
			req := recoveryRequest(sub)
			sub.Grant = []byte("fresh-authorized-grant")
			sub.ExpiresAt = now.Add(time.Hour)
			var wg sync.WaitGroup
			results := make(chan api.ProductEmailRecoveryReceipt, 8)
			failures := make(chan error, 8)
			for range 8 {
				wg.Go(func() { v, e := repo.Recover(ctx, sub, req, now.Add(time.Second)); results <- v; failures <- e })
			}
			wg.Wait()
			close(results)
			close(failures)
			for e := range failures {
				if e != nil {
					t.Fatal(e)
				}
			}
			var receipt api.ProductEmailRecoveryReceipt
			for v := range results {
				if !productemail.MatchesRecovery(v, req) || v.Generation != 2 || v.Version != 2 {
					t.Fatal(v)
				}
				receipt = v
			}
			current, err := repo.Get(ctx, k)
			if err != nil || current.PauseReason != "" || len(current.PauseRecipient) != 0 || current.Generation != 2 || !current.WorkSince.Equal(receipt.RecoveredAt) || !current.HelpSince.Equal(receipt.RecoveredAt) {
				t.Fatal(current, err)
			}
			conflict := req
			conflict.ExpectedVersion++
			if _, e := repo.Recover(ctx, sub, conflict, now.Add(2*time.Second)); !errors.Is(e, productemail.ErrConflict) {
				t.Fatal("changed request recovered receipt", e)
			}
			if err := repo.Enqueue(ctx, current, []api.ProductNotification{item(now, "work_succeeded")}, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			rows, _ := repo.Recent(ctx, k)
			if len(rows) != 1 {
				t.Fatal("replayed pre-recovery update", rows)
			}
			// A late bounce for the old generation must not cancel a new queued message.
			if err := repo.Enqueue(ctx, current, []api.ProductNotification{item(now.Add(3*time.Second), "work_succeeded")}, now.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := repo.Feedback(ctx, bounce, now.Add(4*time.Second)); err != nil {
				t.Fatal(err)
			}
			jobs, err := repo.Claim(ctx, now.Add(4*time.Second))
			if err != nil || len(jobs) != 1 {
				t.Fatal("old-address bounce cancelled replacement", jobs, err)
			}
			replacement := jobs[0]
			replacement.RecipientSealed = []byte("encrypted-replacement-evidence")
			newProvider := uuid.NewString()
			if err = repo.Accept(ctx, replacement, newProvider, now.Add(4*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err = repo.Feedback(ctx, productemail.Feedback{ProviderID: newProvider, Kind: "bounced", HardBounce: true, OccurredAt: now}, now.Add(5*time.Second)); err != nil {
				t.Fatal(err)
			}
			current, _ = repo.Get(ctx, k)
			if current.PauseReason != "hard_bounce" || current.Generation != 2 {
				t.Fatal("new bounce failed to pause", current)
			}
			// Even a complaint from the old address is held for review, including after recovery.
			if err = repo.Feedback(ctx, productemail.Feedback{ProviderID: provider, Kind: "complained", OccurredAt: now}, now.Add(6*time.Second)); err != nil {
				t.Fatal(err)
			}
			current, _ = repo.Get(ctx, k)
			if current.PauseReason != "complaint" {
				t.Fatal(current)
			}
			if v, e := repo.Recover(ctx, sub, req, now.Add(7*time.Second)); e != nil || v != receipt {
				t.Fatal("lost-response replay failed", v, e)
			}
			current, _ = repo.Get(ctx, k)
			if current.PauseReason != "complaint" {
				t.Fatal("old receipt cleared new hold")
			}
			if _, e := repo.Recover(ctx, current, recoveryRequest(current), now.Add(7*time.Second)); !errors.Is(e, productemail.ErrConflict) {
				t.Fatal("complaint recovered", e)
			}
			other := k
			other.ActorID = uuid.NewString()
			ownerSubscription(t, repo, other, now)
			if _, e := repo.RecoveryReceipt(ctx, other, req.ID, now); !errors.Is(e, productemail.ErrNotFound) {
				t.Fatal("cross-person receipt", e)
			}
			scope := productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID, TenantID: k.TenantID}
			page, e := repo.Recoveries(ctx, scope, k.ID(), "", now.Add(8*time.Second))
			if e != nil || len(page.Receipts) != 1 || page.Receipts[0] != receipt {
				t.Fatal(page, e)
			}
			if e = productemail.ValidateRecoveryPage(page, k.TenantID, ""); e != nil {
				t.Fatal(e)
			}
			page, e = repo.Recoveries(ctx, scope, other.ID(), "", now.Add(8*time.Second))
			if e != nil || len(page.Receipts) != 0 {
				t.Fatal(page, e)
			}
		})
	}
}
func TestProductEmailRecoveryBoundsAndPagination(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			repo, k := ownerRepository(t, pg)
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Microsecond)
			sub := ownerSubscription(t, repo, k, now)
			for i := 0; i <= api.ProductControlEmailRecoveryLimit; i++ {
				if err := repo.Enqueue(ctx, sub, []api.ProductNotification{item(now.Add(time.Second), "work_succeeded")}, now); err != nil {
					t.Fatal(err)
				}
				jobs, e := repo.Claim(ctx, now)
				if e != nil || len(jobs) != 1 {
					t.Fatal(jobs, e)
				}
				d := jobs[0]
				d.RecipientSealed = []byte("encrypted-evidence")
				provider := uuid.NewString()
				if e = repo.Accept(ctx, d, provider, now); e != nil {
					t.Fatal(e)
				}
				if e = repo.Feedback(ctx, productemail.Feedback{ProviderID: provider, Kind: "bounced", HardBounce: true, OccurredAt: now}, now); e != nil {
					t.Fatal(e)
				}
				sub, e = repo.Get(ctx, k)
				if e != nil {
					t.Fatal(e)
				}
				r := recoveryRequest(sub)
				_, e = repo.Recover(ctx, sub, r, now)
				if i == api.ProductControlEmailRecoveryLimit {
					if !errors.Is(e, productemail.ErrLimit) {
						t.Fatal("unbounded recoveries", e)
					}
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				sub, e = repo.Get(ctx, k)
				if e != nil {
					t.Fatal(e)
				}
			}
			scope := productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID, TenantID: k.TenantID}
			p, e := repo.Recoveries(ctx, scope, "", "", now)
			if e != nil || len(p.Receipts) != api.ProductControlEmailOwnerPageSize || p.NextCursor == "" {
				t.Fatal(p, e)
			}
			if e = productemail.ValidateRecoveryPage(p, k.TenantID, ""); e != nil {
				t.Fatal(e)
			}
			q, e := repo.Recoveries(ctx, scope, "", p.NextCursor, now)
			if e != nil || len(q.Receipts) != api.ProductControlEmailOwnerPageSize || q.NextCursor != "" {
				t.Fatal(q, e)
			}
			if e = productemail.ValidateRecoveryPage(q, k.TenantID, p.NextCursor); e != nil {
				t.Fatal(e)
			}
			if _, e = repo.Recoveries(ctx, scope, k.ID(), p.NextCursor, now); !errors.Is(e, productemail.ErrInvalid) {
				t.Fatal("cursor changed recipient scope", e)
			}
			if _, e = repo.RecoveryReceipt(ctx, k, p.Receipts[0].ID, now.Add(api.ProductControlNotificationWindow+time.Second)); !errors.Is(e, productemail.ErrNotFound) {
				t.Fatal("expired recovery retained", e)
			}
		})
	}
}

func TestProductEmailConsentSerialization(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			repo, k := ownerRepository(t, pg)
			now := time.Now().UTC()
			ownerSubscription(t, repo, k, now)
			var wg sync.WaitGroup
			errs := make(chan error, 8)
			for range 8 {
				wg.Go(func() {
					errs <- repo.WithConsent(t.Context(), k, func() error {
						sub, e := repo.Get(t.Context(), k)
						if e != nil {
							return e
						}
						_, e = repo.Put(t.Context(), sub, sub.Version, now)
						return e
					})
				})
			}
			wg.Wait()
			close(errs)
			for e := range errs {
				if e != nil {
					t.Fatal(e)
				}
			}
			sub, e := repo.Get(t.Context(), k)
			if e != nil || sub.Version != 9 {
				t.Fatal("overlapping consent mutations", sub.Version, e)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- repo.WithConsent(t.Context(), k, func() error { close(entered); <-release; return nil })
			}()
			<-entered
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if e := repo.WithConsent(ctx, k, func() error { return errors.New("cancelled consent ran") }); !errors.Is(e, context.Canceled) {
				t.Error(e)
			}
			close(release)
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if e := repo.WithConsent(t.Context(), k, func() error { return nil }); e != nil {
				t.Fatal("consent lock leaked", e)
			}
		})
	}
}
