package state

// adr: 800
import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type regionalTaskBillingFixture struct {
	capacity regionalCapacityFixture
	store    Store
	claim    AppTask
}

func regionalTaskBillingCreate(t *testing.T, s Store, account Account, app string, kind AppTaskKind, base time.Time) AppTask {
	t.Helper()
	dep, err := s.CreateDeployment(t.Context(), Deployment{AppID: app, Kind: DeploymentKindImage, ImageDigest: "sha256:task-billing", Status: DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(t.Context(), dep.ID, "/tmp/task-billing.ext4", "apps/task-billing/rootfs.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if kind == AppTaskKindCron {
		cron, err := s.CreateCronWithOptions(t.Context(), app, "* * * * *", "", true, CronOptions{Command: []string{"bin/maintenance"}, RetryMax: 1, RetryBackoffSeconds: 2})
		if err != nil {
			t.Fatal(err)
		}
		task, created, err := s.CreateScheduledCronAppTask(t.Context(), cron.ID, nil, base)
		if err != nil || !created {
			t.Fatal(task, created, err)
		}
		return task
	}
	task, err := s.CreateAppTask(t.Context(), CreateAppTaskParams{AccountID: account.ID, AppID: app, DeploymentID: dep.ID, Kind: kind, Command: []string{"bin/check"}, TimeoutSeconds: 30, CreatedAt: base})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func newRegionalTaskBillingFixture(t *testing.T, s Store, account Account, cell string, kind AppTaskKind, running bool) regionalTaskBillingFixture {
	t.Helper()
	var claim AppTask
	capacity := newRegionalCapacityFixture(t, s, s.(RegionalRuntimeCapacityLedger), account, cell, func(q regionalQuotaFixture) {
		base := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
		task := regionalTaskBillingCreate(t, s, account, q.grant.Binding.RegionalAppID, kind, base)
		var err error
		claim, err = s.ClaimNextAppTask(t.Context(), "original-task-scheduler", base.Add(time.Second), time.Hour)
		if err != nil || claim.ID != task.ID {
			t.Fatal(claim, err)
		}
		if running {
			if _, err = s.MarkAppTaskRunning(t.Context(), task.ID, *claim.LeaseToken, base.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	})
	return regionalTaskBillingFixture{capacity, s, claim}
}

func taskBillingRows(t *testing.T, s RegionalAppTaskBillingHistoryStore, app string, minute time.Time) []RegionalAppTaskBillingInterval {
	t.Helper()
	rows, err := s.ListRegionalAppTaskBillingIntervals(t.Context(), app, minute, 0, api.RegionalRuntimeCapacityPageMax)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (f regionalTaskBillingFixture) source(t *testing.T) runtimequota.LaunchSource {
	t.Helper()
	p := f.capacity.permit(runtimequota.CapacityAppTask)
	p.RunID, p.DeploymentID = *f.claim.LeaseToken, regionalQuotaCanonicalID(f.claim.DeploymentID)
	var err error
	p.InstanceID, err = runtimequota.AppTaskInstanceID(f.claim.ID, p.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return runtimequota.LaunchSource{Permit: p, InstanceKey: p.InstanceID, TaskID: f.claim.ID}
}

func runRegionalAppTaskBillingHistoryContract(t *testing.T, open func(*testing.T, AppTaskKind, bool) regionalTaskBillingFixture) {
	t.Helper()
	for _, kind := range []AppTaskKind{AppTaskKindManual, AppTaskKindRelease, AppTaskKindCron} {
		t.Run(string(kind), func(t *testing.T) {
			f := open(t, kind, true)
			minute := f.claim.UpdatedAt.Truncate(time.Minute)
			before := taskBillingRows(t, f.store, f.claim.AppID, minute)
			if len(before) != 1 || !before[0].StartedAt.Equal(f.claim.UpdatedAt) || !before[0].CompleteStart || before[0].RunID != *f.claim.LeaseToken || before[0].LeaseOwner != *f.claim.LeaseOwner || before[0].SourceMatches != 0 || before[0].Permit != nil || before[0].Launch != nil {
				t.Fatal("restoring claim or original start missing", before)
			}
			source := f.source(t)
			resources := RegionalRuntimeResources{RAMMB: 192, VCPU: 2, CPUMillicores: 1000}
			if err := f.store.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: source.Permit}, source, resources); err != nil {
				t.Fatal(err)
			}
			rows := taskBillingRows(t, f.store, f.claim.AppID, minute)
			if len(rows) != 1 || rows[0].SourceMatches != 1 || rows[0].Permit == nil || *rows[0].Permit != source.Permit || rows[0].Launch == nil || rows[0].Launch.Source != source || rows[0].Launch.Resources != resources || !rows[0].StartedAt.Equal(before[0].StartedAt) || !rows[0].RecordedAt.Equal(before[0].RecordedAt) {
				t.Fatal("original issued task source lost", rows)
			}
			exit := 0
			end := f.claim.UpdatedAt.Add(5 * time.Second)
			if _, err := f.store.CompleteAppTask(t.Context(), CompleteAppTaskParams{ID: f.claim.ID, LeaseToken: *f.claim.LeaseToken, Status: AppTaskSucceeded, ExitCode: &exit, FinishedAt: end}); err != nil {
				t.Fatal(err)
			}
			closed := taskBillingRows(t, f.store, f.claim.AppID, minute)
			if len(closed) != 1 || closed[0].EndedAt == nil || !closed[0].EndedAt.Equal(end) || closed[0].UnknownSince != nil {
				t.Fatal("normal completion did not retain exact finish", closed)
			}
			f.capacity.usage(t, 1, 1, 0) // Terminal scheduler state does not release the physical hold.
			fence := uuid.NewString()
			if _, err := f.capacity.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.claim.AppID, source.Permit.ID, fence); err != nil {
				t.Fatal(err)
			}
			f.capacity.stop(t, source.Permit, fence)
			if _, err := f.store.ScheduleAppDeletion(t.Context(), f.claim.AppID, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := f.store.ClaimAppDeletion(t.Context(), f.claim.AppID); err != nil {
				t.Fatal(err)
			}
			if err := f.store.DeleteAppPermanently(t.Context(), f.claim.AppID); err != nil {
				t.Fatal(err)
			}
			if got := taskBillingRows(t, f.store, f.claim.AppID, minute); !reflect.DeepEqual(got, closed) {
				t.Fatal("collection rewrote task billing provenance", got, closed)
			}
			closed[0].Permit.NodeID = uuid.NewString()
			closed[0].Launch.Resources.RAMMB++
			*closed[0].EndedAt = time.Time{}
			got := taskBillingRows(t, f.store, f.claim.AppID, minute)
			if *got[0].Permit != source.Permit || got[0].Launch.Resources != resources || !got[0].EndedAt.Equal(end) {
				t.Fatal("returned task history aliases retention", got)
			}
			if later := taskBillingRows(t, f.store, f.claim.AppID, minute.Add(time.Minute)); len(later) != 0 {
				t.Fatal("closed task remained billable", later)
			}
		})
	}
	for _, running := range []bool{false, true} {
		name := "expired-restoring"
		if running {
			name = "expired-running"
		}
		t.Run(name, func(t *testing.T) {
			f := open(t, AppTaskKindManual, running)
			source := f.source(t)
			if err := f.store.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: source.Permit}, source, RegionalRuntimeResources{RAMMB: 128, VCPU: 1, CPUMillicores: 1000}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.SweepExpiredAppTasks(t.Context(), *f.claim.LeaseExpiresAt); err != nil {
				t.Fatal(err)
			}
			minute := f.claim.LeaseExpiresAt.Truncate(time.Minute).Add(time.Minute)
			rows := taskBillingRows(t, f.store, f.claim.AppID, minute)
			if len(rows) != 1 || rows[0].UnknownSince == nil || rows[0].EndedAt != nil || rows[0].RunID != *f.claim.LeaseToken || rows[0].Launch == nil || rows[0].Launch.Source != source {
				t.Fatal("lease expiry invented a stopped VM", rows)
			}
			f.capacity.usage(t, 1, 1, 0)
		})
	}
	for _, provenance := range []string{"reserved", "wrong-run", "wrong-native-identity", "no-source", "no-task-id", "unknown-resources", "ambiguous"} {
		t.Run(provenance, func(t *testing.T) {
			f := open(t, AppTaskKindManual, false)
			source := f.source(t)
			switch provenance {
			case "wrong-native-identity":
				source.Permit.InstanceID = uuid.NewString()
				f.capacity.reserve(t, source.Permit)
				f.capacity.issue(t, source.Permit)
				if _, err := f.store.ListRegionalAppTaskBillingIntervals(t.Context(), f.claim.AppID, f.claim.UpdatedAt.Truncate(time.Minute), 0, 1); !errors.Is(err, ErrConflict) {
					t.Fatal("lease identity borrowed an unrelated VM", err)
				}
				return
			case "reserved":
				f.capacity.reserve(t, source.Permit)
			case "no-source":
				f.capacity.reserve(t, source.Permit)
				f.capacity.issue(t, source.Permit)
			default:
				if provenance == "wrong-run" {
					source.Permit.RunID = uuid.NewString()
					source.Permit.InstanceID, _ = runtimequota.AppTaskInstanceID(source.TaskID, source.Permit.RunID)
					source.InstanceKey = source.Permit.InstanceID
				}
				if provenance == "no-task-id" {
					source.TaskID = ""
				}
				if err := f.store.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: source.Permit}, source); err != nil {
					t.Fatal(err)
				}
				if provenance == "ambiguous" {
					source.Permit.ID, source.Permit.HostEpochID = uuid.NewString(), uuid.NewString()
					if err := f.store.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: source.Permit}, source); err != nil {
						t.Fatal(err)
					}
				}
			}
			rows := taskBillingRows(t, f.store, f.claim.AppID, f.claim.UpdatedAt.Truncate(time.Minute))
			matches := int64(1)
			if provenance == "reserved" || provenance == "wrong-run" {
				matches = 0
			}
			if provenance == "ambiguous" {
				matches = 2
			}
			if len(rows) != 1 || rows[0].SourceMatches != matches {
				t.Fatal("issuance ambiguity hidden", rows)
			}
			if matches == 0 && (rows[0].Permit != nil || rows[0].Launch != nil || rows[0].IssuedAt != nil) {
				t.Fatal("unissued/unrelated lease lent provenance", rows)
			}
			if provenance == "no-source" && rows[0].Launch != nil {
				t.Fatal("native source fabricated", rows)
			}
			if rows[0].Launch != nil && (rows[0].Launch.Resources != (RegionalRuntimeResources{}) || (provenance == "no-task-id" && rows[0].Launch.Source.TaskID != "")) {
				t.Fatal("historical provenance filled from current task", rows)
			}
		})
	}
	t.Run("frozen-publication-and-cancellation", func(t *testing.T) {
		f := open(t, AppTaskKindManual, false)
		minute := f.claim.UpdatedAt.Truncate(time.Minute)
		before := taskBillingRows(t, f.store, f.claim.AppID, minute)
		if _, err := f.store.MarkAppTaskRunning(t.Context(), f.claim.ID, *f.claim.LeaseToken, f.claim.UpdatedAt.Add(time.Second)); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatal(err)
		}
		if _, err := f.store.RequestAppTaskCancellation(t.Context(), f.claim.AccountID, f.claim.AppID, f.claim.ID, f.claim.UpdatedAt.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		if after := taskBillingRows(t, f.store, f.claim.AppID, minute); !reflect.DeepEqual(before, after) {
			t.Fatal("refusal or cancellation request ended billing", after, before)
		}
	})
}

