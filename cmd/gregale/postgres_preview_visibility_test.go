package main

import (
	"bytes"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestNativePostgresOperationsRequireOptInForDiscovery(t *testing.T) {
	preview := []string{"auto-grow", "backup-schedule", "diagnose", "export", "migrate", "restore-drill", "upgrade-rehearsal"}
	for _, tc := range []struct {
		commands []cliCommand
		enabled  bool
	}{
		{publicReferenceCommands(), false},
		{(capabilityView{all: true}).discoverableCommands(visibleCliCommands()), true},
	} {
		index := slices.IndexFunc(tc.commands, func(c cliCommand) bool { return c.Name == "postgres" })
		if index < 0 {
			t.Fatal("the existing postgres command is missing")
		}
		subs := tc.commands[index].Subcommands
		restore, ok := findCliSubcommand(subs, "restore")
		if !ok {
			t.Fatal("the existing restore command is missing")
		}
		for _, name := range []string{"name", "point-in-time"} {
			i := slices.IndexFunc(restore.Flags, func(f cliFlag) bool { return f.Name == name })
			if i < 0 {
				t.Fatalf("postgres restore --%s is missing", name)
			}
			if got := restore.Flags[i].Req; got != !tc.enabled {
				t.Errorf("postgres restore --%s required = %v, want %v", name, got, !tc.enabled)
			}
		}
		for _, name := range preview {
			if got := slices.ContainsFunc(subs, func(s cliSub) bool { return s.Name == name }); got != tc.enabled {
				t.Errorf("postgres %s discovery = %v, want %v", name, got, tc.enabled)
			}
		}
	}
}

func TestSharedPostgresHelpFiltersNativeOptions(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		view := accountCapabilityView(nil)
		if !enabled {
			view = accountCapabilityView([]string{"native-postgres-operations"})
		}
		commands := map[string]cliCommand{}
		for _, command := range view.discoverableCommands(visibleCliCommands()) {
			commands[command.Name] = command
		}
		postgres, ok := commands["postgres"]
		if !ok {
			t.Fatal("postgres command is missing")
		}
		previews, ok := findCliSubcommand(postgres.Subcommands, "previews")
		if !ok {
			t.Fatal("existing previews command is missing")
		}
		if got := strings.Contains(strings.Join(previews.Notes, "\n"), "masked"); got != enabled {
			t.Errorf("masked preview help = %v, want %v", got, enabled)
		}
		bindings, ok := findCliSubcommand(postgres.Subcommands, "bindings")
		if !ok {
			t.Fatal("existing bindings command is missing")
		}
		if _, got := findCliSubcommand(bindings.Subcommands, "timeouts"); got != enabled {
			t.Errorf("binding timeouts help = %v, want %v", got, enabled)
		}
		rollback, ok := commands["rollback"]
		if !ok {
			t.Fatal("existing rollback command is missing")
		}
		for _, name := range []string{"switch", "switches"} {
			sub, ok := findCliSubcommand(rollback.Subcommands, name)
			if !ok {
				t.Fatalf("existing rollback %s command is missing", name)
			}
			if got := slices.ContainsFunc(sub.Flags, func(f cliFlag) bool { return f.Name == "migration" }); got != enabled {
				t.Errorf("rollback %s --migration help = %v, want %v", name, got, enabled)
			}
		}
	}
}

func TestSharedPostgresUsageOmitsNativeOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() int
		want string
		hide string
	}{
		{"bindings", func() int { return cmdPostgresBindings(nil) }, "rotate", "timeouts"},
		{"bindings create", func() int { return cmdPostgresBindingsCreate([]string{"--access", "invalid"}) }, "read_only", "replica_only"},
		{"add postgres", func() int { return cmdAddPostgres([]string{"--access", "invalid"}) }, "read_only", "replica_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			_ = captureStderrSwap(t, &stderr, tc.run)
			if got := stderr.String(); !strings.Contains(got, tc.want) || strings.Contains(got, tc.hide) {
				t.Errorf("usage = %q, want %q and no %q", got, tc.want, tc.hide)
			}
		})
	}
	if !postgresAccessOK("replica_only") {
		t.Error("explicit replica-only access no longer reaches the provider check")
	}
}

// adr: 1027 — in-place major upgrades are a preview until launch: `gregale
// postgres upgrade` stays callable but out of customer help, completions,
// the usage line and the published CLI reference.
func TestPostgresUpgradeHiddenFromCustomerDiscovery(t *testing.T) {
	for _, command := range cliCommands {
		if command.Name != "postgres" {
			continue
		}
		for _, sub := range command.Subcommands {
			if sub.Name == "upgrade" {
				t.Fatalf("help and completions list `postgres %s`", sub.Name)
			}
		}
	}
	reference, err := os.ReadFile("../../docs/cli-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	inPlaceUpgrade := regexp.MustCompile("(?m)(^|[ \t`])postgres upgrade([ \t`]|$)")
	if inPlaceUpgrade.Match(reference) {
		t.Error("the published CLI reference documents `postgres upgrade`")
	}
	source, err := os.ReadFile("commands_postgres.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "|upgrade|") {
		t.Error("the postgres usage line lists upgrade")
	}
}

// adr: 1027 — storage growth is a preview until launch: `postgres resize`
// keeps --storage-bytes for its accounts but never shows it in help or a
// usage line, and capabilities print storage_resize only to its accounts.
func TestPostgresStorageGrowthHiddenFromCustomerHelp(t *testing.T) {
	var stderr bytes.Buffer
	_ = captureStderrSwap(t, &stderr, func() int {
		code := cmdPostgres([]string{"resize", "-h"})
		_ = cmdPostgres([]string{"resize", "orders"}) // a usage error
		return code
	})
	if !strings.Contains(stderr.String(), "usage: gregale postgres resize") || strings.Contains(stderr.String(), "storage") {
		t.Fatalf("resize help = %q; want the usage without storage", stderr.String())
	}
}
