package buildpublisher

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func publisherFixture(t *testing.T) (Claims, *ecdsa.PrivateKey, []byte, Proof) {
	t.Helper()
	c := Claims{Format: Format, AccountID: uuid.NewString(), OrgID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), BuildID: uuid.NewString(), ClaimStartedAt: time.Now().UTC().Format(time.RFC3339Nano), SourceSHA256: strings.Repeat("a", 64), ExportDigest: "sha256:" + strings.Repeat("b", 64), ExportBytes: 42, Runtime: "node22", BuilderNodeID: "builder-one"}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Sign(t.Context(), c, "company", key)
	if err != nil {
		t.Fatal(err)
	}
	return c, key, der, p
}

func TestBuildPublisherClaimAndDomainBinding(t *testing.T) {
	c, _, der, p := publisherFixture(t)
	if err := Verify(c, p, der); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*Claims){"source": func(c *Claims) { c.SourceSHA256 = strings.Repeat("c", 64) }, "export": func(c *Claims) { c.ExportDigest = "sha256:" + strings.Repeat("c", 64) }, "scope": func(c *Claims) { c.AccountID = uuid.NewString() }, "claim": func(c *Claims) { c.ClaimStartedAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano) }, "build": func(c *Claims) { c.BuildID = uuid.NewString() }, "runtime": func(c *Claims) { c.Runtime = "python312" }, "node": func(c *Claims) { c.BuilderNodeID = "other" }, "bytes": func(c *Claims) { c.ExportBytes++ }}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			altered := c
			change(&altered)
			if Verify(altered, p, der) == nil {
				t.Fatal("claim substitution approved")
			}
		})
	}
	_, _, otherDER, _ := publisherFixture(t)
	if Verify(c, p, otherDER) == nil {
		t.Fatal("unapproved key accepted")
	}
	registry := imagepublisher.ImageSignatureProof{SubjectDigest: c.ExportDigest, PublisherName: p.PublisherName, PublisherKeySHA256: p.PublisherKeySHA256, AttachmentManifestDigest: c.ExportDigest, PayloadDigest: p.PayloadDigest, SignatureDigest: p.SignatureDigest, Evidence: &imagepublisher.ImageSignatureEvidence{Payload: p.Payload, Signature: p.Signature}}
	if imagepublisher.ReverifyImageSignatureProof(registry, der) == nil {
		t.Fatal("build attestation became registry approval")
	}
}

func TestBuildPublisherCanonicalPayloadAndCopy(t *testing.T) {
	c, _, der, p := publisherFixture(t)
	for _, body := range [][]byte{append([]byte(" "), p.Payload...), append(p.Payload, byte('\n')), []byte(`{"critical":{"type":"cosign container image signature"}}`)} {
		bad := p.Clone()
		bad.Payload = body
		bad.PayloadDigest = digest(body)
		if Verify(c, bad, der) == nil {
			t.Fatal("noncanonical or other-domain payload accepted")
		}
	}
	copy := p.Clone()
	copy.Signature[0] ^= 1
	if Verify(c, copy, der) == nil || Verify(c, p, der) != nil {
		t.Fatal("signature alias or corruption")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, key, _, _ := publisherFixture(t)
	if _, err := Sign(ctx, c, "company", key); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, bad := range []Claims{{}, func() Claims { x := c; x.ExportBytes = 0; return x }(), func() Claims { x := c; x.SourceSHA256 = strings.Repeat("A", 64); return x }(), func() Claims { x := c; x.AppID = strings.ReplaceAll(x.AppID, "-", ""); return x }()} {
		if _, err := Payload(bad); err == nil {
			t.Fatal("invalid claim accepted")
		}
	}
}

func TestBuildPublisherPrivateFileAndCompleteExport(t *testing.T) {
	c, key, der, _ := publisherFixture(t)
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "publisher.key")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := NewFileSigner("company", path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := signer.Sign(t.Context(), c)
	if err != nil || Verify(c, p, der) != nil {
		t.Fatal("file publisher failed", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileSigner("company", path); err == nil {
		t.Fatal("readable private key accepted")
	}
	export := filepath.Join(t.TempDir(), "image.tar")
	body := []byte("complete archive including padding")
	if err := os.WriteFile(export, body, 0600); err != nil {
		t.Fatal(err)
	}
	d, n, err := MeasureExport(t.Context(), export)
	if err != nil || d != digest(body) || n != int64(len(body)) {
		t.Fatal("incomplete export measure", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := MeasureExport(ctx, export); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := MeasureExport(t.Context(), filepath.Dir(export)); err == nil {
		t.Fatal("directory export accepted")
	}
}
