package state

// adr: 435. Store and native authority tests do not claim physical-byte ACKs.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type nativeArtifactTestStore interface {
	standardRuntimeCaptureTestStore
	runtimeArtifactCaptureTestStore
	DeploymentRuntimeProducerInputStore
	DeploymentRuntimeScanStore
}

func manageNativeArtifactApp(t *testing.T, s nativeArtifactTestStore, app App) App {
	t.Helper()
	v, err := s.PublishApplicationStandardVersion(t.Context(), ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: "native-artifact", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["8.8.8.0/24"]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, ApplicationStandardReviewRequest{Scope: "application", ScopeID: app.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("native fixture preview blocked: %v %v", p.Blockers, err)
	}
	operation, err := s.ApproveApplicationStandardReview(t.Context(), p.OrgID, app.AccountID, p.ID, p.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "native-artifact-"+uuid.NewString())
	if err != nil || c.OperationID != operation.ID {
		t.Fatalf("fixture claimed a different operation: %v", err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	actual, err := s.AppByID(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return actual
}

func nativeArtifactAttempt(t *testing.T, s nativeArtifactTestStore, app App, dep Deployment) (Instance, runtimeadmission.Binding) {
	t.Helper()
	ins, _ := createRuntimeArtifactCapture(t, s, app, dep)
	return ins, runtimeCaptureTestBinding(t, s, ins)
}

func nativeArtifactReceipt(binding runtimeadmission.Binding, paused bool) runtimeadmission.Receipt {
	return runtimeadmission.Receipt{Binding: binding, NativeInputHash: strings.Repeat("b", 64), Netns: "native-artifact", HostIP: "10.100.0.8", LeaseUID: 20008, Paused: paused, CompletedAtUnixNano: time.Now().UnixNano()}
}

func nativeArtifactGrantLease(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	in, _, app, dep := nativeArtifactFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	scan := publishNativeComposedScan(t, s, app, dep, in.Report)
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || grant.ExpiresAtUnixNano != scan.ExpiresAt.UnixNano() || grant.ExpiresAtUnixNano >= candidate.ExpiresAtUnixNano {
		t.Fatalf("native grant outlived private scan lease: %v", err)
	}
	again, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || again != grant {
		t.Fatalf("retry renewed a native authority clock: %v", err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, nativeArtifactReceipt(grant, false)); err != nil {
		t.Fatal(err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || e.ObservedRevision != 0 {
		t.Fatalf("fresh producer approval fabricated observed adoption: %v", err)
	}
}

func nativeArtifactMissingScan(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	in, base, app, dep := nativeArtifactFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), runtimeArtifactBaseScanInput(in, base)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("unscanned producer authorized a native boot: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func nativeArtifactEnforceFindings(t *testing.T, newStore func(*testing.T) nativeArtifactTestStore) {
	t.Helper()
	for _, severity := range []string{"HIGH", "CRITICAL", "UNKNOWN"} {
		t.Run(severity, func(t *testing.T) {
			s := newStore(t)
			in, _, app, dep := nativeArtifactFixture(t, s, false)
			app = manageNativeArtifactApp(t, s, app)
			in.Report.Vulnerabilities[0].Severity = severity
			in.Report.SeverityCounts = api.SeverityCounts{}
			switch severity {
			case "HIGH":
				in.Report.SeverityCounts.High = 1
			case "CRITICAL":
				in.Report.SeverityCounts.Critical = 1
			case "UNKNOWN":
				in.Report.SeverityCounts.Unknown = 1
			}
			if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
				t.Fatal(err)
			}
			publishNativeComposedScan(t, s, app, dep, in.Report)
			policy := api.AppSecurityPolicyEnforce
			if _, err := s.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy}); err != nil {
				t.Fatal(err)
			}
			ins, candidate := nativeArtifactAttempt(t, s, app, dep)
			if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatalf("enforce admitted %s findings: %v", severity, err)
			}
			assertNativeBootUnpublished(t, s, ins)
		})
	}
}

func nativeArtifactAdvisoryFindings(t *testing.T, newStore func(*testing.T) nativeArtifactTestStore) {
	t.Helper()
	for _, policy := range []api.AppSecurityPolicy{api.AppSecurityPolicyOff, api.AppSecurityPolicyWarn} {
		t.Run(string(policy), func(t *testing.T) {
			s := newStore(t)
			in, _, app, dep := nativeArtifactFixture(t, s, false)
			app = manageNativeArtifactApp(t, s, app)
			in.Report.Vulnerabilities[0].Severity, in.Report.SeverityCounts = "HIGH", api.SeverityCounts{High: 1}
			if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
				t.Fatal(err)
			}
			publishNativeComposedScan(t, s, app, dep, in.Report)
			if _, err := s.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy}); err != nil {
				t.Fatal(err)
			}
			ins, candidate := nativeArtifactAttempt(t, s, app, dep)
			if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate); err != nil {
				t.Fatalf("advisory posture became an enforcement requirement: %v", err)
			}
		})
	}
}

