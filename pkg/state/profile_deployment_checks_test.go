package state_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

func autoProfileStore(t *testing.T, backend string) state.Store {
	t.Helper()
	if backend == "pg" {
		t.Setenv(pgtest.UseTemplateDatabase, "1")
		pool := pgtest.OpenMigrated(t)
		return autoProfilePgStore{PgStore: state.NewPgStore(pool), pool: pool}
	}
	return state.NewMemStore()
}

type autoProfilePgStore struct {
	*state.PgStore
	pool *pgxpool.Pool
}

// CreateDeployment records intent; completion is ordinarily stamped later by
// the scheduler. Seed only this private test database with deterministic times.
func autoProfileDeployment(t *testing.T, store state.Store, input state.Deployment) (state.Deployment, error) {
	t.Helper()
	d, err := store.CreateDeployment(t.Context(), input)
	if err != nil {
		return d, err
	}
	if pg, ok := store.(autoProfilePgStore); ok {
		if input.Status == state.DeployLive {
			if err := store.MarkDeploymentLive(t.Context(), d.ID); err != nil {
				return d, err
			}
		}
		_, err = pg.pool.Exec(t.Context(), `UPDATE deployments SET status=$2,created_at=$3,rollout_state=$4,rollout_completed_at=$5 WHERE id=$1`, d.ID, input.Status, input.CreatedAt, input.RolloutState, input.RolloutCompletedAt)
		if err != nil {
			return d, err
		}
		return store.DeploymentByID(t.Context(), d.ID)
	}
	return d, nil
}

func autoProfileFixture(t *testing.T, store state.Store) (state.Account, state.App, state.Deployment, api.ProfileDeploymentPolicy) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "auto-" + uuid.NewString()[:8], Runtime: "node24", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour)
	base, err := autoProfileDeployment(t, store, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Scope: "prod", Kind: state.DeploymentKindImage, Status: state.DeploySuperseded, RolloutState: "complete", RolloutCompletedAt: &old, CreatedAt: old.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	checks := store.(state.ProfileDeploymentCheckStore)
	policy, err := checks.GetProfileDeploymentPolicy(ctx, acct.ID, app.ID)
	if err != nil || policy.Revision != 0 || policy.Config.Enabled {
		t.Fatal(policy, err)
	}
	policy.Config.Enabled = true
	policy.Config.WarmupSeconds = 0
	policy.Config.WindowSeconds = 60
	zero := int64(0)
	policy, err = checks.SaveProfileDeploymentPolicy(ctx, acct.ID, app.ID, api.SaveProfileDeploymentPolicyRequest{ExpectedRevision: &zero, Config: policy.Config})
	if err != nil {
		t.Fatal(err)
	}
	return acct, app, base, policy
}

