package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitSourceHealthSeparatesPollingVerificationAndAuthority(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		healthStore := store.(state.EnvironmentGitSourceHealthStore)
		if empty, err := healthStore.EnvironmentGitSourceHealth(t.Context(), time.Now(), api.EnvironmentGitSourceStaleAfter); err != nil || empty != (state.EnvironmentGitSourceHealth{}) {
			t.Fatalf("empty fleet health: %+v %v", empty, err)
		}
		for _, bad := range []struct {
			now        time.Time
			staleAfter time.Duration
		}{
			{time.Time{}, time.Minute}, {time.Now(), 0}, {time.Now(), -time.Minute},
		} {
			if _, err := healthStore.EnvironmentGitSourceHealth(t.Context(), bad.now, bad.staleAfter); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid health clock accepted: %v", err)
			}
		}
		source, desired := seed(t, store)
		health := func(at time.Time) state.EnvironmentGitSourceHealth {
			t.Helper()
			got, err := healthStore.EnvironmentGitSourceHealth(t.Context(), at, api.EnvironmentGitSourceStaleAfter)
			if err != nil {
				t.Fatal(err)
			}
			return got
		}
		initial := health(source.CreatedAt.Add(time.Minute))
		if initial != (state.EnvironmentGitSourceHealth{Active: 1, Unchecked: 1, Unverified: 1, OldestCheckAgeSeconds: 60, OldestVerificationAgeSeconds: 60}) {
			t.Fatalf("new source needs its first check, with startup grace: %+v", initial)
		}
		now := source.CreatedAt.Add(api.EnvironmentGitSourceStaleAfter)
		stale := health(now)
		if stale.PollStale != 1 || stale.VerificationStale != 1 || stale.OldestCheckAgeSeconds != api.EnvironmentGitSourceStaleAfter.Seconds() {
			t.Fatalf("unpolled source did not become stale at the cutoff: %+v", stale)
		}
		polls := store.(state.EnvironmentGitSourcePollStore)
		poll := func(at time.Time, result state.EnvironmentGitSourcePollResult) {
			t.Helper()
			lease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), at, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), lease, result, at, at.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		candidate := state.EnvironmentGitSourcePollResult{CommitSHA: strings.Repeat("a", 40), Digest: desired.Digest}
		poll(now, candidate)
		if got := health(now); got != (state.EnvironmentGitSourceHealth{Active: 1, CandidatePendingApproval: 1}) {
			t.Fatalf("candidate discovery did not separate freshness from approval: %+v", got)
		}
		var err error
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, candidate.CommitSHA))
		if err != nil {
			t.Fatal(err)
		}
		if got := health(now); got != (state.EnvironmentGitSourceHealth{Active: 1, ApprovedPendingApply: 1}) {
			t.Fatalf("approval was reported as application: %+v", got)
		}
		poll(now.Add(time.Minute), state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_source_unavailable"})
		failed := health(now.Add(api.EnvironmentGitSourceStaleAfter))
		if failed.PollStale != 0 || failed.VerificationStale != 1 || failed.Unavailable != 1 || failed.ApprovedPendingApply != 1 || failed.CandidatePendingApproval != 0 ||
			failed.OldestCheckAgeSeconds != (api.EnvironmentGitSourceStaleAfter-time.Minute).Seconds() || failed.OldestVerificationAgeSeconds != api.EnvironmentGitSourceStaleAfter.Seconds() {
			t.Fatalf("fresh failed checks concealed stale verification: %+v", failed)
		}
		// Digest equality matters as well as the commit identity.
		changed, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{}})
		if err != nil {
			t.Fatal(err)
		}
		candidate.Digest = changed.Digest
		recoveredAt := now.Add(api.EnvironmentGitSourceStaleAfter + time.Minute)
		poll(recoveredAt, candidate)
		if got := health(recoveredAt); got != (state.EnvironmentGitSourceHealth{Active: 1, CandidatePendingApproval: 1, ApprovedPendingApply: 1}) {
			t.Fatalf("changed digest or successful recovery lost: %+v", got)
		}
		if got := health(now); got.OldestCheckAgeSeconds != 0 || got.OldestVerificationAgeSeconds != 0 {
			t.Fatalf("clock skew produced negative ages: %+v", got)
		}
		suspended := true
		if _, err := store.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID,
			state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Suspended: &suspended}); err != nil {
			t.Fatal(err)
		}
		if got := health(recoveredAt.Add(time.Hour)); got != (state.EnvironmentGitSourceHealth{Suspended: 1}) {
			t.Fatalf("suspended source generated actionable health conditions: %+v", got)
		}
	})
}

func TestEnvironmentGitSourceHealthReflectsAppliedRevisionAndOtherEnvironments(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, _ := seed(t, store)
		desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
		staging, err := store.CreateEnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "staging", source.Spec)
		if err != nil {
			t.Fatal(err)
		}
		now := staging.CreatedAt.Add(api.EnvironmentGitSourceStaleAfter)
		polls := store.(state.EnvironmentGitSourcePollStore)
		// Both sources are due; publish only production and retain the other
		// lease so it remains a genuinely unobserved environment.
		for range 2 {
			lease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if lease.Source.ID == source.ID {
				if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), lease, state.EnvironmentGitSourcePollResult{CommitSHA: strings.Repeat("a", 40), Digest: desired.Digest}, now, now.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store.(intentTestStore), lease, desired)
		raw, _ := json.Marshal(plan)
		if err := store.FinishEnvironmentGitOps(t.Context(), lease, "converged", raw, json.RawMessage(`[]`), "", now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		healthStore := store.(state.EnvironmentGitSourceHealthStore)
		got, err := healthStore.EnvironmentGitSourceHealth(t.Context(), now, api.EnvironmentGitSourceStaleAfter)
		if err != nil || got.Active != 2 || got.Unchecked != 1 || got.Unverified != 1 || got.PollStale != 1 || got.VerificationStale != 1 || got.CandidatePendingApproval != 0 || got.ApprovedPendingApply != 0 {
			t.Fatalf("applied production hid stale staging or retained pending approval: %+v %v", got, err)
		}
		if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40))); err != nil {
			t.Fatal(err)
		}
		got, err = healthStore.EnvironmentGitSourceHealth(t.Context(), now, api.EnvironmentGitSourceStaleAfter)
		if err != nil || got.ApprovedPendingApply != 1 || got.CandidatePendingApproval != 1 {
			t.Fatalf("a newer approval concealed the previous fully applied revision: %+v %v", got, err)
		}
	})
}
