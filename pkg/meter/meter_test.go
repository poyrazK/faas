package meter_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeNotifier records every Notify call so tests can assert on the
// payload without standing up Postgres.
type fakeNotifier struct {
	mu       sync.Mutex
	captured []capturedNotify
}

type capturedNotify struct {
	Channel string
	Payload string
}

func (f *fakeNotifier) Notify(_ context.Context, channel, payload string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captured = append(f.captured, capturedNotify{Channel: channel, Payload: payload})
	return nil
}

func (f *fakeNotifier) byChannel(ch string) []capturedNotify {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []capturedNotify
	for _, c := range f.captured {
		if c.Channel == ch {
			out = append(out, c)
		}
	}
	return out
}

// fakeParker records ParkInstance calls. Returns nil so meterd's
// quota loop continues.
type fakeParker struct {
	mu      sync.Mutex
	parked  []parkedCall
	parkErr error
}

type parkedCall struct {
	InstanceID string
	Reason     string
}

func (p *fakeParker) ParkInstance(_ context.Context, instanceID, reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.parked = append(p.parked, parkedCall{InstanceID: instanceID, Reason: reason})
	return p.parkErr
}

// fakeMailer records every Send call so quota tests can assert on the
// customer-facing email surface (dedupe gate, body shape). Mirrors
// fakeNotifier/fakeParker; satisfies meter.DunningSender's local
// interface (Send(ctx, mail.Message) error).
type fakeMailer struct {
	mu      sync.Mutex
	sent    []mail.Message
	sendErr error
}

func (m *fakeMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return m.sendErr
}

func (m *fakeMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

// makeAccount returns an active account with the given plan. MemStore
// CreateAccount does the row write.
func makeAccount(t *testing.T, ctx context.Context, s *state.MemStore, plan api.Plan) state.Account {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "u-"+string(plan)+"@example.com", plan)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return acct
}

// makeLiveInstance plants an instance in RUNNING with the given RAM and
// account. Schedd would normally do this; the meter tests skip the state
// machine and just mutate the slice via CreateInstance.
func makeLiveInstance(t *testing.T, ctx context.Context, s *state.MemStore, appID, accountID string, ramMB int) state.Instance {
	t.Helper()
	ins, err := s.CreateInstance(ctx, appID, "deployment-test", string(state.StateRunning), ramMB, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return ins
}

func newApp(t *testing.T, ctx context.Context, s *state.MemStore, accountID string) state.App {
	t.Helper()
	a, err := s.CreateApp(ctx, state.App{
		AccountID: accountID,
		Slug:      "test-app",
		Type:      state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	return a
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestCheckQuota_Bands is the per-plan ladder truth table. The rule is
// pinned to the financial model — never "improve" these thresholds.
func TestCheckQuota_Bands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		plan   api.Plan
		usedGB float64
		want   string
		pct    int
	}{
		{"free under", api.PlanFree, 4.0, "", 80},
		{"free at", api.PlanFree, 5.0, "stop", 100},
		{"free over", api.PlanFree, 12.5, "stop", 250},
		{"hobby under", api.PlanHobby, 49.0, "", 98},
		{"hobby at", api.PlanHobby, 50.0, "warn", 100},
		{"hobby over", api.PlanHobby, 200.0, "warn", 400},
		{"pro over", api.PlanPro, 2500.0, "warn", 999}, // capped at 999
		{"scale over", api.PlanScale, 150000.0, "warn", 999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := meter.CheckQuota(tc.plan, tc.usedGB)
			if r.Action != tc.want {
				t.Fatalf("Action = %q, want %q (plan=%s used=%.3f)", r.Action, tc.want, tc.plan, tc.usedGB)
			}
			if r.Percent != tc.pct {
				t.Fatalf("Percent = %d, want %d", r.Percent, tc.pct)
			}
			if r.QuotaGB == 0 {
				t.Fatalf("QuotaGB = 0 for %s — table drift", tc.plan)
			}
		})
	}
}

// TestMonthlyUsageGB_Math pins down the rounding so the financial-model
// cells line up without float drift. The helper rounds to 6 dp so the
// shadow-side totals match the financial-model cells within 0.1 %.
func TestMonthlyUsageGB_Math(t *testing.T) {
	t.Parallel()
	usages := []state.Usage{{MBSeconds: 1_000_000}}
	got := meter.MonthlyUsageGB(usages)
	want := float64(1_000_000) / 1024.0 / 3600.0
	wantRounded := float64(int64(want*1e6+0.5)) / 1e6
	if got != wantRounded {
		t.Fatalf("MonthlyUsageGB = %v, want %v (6-dp rounded)", got, wantRounded)
	}
}

// TestSampler_RollsOneMinutePerInstance walks the full sample path:
// create one app, one RUNNING instance, sample once, assert exactly one
// row with admissionMB * 60 mb_seconds.
func TestSampler_RollsOneMinutePerInstance(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 30, 0, time.UTC)
	clock := func() time.Time { return now }

	acct := makeAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	ins := makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)
	_ = ins

	sampler := meter.NewSampler(s, clock)
	rows, err := sampler.SampleAndRoll(ctx)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.AdmissionMB != 256+api.PerVMOverheadMB {
		t.Fatalf("AdmissionMB = %d, want %d", got.AdmissionMB, 256+api.PerVMOverheadMB)
	}
	if got.MBSeconds != int64(256+api.PerVMOverheadMB)*60 {
		t.Fatalf("MBSeconds = %d, want %d", got.MBSeconds, (256+api.PerVMOverheadMB)*60)
	}

	// Read-back via UsageByMonth — the per-month shape the dashboard uses.
	month := meter.AccountMonthKey(now)
	rows2, err := s.UsageByMonth(ctx, acct.ID, month)
	if err != nil {
		t.Fatalf("usage by month: %v", err)
	}
	if len(rows2) != 1 {
		t.Fatalf("rows2 = %d, want 1", len(rows2))
	}
	if rows2[0].MBSeconds != int64(256+api.PerVMOverheadMB)*60 {
		t.Fatalf("UsageByMonth MBSeconds = %d, want %d",
			rows2[0].MBSeconds, int64(256+api.PerVMOverheadMB)*60)
	}
}