func autoProfileCandidate(t *testing.T, store state.Store, app state.App, scope string, at time.Time) state.Deployment {
	t.Helper()
	d, err := autoProfileDeployment(t, store, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, Status: state.DeployLive, RolloutState: "complete", RolloutCompletedAt: &at, CreatedAt: at.Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func autoProfileAssessment(check api.ProfileDeploymentCheck, at time.Time, regressed bool) api.ProfileRegressionAssessment {
	in := api.ProfileInvestigation{Revision: 1, Investigation: api.ProfileInvestigationInput{Candidate: check.Candidate}}
	if check.Baseline != nil {
		in.Investigation.Baseline = *check.Baseline
	}
	a := profiling.NewRegressionAssessment(in, check.Config.Options, at)
	a.Reason = "Capture coverage is not yet sufficient."
	if !regressed {
		return a
	}
	view := func(q api.ProfileQuery, cpu float64) api.ProfileResponse {
		seconds := q.End.Sub(q.Start).Seconds()
		return api.ProfileResponse{Query: q, CPUSeconds: cpu, Functions: []api.ProfileFunction{{Name: "work", File: "app.js", SelfCPUSeconds: cpu}}, Flamegraph: &api.ProfileStack{Name: "all", CPUSeconds: cpu, Children: []*api.ProfileStack{{Name: "work", File: "app.js", CPUSeconds: cpu}}}, Coverage: &api.ProfileCoverage{Available: true, ReceivedProfiles: 10, ContributingCollectors: 1, WindowSeconds: seconds, CoveredSeconds: seconds}}
	}
	return profiling.AssessRegression(a, view(*check.Baseline, 12), view(check.Candidate, 30))
}

func TestAutomaticProfileChecksDiscoveryLeasesAndAtomicInvestigation(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			store := autoProfileStore(t, backend)
			acct, app, base, policy := autoProfileFixture(t, store)
			checks := store.(state.ProfileDeploymentCheckStore)
			ctx := t.Context()
			originalAt := *policy.UpdatedAt
			*policy.UpdatedAt = originalAt.Add(-time.Hour)
			current, err := checks.GetProfileDeploymentPolicy(ctx, acct.ID, app.ID)
			if err != nil || !current.UpdatedAt.Equal(originalAt) {
				t.Fatal("policy aliasing", current, err)
			}
			if n, err := checks.DiscoverProfileDeploymentChecks(ctx, originalAt); err != nil || n != 0 {
				t.Fatal("existing deployments were backfilled", n, err)
			}
			otherScopeTime := originalAt.Add(-time.Minute)
			autoProfileCandidate(t, store, app, "staging", otherScopeTime)
			completed := originalAt.Add(2 * time.Second)
			candidate := autoProfileCandidate(t, store, app, "prod", completed)
			if n, err := checks.DiscoverProfileDeploymentChecks(ctx, completed); err != nil || n != 1 {
				t.Fatal("discovery", n, err)
			}
			if n, err := checks.DiscoverProfileDeploymentChecks(ctx, completed); err != nil || n != 0 {
				t.Fatal("duplicate discovery", n, err)
			}
			row, err := checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, candidate.ID)
			if err != nil || row.Baseline == nil || row.Baseline.DeploymentID != base.ID || row.Scope != "prod" || row.Candidate.Runtime != row.Baseline.Runtime || !row.Baseline.End.Equal(candidate.CreatedAt) || row.Candidate.End.Sub(row.Candidate.Start) != row.Baseline.End.Sub(row.Baseline.Start) {
				t.Fatal("pair/window selection", row, err)
			}
			if _, err := checks.GetProfileDeploymentCheck(ctx, uuid.NewString(), app.ID, candidate.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign receipt", err)
			}
			if _, err := checks.ClaimProfileDeploymentCheck(ctx, completed); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("capture warm-up bypassed", err)
			}
			claimAt := row.NextAttemptAt.Add(time.Second)
			works := make(chan state.ProfileDeploymentCheckWork, 2)
			errs := make(chan error, 2)
			var workers sync.WaitGroup
			for range 2 {
				workers.Go(func() { work, err := checks.ClaimProfileDeploymentCheck(ctx, claimAt); works <- work; errs <- err })
			}
			workers.Wait()
			close(works)
			close(errs)
			wins, misses := 0, 0
			var first state.ProfileDeploymentCheckWork
			for err := range errs {
				if err == nil {
					wins++
				} else if errors.Is(err, state.ErrNotFound) {
					misses++
				} else {
					t.Fatal(err)
				}
			}
			for work := range works {
				if work.Token != "" {
					first = work
				}
			}
			if wins != 1 || misses != 1 {
				t.Fatal("concurrent claim", wins, misses)
			}
			queued, err := checks.FinishProfileDeploymentCheck(ctx, first, autoProfileAssessment(first.Check, claimAt, false), true, claimAt)
			if err != nil || queued.Status != "queued" || queued.Attempts != 1 || queued.InvestigationID != "" || !queued.Candidate.End.Equal(row.Candidate.End) {
				t.Fatal("retry changed windows", queued, err)
			}
			secondAt := queued.NextAttemptAt.Add(time.Second)
			second, err := checks.ClaimProfileDeploymentCheck(ctx, secondAt)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := checks.FinishProfileDeploymentCheck(ctx, first, autoProfileAssessment(first.Check, secondAt, false), false, secondAt); !errors.Is(err, state.ErrProfileCheckLease) {
				t.Fatal("stale worker published", err)
			}
			a := autoProfileAssessment(second.Check, secondAt, true)
			done, err := checks.FinishProfileDeploymentCheck(ctx, second, a, false, secondAt)
			if err != nil || done.Status != "regressed" || done.InvestigationID == "" || done.CompletedAt == nil || done.NextAttemptAt != nil {
				t.Fatal("result", done, err)
			}
			investigation, err := store.(state.ProfileInvestigationStore).GetProfileInvestigation(ctx, acct.ID, app.ID, done.InvestigationID)
			if err != nil || investigation.Revision != 2 || investigation.Assessment == nil || investigation.Assessment.InvestigationRevision != 2 || investigation.Assessment.Status != "regressed" || investigation.Investigation.SelectedPath == nil || !investigation.Investigation.Candidate.End.Equal(row.Candidate.End) {
				t.Fatal("saved assessment", investigation, err)
			}
			if _, err := checks.FinishProfileDeploymentCheck(ctx, second, a, false, secondAt); !errors.Is(err, state.ErrProfileCheckLease) {
				t.Fatal("duplicate publication", err)
			}
			if rows, err := store.(state.ProfileInvestigationStore).ListProfileInvestigations(ctx, acct.ID, app.ID); err != nil || len(rows) != 1 {
				t.Fatal("duplicate investigation", len(rows), err)
			}
			if n, err := checks.DiscoverProfileDeploymentChecks(ctx, secondAt); err != nil || n != 0 {
				t.Fatal("completed check rediscovered", n, err)
			}
			if err := store.(state.ProfileInvestigationStore).DeleteProfileInvestigation(ctx, acct.ID, app.ID, investigation.ID, investigation.Revision); err != nil {
				t.Fatal(err)
			}
			retained, err := checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, candidate.ID)
			if err != nil || retained.Status != "regressed" || retained.InvestigationID != "" || retained.Baseline == nil || !retained.Candidate.End.Equal(row.Candidate.End) {
				t.Fatal("deleting notes erased receipt/windows", retained, err)
			}
		})
	}
}

