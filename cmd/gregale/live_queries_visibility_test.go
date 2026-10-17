package main

import (
	"os"
	"strings"
	"testing"
)

// adr: 1384 — live queries are an operator preview until launch: `gregale
// postgres live` stays callable but out of customer help, completions and
// the published CLI reference.
func TestLiveQueriesHiddenFromCustomerDiscovery(t *testing.T) {
	for _, command := range customerCliCommands() {
		if command.Name != "postgres" {
			continue
		}
		for _, sub := range command.Subcommands {
			if sub.Name == "live" {
				t.Fatal("customer help and completions list `postgres live`")
			}
		}
	}
	reference, err := os.ReadFile("../../docs/cli-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reference), "postgres live") {
		t.Error("the published CLI reference documents `postgres live`")
	}
}
