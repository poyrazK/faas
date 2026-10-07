package state

// adr: 435. Portable store facts are not native composition or approval proof.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/scanview"
)

type runtimeScanTestStore interface {
	runtimeProducerInputTestStore
	DeploymentRuntimeScanStore
}

func runtimeScanInputFixture(t *testing.T, inputs DeploymentRuntimeProducerInputs) DeploymentRuntimeScanInput {
	t.Helper()
	hash, err := runtimeadmission.HashArtifactSources(runtimeProducerSources(inputs.deploymentRuntimeArtifactIdentity))
	if err != nil {
		t.Fatal(err)
	}
	facts := runtimescan.Facts{Version: runtimescan.Version, InputHash: inputs.InputHash, SourcesHash: hash}
	reports := []DeploymentRuntimeScanReport{}
	for _, source := range inputs.Artifacts {
		if source.Kind == "base-image" {
			continue
		}
		tree := scanview.Tree{Version: scanview.Version, Digest: strings.Repeat("b", 64), ProjectionDigest: strings.Repeat("c", 64), Entries: 2, Bytes: 7}
		facts.Views = append(facts.Views, runtimescan.View{WorkloadName: source.WorkloadName, SourceTree: tree, ProjectionTree: tree})
		reports = append(reports, DeploymentRuntimeScanReport{WorkloadName: source.WorkloadName, Report: api.ScanResult{
			ImageDigest: "sha256:" + tree.Digest, ArtifactDigest: "sha256:" + hash, ScannerVersion: "0.116.0",
			ScannerDBStatus: "valid", ScannerDBVersion: "v6.0.2", ScannerDBBuiltAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
			Vulnerabilities: []api.Vulnerability{{ID: "CVE-fixture", Severity: "HIGH", Paths: []string{"/app/package"}}}, SeverityCounts: api.SeverityCounts{High: 1}}})
	}
	in, err := NewDeploymentRuntimeScanInput(uuid.NewString(), inputs, facts, reports)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func runtimeScanLifecycle(t *testing.T, s runtimeScanTestStore) {
	t.Helper()
	_, _, app, dep := artifactScanBaseFixture(t, s)
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("missing runtime scan became fresh: %v", err)
	}
	in := runtimeScanInputFixture(t, inputs)
	value, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || evidence.Scan.ID != value.ID || evidence.ExpiresAt.After(value.ExpiresAt) || evidence.ExpiresAt.After(inputs.ExpiresAt) || !evidence.ExpiresAt.After(evidence.CheckedAt) {
		t.Fatalf("runtime facts lost complete binding/deadline: %v", err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("composed facts conferred component/native approval: %v", err)
	}
	retry, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil || !reflect.DeepEqual(value, retry) {
		t.Fatalf("exact retry renewed immutable clocks: %v", err)
	}
	value.Input.Artifacts[0].StorageKey = "mutated"
	value.Input.Facts.Views[0].SourceTree.Digest = "mutated"
	value.Input.Reports[0].Report.Vulnerabilities[0].Paths[0] = "mutated"
	history, err := s.GetCurrentDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || !reflect.DeepEqual(history, retry) {
		t.Fatalf("returned facts aliased immutable storage: %v", err)
	}
	changed := cloneDeploymentRuntimeScan(retry).Input
	changed.Reports[0].Report.ScannerVersion = "different"
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed same-ID report overwrote evidence: %v", err)
	}
	failed, err := NewFailedDeploymentRuntimeScanInput(uuid.NewString(), inputs, "scanner_invalid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), failed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("failed rescan retained historical success: %v", err)
	}
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("historical retry reselected superseded success: %v", err)
	}
	in.ID = uuid.NewString()
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), uuid.NewString(), app.ID, dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runtime facts crossed account scope: %v", err)
	}
	_, otherApp, other := registryVerificationFixture(t, s, false)
	if _, err := s.GetCurrentDeploymentRuntimeScan(t.Context(), otherApp.AccountID, otherApp.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owned absent history differed between stores: %v", err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runtime facts crossed application scope: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.PublishDeploymentRuntimeScan(cancelled, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled publication succeeded: %v", err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
		t.Fatalf("revoked publisher retained fresh facts: %v", err)
	}
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), in); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
		t.Fatalf("revoked publisher renewed facts: %v", err)
	}
}

