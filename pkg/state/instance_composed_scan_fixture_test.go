package state

// adr: 435. These are explicit composed-report fixtures, not Grype/KVM proof.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func nativeArtifactFixture(t *testing.T, s nativeArtifactTestStore, sidecar bool) (DeploymentArtifactScanInput, BaseImageProducer, App, Deployment) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixtureWithSidecar(t, s, sidecar)
	in.Report.Vulnerabilities = []api.Vulnerability{{ID: "CVE-fixture", Severity: "LOW", Paths: []string{"/app/package"}}}
	in.Report.SeverityCounts = api.SeverityCounts{Low: 1}
	return in, base, app, dep
}

func nativeComposedScanInput(t *testing.T, s nativeArtifactTestStore, app App, dep Deployment, report *api.ScanResult) DeploymentRuntimeScanInput {
	t.Helper()
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := runtimeScanInputFixture(t, inputs)
	for i := range in.Reports {
		value := cloneArtifactScanResult(*report)
		value.ImageDigest, value.ArtifactDigest = in.Reports[i].Report.ImageDigest, in.Reports[i].Report.ArtifactDigest
		in.Reports[i].Report = value
	}
	return in
}

func publishNativeComposedScan(t *testing.T, s nativeArtifactTestStore, app App, dep Deployment, report *api.ScanResult) DeploymentRuntimeScan {
	t.Helper()
	value, err := s.PublishDeploymentRuntimeScan(t.Context(), nativeComposedScanInput(t, s, app, dep, report))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
