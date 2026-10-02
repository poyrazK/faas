package state

// adr: 430. Private store evidence, not native scanner/VM acceptance.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type artifactEvidenceTestStore interface {
	artifactScanTestStore
	DeploymentArtifactScanEvidenceStore
}

func artifactEvidenceLifecycle(t *testing.T, s artifactEvidenceTestStore) {
	for _, sidecar := range []bool{false, true} {
		t.Run(map[bool]string{false: "main", true: "sidecar"}[sidecar], func(t *testing.T) {
			in, root, app, dep := artifactScanFixture(t, s, sidecar)
			if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatalf("producer without complete scans gained fresh evidence: %v", err)
			}
			origin, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
			if err != nil {
				t.Fatal(err)
			}
			origin.Input.ID = uuid.NewString()
			approved, err := s.RecordDeploymentRegistryVerification(t.Context(), origin.Input)
			if err != nil {
				t.Fatal(err)
			}
			in.RegistryVerificationID, in.RegistryInputHash = approved.ID, approved.InputHash
			published, err := s.PublishDeploymentArtifactScan(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := s.GetFreshDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, in.WorkloadName)
			if err != nil || fresh.ID != published.ID || !fresh.ScannedAt.Equal(published.ScannedAt) || !fresh.ExpiresAt.Equal(published.ExpiresAt) {
				t.Fatalf("fresh read changed immutable scan clock: %v", err)
			}
			bundle, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID)
			if sidecar {
				if !errors.Is(err, ErrApplicationStandardRuntimeStale) {
					t.Fatalf("sidecar alone authorized complete component set: %v", err)
				}
			} else if err != nil || len(bundle.Components) != 1 || len(bundle.Bases) != 0 || !bundle.ExpiresAt.Equal(fresh.ExpiresAt) || bundle.CheckedAt.Before(fresh.ScannedAt) {
				t.Fatalf("complete private evidence lost membership/lease: %v", err)
			}
			fresh.Result.Vulnerabilities[0].Paths[0] = "returned mutation"
			again, err := s.GetFreshDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, in.WorkloadName)
			if err != nil || again.Result.Vulnerabilities[0].Paths[0] == "returned mutation" {
				t.Fatalf("fresh result aliased private bytes: %v", err)
			}
			if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), uuid.NewString(), app.ID, dep.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("fresh evidence crossed owner: %v", err)
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := s.GetFreshDeploymentArtifactScan(cancelled, app.AccountID, app.ID, dep.ID, in.WorkloadName); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled fresh read succeeded: %v", err)
			}
			if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetFreshDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, in.WorkloadName); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
				t.Fatalf("revoked publisher retained fresh evidence: %v", err)
			}
			history, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, in.WorkloadName)
			if err != nil || history.ID != published.ID || !history.ExpiresAt.Equal(published.ExpiresAt) {
				t.Fatalf("fresh refusal rewrote history: %v", err)
			}
		})
	}
}

func artifactEvidenceCannotBecomeLegacy(t *testing.T, s artifactEvidenceTestStore) {
	_, app, dep := registryVerificationFixture(t, s, false)
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrDeploymentArtifactScanEvidenceAbsent) {
		t.Fatalf("owned unproved deployment has wrong absence: %v", err)
	}
	in, _, app, dep := artifactScanFixture(t, s, false)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(t.Context(), dep.ID, "/changed.ext4", "apps/changed.ext4", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("stale private lineage fell back to legacy: %v", err)
	}
}

func TestMemArtifactEvidenceLifecycle(t *testing.T) { artifactEvidenceLifecycle(t, NewMemStore()) }
func TestMemArtifactEvidenceCannotBecomeLegacy(t *testing.T) {
	artifactEvidenceCannotBecomeLegacy(t, NewMemStore())
}

