package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func commandObservationCron(t *testing.T, s state.Store, slug string, at time.Time, policy *workpolicy.SchedulePolicy) (state.App, state.Deployment, state.Cron) {
	t.Helper()
	account, err := s.CreateAccount(t.Context(), slug+"@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	a, d := primeObservationCandidate(t, s, account.ID, slug, nil)
	if err := s.UpdateDeploymentStatus(t.Context(), d.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateCronWithOptions(t.Context(), a.ID, "* * * * *", "", true, state.CronOptions{Command: []string{"/app/private-command", "secret-argument"}, SchedulePolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.UpdateCron(t.Context(), c.ID, nil, nil, nil, &at)
	if err != nil {
		t.Fatal(err)
	}
	return a, d, c
}
func commandObservation(t *testing.T, s state.Store, now time.Time, owner, local string) commandCronSnapshot {
	t.Helper()
	rows, err := s.(state.CommandCronObservationStore).CommandCronObservations(t.Context(), owner, local)
	if err != nil {
		t.Fatal(err)
	}
	got, err := observeCommandCrons(t.Context(), rows, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCommandCronSnapshot(got, now); err != nil {
		t.Fatal(err, got)
	}
	return got
}

// adr: 20261017150710907 — Read-only schedule evidence and committed admission outcomes.
func TestCommandCronObservationPolicyAndTiming(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	base := state.CommandCronObservation{Schedule: "* * * * *", Timezone: "UTC", CreatedAt: now.Add(-2 * time.Minute), Enabled: true, AccountActive: true, LiveDeployment: true, TimingValid: true, PolicyValid: true, BindingValid: true}
	cases := []struct {
		name  string
		edit  func(*state.CommandCronObservation)
		class commandCronClass
		at    time.Time
	}{
		{"first boundary", func(*state.CommandCronObservation) {}, commandCronDue, now.Add(-time.Minute)},
		{"inclusive due", func(r *state.CommandCronObservation) { r.LastFiredAt = now.Add(-time.Minute) }, commandCronDue, now},
		{"exclusive cursor", func(r *state.CommandCronObservation) { r.LastFiredAt = now }, commandCronNotDue, time.Time{}},
		{"disabled", func(r *state.CommandCronObservation) { r.Enabled = false; r.Schedule = "invalid" }, commandCronDisabled, time.Time{}},
		{"suspended", func(r *state.CommandCronObservation) { r.Suspended = true }, commandCronSuspended, time.Time{}},
		{"deleted", func(r *state.CommandCronObservation) { r.AppDeleted = true }, commandCronSuspended, time.Time{}},
		{"held", func(r *state.CommandCronObservation) { r.AccountActive = false }, commandCronAccountHeld, time.Time{}},
		{"missing deployment", func(r *state.CommandCronObservation) { r.LiveDeployment = false }, commandCronDeploymentMissing, now.Add(-time.Minute)},
		{"overlap pending decision", func(r *state.CommandCronObservation) { r.SkipIfRunning = true; r.ActiveTasks = true }, commandCronOverlapDue, now.Add(-time.Minute)},
		{"allow overlap", func(r *state.CommandCronObservation) { r.ActiveTasks = true }, commandCronDue, now.Add(-time.Minute)},
		{"replace waits", func(r *state.CommandCronObservation) {
			r.ActiveTasks = true
			r.Policy = &workpolicy.SchedulePolicy{Version: 1, Overlap: "replace", MissedRuns: "skip"}
		}, commandCronReplaceWait, now.Add(-time.Minute)},
		{"exclusive does not predict lane readiness", func(r *state.CommandCronObservation) {
			r.ExclusiveBound = true
			r.ActiveTasks = true
			r.Policy = &workpolicy.SchedulePolicy{Version: 1, Overlap: "replace", MissedRuns: "skip"}
		}, commandCronExclusiveDue, now.Add(-time.Minute)},
		{"ordinary overlap before exclusive", func(r *state.CommandCronObservation) {
			r.ExclusiveBound = true
			r.ActiveTasks = true
			r.ActiveOrdinaryTasks = true
			r.SkipIfRunning = true
		}, commandCronOverlapDue, now.Add(-time.Minute)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.edit(&r)
			c, at := classifyCommandCron(r, now)
			if c != tc.class || !at.Equal(tc.at) {
				t.Fatalf("%s %v want %s %v", c, at, tc.class, tc.at)
			}
		})
	}
	for name, edit := range map[string]func(*state.CommandCronObservation){
		"missing creation":        func(r *state.CommandCronObservation) { r.CreatedAt = time.Time{} },
		"future creation":         func(r *state.CommandCronObservation) { r.CreatedAt = now.Add(time.Second) },
		"future cursor":           func(r *state.CommandCronObservation) { r.LastFiredAt = now.Add(time.Second) },
		"invalid finite evidence": func(r *state.CommandCronObservation) { r.TimingValid = false },
		"bad policy":              func(r *state.CommandCronObservation) { r.PolicyValid = false },
		"bad binding":             func(r *state.CommandCronObservation) { r.BindingValid = false },
		"bad schedule":            func(r *state.CommandCronObservation) { r.Schedule = "invalid" },
		"bad timezone":            func(r *state.CommandCronObservation) { r.Timezone = "Invalid/Zone" },
		"never fires":             func(r *state.CommandCronObservation) { r.Schedule = "0 0 30 2 *" },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			edit(&r)
			c, at := classifyCommandCron(r, now)
			if c != commandCronUnknown || !at.IsZero() {
				t.Fatal(c, at)
			}
		})
	}
}
func TestCommandCronObservationDST(t *testing.T) {
	for _, tc := range []struct{ name, schedule, cursor, want string }{
		{"spring gap", "30 2 * * *", "2026-03-08T06:00:00Z", "2026-03-08T07:00:00Z"},
		{"first fall boundary", "30 1 * * *", "2026-11-01T04:00:00Z", "2026-11-01T05:30:00Z"},
		{"fixed fall boundary is not repeated", "30 1 * * *", "2026-11-01T05:30:00Z", "2026-11-02T06:30:00Z"},
		{"interval repeats with clock", "*/30 * * * *", "2026-11-01T05:30:00Z", "2026-11-01T06:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursor, err := time.Parse(time.RFC3339, tc.cursor)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			r := state.CommandCronObservation{Schedule: tc.schedule, Timezone: "America/New_York", CreatedAt: cursor, Enabled: true, AccountActive: true, LiveDeployment: true, TimingValid: true, PolicyValid: true, BindingValid: true}
			c, at := classifyCommandCron(r, want)
			if c != commandCronDue || !at.Equal(want) {
				t.Fatal(c, at, want)
			}
		})
	}
}
func TestCommandCronObservationStoreLifecycle(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := taskObservationStore(t, backend)
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Minute)
			created := now.Add(-time.Minute)
			if got := commandObservation(t, s, now, "", ""); got != (commandCronSnapshot{}) {
				t.Fatal(got)
			}
			a, d, c := commandObservationCron(t, s, "cron-observation", created, nil)
			if _, err := s.CreateCron(ctx, a.ID, "* * * * *", "/private-http", true); err != nil {
				t.Fatal(err)
			}
			assert := func(class commandCronClass, at time.Time) {
				t.Helper()
				got := commandObservation(t, s, now, "", "")
				var want commandCronSnapshot
				p := state.AppTaskObservationCohort{Pending: 1, OldestAt: at}
				if at.IsZero() {
					p.UnknownAge = 1
				}
				want[class] = p
				if got != want {
					t.Fatal(got, want)
				}
			}
			assert(commandCronDue, now)
			if err := s.UpdateAccountStatus(ctx, a.AccountID, state.AccountPastDue); err != nil {
				t.Fatal(err)
			}
			assert(commandCronDue, now)
			if err := s.UpdateAccountStatus(ctx, a.AccountID, state.AccountSuspended); err != nil {
				t.Fatal(err)
			}
			assert(commandCronAccountHeld, time.Time{})
			if err := s.UpdateAccountStatus(ctx, a.AccountID, state.AccountActive); err != nil {
				t.Fatal(err)
			}
			disabled := false
			if _, err := s.UpdateCron(ctx, c.ID, nil, nil, &disabled, nil); err != nil {
				t.Fatal(err)
			}
			assert(commandCronDisabled, time.Time{})
			enabled := true
			if _, err := s.UpdateCron(ctx, c.ID, nil, nil, &enabled, nil); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeployFailed, ""); err != nil {
				t.Fatal(err)
			}
			assert(commandCronDeploymentMissing, now)
			if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := s.(state.CronSuspensionStore).SuspendCronsForApp(ctx, a.ID, "no_live_deployment"); err != nil {
				t.Fatal(err)
			}
			assert(commandCronSuspended, time.Time{})
			got, err := s.CronByID(ctx, c.ID)
			if err != nil || !got.LastFiredAt.IsZero() {
				t.Fatal("observer advanced cursor", got, err)
			}
			history, err := s.(state.ScheduleOccurrenceHistoryStore).ScheduleOccurrenceListByCron(ctx, c.ID, 10, "")
			if err != nil || len(history) != 0 {
				t.Fatal("observer wrote occurrences", history, err)
			}
			tasks, err := s.(state.AppTaskStore).ListCronAppTaskRuns(ctx, c.ID, 10, "")
			if err != nil || len(tasks) != 0 {
				t.Fatal("observer wrote tasks", tasks, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := s.(state.CommandCronObservationStore).CommandCronObservations(canceled, "", ""); err == nil {
				t.Fatal("canceled read succeeded")
			}
		})
	}
}
func TestCommandCronObservationFullBacklogAndOwnership(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := taskObservationStore(t, backend)
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Minute)
			a, _, _ := commandObservationCron(t, s, "cron-backlog", now.Add(-time.Minute), nil)
			// Deliberately exceed all execution page sizes; observation has no page cap.
			for i := range 260 {
				c, err := s.CreateCronWithOptions(ctx, a.ID, "* * * * *", "", true, state.CronOptions{Command: []string{fmt.Sprintf("task-%d", i)}})
				if err != nil {
					t.Fatal(err)
				}
				created := now.Add(-time.Minute)
				if _, err := s.UpdateCron(ctx, c.ID, nil, nil, nil, &created); err != nil {
					t.Fatal(err)
				}
			}
			if got := commandObservation(t, s, now, "", ""); got[commandCronDue].Pending != 261 || !got[commandCronDue].OldestAt.Equal(now) {
				t.Fatal(got)
			}
			local, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			if got := commandObservation(t, s, now, local.ID, ""); got != (commandCronSnapshot{}) {
				t.Fatal("owned list included unplaced", got)
			}
			if got := commandObservation(t, s, now, "", local.ID); got[commandCronDue].Pending != 261 {
				t.Fatal("legacy fallback omitted unplaced", got)
			}
			if err := s.SetAppNodeID(ctx, a.ID, local.ID); err != nil {
				t.Fatal(err)
			}
			if got := commandObservation(t, s, now, local.ID, ""); got[commandCronDue].Pending != 261 {
				t.Fatal(got)
			}
			if got := commandObservation(t, s, now, "other-node", local.ID); got != (commandCronSnapshot{}) {
				t.Fatal("owner fallback leaked local rows", got)
			}
			if got := commandObservation(t, s, now, "", "other-node"); got != (commandCronSnapshot{}) {
				t.Fatal("legacy local filter leaked remote rows", got)
			}
		})
	}
}
func TestCommandCronObservationActualOverlapAndExclusive(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := taskObservationStore(t, backend)
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Minute)
			policy := &workpolicy.SchedulePolicy{Version: 1, Overlap: "replace", MissedRuns: "skip", StartDeadlineSeconds: 120}
			a, _, c := commandObservationCron(t, s, "cron-overlap-observation", now.Add(-2*time.Minute), policy)
			_, _, created, err := s.(state.ScheduledCronOccurrenceStore).CreateScheduledCronAppTaskOccurrence(ctx, c.ID, nil, now.Add(-time.Minute), state.CronScheduledOccurrenceOptions{ScheduledFor: now.Add(-time.Minute), ScheduleRevision: c.ScheduleRevision})
			if err != nil || !created {
				t.Fatal(created, err)
			}
			got := commandObservation(t, s, now, "", "")
			if got[commandCronReplaceWait].Pending != 1 || !got[commandCronReplaceWait].OldestAt.Equal(now) {
				t.Fatal(got)
			}
			owners := s.(state.ExclusiveWorkStore)
			if _, err := owners.UpsertExclusiveWorkPolicy(ctx, a.AccountID, exclusivework.Policy{Name: "cron-observation", Scope: "account", MemberAppIDs: []string{a.ID}, Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.(state.ExclusiveTriggerBindingStore).UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{Source: "cron", TriggerID: c.ID, AccountID: a.AccountID, PolicyName: "cron-observation", Key: json.RawMessage(`"private-key"`)}); err != nil {
				t.Fatal(err)
			}
			got = commandObservation(t, s, now, "", "")
			if got[commandCronOverlapDue].Pending != 1 {
				t.Fatal("observer omitted ordinary overlap before bound admission", got)
			}
		})
	}
}