func TestAutomaticProfileChecksRecoveryExhaustionAndPolicyCancellation(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			store := autoProfileStore(t, backend)
			acct, app, _, policy := autoProfileFixture(t, store)
			checks := store.(state.ProfileDeploymentCheckStore)
			ctx := t.Context()
			completed := policy.UpdatedAt.Add(2 * time.Second)
			candidate := autoProfileCandidate(t, store, app, "prod", completed)
			if _, err := checks.DiscoverProfileDeploymentChecks(ctx, completed); err != nil {
				t.Fatal(err)
			}
			row, err := checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			at := row.NextAttemptAt.Add(time.Second)
			first, err := checks.ClaimProfileDeploymentCheck(ctx, at)
			if err != nil {
				t.Fatal(err)
			}
			at = at.Add(api.ProfileAutoLeaseDuration + time.Second)
			recovered, err := checks.ClaimProfileDeploymentCheck(ctx, at)
			if err != nil || recovered.Check.Attempts != 2 || recovered.Token == first.Token {
				t.Fatal("lease recovery", recovered, err)
			}
			if _, err := checks.FinishProfileDeploymentCheck(ctx, first, autoProfileAssessment(first.Check, at, false), false, at); !errors.Is(err, state.ErrProfileCheckLease) {
				t.Fatal("expired worker published", err)
			}
			work := recovered
			for attempt := 2; attempt <= api.ProfileAutoMaxAttempts; attempt++ {
				row, err = checks.FinishProfileDeploymentCheck(ctx, work, autoProfileAssessment(work.Check, at, false), true, at)
				if err != nil {
					t.Fatal(err)
				}
				if attempt < api.ProfileAutoMaxAttempts {
					at = row.NextAttemptAt.Add(time.Second)
					work, err = checks.ClaimProfileDeploymentCheck(ctx, at)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if row.Status != "inconclusive" || row.Attempts != api.ProfileAutoMaxAttempts || row.InvestigationID == "" {
				t.Fatal("retry exhaustion", row)
			}
			if _, err := checks.ClaimProfileDeploymentCheck(ctx, at.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("terminal job retried", err)
			}
			later := at.Add(time.Second)
			other := autoProfileCandidate(t, store, app, "prod", later)
			if _, err := checks.DiscoverProfileDeploymentChecks(ctx, later); err != nil {
				t.Fatal(err)
			}
			otherRow, err := checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, other.ID)
			if err != nil {
				t.Fatal(err)
			}
			leased, err := checks.ClaimProfileDeploymentCheck(ctx, otherRow.NextAttemptAt.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			config := policy.Config
			config.Enabled = false
			if _, err := checks.SaveProfileDeploymentPolicy(ctx, acct.ID, app.ID, api.SaveProfileDeploymentPolicyRequest{ExpectedRevision: &policy.Revision, Config: config}); err != nil {
				t.Fatal(err)
			}
			cancelled, err := checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, other.ID)
			if err != nil || cancelled.Status != "cancelled" {
				t.Fatal("policy cancellation", cancelled, err)
			}
			if _, err := checks.FinishProfileDeploymentCheck(ctx, leased, autoProfileAssessment(leased.Check, later.Add(time.Hour), false), false, later.Add(time.Hour)); !errors.Is(err, state.ErrProfileCheckLease) {
				t.Fatal("disabled policy published", err)
			}
		})
	}
}

