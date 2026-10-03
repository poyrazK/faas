package imaged

// adr: 435

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

type baseScanCompatibility struct {
	Image, Source        string
	Findings             map[string]int `json:"findings"`
	FixAvailableFindings map[string]int `json:"fix_available_findings"`
	ScannedAt            time.Time      `json:"scanned_at"`
}

func readBaseScanCompatibility(t *testing.T, be storage.StorageBackend, key string) baseScanCompatibility {
	t.Helper()
	rc, err := be.Get(t.Context(), wire.ScanKeyForBaseKey(key))
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var value baseScanCompatibility
	if err := json.NewDecoder(rc).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func requireBaseScanRefusal(t *testing.T, value baseScanCompatibility, scanned bool) {
	t.Helper()
	if value.Findings[SeverityHigh] != 9999 || value.Findings[SeverityCritical] != 9999 || value.FixAvailableFindings[SeverityHigh] != 9999 || value.ScannedAt.IsZero() == scanned {
		t.Fatalf("old clean report or invented scan clock survived: %+v", value)
	}
}

// Fixtures exercise real byte publication, protection and lineage with injected
// mkfs and Grype results. They are not native ext4, scanner or consumer ACKs.
func TestProducedBaseScanProtectedBytesAndCache(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "streamed backend"}[streamed], func(t *testing.T) {
			producedBaseScanProtectedBytesAndCache(t, streamed)
		})
	}
}

func producedBaseScanProtectedBytesAndCache(t *testing.T, streamed bool) {
	t.Helper()
	h, store, puller, _, ref := verifiedBaseFixture(t, "complete")
	if streamed {
		be, err := h.storageFor()
		if err != nil {
			t.Fatal(err)
		}
		h.WithStorage(noLocalPathBackend{be})
	}
	const key = "base/verified.ext4"
	calls, scanPath := 0, ""
	h.WithGrypeRun(func(_ context.Context, path string) (*ScanResult, error) {
		calls++
		scanPath = path
		file, err := os.Stat(path)
		parent, parentErr := os.Stat(filepath.Dir(path))
		body, readErr := os.ReadFile(path)
		if err != nil || parentErr != nil || readErr != nil || file.Mode().Perm() != 0o600 || parent.Mode().Perm() != 0o700 || !bytes.Equal(body, make([]byte, 1024)) || path == filepath.Join(h.appsRoot, key) {
			t.Fatalf("scanner did not get protected complete base bytes: %v %v %v", err, parentErr, readErr)
		}
		return producedScanResult(t, false), nil
	})
	first, err := h.EnsureBaseExt4(t.Context(), ref, key, "base/verified.digest", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	scan, err := store.GetFreshBaseImageScan(t.Context(), first.Producer.ID, first.Producer.InputHash)
	if err != nil || scan.ID == first.Producer.ID || scan.Input.BaseProducerID != first.Producer.ID || scan.Input.Artifact != first.Producer.Input.Artifact || scan.Input.SourceReference != ref || scan.Input.Report.ScannedAt != "" || scan.Input.Artifact.Bytes <= first.Producer.Input.ContentBytes {
		t.Fatalf("scan lost exact complete producer binding: %v", err)
	}
	puller.resolveErr = errors.New("registry unavailable")
	cached, err := h.EnsureBaseExt4(t.Context(), ref, key, "base/verified.digest", "", "", "")
	got, getErr := store.GetFreshBaseImageScan(t.Context(), first.Producer.ID, first.Producer.InputHash)
	if err != nil || getErr != nil || !cached.Skipped || calls != 1 || got.ID != scan.ID || !got.ScannedAt.Equal(scan.ScannedAt) || !got.ExpiresAt.Equal(scan.ExpiresAt) {
		t.Fatalf("cache reuse rescanned or renewed immutable evidence: %v %v", err, getErr)
	}
	if _, err := os.Stat(scanPath); !os.IsNotExist(err) {
		t.Fatal("protected base scan snapshot leaked")
	}
	be, _ := h.storageFor()
	compat := readBaseScanCompatibility(t, be, key)
	if compat.Findings[SeverityHigh] != 0 || compat.Image != ref || !compat.ScannedAt.Equal(scan.ScannedAt) {
		t.Fatal("compatibility output was not derived from retained scan")
	}
	if streamed && compat.Source != "" {
		t.Fatal("streamed backend invented a canonical local path")
	}
}

func TestProducedBaseScanRefusesUncertainRefresh(t *testing.T) {
	for _, mode := range []string{"canonical before", "canonical during", "snapshot during", "nil result", "wrong scanner", "caller clock", "invalid database", "scanner cancelled", "producer replaced"} {
		t.Run(mode, func(t *testing.T) {
			h, store, _, _, ref := verifiedBaseFixture(t, "complete")
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
			base, err := h.EnsureBaseExt4(t.Context(), ref, "base/verified.ext4", "base/verified.digest", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			old, err := store.GetFreshBaseImageScan(t.Context(), base.Producer.ID, base.Producer.InputHash)
			if err != nil {
				t.Fatal(err)
			}
			failed := old.Input
			failed.ID, failed.Status, failed.Failure, failed.ScannerName, failed.Report = uuid.NewString(), "failed", "scanner_unavailable", "", nil
			if _, err := store.PublishBaseImageScan(t.Context(), failed); err != nil {
				t.Fatal(err)
			}
			be, _ := h.storageFor()
			if mode == "canonical before" {
				if err := be.Put(t.Context(), base.StorageKey, strings.NewReader("changed base")); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			h.WithGrypeRun(func(ctx context.Context, path string) (*ScanResult, error) {
				called = true
				result := producedScanResult(t, false)
				switch mode {
				case "canonical during":
					if err := be.Put(ctx, base.StorageKey, strings.NewReader("changed base")); err != nil {
						t.Fatal(err)
					}
				case "snapshot during":
					if err := os.WriteFile(path, []byte("changed scratch"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "nil result":
					return nil, nil
				case "wrong scanner":
					result.ScannerName = "other"
				case "caller clock":
					result.ScannedAt = time.Now().UTC().Format(time.RFC3339Nano)
				case "invalid database":
					result.ScannerDBStatus = "invalid"
				case "scanner cancelled":
					return nil, context.Canceled
				case "producer replaced":
					in := base.Producer.Input
					in.ID = uuid.NewString()
					if _, err := store.PublishBaseImageProducer(ctx, in); err != nil {
						t.Fatal(err)
					}
				}
				return result, nil
			})
			value, err := h.ensureProducedBaseScan(t.Context(), be, base.Producer, "")
			compat := readBaseScanCompatibility(t, be, base.StorageKey)
			if mode == "producer replaced" {
				if !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
					t.Fatalf("superseded producer published scan: %v", err)
				}
				requireBaseScanRefusal(t, compat, false)
				return
			}
			if err != nil || value.Input.Status != "failed" || value.Result.Status != "failed" || value.Input.Report != nil || value.Result.SeverityCounts != (api.SeverityCounts{}) || value.ID == old.ID || value.Result.Error == "" {
				t.Fatalf("uncertain scan did not retain explicit failure: %v", err)
			}
			if mode == "canonical before" && called {
				t.Fatal("scanner ran on mismatched canonical bytes")
			}
			requireBaseScanRefusal(t, compat, true)
		})
	}
}

type refusingBaseProducerStore struct{ *state.MemStore }

func (s refusingBaseProducerStore) PublishBaseImageProducer(context.Context, state.BaseImageProducerInput) (state.BaseImageProducer, error) {
	return state.BaseImageProducer{}, errors.New("injected producer publication refusal")
}

func TestProducedBaseRebuildInvalidatesOldCompatibilityBeforePublication(t *testing.T) {
	for _, mode := range []string{"bad CRC", "producer publication"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := mode
			if mode == "producer publication" {
				fixtureMode = "complete"
			}
			h, store, _, _, ref := verifiedBaseFixture(t, fixtureMode)
			if mode == "producer publication" {
				h.store = refusingBaseProducerStore{store}
			}
			be, _ := h.storageFor()
			key := "base/verified.ext4"
			if err := be.Put(t.Context(), wire.ScanKeyForBaseKey(key), strings.NewReader(`{"findings":{"Critical":0,"High":0},"scanned_at":"2026-09-30T00:00:00Z"}`)); err != nil {
				t.Fatal(err)
			}
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) {
				t.Fatal("scan ran before producer publication")
				return nil, nil
			})
			if _, err := h.EnsureBaseExt4(t.Context(), ref, key, "base/verified.digest", "", "", ""); err == nil {
				t.Fatal("failed build/publication accepted")
			}
			requireBaseScanRefusal(t, readBaseScanCompatibility(t, be, key), false)
		})
	}
}

