// Concurrent createApp quota enforcement tests (PR fix for the TOCTOU
// in handlers.go::createApp). MemStore serializes under m.mu so the
// race is impossible there in production, but the test guards against
// future regressions (e.g. someone adding a pre-check that reads then
// inserts without holding the lock). PgStore tests live in
// pkg/state/pgstore_test.go because they need a real Postgres.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestCreateApp_ConcurrentQuotaEnforcement_MemStore fires N goroutines
// at POST /v1/apps on a Free account (cap = 1). Exactly one must
// succeed with 201, the rest must surface 403 CodePlanLimitApps. The
// handler no longer reads-then-inserts; CreateAppIfUnderQuota holds the
// critical section for both the count and the insert, so this asserts
// the wire contract — even if MemStore's mutex already made the race
// unreachable, the handler-level invariant is what matters.
func TestCreateApp_ConcurrentQuotaEnforcement_MemStore(t *testing.T) {
	const n = 10
	e := setup(t, api.PlanFree) // Free.DeployedApps = 1

	var (
		ok       atomic.Int32
		quota403 atomic.Int32
		other    atomic.Int32
		wg       sync.WaitGroup
	)
	wg.Add(n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once to maximise contention
			req := httptest.NewRequest("POST", "/v1/apps",
				strings.NewReader(fmt.Sprintf(`{"slug":"app-%d"}`, i)))
			req.Header.Set("Authorization", "Bearer "+e.key)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, req)
			switch rec.Code {
			case http.StatusCreated:
				ok.Add(1)
			case http.StatusForbidden:
				var p api.Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &p); err == nil && p.Code == api.CodePlanLimitApps {
					quota403.Add(1)
					return
				}
				other.Add(1)
				t.Logf("non-quota 403: %d %s", rec.Code, rec.Body)
			default:
				other.Add(1)
				t.Logf("unexpected status: %d %s", rec.Code, rec.Body)
			}
		}()
	}
	close(start)
	wg.Wait()

	if ok.Load() != 1 {
		t.Errorf("expected exactly one 201 under cap=1, got %d", ok.Load())
	}
	if quota403.Load() != n-1 {
		t.Errorf("expected %d 403 plan_limit_apps, got %d", n-1, quota403.Load())
	}
	if other.Load() != 0 {
		t.Errorf("got %d unexpected statuses", other.Load())
	}
	// The store-side ground truth: cap was enforced end-to-end, not
	// just at the wire.
	storeN, err := e.store.CountDeployedApps(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("CountDeployedApps: %v", err)
	}
	if storeN != 1 {
		t.Errorf("store holds %d apps, want 1", storeN)
	}
}

// TestStore_CreateAppIfUnderQuota_MemStore covers the store method
// directly: quota breach returns *state.QuotaError with the observed
// count, slug collision returns ErrConflict, success returns the
// inserted App.
func TestStore_CreateAppIfUnderQuota_MemStore(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "q@example.com", api.PlanFree)
	limits := api.MustLimitsFor(acct.Plan) // DeployedApps=1 on Free

	app := state.App{
		AccountID: acct.ID, Slug: "first", Type: state.AppTypeApp,
		RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive,
	}
	created, err := store.CreateAppIfUnderQuota(context.Background(), app, limits)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if created.ID == "" || created.Slug != "first" {
		t.Errorf("unexpected created: %+v", created)
	}

	// Second insert with a different slug must trip the cap.
	_, err = store.CreateAppIfUnderQuota(context.Background(),
		state.App{AccountID: acct.ID, Slug: "second", Type: state.AppTypeApp,
			RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive},
		limits)
	if err == nil {
		t.Fatalf("expected quota error, got nil")
	}
	var qe *state.QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *QuotaError, got %T: %v", err, err)
	}
	if qe.Limit != 1 || qe.Observed != 1 {
		t.Errorf("QuotaError = {Limit:%d, Observed:%d}, want {1,1}", qe.Limit, qe.Observed)
	}

	// Slug collision on a fresh account returns ErrConflict (separate
	// code path from the cap).
	otherAcct, _ := store.CreateAccount(context.Background(), "h@example.com", api.PlanHobby)
	hLimits := api.MustLimitsFor(otherAcct.Plan) // DeployedApps=5 on Hobby
	_, err = store.CreateAppIfUnderQuota(context.Background(),
		state.App{AccountID: otherAcct.ID, Slug: "first", Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive},
		hLimits)
	if err == nil {
		t.Fatalf("expected conflict, got nil")
	}
	if !errors.Is(err, state.ErrConflict) {
		t.Errorf("slug collision = %v, want ErrConflict", err)
	}
}