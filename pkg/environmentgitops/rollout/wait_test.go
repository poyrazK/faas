package rollout

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWaitBoundsReadsRetriesTransientErrorsAndStopsAtServing(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	calls, progress := 0, 0
	out, err := Wait(ctx, newTracker(t), func(ctx context.Context) (api.EnvironmentGitOpsStatusResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Error("read is unbounded")
		}
		calls++
		if calls == 1 {
			return api.EnvironmentGitOpsStatusResponse{}, errors.New("PRIVATE_TRANSPORT_ERROR")
		}
		status := servingStatus(time.Now().UTC())
		if calls == 2 {
			status.WorkloadEvidence.Serving = false
		}
		return status, nil
	}, time.Millisecond, func(out Result) error {
		progress++
		if progress == 1 && (out.Status != "unavailable" || out.Explanation != nil) {
			t.Fatal(out)
		}
		return nil
	})
	if err != nil || out.ExitCode() != 0 || calls != 3 || progress != 3 {
		t.Fatal(out, err, calls, progress)
	}
}

func TestWaitTimeoutAndCancellationCannotReturnLateSuccess(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "pending"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			out, err := Wait(ctx, newTracker(t), func(ctx context.Context) (api.EnvironmentGitOpsStatusResponse, error) {
				if mode == "cancel" {
					cancel()
				}
				status := servingStatus(time.Now().UTC())
				if mode == "pending" {
					status.WorkloadEvidence.CommitSHA = strings.Repeat("b", 40)
					return status, nil
				}
				<-ctx.Done()
				return status, nil
			}, time.Millisecond, nil)
			want := 3
			if mode == "cancel" {
				want = 130
			}
			if err != nil || out.ExitCode() != want {
				t.Fatal(out, err)
			}
		})
	}
}

func TestWaitPermanentErrorsAndOutputErrorsStopWithoutRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	calls := 0
	_, err := Wait(ctx, newTracker(t), func(context.Context) (api.EnvironmentGitOpsStatusResponse, error) {
		calls++
		return api.EnvironmentGitOpsStatusResponse{}, &api.APIError{Problem: api.Problem{Status: 403, Detail: "PRIVATE_SCOPE_ERROR"}}
	}, time.Millisecond, nil)
	if err == nil || calls != 1 || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal(err, calls)
	}
	want := errors.New("output closed")
	_, err = Wait(ctx, newTracker(t), func(context.Context) (api.EnvironmentGitOpsStatusResponse, error) {
		return servingStatus(time.Now().UTC()), nil
	}, time.Millisecond,
		func(Result) error { return want })
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
	if _, err := Wait(context.Background(), newTracker(t), func(context.Context) (api.EnvironmentGitOpsStatusResponse, error) {
		t.Fatal("unbounded read")
		return api.EnvironmentGitOpsStatusResponse{}, nil
	}, time.Millisecond, nil); err == nil {
		t.Fatal("unbounded wait accepted")
	}
}

func TestWaitCancellationDuringProgressCannotReturnSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	out, err := Wait(ctx, newTracker(t), func(context.Context) (api.EnvironmentGitOpsStatusResponse, error) {
		return servingStatus(time.Now().UTC()), nil
	}, time.Millisecond, func(Result) error { cancel(); return nil })
	if err != nil || out.Status != "interrupted" || out.ExitCode() != 130 {
		t.Fatal(out, err)
	}
}
