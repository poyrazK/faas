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
		if !slices.ContainsFunc(subs, func(s cliSub) bool { return s.Name == "restore" }) {
			t.Fatal("the existing restore command is missing")
		}
		for _, name := range preview {
			if got := slices.ContainsFunc(subs, func(s cliSub) bool { return s.Name == name }); got != tc.enabled {
				t.Errorf("postgres %s discovery = %v, want %v", name, got, tc.enabled)
			}
		}
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