// TestSampler_SkipsParkedInstances is the invariant §6.2-4 guard: a parked
// instance's cgroup is gone, so it must not accrue usage.
func TestSampler_SkipsParkedInstances(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	acct := makeAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	_, _ = s.CreateInstance(ctx, app.ID, "dep1", string(state.StateParked), 256, state.DefaultLocalNodeName, "")

	rows, err := meter.NewSampler(s, func() time.Time { return now }).SampleAndRoll(ctx)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 (parked)", len(rows))
	}
}

// TestInvoiceShadow24h is the §14 M7 acceptance gate: a 256 MB + 8 MB
// Hobby instance resident for 24 h accrues (264 * 86400) mb_seconds,
// which is exactly (264/1024) GB-hours = 0.2578125. Shadow math must
// match the hand-computed number within 0.1 %.
func TestInvoiceShadow24h(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	acct := makeAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	_, _ = s.CreateInstance(ctx, app.ID, "dep1", string(state.StateRunning), 256, state.DefaultLocalNodeName, "")

	sampler := meter.NewSampler(s, clock)
	const minutesIn24h = 24 * 60
	for i := 0; i < minutesIn24h; i++ {
		now = now.Add(time.Minute)
		if _, err := sampler.SampleAndRoll(ctx); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}

	month := meter.AccountMonthKey(time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC))
	usages, err := s.UsageByMonth(ctx, acct.ID, month)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(usages) != 1 {
		t.Fatalf("usage rows = %d, want 1", len(usages))
	}

	shadowGB := meter.MonthlyUsageGB(usages)
	wantMB := (256 + api.PerVMOverheadMB) * 60 * minutesIn24h
	wantGB := meter.GBHours(int64(wantMB))
	delta := shadowGB - wantGB
	if delta < 0 {
		delta = -delta
	}
	if delta/wantGB > 0.001 {
		t.Fatalf("invoice shadow delta %.6f GB (%.4f%%) exceeds 0.1%%: shadow=%.6f want=%.6f",
			delta, delta/wantGB*100, shadowGB, wantGB)
	}
}

