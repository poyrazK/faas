package meter_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// Billing review H17-4: schedd parks the apps a lowered plan no longer
// covers (ADR-20261103131742). meterd tells the subject which ones, once per
// over-plan set, and again after it fit and went over once more.
func TestPlanFitNoticeNamesTheParkedAppsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := state.NewMemStore()
	acct := makeAccount(t, ctx, s, api.PlanPro)
	for i, slug := range []string{"oldest", "second", "big"} {
		ram := 256
		if slug == "big" {
			ram = 1024
		}
		if _, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: ram,
			Status: state.AppActive, CreatedAt: time.Date(2026, 11, 1+i, 0, 0, 0, 0, time.UTC)}); err != nil {
			t.Fatal(err)
		}
	}
	mailer := &fakeMailer{}
	loop := meter.NewLoop(s, nil, nil, nil, nil, mailer, nil, nil, nil, time.Now, discardLog(), nil, nil)
	run := func() {
		t.Helper()
		if err := loop.RunPlanFitNoticesOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}

	run() // Pro covers all three apps
	if mailer.count() != 0 {
		t.Fatalf("mails on Pro = %d, want 0", mailer.count())
	}
	if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	run()
	if mailer.count() != 1 {
		t.Fatalf("mails after the downgrade = %d, want 1", mailer.count())
	}
	msg := mailer.sent[0]
	if msg.To[0] != acct.Email || !strings.Contains(msg.Subject, "2 of your apps are parked") ||
		!strings.Contains(msg.TextBody, "- big: its RAM (1024 MB) is above the Free plan's 256 MB") ||
		!strings.Contains(msg.TextBody, "- second: the Free plan allows 1 deployed app(s)") ||
		strings.Contains(msg.TextBody, "- oldest") {
		t.Fatalf("mail = %q / %q", msg.Subject, msg.TextBody)
	}
	run()
	if mailer.count() != 1 {
		t.Fatalf("second tick sent %d mails, want 1", mailer.count())
	}
	// Fitting again forgets the notice, so the next excess is mailed.
	if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	run()
	if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	run()
	if mailer.count() != 2 {
		t.Fatalf("mails after a second downgrade = %d, want 2", mailer.count())
	}
}
