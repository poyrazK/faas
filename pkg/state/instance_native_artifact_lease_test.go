package state

// adr: 431. Store and native authority tests do not claim physical-byte ACKs.

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
	in, _, app, dep := artifactScanFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	scan, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
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
	_, _, app, dep := artifactScanFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
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
			in, _, app, dep := artifactScanFixture(t, s, false)
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
			in, _, app, dep := artifactScanFixture(t, s, false)
			app = manageNativeArtifactApp(t, s, app)
			in.Report.Vulnerabilities[0].Severity, in.Report.SeverityCounts = "HIGH", api.SeverityCounts{High: 1}
			if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
				t.Fatal(err)
			}
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

func nativeArtifactBaseDatabaseLease(t *testing.T, s nativeArtifactTestStore) {
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
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || grant.ExpiresAtUnixNano != deadline.UnixNano() {
		t.Fatalf("native grant outlived the base scanner database: %v", err)
	}
	receipt := nativeArtifactReceipt(grant, false)
	time.Sleep(time.Until(deadline) + 20*time.Millisecond)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired base authority published a native receipt: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func nativeArtifactPausedBaseFixture(t *testing.T, s nativeArtifactTestStore) (runtimeadmission.Receipt, BaseImageScanInput, time.Time) {
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
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil {
		t.Fatal(err)
	}
	parent := nativeArtifactReceipt(grant, true)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateWarm, parent); err != nil {
		t.Fatal(err)
	}
	return parent, b, deadline
}

func nativeArtifactPromotionRenewsApproval(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	parent, b, deadline := nativeArtifactPausedBaseFixture(t, s)
	time.Sleep(time.Until(deadline) + 20*time.Millisecond)
	if _, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("paused receipt substituted for fresh artifact approval: %v", err)
	}
	b.ID, b.Report.ScannerDBBuiltAt = uuid.NewString(), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := s.PublishBaseImageScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil || p.Binding.ExpiresAtUnixNano <= parent.Binding.ExpiresAtUnixNano {
		t.Fatalf("fresh base approval could not authorize a new promotion: %v", err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	b.ID, b.Status, b.ScannerName, b.Report, b.Failure = uuid.NewString(), "failed", "", nil, "scanner_unavailable"
	if _, err := s.PublishBaseImageScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err != nil {
		t.Fatalf("lost acknowledgment recovery issued new authority: %v", err)
	}
}

func nativeArtifactSidecarLease(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	side, root, app, dep := artifactScanFixture(t, s, true)
	mainRoot := completeRuntimeArtifactSidecar(t, s, root, app, dep)
	app = manageNativeArtifactApp(t, s, app)
	main := cloneDeploymentArtifactScan(DeploymentArtifactScan{Input: side}).Input
	main.ID, main.WorkloadName, main.RootfsProducerID, main.RootfsInputHash = uuid.NewString(), "", mainRoot.ID, mainRoot.InputHash
	main.ImageReference, main.ArtifactDigest, main.ArtifactBytes = dep.ImageDigest, mainRoot.Input.ArtifactDigest, mainRoot.Input.ArtifactBytes
	main.Report.ImageDigest, main.Report.ArtifactDigest = main.ImageReference, main.ArtifactDigest
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), main); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Second)
	side.Report.ScannerDBBuiltAt = deadline.Add(-api.ApplicationStandardScannerDBMaxAge).Format(time.RFC3339Nano)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), side); err != nil {
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
func TestMemNativeArtifactBaseDatabaseLease(t *testing.T) {
	nativeArtifactBaseDatabaseLease(t, NewMemStore())
}
func TestMemNativeArtifactPromotionRenewsApproval(t *testing.T) {
	nativeArtifactPromotionRenewsApproval(t, NewMemStore())
}
