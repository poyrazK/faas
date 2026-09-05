package imaged

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

type resolvingTestPuller struct {
	*fakeManifestPuller
	resolution    oci.ImageResolution
	resolveErr    error
	input         string
	auth          *oci.BasicAuth
	signature     []byte
	signatureRefs []string
	configRefs    []string
	manifestRefs  []string
}

func (p *resolvingTestPuller) ResolveImage(_ context.Context, ref string, auth *oci.BasicAuth) (oci.ImageResolution, error) {
	p.input, p.auth = ref, auth
	return p.resolution, p.resolveErr
}
func (p *resolvingTestPuller) PullDigest(_ context.Context, ref string) (string, error) {
	p.signatureRefs = append(p.signatureRefs, ref)
	if ref != p.resolution.SourceReference {
		return "", errors.New("signature lookup used mutable tag or child")
	}
	return p.resolution.SourceDigest, nil
}
func (p *resolvingTestPuller) PullBlob(ctx context.Context, repo, digest string) (io.ReadCloser, error) {
	if digest == p.resolution.SourceDigest {
		return io.NopCloser(bytes.NewReader(p.signature)), nil
	}
	return p.fakeManifestPuller.PullBlob(ctx, repo, digest)
}
func (p *resolvingTestPuller) PullImageConfig(ctx context.Context, ref string) (oci.ImageConfig, error) {
	p.configRefs = append(p.configRefs, ref)
	if ref != p.resolution.Reference {
		return oci.ImageConfig{}, errors.New("config lookup did not use selected child")
	}
	return p.fakeManifestPuller.PullImageConfig(ctx, ref)
}
func (p *resolvingTestPuller) PullManifestWithAuth(ctx context.Context, ref string, auth *oci.BasicAuth) (oci.Manifest, error) {
	p.manifestRefs = append(p.manifestRefs, ref)
	if ref == p.input {
		return oci.Manifest{}, errors.New("manifest lookup reread mutable tag")
	}
	return p.fakeManifestPuller.PullManifestWithAuth(ctx, ref, auth)
}

func TestPrepareContainerImageSignatureBindsSource(t *testing.T) {
	source := "sha256:" + strings.Repeat("a", 64)
	child := "sha256:" + strings.Repeat("b", 64)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"signed index", "signed child only", "resolution failure"} {
		t.Run(mode, func(t *testing.T) {
			th := newTestHarness(t, state.DeploymentKindImage, "pro", "")
			th.app.RequireSigned = true
			th.dep.ImageDigest = "example.com/org/service:latest"
			p := &resolvingTestPuller{fakeManifestPuller: &fakeManifestPuller{}, resolution: oci.ImageResolution{
				SourceDigest: source, SourceReference: "example.com/org/service@" + source,
				Digest: child, Reference: "example.com/org/service@" + child}}
			signedDigest := source
			if mode == "signed child only" {
				signedDigest = child
			}
			raw, _ := hex.DecodeString(strings.TrimPrefix(signedDigest, "sha256:"))
			r, s, err := ecdsa.Sign(rand.Reader, key, raw)
			if err != nil {
				t.Fatal(err)
			}
			p.signature = make([]byte, 64)
			r.FillBytes(p.signature[:32])
			s.FillBytes(p.signature[32:])
			if mode == "resolution failure" {
				p.resolveErr = &oci.PlatformSelectionError{Reason: "no compatible image"}
			}
			h := New(th.store, th.notif, p, th.bld, "./init", th.appsR, silentLogger())
			h.trustedPublishersCacheOK = true
			h.trustedPublishersCache = map[string][]cosign.TrustedPublisher{th.app.ID: {{Name: "publisher", PublicKey: &key.PublicKey}}}
			auth := &oci.BasicAuth{Username: "user", Password: "secret"}
			ref, digest, err := h.prepareContainerImage(context.Background(), th.app, th.dep, auth)
			if p.input != th.dep.ImageDigest || p.auth != auth {
				t.Fatal("resolution lost original reference or credentials")
			}
			if mode == "signed index" {
				if err != nil || ref != p.resolution.Reference || digest != child {
					t.Fatalf("resolution: %q %q %v", ref, digest, err)
				}
			} else {
				if err == nil || ref != "" || digest != "" {
					t.Fatalf("accepted invalid source: %q %q %v", ref, digest, err)
				}
				dep, _ := th.store.DeploymentByID(context.Background(), th.dep.ID)
				if dep.Status != state.DeployFailed {
					t.Fatalf("failure not persisted: %+v", dep)
				}
			}
			if mode == "resolution failure" && len(p.signatureRefs) != 0 {
				t.Fatal("signature read after resolution failure")
			}
			if mode != "resolution failure" && (len(p.signatureRefs) != 1 || p.signatureRefs[0] != p.resolution.SourceReference) {
				t.Fatalf("wrong signature subject: %v", p.signatureRefs)
			}
			if len(p.configRefs) != 0 || len(p.manifestRefs) != 0 {
				t.Fatal("build reads started before signature gate")
			}
		})
	}
}
