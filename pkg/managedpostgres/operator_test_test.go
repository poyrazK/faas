package managedpostgres

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOperatorTestGateRequiresPrivateBoundedExactApproval(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*OperatorTestApproval)
		mode   os.FileMode
		want   bool
	}{
		{name: "operator test", want: true},
		{name: "three-hour boundary", mutate: func(a *OperatorTestApproval) { a.ExpiresAt = a.StartedAt.Add(3 * time.Hour) }, want: true},
		{name: "expired", mutate: func(a *OperatorTestApproval) { a.ExpiresAt = a.StartedAt.Add(time.Minute) }},
		{name: "future", mutate: func(a *OperatorTestApproval) { a.StartedAt = a.StartedAt.Add(time.Hour) }},
		{name: "too long", mutate: func(a *OperatorTestApproval) { a.ExpiresAt = a.StartedAt.Add(3*time.Hour + time.Second) }},
		{name: "purpose", mutate: func(a *OperatorTestApproval) { a.Purpose = "qualified" }},
		{name: "version", mutate: func(a *OperatorTestApproval) { a.Version = 8 }},
		{name: "backend", mutate: func(a *OperatorTestApproval) { a.BackendID = "other" }},
		{name: "placement", mutate: func(a *OperatorTestApproval) { a.BackendFingerprint = "other" }},
		{name: "account", mutate: func(a *OperatorTestApproval) { a.CanaryAccounts = []string{"account-b"} }},
		{name: "empty accounts", mutate: func(a *OperatorTestApproval) { a.CanaryAccounts = nil }},
		{name: "writable approval", mode: 0o666},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, artifact, env, now := productionCanaryFixture(t)
			// Even a valid v8 artifact must not override an invalid operator file.
			writeCanaryArtifact(t, env[QualificationApprovalPathEnv], artifact)
			approval := OperatorTestApproval{Version: 1, Purpose: "operator_test", BackendID: artifact.BackendID, BackendFingerprint: artifact.BackendFingerprint, CanaryAccounts: []string{"account-a"}, StartedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
			if test.mutate != nil {
				test.mutate(&approval)
			}
			path := filepath.Join(t.TempDir(), "operator-test.json")
			encoded, err := json.Marshal(approval)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			if test.mode != 0 {
				if err := os.Chmod(path, test.mode); err != nil {
					t.Fatal(err)
				}
			}
			env[OperatorTestPathEnv] = path
			gate := NewProvisioningGate(registry, func(key string) string { return env[key] }, func() time.Time { return now })
			if got := gate(); got != test.want {
				t.Fatalf("gate = %t, want %t", got, test.want)
			}
			if test.want {
				now = approval.ExpiresAt
				if gate() {
					t.Fatal("operator test did not expire without restart")
				}
			}
		})
	}
}

func TestOperatorTestCannotBecomeQualificationEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator-test.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"purpose":"operator_test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadQualificationArtifact(path); err == nil {
		t.Fatal("operator test approval was accepted as qualification evidence")
	}
}
