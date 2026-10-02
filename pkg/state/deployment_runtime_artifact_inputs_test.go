package state

// adr: 430. Real producer/approval stores; no native byte or overlay-scan claims.

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type runtimeArtifactInputTestStore interface {
	artifactBaseEvidenceTestStore
	DeploymentRuntimeArtifactInputStore
}

func runtimeArtifactInputRenewal(t *testing.T, s runtimeArtifactInputTestStore) {
	t.Helper()
	in, root, app, dep := artifactScanFixture(t, s, false)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || len(before.Artifacts) != 1 || before.Artifacts[0] != runtimeArtifactFromRootfs(root) || len(before.InputHash) != 64 || before.CheckedAt.IsZero() || !sameStandardUUID(before.AppID, app.ID) {
		t.Fatalf("producer input set lost identity or complete artifact bytes: %v", err)
	}
	old, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
	if err != nil {
		t.Fatal(err)
	}
	old.Input.ID = uuid.NewString()
	approval, err := s.RecordDeploymentRegistryVerification(t.Context(), old.Input)
	if err != nil {
		t.Fatal(err)
	}
	in.ID, in.RegistryVerificationID, in.RegistryInputHash = uuid.NewString(), approval.ID, approval.InputHash
	scan, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || after.InputHash != before.InputHash || !reflect.DeepEqual(after.Artifacts, before.Artifacts) || !after.ExpiresAt.Equal(scan.ExpiresAt) || after.CheckedAt.Before(scan.ScannedAt) {
		t.Fatalf("approval/scan renewal changed producer identity or lost its fresh lease: %v", err)
	}
	after.Artifacts[0].StorageKey = "returned-mutation"
	again, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || again.InputHash != before.InputHash || again.Artifacts[0].StorageKey == "returned-mutation" {
		t.Fatalf("returned inputs aliased producer data: %v", err)
	}
	replacement := root.Input
	replacement.ID = uuid.NewString()
	newRoot, err := s.PublishDeploymentRegistryRootfs(t.Context(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("new producer reused an old scan: %v", err)
	}
	in.ID, in.RootfsProducerID, in.RootfsInputHash = uuid.NewString(), newRoot.ID, newRoot.InputHash
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	changed, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || changed.InputHash == before.InputHash || changed.Artifacts[0].ProducerID != newRoot.ID {
		t.Fatalf("replacement producer retained captured identity: %v", err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), uuid.NewString(), app.ID, dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("producer input set crossed account scope: %v", err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
		t.Fatalf("revoked publisher retained a fresh input lease: %v", err)
	}
}

func runtimeArtifactBaseScanInput(in DeploymentArtifactScanInput, base BaseImageProducer) BaseImageScanInput {
	return BaseImageScanInput{ID: uuid.NewString(), BaseProducerID: base.ID, BaseInputHash: base.InputHash, Artifact: base.Input.Artifact, SourceReference: base.Input.SourceReference, Status: "complete", ScannerName: "grype", Report: &api.ScanResult{ImageDigest: base.Input.SourceReference, ArtifactDigest: base.Input.Artifact.Digest, ScannerVersion: in.Report.ScannerVersion, ScannerDBStatus: in.Report.ScannerDBStatus, ScannerDBVersion: in.Report.ScannerDBVersion, ScannerDBBuiltAt: in.Report.ScannerDBBuiltAt, Vulnerabilities: []api.Vulnerability{}}}
}

func runtimeArtifactInputsTwoDrives(t *testing.T, s runtimeArtifactInputTestStore) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	main, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("missing shared base supplied complete inputs: %v", err)
	}
	baseScan := runtimeArtifactBaseScanInput(in, base)
	if _, err := s.PublishBaseImageScan(t.Context(), baseScan); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || len(before.Artifacts) != 2 || before.Artifacts[0].Kind != "base-image" || before.Artifacts[0].ProducerID != base.ID || before.Artifacts[1].BaseProducerID != base.ID || before.Artifacts[1].Bytes != in.ArtifactBytes || !before.ExpiresAt.Equal(main.ExpiresAt) {
		t.Fatalf("two-drive set lost separate producer/byte identities or minimum lease: %v", err)
	}
	baseScan.ID = uuid.NewString()
	if _, err := s.PublishBaseImageScan(t.Context(), baseScan); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || before.InputHash != after.InputHash || !after.ExpiresAt.Equal(main.ExpiresAt) {
		t.Fatalf("base scan renewal changed producer identity or extended another lease: %v", err)
	}
	replacement := base.Input
	replacement.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("changed shared base retained a captured input lease: %v", err)
	}
}

