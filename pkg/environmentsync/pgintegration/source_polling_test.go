package pgintegration_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitSourcePollingPreservesApprovalDuringOutage(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, desired := seed(t, store)
		polls := store.(state.EnvironmentGitSourcePollStore)
		now := time.Now().UTC()
		lease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("competing poll acquired source: %v", err)
		}
		candidate := state.EnvironmentGitSourcePollResult{CommitSHA: strings.Repeat("a", 40), Digest: desired.Digest}
		next := now.Add(5 * time.Minute)
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), lease, candidate, now, next); err != nil {
			t.Fatal(err)
		}
		observed, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || observed.SourceCommitSHA != candidate.CommitSHA || observed.SourceDefinitionDigest != candidate.Digest ||
			observed.SourceVerifiedAt == nil || observed.SourceCheckedAt == nil || observed.Generation != source.Generation ||
			observed.IntentVersion != source.IntentVersion || observed.ApprovedRevisionID != "" || observed.AppliedRevisionID != "" {
			t.Fatalf("discovery mutated authority or lost verified candidate: %+v %v", observed, err)
		}
		if _, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), now, time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("candidate became executable without approval: %v", err)
		}
		approved, revision, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, candidate.CommitSHA))
		if err != nil {
			t.Fatal(err)
		}
		failedLease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), next, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), failedLease, state.EnvironmentGitSourcePollResult{
			ErrorCode: "environment_git_source_unavailable"}, next, next.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		after, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || after.ApprovedRevisionID != revision.ID || after.Generation != approved.Generation ||
			after.SourceErrorCode != "environment_git_source_unavailable" || after.SourceCommitSHA != candidate.CommitSHA ||
			after.SourceVerifiedAt == nil || !after.SourceVerifiedAt.Equal(*observed.SourceVerifiedAt) {
			t.Fatalf("outage replaced approval or verified candidate: %+v %v", after, err)
		}
		run, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), next, time.Minute)
		if err != nil || run.Revision.ID != revision.ID || run.Revision.Digest != desired.Digest {
			t.Fatalf("Git outage prevented approved definition recovery: %+v %v", run, err)
		}
	})
}

func TestEnvironmentGitSourcePollingRejectsExpiredAndSupersededResults(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, desired := seed(t, store)
		polls := store.(state.EnvironmentGitSourcePollStore)
		now := time.Now().UTC()
		claim := func(at time.Time) state.EnvironmentGitSourcePollLease {
			t.Helper()
			lease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), at, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			return lease
		}
		result := state.EnvironmentGitSourcePollResult{CommitSHA: strings.Repeat("a", 40), Digest: desired.Digest}
		old := claim(now)
		recoveredAt := now.Add(2 * time.Minute)
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), old, result, recoveredAt, recoveredAt.Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("expired lease reported availability: %v", err)
		}
		current := claim(recoveredAt)
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), old, result, recoveredAt, recoveredAt.Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("replaced lease reported availability: %v", err)
		}
		if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, result.CommitSHA)); err != nil {
			t.Fatal(err)
		}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), current, result, recoveredAt, recoveredAt.Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded source generation reported availability: %v", err)
		}
		recovered := claim(recoveredAt.Add(2 * time.Minute))
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), recovered, state.EnvironmentGitSourcePollResult{
			ErrorCode: "raw customer definition or transport credential"}, recoveredAt.Add(2*time.Minute), recoveredAt.Add(3*time.Minute)); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("unsafe diagnostic was accepted: %v", err)
		}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), recovered, result, recoveredAt.Add(2*time.Minute), recoveredAt.Add(3*time.Minute)); err != nil {
			t.Fatal(err)
		}
		latest, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || latest.SourceCommitSHA != result.CommitSHA || latest.Generation != 1 {
			t.Fatalf("recovery lost source generation: %+v %v", latest, err)
		}
		suspended := true
		if _, err := store.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID,
			state.EnvironmentGitSourceUpdate{ExpectedGeneration: latest.Generation, Suspended: &suspended}); err != nil {
			t.Fatal(err)
		}
		if _, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), recoveredAt.Add(4*time.Minute), time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("suspended source was polled: %v", err)
		}
	})
}

func TestEnvironmentGitSourcePollingConcurrentClaimsAreExclusive(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, _ := seed(t, store)
		if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
		staging, err := store.CreateEnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "staging", source.Spec)
		if err != nil {
			t.Fatal(err)
		}
		polls := store.(state.EnvironmentGitSourcePollStore)
		now := time.Now().UTC()
		ready := make(chan struct{})
		claims, failures := make(chan string, 8), make(chan error, 8)
		var group sync.WaitGroup
		for range 8 {
			group.Add(1)
			go func() {
				defer group.Done()
				<-ready
				lease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
				if err == nil {
					claims <- lease.Source.ID
				} else if !errors.Is(err, state.ErrNotFound) {
					failures <- err
				}
			}()
		}
		close(ready)
		group.Wait()
		close(claims)
		close(failures)
		for err := range failures {
			t.Fatalf("concurrent poll failed: %v", err)
		}
		counts := map[string]int{}
		for id := range claims {
			counts[id]++
		}
		if len(counts) != 2 || counts[source.ID] != 1 || counts[staging.ID] != 1 {
			t.Fatalf("each due source needs one winner: %+v", counts)
		}
	})
}
