package productemail_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func preparedOptOut(t *testing.T, repo productemail.Store, sub productemail.Subscription, now time.Time) (productemail.Delivery, []byte) {
	t.Helper()
	if err := repo.Enqueue(t.Context(), sub, []api.ProductNotification{item(now.Add(time.Second), "work_succeeded")}, now); err != nil {
		t.Fatal(err)
	}
	jobs, err := repo.Claim(t.Context(), now)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	d := jobs[0]
	token, err := productemail.NewUnsubscribeToken()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := productemail.UnsubscribeHash(token)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := repo.PrepareUnsubscribe(t.Context(), d, hash, []byte("encrypted-link-fixture"), now)
	if err != nil || string(sealed) != "encrypted-link-fixture" {
		t.Fatal(sealed, err)
	}
	// A retry must reuse the original capability, even with newly proposed material.
	other := append([]byte{}, hash...)
	other[0] ^= 1
	sealed[0] = 'X'
	sealed, err = repo.PrepareUnsubscribe(t.Context(), d, other, []byte("different-fixture"), now)
	if err != nil || string(sealed) != "encrypted-link-fixture" {
		t.Fatal("unstable or mutable link", sealed, err)
	}
	if err = repo.Finish(t.Context(), d, "captured", "", now, now); err != nil {
		t.Fatal(err)
	}
	return d, hash
}

func TestProductEmailUnsubscribeContract(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			repo, k := ownerRepository(t, pg)
			ctx, now := t.Context(), time.Now().UTC().Truncate(time.Microsecond)
			sub := ownerSubscription(t, repo, k, now)
			other := k
			other.ActorID = uuid.NewString()
			ownerSubscription(t, repo, other, now)
			d, hash := preparedOptOut(t, repo, sub, now)
			items := []api.ProductNotification{item(now.Add(time.Second), "work_failed"), item(now.Add(time.Second), "help_replied")}
			if err := repo.Enqueue(ctx, sub, items, now); err != nil {
				t.Fatal(err)
			}
			leased, err := repo.Claim(ctx, now)
			if err != nil || len(leased) != 1 {
				t.Fatal(leased, err)
			}
			var wg sync.WaitGroup
			results := make(chan api.ProductEmailUnsubscribeResult, 8)
			failures := make(chan error, 8)
			for range 8 {
				wg.Go(func() { v, e := repo.Unsubscribe(ctx, hash, now.Add(time.Second)); results <- v; failures <- e })
			}
			wg.Wait()
			close(results)
			close(failures)
			for e := range failures {
				if e != nil {
					t.Fatal(e)
				}
			}
			var result api.ProductEmailUnsubscribeResult
			for v := range results {
				if !v.Current || v.ReceiptID == "" || result.ReceiptID != "" && result != v {
					t.Fatal("non-idempotent opt-out", result, v)
				}
				result = v
			}
			current, err := repo.Get(ctx, k)
			if err != nil || current.Work || current.Help || len(current.Grant) != 0 || !current.ExpiresAt.IsZero() || current.Version != sub.Version+1 || current.Generation != sub.Generation {
				t.Fatal(current, err)
			}
			if _, err = repo.Ready(ctx, leased[0], now.Add(time.Second)); !errors.Is(err, productemail.ErrConflict) {
				t.Fatal("leased job survived unsubscribe", err)
			}
			rows, err := repo.Recent(ctx, k)
			if err != nil || len(rows) != 3 {
				t.Fatal(rows, err)
			}
			cancelled := 0
			for _, row := range rows {
				if row.Status == "cancelled" && row.Reason == "unsubscribed" {
					cancelled++
				}
				if row.ID == d.ID && (row.Status != "captured" || row.Attempts != 1) {
					t.Fatal("changed accepted history", row)
				}
			}
			if cancelled != 1 {
				t.Fatal("pending job not cancelled immediately", rows)
			}
			if err = repo.Finish(ctx, leased[0], "cancelled", "preferences_changed", now, now); err != nil {
				t.Fatal(err)
			}
			unaffected, _ := repo.Get(ctx, other)
			if !unaffected.Work || unaffected.Version != 1 {
				t.Fatal("cross-person unsubscribe", unaffected)
			}
			scope := productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID, TenantID: k.TenantID}
			page, err := repo.OptOuts(ctx, scope, k.ID(), "", now.Add(time.Second))
			if err != nil || len(page.Receipts) != 1 || page.Receipts[0].ID != result.ReceiptID || page.Receipts[0].DeliveryID != d.ID || productemail.ValidateOptOutPage(page, k.TenantID, "") != nil {
				t.Fatal(page, err)
			}
			encoded, _ := json.Marshal(result)
			for _, private := range []string{k.AccountID, k.AppID, k.TenantID, k.ActorID, k.ID(), d.ID} {
				if bytes.Contains(encoded, []byte(private)) {
					t.Fatal("bearer response disclosed identity", string(encoded))
				}
			}
			current.Work, current.Help, current.Grant, current.ExpiresAt = true, true, []byte("fresh-encrypted-consent"), now.Add(time.Hour)
			current, err = repo.Put(ctx, current, current.Version, now.Add(2*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if err = repo.Enqueue(ctx, current, []api.ProductNotification{item(now, "work_succeeded"), item(now.Add(3*time.Second), "work_failed")}, now.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			replay, err := repo.Unsubscribe(ctx, hash, now.Add(4*time.Second))
			if err != nil || replay.Current || replay.ReceiptID != result.ReceiptID || !replay.UnsubscribedAt.Equal(result.UnsubscribedAt) {
				t.Fatal("old link failed to recover historical receipt", replay, err)
			}
			jobs, err := repo.Claim(ctx, now.Add(4*time.Second))
			if err != nil || len(jobs) != 1 || jobs[0].Kind != "work_failed" {
				t.Fatal("old link cancelled renewed consent or replayed old work", jobs, err)
			}
			if _, err = repo.Unsubscribe(ctx, hash, now.Add(api.ProductControlNotificationWindow)); !errors.Is(err, productemail.ErrNotFound) {
				t.Fatal("expired link accepted", err)
			}
			page, err = repo.OptOuts(ctx, scope, "", "", now.Add(api.ProductControlNotificationWindow))
			if err != nil || len(page.Receipts) != 0 {
				t.Fatal("history outlived source delivery", page, err)
			}
		})
	}
}

