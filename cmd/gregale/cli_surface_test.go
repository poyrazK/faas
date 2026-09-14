package main

import (
	"strings"
	"testing"
)

func TestTopLevelUsageGroupsCustomerCommands(t *testing.T) {
	out := topLevelUsage(false)
	for _, want := range []string{
		"Customer commands:",
		"Core:",
		"API:",
		"Data:",
		"Delivery:",
		"Observe:",
		"Operator commands live in gregalectl.",
		"Run 'gregale help --all'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("default help missing %q", want)
		}
	}
	for _, hidden := range []string{"admin", "mail", "rollouts", "github-webhook-secret"} {
		if strings.Contains(out, "  "+hidden+" ") {
			t.Errorf("default help exposes hidden command %q:\n%s", hidden, out)
		}
	}
	for _, visible := range []string{"deploy", "dev", "inspect", "logs", "openapi"} {
		if !strings.Contains(out, "  "+visible+" ") {
			t.Errorf("default help omits customer command %q", visible)
		}
	}
}

func TestTopLevelUsageAllKeepsCompatibilityAliasesDiscoverable(t *testing.T) {
	out := topLevelUsage(true)
	if !strings.Contains(out, "Advanced/operator compatibility:") {
		t.Fatalf("--all help missing advanced section:\n%s", out)
	}
	for _, command := range []string{"admin", "mail", "postgres", "rollouts", "github-webhook-secret"} {
		if !strings.Contains(out, "  "+command+" ") {
			t.Errorf("--all help omits compatibility command %q", command)
		}
	}
}

func TestCliAudienceFiltersOnlyDiscoverySurfaces(t *testing.T) {
	for _, command := range cliCommands {
		if command.Audience == cliAudienceCustomer {
			continue
		}
		if _, ok := lookupCliCommand(command.Name); !ok {
			t.Errorf("hidden command %q is missing from lookup manifest", command.Name)
		}
	}
	if got := len(customerCliCommands()) + len(advancedCliCommands()); got != len(cliCommands) {
		t.Fatalf("audience partition lost commands: customer+advanced=%d manifest=%d", got, len(cliCommands))
	}
}
