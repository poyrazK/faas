package main

import (
	"bytes"
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
