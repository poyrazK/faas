package sched

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/onebox-faas/faas/pkg/workpolicy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type commandObservationReaderFunc func(context.Context, string, string) ([]state.CommandCronObservation, error)

func (f commandObservationReaderFunc) CommandCronObservations(ctx context.Context, owner, local string) ([]state.CommandCronObservation, error) {
	return f(ctx, owner, local)
}

// adr: 20261017150710907 — Read-only schedule evidence and committed admission outcomes.
func TestCommandCronMetricsFreshnessAndIndependentRead(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newCommandCronMetrics(reg)
	now := time.Unix(1700000000, 0).UTC()
	start := now
	m.now = func() time.Time { return now }
	const count = "schedd_command_cron_schedules/due"
	const last = "schedd_command_cron_collection_last_success_timestamp_seconds"
	if _, ok := primeGauges(t, reg)[count]; ok {
		t.Fatal("startup fabricated empty schedules")
	}
	r := commandObservationReaderFunc(func(ctx context.Context, owner, local string) ([]state.CommandCronObservation, error) {
		if owner != "owner" || local != "local" {
			t.Error(owner, local)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > api.ObsCommandCronCollectionBudget {
			t.Error("unbounded read")
		}
		now = now.Add(time.Second)
		return []state.CommandCronObservation{{Schedule: "* * * * *", CreatedAt: start.Add(-time.Hour), Enabled: true, AccountActive: true, LiveDeployment: true, TimingValid: true, PolicyValid: true, BindingValid: true}}, nil
	})
	if err := m.refresh(t.Context(), r, "owner", "local"); err != nil {
		t.Fatal(err)
	}
	got := primeGauges(t, reg)
	if len(got) != 36 || got[count] != 1 || got[last] != primeUnix(start) || got["schedd_command_cron_schedules/unknown"] != 0 {
		t.Fatal(got)
	}
	now = start.Add(api.ObsCommandCronFreshnessMaxAge)
	if _, ok := primeGauges(t, reg)[count]; !ok {
		t.Fatal("exact ceiling expired")
	}
	now = now.Add(time.Nanosecond)
	if _, ok := primeGauges(t, reg)[count]; ok {
		t.Fatal("stale work visible")
	}
	if err := m.refresh(t.Context(), nil, "", ""); err == nil {
		t.Fatal("missing reader hidden")
	}
	got = primeGauges(t, reg)
	if got[last] != primeUnix(start) || got["schedd_command_cron_collection_success"] != 0 {
		t.Fatal(got)
	}
	empty := commandObservationReaderFunc(func(context.Context, string, string) ([]state.CommandCronObservation, error) { return nil, nil })
	if err := m.refresh(t.Context(), empty, "", ""); err != nil {
		t.Fatal(err)
	}
	if v, ok := primeGauges(t, reg)[count]; !ok || v != 0 {
		t.Fatal("known empty lost")
	}
	now = now.Add(-time.Second)
	if _, ok := primeGauges(t, reg)[count]; ok {
		t.Fatal("future observation accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.refresh(ctx, empty, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatal("late canceled success", err)
	}
	// Scraping must neither wait for the database nor retain its mutex.
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	blocked := commandObservationReaderFunc(func(context.Context, string, string) ([]state.CommandCronObservation, error) {
		close(entered)
		<-release
		return nil, errors.New("failed")
	})
	go func() { _ = m.refresh(t.Context(), blocked, "", ""); close(done) }()
	<-entered
	scraped := make(chan struct{})
	go func() { _, _ = reg.Gather(); close(scraped) }()
	select {
	case <-scraped:
	case <-time.After(time.Second):
		t.Error("scrape blocked on observation")
	}
	close(release)
	<-done
}
func TestCommandCronSnapshotRejectsFabricatedEvidence(t *testing.T) {
	now := time.Now().UTC()
	for name, p := range map[string]state.AppTaskObservationCohort{
		"negative": {Pending: -1}, "excess unknown": {Pending: 1, UnknownAge: 2}, "missing time": {Pending: 1},
		"time without work": {OldestAt: now}, "unknown with time": {Pending: 1, UnknownAge: 1, OldestAt: now}, "future": {Pending: 1, OldestAt: now.Add(time.Second)},
	} {
		t.Run(name, func(t *testing.T) {
			var s commandCronSnapshot
			s[commandCronDue] = p
			if validateCommandCronSnapshot(s, now) == nil {
				t.Fatal("accepted", s)
			}
		})
	}
	var s commandCronSnapshot
	s[commandCronSuspended] = state.AppTaskObservationCohort{Pending: 1, OldestAt: now}
	if validateCommandCronSnapshot(s, now) == nil {
		t.Fatal("suspended age fabricated")
	}
}
func TestCommandCronMetricsActualAdmissionDispositions(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"queued", "coalesced", "missed_deadline", "skipped_overlap", "replacement_wait", "deployment_unavailable"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				s := taskObservationStore(t, backend)
				ctx := t.Context()
				now := time.Now().UTC().Truncate(time.Minute).Add(15 * time.Second)
				boundary := now.Truncate(time.Minute)
				policy := &workpolicy.SchedulePolicy{Version: 1, Overlap: "allow", MissedRuns: "skip", StartDeadlineSeconds: 120}
				if mode == "coalesced" {
					policy.MissedRuns = "coalesce_latest"
				}
				if mode == "missed_deadline" {
					policy.StartDeadlineSeconds = 1
				}
				if mode == "skipped_overlap" {
					policy.Overlap = "skip"
				}
				if mode == "replacement_wait" {
					policy.Overlap = "replace"
				}
				created := boundary.Add(-time.Minute)
				if mode == "coalesced" {
					created = boundary.Add(-3 * time.Minute)
				}
				_, d, c := commandObservationCron(t, s, "admission-metrics", created, policy)
				if mode == "skipped_overlap" || mode == "replacement_wait" {
					// A real leased task stays active until its cancellation is acknowledged.
					_, _, queued, err := s.(state.ScheduledCronOccurrenceStore).CreateScheduledCronAppTaskOccurrence(ctx, c.ID, nil, created.Add(time.Minute), state.CronScheduledOccurrenceOptions{ScheduledFor: created.Add(time.Minute), ScheduleRevision: c.ScheduleRevision})
					if err != nil || !queued {
						t.Fatal(queued, err)
					}
					if mode == "replacement_wait" {
						if _, err := s.(state.AppTaskStore).ClaimNextAppTask(ctx, "worker", boundary, time.Minute); err != nil {
							t.Fatal(err)
						}
					}
					c, err = s.CronByID(ctx, c.ID)
					if err != nil {
						t.Fatal(err)
					}
					now = now.Add(time.Minute)
				}
				if mode == "deployment_unavailable" {
					if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeployFailed, ""); err != nil {
						t.Fatal(err)
					}
				}
				loop := NewLoop(nil, newEngine(t, s, &fakeWakeVMM{}, nil, "1.10.0"), testLog()).WithOpsMetrics(wire.NewOpsMetrics("schedd"))
				m := loop.cronMetrics
				m.now = func() time.Time { return now.Add(time.Minute) }
				loop.dispatchOneCron(ctx, c, now)
				if mode == "replacement_wait" {
					if testutil.ToFloat64(m.decisions.WithLabelValues(mode)) != 1 {
						t.Fatal("missing replacement wait")
					}
					return
				}
				want := float64(1)
				if mode == "coalesced" {
					want = 2
				}
				if v := testutil.ToFloat64(m.admissions.WithLabelValues(mode)); v != want {
					t.Fatalf("%s=%v want %v", mode, v, want)
				}
				if mode == "queued" {
					loop.dispatchOneCron(ctx, c, now)
					if testutil.ToFloat64(m.admissions.WithLabelValues("stale")) != 1 {
						t.Fatal("stale snapshot counted as queued")
					}
				}
				if mode == "deployment_unavailable" {
					fresh, err := s.CronByID(ctx, c.ID)
					if err != nil || fresh.SuspendedReason == "" {
						t.Fatal("suspension policy changed", fresh, err)
					}
				}
			})
		}
	}
}
func TestCommandCronMetricsClosedOutcomesAndLag(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newCommandCronMetrics(reg)
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m.occurrence(ctx, state.ScheduleOccurrence{}, false, errors.New("private error"))
	m.occurrence(t.Context(), state.ScheduleOccurrence{}, false, errors.New("private error"))
	m.occurrence(t.Context(), state.ScheduleOccurrence{ID: "private-id", Status: "private-status", ScheduledFor: now.Add(-time.Minute), CreatedAt: now}, true, nil)
	m.occurrence(t.Context(), state.ScheduleOccurrence{ID: "private-id", Status: "queued", AppTaskID: "private-task", ScheduledFor: now, CreatedAt: now.Add(-time.Second)}, true, nil)
	for _, o := range []string{"canceled", "error", "unknown", "queued"} {
		if testutil.ToFloat64(m.admissions.WithLabelValues(o)) != 1 {
			t.Fatal(o)
		}
	}
	if testutil.ToFloat64(m.lagUnknown) != 1 {
		t.Fatal("invalid lag fabricated zero")
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, f := range families {
		counts[f.GetName()] = len(f.Metric)
		for _, v := range f.Metric {
			for _, label := range v.Label {
				if strings.Contains(label.GetValue(), "private") {
					t.Fatal("payload in metric", label)
				}
			}
		}
		if f.GetName() == "schedd_command_cron_admission_lag_seconds" && f.Metric[0].Histogram.GetSampleSum() != 60 {
			t.Fatal("lag is not durable admission time")
		}
	}
	if counts["schedd_command_cron_admissions_total"] != 11 || counts["schedd_command_cron_decisions_total"] != 14 || counts["schedd_command_cron_sweeps_total"] != 3 {
		t.Fatal(counts)
	}
}
func TestCommandCronSweepProgressAndRegistryLifecycle(t *testing.T) {
	s := state.NewMemStore()
	engine := newEngine(t, s, &fakeWakeVMM{}, nil, "1.10.0")
	first, second := wire.NewOpsMetrics("schedd"), wire.NewOpsMetrics("schedd")
	loop := NewLoop(nil, engine, testLog()).WithOpsMetrics(first)
	m := loop.cronMetrics
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	loop.runCronTick(t.Context())
	if testutil.ToFloat64(m.sweeps.WithLabelValues("success")) != 1 || !m.lastSweep.Equal(now) {
		t.Fatal("empty sweep did not prove progress")
	}
	at := m.beginSweep()
	now = now.Add(time.Second)
	m.finishSweep(t.Context(), at, errors.New("list failure"))
	if !m.lastSweep.Equal(at) || testutil.ToFloat64(m.sweeps.WithLabelValues("error")) != 1 {
		t.Fatal("failure renewed success")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	at = m.beginSweep()
	m.finishSweep(ctx, at, nil)
	if testutil.ToFloat64(m.sweeps.WithLabelValues("canceled")) != 1 {
		t.Fatal("canceled sweep succeeded")
	}
	loop.WithOpsMetrics(first)
	if loop.cronMetrics != m {
		t.Fatal("duplicate registration")
	}
	loop.WithOpsMetrics(second)
	families, err := first.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if strings.HasPrefix(f.GetName(), "schedd_command_cron_") {
			t.Fatal("old registry retained collector")
		}
	}
	stop := loop.startCommandCronObservation(ctx)
	stop()
	if primeGauges(t, second.Registry())["schedd_command_cron_observation_enabled"] != 0 {
		t.Fatal("shutdown implies active observation")
	}
}

func TestCommandCronMetricsExclusivePendingAndJoin(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := taskObservationStore(t, backend)
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Minute)
			a, _, c := commandObservationCron(t, s, "cron-exclusive-metrics", now.Add(-time.Minute), nil)
			if _, err := s.(state.ExclusiveWorkStore).UpsertExclusiveWorkPolicy(ctx, a.AccountID, exclusivework.Policy{Name: "cron-metrics", Scope: "account", MemberAppIDs: []string{a.ID}, Contention: "join_existing", LeaseSeconds: 5, MaxAttemptSeconds: 60}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.(state.ExclusiveTriggerBindingStore).UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{Source: "cron", TriggerID: c.ID, AccountID: a.AccountID, PolicyName: "cron-metrics", Key: json.RawMessage(`"private-business-key"`), EquivalenceKey: "same-command"}); err != nil {
				t.Fatal(err)
			}
			loop := NewLoop(nil, newEngine(t, s, &fakeWakeVMM{}, nil, "1.10.0"), testLog()).WithOpsMetrics(wire.NewOpsMetrics("schedd"))
			m := loop.cronMetrics
			m.now = func() time.Time { return now.Add(2 * time.Minute) }
			loop.dispatchOneCron(ctx, c, now)
			var err error
			c, err = s.CronByID(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			loop.dispatchOneCron(ctx, c, now.Add(time.Minute))
			for _, outcome := range []string{"exclusive_pending", "coalesced"} {
				if testutil.ToFloat64(m.admissions.WithLabelValues(outcome)) != 1 {
					t.Fatal(outcome)
				}
			}
			if testutil.ToFloat64(m.admissions.WithLabelValues("queued")) != 0 {
				t.Fatal("pending exclusive operation reported execution task")
			}
		})
	}
}

