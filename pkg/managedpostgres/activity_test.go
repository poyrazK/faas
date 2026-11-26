package managedpostgres

import (
	"slices"
	"strings"
	"testing"
)

// Hints cover the causes holding at least a fifth of the awake time,
// largest first, never requests, and none for an always-on database.
func TestActivityHints(t *testing.T) {
	database := Database{Name: "orders", Spec: Spec{ScaleToZero: true}}
	causes := []ComputeStartCause{
		{Cause: CauseCron, AppSlug: "api", Detail: "/cron/sync", Starts: 96, AwakeSeconds: 6000},
		{Cause: CauseRequest, AppSlug: "api", Starts: 10, AwakeSeconds: 3000},
		{Cause: CauseExternal, Starts: 1, AwakeSeconds: 2500},
		{Cause: CauseAppStart, AppSlug: "api", Starts: 1, AwakeSeconds: 100},
	}
	hints := ActivityHints(database, causes, 300)
	if len(hints) != 2 || hints[0].Code != "cron_wakes_database" || hints[1].Code != "external_client" {
		t.Fatalf("hints = %+v", hints)
	}
	if hints[0].Command != "gregale cron list --app api" || !strings.Contains(hints[0].Message, "/cron/sync") ||
		!strings.Contains(hints[0].Message, "96 times") || !strings.Contains(hints[0].Message, "300 s") ||
		!slices.Equal(hints[0].FixCommands, []string{"gregale postgres suspend-timeout set orders 1m"}) {
		t.Fatalf("cron hint = %+v", hints[0])
	}
	if hints[1].Command != "gregale postgres bindings list orders" ||
		!slices.Equal(hints[1].FixCommands, []string{"gregale postgres suspend-timeout set orders 1m"}) {
		t.Fatalf("external hint = %+v", hints[1])
	}
	// At the shortest timeout, or an unknown one, there is nothing to run.
	for _, timeout := range []int64{60, 0} {
		if hints := ActivityHints(database, causes, timeout); hints[0].FixCommands != nil || strings.Contains(hints[0].Message, "one minute") {
			t.Fatalf("hint at timeout %d = %+v", timeout, hints[0])
		}
	}
	// Wake-with-app is fixed by turning it off; a code change has no command.
	wake := ActivityHints(database, []ComputeStartCause{{Cause: CauseAppWake, Starts: 5, AwakeSeconds: 600},
		{Cause: CauseRunningApp, AppSlug: "api", Starts: 5, AwakeSeconds: 600}}, 300)
	if len(wake) != 2 || !slices.Equal(wake[0].FixCommands, []string{"gregale postgres app-wake set orders off"}) || wake[1].FixCommands != nil {
		t.Fatalf("wake hints = %+v", wake)
	}
	database.Spec.ScaleToZero = false
	if hints := ActivityHints(database, causes, 0); hints != nil {
		t.Fatalf("always-on hints = %+v", hints)
	}
}
