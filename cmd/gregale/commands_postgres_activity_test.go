package main_test

import (
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderPostgresActivity(t *testing.T) {
	until := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	suspended := until.Add(-50 * time.Minute)
	out := main.RenderPostgresActivity(api.ManagedPostgresActivity{DatabaseName: "orders-db", Since: until.Add(-24 * time.Hour), Until: until,
		Starts: 2, AwakeSeconds: 900, SuspendTimeoutSeconds: 120,
		Causes: []api.ManagedPostgresActivityCause{{Cause: "cron", AppSlug: "api", Detail: "/cron/sync", Starts: 1, AwakeSeconds: 600},
			{Cause: "external", Starts: 1, AwakeSeconds: 300}},
		ComputeStarts: []api.ManagedPostgresActivityComputeStart{{StartedAt: until.Add(-5 * time.Minute), Cause: "external"},
			{StartedAt: until.Add(-time.Hour), SuspendedAt: &suspended, Cause: "cron", AppSlug: "api", Detail: "/cron/sync"}},
		Hints: []api.ManagedPostgresActivityHint{{Code: "cron_wakes_database", Cause: "cron", Message: "Cron /cron/sync on api started the database 1 times.", Command: "gregale cron list --app api",
			FixCommands: []string{"gregale postgres suspend-timeout set orders-db 1m"}}},
		DocsURL: api.ManagedPostgresActivityDocsURL})
	for _, want := range []string{
		"orders-db: awake 15m00s of the last 1d00h, 2 starts (suspend timeout 120s)",
		"cron", "/cron/sync", "10m00s",
		"awake", // the open start
		"look: $ gregale cron list --app api",
		"fix:  $ gregale postgres suspend-timeout set orders-db 1m",
		api.ManagedPostgresActivityDocsURL,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// A command that is also the fix prints once, as the fix.
	off := "gregale postgres app-wake set orders-db off"
	wake := main.RenderPostgresActivity(api.ManagedPostgresActivity{DatabaseName: "orders-db", Since: until.Add(-time.Hour), Until: until,
		Starts: 1, AwakeSeconds: 60, Causes: []api.ManagedPostgresActivityCause{{Cause: "app_wake", Starts: 1, AwakeSeconds: 60}},
		ComputeStarts: []api.ManagedPostgresActivityComputeStart{{StartedAt: until.Add(-time.Minute), Cause: "app_wake"}},
		Hints:         []api.ManagedPostgresActivityHint{{Code: "app_wake_starts_database", Cause: "app_wake", Message: "Turn it off.", Command: off, FixCommands: []string{off}}}})
	if strings.Count(wake, off) != 1 || !strings.Contains(wake, "fix:  $ "+off) {
		t.Errorf("wake output:\n%s", wake)
	}
	empty := main.RenderPostgresActivity(api.ManagedPostgresActivity{DatabaseName: "orders-db", Since: until.Add(-time.Hour), Until: until})
	if !strings.Contains(empty, "No recorded starts") {
		t.Errorf("empty output:\n%s", empty)
	}
}

func TestRenderPostgresDatabaseUsage(t *testing.T) {
	if out := main.RenderPostgresDatabaseUsage(nil); out != "" {
		t.Fatalf("no databases rendered %q", out)
	}
	out := main.RenderPostgresDatabaseUsage([]api.ManagedPostgresDatabaseUsage{
		{DatabaseID: "db-1", Name: "orders-db", ComputeUnitSeconds: 7200, Starts: 96, AwakeSeconds: 14400},
		{DatabaseID: "db-2", ComputeUnitSeconds: 360},
	})
	for _, want := range []string{"orders-db", "2.00 CU-hours", "96", "4h00m", "(deleted) db-2", "gregale postgres activity DATABASE"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
