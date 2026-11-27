package state_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAPISandboxPartnerRedemptionIsAtomic(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "partner-redemption@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: acct.ID, Slug: "api-sbx-partner-pg", RAMMB: 512,
		PreviewOfSlug: state.APISandboxPreviewParent("notes"), PreviewPrState: state.PreviewPrStateOpen,
		PreviewExpiresAt: &expiry, APISandboxPartnerLabel: "Partner A",
		Manifest: state.AppManifest{APISandboxSourceDeploymentID: uuid.NewString(), APISandboxPartner: true},
	}, api.MustLimitsFor(acct.Plan))
	if err != nil || !app.Manifest.APISandboxPartner {
		t.Fatalf("reserve partner copy: %v", err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, acct.ID, app.ID, api.APISandboxPartnerConsumerRef, "Partner")
	if err != nil {
		t.Fatal(err)
	}
	inviteHash := sha256.Sum256([]byte("one-time invite fixture"))
	if _, err := store.SetAPISandboxPartnerInvite(ctx, app.ID, inviteHash[:], now); err != nil {
		t.Fatal(err)
	}
	// An exhausted allowance rolls back the exchange, leaving the invite usable.
	makeRedemption := func() state.APISandboxPartnerRedemption {
		t.Helper()
		_, prefix, hash, err := api.GenerateConsumerKey()
		if err != nil {
			t.Fatal(err)
		}
		return state.APISandboxPartnerRedemption{
			App: app, ConsumerID: consumer.ID, KeyName: uuid.NewString(), Prefix: prefix, KeyHash: hash,
			InviteHash: inviteHash[:], At: now, Quota: state.ConsumerKeyQuota{PerApp: 1, PerAccount: 1},
		}
	}
	denied := makeRedemption()
	denied.Quota.PerApp = 0
	var exceeded *state.ConsumerKeyQuotaError
	if _, err := store.RedeemAPISandboxPartnerInvite(ctx, denied); !errors.As(err, &exceeded) {
		t.Fatalf("expected key quota error, got %v", err)
	}
	type result struct {
		key state.ConsumerKey
		err error
	}
	results := make(chan result, 2)
	for range 2 {
		request := makeRedemption()
		go func() {
			key, err := store.RedeemAPISandboxPartnerInvite(ctx, request)
			results <- result{key, err}
		}()
	}
	var key state.ConsumerKey
	successes := 0
	for range 2 {
		out := <-results
		switch {
		case out.err == nil:
			key = out.key
			successes++
		case errors.Is(out.err, state.ErrNotFound):
		default:
			t.Fatalf("concurrent exchange: %v", out.err)
		}
	}
	if successes != 1 {
		t.Fatalf("one invite created %d keys", successes)
	}
	if active, err := store.APISandboxPartnerKeyActive(ctx, app.ID, key.ID); err != nil || !active {
		t.Fatalf("issued key not bound to partner copy: %v", err)
	}
	if err := store.RecordAPISandboxPartnerSuccess(ctx, app.ID, key.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAPISandboxPartnerSuccess(ctx, app.ID, key.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	p, err := store.GetAPISandboxPartner(ctx, app.ID)
	if err != nil || p.FirstSuccessAt == nil || p.FirstSuccessAt.Sub(now) < 59*time.Second ||
		p.FirstSuccessAt.Sub(now) > 61*time.Second || p.MatchesInvite(inviteHash[:]) {
		t.Fatalf("partner result was not persisted: %v", err)
	}
	if err := store.RevokeAPISandboxPartner(ctx, app.ID, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if active, err := store.APISandboxPartnerKeyActive(ctx, app.ID, key.ID); err != nil || active {
		t.Fatalf("revoked binding still active: %v", err)
	}
	if stored, err := store.GetConsumerKeyByID(ctx, acct.ID, key.ID); err != nil || stored.RevokedAt == nil {
		t.Fatalf("consumer key was not revoked in the same transaction: %v", err)
	}
}