func TestProductEmailUnsubscribeStaleConsentAndHolds(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, reason := range []string{"new_consent", "hard_bounce", "complaint"} {
			t.Run(map[bool]string{false: "memory/", true: "postgres/"}[pg]+reason, func(t *testing.T) {
				repo, k := ownerRepository(t, pg)
				now := time.Now().UTC().Truncate(time.Microsecond)
				sub := ownerSubscription(t, repo, k, now)
				_, hash := preparedOptOut(t, repo, sub, now)
				if reason == "new_consent" {
					if _, err := repo.Put(t.Context(), sub, sub.Version, now); err != nil {
						t.Fatal(err)
					}
					if _, err := repo.Unsubscribe(t.Context(), hash, now); !errors.Is(err, productemail.ErrConflict) {
						t.Fatal("stale link overrode new consent", err)
					}
					return
				}
				if err := repo.Enqueue(t.Context(), sub, []api.ProductNotification{item(now.Add(time.Second), "work_failed")}, now); err != nil {
					t.Fatal(err)
				}
				jobs, err := repo.Claim(t.Context(), now)
				if err != nil || len(jobs) != 1 {
					t.Fatal(jobs, err)
				}
				d := jobs[0]
				d.RecipientSealed = []byte("encrypted-address-evidence")
				provider := uuid.NewString()
				if err = repo.Accept(t.Context(), d, provider, now); err != nil {
					t.Fatal(err)
				}
				f := productemail.Feedback{ProviderID: provider, Kind: "bounced", HardBounce: true, OccurredAt: now}
				if reason == "complaint" {
					f.Kind, f.HardBounce = "complained", false
				}
				if err = repo.Feedback(t.Context(), f, now); err != nil {
					t.Fatal(err)
				}
				if v, e := repo.Unsubscribe(t.Context(), hash, now); e != nil || !v.Current {
					t.Fatal(v, e)
				}
				current, _ := repo.Get(t.Context(), k)
				if current.Work || current.Help || current.PauseReason != reason || !bytes.Equal(current.PauseRecipient, d.RecipientSealed) {
					t.Fatal("unsubscribe cleared a delivery hold", current)
				}
			})
		}
	}
}

