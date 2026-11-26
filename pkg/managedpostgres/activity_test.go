package managedpostgres

import (
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
		!strings.Contains(hints[0].Message, "96 times") || !strings.Contains(hints[0].Message, "suspend-timeout set orders 1m") {
		t.Fatalf("cron hint = %+v", hints[0])
	}
	if hints[1].Command != "gregale postgres bindings list orders" {
		t.Fatalf("external hint = %+v", hints[1])
	}
	// At the shortest timeout there is nothing to shorten.
	if hints := ActivityHints(database, causes, 60); strings.Contains(hints[0].Message, "suspend-timeout") {
		t.Fatalf("hint at the minimum timeout = %+v", hints[0])
	}
	database.Spec.ScaleToZero = false
	if hints := ActivityHints(database, causes, 0); hints != nil {
		t.Fatalf("always-on hints = %+v", hints)
	}
}