func TestMemArtifactEvidenceExpiredLease(t *testing.T) {
	s := NewMemStore()
	in, _, app, dep := artifactScanFixture(t, s, false)
	value, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	// A model-clock fixture only; the PostgreSQL counterpart waits for an
	// actual storage-clock lease to expire without updating immutable rows.
	s.mu.Lock()
	value.ExpiresAt = time.Now().UTC().Add(-time.Nanosecond)
	s.deploymentArtifactScans[value.ID] = value
	s.mu.Unlock()
	if _, err := s.GetFreshDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired private lease was fresh: %v", err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired component set was fresh: %v", err)
	}
}

type artifactBaseEvidenceTestStore interface {
	artifactEvidenceTestStore
	BaseImageProducerStore
	BaseImageScanStore
}

func artifactEvidenceRequiresBase(t *testing.T, s artifactBaseEvidenceTestStore) {
	in, base, app, dep := artifactScanBaseFixture(t, s)
	main, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, ""); err != nil {
		t.Fatalf("component read unexpectedly required a whole-runtime scan: %v", err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("missing shared base authorized complete evidence: %v", err)
	}
	baseInput := BaseImageScanInput{ID: uuid.NewString(), BaseProducerID: base.ID, BaseInputHash: base.InputHash, Artifact: base.Input.Artifact, SourceReference: base.Input.SourceReference, Status: "complete", ScannerName: "grype", Report: &api.ScanResult{ImageDigest: base.Input.SourceReference, ArtifactDigest: base.Input.Artifact.Digest, ScannerVersion: in.Report.ScannerVersion, ScannerDBStatus: in.Report.ScannerDBStatus, ScannerDBVersion: in.Report.ScannerDBVersion, ScannerDBBuiltAt: in.Report.ScannerDBBuiltAt, Vulnerabilities: []api.Vulnerability{}}}
	if _, err := s.PublishBaseImageScan(t.Context(), baseInput); err != nil {
		t.Fatal(err)
	}
	bundle, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || len(bundle.Components) != 1 || len(bundle.Bases) != 1 || bundle.Bases[0].Input.BaseProducerID != base.ID || !bundle.ExpiresAt.Equal(main.ExpiresAt) {
		t.Fatalf("complete two-drive evidence lost membership or minimum lease: %v", err)
	}
	baseInput.ID, baseInput.Status, baseInput.Failure, baseInput.ScannerName, baseInput.Report = uuid.NewString(), "failed", "scanner_unavailable", "", nil
	if _, err := s.PublishBaseImageScan(t.Context(), baseInput); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("failed shared base retained fresh complete evidence: %v", err)
	}
	replacement := base.Input
	replacement.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("replaced shared base retained fresh complete evidence: %v", err)
	}
	current, err := s.DeploymentByID(t.Context(), dep.ID)
	if err != nil || current.ScanStatus != "complete" || !current.ScannedAt.Equal(main.ScannedAt) {
		t.Fatalf("base refusal rewrote the separate main compatibility report: %v", err)
	}
}

func TestMemArtifactEvidenceRequiresBase(t *testing.T) {
	artifactEvidenceRequiresBase(t, NewMemStore())
}

func artifactEvidenceDatabaseAgesOut(t *testing.T, s artifactEvidenceTestStore) {
	in, _, app, dep := artifactScanFixture(t, s, false)
	expires := time.Now().UTC().Add(2 * time.Second)
	in.Report.ScannerDBBuiltAt = expires.Add(-api.ApplicationStandardScannerDBMaxAge).Format(time.RFC3339Nano)
	value, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(expires) + 20*time.Millisecond)
	if _, err := s.GetFreshDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("database age was frozen at scan publication: %v", err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("complete set ignored advancing database age: %v", err)
	}
	history, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || history.ID != value.ID || !history.ExpiresAt.After(time.Now()) || !history.ScannedAt.Equal(value.ScannedAt) {
		t.Fatalf("database-age refusal rewrote the still-unexpired scan: %v", err)
	}
}

func TestMemArtifactEvidenceDatabaseAgesOut(t *testing.T) {
	artifactEvidenceDatabaseAgesOut(t, NewMemStore())
}
