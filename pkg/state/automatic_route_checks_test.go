package state_test

// ADR-449: durable handoffs, superseded leases and independent freshness.

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func automaticCheckFingerprint(snapshot state.RoutePolicySnapshot) string {
	context := routerequirements.Context{Host: snapshot.App.Slug + ".gregale.dev", Rules: snapshot.Rules, App: api.AppResponse{ID: snapshot.App.ID, Slug: snapshot.App.Slug, ConsumerAuthMode: string(snapshot.App.ConsumerAuthMode), EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}}}
	return routerequirements.ConfigurationFingerprint(context, string(snapshot.Account.Plan))
}

func finishAutomaticCheck(t *testing.T, store state.Store, claim state.AutomaticRouteCheckClaim) bool {
	t.Helper()
	var captureSHA string
	var truncated bool
	checker := testSavedRequirementsChecker(claim.DeploymentID)
	result, err := store.(state.RouteRequirementsStore).CheckRouteRequirements(t.Context(), claim.AccountID, claim.AppID, api.CheckRouteRequirementsRequest{DeploymentID: claim.DeploymentID}, func(snapshot state.RoutePolicySnapshot, saved api.SavedRouteRequirements) (api.RouteRequirementsCheck, error) {
		captureSHA, truncated = state.RouteCheckCaptureIdentity(snapshot.Contract)
		return checker(snapshot, saved)
	})
	if err != nil {
		t.Fatal(err)
	}
	done, err := store.(state.AutomaticRouteCheckStore).CompleteAutomaticRouteCheck(t.Context(), claim, result, captureSHA, truncated)
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func TestAutomaticRouteCheckQueueAndFreshness(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			acct, app, policy, intent, saved := savedCheckFixture(t, store)
			queue := store.(state.AutomaticRouteCheckStore)
			lookup := func() api.AutomaticRouteCheck {
				result, err := queue.GetAutomaticRouteCheck(t.Context(), acct.ID, app.ID, policy.DeploymentID, automaticCheckFingerprint)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			initial := lookup()
			if initial.State != "pending" || initial.Check != nil || initial.Freshness != "unavailable" {
				t.Fatalf("intent did not enqueue retained capture: %+v", initial)
			}
			claim, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			if err != nil || lookup().State != "running" {
				t.Fatalf("claim: %+v %v", claim, err)
			}
			if _, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("leased work claimed twice")
			}
			if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), policy.DeploymentID, acct.ID, app.ID, []byte(routeGroupContract), "cold_boot", false); err != nil {
				t.Fatal(err)
			}
			if err := queue.QueueAutomaticRouteCheck(t.Context(), acct.ID, app.ID, policy.DeploymentID); err != nil || !finishAutomaticCheck(t, store, claim) {
				t.Fatal("identical capture or duplicate refresh invalidated current work")
			}
			completed := lookup()
			if completed.State != "complete" || completed.Freshness != "current" || completed.Check.Report.Status != "violated" || completed.CheckedAt == nil {
				t.Fatalf("result: %+v", completed)
			}
			// Configuration edits invalidate the historical result even with no
			// capture or intent write; refresh evaluates the new policy.
			if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: ptrConsumerAuth(api.ConsumerAuthModeRequired)}); err != nil {
				t.Fatal(err)
			}
			stale := lookup()
			if stale.Freshness != "stale" || strings.Join(stale.StaleReasons, ",") != "configuration_changed" {
				t.Fatalf("policy drift reused old pass: %+v", stale)
			}
			if err := queue.QueueAutomaticRouteCheck(t.Context(), acct.ID, app.ID, policy.DeploymentID); err != nil {
				t.Fatal(err)
			}
			claim, err = queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			if err != nil || !finishAutomaticCheck(t, store, claim) || lookup().Freshness != "current" {
				t.Fatalf("refresh: %v", err)
			}
			// A new intent supersedes a worker already holding an old lease.
			if err := queue.QueueAutomaticRouteCheck(t.Context(), acct.ID, app.ID, policy.DeploymentID); err != nil {
				t.Fatal(err)
			}
			oldClaim, _ := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			policy.Requirements.Groups[0].Name = "checkout-new"
			if _, err := intent.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &saved.Revision, Requirements: policy.Requirements}); err != nil {
				t.Fatal(err)
			}
			if finishAutomaticCheck(t, store, oldClaim) {
				t.Fatal("superseded intent lease published evidence")
			}
			if lookup().Freshness != "stale" || lookup().State != "pending" {
				t.Fatal("changed intent did not queue and invalidate old evidence")
			}
			claim, _ = queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			if !finishAutomaticCheck(t, store, claim) {
				t.Fatal("new intent could not complete")
			}
			if err := store.DeleteDeploymentOpenAPIDoc(t.Context(), policy.DeploymentID, acct.ID); err != nil {
				t.Fatal(err)
			}
			if lookup().Freshness != "stale" || lookup().State != "pending" {
				t.Fatal("capture deletion did not invalidate and enqueue")
			}
			claim, _ = queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			if !finishAutomaticCheck(t, store, claim) || lookup().Check.Report.Status != "unknown" || lookup().Freshness != "current" {
				t.Fatal("missing capture passed or did not complete as incomplete evidence")
			}
			other, _ := store.CreateAccount(t.Context(), "foreign-automatic@example.com", api.PlanPro)
			if _, err := queue.GetAutomaticRouteCheck(t.Context(), other.ID, app.ID, policy.DeploymentID, automaticCheckFingerprint); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign stored result exposed")
			}
			if err := queue.QueueAutomaticRouteCheck(t.Context(), other.ID, app.ID, policy.DeploymentID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign refresh accepted")
			}
			if err := queue.QueueAutomaticRouteCheck(t.Context(), acct.ID, app.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("missing deployment refresh accepted")
			}
			if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanFree); err != nil {
				t.Fatal(err)
			}
			if _, err := queue.GetAutomaticRouteCheck(t.Context(), acct.ID, app.ID, policy.DeploymentID, automaticCheckFingerprint); !errors.Is(err, state.ErrAutomaticRouteCheckPlan) {
				t.Fatalf("downgraded plan exposed historical routes: %v", err)
			}
		})
	}
}