func runtimeScanReplacement(t *testing.T, s runtimeScanTestStore) {
	t.Helper()
	_, base, app, dep := artifactScanBaseFixture(t, s)
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := runtimeScanInputFixture(t, inputs)
	value, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	root.Input.ID = uuid.NewString() // Same key, digest and size; distinct producer.
	if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), root.Input); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"read", "publish"} {
		if action == "read" {
			_, err = s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
		} else {
			in.ID = uuid.NewString()
			_, err = s.PublishDeploymentRuntimeScan(t.Context(), in)
		}
		if !errors.Is(err, ErrApplicationStandardRuntimeStale) {
			t.Fatalf("replaced producer retained %s: %v", action, err)
		}
	}
	history, err := s.GetCurrentDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || history.ID != value.ID {
		t.Fatalf("refused publication changed selection: %v", err)
	}
	current, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), runtimeScanInputFixture(t, current)); err != nil {
		t.Fatal(err)
	}
	base.Input.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), base.Input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("replaced base retained runtime freshness: %v", err)
	}
}

func TestMemRuntimeScanLifecycle(t *testing.T)   { runtimeScanLifecycle(t, NewMemStore()) }
func TestMemRuntimeScanReplacement(t *testing.T) { runtimeScanReplacement(t, NewMemStore()) }

func TestRuntimeScanRejectsIncompleteOrUnboundFacts(t *testing.T) {
	s := NewMemStore()
	_, _, app, dep := artifactScanBaseFixture(t, s)
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"input hash", "source hash", "version", "missing view", "duplicate view", "missing report", "duplicate report", "report binding", "tree version", "tree projection", "findings", "scanner DB", "caller clock", "failure payload"} {
		t.Run(mode, func(t *testing.T) {
			in := runtimeScanInputFixture(t, inputs)
			switch mode {
			case "input hash":
				in.Facts.InputHash = strings.Repeat("a", 64)
			case "source hash":
				in.Facts.SourcesHash = strings.Repeat("a", 64)
			case "version":
				in.Facts.Version++
			case "missing view":
				in.Facts.Views = nil
			case "duplicate view":
				in.Facts.Views = append(in.Facts.Views, in.Facts.Views[0])
			case "missing report":
				in.Reports = nil
			case "duplicate report":
				in.Reports = append(in.Reports, in.Reports[0])
			case "report binding":
				in.Reports[0].Report.ArtifactDigest = "sha256:" + strings.Repeat("a", 64)
			case "tree version":
				in.Facts.Views[0].SourceTree.Version++
			case "tree projection":
				in.Facts.Views[0].ProjectionTree.Bytes++
			case "findings":
				in.Reports[0].Report.SeverityCounts.High = 0
			case "scanner DB":
				in.Reports[0].Report.ScannerDBBuiltAt = "unknown"
			case "caller clock":
				in.Reports[0].Report.ScannedAt = time.Now().Format(time.RFC3339Nano)
			case "failure payload":
				in.Status, in.Failure = "failed", "customer payload"
			}
			if _, err := s.PublishDeploymentRuntimeScan(t.Context(), in); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("malformed composed evidence published: %v", err)
			}
		})
	}
}

func TestMemRuntimeScanExpiryCannotBeRenewedBySignatureOrRetry(t *testing.T) {
	s := NewMemStore()
	_, _, app, dep := artifactScanBaseFixture(t, s)
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := runtimeScanInputFixture(t, inputs)
	value, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate elapsed storage time without changing the immutable input/report.
	value.ScannedAt = time.Now().Add(-api.ApplicationStandardArtifactScanTTL - time.Second).UTC()
	value.ExpiresAt = value.ScannedAt.Add(api.ApplicationStandardArtifactScanTTL)
	s.deploymentRuntimeScans[value.ID] = value
	root, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	proof, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
	if err != nil {
		t.Fatal(err)
	}
	proof.Input.ID = uuid.NewString()
	if _, err := s.RecordDeploymentRegistryVerification(t.Context(), proof.Input); err != nil {
		t.Fatal(err)
	}
	retry, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil || !retry.ExpiresAt.Equal(value.ExpiresAt) {
		t.Fatal("retry renewed expired scan", err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("renewed publisher revived expired scan", err)
	}
}