func TestProducedBaseScanGatesTwoDriveDeployment(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete base and app", true: "unsafe base"}[unsafe], func(t *testing.T) {
			h, th := producedScanFixtureWithBase(t, false, true)
			root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
			if err != nil || root.Input.Kind != "app-layer" || root.Input.LayerStart != 1 || root.Input.BaseProducerID == "" {
				t.Fatalf("fixture did not build actual two-drive lineage: %v", err)
			}
			base, err := th.store.GetBaseImageProducerByID(t.Context(), root.Input.BaseProducerID)
			if err != nil {
				t.Fatal(err)
			}
			previous, err := th.store.GetFreshBaseImageScan(t.Context(), base.ID, base.InputHash)
			if err != nil {
				t.Fatal(err)
			}
			failed := previous.Input
			failed.ID, failed.Status, failed.Failure, failed.ScannerName, failed.Report = uuid.NewString(), "failed", "scanner_unavailable", "", nil
			if _, err := th.store.PublishBaseImageScan(t.Context(), failed); err != nil {
				t.Fatal(err)
			}
			calls := 0
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) {
				calls++
				return producedScanResult(t, unsafe && calls == 1), nil
			})
			err = h.runDeployScan(t.Context(), th.app, th.dep)
			if unsafe {
				if !errors.Is(err, errSecurityScanBlocked) || calls != 1 {
					t.Fatalf("unsafe shared base permitted deployment: %v", err)
				}
				return
			}
			main, mainErr := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
			shared, baseErr := th.store.GetFreshBaseImageScan(t.Context(), base.ID, base.InputHash)
			if err != nil || mainErr != nil || baseErr != nil || calls != 2 || main.Input.RootfsProducerID != root.ID || shared.Input.BaseProducerID != base.ID || main.ID == shared.ID {
				t.Fatalf("both drives did not retain separate component scans: %v %v %v", err, mainErr, baseErr)
			}
		})
	}
}
