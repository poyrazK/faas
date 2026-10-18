package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// The durable entity preview (ADR-712) is operator-only: its command and
// starter stay callable but are left out of customer discovery.
func TestDurableEntityPreviewIsHiddenFromCustomerDiscovery(t *testing.T) {
	for _, command := range customerCliCommands() {
		if command.Name == "entities" {
			t.Error("customer help and completions list `entities`")
		}
	}
	if !slices.ContainsFunc(cliCommands, func(c cliCommand) bool { return c.Name == "entities" }) {
		t.Error("`entities` left the manifest; operator scripts would lose dispatch and man coverage")
	}

	if slices.Contains(templates.ListedNames(), "durable-entity-node") {
		t.Error("durable-entity-node is listed for customers")
	}
	if !templates.Exists("durable-entity-node") {
		t.Error("durable-entity-node is no longer materializable by name")
	}

	var buf bytes.Buffer
	if code := runCmdInitList(&buf); code != 0 {
		t.Fatalf("runCmdInitList = %d, want 0", code)
	}
	if out := buf.String(); strings.Contains(out, "durable-entit") {
		t.Errorf("`gregale init --list` mentions the durable entity preview:\n%s", out)
	}
}
