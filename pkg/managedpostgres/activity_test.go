package managedpostgres

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Hints cover the causes holding at least a fifth of the awake time,
// largest first, never requests, and none for an always-on database.
func TestActivityHints(t *testing.T) {
	database := Database{ID: "db-1", Name: "orders", Spec: Spec{ScaleToZero: true}}
	causes := []ComputeStartCause{
		{Cause: CauseCron, AppSlug: "api", Detail: "/cron/sync", Starts: 96, AwakeSeconds: 6000},
		{Cause: CauseRequest, AppSlug: "api", Starts: 10, AwakeSeconds: 3000},
		{Cause: CauseExternal, Starts: 1, AwakeSeconds: 2500},
		{Cause: CauseAppStart, AppSlug: "api", Starts: 1, AwakeSeconds: 100},
	}
	hints := ActivityHints(database, causes, ActivityHintContext{SuspendTimeoutSeconds: 300})
	if len(hints) != 2 || hints[0].Code != "cron_wakes_database" || hints[1].Code != "external_client" {
		t.Fatalf("hints = %+v", hints)
	}
	shorten := "gregale postgres suspend-timeout set orders 1m"
	// The message keeps the command for clients that predate FixCommands.
	if hints[0].Command != "gregale cron list --app api" || !strings.Contains(hints[0].Message, "/cron/sync") ||
		!strings.Contains(hints[0].Message, "96 times") || !strings.Contains(hints[0].Message, "`"+shorten+"`") ||
		!slices.Equal(hints[0].FixCommands, []string{shorten}) {
		t.Fatalf("cron hint = %+v", hints[0])
	}
	// The timeout is one setting: offered on the first hint only.
	if hints[1].Command != "gregale postgres bindings list orders" || hints[1].FixCommands != nil || strings.Contains(hints[1].Message, "suspend-timeout") {
		t.Fatalf("external hint = %+v", hints[1])
	}
	// At the shortest timeout, or an unknown one, there is nothing to run.
	for _, timeout := range []int64{60, 0} {
		if hints := ActivityHints(database, causes, ActivityHintContext{SuspendTimeoutSeconds: timeout}); hints[0].FixCommands != nil || strings.Contains(hints[0].Message, "suspend-timeout") {
			t.Fatalf("hint at timeout %d = %+v", timeout, hints[0])
		}
	}
	// A name another database shares is replaced by the ID.
	if hints := ActivityHints(database, causes, ActivityHintContext{Ref: "db-1", SuspendTimeoutSeconds: 300}); hints[0].FixCommands[0] != "gregale postgres suspend-timeout set db-1 1m" ||
		hints[1].Command != "gregale postgres bindings list db-1" {
		t.Fatalf("hints by ID = %+v", hints)
	}
	// Wake-with-app is fixed by turning it off, only while it is on; a code
	// change has no command.
	wakeCauses := []ComputeStartCause{{Cause: CauseAppWake, Starts: 5, AwakeSeconds: 600}, {Cause: CauseRunningApp, AppSlug: "api", Starts: 5, AwakeSeconds: 600}}
	wake := ActivityHints(database, wakeCauses, ActivityHintContext{SuspendTimeoutSeconds: 300, WakeWithApp: true})
	off := "gregale postgres app-wake set orders off"
	if len(wake) != 2 || !slices.Equal(wake[0].FixCommands, []string{off}) || wake[0].Command != off || wake[1].FixCommands != nil {
		t.Fatalf("wake hints = %+v", wake)
	}
	if wake := ActivityHints(database, wakeCauses, ActivityHintContext{SuspendTimeoutSeconds: 300}); wake[0].FixCommands != nil || wake[0].Command != "" {
		t.Fatalf("wake hint with wake-with-app already off = %+v", wake[0])
	}
	database.Spec.ScaleToZero = false
	if hints := ActivityHints(database, causes, ActivityHintContext{}); hints != nil {
		t.Fatalf("always-on hints = %+v", hints)
	}
}

// listedStore lists fixed databases, as the account's list would.
type listedStore struct {
	*MemoryStore
	items []Database
}

func (s *listedStore) ListCustomerDatabases(context.Context, string) ([]Database, error) {
	return s.items, nil
}

// Commands name the database by name unless another database the account
// lists (a deleted one kept for recovery, say) shares it, then by ID: the CLI
// refuses an ambiguous name.
func TestActivityDatabaseRef(t *testing.T) {
	ctx := context.Background()
	store := &listedStore{MemoryStore: NewMemoryStore()}
	service, err := NewService(testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, func(*Config) {}), store, ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	orders := Database{ID: "db-1", Name: "orders"}
	store.items = []Database{orders, {ID: "db-2", Name: "events"}}
	if ref := service.databaseRef(ctx, "account-a", orders); ref != "orders" {
		t.Fatalf("unique name ref = %q", ref)
	}
	store.items = append(store.items, Database{ID: "db-3", Name: "orders", State: StateDeleted})
	if ref := service.databaseRef(ctx, "account-a", orders); ref != "db-1" {
		t.Fatalf("shared name ref = %q", ref)
	}
}
