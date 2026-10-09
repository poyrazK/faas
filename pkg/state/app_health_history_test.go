package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func appHealthStores(t *testing.T, test func(*testing.T, state.Store, state.AppHealthHistoryStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { s := state.NewMemStore(); test(t, s, s) })
	t.Run("postgres", func(t *testing.T) {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		s := state.NewPgStore(pool)
		test(t, s, s)
	})
}

func historyAssessment(appID, status string, at time.Time) api.AppHealthResponse {
	return api.AppHealthResponse{AppID: appID, Scope: "default", Status: status, Phase: "serving", Summary: "Recorded serving assessment.", EvaluatedAt: at.Format(time.RFC3339Nano), ValidForSeconds: 120,
		ServingDeploymentIDs: []string{"release"}, Capacity: api.AppHealthCapacity{Known: true, Required: 1, Ready: 1},
		Checks:   []api.AppHealthCheck{{Code: "requests", Status: "pass", Reason: "requests_observed", Detail: "Requests observed."}},
		Requests: &api.AppHealthRequests{Known: true, Coverage: "serving_deployments", RequestCount: 100, Policy: api.AppHealthRequestPolicy{MinimumRequests: 50}}}
}

func recordAppHealth(t *testing.T, s state.AppHealthHistoryStore, appID, status string, at time.Time, change func(*api.AppHealthResponse)) api.AppHealthResponse {
	t.Helper()
	claim, err := s.ClaimAppHealth(t.Context(), uuid.NewString(), at)
	if err != nil || claim.AppID != appID {
		t.Fatalf("claim %+v: %v", claim, err)
	}
	a := historyAssessment(appID, status, at.Add(time.Millisecond))
	if change != nil {
		change(&a)
	}
	if err := s.FinishAppHealth(t.Context(), claim, a, at.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAppHealthHistoryMeaningAndGaps(t *testing.T) {
	appHealthStores(t, func(t *testing.T, store state.Store, s state.AppHealthHistoryStore) {
		account, app, _, _ := healthFixture(t, store)
		now := time.Now().UTC().Truncate(time.Second)
		page, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", now)
		if err != nil || len(page.Entries) != 0 || page.Latest != nil || page.CollectorFresh {
			t.Fatalf("read created evidence: %+v %v", page, err)
		}
		first := recordAppHealth(t, s, app.ID, "healthy", now, nil)
		recordAppHealth(t, s, app.ID, "healthy", now.Add(31*time.Second), func(a *api.AppHealthResponse) {
			a.Requests.RequestCount = 200
			a.Checks[0].Detail = "Moving counts and prose."
		})
		page, err = s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", now.Add(32*time.Second))
		if err != nil || len(page.Entries) != 1 || page.Entries[0].Kind != "baseline" || page.Latest.Requests.RequestCount != 200 {
			t.Fatalf("duplicate sample: %+v %v", page, err)
		}
		// Returned JSON cannot mutate retained evidence.
		page.Entries[0].Assessment.Checks[0].Detail = "tampered"
		page.Latest.Requests.RequestCount = 999
		recordAppHealth(t, s, app.ID, "unhealthy", now.Add(62*time.Second), func(a *api.AppHealthResponse) {
			a.Checks[0].Status = "fail"
			a.Checks[0].Reason = "request_error_rate_severe"
		})
		stale, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", now.Add(190*time.Second))
		if err != nil || stale.CollectorFresh || stale.Latest.Status != "unhealthy" || len(stale.Entries) != 2 || stale.Entries[1].Assessment.EvaluatedAt != first.EvaluatedAt || stale.Entries[1].Assessment.Checks[0].Detail == "tampered" {
			t.Fatalf("stale history: %+v %v", stale, err)
		}
		recordAppHealth(t, s, app.ID, "healthy", now.Add(195*time.Second), nil)
		page, err = s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", now.Add(196*time.Second))
		if err != nil || !page.CollectorFresh || len(page.Entries) != 4 || page.Entries[1].Kind != "gap" || page.Entries[1].Assessment.Status != "unknown" || page.Entries[0].PreviousStatus != "unknown" {
			t.Fatalf("gap and new observation: %+v %v", page, err)
		}
		other, err := store.CreateAccount(t.Context(), uuid.NewString()+"@history.test", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ListAppHealthHistory(t.Context(), other.ID, app.ID, 20, "", now); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign account: %v", err)
		}
		one, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 1, "", now.Add(196*time.Second))
		if err != nil || one.NextCursor == "" {
			t.Fatalf("first page %+v %v", one, err)
		}
		two, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 1, one.NextCursor, now.Add(196*time.Second))
		if err != nil || two.Entries[0].ID == one.Entries[0].ID || two.Entries[0].Kind != "gap" {
			t.Fatalf("cursor page %+v %v", two, err)
		}
		if _, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 1, uuid.NewString(), now); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("missing cursor: %v", err)
		}
	})
}

