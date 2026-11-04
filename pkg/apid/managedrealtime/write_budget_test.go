package managedrealtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1323 — each account's realtime writes share one plan rate, and one
// account's bucket never spends another's.
func TestWriteBudgetPerAccount(t *testing.T) {
	hobby, _ := api.RealtimeLimitsFor(api.PlanHobby)
	free, _ := api.RealtimeLimitsFor(api.PlanFree)
	now := time.Unix(1_800_000_000, 0)
	budget := writeBudget{now: func() time.Time { return now }}
	if _, ok := budget.take("busy", hobby, hobby.RetainedWriteBurst, true); !ok {
		t.Fatal("a full burst was refused")
	}
	steps := []struct {
		name    string
		account string
		limits  api.RealtimeLimits
		n       int
		advance time.Duration
		want    bool
		minWait time.Duration
	}{
		{name: "past the burst", account: "busy", limits: hobby, n: 1, want: false, minWait: time.Second / time.Duration(hobby.RetainedWritesPerSecond)},
		{name: "another account keeps its own bucket", account: "quiet", limits: hobby, n: hobby.RetainedWriteBurst, want: true},
		{name: "refilled at the plan rate", account: "busy", limits: hobby, n: hobby.RetainedWritesPerSecond, advance: time.Second, want: true},
		{name: "more than the bucket ever holds", account: "quiet", limits: hobby, n: hobby.RetainedWriteBurst + 1, advance: time.Hour, want: false, minWait: time.Second},
		{name: "a plan without realtime has no rate", account: "free", limits: free, n: 1_000, want: true},
	}
	for _, step := range steps {
		now = now.Add(step.advance)
		wait, ok := budget.take(step.account, step.limits, step.n, true)
		if ok != step.want || (!ok && wait < step.minWait) {
			t.Fatalf("%s: take = (%v, %v), want ok %v and a wait of at least %v", step.name, wait, ok, step.want, step.minWait)
		}
	}
}

// A publisher credential or schedule reuses a plan read from the account for
// a minute, counted from the read, not from the last write.
func TestWriteBudgetCachesPlansFromReadsOnly(t *testing.T) {
	pro, _ := api.RealtimeLimitsFor(api.PlanPro)
	now := time.Unix(1_800_000_000, 0)
	budget := writeBudget{now: func() time.Time { return now }}
	budget.take("acct", pro, 1, true)
	now = now.Add(writeBudgetPlanTTL / 2)
	budget.take("acct", pro, 1, false)
	if got, ok := budget.cachedLimits("acct"); !ok || got != pro {
		t.Fatalf("cached limits within the TTL = (%+v, %v)", got, ok)
	}
	now = now.Add(writeBudgetPlanTTL/2 + time.Second)
	if _, ok := budget.cachedLimits("acct"); ok {
		t.Fatal("a cached plan outlived its TTL because writes kept using it")
	}
}

// adr: 1323 — a schedule of an account over its write rate is postponed
// without an attempt, so it leaves the due list until the bucket has room.
func TestScheduledWriteOverBudgetIsDeferred(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "schedules-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "schedules"})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{AppID: app.ID, AccountID: acct.ID, Enabled: true}, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if _, err := store.PutManagedRealtimeSchedule(ctx, state.ManagedRealtimeSchedule{EndpointID: ep.ID, Channel: "jobs", ID: id, Data: []byte("tick"), DeliverAt: time.Now().Add(20 * time.Millisecond)}); err != nil {
			t.Fatal(err)
		}
	}
	var due []state.ManagedRealtimeSchedule
	for deadline := time.Now().Add(5 * time.Second); len(due) < 2; {
		if time.Now().After(deadline) {
			t.Fatalf("schedules never came due: %d", len(due))
		}
		time.Sleep(10 * time.Millisecond)
		if due, err = store.ListDueManagedRealtimeSchedules(ctx, 128); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{Host: fakeHost{store: store}}
	hobby, _ := api.RealtimeLimitsFor(api.PlanHobby)
	s.writeBudget.take(acct.ID, hobby, hobby.RetainedWriteBurst, true)
	pass := scheduleWritePass{accounts: map[string]string{}, deferred: map[string]int{}}
	before := time.Now()
	for _, row := range due {
		if s.admitScheduledWrite(ctx, row, &pass) {
			t.Fatalf("schedule %s was admitted past the account's write rate", row.ID)
		}
	}
	rows, err := store.ListManagedRealtimeSchedules(ctx, ep.ID, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	var deferredUntil []time.Time
	for _, row := range rows {
		if row.NextAttemptAt == nil || !row.NextAttemptAt.After(before) || row.Attempts != 0 || row.Status != "pending" {
			t.Fatalf("schedule %s after deferral: next attempt %v, attempts %d, status %s", row.ID, row.NextAttemptAt, row.Attempts, row.Status)
		}
		deferredUntil = append(deferredUntil, *row.NextAttemptAt)
	}
	if len(deferredUntil) != 2 || deferredUntil[0].Equal(deferredUntil[1]) {
		t.Fatalf("deferred until %v, want two distinct times spread at the plan rate", deferredUntil)
	}
	again, err := store.ListDueManagedRealtimeSchedules(ctx, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 && time.Now().Before(deferredUntil[0]) && time.Now().Before(deferredUntil[1]) {
		t.Fatalf("%d schedules due before their deferral passed", len(again))
	}
}

// A write-rate refusal writes nothing, so a keyed publish releases its key
// instead of replaying the 429 to every retry.
func TestWriteRateRefusalIsNotReplayed(t *testing.T) {
	acct := state.Account{ID: "acct", Plan: api.PlanHobby}
	hobby, _ := api.RealtimeLimitsFor(api.PlanHobby)
	s := &Service{}
	s.writeBudget.take(acct.ID, hobby, hobby.RetainedWriteBurst, true)
	capture := &publishCaptureWriter{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	if s.allowRealtimeWrites(capture, acct, 1) {
		t.Fatal("a write past the rate was allowed")
	}
	if capture.status != http.StatusTooManyRequests || !capture.unwritten || capture.Header().Get("Retry-After") == "" {
		t.Fatalf("refusal = %d, unwritten %v, Retry-After %q", capture.status, capture.unwritten, capture.Header().Get("Retry-After"))
	}
	if managedRealtimePublishReplayable(capture.status) && !capture.unwritten {
		t.Fatal("the refusal would be stored for replay")
	}
}
