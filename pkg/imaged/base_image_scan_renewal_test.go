package imaged

// adr: 435

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// renewalDueBaseScanStore ages every fresh base scan past its renewal point,
// as a deploy staged more than two minutes after the last scan sees it.
type renewalDueBaseScanStore struct{ MemStore *state.MemStore }

func (s renewalDueBaseScanStore) GetFreshBaseImageScan(ctx context.Context, id, hash string) (state.BaseImageScan, error) {
	value, err := s.MemStore.GetFreshBaseImageScan(ctx, id, hash)
	value.ScannedAt = value.ScannedAt.Add(-api.ApplicationStandardArtifactScanRenewEvery - time.Second)
	return value, err
}

// sidecarCritical is the CRITICAL total vmmd reads from the base's sidecar.
func sidecarCritical(ctx context.Context, t *testing.T, be storage.StorageBackend, key string) int {
	t.Helper()
	rc, err := be.Get(ctx, wire.ScanKeyForBaseKey(key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	var value baseScanCompatibility
	if err := json.NewDecoder(rc).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value.Findings[SeverityCritical]
}

// Production-us rc.304 (Hunt 14 H14-117): a deploy that found the shared
// base's scan due for renewal published the CRITICAL=9999 refusal before
// rescanning, and vmmd on fsn-2 answered cold boots 503 scan_critical for the
// ~50 s Grype run. Renewing a complete scan of unchanged bytes keeps that
// verdict until the new one lands; a failed rescan or changed bytes still
// publish the refusal.
func TestProducedBaseScanRenewalKeepsVerdictUntilReplaced(t *testing.T) {
	for _, tc := range []struct {
		name         string
		newDatabase  bool
		scanFails    bool
		changedBytes bool
		holdExpired  bool // the last complete scan is older than the hold
		wantScans    int
		wantRefusal  bool // the sidecar ends as the refusal
		keepsVerdict bool // no write publishes the refusal before the result
	}{
		{name: "rescan after a database update", newDatabase: true, wantScans: 1, keepsVerdict: true},
		{name: "renewal without rescanning", keepsVerdict: true},
		// A scanner failure on unchanged bytes keeps the last complete scan
		// in force (bug hunt 2026-10-28 H102) until the hold runs out.
		{name: "rescan fails", newDatabase: true, scanFails: true, wantScans: 1, keepsVerdict: true},
		{name: "rescan fails after the hold ran out", newDatabase: true, scanFails: true, holdExpired: true, wantScans: 1, wantRefusal: true, keepsVerdict: true},
		{name: "bytes changed", newDatabase: true, changedBytes: true, wantRefusal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, store, _, _, ref := verifiedBaseFixture(t, "complete")
			var scanned installedScanner
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) {
				result := producedScanResult(t, false)
				scanned, _ = newScannerIdentity(result.ScannerVersion, result.ScannerDBVersion, result.ScannerDBBuiltAt)
				return result, nil
			})
			first, err := h.EnsureBaseExt4(t.Context(), ref, "base/verified.ext4", "base/verified.digest", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			prior, err := store.GetCurrentBaseImageScan(t.Context(), first.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			h.store = statetest.NewMem(renewalDueBaseScanStore{store})
			h.WithScannerIdentity(func(context.Context) (installedScanner, error) {
				if tc.newDatabase {
					s := scanned
					s.DBBuiltAt = "2099-01-01T00:00:00Z"
					return s, nil
				}
				return scanned, nil
			})
			h.scannerIdentityCache.set(installedScanner{}, prior.ScannedAt) // the fixture's real scan filled it
			if tc.holdExpired {
				saved := sharedBaseScanFailureGrace
				sharedBaseScanFailureGrace = 0
				t.Cleanup(func() { sharedBaseScanFailureGrace = saved })
			}
			be, err := h.storageFor()
			if err != nil {
				t.Fatal(err)
			}
			if tc.changedBytes {
				if err := be.Put(t.Context(), first.StorageKey, strings.NewReader("changed base")); err != nil {
					t.Fatal(err)
				}
			}
			scans := 0
			duringScan := -1
			h.WithGrypeRun(func(ctx context.Context, _ string) (*ScanResult, error) {
				scans++
				duringScan = sidecarCritical(ctx, t, be, first.StorageKey)
				if tc.scanFails {
					return nil, errors.New("injected grype failure")
				}
				return producedScanResult(t, false), nil
			})
			rec := &scanPutRecorder{StorageBackend: be, key: wire.ScanKeyForBaseKey(first.StorageKey)}
			value, err := h.ensureProducedBaseScan(t.Context(), rec, first.Producer, "")
			if err != nil || len(rec.critical) == 0 || scans != tc.wantScans {
				t.Fatalf("ensure = %v after %d scans (want %d); writes %v", err, scans, tc.wantScans, rec.critical)
			}
			if scans > 0 && duringScan != 0 {
				t.Fatalf("vmmd read CRITICAL=%d during the rescan, want the prior clean verdict", duringScan)
			}
			published := slices.Index(rec.critical, wire.BaseScanFailedCount)
			if tc.keepsVerdict && published >= 0 && published < len(rec.critical)-1 {
				t.Fatalf("the refusal was published before the result: writes %v", rec.critical)
			}
			if !tc.keepsVerdict && rec.critical[0] != wire.BaseScanFailedCount {
				t.Fatalf("changed bytes kept the old verdict: writes %v", rec.critical)
			}
			if tc.wantRefusal {
				requireBaseScanRefusal(t, readBaseScanCompatibility(t, be, first.StorageKey), value.Input.Status == "failed")
				return
			}
			if value.Input.Status != "complete" || rec.critical[len(rec.critical)-1] != 0 || slices.Contains(rec.critical, wire.BaseScanFailedCount) {
				t.Fatalf("renewal = %q; writes %v", value.Input.Status, rec.critical)
			}
		})
	}
}