func TestAppHealthHistoryLeaseAndInvalidObservation(t *testing.T) {
	appHealthStores(t, func(t *testing.T, store state.Store, s state.AppHealthHistoryStore) {
		_, app, _, _ := healthFixture(t, store)
		now := time.Now().UTC().Truncate(time.Second)
		old, err := s.ClaimAppHealth(t.Context(), "old", now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ClaimAppHealth(t.Context(), "overlap", now.Add(time.Second)); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("overlapping claim: %v", err)
		}
		newAt := now.Add(api.AppHealthCollectorLease + time.Second)
		current, err := s.ClaimAppHealth(t.Context(), "current", newAt)
		if err != nil {
			t.Fatal(err)
		}
		a := historyAssessment(app.ID, "healthy", newAt)
		if err := s.FinishAppHealth(t.Context(), old, a, newAt); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("expired worker: %v", err)
		}
		invalid := a
		invalid.Scope = "preview"
		if err := s.FinishAppHealth(t.Context(), current, invalid, newAt); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("wrong scope: %v", err)
		}
		invalid = a
		invalid.EvaluatedAt = now.Format(time.RFC3339Nano)
		if err := s.FinishAppHealth(t.Context(), current, invalid, newAt); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("old observation: %v", err)
		}
		invalid = a
		invalid.Summary = strings.Repeat("x", api.AppHealthHistoryEntryMaxBytes)
		if err := s.FinishAppHealth(t.Context(), current, invalid, newAt); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("oversize observation: %v", err)
		}
		if err := s.FinishAppHealth(t.Context(), current, a, newAt); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishAppHealth(t.Context(), current, a, newAt); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("duplicate completion: %v", err)
		}
	})
}

func TestAppHealthHistoryConcurrentClaims(t *testing.T) {
	appHealthStores(t, func(t *testing.T, store state.Store, s state.AppHealthHistoryStore) {
		_, _, _, _ = healthFixture(t, store)
		now := time.Now().UTC().Truncate(time.Second)
		results := make(chan error, 12)
		var wg sync.WaitGroup
		for i := range 12 {
			wg.Add(1)
			go func() { defer wg.Done(); _, err := s.ClaimAppHealth(t.Context(), fmt.Sprint(i), now); results <- err }()
		}
		wg.Wait()
		close(results)
		claimed := 0
		for err := range results {
			if err == nil {
				claimed++
			} else if !errors.Is(err, state.ErrNotFound) {
				t.Fatal(err)
			}
		}
		if claimed != 1 {
			t.Fatalf("claimed %d times", claimed)
		}
	})
}

func TestAppHealthHistoryRetentionAndPolicy(t *testing.T) {
	appHealthStores(t, func(t *testing.T, store state.Store, s state.AppHealthHistoryStore) {
		account, app, _, _ := healthFixture(t, store)
		now := time.Now().UTC().Truncate(time.Second)
		recordAppHealth(t, s, app.ID, "healthy", now, nil)
		recordAppHealth(t, s, app.ID, "healthy", now.Add(31*time.Second), func(a *api.AppHealthResponse) { a.Requests.Policy.MinimumRequests = 75 })
		page, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 100, "", now.Add(32*time.Second))
		if err != nil || len(page.Entries) != 2 {
			t.Fatalf("policy change %+v %v", page, err)
		}
		oldID := page.Entries[1].ID
		for i := 2; i < api.AppHealthHistoryMaxEntries+4; i++ {
			status := "healthy"
			if i%2 == 0 {
				status = "unhealthy"
			}
			recordAppHealth(t, s, app.ID, status, now.Add(time.Duration(i)*31*time.Second), nil)
		}
		at := now.Add(time.Duration(api.AppHealthHistoryMaxEntries+4) * 31 * time.Second)
		page, err = s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 100, "", at)
		if err != nil || len(page.Entries) != api.AppHealthHistoryMaxEntries {
			t.Fatalf("retention count %d: %v", len(page.Entries), err)
		}
		if _, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, oldID, at); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("pruned cursor: %v", err)
		}
		agedAt := at.Add(api.AppHealthHistoryMaxAge + time.Hour)
		page, err = s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", agedAt)
		if err != nil || len(page.Entries) != 0 || page.CollectorFresh {
			t.Fatalf("age retention: %+v %v", page, err)
		}
		removed, err := s.PruneExpiredAppHealthHistory(t.Context(), agedAt)
		if err != nil || removed < 1 || removed > api.AppHealthHistoryPruneBatch {
			t.Fatalf("age cleanup %d %v", removed, err)
		}
		if removed, err := s.PruneExpiredAppHealthHistory(t.Context(), agedAt); err != nil || removed != 0 {
			t.Fatalf("repeat age cleanup %d %v", removed, err)
		}
		if _, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, oldID, agedAt); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("aged cursor: %v", err)
		}
	})
}

func TestAppHealthHistoryByteRetention(t *testing.T) {
	appHealthStores(t, func(t *testing.T, store state.Store, s state.AppHealthHistoryStore) {
		account, app, _, _ := healthFixture(t, store)
		now := time.Now().UTC().Truncate(time.Second)
		count := api.AppHealthHistoryMaxBytes/(api.AppHealthHistoryEntryMaxBytes-2048) + 4
		for i := range count {
			status := "healthy"
			if i%2 == 0 {
				status = "unhealthy"
			}
			recordAppHealth(t, s, app.ID, status, now.Add(time.Duration(i)*31*time.Second), func(a *api.AppHealthResponse) {
				a.Summary = strings.Repeat("x", api.AppHealthHistoryEntryMaxBytes-2048)
			})
		}
		page, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 100, "", now.Add(time.Duration(count)*31*time.Second))
		if err != nil || len(page.Entries) == 0 || len(page.Entries) >= count {
			t.Fatalf("byte retention count %d of %d: %v", len(page.Entries), count, err)
		}
		bytes := 0
		for _, entry := range page.Entries {
			body, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			bytes += len(body)
		}
		if bytes > api.AppHealthHistoryMaxBytes {
			t.Fatalf("retained %d bytes", bytes)
		}
	})
}
