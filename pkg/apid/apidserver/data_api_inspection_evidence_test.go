package apidserver

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/dataapiinspect"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDataAPIInspectionEvidenceFailsClosed(t *testing.T) {
	now := time.Now().UTC()
	nonce, trust := strings.Repeat("a", 64), strings.Repeat("b", 64)
	target, err := dataapiinspect.TargetDigest(nonce, "db.example", 5432, "app", "login")
	if err != nil {
		t.Fatal(err)
	}
	expected := managedpostgres.DataAPIBindingExpectation{AppID: "app", BindingID: "binding", DatabaseID: "database", CredentialGeneration: 1, Target: managedpostgres.DataAPIBindingTarget{Host: "db.example", Port: 5432, Database: "app", Login: "login", TrustBundleSHA256: trust}}
	observation := dataapiinspect.Observation{Version: 1, Role: dataapiinspect.Role{Name: "login", Database: "app"}, Relation: dataapiinspect.Relation{Name: "notes", Kind: "r"}, Columns: []dataapiinspect.Column{{Name: "id"}}, Policies: []dataapiinspect.Policy{}, Triggers: []dataapiinspect.Trigger{}, TargetDigest: target, TrustBundleSHA256: trust}
	output, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(output)
	zero := 0
	original := state.DataAPIInspectionRecord{TaskID: "task", AppID: "app", DeploymentID: "deployment", DatabaseID: "database", BindingID: "binding", Command: []string{dataapiinspectioncommand.Command, "notes", nonce}, Status: state.AppTaskSucceeded, Stdout: string(output), ExitCode: &zero, CredentialGeneration: 1, TargetDigest: target, TrustDigest: trust, Current: true, FinishedAt: &now, ProtocolVersion: apptaskproto.DataAPIInspectionVersion, StdoutSHA256: hash[:]}
	for _, tc := range []struct {
		name, wantStatus, wantReason string
		change                       func(*state.DataAPIInspectionRecord, *managedpostgres.DataAPIBindingExpectation, *state.DataAPIInspectionRecord)
	}{
		{"verified", "verified", "provider_target_and_trust_matched", nil},
		{"old protocol", "unknown", "execution_evidence_unavailable", func(r *state.DataAPIInspectionRecord, _ *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			r.ProtocolVersion = 5
		}},
		{"missing provenance", "unknown", "execution_evidence_unavailable", func(r *state.DataAPIInspectionRecord, _ *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			r.StdoutSHA256 = nil
		}},
		{"tampered output", "unknown", "execution_evidence_unavailable", func(r *state.DataAPIInspectionRecord, _ *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			r.Stdout += " "
		}},
		{"truncated", "unknown", "inspection_failed_or_stale", func(r *state.DataAPIInspectionRecord, _ *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			r.OutputTruncated = true
		}},
		{"changed binding", "unknown", "binding_or_deployment_changed", func(r *state.DataAPIInspectionRecord, _ *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			r.Current = false
		}},
		{"changed during provider lookup", "unknown", "binding_or_deployment_changed", func(_ *state.DataAPIInspectionRecord, _ *managedpostgres.DataAPIBindingExpectation, a *state.DataAPIInspectionRecord) {
			a.Current = false
		}},
		{"other binding", "unknown", "provider_expectation_unavailable", func(_ *state.DataAPIInspectionRecord, e *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			e.BindingID = "other"
		}},
		{"rotated credential", "unknown", "provider_expectation_unavailable", func(_ *state.DataAPIInspectionRecord, e *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			e.CredentialGeneration++
		}},
		{"changed provider target", "unknown", "provider_expectation_changed", func(_ *state.DataAPIInspectionRecord, e *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			e.Target.Host = "other.example"
		}},
		{"changed provider trust", "unknown", "provider_expectation_changed", func(_ *state.DataAPIInspectionRecord, e *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			e.Target.TrustBundleSHA256 = strings.Repeat("c", 64)
		}},
		{"replayed nonce", "unknown", "provider_expectation_changed", func(r *state.DataAPIInspectionRecord, _ *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			r.Command = []string{dataapiinspectioncommand.Command, "notes", strings.Repeat("d", 64)}
		}},
		{"changed task", "unknown", "binding_or_deployment_changed", func(_ *state.DataAPIInspectionRecord, _ *managedpostgres.DataAPIBindingExpectation, a *state.DataAPIInspectionRecord) {
			a.TaskID = "different-task"
		}},
		{"stale", "unknown", "inspection_failed_or_stale", func(r *state.DataAPIInspectionRecord, _ *managedpostgres.DataAPIBindingExpectation, _ *state.DataAPIInspectionRecord) {
			old := now.Add(-6 * time.Minute)
			r.FinishedAt = &old
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, e, after := original, expected, original
			if tc.change != nil {
				tc.change(&r, &e, &after)
			}
			got, raw := evaluateDataAPIInspection(r, &e, &after, now)
			if got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Fatalf("got %+v", got)
			}
			if got.Status == "verified" && (len(raw) == 0 || strings.Contains(string(raw), "target_digest") || strings.Contains(string(raw), "trust_bundle_sha256")) {
				t.Fatalf("unsafe public output %s", raw)
			}
			if got.Status == "unknown" && len(raw) != 0 {
				t.Fatal("unknown evidence returned observation")
			}
		})
	}
	// Authenticated observation of a different target is a mismatch only if
	// the separately pinned provider CA trust still matches.
	observation.TargetDigest = strings.Repeat("c", 64)
	output, _ = json.Marshal(observation)
	hash = sha256.Sum256(output)
	mismatch := original
	mismatch.Stdout = string(output)
	mismatch.StdoutSHA256 = hash[:]
	identity, _ := evaluateDataAPIInspection(mismatch, &expected, &mismatch, now)
	if identity.Status != "mismatch" || identity.Reason != "observed_target_differs" {
		t.Fatalf("mismatch: %+v", identity)
	}
	observation.TrustBundleSHA256 = strings.Repeat("d", 64)
	output, _ = json.Marshal(observation)
	hash = sha256.Sum256(output)
	mismatch.Stdout = string(output)
	mismatch.StdoutSHA256 = hash[:]
	identity, _ = evaluateDataAPIInspection(mismatch, &expected, &mismatch, now)
	if identity.Status != "unknown" || identity.Reason != "inspection_trust_mismatch" {
		t.Fatalf("untrusted observation: %+v", identity)
	}
}

func TestGenericAppTaskResponseHidesInspectionOutput(t *testing.T) {
	row := state.AppTask{Command: []string{dataapiinspectioncommand.Command, "notes", strings.Repeat("a", 64)}, StdoutTail: "private catalog metadata", StderrTail: "private diagnostic"}
	response := appTaskResponse(row)
	if response.StdoutTail != "" || response.StderrTail != "" {
		t.Fatal("generic task response exposed inspection output")
	}
	row.Command = []string{"echo", "hello"}
	response = appTaskResponse(row)
	if response.StdoutTail != row.StdoutTail || response.StderrTail != row.StderrTail {
		t.Fatal("generic task output changed for an ordinary command")
	}
}
