package environmentgitops_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/state"
)

type sourceReaderFunc func(context.Context, state.EnvironmentGitSource) (state.EnvironmentGitSourcePollResult, error)

func (f sourceReaderFunc) ReadEnvironmentGitSource(ctx context.Context, source state.EnvironmentGitSource) (state.EnvironmentGitSourcePollResult, error) {
	return f(ctx, source)
}

func TestSourcePollerKeepsLastVerifiedCandidateAndNeverApproves(t *testing.T) {
	store, source, desired, _, _ := setup(t, "report")
	now := time.Now().UTC()
	poller := &environmentgitops.SourcePoller{Store: store, LeaseDuration: time.Minute, ReadTimeout: time.Second,
		CheckInterval: 5 * time.Minute, RetryInterval: time.Second, Now: func() time.Time { return now }}
	sha := strings.Repeat("b", 40)
	poller.Reader = sourceReaderFunc(func(context.Context, state.EnvironmentGitSource) (state.EnvironmentGitSourcePollResult, error) {
		return state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: desired.Digest}, nil
	})
	if worked, err := poller.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("first poll = %v, %v", worked, err)
	}
	checked, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if err != nil || checked.SourceCommitSHA != sha || checked.ApprovedRevisionID != source.ApprovedRevisionID || checked.Generation != source.Generation {
		t.Fatalf("polling changed approval: %+v %v", checked, err)
	}
	if worked, err := poller.RunOnce(t.Context()); err != nil || worked {
		t.Fatalf("repolled before durable due time: %v, %v", worked, err)
	}
	now = now.Add(5 * time.Minute)
	poller.Reader = sourceReaderFunc(func(context.Context, state.EnvironmentGitSource) (state.EnvironmentGitSourcePollResult, error) {
		return state.EnvironmentGitSourcePollResult{}, errors.New("secret transport credential")
	})
	if worked, err := poller.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("failed poll did not record stable availability: %v, %v", worked, err)
	}
	failed, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if err != nil || failed.SourceErrorCode != "environment_git_source_unavailable" || failed.SourceCommitSHA != sha ||
		failed.SourceVerifiedAt == nil || !failed.SourceVerifiedAt.Equal(*checked.SourceVerifiedAt) {
		t.Fatalf("failure lost verified candidate or exposed error: %+v %v", failed, err)
	}
	// A fresh poller resumes the same due row after the shorter outage retry.
	restarted := *poller
	now = now.Add(time.Second)
	restarted.Reader = sourceReaderFunc(func(context.Context, state.EnvironmentGitSource) (state.EnvironmentGitSourcePollResult, error) {
		return state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: desired.Digest}, nil
	})
	if worked, err := restarted.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("fresh poller could not recover: %v, %v", worked, err)
	}
	recovered, _ := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if recovered.SourceErrorCode != "" || recovered.ApprovedRevisionID != source.ApprovedRevisionID || !recovered.SourceVerifiedAt.Equal(now) {
		t.Fatalf("source recovery changed authority: %+v", recovered)
	}
}

func TestSourcePollerCancellationLeavesLeaseForRecovery(t *testing.T) {
	store, source, _, _, _ := setup(t, "report")
	ctx, cancel := context.WithCancel(t.Context())
	poller := &environmentgitops.SourcePoller{Store: store, LeaseDuration: time.Minute, ReadTimeout: time.Second,
		CheckInterval: time.Minute, RetryInterval: time.Second,
		Reader: sourceReaderFunc(func(ctx context.Context, _ state.EnvironmentGitSource) (state.EnvironmentGitSourcePollResult, error) {
			cancel()
			return state.EnvironmentGitSourcePollResult{}, ctx.Err()
		}),
	}
	if worked, err := poller.RunOnce(ctx); !worked || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled poll = %v, %v", worked, err)
	}
	after, _ := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if after.SourceCheckedAt != nil || after.SourceErrorCode != "" {
		t.Fatalf("shutdown became a source outage: %+v", after)
	}
}
