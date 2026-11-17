package conformance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

type platformAlertTestStore interface {
	state.PlatformAlertNoticeStore
	state.CronFailurePauseStore
}

// testFailingCronPausesAndQueuesNotice pins ADR-20261115194000: an HTTP
// cron whose newest runs all failed, over the span, is paused with one
// cron_failing_paused notice; a customer edit resumes it, and the runs from
// before the pause do not pause it again.
func testFailingCronPausesAndQueuesNotice(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(platformAlertTestStore)
	if !ok {
		t.Skip("store has no platform alerts")
	}
	c, err := fx.Store.CreateCron(fx.Ctx, fx.App.ID, "*/15 * * * *", "/report", true)
	if err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	cronID := c.ID
	run := func(at time.Time, ok bool) {
		t.Helper()
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
			AppID: fx.App.ID, AccountID: fx.Account.ID, Source: state.InvocationCron,
			Method: "POST", Path: "/report", CronID: &cronID, DueAt: at, CreatedAt: at,
		})
		if err != nil {
			t.Fatalf("EnqueueInvocation: %v", err)
		}
		if _, err := fx.Store.ClaimInvocation(fx.Ctx, inv.ID, "", 30); err != nil {
			t.Fatalf("ClaimInvocation: %v", err)
		}
		if ok {
			err = fx.Store.CompleteInvocation(fx.Ctx, inv.ID, nil)
		} else {
			err = fx.Store.FailInvocation(fx.Ctx, inv.ID, "HTTP 404", 0, 0)
		}
		if err != nil {
			t.Fatalf("finish run: %v", err)
		}
	}
	pause := func(label string, want bool) {
		t.Helper()
		got, err := store.PauseCronIfFailing(fx.Ctx, cronID, 3, time.Hour, 24*time.Hour)
		if err != nil || got != want {
			t.Fatalf("%s: PauseCronIfFailing = %v, %v; want %v", label, got, err, want)
		}
	}
	suspended := func(want string) {
		t.Helper()
		got, err := fx.Store.CronByID(fx.Ctx, cronID)
		if err != nil || got.SuspendedReason != want {
			t.Fatalf("suspended_reason = %q, %v; want %q", got.SuspendedReason, err, want)
		}
	}

	base := time.Now().UTC().Add(-3 * time.Hour)
	run(base, false)
	run(base.Add(15*time.Minute), true)
	run(base.Add(30*time.Minute), false)
	run(base.Add(45*time.Minute), false)
	pause("a success among the newest three", false)
	run(base.Add(time.Hour), false)
	pause("three failures over the span", true)
	suspended(state.CronSuspendedFailing)
	pause("already paused", false)

	notices, err := store.ClaimPlatformAlertNotices(fx.Ctx, 100, time.Minute)
	if err != nil {
		t.Fatalf("ClaimPlatformAlertNotices: %v", err)
	}
	var mine []state.PlatformAlertNotice
	for _, n := range notices {
		if n.SubjectID == cronID {
			mine = append(mine, n)
		}
	}
	if len(mine) != 1 || mine[0].Kind != state.PlatformAlertCronFailingPaused || mine[0].AppID != fx.App.ID || mine[0].AccountID != fx.Account.ID {
		t.Fatalf("notices for the cron = %+v; want one cron_failing_paused", mine)
	}
	var detail struct {
		Path       string `json:"path"`
		FailedRuns int    `json:"failed_runs"`
	}
	if err := json.Unmarshal(mine[0].Detail, &detail); err != nil || detail.Path != "/report" || detail.FailedRuns != 3 {
		t.Fatalf("detail = %s, %v", mine[0].Detail, err)
	}
	if err := store.FinishPlatformAlertNotice(fx.Ctx, mine[0].ID, state.PlatformAlertSent, ""); err != nil {
		t.Fatalf("FinishPlatformAlertNotice: %v", err)
	}

	enabled := true
	if _, err := fx.Store.UpdateCronWithOptions(fx.Ctx, cronID, nil, nil, &enabled, nil, nil, nil); err != nil {
		t.Fatalf("UpdateCronWithOptions: %v", err)
	}
	suspended("")
	pause("only runs from before the pause", false)
}

// testPlatformAlertNoticeQueueDedupes pins the notice queue: one pending
// notice per kind and subject, none again within the cooldown, and a
// retried notice waits for its next attempt.
func testPlatformAlertNoticeQueueDedupes(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(platformAlertTestStore)
	if !ok {
		t.Skip("store has no platform alerts")
	}
	subject := uuid.NewString()
	n := state.PlatformAlertNotice{AccountID: fx.Account.ID, AppID: fx.App.ID, Kind: state.PlatformAlertAppOutage,
		SubjectID: subject, Detail: json.RawMessage(`{"error_rate_pct":100}`)}
	queue := func(label string, cooldown time.Duration, want bool) {
		t.Helper()
		got, err := store.QueuePlatformAlertNotice(fx.Ctx, n, cooldown)
		if err != nil || got != want {
			t.Fatalf("%s: QueuePlatformAlertNotice = %v, %v; want %v", label, got, err, want)
		}
	}
	claimMine := func() []state.PlatformAlertNotice {
		t.Helper()
		all, err := store.ClaimPlatformAlertNotices(fx.Ctx, 100, time.Minute)
		if err != nil {
			t.Fatalf("ClaimPlatformAlertNotices: %v", err)
		}
		var mine []state.PlatformAlertNotice
		for _, c := range all {
			if c.SubjectID == subject {
				mine = append(mine, c)
			}
		}
		return mine
	}
	queue("first", time.Hour, true)
	queue("while pending", 0, false)
	got := claimMine()
	if len(got) != 1 || got[0].Attempts != 1 {
		t.Fatalf("claimed = %+v; want one on its first attempt", got)
	}
	if again := claimMine(); len(again) != 0 {
		t.Fatalf("a leased notice was claimed again: %+v", again)
	}
	if err := store.RetryPlatformAlertNotice(fx.Ctx, got[0].ID, time.Now().Add(time.Hour), "smtp down"); err != nil {
		t.Fatalf("RetryPlatformAlertNotice: %v", err)
	}
	if again := claimMine(); len(again) != 0 {
		t.Fatalf("a notice retried for later was claimed now: %+v", again)
	}
	if err := store.FinishPlatformAlertNotice(fx.Ctx, got[0].ID, state.PlatformAlertSent, ""); err != nil {
		t.Fatalf("FinishPlatformAlertNotice: %v", err)
	}
	queue("within the cooldown", time.Hour, false)
	queue("after the cooldown", 0, true)
	if trimmed, err := store.TrimPlatformAlertNotices(fx.Ctx, time.Now().Add(time.Minute)); err != nil || trimmed < 1 {
		t.Fatalf("TrimPlatformAlertNotices = %d, %v; want the sent notice trimmed", trimmed, err)
	}
}