func TestProductEmailOptOutHistoryScopeAndPagination(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			repo, k := ownerRepository(t, pg)
			now := time.Now().UTC().Truncate(time.Microsecond)
			sub := ownerSubscription(t, repo, k, now)
			for range api.ProductControlEmailOwnerPageSize + 1 {
				_, hash := preparedOptOut(t, repo, sub, now)
				if _, err := repo.Unsubscribe(t.Context(), hash, now); err != nil {
					t.Fatal(err)
				}
				sub, _ = repo.Get(t.Context(), k)
				sub.Work, sub.Grant, sub.ExpiresAt = true, []byte("fresh-encrypted-consent"), now.Add(time.Hour)
				var err error
				sub, err = repo.Put(t.Context(), sub, sub.Version, now)
				if err != nil {
					t.Fatal(err)
				}
			}
			scope := productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID, TenantID: k.TenantID}
			p, err := repo.OptOuts(t.Context(), scope, "", "", now)
			if err != nil || len(p.Receipts) != api.ProductControlEmailOwnerPageSize || p.NextCursor == "" || productemail.ValidateOptOutPage(p, k.TenantID, "") != nil {
				t.Fatal(p, err)
			}
			q, err := repo.OptOuts(t.Context(), scope, "", p.NextCursor, now)
			if err != nil || len(q.Receipts) != 1 || q.NextCursor != "" || productemail.ValidateOptOutPage(q, k.TenantID, p.NextCursor) != nil {
				t.Fatal(q, err)
			}
			for _, recipient := range []string{k.ID(), uuid.NewString()} {
				if _, err = repo.OptOuts(t.Context(), scope, recipient, p.NextCursor, now); !errors.Is(err, productemail.ErrInvalid) {
					t.Fatal("cursor changed person scope", err)
				}
			}
			for _, field := range []string{"account", "app", "tenant"} {
				other := scope
				switch field {
				case "account":
					other.AccountID = uuid.NewString()
				case "app":
					other.AppID = uuid.NewString()
				case "tenant":
					other.TenantID = uuid.NewString()
				}
				page, e := repo.OptOuts(t.Context(), other, "", "", now)
				if e != nil || len(page.Receipts) != 0 {
					t.Fatal("foreign opt-outs disclosed", page, e)
				}
				if _, e = repo.OptOuts(t.Context(), other, "", p.NextCursor, now); !errors.Is(e, productemail.ErrInvalid) {
					t.Fatal("cursor changed owner scope", e)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, e := repo.Unsubscribe(ctx, make([]byte, 32), now); e == nil {
				t.Fatal("cancelled request accepted")
			}
			if _, e := repo.Unsubscribe(t.Context(), nil, now); !errors.Is(e, productemail.ErrInvalid) {
				t.Fatal(e)
			}
		})
	}
}

func TestProductEmailUnsubscribeCapabilityCanonical(t *testing.T) {
	token, err := productemail.NewUnsubscribeToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = productemail.UnsubscribeHash(token); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", token + "=", token[:42], token[:42] + "B", " " + token, token + "\n"} {
		if _, err = productemail.UnsubscribeHash(value); !errors.Is(err, productemail.ErrInvalid) {
			t.Fatal("noncanonical token accepted", err)
		}
	}
}
