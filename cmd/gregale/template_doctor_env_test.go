package main_test

import (
	"path/filepath"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// Production-us rc.294: `gregale init` then `gregale doctor` failed 15 of 33
// templates on env-required. Doctor counted the platform's FAAS_* variables
// as missing, and nine templates read optional knobs or a migration-only
// MIGRATION_DATABASE_URL that serving refuses (Hunt 14 H14-52). Templates
// whose variables are genuinely required (slack-bot, s3-uploader, …) keep
// their warning.
func TestFreshTemplatesPassDoctorEnvCheck(t *testing.T) {
	for _, name := range []string{
		"mcp-node", "mcp-go", "mcp-python", "rest-api-postgres", "rest-api-postgres-python",
		"data-api", "data-api-starter", "data-api-teams",
		"hello-node", "function-python",
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if err := templates.Materialize(name, dir); err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			for _, check := range main.RunDoctorChecks(dir).Checks {
				if check.Name == "env-required" && check.Status != "ok" {
					t.Fatalf("env-required = %s for a fresh %s: %+v", check.Status, name, check)
				}
			}
		})
	}
	dir := filepath.Join(t.TempDir(), "slack-bot")
	if err := templates.Materialize("slack-bot", dir); err != nil {
		t.Fatalf("Materialize slack-bot: %v", err)
	}
	for _, check := range main.RunDoctorChecks(dir).Checks {
		if check.Name == "env-required" && check.Status == "ok" {
			t.Fatal("slack-bot's required SLACK_SIGNING_SECRET must still be reported")
		}
	}
}
