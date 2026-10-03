package imaged

// adr: 435

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
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type signedSidecarPuller struct {
	*resolvingTestPuller
	layerRefs []string
	layer     []byte
}

func (p *signedSidecarPuller) PullLayers(_ context.Context, ref string) (oci.PullLayersResult, error) {
	p.layerRefs = append(p.layerRefs, ref)
	if ref != p.resolution.Reference {
		return oci.PullLayersResult{}, errors.New("sidecar reread customer tag or signed index")
	}
	return oci.PullLayersResult{Digest: p.resolution.Digest, Config: oci.ImageConfig{Cmd: []string{"/metrics"}}, Layers: []io.ReadCloser{io.NopCloser(bytes.NewReader(p.layer))}}, nil
}

func TestSignedSidecarUsesSelectedChildAndCurrentStoredPublisher(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	source := "sha256:" + strings.Repeat("a", 64)
	child := "sha256:" + strings.Repeat("b", 64)
	ref := "registry.example/team/metrics:latest"
	for _, mode := range []string{"approved index", "missing signature", "signed child only", "removed publisher", "rotated publisher", "artifact replaced during build", "publisher revoked during build"} {
		t.Run(mode, func(t *testing.T) {
			th := newTestHarness(t, state.DeploymentKindImage, api.PlanPro, "")
			th.app.RequireSigned = true
			th.dep.Sidecars = []byte(`[{"name":"metrics","image":"registry.example/team/metrics:latest","type":"sidecar","port":9090}]`)
			th.createReplacementDeployment(t)
			if mode != "removed publisher" {
				currentDER := der
				if mode == "rotated publisher" {
					other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
					if err != nil {
						t.Fatal(err)
					}
					currentDER, err = x509.MarshalPKIXPublicKey(&other.PublicKey)
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, _, err := th.store.UpsertAppTrustedSigner(t.Context(), th.app.AccountID, th.app.ID, "company", currentDER, th.app.AccountID); err != nil {
					t.Fatal(err)
				}
			}
			signedDigest := source
			if mode == "signed child only" {
				signedDigest = child
			}
			payload, err := json.Marshal(map[string]any{"critical": map[string]any{"identity": map[string]string{"docker-reference": "registry.example/team/metrics"}, "image": map[string]string{"docker-manifest-digest": signedDigest}, "type": "cosign container image signature"}})
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(payload)
			sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
			if err != nil {
				t.Fatal(err)
			}
			p := &signedSidecarPuller{resolvingTestPuller: &resolvingTestPuller{fakeManifestPuller: &fakeManifestPuller{}, resolution: oci.ImageResolution{
				SourceReference: "registry.example/team/metrics@" + source, SourceDigest: source, Reference: "registry.example/team/metrics@" + child, Digest: child,
			}, attachments: []oci.ImageSignatureAttachment{{ManifestDigest: "sha256:" + strings.Repeat("c", 64), PayloadDigest: fmt.Sprintf("sha256:%x", sum), Payload: payload, Signature: sig}}}, layer: gzTar(t, map[string]string{"metrics": "#!/bin/sh\n"})}
			if mode == "missing signature" {
				p.attachments = nil
			}
			h := New(th.store, th.notif, p, th.bld, "./init", th.appsR, silentLogger())
			h.trustedPublishersCacheOK = true
			h.trustedPublishersCache = map[string][]cosign.TrustedPublisher{th.app.ID: {{Name: "company", PublicKey: &key.PublicKey}}}
			if mode == "artifact replaced during build" {
				th.bld.buildHook = func() {
					be, e := h.storageFor()
					if e != nil {
						t.Fatal(e)
					}
					// Same-sized replacement defeats a length-only check.
					if e := be.Put(t.Context(), sched.AppSidecarLayerKey(th.app.Slug, th.dep.ID, "metrics"), strings.NewReader("evil ext4")); e != nil {
						t.Fatal(e)
					}
				}
			}
			if mode == "publisher revoked during build" {
				th.bld.buildHook = func() {
					if e := th.store.DeleteAppTrustedSigner(t.Context(), th.app.AccountID, th.app.ID, "company"); e != nil {
						t.Fatal(e)
					}
				}
			}
			_, err = h.buildSidecarLayers(t.Context(), th.app, th.dep, th.acct)
			if p.input != ref {
				t.Fatalf("sidecar resolution used %q", p.input)
			}
			if mode != "approved index" {
				if strings.HasSuffix(mode, "during build") {
					if err == nil || len(p.layerRefs) != 1 || len(th.bld.calls) != 1 {
						t.Fatalf("post-conversion refusal was bypassed: %v", err)
					}
					if layers, e := th.store.ListDeploymentSidecarLayers(t.Context(), th.dep.ID); e != nil || len(layers) != 0 {
						t.Fatalf("refused artifact published metadata: %+v %v", layers, e)
					}
					if _, e := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "metrics"); !errors.Is(e, state.ErrNotFound) {
						t.Fatalf("refused artifact published producer: %v", e)
					}
					if mode == "publisher revoked during build" && findNotify(th.notif, "audit_event") == nil {
						t.Fatal("publisher refusal lacked audit")
					}
					return
				}
				if err == nil || len(p.layerRefs) != 0 || len(th.bld.calls) != 0 {
					t.Fatalf("unapproved sidecar reached conversion: %v refs=%v builds=%d", err, p.layerRefs, len(th.bld.calls))
				}
				if _, e := th.store.GetLatestDeploymentRegistryVerification(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "metrics"); !errors.Is(e, state.ErrNotFound) {
					t.Fatalf("rejected sidecar left proof: %v", e)
				}
				if mode == "rotated publisher" || mode == "removed publisher" {
					if event := findNotify(th.notif, "audit_event"); event == nil || !strings.Contains(event.payload, "app.signature_invalid") {
						t.Fatal("current stored publisher refusal lacked its signature audit")
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(p.layerRefs) != 1 || p.layerRefs[0] != p.resolution.Reference {
				t.Fatalf("layers used %v", p.layerRefs)
			}
			record, err := th.store.GetLatestDeploymentRegistryVerification(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "metrics")
			if err != nil {
				t.Fatal(err)
			}
			if record.Input.SourceReference != p.resolution.SourceReference || record.Input.SelectedReference != p.resolution.Reference {
				t.Fatalf("stored different source/child: %+v", record)
			}
			root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "metrics")
			if err != nil || root.Input.RegistryVerificationID != record.ID || root.Input.RegistryInputHash != record.InputHash ||
				root.Input.ArtifactBytes != int64(len("fake ext4")) || root.Input.ArtifactDigest != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("fake ext4"))) {
				t.Fatalf("sidecar conversion lost exact verification/produced identity: %+v %v", root, err)
			}
			layers, err := th.store.ListDeploymentSidecarLayers(t.Context(), th.dep.ID)
			if err != nil || len(layers) != 1 || layers[0].ContentDigest != p.resolution.Reference {
				t.Fatalf("sidecar metadata lost immutable child: %+v %v", layers, err)
			}
		})
	}
}
