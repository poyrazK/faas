package attention

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (f fixture) event(kind string, ago time.Duration, data string) {
	f.exec("INSERT INTO events(at, kind, data) VALUES ($1, $2, $3::jsonb)", f.now.Add(-ago), kind, data)
}

func (f fixture) job(account, name, status string) string {
	id := uuid.NewString()
	f.exec("INSERT INTO jobs(id, account_id, name, status) VALUES ($1, $2, $3, $4)", id, account, name, status)
	return id
}

func (f fixture) runs(job, trigger string, statuses ...string) {
	// statuses run oldest first, an hour apart, ending an hour ago.
	for i, status := range statuses {
		f.exec("INSERT INTO job_runs(job_id, trigger_kind, aggregate_status, finished_at) VALUES ($1, $2, $3, $4)",
			job, trigger, status, f.now.Add(-time.Duration(len(statuses)-i)*time.Hour))
	}
}

// ADR-20261113180000: the five round 2 rules, contact linking, the
// resolved_on_own outcome and the outcome summary.
func TestEvaluateRound2KindsAndOutcomes(t *testing.T) {
	e, pool := pgEvaluator(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	e.Now = func() time.Time { return now }
	f := fixture{t: t, pool: pool, now: now}

	a, b := f.account(), f.account()
	shop, blog := f.app(a, "shop"), f.app(b, "blog")
	oom := `{"app_id":"%s","peak_mb":"%d","plan_mb":512}`
	for i, ago := range []time.Duration{20 * time.Hour, 3 * time.Hour, time.Hour} {
		f.event("instances.workload_oom_failed", ago, strings.Replace(strings.Replace(oom, "%s", shop, 1), "%d", []string{"530", "601", "560"}[i], 1))
	}
	// Two kills, and a third outside the window: no signal.
	for _, ago := range []time.Duration{30 * time.Hour, 2 * time.Hour, time.Hour} {
		f.event("instances.workload_oom_failed", ago, `{"app_id":"`+blog+`","peak_mb":513,"plan_mb":512}`)
	}
	for i := 0; i < api.AttentionBootFailures; i++ {
		f.event("wake.boot_failed", time.Duration(50-i)*time.Minute, `{"app_id":"`+shop+`","reason":"readiness timeout `+string(rune('a'+i))+`"}`)
		f.event("wake.boot_failed", 2*time.Hour, `{"app_id":"`+blog+`","reason":"old"}`)
	}

	f.runs(f.job(a, "nightly", "active"), "scheduled", "succeeded", "failed", "dead_letter", "failed")
	f.runs(f.job(a, "hourly", "active"), "scheduled", "failed", "failed", "succeeded")
	f.runs(f.job(a, "manual", "active"), "manual", "failed", "failed", "failed")
	f.runs(f.job(a, "paused", "paused"), "scheduled", "failed", "failed", "failed")
	f.runs(f.job(b, "flaky", "active"), "scheduled", "failed", "cancelled", "failed", "failed")

	f.exec("INSERT INTO billing_payment_failure_receipts VALUES ($1, 'payment_failed', $2)", a, now.Add(-48*time.Hour))
	f.exec("INSERT INTO billing_payment_failure_receipts VALUES ($1, 'payment_failed', $2)", b, now.Add(-48*time.Hour))
	f.exec("INSERT INTO billing_payment_recovery_receipts VALUES ($1, $2)", b, now.Add(-24*time.Hour))
	f.exec("INSERT INTO billing_payment_failure_receipts VALUES ($1, 'subscription_past_due', $2)", b, now.Add(-20*24*time.Hour))

	for day := 0; day < api.AttentionLimitDays; day++ {
		ago := time.Duration(day) * 24 * time.Hour
		f.refusals(a, "plan_limit_apps", 1, ago)
		f.refusals(a, "job_quota_exceeded", 1, ago)
		f.refusals(a, "api_rate_limited", 1, ago)
		if day > 0 {
			f.refusals(b, "plan_limit_apps", 2, ago)
		}
	}

	result, err := e.Evaluate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	signals, err := e.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[[3]string]string{
		{a, api.AttentionKindAppOOM, "shop"}:                       "OOM-killed 3 times; peak 601 MB of a 512 MB plan",
		{a, api.AttentionKindBootFailing, "shop"}:                  "readiness timeout e",
		{a, api.AttentionKindJobsFailing, "nightly"}:               "last 3 scheduled runs failed (dead_letter, failed)",
		{a, api.AttentionKindPaymentFailing, "billing"}:            "payment failed (payment_failed) and has not recovered",
		{a, api.AttentionKindLimitPressure, "plan_limit_apps"}:     "3 refusals on 3 days",
		{a, api.AttentionKindLimitPressure, "job_quota_exceeded"}: "3 refusals on 3 days",
	}
	for key, detail := range want {
		s := find(signals, key[0], key[1], key[2])
		if s == nil || s.Detail != detail {
			t.Errorf("%v = %+v, want detail %q", key, s, detail)
		}
	}
	if len(signals) != len(want) || result.Opened != int64(len(want)) {
		t.Fatalf("signals = %+v (result %+v), want exactly %d", signals, result, len(want))
	}

	// Contacting dismisses the signal and links the ticket; replaying the
	// same contact is a no-op, a different ticket is refused.
	staff, ticket := f.account(), uuid.NewString()
	payment := find(signals, a, api.AttentionKindPaymentFailing, "billing")
	if err := e.Contacted(ctx, payment.ID, staff, ticket); err != nil {
		t.Fatal(err)
	}
	if err := e.Contacted(ctx, payment.ID, staff, ticket); err != nil {
		t.Fatalf("replayed contact: %v", err)
	}
	if err := e.Contacted(ctx, payment.ID, staff, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second ticket err = %v", err)
	}
	got, err := e.Get(ctx, payment.ID)
	if err != nil || got.State != api.AttentionStateDismissed || got.Outcome != api.AttentionOutcomeContacted || got.TicketID != ticket || got.DismissedBy != staff {
		t.Fatalf("contacted = %+v, %v", got, err)
	}
	if _, err := e.Get(ctx, 1<<40); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing get = %v", err)
	}
	limit := find(signals, a, api.AttentionKindLimitPressure, "plan_limit_apps")
	if err := e.Dismiss(ctx, limit.ID, staff, api.AttentionOutcomeFalsePositive, ""); err != nil {
		t.Fatal(err)
	}

	// The payment recovers and the OOM kills age out: the contacted signal
	// keeps its outcome, the untouched one resolved on its own.
	f.exec("INSERT INTO billing_payment_recovery_receipts VALUES ($1, $2)", a, now)
	f.exec("DELETE FROM events WHERE kind = 'instances.workload_oom_failed'")
	now = now.Add(5 * time.Minute)
	result, err = e.Evaluate(ctx)
	if err != nil || result.Resolved != 2 || result.ResolvedOnOwnByKind[api.AttentionKindAppOOM] != 1 || len(result.ResolvedOnOwnByKind) != 1 {
		t.Fatalf("resolve = %+v, %v", result, err)
	}
	resolved, _ := e.List(ctx, Filter{States: []string{api.AttentionStateResolved}})
	if s := find(resolved, a, api.AttentionKindPaymentFailing, "billing"); s == nil || s.Outcome != api.AttentionOutcomeContacted || s.TicketID != ticket {
		t.Fatalf("resolved payment = %+v", s)
	}
	if s := find(resolved, a, api.AttentionKindAppOOM, "shop"); s == nil || s.Outcome != api.AttentionOutcomeResolvedOnOwn {
		t.Fatalf("resolved oom = %+v", s)
	}

	summary, err := e.Summary(ctx, now.Add(-api.AttentionSummaryWindow))
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]KindSummary{}
	for _, k := range summary {
		byKind[k.Kind] = k
	}
	if k := byKind[api.AttentionKindPaymentFailing]; k.Total != 1 || k.Contacted != 1 || k.MedianActSeconds == nil || k.FalsePositivePercent() != 0 {
		t.Fatalf("payment summary = %+v", k)
	}
	if k := byKind[api.AttentionKindLimitPressure]; k.Total != 2 || k.Open != 1 || k.FalsePositive != 1 || k.FalsePositivePercent() != 100 {
		t.Fatalf("limit summary = %+v", k)
	}
	if k := byKind[api.AttentionKindAppOOM]; k.ResolvedOnOwn != 1 || k.MedianActSeconds != nil || k.FalsePositivePercent() != -1 || k.MedianAct() != "" {
		t.Fatalf("oom summary = %+v", k)
	}
	if summary[0].Kind != api.AttentionKindAppOOM {
		t.Fatalf("summary order = %+v", summary)
	}
}
