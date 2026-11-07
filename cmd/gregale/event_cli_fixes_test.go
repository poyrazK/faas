package main_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A server timestamp slightly ahead of a laptop clock is fresh, not
// future-dated: production-us 2026-11-07, `event subscription get` failed
// 5/5 on a Mac ~115 ms behind the server with "receipt budget is stale or
// future-dated". A timestamp well past the tolerance is still refused.
func TestSubscriptionCapacityBudgetToleratesClockSkew(t *testing.T) {
	app, sub := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	budget := func(observed time.Time) *api.EventSubscriptionCapacityCommandBudget {
		return &api.EventSubscriptionCapacityCommandBudget{
			AppID: app, SubscriptionID: sub, ObservedAt: observed, Complete: true,
			Limit: api.EventSubscriptionCapacityCommandMaxReceipts, RemainingSlots: api.EventSubscriptionCapacityCommandMaxReceipts,
		}
	}
	if err := main.ValidateSubscriptionCapacityCommandBudget(budget(now.Add(115*time.Millisecond)), app, sub, now, time.Minute); err != nil {
		t.Fatalf("a budget 115 ms ahead of the local clock was refused: %v", err)
	}
	if err := main.ValidateSubscriptionCapacityCommandBudget(budget(now.Add(main.ServerClockSkewTolerance+time.Second)), app, sub, now, time.Minute); err == nil {
		t.Fatal("a budget past the clock-skew tolerance was accepted")
	}
}

// `event backfill --help` showed create's synopsis
// (`<create|get|...> --subscription-id ... --yes <app>`) for the whole group,
// so `get <job-id>` looked like it took an app and a time range.
func TestEventBackfillGroupHelpListsVerbsWithoutCreateFlags(t *testing.T) {
	var out bytes.Buffer
	oldOut := *main.OsStdout
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = oldOut })
	if code := main.Run([]string{"event", "backfill", "--help"}); code != 0 {
		t.Fatalf("event backfill --help = %d", code)
	}
	help := out.String()
	if !strings.Contains(help, "gregale event backfill <create|get|items|retry|cancel>") || strings.Contains(help, "--subscription-id") {
		t.Fatalf("group help still carries create's synopsis:\n%s", help)
	}
	out.Reset()
	if code := main.Run([]string{"event", "backfill", "create", "--help"}); code != 0 || !strings.Contains(out.String(), "--subscription-id") {
		t.Fatalf("create help lost its flags (%d):\n%s", code, out.String())
	}
}

// Without --version the server answered with its schema_versions pattern
// ("entries must contain 1..64 ASCII letters ...") instead of naming the
// missing flag.
func TestEventSchemaCheckRequiresVersion(t *testing.T) {
	var errOut bytes.Buffer
	oldErr := *main.OsStderr
	*main.OsStderr = &errOut
	t.Cleanup(func() { *main.OsStderr = oldErr })
	if code := main.Run([]string{"event", "schema", "check", "orders", "order.created"}); code == 0 {
		t.Fatal("schema check without --version succeeded")
	}
	if !strings.Contains(errOut.String(), "--version") || strings.Contains(errOut.String(), "ASCII letters") {
		t.Fatalf("missing --version is not named:\n%s", errOut.String())
	}
}
