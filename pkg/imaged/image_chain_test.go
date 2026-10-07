package imaged

// adr: 435

import (
	"bytes"
	"compress/gzip"
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
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func layerPlainBytes(t *testing.T, body []byte) []byte {
	t.Helper()
	z, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	plain, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestSignedImageChainFeedsRealConversion(t *testing.T) {
	for _, kind := range []string{"app-layer", "full-rootfs", "sidecar-layer"} {
		for _, mode := range []string{"complete", "wrong DiffID", "bad CRC", "consumer omitted"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				th := newTestHarness(t, state.DeploymentKindImage, api.PlanPro, "")
				th.app.RequireSigned = true
				th.app.Runtime = "node22"
				th.app.ID, th.app.Slug = "", "chain-fixture"
				var err error
				th.app, err = th.store.CreateApp(t.Context(), th.app)
				if err != nil {
					t.Fatal(err)
				}
				th.dep.AppID = th.app.ID
				th.dep.ImageDigest = "registry.example/team/app:latest"
				th.dep.FullRootfsAllowAuto = true
				if kind == "sidecar-layer" {
					th.dep.Sidecars = []byte(`[{"name":"metrics","image":"registry.example/team/app:latest","type":"sidecar","port":9090}]`)
				}
				th.createReplacementDeployment(t)
				baseLayer := gzTar(t, map[string]string{"base/file": "base"})
				baseDiff := imagechain.Digest(layerPlainBytes(t, baseLayer))
				layer := gzTarWithModes(t, map[string]string{"app/server": "#!/bin/sh\n"}, map[string]int64{"app/server": 0o755})
				plain := layerPlainBytes(t, layer)
				diff := imagechain.Digest(plain)
				if mode == "wrong DiffID" {
					diff = imagechain.Digest([]byte("unrelated filesystem"))
				}
				if mode == "bad CRC" {
					layer[len(layer)-8] ^= 1
				}
				descriptors := []oci.Descriptor{{Digest: imagechain.Digest(layer), Size: int64(len(layer))}}
				diffs := []string{diff}
				var secondLayer []byte
				if kind == "app-layer" {
					// Identical DiffIDs with different gzip encodings must keep their
					// original descriptor positions; a map would lose the first blob.
					var second bytes.Buffer
					z := gzip.NewWriter(&second)
					z.Name = "second encoding"
					_, _ = z.Write(plain)
					_ = z.Close()
					secondLayer = bytes.Clone(second.Bytes())
					descriptors = append([]oci.Descriptor{{Digest: imagechain.Digest(baseLayer), Size: int64(len(baseLayer))}}, descriptors...)
					descriptors = append(descriptors, oci.Descriptor{Digest: imagechain.Digest(secondLayer), Size: int64(len(secondLayer))})
					diffs = []string{baseDiff, diff, imagechain.Digest(plain)}
				}
				config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "config": map[string]any{"Entrypoint": []string{"/app/server"}}, "rootfs": map[string]any{"type": "layers", "diff_ids": diffs}})
				manifest := oci.Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json", Config: oci.Descriptor{Digest: imagechain.Digest(config), Size: int64(len(config))}, Layers: descriptors}
				manifestBytes, _ := json.Marshal(manifest)
				index := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":%d,"platform":{"os":"linux","architecture":"amd64"}}]}`, imagechain.Digest(manifestBytes), len(manifestBytes)))
				chain := &imagechain.Evidence{SourceManifest: index, SelectedManifest: manifestBytes, Config: config}
				source, child := imagechain.Digest(index), imagechain.Digest(manifestBytes)
				mp := &fakeManifestPuller{appRef: "registry.example/team/app@" + child, appManifest: manifest, appConfig: oci.Config{Entrypoint: []string{"/app/server"}, DiffIDs: diffs}, baseConfig: oci.Config{DiffIDs: []string{baseDiff}}, layerBlobs: map[string][]byte{imagechain.Digest(config): config, imagechain.Digest(layer): layer, imagechain.Digest(baseLayer): baseLayer}}
				if kind == "app-layer" {
					mp.layerBlobs[imagechain.Digest(secondLayer)] = secondLayer
				}
				if kind == "full-rootfs" {
					mp.baseConfig.DiffIDs = []string{baseDiff, baseDiff}
				}
				baseConfig := []byte(`{"rootfs":{"type":"layers","diff_ids":["` + baseDiff + `"]}}`)
				if kind == "full-rootfs" {
					baseConfig = []byte(`{"rootfs":{"type":"layers","diff_ids":["` + baseDiff + `","` + baseDiff + `"]}}`)
				}
				mp.baseManifest = oci.Manifest{Config: oci.Descriptor{Digest: imagechain.Digest(baseConfig), Size: int64(len(baseConfig))}}
				mp.layerBlobs[imagechain.Digest(baseConfig)] = baseConfig
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
				payload := []byte(fmt.Sprintf(`{"critical":{"identity":{"docker-reference":"registry.example/team/app"},"image":{"docker-manifest-digest":%q},"type":"cosign container image signature"}}`, source))
				hash := sha256.Sum256(payload)
				signature, err := ecdsa.SignASN1(rand.Reader, key, hash[:])
				if err != nil {
					t.Fatal(err)
				}
				p := &resolvingTestPuller{fakeManifestPuller: mp, resolution: oci.ImageResolution{SourceReference: "registry.example/team/app@" + source, SourceDigest: source, Reference: mp.appRef, Digest: child, Evidence: chain}, attachments: []oci.ImageSignatureAttachment{{ManifestDigest: imagechain.Digest([]byte("attachment metadata")), PayloadDigest: imagechain.Digest(payload), Payload: payload, Signature: signature}}}
				guest := filepath.Join(t.TempDir(), "guest-init")
				if err := os.WriteFile(guest, []byte("INIT"), 0o755); err != nil {
					t.Fatal(err)
				}
				runner := &recordingRunner{}
				var builder LayerBuilder = rootfs.NewBuilder(runner)
				if mode == "consumer omitted" {
					builder = th.bld
				}
				h := New(th.store, th.notif, p, builder, guest, th.appsR, silentLogger())
				if kind != "sidecar-layer" {
					// Produce the shared base through the real builder and actual layer
					// streams. The injected mkfs runner is portable byte evidence only.
					baseH, _, baseP, _, _ := verifiedBaseFixture(t, "complete")
					baseCfg, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{baseDiff}}})
					baseManifest := oci.Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json", Config: oci.Descriptor{Digest: imagechain.Digest(baseCfg), Size: int64(len(baseCfg))}, Layers: []oci.Descriptor{{Digest: imagechain.Digest(baseLayer), Size: int64(len(baseLayer))}}}
					baseRaw, _ := json.Marshal(baseManifest)
					baseDigest := imagechain.Digest(baseRaw)
					baseRef := "registry.example/base@" + baseDigest
					h.WithDeployBaseRef(baseRef)
					mp.baseManifest, mp.baseConfig = baseManifest, oci.Config{DiffIDs: []string{baseDiff}}
					mp.layerBlobs[imagechain.Digest(baseCfg)] = baseCfg
					baseP.resolution = oci.ImageResolution{SourceReference: baseRef, Reference: baseRef, SourceDigest: baseDigest, Digest: baseDigest, Evidence: &imagechain.Evidence{SourceManifest: baseRaw, Config: baseCfg}}
					baseP.layers = map[string][]byte{imagechain.Digest(baseLayer): baseLayer}
					baseH.store, baseH.guestInitPath = th.store, guest
					be, getErr := h.storageFor()
					if getErr != nil {
						t.Fatal(getErr)
					}
					baseH.WithStorage(be)
					if _, baseErr := baseH.EnsureBaseExt4(t.Context(), baseRef, sched.BaseKeyForArch(th.app.Runtime, oci.ImageArchitecture), "base/test.digest", "", "", ""); baseErr != nil {
						t.Fatal(baseErr)
					}
				}
				h.trustedPublishersCacheOK = true
				h.trustedPublishersCache = map[string][]cosign.TrustedPublisher{th.app.ID: {{Name: "company", PublicKey: &key.PublicKey}}}
				if kind == "sidecar-layer" {
					_, err = h.buildSidecarLayers(t.Context(), th.app, th.dep, th.acct)
				} else {
					err = h.buildImageLayer(t.Context(), th.app, th.dep, th.acct)
				}
				workload := ""
				if kind == "sidecar-layer" {
					workload = "metrics"
				}
				root, getErr := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, workload)
				if mode != "complete" {
					if err == nil || !errors.Is(getErr, state.ErrNotFound) || len(runner.argv) != 0 {
						t.Fatalf("unverified conversion published: %v root=%+v %v mkfs=%v", err, root, getErr, runner.argv)
					}
					return
				}
				start, count := 0, 1
				if kind == "app-layer" {
					start, count = 1, 2
				}
				if err != nil || getErr != nil || root.Input.Kind != kind || root.Input.LayerStart != start || len(root.Input.Layers) != count {
					t.Fatalf("real conversion lost lineage: %v %+v %v", err, root, getErr)
				}
				for i, consumed := range root.Input.Layers {
					expected := descriptors[start+i]
					if consumed.Index != start+i || consumed.Digest != expected.Digest || consumed.CompressedBytes != expected.Size || consumed.DiffID != diffs[start+i] {
						t.Fatalf("consumed wrong descriptor position: %+v", consumed)
					}
				}
			})
		}
	}
}
