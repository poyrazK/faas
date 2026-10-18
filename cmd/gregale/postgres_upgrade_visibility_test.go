package main

import (
	"os"
	"strings"
	"testing"
)

// adr: 1027 — in-place major upgrades are a preview until launch: `gregale
// postgres upgrade` stays callable but out of customer help, completions,
// the usage line and the published CLI reference.
func TestPostgresUpgradeHiddenFromCustomerDiscovery(t *testing.T) {
	for _, command := range cliCommands {
		if command.Name != "postgres" {
			continue
		}
		for _, sub := range command.Subcommands {
			if strings.HasPrefix(sub.Name, "upgrade") {
				t.Fatalf("help and completions list `postgres %s`", sub.Name)
			}
		}
	}
	reference, err := os.ReadFile("../../docs/cli-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reference), "postgres upgrade") {
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
