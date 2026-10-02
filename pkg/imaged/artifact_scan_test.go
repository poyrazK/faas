package imaged

// adr: 431

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Real layer consumption and byte publication, with injected mkfs and scanner
// fixtures. This verifies portable ownership/identity behavior, not native ext4,
// Grype execution, or any VM consumer ACK.
func producedScanFixture(t *testing.T, sidecar bool) (*Handler, *testHarness) {
	return producedScanFixtureWithBase(t, sidecar, false)
}

func producedScanFixtureWithBase(t *testing.T, sidecar, sharedBase bool) (*Handler, *testHarness) {
	t.Helper()
	th := newTestHarness(t, state.DeploymentKindImage, api.PlanPro, "")
	th.app.RequireSigned, th.app.Runtime = true, "node22"
	th.dep.ImageDigest, th.dep.FullRootfsAllowAuto = "registry.example/team/app:latest", true
	if sidecar {
		th.dep.Sidecars = []byte(`[{"name":"metrics","image":"registry.example/team/app:latest","type":"sidecar","port":9090}]`)
	}
	th.createReplacementDeployment(t)
	layer := gzTarWithModes(t, map[string]string{"app/server": "#!/bin/sh\n"}, map[string]int64{"app/server": 0o755})
	diff := imagechain.Digest(layerPlainBytes(t, layer))
	baseLayer := gzTar(t, map[string]string{"usr/share/base": "base contents"})
	baseDiff := imagechain.Digest(layerPlainBytes(t, baseLayer))
	diffs, layers := []string{diff}, []oci.Descriptor{{Digest: imagechain.Digest(layer), Size: int64(len(layer))}}
	if sharedBase {
		diffs = append([]string{baseDiff}, diffs...)
		layers = append([]oci.Descriptor{{Digest: imagechain.Digest(baseLayer), Size: int64(len(baseLayer))}}, layers...)
	}
	cfg, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "config": map[string]any{"Entrypoint": []string{"/app/server"}}, "rootfs": map[string]any{"type": "layers", "diff_ids": diffs}})
	manifest := oci.Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json", Config: oci.Descriptor{Digest: imagechain.Digest(cfg), Size: int64(len(cfg))}, Layers: layers}
	raw, _ := json.Marshal(manifest)
	digest := imagechain.Digest(raw)
	ref := "registry.example/team/app@" + digest
	baseDiffs := []string{diff, diff} // force the existing full-rootfs fixture
	if sharedBase {
		baseDiffs = []string{baseDiff}
	}
	baseCfg, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": baseDiffs}})
	baseManifest := oci.Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json", Config: oci.Descriptor{Digest: imagechain.Digest(baseCfg), Size: int64(len(baseCfg))}, Layers: []oci.Descriptor{{Digest: imagechain.Digest(baseLayer), Size: int64(len(baseLayer))}}}
	mp := &fakeManifestPuller{appRef: ref, appManifest: manifest, appConfig: oci.Config{Entrypoint: []string{"/app/server"}, DiffIDs: diffs}, baseManifest: baseManifest, baseConfig: oci.Config{DiffIDs: baseDiffs}, layerBlobs: map[string][]byte{imagechain.Digest(layer): layer, imagechain.Digest(baseLayer): baseLayer, imagechain.Digest(cfg): cfg, imagechain.Digest(baseCfg): baseCfg}}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := th.store.UpsertAppTrustedSigner(t.Context(), th.app.AccountID, th.app.ID, "company", der, th.app.AccountID); err != nil {
		t.Fatal(err)
	}
	payload := []byte(fmt.Sprintf(`{"critical":{"identity":{"docker-reference":"registry.example/team/app"},"image":{"docker-manifest-digest":%q},"type":"cosign container image signature"}}`, digest))
	hash := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	p := &resolvingTestPuller{fakeManifestPuller: mp, resolution: oci.ImageResolution{SourceReference: ref, SourceDigest: digest, Reference: ref, Digest: digest, Evidence: &imagechain.Evidence{SourceManifest: raw, Config: cfg}}, attachments: []oci.ImageSignatureAttachment{{ManifestDigest: imagechain.Digest([]byte("fixture attachment")), PayloadDigest: imagechain.Digest(payload), Payload: payload, Signature: sig}}}
	guest := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(guest, []byte("INIT"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New(th.store, th.notif, p, rootfs.NewBuilder(&recordingRunner{}), guest, th.appsR, silentLogger())
	h.trustedPublishersCacheOK = true
	h.trustedPublishersCache = map[string][]cosign.TrustedPublisher{th.app.ID: {{Name: "company", PublicKey: &key.PublicKey}}}
	if sharedBase {
		rawBase, _ := json.Marshal(baseManifest)
		baseRef := "registry.example/base@" + imagechain.Digest(rawBase)
		basePuller := &baseResolutionPuller{minimalManifestPuller: &minimalManifestPuller{manifest: baseManifest, layers: mp.layerBlobs}, resolution: oci.ImageResolution{SourceReference: baseRef, Reference: baseRef, SourceDigest: imagechain.Digest(rawBase), Digest: imagechain.Digest(rawBase), Evidence: &imagechain.Evidence{SourceManifest: rawBase, Config: baseCfg}}}
		baseHandler := New(th.store, th.notif, basePuller, rootfs.NewBuilder(&recordingRunner{}), guest, th.appsR, silentLogger())
		baseHandler.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
		if _, err := baseHandler.EnsureBaseExt4(t.Context(), baseRef, sched.BaseKeyForArch(th.app.Runtime, oci.ImageArchitecture), "base/scan-fixture.digest", "", "", ""); err != nil {
			t.Fatal(err)
		}
		h.WithDeployBaseRef(baseRef)
	}
	if err := h.buildImageLayer(t.Context(), th.app, th.dep, th.acct); err != nil {
		t.Fatal(err)
	}
	if sidecar {
		if _, err := h.buildSidecarLayers(t.Context(), th.app, th.dep, th.acct); err != nil {
			t.Fatal(err)
		}
	}
	policy := api.AppSecurityPolicyEnforce
	th.app, err = th.store.UpdateApp(t.Context(), th.app.ID, state.UpdateAppParams{SecurityPolicy: &policy, SetSecurityPolicy: true})
	if err != nil {
		t.Fatal(err)
	}
	th.dep, err = th.store.DeploymentByID(t.Context(), th.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	return h, th
}

func producedScanResult(t *testing.T, high bool) *ScanResult {
	t.Helper()
	match := "[]"
	if high {
		match = `[{"vulnerability":{"id":"CVE-fixture","severity":"High"},"artifact":{"name":"fixture","version":"1"}}]`
	}
	raw := fmt.Sprintf(`{"matches":%s,"descriptor":{"name":"grype","version":"0.116.0","db":{"status":{"valid":true,"schemaVersion":"v6.0.2","built":%q}}}}`, match, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	result, err := parseGrypeOutput([]byte(raw), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProducedScanUsesProtectedProducerBytes(t *testing.T) {
	h, th := producedScanFixture(t, true)
	var paths []string
	h.WithGrypeRun(func(ctx context.Context, path string) (*ScanResult, error) {
		paths = append(paths, path)
		if path == th.dep.RootfsPath {
			t.Fatal("scanner received canonical rootfs")
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("unprotected scan snapshot: %v", err)
		}
		body, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(body, make([]byte, 1024)) {
			t.Fatalf("wrong produced scan bytes: %v", err)
		}
		return producedScanResult(t, false), nil
	})
	if err := h.runDeployScan(t.Context(), th.app, th.dep); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] == paths[1] {
		t.Fatal("main and sidecar did not get separate protected scans")
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("scan snapshot leaked")
		}
	}
	for _, workload := range []string{"", "metrics"} {
		value, err := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, workload)
		root, rootErr := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, workload)
		if err != nil || rootErr != nil || value.Input.RootfsProducerID != root.ID || value.Input.RootfsInputHash != root.InputHash || value.Result.ArtifactDigest != root.Input.ArtifactDigest || value.Result.Status != "complete" || value.Input.Report.ScannedAt != "" {
			t.Fatalf("scan lost producer or storage clock: %v %v", err, rootErr)
		}
	}
}

func TestProducedScanRefusesMutationAndInvalidScanner(t *testing.T) {
	for _, mode := range []string{"canonical before", "canonical during", "snapshot during", "nil result", "wrong scanner", "invented clock", "database invalid", "revoked key", "cancelled scanner"} {
		t.Run(mode, func(t *testing.T) {
			h, th := producedScanFixture(t, false)
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
			if err := h.runDeployScan(t.Context(), th.app, th.dep); err != nil {
				t.Fatal(err)
			}
			previous, err := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			be, err := h.storageFor()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "canonical before" {
				if err := be.Put(t.Context(), th.dep.RootfsKey, strings.NewReader("different output")); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			h.WithGrypeRun(func(ctx context.Context, path string) (*ScanResult, error) {
				called = true
				result := producedScanResult(t, false)
				switch mode {
				case "canonical during":
					if err := be.Put(ctx, th.dep.RootfsKey, strings.NewReader("different output")); err != nil {
						t.Fatal(err)
					}
				case "snapshot during":
					if err := os.WriteFile(path, []byte("different output"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "nil result":
					return nil, nil
				case "wrong scanner":
					result.ScannerName = "other"
				case "invented clock":
					result.ScannedAt = time.Now().UTC().Format(time.RFC3339Nano)
				case "database invalid":
					result.ScannerDBStatus = "invalid"
				case "revoked key":
					if err := th.store.DeleteAppTrustedSigner(ctx, th.app.AccountID, th.app.ID, "company"); err != nil {
						t.Fatal(err)
					}
				case "cancelled scanner":
					return nil, context.Canceled
				}
				return result, nil
			})
			if err := h.runDeployScan(t.Context(), th.app, th.dep); !errors.Is(err, errSecurityScanBlocked) {
				t.Fatalf("uncertain scan allowed: %v", err)
			}
			if mode == "canonical before" && called {
				t.Fatal("scanner ran on bytes differing from producer")
			}
			value, err := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
			if mode == "revoked key" {
				if err != nil || value.ID != previous.ID {
					t.Fatalf("revoked key renewed historical scan: %v", err)
				}
			} else if err != nil || value.Result.Status != "failed" || value.ID == previous.ID {
				t.Fatalf("uncertain scan did not replace success with failure: %v", err)
			}
		})
	}
}

func TestProducedSidecarScanBlocksAndQuarantinesLiveDeployment(t *testing.T) {
	h, th := producedScanFixture(t, true)
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) {
		calls++
		return producedScanResult(t, calls == 2), nil
	})
	loop := &Loop{store: th.store, handler: h, log: silentLogger()}
	loop.reconcileSecurityScans(t.Context(), time.Now().UTC(), time.Hour)
	main, err := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
	if err != nil || main.Result.SeverityCounts.High != 0 {
		t.Fatalf("sidecar overwrote main result: %v", err)
	}
	sidecar, err := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "metrics")
	if err != nil || sidecar.Result.SeverityCounts.High != 1 {
		t.Fatalf("sidecar findings lost: %v", err)
	}
	app, err := th.store.AppByID(t.Context(), th.app.ID)
	if err != nil || app.Status != state.AppEvictedCold {
		t.Fatalf("unsafe sidecar did not quarantine live app: %v", err)
	}
}

func TestScanCommandAndParserBounds(t *testing.T) {
	buffer := &scanCommandBuffer{limit: 10}
	if _, err := io.Copy(buffer, strings.NewReader(strings.Repeat("x", 11))); err == nil || buffer.buffer.Len() > 10 {
		t.Fatal("copy bypassed scanner output bound")
	}
	for _, raw := range []string{`{}`, `{"matches":null}`, strings.Repeat("x", api.ApplicationStandardScanMaxOutputBytes+1)} {
		if _, err := parseGrypeOutput([]byte(raw), "fixture"); err == nil {
			t.Fatal("missing or oversized scanner output accepted")
		}
	}
	if validDebugFSScanDiagnostics(nil, []byte("debugfs 1.47.0\nrdump: failed to read inode\n")) {
		t.Fatal("debugfs exit-zero error accepted")
	}
	if !validDebugFSScanDiagnostics(nil, []byte("debugfs 1.47.0 (5-Feb-2023)\n")) {
		t.Fatal("normal debugfs banner refused")
	}
}
