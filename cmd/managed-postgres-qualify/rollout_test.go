package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestRolloutVerifierReportsMissingEvidenceWithoutSecrets(t *testing.T) {
	dir := t.TempDir()
	config := managedpostgres.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "neon-test"}, Backends: []managedpostgres.BackendConfig{{ID: "neon-test", Driver: "neon", Region: "us-east-1", Namespace: "org-test", Settings: map[string]string{"region_id": "aws-us-east-1", "max_storage_bytes": "107374182400", "max_restore_window_seconds": "604800"}, SecretEnv: map[string]string{"api-key": "TEST_NEON_KEY"}}}}
	configPath, approvalPath := filepath.Join(dir, "config.json"), filepath.Join(dir, "provider.json")
	data, _ := json.Marshal(config)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(managedpostgres.QualificationArtifact{Version: managedpostgres.QualificationArtifactVersion})
	if err := os.WriteFile(approvalPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{managedpostgres.EnvironmentEnv: "staging", "FAAS_MANAGED_POSTGRES_CONFIG": configPath, "TEST_NEON_KEY": "private-provider-token", managedpostgres.QualificationApprovalPathEnv: approvalPath}
	for _, tc := range []struct {
		name                   string
		args                   []string
		envKey, envValue, want string
	}{
		{"explicit rollout", []string{"--rollout"}, "", "", "guest_report_missing"},
		{"implicit input", []string{"--guest-report", filepath.Join(dir, "missing.json")}, "", "", "guest_report_unreadable"},
		{"daemon requirement", nil, managedpostgres.RequireGuestQualificationEnv, "TRUE", "guest_report_missing"},
		{"malformed policy", nil, managedpostgres.RequireGuestQualificationEnv, "invalid", "guest_requirement_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envKey != "" {
				values[tc.envKey] = tc.envValue
				defer delete(values, tc.envKey)
			}
			var output, errors bytes.Buffer
			code := runVerificationArguments(tc.args, func(k string) string { return values[k] }, &output, &errors)
			if code != 1 {
				t.Fatalf("exit=%d stderr=%s stdout=%s", code, errors.String(), output.String())
			}
			var result managedpostgres.RolloutReadiness
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Ready || !strings.Contains(output.String(), tc.want) {
				t.Fatalf("missing blocker: %s", output.String())
			}
			if strings.Contains(output.String()+errors.String(), "private-provider-token") {
				t.Fatal("secret exposed")
			}
		})
	}
}

func TestRolloutVerifierRejectsAmbiguousArguments(t *testing.T) {
	for _, args := range [][]string{{"--guest-report"}, {"--rollout", "--rollout"}, {"--approval", "one", "--approval", "two"}, {"--guest-report", "--rollout"}, {"--unknown"}} {
		var output, errors bytes.Buffer
		if code := runVerificationArguments(args, func(string) string { return "" }, &output, &errors); code != 2 || output.Len() != 0 {
			t.Fatalf("args=%v exit=%d stdout=%s", args, code, output.String())
		}
	}
}
