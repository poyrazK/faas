//go:build linux

// adr:1659 — migration probes reuse bounded guest dependency checks.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestMigrationServiceConnectionRequiresExactDeclaredHTTPSValue(t *testing.T) {
	check := api.MigrationServiceConnectionCheck{EnvKey: "API_URL", Service: "api", DeploymentID: uuid.NewString()}
	manifest := api.AppManifest{Env: api.ServiceBindingEnv(nil, api.ServiceBindingsForTargets([]string{"api"}))}
	for _, value := range []string{"", "https://other.internal", "http://api.internal", "https://api.internal/path", "https://api.internal?secret=private-value", "https://api.internal"} {
		getenv := func(key string) string {
			if key == "API_URL" {
				return value
			}
			return manifest.Env[key]
		}
		if migrationServiceConnectionMatches(check, getenv) != (value == "https://api.internal") {
			t.Fatal("connection did not require its exact declared HTTPS value")
		}
	}
	if migrationServiceConnectionMatches(check, func(key string) string {
		if key == "API_URL" {
			return "https://api.internal"
		}
		return ""
	}) {
		t.Fatal("undeclared connection accepted")
	}
	// A wrong value is rejected before any network request and never reflected.
	var output bytes.Buffer
	result, err := executeMigrationDependencies(t.Context(), apptaskproto.Request{Command: api.MigrationDependenciesCommand(api.MigrationDependencyChecks{ServiceConnections: []api.MigrationServiceConnectionCheck{check}})}, manifest, map[string]string{"API_URL": "https://private-value.example"}, nil, &output)
	if err != nil || result.Status != apptaskproto.StatusFailed || strings.Contains(output.String(), "private-value") || !strings.Contains(output.String(), "service/API_URL") {
		t.Fatal("invalid connection not safely rejected")
	}
}

func TestMigrationDependenciesMissingAndMalformedValuesAreSanitized(t *testing.T) {
	checks := api.MigrationDependencyChecks{PostgresEnvKey: "PROBE_DATABASE", HTTPEnvKey: "PROBE_API"}
	for _, values := range []map[string]string{{}, {"PROBE_DATABASE": "PRIVATE_SECRET_invalid_url", "PROBE_API": "https://user:PRIVATE_SECRET@localhost/"}} {
		var out bytes.Buffer
		result, err := executeAppTaskCommand(t.Context(), apptaskproto.Request{Command: api.MigrationDependenciesCommand(checks)}, api.AppManifest{}, values, nil, &out, io.Discard)
		if err != nil || result.Status != apptaskproto.StatusFailed {
			t.Fatalf("invalid dependencies passed: %+v %v", result, err)
		}
		if strings.Contains(out.String(), "PRIVATE_SECRET") || strings.Contains(out.String(), "localhost") {
			t.Fatal("secret leaked into report")
		}
		var report api.MigrationDependencyReport
		if err = json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Checks) != 2 {
			t.Fatalf("report: %s %v", out.String(), err)
		}
	}
}
func TestMigrationDependenciesWithRealPostgres(t *testing.T) {
	value := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if value == "" {
		t.Skip("local PostgreSQL fixture not configured")
	}
	var out bytes.Buffer
	result, err := executeAppTaskCommand(t.Context(), apptaskproto.Request{Command: api.MigrationDependenciesCommand(api.MigrationDependencyChecks{PostgresEnvKey: "PROBE_DATABASE"})}, api.AppManifest{}, map[string]string{"PROBE_DATABASE": value}, nil, &out, io.Discard)
	if err != nil || result.Status != apptaskproto.StatusSucceeded {
		t.Fatalf("database check: %+v %s %v", result, out.String(), err)
	}
	if strings.Contains(out.String(), value) {
		t.Fatal("connection value retained")
	}
}
