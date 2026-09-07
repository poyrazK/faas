package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreCronOptionsAndActiveCount(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)

	cron, err := m.CreateCronWithOptions(ctx, app.ID, "0 9 * * *", "/daily", true, CronOptions{
		Timezone:      "America/New_York",
		SkipIfRunning: true,
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	if cron.Timezone != "America/New_York" || !cron.SkipIfRunning {
		t.Fatalf("created cron options = %+v", cron)
	}

	defaultCron, err := m.CreateCronWithOptions(ctx, app.ID, "0 * * * *", "/hourly", true, CronOptions{})
	if err != nil {
		t.Fatalf("CreateCronWithOptions default: %v", err)
	}
	if defaultCron.Timezone != "UTC" || defaultCron.SkipIfRunning {
		t.Fatalf("default cron options = %+v", defaultCron)
	}
	quotaCron, err := m.CreateCronIfUnderQuotaWithOptions(ctx, app.ID, "30 * * * *", "/quota", true, api.Limits{
		CronLimitPerApp:     20,
		CronLimitPerAccount: 20,
	}, CronOptions{Timezone: "Europe/Istanbul", SkipIfRunning: true})
	if err != nil {
		t.Fatalf("CreateCronIfUnderQuotaWithOptions: %v", err)
	}
	if quotaCron.Timezone != "Europe/Istanbul" || !quotaCron.SkipIfRunning {
		t.Fatalf("quota cron options = %+v", quotaCron)
	}

	tz, skip, schedule, path, enabled := "", false, "*/15 * * * *", "/quarter-hour", false
	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	updated, err := m.UpdateCronWithOptions(ctx, cron.ID, &schedule, &path, &enabled, &tz, &skip, &createdAt)
	if err != nil {
		t.Fatalf("UpdateCronWithOptions: %v", err)
	}
	if updated.Timezone != "UTC" || updated.SkipIfRunning || updated.Schedule != schedule || updated.Path != path || updated.Enabled || !updated.CreatedAt.Equal(createdAt) {
		t.Fatalf("updated cron options = %+v", updated)
	}
	if _, err := m.UpdateCronWithOptions(ctx, "missing", nil, nil, nil, nil, nil, nil); err != ErrNotFound {
		t.Fatalf("missing cron update error = %v", err)
	}

	cronID := defaultCron.ID
	for _, state := range []InvocationState{InvocationPending, InvocationDispatching, InvocationCompleted} {
		_, err := m.EnqueueInvocation(ctx, Invocation{
			AppID: app.ID, AccountID: account.ID, Source: InvocationCron, State: state, CronID: &cronID,
			DueAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("EnqueueInvocation(%s): %v", state, err)
		}
	}
	otherSource := InvocationQueue
	_, err = m.EnqueueInvocation(ctx, Invocation{
		AppID: app.ID, AccountID: account.ID, Source: otherSource, State: InvocationPending, CronID: &cronID,
		DueAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation(other source): %v", err)
	}
	if got, err := m.CountActiveCronInvocations(ctx, cronID); err != nil || got != 2 {
		t.Fatalf("CountActiveCronInvocations = %d, %v; want 2", got, err)
	}
	if got, err := m.CountActiveCronInvocations(ctx, "other-cron"); err != nil || got != 0 {
		t.Fatalf("CountActiveCronInvocations(other) = %d, %v; want 0", got, err)
	}
}
