package imaged

// adr: 20261028102540

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
)

// Fixtures inject Grype results and the scanner identity; they do not run a
// native scanner.
func TestProducedBaseScanRenewsWithoutScanningOnlyForTheSameIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity func(scanned scannerIdentity) (scannerIdentity, error)
		failPrev bool
		want     bool
	}{
		{name: "same bytes, scanner and database", identity: func(s scannerIdentity) (scannerIdentity, error) { return s, nil }, want: true},
		{name: "newer database", identity: func(s scannerIdentity) (scannerIdentity, error) {
			s.DBBuiltAt = "2099-01-01T00:00:00Z"
			return s, nil
		}},
		{name: "other scanner version", identity: func(s scannerIdentity) (scannerIdentity, error) {
			s.Version = "0.200.0"
			return s, nil
		}},
		{name: "identity unknown", identity: func(scannerIdentity) (scannerIdentity, error) { return scannerIdentity{}, errors.New("grype missing") }},
		{name: "previous scan failed", identity: func(s scannerIdentity) (scannerIdentity, error) { return s, nil }, failPrev: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, store, _, _, ref := verifiedBaseFixture(t, "complete")
			calls := 0
			var scanned scannerIdentity
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) {
				calls++
				result := producedScanResult(t, false)
				scanned, _ = newScannerIdentity(result.ScannerVersion, result.ScannerDBVersion, result.ScannerDBBuiltAt)
				return result, nil
			})
			first, err := h.EnsureBaseExt4(t.Context(), ref, "base/verified.ext4", "base/verified.digest", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			base := first.Producer
			prior, err := store.GetCurrentBaseImageScan(t.Context(), base.Input.Artifact.StorageKey)
			if err != nil || calls != 1 {
				t.Fatalf("first stage did not scan once: %d calls, %v", calls, err)
			}
			if tc.failPrev {
				in := state.BaseImageScanInput{ID: uuid.NewString(), BaseProducerID: base.ID, BaseInputHash: base.InputHash, Artifact: base.Input.Artifact, SourceReference: base.Input.SourceReference}
				if prior, err = h.publishBaseScanFailure(t.Context(), store, in, "scanner_unavailable"); err != nil {
					t.Fatal(err)
				}
			}
			h.WithScannerIdentity(func(context.Context) (scannerIdentity, error) { return tc.identity(scanned) })
			h.scannerIdentityCache.set(scannerIdentity{}, prior.ScannedAt) // the fixture's real scan filled it
			be, err := h.storageFor()
			if err != nil {
				t.Fatal(err)
			}
			expected := scanArtifactTarget{StorageKey: base.Input.Artifact.StorageKey, ArtifactIdentity: rootfs.ArtifactIdentity{Digest: base.Input.Artifact.Digest, Bytes: base.Input.Artifact.Bytes}}
			value, ok := h.renewProducedBaseScanWithoutScanning(t.Context(), store, be, base, expected)
			if ok != tc.want || calls != 1 {
				t.Fatalf("renewed = %v (want %v) after %d scans", ok, tc.want, calls)
			}
			if !tc.want {
				return
			}
			if value.ID == prior.ID || value.Input.ReusedScanID != prior.ID || value.Input.Status != "complete" || value.Result.SeverityCounts != prior.Result.SeverityCounts {
				t.Fatalf("renewal did not copy the prior report into a new record: %+v", value.Input)
			}
			if value.ExpiresAt.Before(prior.ExpiresAt) {
				t.Fatal("renewal did not get its own lease")
			}
			current, err := store.GetFreshBaseImageScan(t.Context(), base.ID, base.InputHash)
			if err != nil || current.ID != value.ID {
				t.Fatalf("renewed record is not the fresh selection: %v", err)
			}
		})
	}
}
