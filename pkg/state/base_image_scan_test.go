package state

// adr: 435

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

type baseScanTestStore interface {
	BaseImageProducerStore
	BaseImageScanStore
}

// Real layer consumption and private store facts, not native scan/boot proof.
func baseScanFixture(t *testing.T, s baseScanTestStore) (BaseImageScanInput, BaseImageProducer) {
	t.Helper()
	base, err := s.PublishBaseImageProducer(t.Context(), baseProducerFixture(t, "base/scan-fixture.ext4", "base layer"))
	if err != nil {
		t.Fatal(err)
	}
	report := &api.ScanResult{ImageDigest: base.Input.SourceReference, ArtifactDigest: base.Input.Artifact.Digest, ScannerVersion: "0.116.0", ScannerDBStatus: "valid", ScannerDBVersion: "v6.0.2", ScannerDBBuiltAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), SeverityCounts: api.SeverityCounts{Low: 1}, Vulnerabilities: []api.Vulnerability{{ID: "CVE-fixture", Severity: "LOW", Package: "base", Paths: []string{"/base/file"}}}}
	return BaseImageScanInput{ID: uuid.NewString(), BaseProducerID: base.ID, BaseInputHash: base.InputHash, Artifact: base.Input.Artifact, SourceReference: base.Input.SourceReference, Status: "complete", ScannerName: "grype", Report: report}, base
}

func baseScanLifecycle(t *testing.T, s baseScanTestStore) {
	in, base := baseScanFixture(t, s)
	start := time.Now().UTC()
	value, err := s.PublishBaseImageScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if value.ScannedAt.Before(start.Add(-time.Second)) || value.ScannedAt.Before(base.PublishedAt) || value.ExpiresAt.Sub(value.ScannedAt) != api.ApplicationStandardArtifactScanTTL || value.Input.Report.ScannedAt != "" {
		t.Fatal("base scan clock was not storage owned")
	}
	retry, err := s.PublishBaseImageScan(t.Context(), in)
	if err != nil || retry.InputHash != value.InputHash || !retry.ScannedAt.Equal(value.ScannedAt) || !retry.ExpiresAt.Equal(value.ExpiresAt) {
		t.Fatalf("retry renewed base scan: %v", err)
	}
	in.Report.Vulnerabilities[0].Paths[0] = "caller mutation"
	value.Result.Vulnerabilities[0].Paths[0] = "returned result mutation"
	value.Input.Report.Vulnerabilities[0].Paths[0] = "returned input mutation"
	got, err := s.GetFreshBaseImageScan(t.Context(), base.ID, base.InputHash)
	if err != nil || got.Result.Vulnerabilities[0].Paths[0] != "/base/file" || got.Input.Report.Vulnerabilities[0].Paths[0] != "/base/file" {
		t.Fatalf("base scan buffers aliased: %v", err)
	}
	failed := got.Input
	failed.ID, failed.Status, failed.Failure, failed.ScannerName, failed.Report = uuid.NewString(), "failed", "scanner_unavailable", "", nil
	if _, err := s.PublishBaseImageScan(t.Context(), failed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshBaseImageScan(t.Context(), base.ID, base.InputHash); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("failed base scan remained fresh: %v", err)
	}
	history, err := s.GetCurrentBaseImageScan(t.Context(), base.Input.Artifact.StorageKey)
	if err != nil || history.ID != failed.ID || history.Result.Status != "failed" {
		t.Fatalf("failed scan history missing: %v", err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), got.Input); !errors.Is(err, ErrConflict) {
		t.Fatalf("historical success reactivated: %v", err)
	}
	newBase := base.Input
	newBase.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), newBase); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentBaseImageScan(t.Context(), base.Input.Artifact.StorageKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaced producer scan exposed: %v", err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), failed); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("replaced producer renewed scan: %v", err)
	}
}

func baseScanRefusals(t *testing.T, s baseScanTestStore) {
	in, base := baseScanFixture(t, s)
	for _, field := range []string{"hash", "digest", "bytes", "source", "scanner", "clock", "counts", "null matches", "old database", "future database"} {
		t.Run(field, func(t *testing.T) {
			bad := cloneBaseImageScan(BaseImageScan{Input: in}).Input
			bad.ID = uuid.NewString()
			switch field {
			case "hash":
				bad.BaseInputHash = strings.Repeat("0", 64)
			case "digest":
				bad.Artifact.Digest = imagechain.Digest([]byte("other"))
				bad.Report.ArtifactDigest = bad.Artifact.Digest
			case "bytes":
				bad.Artifact.Bytes++
			case "source":
				bad.SourceReference = "registry.example/other@" + base.Input.SourceDigest
				bad.Report.ImageDigest = bad.SourceReference
			case "scanner":
				bad.ScannerName = "other"
			case "clock":
				bad.Report.ScannedAt = time.Now().UTC().Format(time.RFC3339Nano)
			case "counts":
				bad.Report.SeverityCounts.Low = 0
			case "null matches":
				bad.Report.Vulnerabilities = nil
			case "old database":
				bad.Report.ScannerDBBuiltAt = time.Now().Add(-api.ApplicationStandardScannerDBMaxAge - time.Minute).UTC().Format(time.RFC3339Nano)
			case "future database":
				bad.Report.ScannerDBBuiltAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
			}
			if _, err := s.PublishBaseImageScan(t.Context(), bad); err == nil {
				t.Fatal("unbound base scan accepted")
			}
			if _, err := s.GetCurrentBaseImageScan(t.Context(), in.Artifact.StorageKey); !errors.Is(err, ErrNotFound) {
				t.Fatalf("refusal changed current scan: %v", err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.PublishBaseImageScan(cancelled, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan published: %v", err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), in); err != nil {
		t.Fatalf("valid retry refused: %v", err)
	}
}

func TestMemBaseScanLifecycle(t *testing.T) { baseScanLifecycle(t, NewMemStore()) }
func TestMemBaseScanRefusals(t *testing.T)  { baseScanRefusals(t, NewMemStore()) }
func TestMemBaseScanExpiredLease(t *testing.T) {
	s := NewMemStore()
	in, base := baseScanFixture(t, s)
	value, err := s.PublishBaseImageScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	// Model a formerly valid row; the private fresh read must refuse it.
	value.ScannedAt = time.Now().Add(-2 * api.ApplicationStandardArtifactScanTTL).UTC()
	value.ExpiresAt = value.ScannedAt.Add(api.ApplicationStandardArtifactScanTTL)
	value.Result = baseScanResult(value.Input, value.ScannedAt)
	s.mu.Lock()
	s.baseImageScans[in.ID] = value
	s.mu.Unlock()
	if _, err := s.GetFreshBaseImageScan(t.Context(), base.ID, base.InputHash); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired scan remained fresh: %v", err)
	}
	retry, err := s.PublishBaseImageScan(t.Context(), in)
	if err != nil || !retry.ScannedAt.Equal(value.ScannedAt) || !retry.ExpiresAt.Equal(value.ExpiresAt) {
		t.Fatalf("expired retry extended scan: %v", err)
	}
}
