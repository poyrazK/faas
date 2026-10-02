package imaged

// adr: 430

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSignedMainConversionRetainsExactRegistryRootfs(t *testing.T) {
	for _, kind := range []string{"app-layer", "full-rootfs"} {
		t.Run(kind, func(t *testing.T) {
			th := newTestHarness(t, state.DeploymentKindImage, api.PlanPro, "")
			th.app.RequireSigned = true
			th.app.Runtime = "node22"
			th.dep.ImageDigest = "registry.example/team/app:latest"
			th.dep.FullRootfsAllowAuto = true
			th.createReplacementDeployment(t)
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
			source, child := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
			payload := []byte(fmt.Sprintf(`{"critical":{"identity":{"docker-reference":"registry.example/team/app"},"image":{"docker-manifest-digest":%q},"type":"cosign container image signature"}}`, source))
			sum := sha256.Sum256(payload)
			sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
			if err != nil {
				t.Fatal(err)
			}
			baseDiff, appDiff := "sha256:"+strings.Repeat("0", 64), "sha256:"+strings.Repeat("1", 64)
			config, baseConfig, layer := "sha256:"+strings.Repeat("2", 64), "sha256:"+strings.Repeat("3", 64), "sha256:"+strings.Repeat("4", 64)
			mp := &fakeManifestPuller{appRef: "registry.example/team/app@" + child,
				appManifest:  oci.Manifest{Config: oci.Descriptor{Digest: config}, Layers: []oci.Descriptor{{Digest: baseDiff}, {Digest: layer}}},
				appConfig:    oci.Config{Entrypoint: []string{"/app/server"}, DiffIDs: []string{baseDiff, appDiff}},
				baseManifest: oci.Manifest{Config: oci.Descriptor{Digest: baseConfig}}, baseConfig: oci.Config{DiffIDs: []string{baseDiff}}}
			if kind == "full-rootfs" {
				mp.appManifest.Layers = mp.appManifest.Layers[1:]
				mp.appConfig.DiffIDs = []string{appDiff}
				mp.baseConfig.DiffIDs = []string{baseDiff, baseDiff}
			}
			mp.putConfig(config, mp.appConfig)
			mp.putConfig(baseConfig, mp.baseConfig)
			mp.layerBlobs[layer] = gzTar(t, map[string]string{"app/server": "#!/bin/sh\n"})
			p := &resolvingTestPuller{fakeManifestPuller: mp, resolution: oci.ImageResolution{SourceReference: "registry.example/team/app@" + source, SourceDigest: source, Reference: mp.appRef, Digest: child},
				attachments: []oci.ImageSignatureAttachment{{ManifestDigest: "sha256:" + strings.Repeat("c", 64), PayloadDigest: fmt.Sprintf("sha256:%x", sum), Payload: payload, Signature: sig}}}
			h := New(th.store, th.notif, p, th.bld, "./init", th.appsR, silentLogger())
			h.trustedPublishersCacheOK = true
			h.trustedPublishersCache = map[string][]cosign.TrustedPublisher{th.app.ID: {{Name: "company", PublicKey: &key.PublicKey}}}
			if err := h.buildImageLayer(t.Context(), th.app, th.dep, th.acct); err != nil {
				t.Fatal(err)
			}
			verification, err := th.store.GetLatestDeploymentRegistryVerification(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
			body := "fake ext4"
			if kind == "full-rootfs" {
				body = "fake ext4 full-rootfs"
			}
			if err != nil || root.Input.Kind != kind || root.Input.RegistryVerificationID != verification.ID || root.Input.RegistryInputHash != verification.InputHash || root.Input.ArtifactDigest != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) || root.Input.ArtifactBytes != int64(len(body)) {
				t.Fatalf("main lost exact conversion identity: %+v %v", root, err)
			}
			dep, err := th.store.DeploymentByID(t.Context(), th.dep.ID)
			if err != nil || dep.ImageDigest != th.dep.ImageDigest || dep.RootfsKey != root.Input.StorageKey {
				t.Fatalf("rootfs publication changed customer intent: %+v %v", dep, err)
			}
		})
	}
}