// TestFreeHardStop is the §14 M7 acceptance gate: a Free account crossing
// 5 GB-h flips to suspended and parks every live instance.
func TestFreeHardStop(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)

	acct := makeAccount(t, ctx, s, api.PlanFree)
	app := newApp(t, ctx, s, acct.ID)
	ins := makeLiveInstance(t, ctx, s, app.ID, acct.ID, 128)

	// Plant 6 GB-h worth of usage directly via AppendUsage so we don't
	// have to spin 6*1024/128 = 48 hours of sampling. (5 GB-h * 3600 s
	// * 1024 MB = 18_432_000 mb_seconds for the 128 MB instance, but
	// the test uses the math: 5 GB-h overage = 5.0 GB.)
	month := meter.AccountMonthKey(now)
	mbSecAtQuota := int64(float64(api.PlanFree.PlanIncludedGBHours()) * 1024 * 3600)
	// Simulate one minute at 128+8 = 136 MB, repeated until past quota.
	for {
		row, err := s.UsageByMonth(ctx, acct.ID, month)
		if err != nil {
			t.Fatalf("usage: %v", err)
		}
		var used int64
		for _, u := range row {
			used += u.MBSeconds
		}
		if used >= mbSecAtQuota {
			break
		}
		now = now.Add(time.Minute)
		if err := s.AppendUsage(ctx, acct.ID, app.ID, ins.ID, now, 136*60, 0); err != nil {
			t.Fatalf("append usage: %v", err)
		}
	}
	now = now.Add(time.Minute)

	notif := &fakeNotifier{}
	parker := &fakeParker{}
	mailer := &fakeMailer{}

	usages, err := s.UsageByMonth(ctx, acct.ID, month)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	usedGB := meter.MonthlyUsageGB(usages)
	if usedGB < float64(api.PlanFree.PlanIncludedGBHours()) {
		t.Fatalf("usedGB = %.4f, want ≥ 5.0", usedGB)
	}
	if _, err := meter.EnforceQuota(ctx, s, notif, parker, mailer, discardLog(), acct, usedGB, now); err != nil {
		t.Fatalf("enforce: %v", err)
	}

	// Account should be suspended.
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if got.Status != state.AccountSuspended {
		t.Fatalf("status = %s, want suspended", got.Status)
	}

	// ParkInstance should have been called for the live instance.
	if len(parker.parked) != 1 {
		t.Fatalf("parked = %d, want 1: %+v", len(parker.parked), parker.parked)
	}
	if parker.parked[0].InstanceID != ins.ID {
		t.Fatalf("parked id = %s, want %s", parker.parked[0].InstanceID, ins.ID)
	}
	if parker.parked[0].Reason != "quota_exceeded_free" {
		t.Fatalf("reason = %q, want quota_exceeded_free", parker.parked[0].Reason)
	}

	// billing_past_due event should have been emitted.
	calls := notif.byChannel(db.NotifyBillingPastDue)
	if len(calls) != 1 {
		t.Fatalf("billing_past_due = %d, want 1", len(calls))
	}
}

// TestPaidOverageNoStop is the §14 M7 acceptance gate for paid overage:
// the account accrues but is NOT suspended.
func TestPaidOverageNoStop(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)

	acct := makeAccount(t, ctx, s, api.PlanPro)
	app := newApp(t, ctx, s, acct.ID)
	_, _ = s.CreateInstance(ctx, app.ID, "dep1", string(state.StateRunning), 512, state.DefaultLocalNodeName, "")

	// Plant usage equal to one Hobby quota so CheckQuota at Pro's 250 GB-h
	// threshold still trips. (5 GB-h * 1024 * 3600 = 18_432_000.)
	if err := s.AppendUsage(ctx, acct.ID, app.ID, "inst1", now, 18_432_000*100, 0); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	usages, err := s.UsageByMonth(ctx, acct.ID, meter.AccountMonthKey(now))
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	usedGB := meter.MonthlyUsageGB(usages)

	notif := &fakeNotifier{}
	parker := &fakeParker{}
	mailer := &fakeMailer{}
	if _, err := meter.EnforceQuota(ctx, s, notif, parker, mailer, discardLog(), acct, usedGB, now); err != nil {
		t.Fatalf("enforce: %v", err)
	}

	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if got.Status == state.AccountSuspended {
		t.Fatalf("Pro account was suspended — overage should accrue, not stop")
	}
	if len(parker.parked) != 0 {
		t.Fatalf("parked = %d, want 0 (paid overage must not park)", len(parker.parked))
	}
	if warns := notif.byChannel(db.NotifyQuotaWarning); len(warns) == 0 {
		t.Fatalf("quota_warning not emitted on paid overage")
	}
}