func nativeArtifactMissingSignedProducer(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	signed := true
	if _, err := s.UpdateApp(t.Context(), f.app.ID, UpdateAppParams{SetRequireSigned: true, RequireSigned: &signed}); err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(StateColdBooting), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, runtimeCaptureTestBinding(t, s, ins)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("signed policy used unsigned compatibility inputs: %v", err)
	}
}

func nativeArtifactComposedDatabaseLease(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	app = manageNativeArtifactApp(t, s, app)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	b := runtimeArtifactBaseScanInput(in, base)
	deadline := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Second)
	b.Report.ScannerDBBuiltAt = deadline.Add(-api.ApplicationStandardScannerDBMaxAge).Format(time.RFC3339Nano)
	if _, err := s.PublishBaseImageScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	publishNativeComposedScan(t, s, app, dep, b.Report)
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || grant.ExpiresAtUnixNano != deadline.UnixNano() {
		t.Fatalf("native grant outlived the composed scanner database: %v", err)
	}
	receipt := nativeArtifactReceipt(grant, false)
	time.Sleep(time.Until(deadline) + 20*time.Millisecond)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired composed authority published a native receipt: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func nativeArtifactPausedComposedFixture(t *testing.T, s nativeArtifactTestStore) (runtimeadmission.Receipt, DeploymentRuntimeScanInput, time.Time) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	app = manageNativeArtifactApp(t, s, app)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	b := runtimeArtifactBaseScanInput(in, base)
	deadline := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Second)
	b.Report.ScannerDBBuiltAt = deadline.Add(-api.ApplicationStandardScannerDBMaxAge).Format(time.RFC3339Nano)
	if _, err := s.PublishBaseImageScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	composed := publishNativeComposedScan(t, s, app, dep, b.Report)
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil {
		t.Fatal(err)
	}
	parent := nativeArtifactReceipt(grant, true)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateWarm, parent); err != nil {
		t.Fatal(err)
	}
	return parent, composed.Input, deadline
}

func nativeArtifactPromotionRenewsApproval(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	parent, b, deadline := nativeArtifactPausedComposedFixture(t, s)
	time.Sleep(time.Until(deadline) + 20*time.Millisecond)
	if _, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("paused receipt substituted for fresh artifact approval: %v", err)
	}
	b.ID = uuid.NewString()
	for i := range b.Reports {
		b.Reports[i].Report.ScannerDBBuiltAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	}
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil || p.Binding.ExpiresAtUnixNano <= parent.Binding.ExpiresAtUnixNano {
		t.Fatalf("fresh composed approval could not authorize a new promotion: %v", err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	b.ID, b.Status, b.Reports, b.Facts.Views, b.Failure = uuid.NewString(), "failed", nil, nil, "scanner_unavailable"
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err != nil {
		t.Fatalf("lost acknowledgment recovery issued new authority: %v", err)
	}
}

func nativeArtifactSidecarLease(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	main, _, app, dep := nativeArtifactFixture(t, s, true)
	app = manageNativeArtifactApp(t, s, app)
	composed := nativeComposedScanInput(t, s, app, dep, main.Report)
	deadline := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Second)
	for i := range composed.Reports {
		if composed.Reports[i].WorkloadName == "metrics" {
			composed.Reports[i].Report.ScannerDBBuiltAt = deadline.Add(-api.ApplicationStandardScannerDBMaxAge).Format(time.RFC3339Nano)
		}
	}
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), composed); err != nil {
		t.Fatal(err)
	}
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || grant.ExpiresAtUnixNano != deadline.UnixNano() {
		t.Fatalf("native grant outlived its declared image sidecar: %v", err)
	}
	receipt := nativeArtifactReceipt(grant, false)
	time.Sleep(time.Until(deadline) + 20*time.Millisecond)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired sidecar published a native runtime: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func TestMemNativeArtifactSidecarLease(t *testing.T) { nativeArtifactSidecarLease(t, NewMemStore()) }

func TestMemNativeArtifactGrantLease(t *testing.T)  { nativeArtifactGrantLease(t, NewMemStore()) }
func TestMemNativeArtifactMissingScan(t *testing.T) { nativeArtifactMissingScan(t, NewMemStore()) }
func TestMemNativeArtifactEnforceFindings(t *testing.T) {
	nativeArtifactEnforceFindings(t, func(*testing.T) nativeArtifactTestStore { return NewMemStore() })
}
func TestMemNativeArtifactAdvisoryFindings(t *testing.T) {
	nativeArtifactAdvisoryFindings(t, func(*testing.T) nativeArtifactTestStore { return NewMemStore() })
}
func TestMemNativeArtifactMissingSignedProducer(t *testing.T) {
	nativeArtifactMissingSignedProducer(t, NewMemStore())
}
func TestMemNativeArtifactComposedDatabaseLease(t *testing.T) {
	nativeArtifactComposedDatabaseLease(t, NewMemStore())
}
func TestMemNativeArtifactPromotionRenewsApproval(t *testing.T) {
	nativeArtifactPromotionRenewsApproval(t, NewMemStore())
}