func TestMemRegionalAppTaskBillingHistoryContract(t *testing.T) {
	runRegionalAppTaskBillingHistoryContract(t, func(t *testing.T, kind AppTaskKind, running bool) regionalTaskBillingFixture {
		s := NewMemStore()
		account, err := s.CreateAccount(t.Context(), "task-billing@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return newRegionalTaskBillingFixture(t, s, account, uuid.NewString(), kind, running)
	})
}

func TestMemRegionalAppTaskBillingHistoryPageAndInstallBoundary(t *testing.T) {
	s := NewMemStore()
	start, err := s.RegionalAppTaskBillingHistoryStart(t.Context())
	if err != nil || start.IsZero() {
		t.Fatal(start, err)
	}
	minute := time.Now().UTC().Truncate(time.Minute)
	app := uuid.NewString()
	for _, bad := range []struct {
		app    string
		minute time.Time
		after  int64
		limit  int
	}{{"", minute, 0, 1}, {app, minute.Add(time.Second), 0, 1}, {app, minute, -1, 1}, {app, minute, 0, 0}, {app, minute, 0, api.RegionalRuntimeCapacityPageMax + 1}} {
		if _, err := s.ListRegionalAppTaskBillingIntervals(t.Context(), bad.app, bad.minute, bad.after, bad.limit); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(bad, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.RegionalAppTaskBillingHistoryStart(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.ListRegionalAppTaskBillingIntervals(ctx, app, minute, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func runRegionalAppTaskBillingRecoveryContract(t *testing.T, open func(*testing.T) quotaWriteFixture) {
	t.Helper()
	t.Run("expired-restore-keeps-original-lease", func(t *testing.T) {
		f := open(t)
		base := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
		task := f.task(t, base, nil)
		first, err := f.store.ClaimNextAppTask(t.Context(), "old-scheduler", base.Add(time.Second), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.SweepExpiredAppTasks(t.Context(), *first.LeaseExpiresAt); err != nil {
			t.Fatal(err)
		}
		second, err := f.store.ClaimNextAppTask(t.Context(), "replacement-scheduler", base.Add(7*time.Second), time.Minute)
		if err != nil || second.ID != task.ID || *first.LeaseToken == *second.LeaseToken {
			t.Fatal(second, err)
		}
		rows := taskBillingRows(t, f.store, task.AppID, base)
		if len(rows) != 2 || rows[0].UnknownSince == nil || rows[0].EndedAt != nil || rows[0].RunID != *first.LeaseToken || rows[1].RunID != *second.LeaseToken || !rows[1].StartedAt.Equal(second.UpdatedAt) {
			t.Fatal("replacement lost original uncertain attempt", rows)
		}
		before := rows
		if err = f.store.RenewAppTaskLease(t.Context(), task.ID, *second.LeaseToken, base.Add(8*time.Second), time.Minute); err != nil {
			t.Fatal(err)
		}
		if got := taskBillingRows(t, f.store, task.AppID, base); !reflect.DeepEqual(got, before) {
			t.Fatal("renewal changed task billing start", got)
		}
		if _, err = f.store.CompleteAppTask(t.Context(), CompleteAppTaskParams{ID: task.ID, LeaseToken: *first.LeaseToken, Status: AppTaskCancelled, FinishedAt: base.Add(9 * time.Second)}); !errors.Is(err, ErrAppTaskLeaseLost) {
			t.Fatal(err)
		}
		if got := taskBillingRows(t, f.store, task.AppID, base); !reflect.DeepEqual(got, before) {
			t.Fatal("stale completion changed billing", got)
		}
		if _, err = f.store.CompleteAppTask(t.Context(), CompleteAppTaskParams{ID: task.ID, LeaseToken: *second.LeaseToken, Status: AppTaskCancelled, FinishedAt: base.Add(9 * time.Second)}); err != nil {
			t.Fatal(err)
		}
		rows = taskBillingRows(t, f.store, task.AppID, base)
		if len(rows) != 2 || rows[0].UnknownSince == nil || rows[1].EndedAt == nil || rows[1].UnknownSince != nil {
			t.Fatal("new completion closed old lease", rows)
		}
		page, err := f.store.ListRegionalAppTaskBillingIntervals(t.Context(), task.AppID, base, rows[0].ID, 1)
		if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0], rows[1]) {
			t.Fatal("task billing pagination", page, err)
		}
		if later := taskBillingRows(t, f.store, task.AppID, base.Add(time.Minute)); len(later) != 1 || later[0].RunID != *first.LeaseToken {
			t.Fatal("old uncertain lease disappeared", later)
		}
	})
	t.Run("cron-retry-retains-both-completed-attempts", func(t *testing.T) {
		f := open(t)
		base := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
		task := regionalTaskBillingCreate(t, f.store, f.account, f.quota.grant.Binding.RegionalAppID, AppTaskKindCron, base)
		first, err := f.store.ClaimNextAppTask(t.Context(), "cron-scheduler", base.Add(time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.MarkAppTaskRunning(t.Context(), task.ID, *first.LeaseToken, base.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		code, message := "command_failed", "retryable command failure"
		queued, err := f.store.CompleteAppTask(t.Context(), CompleteAppTaskParams{ID: task.ID, LeaseToken: *first.LeaseToken, Status: AppTaskFailed, FailureCode: &code, FailureMessage: &message, FinishedAt: base.Add(3 * time.Second)})
		if err != nil || queued.Status != AppTaskQueued || queued.StartedAt != nil || queued.FinishedAt != nil || queued.RetryAt == nil {
			t.Fatal(queued, err)
		}
		rows := taskBillingRows(t, f.store, task.AppID, base)
		if len(rows) != 1 || rows[0].EndedAt == nil || !rows[0].EndedAt.Equal(base.Add(3*time.Second)) || rows[0].UnknownSince != nil {
			t.Fatal("retry erased terminal intermediate evidence", rows)
		}
		second, err := f.store.ClaimNextAppTask(t.Context(), "cron-replacement", *queued.RetryAt, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.MarkAppTaskRunning(t.Context(), task.ID, *second.LeaseToken, base.Add(6*time.Second)); err != nil {
			t.Fatal(err)
		}
		exit := 0
		if _, err = f.store.CompleteAppTask(t.Context(), CompleteAppTaskParams{ID: task.ID, LeaseToken: *second.LeaseToken, Status: AppTaskSucceeded, ExitCode: &exit, FinishedAt: base.Add(7 * time.Second)}); err != nil {
			t.Fatal(err)
		}
		rows = taskBillingRows(t, f.store, task.AppID, base)
		if len(rows) != 2 || rows[0].RunID != *first.LeaseToken || rows[1].RunID != *second.LeaseToken || rows[0].EndedAt == nil || rows[1].EndedAt == nil || rows[0].UnknownSince != nil || rows[1].UnknownSince != nil {
			t.Fatal("retry merged original resource intervals", rows)
		}
		if later := taskBillingRows(t, f.store, task.AppID, base.Add(time.Minute)); len(later) != 0 {
			t.Fatal("completed cron attempts still billable", later)
		}
	})
}

func TestMemRegionalAppTaskBillingRecoveryContract(t *testing.T) {
	runRegionalAppTaskBillingRecoveryContract(t, func(t *testing.T) quotaWriteFixture {
		s := NewMemStore()
		account, err := s.CreateAccount(t.Context(), "task-recovery-billing@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return newQuotaWriteFixture(t, s, account, uuid.NewString())
	})
}

func TestMemRegionalAppTaskBillingDeletionAndReplacement(t *testing.T) {
	s := NewMemStore()
	now := time.Now().UTC()
	s.clock = func() time.Time { return now }
	run, owner := uuid.NewString(), "original-scheduler"
	expiry := now.Add(time.Hour)
	task := AppTask{ID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), LeaseToken: &run, LeaseOwner: &owner, LeaseExpiresAt: &expiry, Status: AppTaskRestoring, UpdatedAt: now}
	s.mu.Lock()
	s.putAppTaskLocked(task.ID, task)
	s.mu.Unlock()
	now = now.Add(time.Second)
	replacement := uuid.NewString()
	task.LeaseToken = &replacement
	task.UpdatedAt = now
	s.mu.Lock()
	s.putAppTaskLocked(task.ID, task)
	s.mu.Unlock()
	now = now.Add(time.Second)
	s.mu.Lock()
	s.deleteAppTaskLocked(task.ID)
	s.mu.Unlock()
	rows := taskBillingRows(t, s, task.AppID, now.Truncate(time.Minute).Add(time.Minute))
	if len(rows) != 2 || rows[0].RunID != run || rows[1].RunID != replacement || rows[0].UnknownSince == nil || rows[1].UnknownSince == nil || rows[0].EndedAt != nil || rows[1].EndedAt != nil {
		t.Fatal("replacement/delete proved closure", rows)
	}
}
