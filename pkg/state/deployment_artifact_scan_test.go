package state

// adr: 430

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type artifactScanTestStore interface {
	registryRootfsTestStore
	DeploymentArtifactScanStore
}

// This fixture tests private store facts, not a native scanner or VM ACK.
func artifactScanFixture(t *testing.T, s artifactScanTestStore, sidecar bool) (DeploymentArtifactScanInput, DeploymentRegistryRootfs, App, Deployment) {
	t.Helper()
	chain, consumed := registryChainFixture(t)
	registry, app, dep := registryVerificationFixtureWithChain(t, s, sidecar, chain)
	parent, err := s.RecordDeploymentRegistryVerification(t.Context(), registry)
	if err != nil {
		t.Fatal(err)
	}
	in := DeploymentRegistryRootfsInput{ID: uuid.NewString(), RegistryVerificationID: parent.ID, RegistryInputHash: parent.InputHash,
		AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, WorkloadName: registry.WorkloadName, Scope: dep.Scope,
		Kind: "full-rootfs", StorageKey: "apps/" + app.Slug + "/" + dep.ID + ".ext4", RootfsPath: "/srv/" + dep.ID + ".ext4",
		ArtifactDigest: imagechain.Digest([]byte("fixture output")), ArtifactBytes: 4096, ContentBytes: 123, Layers: []imagechain.LayerConsumption{consumed}}
	if sidecar {
		in.Kind, in.RootfsPath = "sidecar-layer", ""
	}
	root, err := s.PublishDeploymentRegistryRootfs(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	report := &api.ScanResult{ImageDigest: registry.ImageReference, ArtifactDigest: in.ArtifactDigest,
		ScannerVersion: "0.116.0", ScannerDBStatus: "valid", ScannerDBVersion: "v6.0.2", ScannerDBBuiltAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		SeverityCounts: api.SeverityCounts{Low: 1}, Vulnerabilities: []api.Vulnerability{{ID: "CVE-fixture", Severity: "LOW", Package: "fixture", Paths: []string{"/app/file"}}}}
	return DeploymentArtifactScanInput{ID: uuid.NewString(), RootfsProducerID: root.ID, RootfsInputHash: root.InputHash,
		AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, WorkloadName: in.WorkloadName, Scope: dep.Scope,
		ImageReference: registry.ImageReference, ArtifactDigest: in.ArtifactDigest, ArtifactBytes: in.ArtifactBytes, Status: "complete", ScannerName: "grype", Report: report}, root, app, dep
}

func artifactScanLifecycle(t *testing.T, s artifactScanTestStore) {
	for _, sidecar := range []bool{false, true} {
		t.Run(map[bool]string{false: "main", true: "sidecar"}[sidecar], func(t *testing.T) {
			in, root, _, dep := artifactScanFixture(t, s, sidecar)
			before, err := s.DeploymentByID(t.Context(), dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now().UTC()
			value, err := s.PublishDeploymentArtifactScan(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			if value.ScannedAt.Before(start.Add(-time.Second)) || value.ScannedAt.After(time.Now().Add(time.Second)) || value.ExpiresAt.Sub(value.ScannedAt) != api.ApplicationStandardArtifactScanTTL || value.ExpiresAt.After(root.ExpiresAt) {
				t.Fatal("scan did not use the bounded storage clock")
			}
			retry, err := s.PublishDeploymentArtifactScan(t.Context(), in)
			if err != nil || retry.InputHash != value.InputHash || !retry.ScannedAt.Equal(value.ScannedAt) || !retry.ExpiresAt.Equal(value.ExpiresAt) {
				t.Fatalf("retry renewed scan: %v", err)
			}
			in.Report.Vulnerabilities[0].Paths[0] = "caller mutation"
			value.Input.Report.Vulnerabilities[0].Paths[0] = "returned input mutation"
			value.Result.Vulnerabilities[0].Paths[0] = "returned result mutation"
			got, err := s.GetCurrentDeploymentArtifactScan(t.Context(), in.AccountID, in.AppID, in.DeploymentID, in.WorkloadName)
			if err != nil || got.Result.Vulnerabilities[0].Paths[0] != "/app/file" || got.Input.Report.Vulnerabilities[0].Paths[0] != "/app/file" {
				t.Fatalf("scan buffers aliased: %v", err)
			}
			after, err := s.DeploymentByID(t.Context(), dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if sidecar {
				if string(after.ScanResult) != string(before.ScanResult) || after.ScanStatus != before.ScanStatus || !after.ScannedAt.Equal(before.ScannedAt) {
					t.Fatal("sidecar scan overwrote main report")
				}
			} else {
				var report api.ScanResult
				if json.Unmarshal(after.ScanResult, &report) != nil || report.ArtifactDigest != root.Input.ArtifactDigest || report.ScannedAt != got.Result.ScannedAt || !after.ScannedAt.Equal(got.ScannedAt) {
					t.Fatal("main report was not published atomically with evidence")
				}
			}
			failed := got.Input
			failed.ID, failed.Status, failed.ScannerName, failed.Report, failed.Failure = uuid.NewString(), "failed", "", nil, "scanner_unavailable"
			if _, err := s.PublishDeploymentArtifactScan(t.Context(), failed); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetCurrentDeploymentArtifactScan(t.Context(), in.AccountID, in.AppID, in.DeploymentID, in.WorkloadName)
			if err != nil || current.ID != failed.ID || current.Result.Status != "failed" || current.Result.Error != failed.Failure {
				t.Fatalf("failed scan retained old success: %v", err)
			}
			if _, err := s.PublishDeploymentArtifactScan(t.Context(), got.Input); !errors.Is(err, ErrConflict) {
				t.Fatalf("historical success reactivated: %v", err)
			}
		})
	}
}

func artifactScanRefusals(t *testing.T, s artifactScanTestStore) {
	in, _, _, _ := artifactScanFixture(t, s, false)
	for _, field := range []string{"producer", "producer hash", "account", "org", "scope", "image", "artifact", "bytes", "scanner", "clock", "counts", "null matches", "database stale", "database future", "paths"} {
		t.Run(field, func(t *testing.T) {
			candidate := cloneDeploymentArtifactScan(DeploymentArtifactScan{Input: in}).Input
			candidate.ID = uuid.NewString()
			switch field {
			case "producer":
				candidate.RootfsProducerID = uuid.NewString()
			case "producer hash":
				candidate.RootfsInputHash = strings.Repeat("0", 64)
			case "account":
				candidate.AccountID = uuid.NewString()
			case "org":
				candidate.OrgID = uuid.NewString()
			case "scope":
				candidate.Scope = "production"
			case "image":
				candidate.ImageReference += "-other"
				candidate.Report.ImageDigest = candidate.ImageReference
			case "artifact":
				candidate.ArtifactDigest = imagechain.Digest([]byte("other"))
				candidate.Report.ArtifactDigest = candidate.ArtifactDigest
			case "bytes":
				candidate.ArtifactBytes++
			case "scanner":
				candidate.ScannerName = "other"
			case "clock":
				candidate.Report.ScannedAt = time.Now().UTC().Format(time.RFC3339Nano)
			case "counts":
				candidate.Report.SeverityCounts.Low = 0
			case "null matches":
				candidate.Report.Vulnerabilities = nil
			case "database stale":
				candidate.Report.ScannerDBBuiltAt = time.Now().Add(-api.ApplicationStandardScannerDBMaxAge - time.Minute).UTC().Format(time.RFC3339Nano)
			case "database future":
				candidate.Report.ScannerDBBuiltAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
			case "paths":
				candidate.Report.Vulnerabilities[0].Paths = []string{strings.Repeat("x", api.ApplicationStandardScanMaxPathBytes+1)}
			}
			if _, err := s.PublishDeploymentArtifactScan(t.Context(), candidate); err == nil {
				t.Fatal("mismatched scan accepted")
			}
			if _, err := s.GetCurrentDeploymentArtifactScan(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
				t.Fatalf("refusal published evidence: %v", err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.PublishDeploymentArtifactScan(cancelled, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled publication: %v", err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatalf("refusal made retry unusable: %v", err)
	}
}

func artifactScanCurrentInputs(t *testing.T, s artifactScanTestStore) {
	in, root, app, _ := artifactScanFixture(t, s, false)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	newRoot := root.Input
	newRoot.ID = uuid.NewString()
	if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), newRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("obsolete producer scan exposed: %v", err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("obsolete producer renewed: %v", err)
	}
	current, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, in.DeploymentID, "")
	if err != nil {
		t.Fatal(err)
	}
	in.ID, in.RootfsProducerID, in.RootfsInputHash = uuid.NewString(), current.ID, current.InputHash
	if err := s.UpdateDeploymentStatus(t.Context(), in.DeploymentID, DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatalf("live rescan unavailable: %v", err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, in.DeploymentID, ""); err != nil {
		t.Fatalf("revocation erased historical evidence: %v", err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
		t.Fatalf("retry ignored revoked publisher: %v", err)
	}
}

func TestMemArtifactScanLifecycle(t *testing.T)     { artifactScanLifecycle(t, NewMemStore()) }
func TestMemArtifactScanRefusals(t *testing.T)      { artifactScanRefusals(t, NewMemStore()) }
func TestMemArtifactScanCurrentInputs(t *testing.T) { artifactScanCurrentInputs(t, NewMemStore()) }

func TestMemArtifactScanExpiredRetryRetainsOriginalClock(t *testing.T) {
	s := NewMemStore()
	in, _, _, _ := artifactScanFixture(t, s, false)
	value, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	// Model an already expired immutable row without waiting five minutes.
	value.ScannedAt = time.Now().Add(-2 * api.ApplicationStandardArtifactScanTTL).UTC()
	value.ExpiresAt = value.ScannedAt.Add(api.ApplicationStandardArtifactScanTTL)
	value.Result = artifactScanResult(value.Input, value.ScannedAt)
	s.mu.Lock()
	s.deploymentArtifactScans[in.ID] = value
	s.mu.Unlock()
	retry, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil || !retry.ScannedAt.Equal(value.ScannedAt) || !retry.ExpiresAt.Equal(value.ExpiresAt) || retry.ExpiresAt.After(time.Now()) {
		t.Fatalf("expired retry renewed authority: %v", err)
	}
}

func TestMemArtifactScanCustomerErasure(t *testing.T) {
	s := NewMemStore()
	in, _, app, _ := artifactScanFixture(t, s, false)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted owner scan exposed: %v", err)
	}
	if _, err := s.RestoreApp(t.Context(), app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.deploymentArtifactScans) != 0 || len(s.deploymentArtifactScanCurrent) != 0 {
		t.Fatal("customer erasure left private scan evidence")
	}
}
