package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestAddPostgresMigrationHintFitsTheAppType pins that add postgres never
// tells a function app to set release.command on itself: function deploys
// refuse it, and migration credentials reach release tasks only.
func TestAddPostgresMigrationHintFitsTheAppType(t *testing.T) {
	var app, function, readOnly bytes.Buffer
	main.PrintAddPostgresMigrationHint(&app, "db", "web", "app", "default", "read_write")
	main.PrintAddPostgresMigrationHint(&function, "db", "fn", "function", "default", "read_write")
	main.PrintAddPostgresMigrationHint(&readOnly, "db", "fn", "function", "default", "read_only")
	if got := app.String(); !strings.Contains(got, "postgres attach db web") || !strings.Contains(got, "release.command in gregale.yaml") {
		t.Fatalf("app hint = %q", got)
	}
	got := function.String()
	if !strings.Contains(got, "function apps cannot run") || !strings.Contains(got, "type: app") || strings.Contains(got, "attach db fn") {
		t.Fatalf("function hint = %q, want it to route migrations through a type: app workload", got)
	}
	if readOnly.Len() != 0 {
		t.Fatalf("read-only binding printed %q", readOnly.String())
	}
}