func runtimeArtifactInputsSidecars(t *testing.T, s runtimeArtifactInputTestStore) {
	t.Helper()
	sideScan, sideRoot, app, dep := artifactScanFixture(t, s, true)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), sideScan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("sidecar alone supplied a runtime input set: %v", err)
	}
	origin, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, sideRoot.Input.RegistryVerificationID)
	if err != nil {
		t.Fatal(err)
	}
	main := cloneRegistryVerificationInput(origin.Input)
	main.ID, main.WorkloadName, main.ImageReference = uuid.NewString(), "", dep.ImageDigest
	repo := strings.TrimSuffix(dep.ImageDigest, ":latest")
	main.SourceReference, main.SelectedReference = repo+"@"+main.Proof.SubjectDigest, repo+"@"+main.SelectedDigest
	proof, err := s.RecordDeploymentRegistryVerification(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	rootInput := sideRoot.Input
	rootInput.ID, rootInput.WorkloadName, rootInput.Kind, rootInput.RootfsPath = uuid.NewString(), "", "full-rootfs", "/srv/main.ext4"
	rootInput.RegistryVerificationID, rootInput.RegistryInputHash = proof.ID, proof.InputHash
	rootInput.StorageKey, rootInput.ArtifactDigest, rootInput.ArtifactBytes = "apps/main.ext4", imagechain.Digest([]byte("main output")), 8192
	root, err := s.PublishDeploymentRegistryRootfs(t.Context(), rootInput)
	if err != nil {
		t.Fatal(err)
	}
	mainScan := sideScan
	mainScan.ID, mainScan.WorkloadName, mainScan.ImageReference = uuid.NewString(), "", main.ImageReference
	mainScan.RootfsProducerID, mainScan.RootfsInputHash, mainScan.ArtifactDigest, mainScan.ArtifactBytes = root.ID, root.InputHash, rootInput.ArtifactDigest, rootInput.ArtifactBytes
	report := cloneArtifactScanResult(*sideScan.Report)
	report.ImageDigest, report.ArtifactDigest = main.ImageReference, rootInput.ArtifactDigest
	mainScan.Report = &report
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), mainScan); err != nil {
		t.Fatal(err)
	}
	inputs, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || len(inputs.Artifacts) != 2 || inputs.Artifacts[0].ProducerID != root.ID || inputs.Artifacts[1].ProducerID != sideRoot.ID || inputs.Artifacts[1].WorkloadName != "metrics" {
		t.Fatalf("main/sidecar set lost exact membership: %v", err)
	}
	evidence, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(evidence.Artifacts)
	reordered, err := deploymentRuntimeArtifactInputs(evidence)
	if err != nil || inputs.InputHash != reordered.InputHash {
		t.Fatalf("input iteration order changed stable identity: %v", err)
	}
}

func TestMemRuntimeArtifactInputsRenewal(t *testing.T) { runtimeArtifactInputRenewal(t, NewMemStore()) }
func TestMemRuntimeArtifactInputsTwoDrives(t *testing.T) {
	runtimeArtifactInputsTwoDrives(t, NewMemStore())
}
func TestMemRuntimeArtifactInputsSidecars(t *testing.T) {
	runtimeArtifactInputsSidecars(t, NewMemStore())
}

