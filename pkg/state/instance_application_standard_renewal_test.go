package state

// adr: 430. Real private evidence, simulated native receipts; no observed ACKs.

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func publishRenewedStandardScan(t *testing.T, s nativeArtifactTestStore, input DeploymentArtifactScanInput, status string) DeploymentArtifactScanInput {
	t.Helper()
	in := cloneDeploymentArtifactScan(DeploymentArtifactScan{Input: input}).Input
	in.ID = uuid.NewString()
	if status == "failed" {
		in.Status, in.ScannerName, in.Report, in.Failure = "failed", "", nil, "scanner_unavailable"
	} else {
		in.Report.ScannerVersion += "-renewed"
		if status != "LOW" {
			in.Report.Vulnerabilities[0].Severity = status
			in.Report.SeverityCounts = api.SeverityCounts{}
			switch status {
			case "HIGH":
				in.Report.SeverityCounts.High = 1
			case "CRITICAL":
				in.Report.SeverityCounts.Critical = 1
			case "UNKNOWN":
				in.Report.SeverityCounts.Unknown = 1
			}
		}
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	return in
}

func assertStandardCaptureHistory(t *testing.T, s nativeArtifactTestStore, before InstanceApplicationStandardAdmission) {
	t.Helper()
	after, err := s.GetInstanceApplicationStandardAdmission(t.Context(), before.InstanceID)
	if err != nil || !bytes.Equal(before.inputs, after.inputs) || before.InputHash != after.InputHash || before.NativeInputHash != after.NativeInputHash || !before.CapturedAt.Equal(after.CapturedAt) || !reflect.DeepEqual(before.RuntimeArtifacts, after.RuntimeArtifacts) {
		t.Fatalf("renewal replaced immutable capture history: %v", err)
	}
	app, err := s.AppByID(t.Context(), before.AppID)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, before.AppID)
	if err != nil || e.ObservedRevision != 0 {
		t.Fatalf("storage receipt fabricated consumer observation: %v", err)
	}
}

func assertRenewalCallerAdmission(t *testing.T, s nativeArtifactTestStore, ins Instance, app App, dep Deployment) {
	t.Helper()
	account, err := s.AccountByID(t.Context(), app.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckInstanceApplicationStandardAdmission(t.Context(), s, ins.ID, app, account, current); err != nil {
		t.Fatalf("renewal refused unchanged scheduler inputs: %v", err)
	}
}

func standardArtifactRenewalBeforeBoot(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	in, _, app, dep := artifactScanFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	in = publishRenewedStandardScan(t, s, in, "LOW")
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	in = publishRenewedStandardScan(t, s, in, "LOW")
	assertRenewalCallerAdmission(t, s, ins, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil {
		t.Fatalf("fresh approval could not authorize stable capture: %v", err)
	}
	publishRenewedStandardScan(t, s, in, "LOW")
	retry, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || retry != grant {
		t.Fatalf("scan refresh renewed or invalidated an existing grant: %v", err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, nativeArtifactReceipt(grant, false)); err != nil {
		t.Fatalf("fresh approval could not publish original grant: %v", err)
	}
	assertStandardCaptureHistory(t, s, capture)
}

func standardArtifactRenewalPromotion(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	in, _, app, dep := artifactScanFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	in = publishRenewedStandardScan(t, s, in, "LOW")
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil {
		t.Fatal(err)
	}
	parent := nativeArtifactReceipt(grant, true)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateWarm, parent); err != nil {
		t.Fatal(err)
	}
	in = publishRenewedStandardScan(t, s, in, "LOW")
	loaded, err := s.GetInstanceApplicationStandardWarmParent(t.Context(), ins.ID)
	if err != nil || loaded != parent {
		t.Fatalf("main scan renewal invalidated paused history: %v", err)
	}
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	publishRenewedStandardScan(t, s, in, "failed")
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("failed current scan reused fresh promotion grant: %v", err)
	}
	current, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || current.State != string(StateWarm) || current.Netns != parent.Netns {
		t.Fatalf("refused promotion changed residency: %v", err)
	}
	publishRenewedStandardScan(t, s, in, "LOW")
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err != nil {
		t.Fatalf("fresh main scan could not recover uncommitted promotion: %v", err)
	}
	publishRenewedStandardScan(t, s, in, "failed")
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err != nil {
		t.Fatalf("exact committed ACK retry required new approval: %v", err)
	}
	if err := s.MarkInstanceMigrating(t.Context(), ins.ID, ins.NodeID, uuid.NewString()); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("failed scan allowed a new managed lifecycle write: %v", err)
	}
	current, err = s.InstanceByID(t.Context(), ins.ID)
	if err != nil || current.State != string(StateRunning) {
		t.Fatalf("refused migration changed residency: %v", err)
	}
	assertStandardCaptureHistory(t, s, capture)
}

