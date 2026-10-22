package sched

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type taskObservationReaderFunc func(context.Context, time.Time) (state.AppTaskObservationSnapshot, error)

func (f taskObservationReaderFunc) AppTaskObservationSnapshot(ctx context.Context, at time.Time) (state.AppTaskObservationSnapshot, error) {
	return f(ctx, at)
}

func taskGauges(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, f := range families {
		for _, m := range f.Metric {
			if m.Gauge == nil {
				continue
			}
			key := f.GetName()
			for _, l := range m.Label {
				key += "/" + l.GetValue()
			}
			out[key] = m.Gauge.GetValue()
		}
	}
	return out
}

// adr: 20261017141024523 — Bounded task evidence, freshness and coordinator progress.
func TestAppTaskMetricsFreshnessAndIntegrity(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := NewAppTaskMetrics(reg, false, 0)
	now := time.Unix(1700000000, 0).UTC()
	m.now = func() time.Time { return now }
	const count = "schedd_app_task_pending/queued/release"
	const last = "schedd_app_task_collection_last_success_timestamp_seconds"
	if _, ok := taskGauges(t, reg)[count]; ok {
		t.Fatal("startup fabricated empty work")
	}
	s := state.AppTaskObservationSnapshot{}
	s[0][state.AppTaskObservationQueued] = state.AppTaskObservationCohort{Pending: 2, UnknownAge: 1, OldestAt: now.Add(-time.Minute)}
	r := taskObservationReaderFunc(func(ctx context.Context, at time.Time) (state.AppTaskObservationSnapshot, error) {
		if !at.Equal(now) {
			t.Error("wrong read boundary")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > api.ObsAppTaskCollectionBudget {
			t.Error("unbounded reader")
		}
		return s, nil
	})
	if err := m.refresh(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	got := taskGauges(t, reg)
	if len(got) != 90 || got[count] != 2 || got["schedd_app_task_age_unknown/queued/release"] != 1 || got["schedd_app_task_dispatch_enabled"] != 0 || got["schedd_app_task_slots_configured"] != 1 {
		t.Fatal(got)
	}
	observed := got[last]
	now = now.Add(api.ObsAppTaskFreshnessMaxAge)
	if _, ok := taskGauges(t, reg)[count]; !ok {
		t.Fatal("exact ceiling expired")
	}
	now = now.Add(time.Nanosecond)
	if _, ok := taskGauges(t, reg)[count]; ok {
		t.Fatal("stale counts emitted")
	}
	broken := taskObservationReaderFunc(func(context.Context, time.Time) (state.AppTaskObservationSnapshot, error) {
		return state.AppTaskObservationSnapshot{}, errors.New("database failed")
	})
	if err := m.refresh(t.Context(), broken); err == nil {
		t.Fatal("failure hidden")
	}
	got = taskGauges(t, reg)
	if got[last] != observed || got["schedd_app_task_collection_success"] != 0 {
		t.Fatal(got)
	}
	if _, ok := got[count]; ok {
		t.Fatal("failed read invented empty queue")
	}
	s = state.AppTaskObservationSnapshot{}
	if err := m.refresh(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if v, ok := taskGauges(t, reg)[count]; !ok || v != 0 {
		t.Fatal("known empty omitted")
	}
	now = now.Add(-time.Second)
	if _, ok := taskGauges(t, reg)[count]; ok {
		t.Fatal("future snapshot emitted")
	}
}

func TestAppTaskMetricsRejectInvalidObservations(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	for name, p := range map[string]state.AppTaskObservationCohort{
		"negative": {Pending: -1}, "excess unknown": {Pending: 1, UnknownAge: 2}, "missing time": {Pending: 1}, "unexpected time": {OldestAt: now}, "future": {Pending: 1, OldestAt: now.Add(time.Second)}, "epoch": {Pending: 1, OldestAt: time.Unix(0, 0)},
	} {
		t.Run(name, func(t *testing.T) {
			s := state.AppTaskObservationSnapshot{}
			s[0][state.AppTaskObservationQueued] = p
			if validateAppTaskObservation(s, now) == nil {
				t.Fatal("invalid observation accepted")
			}
		})
	}
	for _, c := range []state.AppTaskObservationClass{state.AppTaskObservationUnknown, state.AppTaskObservationRestoring, state.AppTaskObservationRetryWait, state.AppTaskObservationHeld} {
		s := state.AppTaskObservationSnapshot{}
		s[0][c] = state.AppTaskObservationCohort{Pending: 1, OldestAt: now}
		if validateAppTaskObservation(s, now) == nil {
			t.Fatal("invented boundary accepted", c)
		}
	}
}

func TestAppTaskMetricsReadStartAndCancellation(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := NewAppTaskMetrics(reg, true, 2)
	now := time.Unix(1700000000, 0).UTC()
	start := now
	m.now = func() time.Time { return now }
	r := taskObservationReaderFunc(func(context.Context, time.Time) (state.AppTaskObservationSnapshot, error) {
		now = now.Add(time.Second)
		return state.AppTaskObservationSnapshot{}, nil
	})
	if err := m.refresh(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if taskGauges(t, reg)["schedd_app_task_collection_last_success_timestamp_seconds"] != primeUnix(start) {
		t.Fatal("completion renewed freshness")
	}
	ctx, cancel := context.WithCancel(t.Context())
	r = taskObservationReaderFunc(func(context.Context, time.Time) (state.AppTaskObservationSnapshot, error) {
		cancel()
		return state.AppTaskObservationSnapshot{}, nil
	})
	if err := m.refresh(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, ok := taskGauges(t, reg)["schedd_app_task_pending/queued/release"]; ok {
		t.Fatal("late canceled success emitted")
	}
}

func TestAppTaskMetricsObservationIndependentOfBlockedRunner(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := NewAppTaskMetrics(reg, true, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stop := m.runnerStart(ctx)
	defer stop()
	release := m.slot()
	defer release()
	entered, done := make(chan struct{}), make(chan struct{})
	reader := taskObservationReaderFunc(func(ctx context.Context, _ time.Time) (state.AppTaskObservationSnapshot, error) {
		close(entered)
		<-ctx.Done()
		return state.AppTaskObservationSnapshot{}, ctx.Err()
	})
	go func() { m.runWithTicks(ctx, reader, nil, make(chan time.Time)); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("observer blocked")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() { _, err := reg.Gather(); errs <- err })
	}
	scraped := make(chan struct{})
	go func() { wg.Wait(); close(scraped) }()
	select {
	case <-scraped:
	case <-time.After(time.Second):
		t.Fatal("scrape blocked on read")
	}
	for range 20 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer leaked")
	}
	got := taskGauges(t, reg)
	if got["schedd_app_task_observation_enabled"] != 0 || got["schedd_app_task_runner_running"] != 0 || got["schedd_app_task_slots_in_use"] != 1 {
		t.Fatal(got)
	}
}

func TestAppTaskCoordinatorMetricsPreserveVerdictsAndTeardownFence(t *testing.T) {
	for _, mode := range []string{"success", "customer_failure", "customer_timeout", "restore_error", "execute_error", "destroy_error"} {
		t.Run(mode, func(t *testing.T) {
			store, _, _, _, tasks := newAppTaskCoordinatorFixture(t, 1, 600, 1024)
			reg := prometheus.NewPedanticRegistry()
			m := NewAppTaskMetrics(reg, true, 1)
			backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
				if mode == "restore_error" {
					return nil, errors.New("private restore details")
				}
				return appTaskSessionFuncs{execute: func(context.Context, AppTaskExecuteRequest) (AppTaskOutcome, error) {
					if taskGauges(t, reg)["schedd_app_task_slots_in_use"] != 1 {
						t.Error("slot missing")
					}
					if mode == "execute_error" {
						return AppTaskOutcome{}, errors.New("private transport details")
					}
					exit := 0
					status := state.AppTaskSucceeded
					if mode == "customer_failure" {
						exit = 42
						status = state.AppTaskFailed
					}
					if mode == "customer_timeout" {
						status = state.AppTaskTimedOut
					}
					out := AppTaskOutcome{Status: status, ExitCode: &exit}
					if status != state.AppTaskSucceeded {
						out.FailureCode = "command_verdict"
						out.FailureMessage = "customer command failed or timed out"
					}
					return out, nil
				}, destroy: func(context.Context) error {
					if mode == "destroy_error" {
						return errors.New("private destroy details")
					}
					return nil
				}}, nil
			})
			c := NewAppTaskCoordinator(store, backend, AppTaskCoordinatorConfig{Enabled: true, Metrics: m}, nil)
			processed, err := c.ProcessNext(t.Context())
			if !processed || (err != nil) != (mode == "destroy_error") {
				t.Fatal(processed, err)
			}
			if got := taskGauges(t, reg)["schedd_app_task_slots_in_use"]; got != 0 {
				t.Fatal("slot leaked", got)
			}
			if mode == "destroy_error" {
				task, _ := store.AppTaskByID(t.Context(), tasks[0].AccountID, tasks[0].AppID, tasks[0].ID)
				if task.Status != state.AppTaskRunning || testutil.ToFloat64(m.operations.WithLabelValues("complete", "success")) != 0 {
					t.Fatal("cleanup fence changed")
				}
			} else {
				status := "failed"
				if mode == "success" {
					status = "succeeded"
				}
				if mode == "customer_timeout" {
					status = "timed_out"
				}
				if testutil.ToFloat64(m.results.WithLabelValues("manual", status)) != 1 {
					t.Fatal("persisted verdict lost")
				}
			}
			if mode == "customer_failure" || mode == "customer_timeout" {
				if testutil.ToFloat64(m.operations.WithLabelValues("execute", "success")) != 1 || testutil.ToFloat64(m.operations.WithLabelValues("execute", "error")) != 0 {
					t.Fatal("customer exit counted as infrastructure error")
				}
			}
			families, gatherErr := reg.Gather()
			if gatherErr != nil {
				t.Fatal(gatherErr)
			}
			for _, f := range families {
				for _, metric := range f.Metric {
					for _, l := range metric.Label {
						if strings.Contains(l.GetValue(), "private") || strings.Contains(l.GetValue(), tasks[0].ID) {
							t.Fatal("unbounded value in label")
						}
					}
				}
			}
		})
	}
}

func TestAppTaskCoordinatorMetricsRunAndCancellationRace(t *testing.T) {
	store, account, app, _, _ := newAppTaskCoordinatorFixture(t, 1, 600, 1024)
	reg := prometheus.NewPedanticRegistry()
	m := NewAppTaskMetrics(reg, true, 1)
	wrapped := &cancelOnFirstAppTaskCompletionStore{AppTaskStore: store, accountID: account.ID, appID: app.ID}
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return appTaskSessionFuncs{execute: func(ctx context.Context, _ AppTaskExecuteRequest) (AppTaskOutcome, error) {
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for testutil.ToFloat64(m.operations.WithLabelValues("renew", "success")) < 1 {
				select {
				case <-ctx.Done():
					return AppTaskOutcome{}, ctx.Err()
				case <-ticker.C:
				}
			}
			exit := 0
			return AppTaskOutcome{Status: state.AppTaskSucceeded, ExitCode: &exit}, nil
		}, destroy: func(context.Context) error { return nil }}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := NewAppTaskCoordinator(wrapped, backend, AppTaskCoordinatorConfig{Enabled: true, Metrics: m, LeaseDuration: time.Second, LeaseRenewInterval: 5 * time.Millisecond, SweepInterval: 5 * time.Millisecond, PollInterval: time.Millisecond}, nil)
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for testutil.ToFloat64(m.results.WithLabelValues("manual", "cancelled")) < 1 || testutil.ToFloat64(m.operations.WithLabelValues("claim", "empty")) < 1 {
		select {
		case <-deadline.C:
			t.Fatal("runner made no progress")
		case <-ticker.C:
		}
	}
	got := taskGauges(t, reg)
	if got["schedd_app_task_runner_running"] != 1 || got["schedd_app_task_sweep_last_success_timestamp_seconds"] <= 0 || got["schedd_app_task_claim_last_success_timestamp_seconds"] <= 0 {
		t.Fatal(got)
	}
	if testutil.ToFloat64(m.operations.WithLabelValues("complete", "cancellation_pending")) != 1 || testutil.ToFloat64(m.operations.WithLabelValues("complete", "error")) != 0 || testutil.ToFloat64(m.results.WithLabelValues("manual", "succeeded")) != 0 {
		t.Fatal("cancellation race counted as infrastructure error or success")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
	if taskGauges(t, reg)["schedd_app_task_runner_running"] != 0 {
		t.Fatal("stopped runner remains running")
	}
}

type deadlineCompletingAppTaskStore struct{ state.AppTaskStore }

func (s deadlineCompletingAppTaskStore) CompleteAppTask(ctx context.Context, _ state.CompleteAppTaskParams) (state.AppTask, error) {
	<-ctx.Done()
	return state.AppTask{}, status.Error(codes.DeadlineExceeded, "fixture RPC deadline")
}

func TestAppTaskCoordinatorMetricsDeadlineAttribution(t *testing.T) {
	for _, mode := range []string{"execute_deadline", "restore_deadline", "restore_cancel", "destroy_deadline", "complete_deadline"} {
		t.Run(mode, func(t *testing.T) {
			store, _, _, _, tasks := newAppTaskCoordinatorFixture(t, 1, 1, 1024)
			reg := prometheus.NewPedanticRegistry()
			m := NewAppTaskMetrics(reg, true, 1)
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			backend := appTaskBackendFunc(func(ctx context.Context, _ AppTaskRestoreRequest) (AppTaskSession, error) {
				if mode == "restore_deadline" {
					<-ctx.Done()
					return nil, status.Error(codes.DeadlineExceeded, "fixture RPC deadline")
				}
				if mode == "restore_cancel" {
					cancel()
					return nil, status.Error(codes.Canceled, "fixture RPC cancellation")
				}
				return appTaskSessionFuncs{execute: func(ctx context.Context, _ AppTaskExecuteRequest) (AppTaskOutcome, error) {
					if mode == "execute_deadline" {
						<-ctx.Done()
						return AppTaskOutcome{}, status.Error(codes.DeadlineExceeded, "fixture command deadline")
					}
					exit := 0
					return AppTaskOutcome{Status: state.AppTaskSucceeded, ExitCode: &exit}, nil
				}, destroy: func(ctx context.Context) error {
					if mode == "destroy_deadline" {
						<-ctx.Done()
						return status.Error(codes.DeadlineExceeded, "fixture teardown deadline")
					}
					return nil
				}}, nil
			})
			var taskStore state.AppTaskStore = store
			if mode == "complete_deadline" {
				taskStore = deadlineCompletingAppTaskStore{AppTaskStore: store}
			}
			c := NewAppTaskCoordinator(taskStore, backend, AppTaskCoordinatorConfig{Enabled: true, Metrics: m, RestoreTimeout: 5 * time.Millisecond, DestroyTimeout: 5 * time.Millisecond, FinalizeTimeout: 5 * time.Millisecond}, nil)
			processed, err := c.ProcessNext(parent)
			expectErr := mode == "restore_cancel" || mode == "destroy_deadline" || mode == "complete_deadline"
			if !processed || (err != nil) != expectErr {
				t.Fatal(processed, err)
			}
			op, outcome := "execute", "canceled"
			switch mode {
			case "restore_deadline":
				op, outcome = "restore", "error"
			case "restore_cancel":
				op = "restore"
			case "destroy_deadline":
				op, outcome = "destroy", "error"
			case "complete_deadline":
				op, outcome = "complete", "error"
			}
			if v := testutil.ToFloat64(m.operations.WithLabelValues(op, outcome)); v != 1 {
				t.Fatalf("%s/%s=%v", op, outcome, v)
			}
			other := "error"
			if outcome == "error" {
				other = "canceled"
			}
			if v := testutil.ToFloat64(m.operations.WithLabelValues(op, other)); v != 0 {
				t.Fatalf("misattributed %s/%s=%v", op, other, v)
			}
			if mode == "execute_deadline" {
				task, err := store.AppTaskByID(t.Context(), tasks[0].AccountID, tasks[0].AppID, tasks[0].ID)
				if err != nil || task.Status != state.AppTaskTimedOut || testutil.ToFloat64(m.results.WithLabelValues("manual", "timed_out")) != 1 {
					t.Fatal("command timeout verdict changed", task.Status, err)
				}
			}
		})
	}
}
