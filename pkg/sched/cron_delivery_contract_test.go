// adr: 20261026201726
package sched_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// cronTestNoon is a tick just past a boundary of the crons cronDue builds.
var cronTestNoon = time.Date(2026, 7, 17, 12, 0, 1, 0, time.UTC)

// hourlyCron adds an app (with a live deployment unless undeployed) and an
// hourly cron created at 11:00, so a tick at cronTestNoon finds it due.
func hourlyCron(t *testing.T, store *state.MemStore, accountID, slug string, deployed bool, opts state.CronOptions) state.Cron {
	t.Helper()
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if deployed {
		if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := store.CreateCronWithOptions(ctx, app.ID, "0 * * * *", "/hourly", true, opts)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC)
	if c, err = store.UpdateCron(ctx, c.ID, nil, nil, nil, &created); err != nil {
		t.Fatal(err)
	}
	return c
}

func cronTestLoop(t *testing.T, store state.Store) *sched.Loop {
	t.Helper()
	eng, _ := sched.MakeEngine(t, store, &sched.FakeWakeVMM{})
	return sched.NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(&sched.RecordingSynth{}).
		WithClock(func() time.Time { return cronTestNoon })
}

func queuedCronRuns(t *testing.T, store state.Store, accountID string) []state.Invocation {
	t.Helper()
	rows, err := store.ListInvocationsForAccount(context.Background(), accountID, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []state.Invocation
	for _, row := range rows {
		if row.Source == state.InvocationCron {
			out = append(out, row)
		}
	}
	return out
}

// A cron with a schedule policy but no failure rules is delivered once per
// scheduled time (amendment 2026-11-02): its run used to take the plan's queue
// retry budget, up to 25 deliveries. Failure rules keep that budget.
func TestScheduledPolicyCronRunsMakeOneAttempt(t *testing.T) {
	t.Parallel()
	policy := &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", MissedRuns: "skip"}
	rules := &workpolicy.FailureRules{Version: workpolicy.Version, UnmatchedFailure: "retry", UncertainOutcome: "hold"}
	for _, tc := range []struct {
		name   string
		opts   state.CronOptions
		single bool
	}{
		{"schedule policy only", state.CronOptions{SchedulePolicy: policy}, true},
		{"with failure rules", state.CronOptions{SchedulePolicy: policy, FailureRules: rules}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			acct, _ := store.CreateAccount(t.Context(), "policy-attempts@example.com", api.PlanScale)
			hourlyCron(t, store, acct.ID, "policy-attempts", true, tc.opts)
			cronTestLoop(t, store).RunCronTick(t.Context())
			runs := queuedCronRuns(t, store, acct.ID)
			if len(runs) != 1 {
				t.Fatalf("queued runs = %d, want 1", len(runs))
			}
			if single := runs[0].RetryPolicy().MaxAttempts == 1; single != tc.single {
				t.Fatalf("single attempt = %v (policy %s), want %v", single, runs[0].RetryPolicyJSON, tc.single)
			}
		})
	}
}

// A schedule-policy cron on an app with no live deployment is suspended, as a
// plain cron is, instead of queuing a failing run every tick.
func TestScheduledPolicyCronWithoutDeploymentIsSuspended(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(t.Context(), "policy-undeployed@example.com", api.PlanPro)
	policy := &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", MissedRuns: "skip"}
	c := hourlyCron(t, store, acct.ID, "policy-undeployed", false, state.CronOptions{SchedulePolicy: policy})
	cronTestLoop(t, store).RunCronTick(t.Context())
	if runs := queuedCronRuns(t, store, acct.ID); len(runs) != 0 {
		t.Fatalf("queued runs = %d, want none", len(runs))
	}
	got, err := store.CronByID(t.Context(), c.ID)
	if err != nil || got.SuspendedReason != state.CronSuspendedNoLiveDeployment {
		t.Fatalf("cron = %+v, err %v; want suspended %s", got, err, state.CronSuspendedNoLiveDeployment)
	}
}

// failingEnqueueStore fails the first enqueues it is given.
type failingEnqueueStore struct {
	MemStore *state.MemStore
	fails    *atomic.Int32
}

func (f failingEnqueueStore) EnqueueInvocation(ctx context.Context, inv state.Invocation) (state.Invocation, error) {
	if f.fails.Add(-1) >= 0 {
		return state.Invocation{}, errors.New("database unavailable")
	}
	return f.MemStore.EnqueueInvocation(ctx, inv)
}

