package cosign

// adr: 393

// ADR-393: real keyed signatures authenticate exact payload bytes and the
// resolved immutable subject. These are cryptographic checks, not native KVM
// acceptance or a durable proof of the converted rootfs.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type attachedTestPuller struct {
	digest               string
	attachments          []ImageSignatureAttachment
	resolveErr, fetchErr error
	fetched              bool
	onFetch              func()
}

func (p *attachedTestPuller) ResolveDigest(context.Context, string) (string, error) {
	return p.digest, p.resolveErr
}
func (p *attachedTestPuller) FetchSignatureAttachments(context.Context, string, string) ([]ImageSignatureAttachment, error) {
	p.fetched = true
	if p.onFetch != nil {
		p.onFetch()
	}
	return p.attachments, p.fetchErr
}

func attachedTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func attachedTestPayload(digest string) []byte {
	return []byte(fmt.Sprintf(`{"critical":{"identity":{"docker-reference":"registry.example/team/service"},"image":{"docker-manifest-digest":%q},"type":"cosign container image signature"},"optional":{"build":"123"}}`, digest))
}
func attachedTestSign(t *testing.T, key *ecdsa.PrivateKey, payload []byte) ImageSignatureAttachment {
	t.Helper()
	sum := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return ImageSignatureAttachment{ManifestDigest: "sha256:" + strings.Repeat("c", 64), PayloadDigest: fmt.Sprintf("sha256:%x", sum), Payload: payload, Signature: sig}
}