func TestCommandCronObservationPostgresNonfiniteEvidence(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	now := time.Now().UTC().Truncate(time.Minute)
	_, _, c := commandObservationCron(t, s, "cron-nonfinite", now.Add(-time.Minute), nil)
	for _, column := range []string{"created_at", "last_fired_at"} {
		// Fixture corruption only: production reads remain SQLC-backed.
		query := "UPDATE crons SET " + column + "='infinity' WHERE id=$1"
		if _, err := pool.Exec(t.Context(), query, c.ID); err != nil {
			t.Fatal(err)
		}
		if got := commandObservation(t, s, now, "", ""); got[commandCronUnknown].Pending != 1 {
			t.Fatal(got)
		}
		if _, err := pool.Exec(t.Context(), "UPDATE crons SET created_at=$2,last_fired_at=NULL WHERE id=$1", c.ID, now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCommandCronObservationAbuseHold(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := taskObservationStore(t, backend)
			now := time.Now().UTC().Truncate(time.Minute)
			a, _, _ := commandObservationCron(t, s, "cron-abuse-hold", now.Add(-time.Minute), nil)
			if _, err := s.(state.AccountAbuseHoldStore).SetAccountAbuseHold(t.Context(), a.AccountID, state.AccountAbuseHoldOperator, now); err != nil {
				t.Fatal(err)
			}
			if got := commandObservation(t, s, now, "", ""); got[commandCronAccountHeld].Pending != 1 {
				t.Fatal(got)
			}
		})
	}
}