func TestAutomaticProfileChecksQuotaAndMissingPredecessor(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			store := autoProfileStore(t, backend)
			acct, app, base, policy := autoProfileFixture(t, store)
			checks := store.(state.ProfileDeploymentCheckStore)
			ctx := t.Context()
			// A different environment has no predecessor and remains explicit.
			completed := policy.UpdatedAt.Add(2 * time.Second)
			missing := autoProfileCandidate(t, store, app, "staging", completed)
			if _, err := checks.DiscoverProfileDeploymentChecks(ctx, completed); err != nil {
				t.Fatal(err)
			}
			row, err := checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, missing.ID)
			if err != nil || row.Baseline != nil {
				t.Fatal("guessed predecessor", row, err)
			}
			at := row.NextAttemptAt.Add(time.Second)
			work, err := checks.ClaimProfileDeploymentCheck(ctx, at)
			if err != nil {
				t.Fatal(err)
			}
			done, err := checks.FinishProfileDeploymentCheck(ctx, work, autoProfileAssessment(work.Check, at, false), false, at)
			if err != nil || done.Status != "inconclusive" || done.InvestigationID != "" {
				t.Fatal(done, err)
			}
			// Filling metadata quota must preserve existing user investigations.
			investigations := store.(state.ProfileInvestigationStore)
			q := api.ProfileQuery{DeploymentID: base.ID, Runtime: "node24", End: time.Now().Add(-time.Minute), Start: time.Now().Add(-2 * time.Minute)}
			zero := int64(0)
			for range api.ProfileInvestigationMaxPerApp {
				_, err := investigations.SaveProfileInvestigation(ctx, acct.ID, app.ID, "", api.SaveProfileInvestigationRequest{ExpectedRevision: &zero, Investigation: api.ProfileInvestigationInput{Title: "User notes", Baseline: q, Candidate: q}})
				if err != nil {
					t.Fatal(err)
				}
			}
			candidate := autoProfileCandidate(t, store, app, "prod", at)
			if _, err := checks.DiscoverProfileDeploymentChecks(ctx, at); err != nil {
				t.Fatal(err)
			}
			row, err = checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			at = row.NextAttemptAt.Add(time.Second)
			work, err = checks.ClaimProfileDeploymentCheck(ctx, at)
			if err != nil {
				t.Fatal(err)
			}
			done, err = checks.FinishProfileDeploymentCheck(ctx, work, autoProfileAssessment(work.Check, at, true), false, at)
			if err != nil || done.Status != "regressed" || done.InvestigationID != "" || !strings.Contains(done.Reason, "limit is full") {
				t.Fatal("quota erased result", done, err)
			}
			rows, err := investigations.ListProfileInvestigations(ctx, acct.ID, app.ID)
			if err != nil || len(rows) != api.ProfileInvestigationMaxPerApp {
				t.Fatal(len(rows), err)
			}
			for _, saved := range rows {
				if saved.Investigation.Title != "User notes" || saved.Revision != 1 {
					t.Fatal("user record modified", saved)
				}
			}
		})
	}
}

func TestAutomaticProfileChecksCrashLimitAndReceiptRetention(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			store := autoProfileStore(t, backend)
			acct, app, _, policy := autoProfileFixture(t, store)
			checks := store.(state.ProfileDeploymentCheckStore)
			ctx := t.Context()
			at := policy.UpdatedAt.Add(2 * time.Second)
			candidate := autoProfileCandidate(t, store, app, "prod", at)
			if _, err := checks.DiscoverProfileDeploymentChecks(ctx, at); err != nil {
				t.Fatal(err)
			}
			row, err := checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			at = row.NextAttemptAt.Add(time.Second)
			for attempt := 1; attempt <= api.ProfileAutoMaxAttempts; attempt++ {
				work, err := checks.ClaimProfileDeploymentCheck(ctx, at)
				if err != nil || work.Check.Attempts != attempt {
					t.Fatal(work, err)
				}
				at = at.Add(api.ProfileAutoLeaseDuration + time.Second)
			}
			if err := checks.MaintainProfileDeploymentChecks(ctx, at); err != nil {
				t.Fatal(err)
			}
			row, err = checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, candidate.ID)
			if err != nil || row.Status != "inconclusive" || row.CompletedAt == nil || row.Attempts != api.ProfileAutoMaxAttempts {
				t.Fatal(row, err)
			}
			if err := checks.MaintainProfileDeploymentChecks(ctx, at.Add(api.ProfileAutoReceiptRetention+time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := checks.GetProfileDeploymentCheck(ctx, acct.ID, app.ID, candidate.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("expired receipt retained", err)
			}
		})
	}
}