type commandCronOwnerErrorStore struct {
	MemStore *state.MemStore
	cancel   context.CancelFunc
}

func (f *commandCronOwnerErrorStore) AppByID(context.Context, string) (state.App, error) {
	if f.cancel != nil {
		f.cancel()
	}
	return state.App{}, errors.New("owner lookup failed")
}
func TestCommandCronSweepCountsActualLookupFailure(t *testing.T) {
	for _, outcome := range []string{"error", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			s := state.NewMemStore()
			now := time.Now().UTC().Truncate(time.Minute)
			commandObservationCron(t, s, "cron-owner-failure", now.Add(-time.Minute), nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fake := &commandCronOwnerErrorStore{MemStore: s}
			if outcome == "canceled" {
				fake.cancel = cancel
			}
			loop := NewLoop(nil, &Engine{store: statetest.NewMem(fake)}, testLog()).WithOpsMetrics(wire.NewOpsMetrics("schedd"))
			loop.runCronTick(ctx)
			if testutil.ToFloat64(loop.cronMetrics.sweeps.WithLabelValues(outcome)) != 1 || !loop.cronMetrics.lastSweep.IsZero() {
				t.Fatal("failed owner lookup renewed progress")
			}
			decision := "lookup_error"
			if outcome == "canceled" {
				decision = "canceled"
			}
			if testutil.ToFloat64(loop.cronMetrics.decisions.WithLabelValues(decision)) != 1 {
				t.Fatal("lookup failure not counted")
			}
		})
	}
}
