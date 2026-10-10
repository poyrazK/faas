// adr: 568 — continuous reports must preserve intent wakeups and override expiry.
package pgintegration_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func reportSchedulingFixture(t *testing.T, base gitOpsTestStore) (intentTestStore, state.EnvironmentGitSource, environmentsync.DesiredState, state.App) {
	t.Helper()
	store := base.(intentTestStore)
	source, desired, app := intentFixture(t, store, "report")
	preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption preview: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	return store, source, desired, app
}

func observedReportPlan(t *testing.T, store intentTestStore, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState, now time.Time) json.RawMessage {
	t.Helper()
	observation, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, observation.State, observation.Owners, environmentsync.PlanOptions{
		Manager: lease.Source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA,
		Generation: lease.Source.Generation, Now: now, Overrides: observation.Overrides,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEnvironmentGitOpsReportPreservesUnobservedIntentWakeup(t *testing.T) {
	for _, status := range []string{"drifted", "blocked", "partial", "overridden", "failed"} {
		t.Run(status, func(t *testing.T) {
			stores(t, func(t *testing.T, base gitOpsTestStore) {
				store, source, desired, app := reportSchedulingFixture(t, base)
				now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Second)
				lease, err := store.ClaimEnvironmentGitOps(t.Context(), "first-report", now, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				plan := observedReportPlan(t, store, lease, desired, now)
				if status == "failed" {
					plan = json.RawMessage(`{}`) // Observation failed before a plan existed.
				}
				if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, source.EnvironmentSlug, "MODE", "new-console-edit"); err != nil {
					t.Fatal(err)
				}
				if err := store.FinishEnvironmentGitOps(t.Context(), lease, status, plan, json.RawMessage(`[]`), "", now, now.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				next, err := store.ClaimEnvironmentGitOps(t.Context(), "followup-report", now, time.Minute)
				if err != nil || next.Source.IntentVersion <= lease.Source.IntentVersion {
					t.Fatalf("finish lost the pending intent wakeup: %+v %v", next, err)
				}
				runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
				if err != nil || len(runs) != 2 {
					t.Fatalf("report history: %+v %v", runs, err)
				}
				for _, run := range runs {
					if run.ID == lease.RunID && (run.Status != status || run.CompletedAt == nil) {
						t.Fatalf("prior report was not retained: %+v", run)
					}
				}
			})
		})
	}
}

func TestEnvironmentGitOpsReportKeepsCadenceAfterFreshObservation(t *testing.T) {
	stores(t, func(t *testing.T, base gitOpsTestStore) {
		store, source, desired, app := reportSchedulingFixture(t, base)
		now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Second)
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "first-report", now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, source.EnvironmentSlug, "MODE", "observed-console-edit"); err != nil {
			t.Fatal(err)
		}
		plan := observedReportPlan(t, store, lease, desired, now)
		next := now.Add(time.Hour)
		if err := store.FinishEnvironmentGitOps(t.Context(), lease, "drifted", plan, json.RawMessage(`[]`), "", now, next); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimEnvironmentGitOps(t.Context(), "premature-report", next.Add(-time.Microsecond), time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("already observed edit caused a busy loop: %v", err)
		}
		if _, err := store.ClaimEnvironmentGitOps(t.Context(), "scheduled-report", next, time.Minute); err != nil {
			t.Fatalf("regular report was not scheduled: %v", err)
		}
	})
}

func TestEnvironmentGitOpsReportKeepsRetryCadenceWithoutPlan(t *testing.T) {
	stores(t, func(t *testing.T, base gitOpsTestStore) {
		store, _, _, _ := reportSchedulingFixture(t, base)
		now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Second)
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "failed-observation", now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		next := now.Add(time.Minute)
		if err := store.FinishEnvironmentGitOps(t.Context(), lease, "failed", json.RawMessage(`{}`), json.RawMessage(`[]`), "environment_observation_failed", now, next); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimEnvironmentGitOps(t.Context(), "premature-retry", next.Add(-time.Microsecond), time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failed observation caused a busy loop: %v", err)
		}
		if _, err := store.ClaimEnvironmentGitOps(t.Context(), "scheduled-retry", next, time.Minute); err != nil {
			t.Fatalf("failed observation lost its retry: %v", err)
		}
	})
}

func TestEnvironmentGitOpsReportWakesAtOverrideExpiry(t *testing.T) {
	for _, expiredDuringReport := range []bool{false, true} {
		name := "active"
		if expiredDuringReport {
			name = "expired-during-report"
		}
		t.Run(name, func(t *testing.T) {
			stores(t, func(t *testing.T, base gitOpsTestStore) {
				store, source, desired, _ := reportSchedulingFixture(t, base)
				now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Second)
				expires := now.Add(10 * time.Second)
				if err := store.SetEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, api.EnvironmentGitOpsOverrideRequest{
					Resource: "workload/api", Path: "variables/MODE", Reason: "temporary console edit", ExpiresAt: expires,
				}); err != nil {
					t.Fatal(err)
				}
				lease, err := store.ClaimEnvironmentGitOps(t.Context(), "overridden-report", now, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				plan := observedReportPlan(t, store, lease, desired, now)
				finishAt := now
				if expiredDuringReport {
					finishAt = expires
				}
				if err := store.FinishEnvironmentGitOps(t.Context(), lease, "overridden", plan, json.RawMessage(`[]`), "", finishAt, finishAt.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				if _, err := store.ClaimEnvironmentGitOps(t.Context(), "before-expiry", expires.Add(-time.Microsecond), time.Minute); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("override caused a premature report: %v", err)
				}
				followup, err := store.ClaimEnvironmentGitOps(t.Context(), "expiry-report", expires, time.Minute)
				if err != nil {
					t.Fatalf("expiry did not wake the reporter: %v", err)
				}
				fresh := observedReportPlan(t, store, followup, desired, expires)
				if err := store.FinishEnvironmentGitOps(t.Context(), followup, "drifted", fresh, json.RawMessage(`[]`), "", expires, expires.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				if _, err := store.ClaimEnvironmentGitOps(t.Context(), "expired-record", expires.Add(time.Second), time.Minute); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("expired override record caused a busy loop: %v", err)
				}
			})
		})
	}
}