// TestPaidOverageDedupesPerDay is the audit-finding #1 closure: a
// paid-tier account over its quota emits exactly one quota_warning
// pg_notify event per UTC day, across the dedupe column on the account
// row. Same-day repeats are no-ops; the stamp advances at UTC midnight.
func TestPaidOverageDedupesPerDay(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	// Anchor on a non-midnight hour so the day-rollover is mid-test.
	day1Hour1 := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	day1Hour2 := day1Hour1.Add(2 * time.Hour)
	day2Hour1 := day1Hour1.Add(25 * time.Hour) // next UTC day, same wall hour

	acct := makeAccount(t, ctx, s, api.PlanPro)
	app := newApp(t, ctx, s, acct.ID)
	_, _ = s.CreateInstance(ctx, app.ID, "dep1", string(state.StateRunning), 512, state.DefaultLocalNodeName, "")
	// Plant usage equal to one Hobby quota so CheckQuota at Pro's
	// 250 GB-h threshold trips.
	if err := s.AppendUsage(ctx, acct.ID, app.ID, "inst1", day1Hour1, 18_432_000*100, 0); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	usages, err := s.UsageByMonth(ctx, acct.ID, meter.AccountMonthKey(day1Hour1))
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	usedGB := meter.MonthlyUsageGB(usages)

	notif := &fakeNotifier{}
	parker := &fakeParker{}
	mailer := &fakeMailer{}
	log := discardLog()

	// Tick 1: first warning of the day.
	if _, err := meter.EnforceQuota(ctx, s, notif, parker, mailer, log, acct, usedGB, day1Hour1); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if warns := notif.byChannel(db.NotifyQuotaWarning); len(warns) != 1 {
		t.Fatalf("tick 1: quota_warning = %d, want 1", len(warns))
	}
	if n := mailer.count(); n != 1 {
		t.Fatalf("tick 1: mailer.sent = %d, want 1 (first day warning must email)", n)
	}

	// Tick 2: same UTC day — must NOT emit a second warning.
	if _, err := meter.EnforceQuota(ctx, s, notif, parker, mailer, log, acct, usedGB, day1Hour2); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if warns := notif.byChannel(db.NotifyQuotaWarning); len(warns) != 1 {
		t.Fatalf("tick 2: quota_warning = %d, want 1 (same-day repeat must dedupe)", len(warns))
	}
	if n := mailer.count(); n != 1 {
		t.Errorf("tick 2: mailer.sent = %d, want 1 (same-day repeat must dedupe)", n)
	}

	// Tick 3: next UTC day — fresh warning.
	if _, err := meter.EnforceQuota(ctx, s, notif, parker, mailer, log, acct, usedGB, day2Hour1); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if warns := notif.byChannel(db.NotifyQuotaWarning); len(warns) != 2 {
		t.Fatalf("tick 3: quota_warning = %d, want 2 (next-day must emit a fresh warning)", len(warns))
	}
	if n := mailer.count(); n != 2 {
		t.Errorf("tick 3: mailer.sent = %d, want 2 (next-day must email)", n)
	}

	// ClearQuotaWarning (apid's payment_succeeded hook) lets the next
	// tick of the SAME day emit a fresh warning. Without the clear,
	// the second day-2 tick would still dedupe.
	if err := s.ClearQuotaWarning(ctx, acct.ID); err != nil {
		t.Fatalf("ClearQuotaWarning: %v", err)
	}
	if _, err := meter.EnforceQuota(ctx, s, notif, parker, mailer, log, acct, usedGB, day2Hour1); err != nil {
		t.Fatalf("tick 4: %v", err)
	}
	if warns := notif.byChannel(db.NotifyQuotaWarning); len(warns) != 3 {
		t.Fatalf("tick 4 (post-Clear): quota_warning = %d, want 3", len(warns))
	}
	if n := mailer.count(); n != 3 {
		t.Errorf("tick 4 (post-Clear): mailer.sent = %d, want 3", n)
	}
}

// TestAppendUsagePerInstanceMinute pins the MemStore contract: AppendUsage is
// idempotent on (instance_id, minute) — the first write wins; a redelivered
// minute is a no-op. Different minutes stay separate. Matches the production
// INSERT … ON CONFLICT (instance_id, minute) DO NOTHING semantics introduced
// by the M7 hardening PR (feat/m7-beta-hardening).
func TestAppendUsagePerInstanceMinute(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	m := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	acct := makeAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)

	// First write for (inst-A, m) — wins, mb_seconds=100, requests=1.
	if err := s.AppendUsage(ctx, acct.ID, app.ID, "inst-A", m, 100, 1); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	// Redelivered minute: a no-op. mb_seconds stays 100, requests stays 1.
	if err := s.AppendUsage(ctx, acct.ID, app.ID, "inst-A", m, 50, 2); err != nil {
		t.Fatalf("append 2 (redelivered): %v", err)
	}
	// Different minute: separate row.
	if err := s.AppendUsage(ctx, acct.ID, app.ID, "inst-A", m.Add(time.Minute), 75, 1); err != nil {
		t.Fatalf("append 3: %v", err)
	}
	usages, err := s.UsageByMonth(ctx, acct.ID, meter.AccountMonthKey(m))
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(usages) != 1 {
		t.Fatalf("rows = %d, want 1 (per app)", len(usages))
	}
	// First write wins for m: 100 mb_seconds + 1 request. The m+1 row adds 75 + 1.
	if usages[0].MBSeconds != 100+75 {
		t.Fatalf("MBSeconds = %d, want 175 (100 from first m-write + 75 from m+1)", usages[0].MBSeconds)
	}
	if usages[0].Requests != 1+1 {
		t.Fatalf("Requests = %d, want 2 (1 from first m-write + 1 from m+1)", usages[0].Requests)
	}
}

// TestAccountByStripeCustomerID_NotFound: pkg/state.Store.AccountByStripeCustomerID
// lands in Slice 2 (pkg/stripex). When that method is added this test
// should be moved to pkg/state/memstore_test.go and extended. For now
// we skip — the slice-2 PR adds the assertion.
func TestAccountByStripeCustomerID_NotFound(t *testing.T) {
	t.Skip("AccountByStripeCustomerID is added in Slice 2; this test is the migration target")
}