func ptrConsumerAuth(value string) *string { return &value }

func TestAutomaticRouteCheckLeaseRetryAndConcurrentClaims(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			acct, app, policy, _, _ := savedCheckFixture(t, store)
			queue := store.(state.AutomaticRouteCheckStore)
			var wg sync.WaitGroup
			claims := make(chan state.AutomaticRouteCheckClaim, 2)
			for range 2 {
				wg.Go(func() {
					claim, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
					if err == nil {
						claims <- claim
					} else if !errors.Is(err, state.ErrNotFound) {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			close(claims)
			if len(claims) != 1 {
				t.Fatal("concurrent claim was not exclusive")
			}
			first := <-claims
			if !finishAutomaticCheck(t, store, first) {
				t.Fatal("exclusive claim could not publish")
			}
			if err := queue.QueueAutomaticRouteCheck(t.Context(), acct.ID, app.ID, policy.DeploymentID); err != nil {
				t.Fatal(err)
			}
			old, err := queue.ClaimAutomaticRouteCheck(t.Context(), 10*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(25 * time.Millisecond)
			claim, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			if err != nil || claim.RequestID != old.RequestID || claim.LeaseToken == old.LeaseToken || claim.Attempts != 2 || finishAutomaticCheck(t, store, old) {
				t.Fatalf("expired lease fenced incorrectly: %+v %v", claim, err)
			}
			if done, err := queue.FailAutomaticRouteCheck(t.Context(), claim); err != nil || !done {
				t.Fatal("failure not scheduled")
			}
			result, err := queue.GetAutomaticRouteCheck(t.Context(), acct.ID, app.ID, policy.DeploymentID, automaticCheckFingerprint)
			if err != nil || result.State != "retrying" || result.LastErrorCode != "check_failed" || result.NextAttemptAt == nil || !result.NextAttemptAt.After(time.Now()) {
				t.Fatalf("retry state: %+v %v", result, err)
			}
			if _, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("retry backoff ignored")
			}
			if err := queue.QueueAutomaticRouteCheck(t.Context(), acct.ID, app.ID, policy.DeploymentID); err != nil {
				t.Fatal(err)
			}
			claim, err = queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			if err != nil || claim.Attempts != 1 || !finishAutomaticCheck(t, store, claim) {
				t.Fatalf("explicit refresh did not reset failed work: %+v %v", claim, err)
			}
		})
	}
}