// A plain cron whose run could not be queued keeps its occurrence: the next
// sweep queues it. It used to consume the claim and lose the run.
func TestCronRunNotQueuedIsRetriedNextSweep(t *testing.T) {
	t.Parallel()
	mem := state.NewMemStore()
	acct, _ := mem.CreateAccount(t.Context(), "enqueue-retry@example.com", api.PlanPro)
	c := hourlyCron(t, mem, acct.ID, "enqueue-retry", true, state.CronOptions{})
	fails := &atomic.Int32{}
	fails.Store(1)
	loop := cronTestLoop(t, statetest.NewMem(failingEnqueueStore{MemStore: mem, fails: fails}))
	loop.RunCronTick(t.Context())
	if runs := queuedCronRuns(t, mem, acct.ID); len(runs) != 0 {
		t.Fatalf("queued runs after the failed enqueue = %d, want 0", len(runs))
	}
	if got, _ := mem.CronByID(t.Context(), c.ID); !got.LastFiredAt.IsZero() {
		t.Fatalf("cursor advanced to %v by a run that was not queued", got.LastFiredAt)
	}
	loop.RunCronTick(t.Context())
	if runs := queuedCronRuns(t, mem, acct.ID); len(runs) != 1 {
		t.Fatalf("queued runs after the retry = %d, want 1", len(runs))
	}
}

// A sweep that uses its slice asks the loop to continue, and the continuation
// dispatches the crons the first pass did not reach, each once.
func TestCronSweepContinuesAfterItsSlice(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(t.Context(), "sweep-slice@example.com", api.PlanPro)
	for i := range 3 {
		hourlyCron(t, store, acct.ID, fmt.Sprintf("sweep-%d", i), true, state.CronOptions{})
	}
	loop := cronTestLoop(t, store).WithCronSweepSlice(time.Nanosecond)
	loop.RunCronTick(t.Context())
	for want := 1; want <= 3; want++ {
		if got := len(queuedCronRuns(t, store, acct.ID)); got != want {
			t.Fatalf("queued runs after pass %d = %d, want %d", want, got, want)
		}
		if !loop.TakeCronSweepResume() {
			t.Fatalf("pass %d used its slice but asked for no continuation", want)
		}
		loop.RunCronSweep(t.Context())
	}
	if got := len(queuedCronRuns(t, store, acct.ID)); got != 3 {
		t.Fatalf("queued runs after the sweep = %d, want 3", got)
	}
	if loop.TakeCronSweepResume() {
		t.Fatal("a finished sweep asked for another pass")
	}
}

// A manual run schedd refuses tells the requester why.
func TestFireNowRefusalNamesTheReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{sched.ErrCronsNotEnabled, "crons are not enabled for this account"},
		{sched.ErrNoLiveDeployment, "no live deployment"},
		{fmt.Errorf("%w: %s", sched.ErrCronSuspended, state.CronSuspendedNoLiveDeployment), "cron suspended: no_live_deployment"},
		{fmt.Errorf("%w: %w", sched.ErrCronRunNotQueued, errors.New("pq: connection reset")), "could not be queued"},
	} {
		got := sched.XFireNowRefusal(tc.err)
		if !strings.Contains(got, tc.want) {
			t.Errorf("refusal for %v = %q, want it to contain %q", tc.err, got, tc.want)
		}
		if strings.Contains(got, "pq:") {
			t.Errorf("refusal %q leaks the store error", got)
		}
	}
	if got := sched.XFireNowRefusal(errors.New("other")); got != "" {
		t.Errorf("refusal for an unknown error = %q, want empty", got)
	}
}

// A suspended HTTP cron is not run by hand.
func TestRunCronNowRefusesSuspendedCron(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(t.Context(), "run-suspended@example.com", api.PlanPro)
	app, c := sched.NewAppAndCron(t, store, acct.ID, true)
	if _, err := store.SuspendCronsForApp(t.Context(), app.ID, state.CronSuspendedNoLiveDeployment); err != nil {
		t.Fatal(err)
	}
	_, err := cronTestLoop(t, store).RunCronNow(t.Context(), c.ID, acct.ID)
	if !errors.Is(err, sched.ErrCronSuspended) {
		t.Fatalf("RunCronNow = %v, want ErrCronSuspended", err)
	}
}
