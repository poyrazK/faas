package alerts_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// seedFailureRule is seedRouteAlertRule with the rule shaped by mutate
// before it is created: a channel-only rule on the 15m window.
func seedFailureRule(t *testing.T, store *state.MemStore, metric state.AlertMetric, mutate func(*state.AlertRule)) (state.AlertRule, *age.X25519Identity) {
	t.Helper()
	ctx := context.Background()
	base, ident, _ := seedRule(t, store, metric, state.AlertGt, 0)
	email, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: base.AccountID, Name: "me", Kind: "email", Email: "me@example.com"}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAlertRule(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	base.ID, base.WindowSpec, base.WebhookURL, base.WebhookSecretSealed = "", state.AlertWindow15m, "", []byte{}
	mutate(&base)
	rule, err := store.CreateAlertRule(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlertRuleChannels(ctx, rule.ID, []string{email.ID}); err != nil {
		t.Fatal(err)
	}
	return rule, ident
}

// cronFailureStore answers the one-cron count and the failed invocation
// details from fixed values and records what it was asked.
type cronFailureStore struct {
	MemStore          *state.MemStore
	n                 int64
	invocations       []state.FailedInvocation
	app, env, cron    string
	listSource        state.AlertFailureSource
	listCron, listApp string
}

func (s *cronFailureStore) CountFailedCronInvocations(_ context.Context, _, appID, environmentID, cronID string, _ time.Time) (int64, error) {
	s.app, s.env, s.cron = appID, environmentID, cronID
	return s.n, nil
}

func (s *cronFailureStore) ListFailedInvocations(_ context.Context, _, appID, _ string, source state.AlertFailureSource, cronID string, _ time.Time, limit int) ([]state.FailedInvocation, error) {
	s.listApp, s.listSource, s.listCron = appID, source, cronID
	return s.invocations[:min(limit, len(s.invocations))], nil
}

// ADR-045 amendment 1: a failed_invocations rule with a cron filter counts
// only that cron, and its notification names the failed invocations.
func TestEvaluatorCronFilteredFailures(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	var cron string
	mem := state.NewMemStore()
	rule, ident := seedFailureRule(t, mem, state.AlertMetricFailedInvocs, func(r *state.AlertRule) {
		c, err := mem.CreateCron(context.Background(), r.AppID, "0 * * * *", "/tasks/bill", true)
		if err != nil {
			t.Fatal(err)
		}
		r.FailureSource, r.CronID, cron = state.AlertFailureCron, c.ID, c.ID
	})
	if rule.CronID != cron {
		t.Fatalf("stored cron filter %q, want %q", rule.CronID, cron)
	}
	store := &cronFailureStore{MemStore: mem, n: 2, invocations: []state.FailedInvocation{{
		ID: "inv-1", Source: "cron", Path: "/tasks/bill", CronID: cron, Attempts: 3,
		LastError: "upstream\n  timed out", FailedAt: now.Add(-2 * time.Minute)}}}
	stats, channels := runFailureCountEvaluator(t, statetest.NewMem(store), ident, now)
	if stats.Fired != 1 || len(channels.sent) != 1 {
		t.Fatalf("stats %+v sent %d, want one fire", stats, len(channels.sent))
	}
	if store.app != rule.AppID || store.cron != cron || store.env != "" {
		t.Fatalf("counted app=%q cron=%q env=%q", store.app, store.cron, store.env)
	}
	if store.listApp != rule.AppID || store.listSource != state.AlertFailureCron || store.listCron != cron {
		t.Fatalf("listed app=%q source=%q cron=%q", store.listApp, store.listSource, store.listCron)
	}
	want := "cron /tasks/bill failed 2026-11-28 11:58 UTC, cron " + cron + " after 3 attempts — upstream timed out"
	if notes := channels.sent[0].msg.Notes; len(notes) == 0 || notes[0] != want {
		t.Fatalf("notes %q, want first %q", notes, want)
	}
}

// jobFailureStore answers job_failures and its details from fixed values.
type jobFailureStore struct {
	MemStore           *state.MemStore
	runs               []state.FailedJobRun
	countJob, listJob  string
	countCalls, listed int
}

func (s *jobFailureStore) CountAccountFailedJobRuns(_ context.Context, _, jobName string, _, _ time.Time) (int64, error) {
	s.countCalls++
	s.countJob = jobName
	return int64(len(s.runs)), nil
}

func (s *jobFailureStore) ListAccountFailedJobRuns(_ context.Context, _, jobName string, _, _ time.Time, limit int) ([]state.FailedJobRun, error) {
	s.listed = limit
	s.listJob = jobName
	return s.runs[:min(limit, len(s.runs))], nil
}

// A job_failures rule with a job filter counts and lists only that job, and
// its notification names at most AlertFailureNotes runs with their first
// error, collapsed to one bounded line.
func TestEvaluatorJobFailureFilterAndNotes(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	mem := state.NewMemStore()
	_, ident := seedFailureRule(t, mem, state.AlertMetricJobFailures, func(r *state.AlertRule) {
		if _, err := mem.JobCreate(context.Background(), r.AccountID, "nightly-export", "batch", "registry.example/job:1", []string{"run"}, 256, 60, 1, 0, nil); err != nil {
			t.Fatal(err)
		}
		r.JobName = "nightly-export"
	})
	long := strings.Repeat("x", 400)
	store := &jobFailureStore{MemStore: mem}
	for i := range 7 {
		store.runs = append(store.runs, state.FailedJobRun{ID: "run-" + string(rune('a'+i)), JobName: "nightly-export", Status: "failed",
			Tasks: 10, TasksFailed: 3, FinishedAt: now.Add(-time.Minute), FirstError: "line one\nline two"})
	}
	store.runs[1].Status, store.runs[1].FirstError = "dead_letter", long
	stats, channels := runFailureCountEvaluator(t, statetest.NewMem(store), ident, now)
	if stats.Fired != 1 || len(channels.sent) != 1 {
		t.Fatalf("stats %+v sent %d, want one fire", stats, len(channels.sent))
	}
	if store.countJob != "nightly-export" || store.listJob != "nightly-export" || store.listed != api.AlertFailureNotes {
		t.Fatalf("count job %q list job %q limit %d", store.countJob, store.listJob, store.listed)
	}
	notes := channels.sent[0].msg.Notes
	if len(notes) < api.AlertFailureNotes {
		t.Fatalf("notes %q, want %d failure notes", notes, api.AlertFailureNotes)
	}
	if want := "Job nightly-export run run-a failed 2026-11-28 11:59 UTC: 3 of 10 tasks failed — line one line two"; notes[0] != want {
		t.Fatalf("note %q, want %q", notes[0], want)
	}
	if !strings.Contains(notes[1], "run-b dead-lettered") || !strings.HasSuffix(notes[1], "…") || len(notes[1]) > 500 {
		t.Fatalf("long error note %q (%d bytes)", notes[1], len(notes[1]))
	}
}

// A boot_failures notification names each failed boot's deployment and
// node, newest first.
func TestEvaluatorBootFailureNotes(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	store := state.NewMemStore()
	rule, ident := seedRouteAlertRule(t, store, state.AlertMetricBootFailures)
	for i, ago := range []time.Duration{3 * time.Minute, time.Minute} {
		data, _ := json.Marshal(map[string]any{"app_id": rule.AppID, "reason": "vmm_boot_failed", "deployment_id": "dep-" + string(rune('1'+i)),
			"node_id": "bhs-2", "method": "cold_boot"})
		if err := store.AppendEventAt(context.Background(), "schedd", "wake.boot_failed", nil, data, now.Add(-ago)); err != nil {
			t.Fatal(err)
		}
	}
	stats, channels := runFailureCountEvaluator(t, store, ident, now)
	if stats.Fired != 1 || len(channels.sent) != 1 {
		t.Fatalf("stats %+v sent %d, want one fire", stats, len(channels.sent))
	}
	notes := channels.sent[0].msg.Notes
	want := []string{"Boot failed 2026-11-28 11:59 UTC, deployment dep-2 (cold_boot) on node bhs-2",
		"Boot failed 2026-11-28 11:57 UTC, deployment dep-1 (cold_boot) on node bhs-2"}
	if len(notes) < 2 || notes[0] != want[0] || notes[1] != want[1] {
		t.Fatalf("notes %q, want %q first", notes, want)
	}
}

// A filtered rule whose job or cron is gone degrades instead of counting
// zero forever: a deploy that changes a cron's schedule replaces it.
func TestEvaluatorFailureFilterTargetGone(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		metric state.AlertMetric
		gone   func(t *testing.T, mem *state.MemStore, r *state.AlertRule)
	}{
		{"deleted cron", state.AlertMetricFailedInvocs, func(t *testing.T, mem *state.MemStore, r *state.AlertRule) {
			c, err := mem.CreateCron(context.Background(), r.AppID, "0 * * * *", "/bill", true)
			if err != nil {
				t.Fatal(err)
			}
			r.FailureSource, r.CronID = state.AlertFailureCron, c.ID
		}},
		{"deleted job", state.AlertMetricJobFailures, func(_ *testing.T, _ *state.MemStore, r *state.AlertRule) { r.JobName = "removed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := state.NewMemStore()
			rule, ident := seedFailureRule(t, mem, tc.metric, func(r *state.AlertRule) { tc.gone(t, mem, r) })
			if rule.CronID != "" {
				if err := mem.DeleteCron(context.Background(), rule.CronID, rule.AppID); err != nil {
					t.Fatal(err)
				}
			}
			if stats, _ := runFailureCountEvaluator(t, mem, ident, now); stats.Fired != 0 {
				t.Fatalf("stats %+v, want no fire", stats)
			}
			got, err := mem.AlertRuleByID(context.Background(), rule.ID)
			if err != nil || got.State != state.AlertStateDegraded {
				t.Fatalf("state %q, %v; want degraded", got.State, err)
			}
		})
	}
}