func standardArtifactRenewalRefusesUnsafe(t *testing.T, newStore func(*testing.T) nativeArtifactTestStore) {
	t.Helper()
	for _, status := range []string{"failed", "HIGH", "CRITICAL", "UNKNOWN"} {
		t.Run(status, func(t *testing.T) {
			s := newStore(t)
			in, _, app, dep := artifactScanFixture(t, s, false)
			app = manageNativeArtifactApp(t, s, app)
			policy := api.AppSecurityPolicyEnforce
			app, err := s.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy})
			if err != nil {
				t.Fatal(err)
			}
			in = publishRenewedStandardScan(t, s, in, "LOW")
			ins, candidate := nativeArtifactAttempt(t, s, app, dep)
			grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
			if err != nil {
				t.Fatal(err)
			}
			publishRenewedStandardScan(t, s, in, status)
			if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatalf("%s current evidence authorized retry: %v", status, err)
			}
			if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, nativeArtifactReceipt(grant, false)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatalf("%s evidence published prior grant: %v", status, err)
			}
			assertNativeBootUnpublished(t, s, ins)
		})
	}
}

func TestMemApplicationStandardArtifactRenewalBeforeBoot(t *testing.T) {
	standardArtifactRenewalBeforeBoot(t, NewMemStore())
}
func TestMemApplicationStandardArtifactRenewalPromotion(t *testing.T) {
	standardArtifactRenewalPromotion(t, NewMemStore())
}
func TestMemApplicationStandardArtifactRenewalRefusesUnsafe(t *testing.T) {
	standardArtifactRenewalRefusesUnsafe(t, func(*testing.T) nativeArtifactTestStore { return NewMemStore() })
}

func TestApplicationStandardStableInputProjection(t *testing.T) {
	s := NewMemStore()
	_, _, app, dep := artifactScanFixture(t, s, false)
	_, capture := createRuntimeArtifactCapture(t, s, app, dep)
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(capture.inputs, &legacy); err != nil {
		t.Fatal(err)
	}
	var artifact map[string]json.RawMessage
	if err := json.Unmarshal(legacy["artifact"], &artifact); err != nil {
		t.Fatal(err)
	}
	artifact["scan_status"], artifact["scan_result_hash"] = json.RawMessage(`"complete"`), json.RawMessage(`"historical-report"`)
	legacy["artifact"], _ = json.Marshal(artifact)
	before, _ := json.Marshal(legacy)
	if matched, err := standardNativeRuntimeInputsMatch(before, capture.inputs); err != nil || !matched {
		t.Fatalf("valid historical producer capture lost handoff: %v", err)
	}
	for _, field := range []string{"runtime_artifacts", "account_plan", "future_input"} {
		t.Run(field, func(t *testing.T) {
			var changed map[string]json.RawMessage
			json.Unmarshal(before, &changed)
			switch field {
			case "runtime_artifacts":
				changed[field] = json.RawMessage(`null`)
			case "account_plan":
				changed[field] = json.RawMessage(`"scale"`)
			case "future_input":
				changed[field] = json.RawMessage(`9007199254740993`)
			}
			raw, _ := json.Marshal(changed)
			if matched, err := standardNativeRuntimeInputsMatch(raw, capture.inputs); err == nil && matched {
				t.Fatalf("native comparison discarded %s", field)
			}
		})
	}
	delete(legacy, "runtime_artifacts")
	before, _ = json.Marshal(legacy)
	artifact["scan_result_hash"] = json.RawMessage(`"new-report"`)
	legacy["artifact"], _ = json.Marshal(artifact)
	after, _ := json.Marshal(legacy)
	if matched, err := standardNativeRuntimeInputsMatch(before, after); err != nil || matched {
		t.Fatalf("compatibility inputs without private lineage ignored scan changes: %v", err)
	}
	if !bytes.Contains(before, []byte("historical-report")) {
		t.Fatal("projection mutated historical snapshot bytes")
	}
	legacy["future_input"] = json.RawMessage(`9007199254740992`)
	before, _ = json.Marshal(legacy)
	legacy["future_input"] = json.RawMessage(`9007199254740993`)
	after, _ = json.Marshal(legacy)
	if matched, err := standardNativeRuntimeInputsMatch(before, after); err != nil || matched {
		t.Fatalf("numeric precision discarded an unknown input change: %v", err)
	}
}