func TestAttachedImageProofAuthenticatesContentAndKey(t *testing.T) {
	key := attachedTestKey(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	a := attachedTestSign(t, key, attachedTestPayload(digest))
	p := &attachedTestPuller{digest: digest, attachments: []ImageSignatureAttachment{a}}
	proof, err := VerifyImageSignatureAttachments(context.Background(), p, "registry.example/team/service:latest", []TrustedPublisher{{Name: "company", PublicKey: &key.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if proof.SubjectDigest != digest || proof.PublisherName != "company" || proof.PublisherKeySHA256 != fmt.Sprintf("%x", sha256.Sum256(der)) || proof.AttachmentManifestDigest != a.ManifestDigest || proof.PayloadDigest != a.PayloadDigest || proof.SignatureDigest != fmt.Sprintf("sha256:%x", sha256.Sum256(a.Signature)) {
		t.Fatalf("unbound proof: %+v", proof)
	}
	// Repository naming is not an implicit publisher restriction. An approved
	// content digest copied to a different repository remains the same content.
	if _, err := VerifyImageSignatureAttachments(context.Background(), p, "other.example/copied@"+digest, []TrustedPublisher{{Name: "renamed", PublicKey: &key.PublicKey}}); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedImageSignatureProofRequiresCurrentKeyAndExactBytes(t *testing.T) {
	key := attachedTestKey(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	attachment := attachedTestSign(t, key, attachedTestPayload(digest))
	proof, err := VerifyImageSignatureAttachments(t.Context(), &attachedTestPuller{digest: digest, attachments: []ImageSignatureAttachment{attachment}},
		"registry.example/team/service@"+digest, []TrustedPublisher{{Name: "company", PublicKey: &key.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	// The producer cannot alter evidence after verification by mutating its
	// attachment buffers. Retained bytes own their storage.
	attachment.Payload[0] ^= 1
	attachment.Signature[0] ^= 1
	if err := ReverifyImageSignatureProof(proof, der); err != nil {
		t.Fatalf("producer aliased retained bytes: %v", err)
	}
	other, err := x509.MarshalPKIXPublicKey(&attachedTestKey(t).PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"rotated key", "missing bytes", "payload", "signature", "fingerprint", "signature digest", "subject", "payload digest"} {
		t.Run(field, func(t *testing.T) {
			candidate := proof
			candidate.Evidence = &ImageSignatureEvidence{Payload: append([]byte(nil), proof.Evidence.Payload...), Signature: append([]byte(nil), proof.Evidence.Signature...)}
			currentKey := der
			switch field {
			case "rotated key":
				currentKey = other
			case "missing bytes":
				candidate.Evidence = nil
			case "payload":
				candidate.Evidence.Payload[0] ^= 1
			case "signature":
				candidate.Evidence.Signature[0] ^= 1
			case "fingerprint":
				candidate.PublisherKeySHA256 = strings.Repeat("f", 64)
			case "signature digest":
				candidate.SignatureDigest = "sha256:" + strings.Repeat("f", 64)
			case "subject":
				candidate.SubjectDigest = "sha256:" + strings.Repeat("f", 64)
			case "payload digest":
				candidate.PayloadDigest = "sha256:" + strings.Repeat("f", 64)
			}
			if !errors.Is(ReverifyImageSignatureProof(candidate, currentKey), ErrSignatureInvalid) {
				t.Fatal("accepted substituted evidence or stale key")
			}
		})
	}
}

func TestAttachedImageRejectsUntrustedOrChangedEvidence(t *testing.T) {
	key, other := attachedTestKey(t), attachedTestKey(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name       string
		mutate     func(*ImageSignatureAttachment)
		publishers []TrustedPublisher
	}{
		{"wrong key", nil, []TrustedPublisher{{Name: "other", PublicKey: &other.PublicKey}}},
		{"unnamed key", nil, []TrustedPublisher{{PublicKey: &key.PublicKey}}},
		{"nil key", nil, []TrustedPublisher{{Name: "company"}}},
		{"off curve key", nil, []TrustedPublisher{{Name: "company", PublicKey: &ecdsa.PublicKey{Curve: elliptic.P256(), X: big.NewInt(1), Y: big.NewInt(1)}}}},
		{"payload tampered", func(a *ImageSignatureAttachment) { a.Payload = append(a.Payload, ' ') }, nil},
		{"payload descriptor changed", func(a *ImageSignatureAttachment) { a.PayloadDigest = "sha256:" + strings.Repeat("b", 64) }, nil},
		{"manifest digest noncanonical", func(a *ImageSignatureAttachment) { a.ManifestDigest = "sha256:ABC" }, nil},
		{"invalid DER", func(a *ImageSignatureAttachment) { a.Signature = []byte{0x30, 0x01, 0x00} }, nil},
		{"raw r s", func(a *ImageSignatureAttachment) {
			sum := sha256.Sum256(a.Payload)
			r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
			if err != nil {
				t.Fatal(err)
			}
			a.Signature = make([]byte, 64)
			r.FillBytes(a.Signature[:32])
			s.FillBytes(a.Signature[32:])
		}, nil},
		{"double hashed", func(a *ImageSignatureAttachment) {
			sum := sha256.Sum256(a.Payload)
			twice := sha256.Sum256(sum[:])
			sig, err := ecdsa.SignASN1(rand.Reader, key, twice[:])
			if err != nil {
				t.Fatal(err)
			}
			a.Signature = sig
		}, nil},
		{"payload over limit", func(a *ImageSignatureAttachment) { a.Payload = make([]byte, api.ImageSignatureMaxPayloadBytes+1) }, nil},
		{"signature over limit", func(a *ImageSignatureAttachment) { a.Signature = make([]byte, api.ImageSignatureMaxDERBytes+1) }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := attachedTestSign(t, key, attachedTestPayload(digest))
			if tc.mutate != nil {
				tc.mutate(&a)
			}
			pubs := tc.publishers
			if pubs == nil {
				pubs = []TrustedPublisher{{Name: "company", PublicKey: &key.PublicKey}}
			}
			proof, err := VerifyImageSignatureAttachments(context.Background(), &attachedTestPuller{digest: digest, attachments: []ImageSignatureAttachment{a}}, "registry.example/team/service", pubs)
			if !errors.Is(err, ErrSignatureInvalid) || proof != (ImageSignatureProof{}) {
				t.Fatalf("accepted invalid proof: %+v %v", proof, err)
			}
		})
	}
}

func TestAttachedImageRejectsSignedAmbiguousOrWrongClaims(t *testing.T) {
	key := attachedTestKey(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	good := string(attachedTestPayload(digest))
	for name, payload := range map[string]string{
		"different digest":   string(attachedTestPayload("sha256:" + strings.Repeat("b", 64))),
		"wrong type":         strings.Replace(good, "cosign container image signature", "unrelated signature", 1),
		"unknown critical":   strings.Replace(good, `"type":`, `"allow":true,"type":`, 1),
		"wrong case":         strings.Replace(good, `"image":`, `"Image":`, 1),
		"duplicate digest":   strings.Replace(good, `"docker-manifest-digest":`, `"docker-manifest-digest":"wrong","docker-manifest-digest":`, 1),
		"escaped duplicate":  strings.Replace(good, `"docker-manifest-digest":`, `"docker-manifest-di\u0067est":"wrong","docker-manifest-digest":`, 1),
		"duplicate critical": strings.Replace(good, `{"critical":`, `{"critical":{},"critical":`, 1),
		"extra JSON":         good + ` {}`,
		"optional scalar":    strings.Replace(good, `{"build":"123"}`, `"irrelevant"`, 1),
		"identity absent":    strings.Replace(good, `"docker-reference":"registry.example/team/service"`, `"docker-reference":""`, 1),
		"too deep":           strings.Replace(good, `"123"`, strings.Repeat("[", api.ImageSignatureMaxJSONDepth)+"0"+strings.Repeat("]", api.ImageSignatureMaxJSONDepth), 1),
	} {
		t.Run(name, func(t *testing.T) {
			a := attachedTestSign(t, key, []byte(payload))
			_, err := VerifyImageSignatureAttachments(context.Background(), &attachedTestPuller{digest: digest, attachments: []ImageSignatureAttachment{a}}, "registry.example/team/service", []TrustedPublisher{{Name: "company", PublicKey: &key.PublicKey}})
			if !errors.Is(err, ErrSignatureInvalid) {
				t.Fatalf("signed unsupported claim: %v", err)
			}
		})
	}
}

func TestAttachedImageCandidatesAndCancellation(t *testing.T) {
	key := attachedTestKey(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	good := attachedTestSign(t, key, attachedTestPayload(digest))
	bad := good
	bad.Signature = []byte("not a signature")
	pubs := []TrustedPublisher{{Name: "broken"}, {Name: "company", PublicKey: &key.PublicKey}}
	p := &attachedTestPuller{digest: digest, attachments: []ImageSignatureAttachment{bad, good}}
	if _, err := VerifyImageSignatureAttachments(context.Background(), p, "registry.example/team/service", pubs); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.onFetch = cancel
	if proof, err := VerifyImageSignatureAttachments(ctx, p, "registry.example/team/service", pubs); !errors.Is(err, context.Canceled) || proof != (ImageSignatureProof{}) {
		t.Fatalf("cancelled proof: %+v %v", proof, err)
	}
	for _, tc := range []struct {
		name string
		p    *attachedTestPuller
		want error
	}{
		{"resolve network", &attachedTestPuller{resolveErr: context.DeadlineExceeded}, context.DeadlineExceeded},
		{"fetch network", &attachedTestPuller{digest: digest, fetchErr: context.DeadlineExceeded}, context.DeadlineExceeded},
		{"missing", &attachedTestPuller{digest: digest}, ErrSignatureMissing},
		{"invalid subject", &attachedTestPuller{digest: "sha256:ABC"}, ErrSignatureInvalid},
		{"too many", &attachedTestPuller{digest: digest, attachments: make([]ImageSignatureAttachment, api.ImageSignatureMaxEntries+1)}, ErrSignatureInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyImageSignatureAttachments(context.Background(), tc.p, "registry.example/team/service", pubs)
			if !errors.Is(err, tc.want) {
				t.Fatalf("%v", err)
			}
			if tc.name == "invalid subject" && tc.p.fetched {
				t.Fatal("fetched for noncanonical subject")
			}
		})
	}
}
