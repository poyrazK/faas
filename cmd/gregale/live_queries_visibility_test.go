package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"strings"
	"testing"
)

// adr: 20261016151947 — Data API live reads are a customer feature
// (`gregale data-api live`); sealed live queries stay an operator preview:
// `gregale postgres live` stays callable but out of customer help,
// completions and the published CLI reference.
func TestLiveQueriesHiddenFromCustomerDiscovery(t *testing.T) {
	dataAPILive := false
	for _, command := range main.CustomerCliCommands() {
		for _, sub := range command.Subcommands {
			switch {
			case command.Name == "postgres" && sub.Name == "live":
				t.Fatal("customer help and completions list `postgres live`")
			case command.Name == "data-api" && sub.Name == "live":
				dataAPILive = true
			}
		}
	}
	if !dataAPILive {
		t.Error("customer help does not list `data-api live`")
	}
	reference, err := os.ReadFile("../../docs/cli-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reference), "postgres live") {
		t.Error("the published CLI reference documents `postgres live`")
	}
	if !strings.Contains(string(reference), "gregale data-api live enable") {
		t.Error("the published CLI reference does not document `data-api live enable`")
	}
}
