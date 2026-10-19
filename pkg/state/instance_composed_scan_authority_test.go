package state

// adr: 435. Real stores with simulated reports/receipts, not native acceptance.

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func nativeComposedPolicyAuthority(t *testing.T, newStore func(*testing.T) nativeArtifactTestStore) {
	t.Helper()
	for _, mode := range []string{"clean composed with component HIGH", "clean composed with base HIGH", "main HIGH with clean components", "sidecar UNKNOWN with clean components", "failed composed after grant"} {
		t.Run(mode, func(t *testing.T) {
			s := newStore(t)
			in, base, app, dep := nativeArtifactFixture(t, s, true)
			app = manageNativeArtifactApp(t, s, app)
			clean := cloneArtifactScanResult(*in.Report)
			if mode == "clean composed with component HIGH" {
				in.Report.Vulnerabilities[0].Severity = "HIGH"
				in.Report.SeverityCounts = api.SeverityCounts{High: 1}
			}
			if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
				t.Fatal(err)
			}
			b := runtimeArtifactBaseScanInput(in, base)
			if mode == "clean composed with base HIGH" {
				b.Report.Vulnerabilities = []api.Vulnerability{{ID: "CVE-hidden-base-fixture", Severity: "HIGH"}}
				b.Report.SeverityCounts = api.SeverityCounts{High: 1}
			}
			if _, err := s.PublishBaseImageScan(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			composed := nativeComposedScanInput(t, s, app, dep, &clean)
			for i := range composed.Reports {
				r := &composed.Reports[i]
				if mode == "main HIGH with clean components" && r.WorkloadName == "" {
					r.Report.Vulnerabilities[0].Severity = "HIGH"
					r.Report.SeverityCounts = api.SeverityCounts{High: 1}
				}
				if mode == "sidecar UNKNOWN with clean components" && r.WorkloadName == "metrics" {
					r.Report.Vulnerabilities[0].Severity = "UNKNOWN"
					r.Report.SeverityCounts = api.SeverityCounts{Unknown: 1}
				}
			}
			if _, err := s.PublishDeploymentRuntimeScan(t.Context(), composed); err != nil {
				t.Fatal(err)
			}
			policy := api.AppSecurityPolicyEnforce
			if _, err := s.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy}); err != nil {
				t.Fatal(err)
			}
			ins, candidate := nativeArtifactAttempt(t, s, app, dep)
			grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
			if mode == "main HIGH with clean components" || mode == "sidecar UNKNOWN with clean components" {
				if !errors.Is(err, ErrApplicationStandardRuntimeStale) {
					t.Fatal("composed unsafe view bypassed enforcement", err)
				}
				assertNativeBootUnpublished(t, s, ins)
				return
			}
			if err != nil {
				t.Fatal("component findings overrode current composed facts", err)
			}
			if mode == "failed composed after grant" {
				composed.ID, composed.Status, composed.Failure = uuid.NewString(), "failed", "scanner_invalid"
				composed.Reports, composed.Facts.Views = nil, nil
				if _, err := s.PublishDeploymentRuntimeScan(t.Context(), composed); err != nil {
					t.Fatal(err)
				}
				if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, nativeArtifactReceipt(grant, false)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
					t.Fatal("old grant survived failed composed scan", err)
				}
				assertNativeBootUnpublished(t, s, ins)
				return
			}
			if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, nativeArtifactReceipt(grant, false)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMemNativeComposedPolicyAuthority(t *testing.T) {
	nativeComposedPolicyAuthority(t, func(*testing.T) nativeArtifactTestStore { return NewMemStore() })
}
