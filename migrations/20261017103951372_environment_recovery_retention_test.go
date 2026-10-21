package migrations_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRecoveryRetentionMigrationUsesCentralCeilingsAndReplayGuard(t *testing.T) {
	raw, err := migrations.FS.ReadFile("20261017103951372_environment_recovery_retention.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		fmt.Sprintf("NOT BETWEEN 1 AND %d", api.EnvironmentRecoveryMaxReleases),
		fmt.Sprintf("NOT BETWEEN 1 AND %d", api.EnvironmentRecoveryMaxSeconds),
		fmt.Sprintf("NOT BETWEEN 1 AND %d", api.EnvironmentRecoveryMaxBytes),
		fmt.Sprintf("reserved>%d", api.EnvironmentRecoveryAccountMaxBytes),
		"IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='project_environments'::regclass AND tgname='environment_recovery_metadata_guard' AND NOT tgisinternal) THEN RETURN; END IF;",
		"-- +goose NO TRANSACTION",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatal("migration drifted from contract", want)
		}
	}
}