func runtimeArtifactInputDatabaseDeadline(t *testing.T, s runtimeArtifactInputTestStore) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	main, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	baseScan := runtimeArtifactBaseScanInput(in, base)
	deadline := time.Now().UTC().Add(2 * time.Second)
	baseScan.Report.ScannerDBBuiltAt = deadline.Add(-api.ApplicationStandardScannerDBMaxAge).Format(time.RFC3339Nano)
	if _, err := s.PublishBaseImageScan(t.Context(), baseScan); err != nil {
		t.Fatal(err)
	}
	inputs, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || !inputs.ExpiresAt.Equal(deadline) || !inputs.ExpiresAt.Before(main.ExpiresAt) {
		t.Fatalf("input lease outlived shared-base scanner database authority: %v", err)
	}
	time.Sleep(time.Until(deadline) + 20*time.Millisecond)
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("aged-out database retained a native input lease: %v", err)
	}
	history, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || history.ID != main.ID || !history.ExpiresAt.Equal(main.ExpiresAt) {
		t.Fatalf("input refusal changed immutable component history: %v", err)
	}
}

func TestMemRuntimeArtifactInputsDatabaseDeadline(t *testing.T) {
	runtimeArtifactInputDatabaseDeadline(t, NewMemStore())
}

func TestRuntimeArtifactInputsRejectIncompleteMembership(t *testing.T) {
	s := NewMemStore()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), runtimeArtifactBaseScanInput(in, base)); err != nil {
		t.Fatal(err)
	}
	evidence, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*DeploymentArtifactScanEvidence)
	}{
		{"missing root", func(e *DeploymentArtifactScanEvidence) { e.Artifacts = e.Artifacts[1:] }},
		{"missing base", func(e *DeploymentArtifactScanEvidence) { e.Bases = nil; e.Artifacts = e.Artifacts[:1] }},
		{"unknown kind", func(e *DeploymentArtifactScanEvidence) { e.Artifacts[0].Kind = "unknown" }},
		{"wrong producer", func(e *DeploymentArtifactScanEvidence) { e.Artifacts[0].ProducerID = uuid.NewString() }},
		{"wrong hash", func(e *DeploymentArtifactScanEvidence) { e.Artifacts[0].ProducerHash = strings.Repeat("f", 64) }},
		{"wrong bytes", func(e *DeploymentArtifactScanEvidence) { e.Artifacts[0].Bytes++ }},
		{"wrong base association", func(e *DeploymentArtifactScanEvidence) { e.Artifacts[0].BaseProducerID = uuid.NewString() }},
		{"wrong base hash", func(e *DeploymentArtifactScanEvidence) { e.Artifacts[0].BaseInputHash = strings.Repeat("f", 64) }},
		{"same two-drive key", func(e *DeploymentArtifactScanEvidence) { e.Artifacts[0].StorageKey = e.Artifacts[1].StorageKey }},
		{"expired at capture", func(e *DeploymentArtifactScanEvidence) { e.ExpiresAt = e.CheckedAt }},
		{"duplicate roots", func(e *DeploymentArtifactScanEvidence) {
			e.Artifacts = append(e.Artifacts, e.Artifacts[0])
			e.Components = append(e.Components, e.Components[0])
		}},
		{"duplicate base", func(e *DeploymentArtifactScanEvidence) {
			e.Artifacts = append(e.Artifacts, e.Artifacts[1])
			e.Bases = append(e.Bases, e.Bases[0])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := evidence
			copy.Artifacts = slices.Clone(evidence.Artifacts)
			copy.Components = slices.Clone(evidence.Components)
			copy.Bases = slices.Clone(evidence.Bases)
			tc.change(&copy)
			if _, err := deploymentRuntimeArtifactInputs(copy); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatalf("incomplete producer inputs were captured: %v", err)
			}
		})
	}
}
