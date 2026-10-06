package conformance

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func recoveryService(t *testing.T, fx *Fixture) {
	t.Helper()
	manifest := fx.App.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	if _, err := fx.Store.UpdateApp(fx.Ctx, fx.App.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
}

// ADR-420: claims, cooldown and stale-worker fencing must hold on both stores.
func testServiceRecoveryClaims(t *testing.T, fx *Fixture) {
	recoveryService(t, fx)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	if _, err := fx.Store.ServiceRecoveryByApp(fx.Ctx, fx.App.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing ledger: %v", err)
	}
	const contenders = 8
	var wg sync.WaitGroup
	winners := make(chan state.ServiceRecovery, contenders)
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, claimed, err := fx.Store.ClaimServiceRecovery(fx.Ctx, fx.App.ID, "rev1", uuid.NewString(), now, now.Add(time.Minute))
			if err != nil {
				t.Error(err)
			}
			if claimed {
				winners <- r
			}
		}()
	}
	wg.Wait()
	close(winners)
	if len(winners) != 1 {
		t.Fatalf("claim winners: %d, want 1", len(winners))
	}
	r := <-winners
	oldToken := r.ClaimToken
	if _, claimed, err := fx.Store.ClaimServiceRecovery(fx.Ctx, fx.App.ID, "rev2", uuid.NewString(), now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("revision must not steal an active lease: claimed=%v err=%v", claimed, err)
	}
	r.Status, r.Failures, r.UpdatedAt, r.NextAttemptAt = "retrying_startup", 3, now, now.Add(20*time.Second)
	if err := fx.Store.CompleteServiceRecovery(fx.Ctx, oldToken, r); err != nil {
		t.Fatal(err)
	}
	stored, err := fx.Store.ServiceRecoveryByApp(fx.Ctx, fx.App.ID)
	if err != nil || stored.Status != "retrying_startup" || stored.Failures != 3 || !stored.NextAttemptAt.Equal(now.Add(20*time.Second)) || stored.ClaimToken != "" {
		t.Fatalf("persisted retry: %+v err=%v", stored, err)
	}
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, "", now.Add(19*time.Second), 10); err != nil || len(ids) != 0 {
		t.Fatalf("retry not due: %v err=%v", ids, err)
	}
	if _, claimed, err := fx.Store.ClaimServiceRecovery(fx.Ctx, fx.App.ID, "rev1", uuid.NewString(), now.Add(time.Second), now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("event bypassed persisted cooldown: claimed=%v err=%v", claimed, err)
	}
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, "", now.Add(20*time.Second), 10); err != nil || !slices.Equal(ids, []string{fx.App.ID}) {
		t.Fatalf("retry due: %v err=%v", ids, err)
	}
	newToken := uuid.NewString()
	r, claimed, err := fx.Store.ClaimServiceRecovery(fx.Ctx, fx.App.ID, "rev2", newToken, now.Add(time.Second), now.Add(time.Minute))
	if err != nil || !claimed || r.Failures != 0 {
		t.Fatalf("new intent resets cooldown: %+v claimed=%v err=%v", r, claimed, err)
	}
	r.Status, r.NextAttemptAt = "ready", now.Add(30*time.Second)
	if err := fx.Store.CompleteServiceRecovery(fx.Ctx, oldToken, r); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale completion: %v", err)
	}
	// A process can die without completing. Another scheduler reclaims the
	// expired lease; its predecessor cannot overwrite the new retry state.
	r, claimed, err = fx.Store.ClaimServiceRecovery(fx.Ctx, fx.App.ID, "rev2", uuid.NewString(), now.Add(time.Minute), now.Add(2*time.Minute))
	if err != nil || !claimed {
		t.Fatalf("expired lease not reclaimed: claimed=%v err=%v", claimed, err)
	}
	r.Status, r.Failures, r.NextAttemptAt = "ready", 0, now.Add(90*time.Second)
	if err := fx.Store.CompleteServiceRecovery(fx.Ctx, newToken, r); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker completion: %v", err)
	}
	if err := fx.Store.CompleteServiceRecovery(fx.Ctx, r.ClaimToken, r); err != nil {
		t.Fatal(err)
	}
	r.Status = "arbitrary_error_text"
	if err := fx.Store.CompleteServiceRecovery(fx.Ctx, r.ClaimToken, r); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unbounded status accepted: %v", err)
	}
}

// ADR-420: discovery must neither revive stopped workloads nor cross owners.
func testServiceRecoveryCandidates(t *testing.T, fx *Fixture) {
	now := time.Now().UTC()
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("request-mode app selected: %v %v", ids, err)
	}
	recoveryService(t, fx)
	if err := fx.Store.SetAppNodeID(fx.Ctx, fx.App.ID, fx.Node.ID); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, uuid.NewString(), now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("foreign app selected: %v %v", ids, err)
	}
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, fx.Node.ID, now, 1); err != nil || !slices.Equal(ids, []string{fx.App.ID}) {
		t.Fatalf("owned app omitted: %v %v", ids, err)
	}
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, "", now, 0); err != nil || len(ids) != 0 {
		t.Fatalf("zero batch limit: %v %v", ids, err)
	}
	inactive := state.AppEvictedCold
	if _, err := fx.Store.UpdateApp(fx.Ctx, fx.App.ID, state.UpdateAppParams{Status: &inactive}); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("inactive app selected: %v %v", ids, err)
	}
	if _, claimed, err := fx.Store.ClaimServiceRecovery(fx.Ctx, fx.App.ID, "r", uuid.NewString(), now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("inactive app claimed: %v %v", claimed, err)
	}
	active := state.AppActive
	if _, err := fx.Store.UpdateApp(fx.Ctx, fx.App.ID, state.UpdateAppParams{Status: &active}); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateAccountStatus(fx.Ctx, fx.Account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("suspended account selected: %v %v", ids, err)
	}
	if _, claimed, err := fx.Store.ClaimServiceRecovery(fx.Ctx, fx.App.ID, "r", uuid.NewString(), now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("suspended account claimed: %v %v", claimed, err)
	}
	if err := fx.Store.UpdateAccountStatus(fx.Ctx, fx.Account.ID, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 1 {
		t.Fatalf("past-due grace should preserve service: %v %v", ids, err)
	}
	holds, ok := fx.Store.(state.AccountAbuseHoldStore)
	if !ok {
		t.Fatal("store does not implement account abuse holds")
	}
	if _, err := holds.SetAccountAbuseHold(fx.Ctx, fx.Account.ID, state.AccountAbuseHoldOperator, now); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListServiceRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("abuse-held account selected: %v %v", ids, err)
	}
	if _, claimed, err := fx.Store.ClaimServiceRecovery(fx.Ctx, fx.App.ID, "r", uuid.NewString(), now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("abuse-held account claimed: %v %v", claimed, err)
	}
}
